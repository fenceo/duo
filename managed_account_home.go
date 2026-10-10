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

//go:embed managed_account_home.py
var managedAccountHomeScript string

func createManagedAccountHome(ctx context.Context, env Environment, data, id, engine string, files map[string][]byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := map[string]string{"codex": "config.toml", "claude": "settings.json"}[engine]
	if !safeWorkbenchID(id) || len(id) > 48 || name == "" || len(files) != 1 || files[name] == nil || len(files[name]) > 128*1024 {
		return "", errors.New("账号目录请求无效")
	}
	if env.Type == "windows" {
		dir, err := localAccountDirectory(filepath.Join(data, "engine-accounts", id), engine)
		if err != nil {
			return "", err
		}
		if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			return "", errors.New("无法创建账号目录")
		}
		if err = os.Mkdir(dir, 0700); err != nil {
			return "", errors.New("账号目录已存在或无法创建，请重新开始")
		}
		if err = os.WriteFile(filepath.Join(dir, name), files[name], 0600); err != nil {
			return "", errors.New("无法保存原生账号配置")
		}
		return dir, nil
	}
	if env.Type != "wsl" && env.Type != "ssh" {
		return "", errors.New("目标环境不支持账号配置")
	}
	raw, _ := json.Marshal(struct {
		ID     string            `json:"id"`
		Engine string            `json:"engine"`
		Files  map[string][]byte `json:"files"`
	}{id, engine, files})
	cmd := commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", "exec python3 -c "+posixQuote(managedAccountHomeScript)))
	cmd.Stdin = bytes.NewReader(raw)
	out := &limitedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	cmd.WaitDelay = 2 * time.Second
	hideCommand(cmd)
	if err := cmd.Run(); err != nil {
		return "", errors.New("无法在目标用户下创建独立账号目录，请检查 WSL/SSH 连接、Python 3 和主目录权限")
	}
	var result struct {
		Directory string `json:"directory"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || !strings.HasPrefix(result.Directory, "/") || !strings.HasSuffix(result.Directory, "/.local/share/duo/engine-accounts/"+id) || strings.ContainsAny(result.Directory, "\x00\r\n") {
		return "", errors.New("目标环境返回无效的账号目录")
	}
	return result.Directory, nil
}

func sameAccountEnvironment(before, after Environment) bool {
	return before.ID == after.ID && before.Type == after.Type && before.Distro == after.Distro && before.User == after.User && before.Host == after.Host && before.Port == after.Port && before.Identity == after.Identity
}
