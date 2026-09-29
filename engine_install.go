package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var managedEnginePackages = map[string]string{"codex": "@openai/codex", "claude": "@anthropic-ai/claude-code", "deepseek-harness": "@deepseek-ai/dsh"}

const managedNodeVersion = "24.19.0"

// Official nodejs.org v24.19.0 SHASUMS256.txt, pinned with the runtime version.
const managedNodeSHA256 = "57f71ab3652e797d84acddc79c81cc9ff1c6ddb2a1974cdb83f00fee9bff4c73"

func extractManagedRuntime(archive, destination string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	if len(z.File) > 30000 {
		return errors.New("运行时压缩包文件过多")
	}
	var size uint64
	for _, f := range z.File {
		if !filepath.IsLocal(filepath.FromSlash(f.Name)) || strings.Contains(f.Name, "\\") || strings.Contains(f.Name, ":") || f.Mode()&os.ModeSymlink != 0 {
			return errors.New("运行时压缩包路径无效")
		}
		size += f.UncompressedSize64
		if size > 350*1024*1024 {
			return errors.New("运行时压缩包过大")
		}
		target := filepath.Join(destination, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(src, int64(f.UncompressedSize64)+1))
		closeErr := out.Close()
		src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
func downloadManagedNode(ctx context.Context, directory string) (string, error) {
	name := "node-v" + managedNodeVersion + "-win-x64"
	req, err := http.NewRequestWithContext(ctx, "GET", "https://nodejs.org/dist/v"+managedNodeVersion+"/"+name+".zip", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 4 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Host != "nodejs.org" || len(via) > 3 {
			return errors.New("运行时下载重定向无效")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("无法下载 Node.js 运行时，请检查网络后重试")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("官方运行时下载暂不可用")
	}
	archive := filepath.Join(directory, "node.zip")
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, digest), io.LimitReader(res.Body, 100*1024*1024+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n > 100*1024*1024 {
		return "", errors.New("运行时下载未完成或文件过大")
	}
	if hex.EncodeToString(digest.Sum(nil)) != managedNodeSHA256 {
		return "", errors.New("运行时校验不匹配，未执行下载文件")
	}
	if err = extractManagedRuntime(archive, directory); err != nil {
		return "", errors.New("运行时解压失败")
	}
	return filepath.Join(directory, name), nil
}
func installManagedEngine(ctx context.Context, directory, engine string, progress func(string)) (string, error) {
	pkg, ok := managedEnginePackages[engine]
	if !ok {
		return "", errors.New("引擎安装包无效")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return "", errors.New("自动安装目前支持 Windows x64")
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		return "", err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", errors.New("安装目录已存在或无法创建，请重新开始")
	}
	progress("正在下载并校验 Node.js 运行时…")
	nodeDir, err := downloadManagedNode(ctx, directory)
	if err != nil {
		return "", err
	}
	progress("正在从官方 npm 仓库安装 " + pkg + "…")
	npmConfig := filepath.Join(directory, "npmrc")
	if err = os.WriteFile(npmConfig, []byte("registry=https://registry.npmjs.org/\n"), 0600); err != nil {
		return "", err
	}
	node := filepath.Join(nodeDir, "node.exe")
	cmd := exec.CommandContext(ctx, node, filepath.Join(nodeDir, "node_modules", "npm", "bin", "npm-cli.js"), "install", "--global", "--prefix", nodeDir, "--registry=https://registry.npmjs.org/", "--userconfig", npmConfig, "--cache", filepath.Join(directory, "npm-cache"), "--no-audit", "--no-fund", pkg+"@latest")
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "PATH="+nodeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	hideCommand(cmd)
	// npm can print inherited registry or proxy diagnostics. Never relay raw
	// installer output through HTTP or mix it with the conversation history.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopAppServerTree(Config{}, cmd, 0)
	}
	if err = cmd.Run(); err != nil {
		return "", errors.New("工具安装失败，请检查网络后重试；已安装的其它版本未修改")
	}
	binary := ""
	if engine == "deepseek-harness" {
		binary = filepath.Join(nodeDir, "dsh.cmd")
		if _, err = os.Stat(binary); err != nil {
			return "", errors.New("安装包缺少 Harness 启动入口")
		}
	} else if engine == "claude" {
		// Claude's npm postinstall links/copies the platform binary into bin;
		// both this entry and its optional dependency may contain claude.exe.
		binary = filepath.Join(nodeDir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
		if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
			return "", errors.New("Claude 安装包缺少声明的 Windows 入口")
		}
	} else {
		name := engine + ".exe"
		err = filepath.WalkDir(filepath.Join(nodeDir, "node_modules"), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !d.IsDir() && strings.EqualFold(d.Name(), name) {
				if binary != "" {
					return errors.New("安装包包含多个本机执行入口")
				}
				binary = path
			}
			return nil
		})
		if err != nil || binary == "" {
			return "", errors.New("安装包缺少可识别的 Windows 程序")
		}
	}
	progress("正在检查工具版本…")
	args := []string{binary, "--version"}
	if engine == "deepseek-harness" {
		args = []string{node, filepath.Join(nodeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"), "--version"}
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verify := exec.CommandContext(verifyCtx, args[0], args[1:]...)
	hideCommand(verify)
	verify.Stdout = io.Discard
	verify.Stderr = io.Discard
	if err = verify.Run(); err != nil {
		return "", errors.New("工具已下载，但启动检查失败，尚未替换环境配置")
	}
	return binary, nil
}
