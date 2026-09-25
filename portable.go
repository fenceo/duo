package main

import (
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const version = "0.20.16"

// Only the native launcher's first-run wizard uses this stdin-only operation.
// Existing databases are never reset by initialization.
func initializePortable(dir string, reader io.Reader) error {
	var v struct {
		Password string `json:"password"`
		Config   Config `json:"config"`
	}
	if e := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&v); e != nil {
		return e
	}
	if strings.TrimSpace(v.Password) != v.Password || len(v.Password) < 6 || len(v.Password) > 72 {
		return errors.New("密码须为 6–72 字节，且首尾没有空白")
	}
	if _, e := os.Stat(filepath.Join(dir, "jianzuo.db")); !os.IsNotExist(e) {
		return errors.New("数据目录已经初始化，不会覆盖密码或任务")
	}
	if v.Config.Feishu.Enabled || v.Config.Feishu.Secret != "" {
		return errors.New("首次初始化不导入飞书凭据，请启动后扫码配置")
	}
	// The first-run UI can finish with only a password while discovery is
	// running. Keep that minimal initialization local by default; the user can
	// opt into LAN/Tailscale access later from the settings page.
	if strings.TrimSpace(v.Config.Listen) == "" {
		v.Config.Listen = "127.0.0.1:8789"
	}
	if e := normalizeAccess(&v.Config.Access); e != nil {
		return e
	}
	if e := normalizeEnvironments(&v.Config); e != nil {
		return e
	}
	c := &ConfigFile{path: filepath.Join(dir, "config.json")}
	if e := c.save(v.Config); e != nil {
		return e
	}
	encoded, e := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
	if e != nil {
		return e
	}
	s, e := openStore(dir)
	if e != nil {
		return e
	}
	defer s.Close()
	return s.set("password_hash", string(encoded))
}
