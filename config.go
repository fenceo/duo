package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	HardwareAI *HardwareRuntime `json:"-"`
	// EngineEnv is populated for one run from the selected external profile.
	// It is never serialized to config.json or returned by the settings API.
	EngineEnv          map[string]string `json:"-"`
	Access             AccessConfig      `json:"access"`
	Environments       []Environment     `json:"environments"`
	DefaultEnvironment string            `json:"default_environment"`
	SSHHost            string            `json:"-"`
	SSHPort            int               `json:"-"`
	SSHKey             string            `json:"-"`
	Listen             string            `json:"listen"`
	Distro             string            `json:"distro"`
	User               string            `json:"user"`
	Codex              string            `json:"codex"`
	Claude             string            `json:"-"`
	Harness            string            `json:"-"`
	HarnessModel       string            `json:"-"`
	HarnessProvider    string            `json:"-"`
	Workspaces         []string          `json:"workspaces"`
	Model              string            `json:"model"`
	Feishu             FeishuConfig      `json:"feishu"`
}
type FeishuConfig struct {
	Enabled bool   `json:"enabled"`
	AppID   string `json:"app_id"`
	Secret  string `json:"secret,omitempty"`
	Owner   string `json:"owner,omitempty"`
}
type ConfigFile struct {
	sync.RWMutex
	path  string
	value Config
}

func loadConfig(dir string) (*ConfigFile, error) {
	c := &ConfigFile{path: filepath.Join(dir, "config.json")}
	c.value = Config{Listen: "0.0.0.0:8789", Distro: "Ubuntu-22.04", User: "dev", Codex: "codex", Workspaces: []string{"/home/dev/work"}}
	b, e := os.ReadFile(c.path)
	if e == nil {
		e = json.Unmarshal(b, &c.value)
		if e == nil {
			if len(c.value.Environments) == 0 {
				e = c.save(c.value)
			} else {
				e = normalizeEnvironments(&c.value)
			}
		}
	} else if os.IsNotExist(e) {
		e = c.save(c.value)
	}
	return c, e
}
func (c *ConfigFile) get() Config {
	c.RLock()
	defer c.RUnlock()
	var v Config
	raw, _ := json.Marshal(c.value)
	_ = json.Unmarshal(raw, &v)
	return v
}
func (c *ConfigFile) save(v Config) error {
	if err := normalizeAccess(&v.Access); err != nil {
		return err
	}
	if v.Listen == "" {
		return errors.New("监听地址不能为空")
	}
	if err := normalizeEnvironments(&v); err != nil {
		return err
	}
	if v.Feishu.Enabled && (v.Feishu.AppID == "" || v.Feishu.Secret == "") {
		return errors.New("请填写飞书 App ID 和 App Secret")
	}
	c.Lock()
	defer c.Unlock()
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := c.path + ".tmp"
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(tmp, c.path); e != nil {
		return e
	}
	c.value = v
	return nil
}
