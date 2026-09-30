package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed account_files.py
var nativeAccountScript string

type nativeAccountRequest struct {
	Operation string            `json:"operation"`
	Engine    string            `json:"engine"`
	Directory string            `json:"directory"`
	Expected  map[string][]byte `json:"expected,omitempty"`
	Files     map[string][]byte `json:"files,omitempty"`
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		return 0, errors.New("bounded output exceeded")
	}
	return b.buffer.Write(p)
}
func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

func nativeAccountNames(engine string) ([]string, error) {
	switch engine {
	case "codex":
		return []string{"auth.json", "config.toml"}, nil
	case "claude":
		return []string{".credentials.json", "settings.json"}, nil
	default:
		return nil, errors.New("此引擎不支持账号文件同步")
	}
}

func localAccountDirectory(dir, engine string) (string, error) {
	if _, err := nativeAccountNames(engine); err != nil {
		return "", err
	}
	if dir == "" {
		key := "CODEX_HOME"
		if engine == "claude" {
			key = "CLAUDE_CONFIG_DIR"
		}
		dir = os.Getenv(key)
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", errors.New("无法确定用户配置目录")
			}
			dir = filepath.Join(home, "."+engine)
		}
	}
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\r\n") {
		return "", errors.New("账号配置目录必须为绝对路径")
	}
	// Reject redirecting directories including parent junctions/symlinks.
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", errors.New("账号路径不能经过符号链接或非目录")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", errors.New("账号目录不可访问")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return filepath.Clean(dir), nil
}

func readLocalAccountFiles(dir, engine string) (map[string][]byte, error) {
	names, err := nativeAccountNames(engine)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			out[name] = nil
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxCodexAuthBytes {
			return nil, errors.New("账号文件无法读取、不是普通文件或过大")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, errors.New("无法读取账号文件")
		}
		data, err := io.ReadAll(io.LimitReader(f, maxCodexAuthBytes+1))
		f.Close()
		if err != nil || len(data) > maxCodexAuthBytes {
			return nil, errors.New("账号文件无法读取或过大")
		}
		out[name] = data
	}
	return out, nil
}

func writeLocalAccountFiles(dir, engine string, expected, next map[string][]byte) error {
	names, err := nativeAccountNames(engine)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建账号目录")
	}
	lock := filepath.Join(dir, ".duo-account-sync.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("此账号目录正在同步；若上次进程中断，请检查备份后清理同步锁")
	}
	f.Close()
	defer os.Remove(lock)
	old, err := readLocalAccountFiles(dir, engine)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !bytes.Equal(old[name], expected[name]) || (old[name] == nil) != (expected[name] == nil) {
			return errors.New("目标账号文件已变化，请重新同步")
		}
		if _, ok := next[name]; !ok || len(next[name]) > maxCodexAuthBytes {
			return errors.New("账号写入内容无效")
		}
	}
	stage, err := os.MkdirTemp(dir, ".duo-account-backup-")
	if err != nil {
		return errors.New("无法创建账号备份")
	}
	// Leave complete backups after both success and failure for recovery.
	for _, name := range names {
		if old[name] != nil {
			if err = os.WriteFile(filepath.Join(stage, name), old[name], 0600); err != nil {
				return errors.New("无法备份账号文件")
			}
		}
		if next[name] != nil {
			if err = os.WriteFile(filepath.Join(stage, name+".new"), next[name], 0600); err != nil {
				return errors.New("无法准备账号文件")
			}
		}
	}
	changed := []string{}
	for _, name := range names {
		target := filepath.Join(dir, name)
		if next[name] != nil {
			err = os.Rename(filepath.Join(stage, name+".new"), target)
		} else {
			err = os.Remove(target)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			break
		}
		changed = append(changed, name)
	}
	if err != nil {
		for _, name := range changed {
			path := filepath.Join(dir, name)
			if old[name] == nil {
				_ = os.Remove(path)
			} else {
				_ = os.WriteFile(path, old[name], 0600)
			}
		}
		return errors.New("写入失败，已尝试恢复原文件；完整备份保留在目标账号目录")
	}
	return nil
}

func remoteNativeAccountFiles(ctx context.Context, env Environment, req nativeAccountRequest) (map[string][]byte, error) {
	if env.Type != "wsl" && env.Type != "ssh" {
		return nil, errors.New("账号远程操作仅支持 WSL/SSH")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, errors.New("账号请求无效")
	}
	base := environmentProbeCommand(env, "sh", "-lc", "exec python3 -c "+posixQuote(nativeAccountScript))
	cmd := commandWithContext(ctx, base)
	cmd.Stdin = bytes.NewReader(data)
	// Bounded stdout is decoded only in memory, never shown in diagnostics.
	out := &limitedBuffer{limit: 12 * 1024 * 1024}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	hideCommand(cmd)
	if err = cmd.Run(); err != nil {
		return nil, errors.New("远程账号操作失败，请检查 Python 3、连接、文件权限和同步锁；原文件备份留在目标配置目录")
	}
	var result map[string][]byte
	if json.Unmarshal(out.Bytes(), &result) != nil {
		return nil, errors.New("目标环境返回无效的账号操作结果")
	}
	return result, nil
}

func readNativeAccountFiles(ctx context.Context, env Environment, dir, engine string) (map[string][]byte, error) {
	if env.Type != "windows" {
		return remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "read", Engine: engine, Directory: dir})
	}
	dir, err := localAccountDirectory(dir, engine)
	if err != nil {
		return nil, err
	}
	return readLocalAccountFiles(dir, engine)
}

func writeNativeAccountFiles(ctx context.Context, env Environment, dir, engine string, old, next map[string][]byte) error {
	if env.Type != "windows" {
		_, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "write", Engine: engine, Directory: dir, Expected: old, Files: next})
		return err
	}
	dir, err := localAccountDirectory(dir, engine)
	if err != nil {
		return err
	}
	return writeLocalAccountFiles(dir, engine, old, next)
}
