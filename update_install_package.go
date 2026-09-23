package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type updatePlan struct {
	Mode    string `json:"mode"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, io.LimitReader(file, maxPortableArchiveSize+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func copyUpdateFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, maxPortableArchiveSize+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func validateInnoInstaller(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPortableArchiveSize {
		return errors.New("安装更新包文件无效或过大")
	}
	if err = validatePEFile(path); err != nil {
		return fmt.Errorf("安装更新包不是有效的 Windows 程序：%w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("Inno Setup Setup Data")) {
		return errors.New("安装更新包不是支持的 Inno Setup 格式，已取消自动执行；请手动安装")
	}
	return nil
}

func readUpdatePlan(stage, release string) (updatePlan, error) {
	var plan updatePlan
	if err := validateUpdateStage(filepath.Dir(stage), stage); err != nil {
		return plan, err
	}
	path := filepath.Join(stage, "update-plan.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return plan, errors.New("更新计划文件缺失或无效")
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &plan) != nil || plan.Version != release || (plan.Mode != "installed" && plan.Mode != "portable") {
		return plan, errors.New("更新计划与请求版本不匹配")
	}
	return plan, nil
}

func validateUpdateStage(root, stage string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(stage) || filepath.Dir(stage) != root || !strings.HasPrefix(filepath.Base(stage), ".jianzuo-update-") {
		return errors.New("更新暂存目录超出目标目录")
	}
	info, err := os.Lstat(stage)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("更新暂存目录无效或包含目录联接")
	}
	return nil
}

func innoUpdateArguments(root, dataDir string) []string {
	return []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/UPDATE=1", "/NOLAUNCH", "/DIR=" + root, "/DATADIR=" + dataDir, "/LOG=" + filepath.Join(dataDir, "update-installer.log")}
}

// execute is injected in regression tests; tests must never run a real installer.
func installStagedProgram(dataDir, root, stage, release string, execute func(string, []string, string) error) error {
	if err := validateUpdateStage(root, stage); err != nil {
		return err
	}
	plan, err := readUpdatePlan(stage, release)
	if err != nil {
		return err
	}
	if plan.Mode != "installed" || !innoReleaseSupported(release) {
		return errors.New("该安装更新包不支持静默升级协议")
	}
	mode, err := installationMode(root, filepath.Join(root, "duo-service.exe"))
	if err != nil || mode != "installed" {
		return errors.New("安装所有权已变化，取消更新")
	}
	installer := filepath.Join(stage, installerAssetName)
	if err = validateInnoInstaller(installer); err != nil {
		return err
	}
	digest, err := fileSHA256(installer)
	if err != nil || len(plan.Digest) != 64 || !strings.EqualFold(digest, plan.Digest) {
		return errors.New("暂存安装包 SHA-256 校验失败，未执行安装")
	}
	if err = execute(installer, innoUpdateArguments(root, dataDir), root); err != nil {
		return fmt.Errorf("安装器未成功完成：%w；未自动回退注册表或数据库，请查看 update-installer.log 并使用安装包修复，勿删除数据", err)
	}
	return nil
}

func runUpdateInstaller(path string, args []string, directory string) error {
	process, err := startDetachedProcess(path, args, directory)
	if err != nil {
		return err
	}
	state, err := process.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return fmt.Errorf("安装器退出码 %d", state.ExitCode())
	}
	return nil
}

func runInstalledUpdate(dataDir, root, stage, release string, unlock func()) error {
	// The data lock remains held while Inno maintains files, registry and entries.
	if err := installStagedProgram(dataDir, root, stage, release, runUpdateInstaller); err != nil {
		logUpdate(dataDir, "installed update failed: %v", err)
		return err
	}
	unlock()
	launcher, err := launchPortable(root, dataDir)
	if err == nil {
		defer launcher.Release()
		err = waitForPortableRestart(dataDir, release, 45*time.Second)
	}
	if err != nil {
		// A newly started service may have migrated data. Reverting only binaries
		// would not constitute a safe installed-app or database rollback.
		logUpdate(dataDir, "installer succeeded but restart verification failed: %v; program/data not rolled back; repair installation or restore a compatible data backup", err)
		return fmt.Errorf("安装已完成但新版未通过启动检查：%w；未回退程序或数据，请查看 update.log 后修复安装", err)
	}
	_ = os.RemoveAll(stage)
	logUpdate(dataDir, "installed update %s completed", release)
	return nil
}
