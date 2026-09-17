package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	maxPortableArchiveSize   = int64(256 << 20)
	maxPortableExtractedSize = uint64(320 << 20)
	updateHelperFlag         = "--update-helper"
)

var portableArchiveFiles = []string{
	"简作.exe",
	"jianzuo-service.exe",
	"使用说明.md",
	"THIRD-PARTY-NOTICES.txt",
}

var portableArchiveFileSet = map[string]bool{
	"简作.exe":                 true,
	"jianzuo-service.exe":    true,
	"使用说明.md":               true,
	"THIRD-PARTY-NOTICES.txt": true,
}

type UpdateInstallResult struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Message string `json:"message"`
}

func newUpdateDownloadClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("更新包下载重定向次数过多")
			}
			if req.URL.Scheme != "https" || req.URL.User != nil || !updateDownloadHost(req.URL.Hostname()) {
				return errors.New("更新包重定向到非 GitHub 地址")
			}
			return nil
		},
	}
}

func updateDownloadHost(host string) bool {
	switch strings.ToLower(host) {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	default:
		return false
	}
}

func (s *Server) installSupport() (bool, string) {
	if runtime.GOOS != "windows" {
		return false, "自动安装仅支持 Windows 便携版；请使用手动下载。"
	}
	if strings.TrimSpace(s.updateRoot) == "" || s.launcherPID <= 0 {
		return false, "请通过 简作.exe 启动便携版后再自动更新；也可手动下载。"
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(filepath.Base(exe), "jianzuo-service.exe") {
		return false, "当前不是便携版服务进程；请使用手动下载。"
	}
	if _, err = os.Stat(filepath.Join(s.updateRoot, "简作.exe")); err != nil {
		return false, "便携版启动器不存在；请使用手动下载。"
	}
	return true, ""
}

func (s *Server) decorateUpdate(v UpdateInfo) UpdateInfo {
	v.InstallSupported, v.InstallMessage = s.installSupport()
	return v
}

func (s *Server) installUpdate(ctx context.Context, checker *UpdateChecker) (UpdateInstallResult, error) {
	supported, reason := s.installSupport()
	if !supported {
		return UpdateInstallResult{}, errors.New(reason)
	}
	if !s.updateMu.TryLock() {
		return UpdateInstallResult{}, errors.New("已有更新正在准备，请稍候")
	}
	defer s.updateMu.Unlock()

	root := filepath.Clean(s.updateRoot)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	info, err := checker.check(ctx, s.app.store.updateRepository())
	if err != nil {
		return UpdateInstallResult{}, err
	}
	if info.State != "available" {
		if info.Message != "" {
			return UpdateInstallResult{}, errors.New(info.Message)
		}
		return UpdateInstallResult{}, errors.New("当前没有可自动安装的新版本")
	}
	if stableVersionPattern.FindStringSubmatch(info.Latest) == nil {
		return UpdateInstallResult{}, errors.New("更新版本号无效")
	}
	if info.DownloadURL == "" {
		return UpdateInstallResult{}, errors.New("该版本尚未上传便携包，请先查看更新说明")
	}

	archive, err := downloadPortableArchive(ctx, checker, info)
	if err != nil {
		return UpdateInstallResult{}, err
	}
	defer os.Remove(archive)

	stage, err := os.MkdirTemp(root, ".jianzuo-update-"+safeUpdateVersion(info.Latest)+"-")
	if err != nil {
		return UpdateInstallResult{}, fmt.Errorf("无法在程序目录准备更新：%w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = extractPortableArchive(archive, stage); err != nil {
		return UpdateInstallResult{}, err
	}
	if err = validatePortableStaging(stage); err != nil {
		return UpdateInstallResult{}, err
	}
	helper, err := copyUpdateHelper()
	if err != nil {
		return UpdateInstallResult{}, fmt.Errorf("无法准备自动更新助手：%w", err)
	}
	helperStarted := false
	defer func() {
		if !helperStarted {
			_ = os.Remove(helper)
		}
	}()

	dataDir := filepath.Dir(s.app.config.path)
	args := []string{
		updateHelperFlag,
		"--data", dataDir,
		"--update-root", root,
		"--update-stage", stage,
		"--update-version", info.Latest,
		"--launcher-pid", strconv.Itoa(s.launcherPID),
	}
	helperProcess, err := startDetachedProcess(helper, args, dataDir)
	if err != nil {
		return UpdateInstallResult{}, fmt.Errorf("无法启动自动更新助手：%w", err)
	}
	_ = helperProcess.Release()
	helperStarted = true
	keepStage = true
	fmt.Printf("JIANZUO_UPDATE %s\n", info.Latest)
	return UpdateInstallResult{
		State:   "scheduled",
		Version: info.Latest,
		Message: "更新已准备完成，简作将停止任务、替换程序并自动重启。",
	}, nil
}

func safeUpdateVersion(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "release"
	}
	return b.String()
}

func downloadPortableArchive(ctx context.Context, checker *UpdateChecker, info UpdateInfo) (string, error) {
	if info.Size > maxPortableArchiveSize {
		return "", errors.New("更新包超过自动安装允许的大小")
	}
	expected, err := updateArchiveDigest(ctx, checker, info)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", info.DownloadURL, nil)
	if err != nil {
		return "", errors.New("更新包下载地址无效")
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Jianzuo/"+version)
	if checker.downloadClient == nil {
		checker.downloadClient = newUpdateDownloadClient()
	}
	response, err := checker.downloadClient.Do(req)
	if err != nil {
		return "", errors.New("无法下载更新包，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("更新包下载失败：GitHub 返回 HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxPortableArchiveSize {
		return "", errors.New("更新包超过自动安装允许的大小")
	}

	file, err := os.CreateTemp("", "jianzuo-update-*.zip")
	if err != nil {
		return "", errors.New("无法创建更新包临时文件")
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, sum), io.LimitReader(response.Body, maxPortableArchiveSize+1))
	if err != nil {
		return "", errors.New("更新包下载中断，请重试")
	}
	if n > maxPortableArchiveSize {
		return "", errors.New("更新包超过自动安装允许的大小")
	}
	if info.Size > 0 && n != info.Size {
		return "", errors.New("更新包大小与 Release 信息不一致")
	}
	if actual := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(actual, expected) {
		return "", errors.New("更新包 SHA-256 校验失败，已取消安装")
	}
	if err = file.Sync(); err != nil {
		return "", errors.New("更新包写入失败，请重试")
	}
	if err = file.Close(); err != nil {
		return "", errors.New("更新包写入失败，请重试")
	}
	ok = true
	return path, nil
}

func updateArchiveDigest(ctx context.Context, checker *UpdateChecker, info UpdateInfo) (string, error) {
	if strings.HasPrefix(strings.ToLower(info.Digest), "sha256:") {
		value := strings.TrimPrefix(strings.ToLower(info.Digest), "sha256:")
		if len(value) == 64 {
			if _, err := hex.DecodeString(value); err == nil {
				return value, nil
			}
		}
	}
	if info.ChecksumURL == "" {
		return "", errors.New("该版本未提供 SHA-256，自动安装已取消")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", info.ChecksumURL, nil)
	if err != nil {
		return "", errors.New("校验文件地址无效")
	}
	req.Header.Set("User-Agent", "Jianzuo/"+version)
	if checker.downloadClient == nil {
		checker.downloadClient = newUpdateDownloadClient()
	}
	response, err := checker.downloadClient.Do(req)
	if err != nil {
		return "", errors.New("无法下载 SHA-256 校验文件")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("无法读取 SHA-256 校验文件")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("SHA-256 校验文件无效")
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", errors.New("SHA-256 校验文件无效")
	}
	value := strings.ToLower(fields[0])
	if _, err = hex.DecodeString(value); err != nil {
		return "", errors.New("SHA-256 校验文件无效")
	}
	return value, nil
}

func extractPortableArchive(archivePath, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return errors.New("更新包不是有效的 ZIP 文件")
	}
	defer archive.Close()
	if err = os.MkdirAll(destination, 0700); err != nil {
		return errors.New("无法创建更新暂存目录")
	}

	seen := map[string]bool{}
	var total uint64
	for _, file := range archive.File {
		name := file.Name
		if file.FileInfo().IsDir() || file.Flags&0x1 != 0 || file.Mode()&os.ModeSymlink != 0 ||
			name != filepath.Base(name) || strings.ContainsAny(name, `/\`+"\x00") || !portableArchiveFileSet[name] {
			return errors.New("更新包包含不允许的文件")
		}
		if seen[name] {
			return errors.New("更新包包含重复文件")
		}
		limit := portableFileLimit(name)
		if file.UncompressedSize64 > limit || total > maxPortableExtractedSize-file.UncompressedSize64 {
			return errors.New("更新包解压后超过允许的大小")
		}
		total += file.UncompressedSize64
		seen[name] = true
	}
	if len(seen) != len(portableArchiveFiles) {
		return errors.New("更新包不完整，自动安装已取消")
	}
	for _, file := range archive.File {
		if err = extractPortableFile(file, filepath.Join(destination, file.Name)); err != nil {
			return err
		}
	}
	return nil
}

func portableFileLimit(name string) uint64 {
	switch name {
	case "jianzuo-service.exe":
		return 256 << 20
	case "简作.exe":
		return 64 << 20
	default:
		return 8 << 20
	}
}

func extractPortableFile(file *zip.File, destination string) error {
	reader, err := file.Open()
	if err != nil {
		return errors.New("无法读取更新包文件")
	}
	defer reader.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("无法写入更新暂存文件")
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	n, err := io.Copy(output, io.LimitReader(reader, int64(portableFileLimit(file.Name))+1))
	if err != nil || uint64(n) > portableFileLimit(file.Name) {
		return errors.New("更新包文件解压失败或过大")
	}
	if err = output.Sync(); err != nil {
		return errors.New("更新暂存文件写入失败")
	}
	if err = output.Close(); err != nil {
		return errors.New("更新暂存文件写入失败")
	}
	ok = true
	return nil
}

func validatePortableStaging(directory string) error {
	for _, name := range portableArchiveFiles {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("更新包文件缺失或类型无效")
		}
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			if err = validatePEFile(path); err != nil {
				return fmt.Errorf("%s 不是有效的 Windows 程序", name)
			}
		}
	}
	return nil
}

func validatePEFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 64 {
		return errors.New("文件过小")
	}
	var header [64]byte
	if _, err = io.ReadFull(file, header[:]); err != nil {
		return err
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return errors.New("缺少 MZ 标记")
	}
	offset := int64(binary.LittleEndian.Uint32(header[60:64]))
	if offset < 64 || offset+4 > info.Size() {
		return errors.New("PE 偏移无效")
	}
	var signature [4]byte
	if _, err = file.ReadAt(signature[:], offset); err != nil {
		return err
	}
	if !bytes.Equal(signature[:], []byte{'P', 'E', 0, 0}) {
		return errors.New("缺少 PE 标记")
	}
	return nil
}

func copyUpdateHelper() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	source, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	defer source.Close()
	helper, err := os.CreateTemp("", "jianzuo-update-helper-*.exe")
	if err != nil {
		return "", err
	}
	path := helper.Name()
	ok := false
	defer func() {
		_ = helper.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err = io.Copy(helper, source); err != nil {
		return "", err
	}
	if err = helper.Sync(); err != nil {
		return "", err
	}
	if err = helper.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func runUpdateHelper(dataDir, root, stage, release string, launcherPID int) error {
	root = filepath.Clean(root)
	dataDir = filepath.Clean(dataDir)
	stage = filepath.Clean(stage)
	logUpdate(dataDir, "starting update %s from %s", release, stage)
	if launcherPID <= 0 {
		logUpdate(dataDir, "missing launcher pid")
		return errors.New("更新助手缺少启动器进程")
	}
	if stableVersionPattern.FindStringSubmatch(release) == nil {
		logUpdate(dataDir, "invalid version %q", release)
		return errors.New("更新助手收到无效版本")
	}
	if !pathWithin(root, stage) {
		logUpdate(dataDir, "stage is outside program directory")
		return errors.New("更新暂存目录无效")
	}
	if err := waitForProcessExit(launcherPID, 8*time.Second); err != nil {
		logUpdate(dataDir, "launcher did not exit: %v", err)
		return err
	}
	releaseData, err := waitForDataRelease(dataDir, 45*time.Second)
	if err != nil {
		logUpdate(dataDir, "service did not release data lock: %v", err)
		return err
	}
	locked := true
	unlock := func() {
		if locked {
			releaseData()
			locked = false
		}
	}
	defer unlock()

	if err = validatePortableStaging(stage); err != nil {
		unlock()
		launchPortableAndRelease(root, dataDir)
		logUpdate(dataDir, "staged files invalid: %v", err)
		return err
	}
	backup, err := replacePortableFiles(root, stage)
	if err != nil {
		unlock()
		launchPortableAndRelease(root, dataDir)
		logUpdate(dataDir, "replace failed: %v", err)
		return err
	}
	logUpdate(dataDir, "files replaced, backup %s", backup)
	unlock()
	launcher, err := launchPortable(root, dataDir)
	if err != nil {
		logUpdate(dataDir, "new launcher failed: %v", err)
		if rollbackErr := rollbackPortableFiles(root, backup); rollbackErr != nil {
			logUpdate(dataDir, "rollback failed: %v", rollbackErr)
			return fmt.Errorf("新版启动失败，且回滚未完成：%w", rollbackErr)
		}
		launchPortableAndRelease(root, dataDir)
		return fmt.Errorf("新版启动失败，已回滚：%w", err)
	}
	if err = waitForPortableRestart(dataDir, release, 45*time.Second); err != nil {
		logUpdate(dataDir, "new service health check failed: %v", err)
		stopPortable(launcher, dataDir)
		if rollbackErr := rollbackPortableFiles(root, backup); rollbackErr != nil {
			logUpdate(dataDir, "rollback failed: %v", rollbackErr)
			return fmt.Errorf("新版启动后未通过检查，且回滚未完成：%w", rollbackErr)
		}
		launchPortableAndRelease(root, dataDir)
		return fmt.Errorf("新版启动后未通过检查，已回滚：%w", err)
	}
	_ = launcher.Release()
	_ = os.RemoveAll(backup)
	_ = os.RemoveAll(stage)
	logUpdate(dataDir, "update %s completed", release)
	return nil
}

func waitForDataRelease(dataDir string, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		release, err := lockData(dataDir)
		if err == nil {
			return release, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func replacePortableFiles(root, stage string) (string, error) {
	backup := filepath.Join(root, ".jianzuo-update-backup-"+uid())
	if err := os.Mkdir(backup, 0700); err != nil {
		return "", err
	}
	replaced := make([]string, 0, len(portableArchiveFiles))
	for _, name := range portableArchiveFiles {
		destination := filepath.Join(root, name)
		saved := filepath.Join(backup, name)
		if err := os.Rename(destination, saved); err != nil {
			_ = rollbackPortableFiles(root, backup)
			return "", err
		}
		if err := os.Rename(filepath.Join(stage, name), destination); err != nil {
			_ = os.Rename(saved, destination)
			_ = rollbackPortableFiles(root, backup)
			return "", err
		}
		replaced = append(replaced, name)
	}
	return backup, nil
}

func rollbackPortableFiles(root, backup string) error {
	var failures []string
	for index := len(portableArchiveFiles) - 1; index >= 0; index-- {
		name := portableArchiveFiles[index]
		saved := filepath.Join(backup, name)
		if _, err := os.Stat(saved); err != nil {
			continue
		}
		destination := filepath.Join(root, name)
		_ = os.Remove(destination)
		if err := os.Rename(saved, destination); err != nil {
			failures = append(failures, name+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func launchPortable(root, dataDir string) (*os.Process, error) {
	launcher := filepath.Join(root, "简作.exe")
	if _, err := os.Stat(launcher); err != nil {
		return nil, err
	}
	return startDetachedProcess(launcher, []string{"--data", dataDir, "--background"}, root)
}

func launchPortableAndRelease(root, dataDir string) {
	process, err := launchPortable(root, dataDir)
	if err == nil {
		_ = process.Release()
	}
}

func waitForPortableRestart(dataDir, release string, timeout time.Duration) error {
	healthURL, err := portableHealthURL(dataDir)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:   750 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	expected := strings.TrimPrefix(strings.TrimSpace(release), "v") + "-portable"
	if err = waitForPortableHealth(client, healthURL, expected, timeout); err != nil {
		return err
	}
	return nil
}

func portableHealthURL(dataDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return "", fmt.Errorf("无法读取新版服务地址：%w", err)
	}
	var config struct {
		Listen string `json:"listen"`
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return "", errors.New("新版服务地址配置无效")
	}
	address := strings.TrimSpace(config.Listen)
	if address == "" {
		address = "0.0.0.0:8789"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(port) == "" {
		return "", errors.New("新版服务监听地址无效")
	}
	switch strings.Trim(host, "[]") {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

func waitForPortableHealth(client *http.Client, healthURL, expectedVersion string, timeout time.Duration) error {
	if client == nil {
		client = &http.Client{Timeout: 750 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		request, err := http.NewRequest(http.MethodGet, healthURL, nil)
		if err == nil {
			request.Header.Set("Cache-Control", "no-store")
			var response *http.Response
			response, err = client.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && readErr == nil && len(body) <= 4096 {
					var status struct {
						App     string `json:"app"`
						Version string `json:"version"`
					}
					if json.Unmarshal(body, &status) == nil && status.App == "jianzuo" {
						if status.Version == expectedVersion {
							return nil
						}
						err = fmt.Errorf("服务版本为 %s，预期 %s", status.Version, expectedVersion)
					} else {
						err = errors.New("健康检查响应无效")
					}
				} else {
					err = fmt.Errorf("健康检查返回 HTTP %d", response.StatusCode)
				}
			}
		}
		lastErr = err
		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("新版服务未在限定时间内启动")
			}
			return fmt.Errorf("新版服务未通过健康检查：%w", lastErr)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func stopPortable(process *os.Process, dataDir string) {
	if process != nil {
		_ = process.Kill()
		_, _ = process.Wait()
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		release, err := lockData(dataDir)
		if err == nil {
			release()
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func logUpdate(dataDir, format string, args ...any) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dataDir, "update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
