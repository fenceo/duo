package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var errUpdateBusy = errors.New("正在准备更新，请等待更新完成后再操作")

// Lock all work-admission points, including Feishu and WebSocket handshakes.
// No existing work is cancelled merely to install an update.
func (a *App) beginUpdate() (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminals.mu.Lock()
	defer a.terminals.mu.Unlock()
	a.hardware.mu.Lock()
	defer a.hardware.mu.Unlock()
	if a.updating.Load() {
		return nil, errUpdateBusy
	}
	var pending int
	if err := a.store.QueryRow("SELECT count(*) FROM runs WHERE status IN ('queued','running')").Scan(&pending); err != nil {
		return nil, fmt.Errorf("无法确认任务空闲，已取消更新：%w", err)
	}
	var busy []string
	if len(a.workers) > 0 || pending > 0 {
		busy = append(busy, "AI 任务仍在执行或排队")
	}
	if len(a.terminals.links) > 0 {
		busy = append(busy, "终端仍在连接")
	}
	if len(a.hardware.links) > 0 {
		busy = append(busy, "串口/继电器等硬件仍在连接")
	}
	if len(busy) > 0 {
		return nil, fmt.Errorf("%w：%s；请先结束或断开后重试，不会强制停止", errUpdateBusy, strings.Join(busy, "、"))
	}
	a.updating.Store(true)
	a.terminals.updating = true
	a.hardware.updating = true
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.terminals.mu.Lock()
		defer a.terminals.mu.Unlock()
		a.hardware.mu.Lock()
		defer a.hardware.mu.Unlock()
		a.hardware.updating = false
		a.terminals.updating = false
		a.updating.Store(false)
	}, nil
}

func installationMode(root, service string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || !filepath.IsAbs(service) {
		return "unmanaged", errors.New("当前为开发/独立服务启动，请下载 Windows 安装版进行安装")
	}
	root = filepath.Clean(root)
	if !strings.EqualFold(filepath.Clean(service), filepath.Join(root, "duo-service.exe")) {
		return "unmanaged", errors.New("运行服务不属于启动器所在程序目录，请手动安装")
	}
	for path := root; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "unmanaged", errors.New("程序目录无效或包含目录联接，已禁用自动更新")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	marker := filepath.Join(root, ".jianzuo-install")
	if info, err := os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 16384 {
			return "unmanaged", errors.New("安装所有权标记无效，请使用安装包修复；不会按便携版覆盖")
		}
		raw, err := os.ReadFile(marker)
		// UTF-8 BOM may have been written by the legacy .NET installer.
		text := strings.TrimPrefix(string(raw), "\ufeff")
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if err != nil || !strings.EqualFold(text, "Jianzuo installer ownership v1\n"+root) {
			return "unmanaged", errors.New("安装所有权标记损坏或指向其它目录，请使用安装包修复；不会按便携版覆盖")
		}
		return "installed", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "unmanaged", errors.New("无法读取安装所有权标记，已禁用自动更新")
	}
	for _, name := range portableArchiveFiles {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || !info.Mode().IsRegular() {
			return "unmanaged", errors.New("当前目录不是完整的便携包，请下载 Windows 安装版或重新解压便携包")
		}
	}
	for _, name := range []string{"卸载简作.exe", "unins000.exe"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			return "unmanaged", errors.New("检测到安装器但缺少有效所有权标记，请使用安装包修复")
		}
	}
	return "portable", nil
}

func (s *Server) currentInstallation() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "unmanaged", err
	}
	return installationMode(s.updateRoot, exe)
}

func innoReleaseSupported(release string) bool {
	comparison, err := compareReleaseVersions(release, "0.19.0")
	return err == nil && comparison >= 0
}

func selectUpdateAsset(v UpdateInfo, mode string) UpdateInfo {
	v.InstallationMode = mode
	asset := v.portable
	v.PackageKind = "portable"
	if mode != "portable" {
		asset = v.installer
		v.PackageKind = "installer"
	}
	v.DownloadURL, v.ChecksumURL, v.Digest, v.Size = asset.URL, asset.ChecksumURL, asset.Digest, asset.Size
	return v
}
