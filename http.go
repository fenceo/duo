package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html web/app.optimized.js web/style.css web/workbench.css web/vendor web/icon.svg web/favicon.ico web/icon-256.png web/apple-touch-icon.png
var assets embed.FS

type attempts struct {
	n     int
	since time.Time
}
type Server struct {
	app                  *App
	mu                   sync.Mutex
	attempts             map[string]attempts
	environmentDetection sync.Mutex
	accountInfoMu        sync.Mutex
	accountInfoBusy      map[string]bool
	updateMu             sync.Mutex
	modelProbeMu         sync.Mutex
	updateRoot           string
	dataDir              string
	switchRequest        string
	launcherPID          int
	shutdown             func()
}

func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]string{"error": msg})
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	d := json.NewDecoder(r.Body)
	if d.Decode(v) != nil {
		fail(w, 400, "请求内容无效")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "请求内容无效")
		return false
	}
	return true
}
func (s *Server) identity(r *http.Request) (string, string) {
	cookie, e := r.Cookie("jianzuo_session")
	if e != nil {
		return "", ""
	}
	var csrf string
	e = s.app.store.QueryRow("SELECT csrf FROM sessions WHERE token=? AND expires>?", hash(cookie.Value), now()).Scan(&csrf)
	if e != nil {
		return "", ""
	}
	return hash(cookie.Value), csrf
}
func (s *Server) secure(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, csrf := s.identity(r)
		if csrf == "" {
			fail(w, 401, "请先登录")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				fail(w, 403, "登录校验已失效，请刷新页面")
				return
			}
			if s.app.updating.Load() {
				fail(w, http.StatusConflict, errUpdateBusy.Error())
				return
			}
		}
		next(w, r)
	}
}
func (s *Server) Handler() http.Handler {
	s.attempts = map[string]attempts{}
	m := http.NewServeMux()
	s.organizeRoutes(m)
	s.workbenchRoutes(m)
	s.updateRoutes(m)
	s.engineRoutes(m)
	s.workspaceRoutes(m)
	s.workspaceTransferRoutes(m)
	s.handoffRoutes(m)
	s.scratchRoutes(m)
	s.stickyRoutes(m)
	s.knowledgeRoutes(m)
	s.libraryRoutes(m)
	s.hardwareRoutes(m)
	s.hardwareAIRoutes(m)
	s.terminalRoutes(m)
	s.discoveryRoutes(m)
	s.codexApprovalRoutes(m)
	m.HandleFunc("POST /api/tasks/{id}/note/adopt", s.secure(s.adoptKnowledge))
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// v0.18.x update helpers expect the historical -portable health version
		// exactly. Keep this wire field while exposing the actual release version.
		release := strings.TrimSuffix(version, "-portable")
		jsonOut(w, 200, map[string]string{"app": "jianzuo", "product": "Duo", "version": release + "-portable", "release_version": release})
	})
	m.HandleFunc("GET /api/auth", func(w http.ResponseWriter, r *http.Request) {
		_, csrf := s.identity(r)
		jsonOut(w, 200, map[string]any{"authenticated": csrf != "", "csrf": csrf, "version": strings.TrimSuffix(version, "-portable")})
	})
	m.HandleFunc("POST /api/login", s.login)
	m.HandleFunc("POST /api/logout", s.secure(func(w http.ResponseWriter, r *http.Request) {
		token, _ := s.identity(r)
		_, _ = s.app.store.Exec("DELETE FROM sessions WHERE token=?", token)
		s.app.terminals.closeOwner(token)
		s.app.discovery.cancelOwner(token)
		http.SetCookie(w, &http.Cookie{Name: "jianzuo_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/tasks", s.secure(func(w http.ResponseWriter, r *http.Request) {
		v, e := s.app.store.tasks()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		jsonOut(w, 200, v)
	}))
	m.HandleFunc("POST /api/tasks", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			SubmitOptions
			Title, Workspace, Model, Input string
			EnvironmentID                  string `json:"environment_id"`
			ReasoningEffort                string `json:"reasoning_effort"`
			Engine                         string `json:"engine"`
		}
		if !body(w, r, &v) {
			return
		}
		mode, e := s.app.store.resolveMode(v.ModeID, nil)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		t, e := s.app.createWithExecutionAndMode(v.Title, v.Workspace, v.Model, v.Engine, v.ReasoningEffort, &mode, v.EnvironmentID)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		if strings.TrimSpace(v.Input) != "" {
			if _, e = s.app.submitWithOptions(t.ID, v.Input, "chat", "web", v.SubmitOptions); e != nil {
				jsonOut(w, 201, map[string]any{"task": t, "start_error": e.Error()})
				return
			}
		}
		jsonOut(w, 201, map[string]any{"task": t})
	}))
	m.HandleFunc("GET /api/tasks/{id}", s.secure(s.detail))
	m.HandleFunc("PATCH /api/tasks/{id}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Title           *string `json:"title"`
			Model           *string `json:"model"`
			ReasoningEffort *string `json:"reasoning_effort"`
		}
		if !body(w, r, &v) {
			return
		}
		id := r.PathValue("id")
		_, e := s.app.store.task(id)
		if e != nil {
			fail(w, 404, "任务不存在")
			return
		}
		if v.Title == nil && v.Model == nil && v.ReasoningEffort == nil {
			fail(w, 400, "没有需要修改的内容")
			return
		}
		if v.Model != nil || v.ReasoningEffort != nil {
			updated, err := s.app.updateTaskModel(id, v.Title, v.Model, v.ReasoningEffort)
			if err != nil {
				fail(w, 409, err.Error())
				return
			}
			jsonOut(w, 200, updated)
			return
		}
		if v.Title != nil {
			title := strings.TrimSpace(*v.Title)
			if title == "" || len([]rune(title)) > 180 {
				fail(w, 400, "任务名称无效")
				return
			}
			if _, e = s.app.store.Exec("UPDATE tasks SET title=?,updated=? WHERE id=?", title, now(), id); e != nil {
				fail(w, 500, e.Error())
				return
			}
		}
		s.app.changed()
		if updated, e := s.app.store.task(id); e == nil {
			jsonOut(w, 200, updated)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/tasks/{id}/messages", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Content string
			SubmitOptions
		}
		if !body(w, r, &v) {
			return
		}
		run, e := s.app.submitWithOptions(r.PathValue("id"), v.Content, "chat", "web", v.SubmitOptions)
		if e != nil {
			status := http.StatusBadRequest
			if errors.Is(e, errLiveTurnChanged) {
				status = http.StatusConflict
			}
			fail(w, status, e.Error())
			return
		}
		jsonOut(w, 202, run)
	}))
	m.HandleFunc("POST /api/tasks/{id}/stop", s.secure(func(w http.ResponseWriter, r *http.Request) {
		if e := s.app.stop(r.PathValue("id")); e != nil {
			fail(w, 404, "任务不存在")
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/tasks/{id}/summarize", s.secure(func(w http.ResponseWriter, r *http.Request) {
		v, e := s.app.knowledge(r.PathValue("id"), "web")
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		jsonOut(w, 202, v)
	}))
	m.HandleFunc("POST /api/tasks/{id}/session/reset", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, e := s.app.resetSession(r.PathValue("id"))
		if e != nil {
			switch {
			case errors.Is(e, sql.ErrNoRows):
				fail(w, http.StatusNotFound, "任务不存在")
			case errors.Is(e, errSessionResetBlocked):
				fail(w, http.StatusConflict, e.Error())
			default:
				fail(w, http.StatusInternalServerError, e.Error())
			}
			return
		}
		jsonOut(w, http.StatusOK, task)
	}))
	m.HandleFunc("GET /api/tasks/{id}/context", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, e := s.app.store.task(r.PathValue("id"))
		if e != nil {
			fail(w, 404, "任务不存在")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		jsonOut(w, 200, map[string]any{"files": externalContextFiles(ctx, task)})
	}))
	// Preview the portable context that can be handed to another engine. This
	// endpoint is intentionally read-only: it never invokes a model or submits
	// the snapshot, so the user can inspect the redacted text first.
	m.HandleFunc("GET /api/tasks/{id}/continuation/preview", s.secure(func(w http.ResponseWriter, r *http.Request) {
		preview, e := s.app.continuationPreview(r.PathValue("id"), r.URL.Query().Get("mode"))
		if e != nil {
			fail(w, http.StatusNotFound, "任务不存在")
			return
		}
		jsonOut(w, http.StatusOK, preview)
	}))
	m.HandleFunc("GET /api/tasks/{id}/note", s.secure(s.note))
	m.HandleFunc("PUT /api/tasks/{id}/note", s.secure(s.note))
	m.HandleFunc("POST /api/environments/discover", s.secure(s.detectEnvironments))
	m.HandleFunc("GET /api/settings", s.secure(s.settings))
	m.HandleFunc("POST /api/data/switch", s.secure(s.switchDataDirectory))
	m.HandleFunc("GET /api/environments/{id}/models", s.secure(func(w http.ResponseWriter, r *http.Request) {
		env, e := s.app.config.get().environment(r.PathValue("id"))
		if e != nil {
			fail(w, 404, e.Error())
			return
		}
		engine := r.URL.Query().Get("engine")
		if engine == "" {
			engine = "codex"
		}
		if !validEngine(engine) {
			fail(w, http.StatusBadRequest, "AI 工具无效")
			return
		}
		if workspace := r.URL.Query().Get("workspace"); workspace != "" {
			workspace, err := probeWorkspace(env, workspace)
			if err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			env.Workspaces = []string{workspace}
		}
		// Model metadata creates a short-lived native CLI process. Admit it
		// atomically with model probes and maintenance, without paid generation.
		if !s.admitModelCatalog(r.Context()) {
			fail(w, http.StatusConflict, "已有模型读取、测试或更新正在进行，请稍后重试")
			return
		}
		defer s.modelProbeMu.Unlock()
		if s.app.updating.Load() {
			fail(w, http.StatusConflict, errUpdateBusy.Error())
			return
		}
		profileEnv := s.app.activeEngineEnvironment(Task{Engine: engine, Environment: &env})
		if taskID := r.URL.Query().Get("task_id"); taskID != "" {
			task, err := s.app.store.task(taskID)
			if err != nil || task.Environment == nil || task.Environment.ID != env.ID || task.Engine != engine {
				fail(w, http.StatusBadRequest, "任务与模型目录目标不匹配")
				return
			}
			if task.Binding == nil && task.Session != "" {
				fail(w, http.StatusConflict, "旧会话未记录账号，请先通过切换 AI 确认配置")
				return
			}
			env = *task.Environment
			env.Workspaces = []string{task.Workspace}
			task, err = s.app.store.taskAccountForLookup(task)
			if err != nil {
				fail(w, http.StatusConflict, err.Error())
				return
			}
			profileEnv = s.app.activeEngineEnvironment(task)
		} else if r.URL.Query().Has("profile_id") {
			profile, err := s.app.store.selectedEngineProfile(env.ID, engine, r.URL.Query().Get("profile_id"))
			if err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			profileEnv = nil
			if profile != nil {
				profileEnv = engineProfileEnv(*profile)
			}
		}
		models, e := modelsForEngine(r.Context(), env, engine, profileEnv)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		jsonOut(w, 200, models)
	}))
	m.HandleFunc("POST /api/environments/{id}/models/test", s.secure(s.testModels))
	m.HandleFunc("PUT /api/settings", s.secure(s.settings))
	m.HandleFunc("POST /api/check", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			EnvironmentID string `json:"environment_id"`
			Engine        string `json:"engine"`
		}
		if !body(w, r, &v) {
			return
		}
		c := s.app.config.get()
		env, err := c.environment(v.EnvironmentID)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if v.Engine == "" {
			v.Engine = "codex"
		}
		if !validEngine(v.Engine) {
			fail(w, 400, "AI 工具无效")
			return
		}
		runtime := runtimeConfig(c, env)
		runtime.EngineEnv = s.app.activeEngineEnvironment(Task{Engine: v.Engine, Environment: &env})
		output, e := checkEngine(runtime, v.Engine)
		if e != nil && output == "" {
			output = e.Error()
		}
		jsonOut(w, 200, map[string]any{"ok": e == nil, "output": output})
	}))
	m.HandleFunc("POST /api/feishu/pair", s.secure(func(w http.ResponseWriter, r *http.Request) {
		code := s.app.feishu.pairCode()
		jsonOut(w, 200, map[string]string{"code": code})
	}))
	m.HandleFunc("POST /api/tasks/{id}/bind", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct{ Chat string }
		if !body(w, r, &v) {
			return
		}
		owner := s.app.config.get().Feishu.Owner
		if owner == "" {
			fail(w, 400, "请先配对飞书账号")
			return
		}
		var known int
		_ = s.app.store.QueryRow("SELECT count(*) FROM settings WHERE key='feishu_chat' AND value=?", v.Chat).Scan(&known)
		if known != 1 {
			fail(w, 400, "请选择已配对的飞书私聊")
			return
		}
		if e := s.app.bind(v.Chat, r.PathValue("id")); e != nil {
			fail(w, 400, e.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("DELETE /api/tasks/{id}/bind", s.secure(func(w http.ResponseWriter, r *http.Request) {
		_, e := s.app.store.Exec("DELETE FROM bindings WHERE task_id=?", r.PathValue("id"))
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	s.setupRoutes(m)
	web, _ := fs.Sub(assets, "web")
	m.Handle("GET /", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/mcp/hardware" {
			origin := r.Header.Get("Origin")
			u, e := url.Parse(origin)
			if e != nil || origin == "" || !strings.EqualFold(u.Host, r.Host) {
				fail(w, 403, "不允许跨站请求")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	a := s.attempts[ip]
	if time.Since(a.since) > 5*time.Minute {
		a = attempts{since: time.Now()}
	}
	a.n++
	s.attempts[ip] = a
	blocked := a.n > 10
	if len(s.attempts) > 1000 {
		for k, v := range s.attempts {
			if time.Since(v.since) > 5*time.Minute {
				delete(s.attempts, k)
			}
		}
	}
	s.mu.Unlock()
	if blocked {
		fail(w, 429, "尝试次数过多，请五分钟后再试")
		return
	}
	var v struct{ Password string }
	if !body(w, r, &v) {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.app.store.setting("password_hash")), []byte(v.Password)) != nil {
		fail(w, 401, "密码不正确")
		return
	}
	token, csrf := uid()+uid(), uid()
	_, e := s.app.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash(token), csrf, time.Now().Add(7*24*time.Hour).UnixMilli())
	if e != nil {
		fail(w, 500, "登录失败")
		return
	}
	_, _ = s.app.store.Exec("DELETE FROM sessions WHERE expires<?", now())
	s.mu.Lock()
	delete(s.attempts, ip)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "jianzuo_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 7 * 86400})
	jsonOut(w, 200, map[string]string{"csrf": csrf})
}
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, e := s.app.store.task(id)
	if e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if r.URL.Query().Has("event") {
		seq, err := conversationCursor(r.URL.Query(), "event")
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		var event Event
		err = s.app.store.QueryRow("SELECT seq,run_id,kind,text,created FROM events WHERE task_id=? AND seq=?", id, seq).Scan(&event.Seq, &event.RunID, &event.Kind, &event.Text, &event.Created)
		if err != nil {
			fail(w, 404, "记录不存在")
			return
		}
		jsonOut(w, 200, event)
		return
	}
	var runs []Run
	var events []Event
	var window conversationWindow
	if r.URL.Query().Get("recent") == "1" {
		runs, events, window, e = s.app.store.conversationPage(r.Context(), id, r.URL.Query())
		if errors.Is(e, errConversationCursor) {
			fail(w, 400, e.Error())
			return
		}
		if errors.Is(e, sql.ErrNoRows) {
			fail(w, 404, "历史位置不存在")
			return
		}
	} else {
		runs, e = s.app.store.runs(id)
		if e == nil {
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			events, e = s.app.store.events(id, after)
		}
	}
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	var chat string
	_ = s.app.store.QueryRow("SELECT chat_id FROM bindings WHERE task_id=?", id).Scan(&chat)
	// The task view shows how long the engine has been carrying this session
	// and which foreign instruction files sit in the working directory, so a
	// reply that follows old context is explainable instead of surprising.
	response := map[string]any{"task": t, "runs": runs, "events": events, "chat": chat, "session_started": sessionStarted(t.Session), "approvals": s.app.codexRequests.list(id), "interaction": s.app.liveInteraction(id)}
	questions, err := s.app.store.pendingAsyncQuestions(t)
	if err != nil {
		fail(w, 500, "读取待回答问题失败")
		return
	}
	response["approvals"] = append(s.app.codexRequests.list(id), questions...)
	response["desktop"] = s.app.desktop.status()
	if r.URL.Query().Get("recent") == "1" {
		response["conversation"] = window
	}
	if continuation, err := s.app.store.taskContinuation(id); err == nil {
		response["continuation"] = continuation
	}
	if runtime := harnessRuntimeStatus(t); runtime != nil {
		response["runtime"] = runtime
	}
	jsonOut(w, 200, response)
}
func (s *Server) note(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := s.app.store.task(id); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if r.Method == "PUT" {
		var v Note
		if !body(w, r, &v) {
			return
		}
		if v.Revision < 0 || len(v.Content) > 500000 {
			fail(w, 400, "知识内容过长或版本无效")
			return
		}
		n, e := s.app.store.saveNote(id, v.Content, v.Revision)
		if errors.Is(e, errConflict) {
			fail(w, 409, e.Error())
			return
		}
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		jsonOut(w, 200, n)
		return
	}
	n, e := s.app.store.note(id)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="task-knowledge.md"`)
		_, _ = w.Write([]byte(n.Content))
		return
	}
	jsonOut(w, 200, n)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.app.feishu.setupMu.Lock()
	defer s.app.feishu.setupMu.Unlock()
	c := s.app.config.get()
	if r.Method == "PUT" {
		if s.app.feishu.setupBusy() {
			fail(w, 409, "扫码创建正在进行，请先完成或取消再保存设置")
			return
		}

		var v Config
		if !body(w, r, &v) {
			return
		}
		v.Listen = c.Listen
		if v.Feishu.Secret == "" {
			v.Feishu.Secret = c.Feishu.Secret
		}
		v.Feishu.Owner = c.Feishu.Owner
		if v.Feishu.AppID != c.Feishu.AppID {
			v.Feishu.Owner = ""
		}
		if e := s.app.config.save(v); e != nil {
			fail(w, 400, e.Error())
			return
		}
		if v.Feishu.AppID != c.Feishu.AppID {
			_, _ = s.app.store.Exec("DELETE FROM bindings")
			_, _ = s.app.store.Exec("UPDATE outbox SET status='cancelled' WHERE status='pending'")
			_, _ = s.app.store.Exec("UPDATE feishu_run_cards SET state='cancelled' WHERE state IN ('pending','fallback')")
			_ = s.app.store.set("feishu_chat", "")
		}
		if v.Feishu != c.Feishu {
			s.app.feishu.restart()
		}
		c = s.app.config.get()
	}
	secret := c.Feishu.Secret != ""
	c.Feishu.Secret = ""
	jsonOut(w, 200, map[string]any{"config": c, "data_dir": s.dataDir, "data_switch_supported": s.launcherPID > 0, "secret_configured": secret, "feishu_status": s.app.feishu.status(), "chat": s.app.store.setting("feishu_chat")})
}
