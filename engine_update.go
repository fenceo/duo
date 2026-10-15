package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Each upgrade installs into a new directory. Only snapshots that used one of
// its predecessor paths on this exact target follow the verified replacement.
type ManagedEngineTool struct {
	Path          string   `json:"path"`
	Version       string   `json:"version"`
	PreviousPaths []string `json:"previous_paths"`
}

var errEngineUpdateSettingsBusy = errors.New("正在更新 CLI，请等待完成或取消后再保存设置")
var errEngineUpdateSettingsStale = errors.New("CLI 更新记录已变化，请重新打开设置后再保存")

// Serialize settings with CLI activation. Update metadata is server-owned:
// a stale settings tab must not undo a completed update or invent path aliases.
func (a *App) saveSettingsConfig(c Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m := a.engineSetup; m != nil {
		m.mu.Lock()
		updating := m.updatingTarget != nil
		m.mu.Unlock()
		if updating {
			return errEngineUpdateSettingsBusy
		}
	}
	return a.config.save(c)
}

func sameManagedEngineTools(current, next Config) bool {
	for _, env := range next.Environments {
		before, _ := current.environment(env.ID)
		if len(env.ManagedTools) == 0 && len(before.ManagedTools) == 0 {
			continue
		}
		if !reflect.DeepEqual(env.ManagedTools, before.ManagedTools) {
			return false
		}
	}
	return true
}

func sameEngineTarget(a, b Environment) bool {
	return a.ID == b.ID && a.Type == b.Type && a.Distro == b.Distro && a.User == b.User && a.Host == b.Host && a.Port == b.Port && a.Identity == b.Identity
}

func environmentEnginePath(e Environment, engine string) string {
	switch engine {
	case "codex":
		return e.Codex
	case "claude":
		return e.Claude
	case "deepseek-harness":
		return e.Harness
	case "mimo":
		return e.Mimo
	case "opencode":
		return e.OpenCode
	}
	return ""
}

func managedEnginePath(c Config, e Environment, engine string) string {
	old := environmentEnginePath(e, engine)
	current, err := c.environment(e.ID)
	if err != nil || !sameEngineTarget(current, e) {
		return old
	}
	tool, ok := current.ManagedTools[engine]
	if !ok || tool.Path == "" || tool.Path != environmentEnginePath(current, engine) {
		return old
	}
	for _, previous := range tool.PreviousPaths {
		if previous == old {
			return tool.Path
		}
	}
	return old
}

var engineVersionPattern = regexp.MustCompile(`(?m)(?:^|\s)v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)(?:\s|$)`)

func readEngineVersion(ctx context.Context, probe environmentProbe, env Environment, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("未配置 CLI 路径，请先安装")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{path, "--version"}
	if env.Type == "windows" && strings.EqualFold(filepath.Base(path), "dsh.cmd") {
		entry, err := harnessExecutable(path)
		if err != nil {
			return "", errors.New("Harness CLI 入口检查失败，原工具路径保持不变")
		}
		args = append(entry, "--version")
	}
	out, err := probe(bounded, env, args...)
	match := engineVersionPattern.FindStringSubmatch(out)
	if err != nil || len(match) < 2 || len(match[1]) > 80 {
		return "", errors.New("CLI 版本检查失败，原工具路径保持不变")
	}
	return match[1], nil
}

// Caller holds a.mu, which also serializes task submission and worker teardown.
func (a *App) engineTargetBusy(env Environment, engine string) error {
	ids := map[string]bool{}
	for id := range a.workers {
		ids[id] = true
	}
	rows, err := a.store.Query("SELECT DISTINCT task_id FROM runs WHERE status IN ('running','queued')")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range ids {
		task, err := a.store.task(id)
		if err != nil {
			return err
		}
		if task.Engine == engine && task.Environment != nil && sameEngineTarget(*task.Environment, env) {
			return errors.New("该环境的引擎仍有执行或排队任务，请先结束后再更新 CLI")
		}
	}
	return nil
}

func (a *App) engineUpdateBlocks(task Task) bool {
	m := a.engineSetup
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updatingTarget != nil && task.Environment != nil && task.Engine == m.updatingEngine && sameEngineTarget(*task.Environment, *m.updatingTarget)
}

// An idle ACP worker can reopen its durable session after a verified CLI update.
// Any other fingerprint change still requires the existing explicit workflow.
func managedHarnessReplacement(c Config, t Task, old Config, fingerprint string) bool {
	if t.Environment == nil || old.Harness == c.Harness {
		return false
	}
	snapshot := *t.Environment
	snapshot.Harness = old.Harness
	if managedEnginePath(c, snapshot, "deepseek-harness") != c.Harness {
		return false
	}
	c.Harness = old.Harness
	return harnessFingerprint(c, t) == fingerprint
}
