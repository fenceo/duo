package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// uiFixtureRunner never starts a process, uses credentials, or calls a model.
// [wait] exercises stop/queued cancellation; [fail] exercises failure recovery.
type uiFixtureRunner struct{}

func (uiFixtureRunner) Run(ctx context.Context, _ Config, task Task, input string, emit func(string, string)) (string, string, error) {
	session := "ui-fixture-codex-session"
	if task.Engine == "deepseek-harness" {
		// Do not invent a live native Harness worker. The seeded expired session
		// exercises the real runtime guard; a reset task gets a synthetic reply.
		session = ""
	}
	if strings.HasPrefix(input, "[wait]") {
		emit("progress", "UI 验收：模拟执行中，请点击停止；没有调用模型或工具。")
		<-ctx.Done()
		return session, "", ctx.Err()
	}
	if strings.HasPrefix(input, "[fail]") {
		return session, "", errors.New("UI 验收：模拟失败；输入普通消息可继续，没有调用模型")
	}
	select {
	case <-ctx.Done():
		return session, "", ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	reply := "UI 验收模拟回复（未调用模型）：" + input
	emit("assistant", reply)
	return session, reply, nil
}

// Explicit opt-in only. All state lives in testing.TempDir and is removed when
// the fixture finishes; the listener is loopback-only and expires in 10 minutes.
func TestUIFixture(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_UI_FIXTURE") != "1" {
		t.Skip("set JIANZUO_TEST_UI_FIXTURE=1 to launch an isolated manual UI fixture")
	}
	a := fixture(t, uiFixtureRunner{})
	workspace := t.TempDir()
	env := Environment{
		ID: "ui-fixture", Name: "UI 验收环境（无真实模型）", Type: "windows",
		Codex: filepath.Join(workspace, "no-real-codex.exe"), Claude: filepath.Join(workspace, "no-real-claude.exe"), Harness: filepath.Join(workspace, "no-real-harness.exe"),
		Model: "fixture-model", HarnessModel: "fixture-model", HarnessProvider: "fixture-provider", DefaultEngine: "codex",
		ModelCache: filepath.Join(workspace, "fixture-models.json"), Models: []ModelOption{{ID: "fixture-model", Name: "合成测试模型", ReasoningLevels: []string{}}}, Workspaces: []string{workspace},
	}
	emptyEnv, errorEnv := env, env
	emptyEnv.ID, emptyEnv.Name, emptyEnv.Model, emptyEnv.Models = "ui-empty", "空模型目录（合成测试）", "", nil
	errorEnv.ID, errorEnv.Name, errorEnv.Model, errorEnv.Models = "ui-error", "模型读取失败（合成测试）", "", nil
	c := Config{Listen: "127.0.0.1:0", Environments: []Environment{env, emptyEnv, errorEnv}, DefaultEnvironment: env.ID, Workspaces: []string{workspace}, Codex: env.Codex, Model: env.Model}
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	const password = "jianzuo-ui-fixture-only"
	digest, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.set("password_hash", string(digest)); err != nil {
		t.Fatal(err)
	}
	mode := WorkMode{ID: "harness:read", Name: "只读·可联网", Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}
	expired, err := a.createWithExecutionAndMode("验收 · Harness 已失效会话", workspace, env.HarnessModel, "deepseek-harness", "", &mode, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE tasks SET session='expired-fixture',status='done' WHERE id=?", expired.ID); err != nil {
		t.Fatal(err)
	}
	runID := uid()
	if _, err = a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,'合成历史问题','chat','web','done','合成历史结果：此内容在重置会话后应保留。',?,?)", runID, expired.ID, now(), now()); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct{ kind, text string }{{"user", "合成历史问题"}, {"assistant", "合成历史结果：此内容在重置会话后应保留。"}} {
		if err := a.store.event(expired.ID, runID, event.kind, event.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.writeKnowledge(Knowledge{ID: uid(), TaskID: expired.ID, Title: "合成知识：重置不应删除", Content: "这条知识仅用于 UI 验收，不来自任何真实项目。", Status: "verified", Source: "manual", Revision: 1, Created: now(), Updated: now()}, true); err != nil {
		t.Fatal(err)
	}
	ordinary, err := a.createWithExecution("验收 · Codex 普通任务", workspace, env.Model, "codex", "", env.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{app: a}
	production := server.Handler()
	finished := make(chan struct{})
	var finishOnce sync.Once
	finish := server.secure(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
		finishOnce.Do(func() { close(finished) })
	})
	// Catalog responses are synthetic too: production catalog discovery may
	// read the user's global CLI caches even with an explicit ModelCache path.
	catalog := server.secure(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/ui-error/") {
			fail(w, http.StatusBadGateway, "合成错误：目标环境暂不可达；未读取其它环境或账号")
			return
		}
		models := []map[string]any{}
		message, status, defaultModel := "", "native", "fixture-model"
		engine := r.URL.Query().Get("engine")
		if engine == "" {
			engine = "codex"
		}
		if strings.Contains(r.URL.Path, "/ui-empty/") {
			message, status, defaultModel = "目标引擎暂未返回模型；可以重读或输入自定义模型。", "empty", ""
		} else {
			prefix := engine
			if engine != "codex" {
				defaultModel = prefix + "-fixture-default"
			}
			models = append(models,
				map[string]any{"id": defaultModel, "name": prefix + " · 合成默认模型", "origin": "native", "reasoning_levels": []string{"low", "high"}},
				map[string]any{"id": prefix + "-fixture-fast", "name": prefix + " · 合成快速模型", "origin": "native", "reasoning_levels": []string{}},
				map[string]any{"id": prefix + "-fixture-custom", "name": prefix + " · 自定义模型（未验证调用）", "origin": "configured"},
			)
		}
		jsonOut(w, http.StatusOK, map[string]any{"source": "UI fixture（合成模型，不调用 CLI）", "models": models, "message": message, "status": status, "default_model": defaultModel})
	})
	blocked := server.secure(func(w http.ResponseWriter, _ *http.Request) {
		fail(w, http.StatusForbidden, "UI 验收环境禁止真实 CLI、模型探测、设备、环境发现及配置修改")
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
			fail(w, http.StatusForbidden, "loopback only")
			return
		}
		path := r.URL.Path
		if path == "/__fixture/finish" {
			finish(w, r)
			return
		}
		if (path == "/api/environments/ui-fixture/models" || path == "/api/environments/ui-empty/models" || path == "/api/environments/ui-error/models") && r.Method == http.MethodGet {
			catalog(w, r)
			return
		}
		if strings.HasPrefix(path, "/api/") {
			core := path == "/api/auth" || path == "/api/login" || path == "/api/logout" || path == "/api/tasks" || path == "/api/trash" || path == "/api/workbench" || strings.HasPrefix(path, "/api/tasks/") || strings.HasPrefix(path, "/api/sticky") || (path == "/api/settings" || path == "/api/scratch") && r.Method == http.MethodGet
			for _, forbidden := range []string{"/terminal", "/hardware", "/context", "/files", "/browse", "/search"} {
				if strings.Contains(path, forbidden) {
					core = false
				}
			}
			if !core {
				blocked(w, r)
				return
			}
		}
		production.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	fmt.Printf("UI_FIXTURE_URL=%s/\nUI_FIXTURE_PASSWORD=%s\nUI_FIXTURE_EXPIRED_TASK=%s/?task=%s\nUI_FIXTURE_CODEX_TASK=%s/?task=%s\nUI_FIXTURE_FINISH=POST %s/__fixture/finish (login cookie + X-CSRF-Token)\n", srv.URL, password, srv.URL, expired.ID, srv.URL, ordinary.ID, srv.URL)
	t.Log("Synthetic state only. Ordinary text replies; [wait] waits for Stop; [fail] simulates failure. Automatic expiry: 10 minutes.")
	select {
	case <-finished:
		t.Log("UI fixture explicitly finished")
	case <-time.After(10 * time.Minute):
		t.Log("UI fixture expired after 10 minutes")
	}
}
