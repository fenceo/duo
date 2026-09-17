package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HardwareGrant struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Power bool `json:"power"`
}
type HardwareRuntime struct {
	URL, Token   string
	FallbackURLs []string
}
type hardwareLease struct {
	task, run string
	grants    map[string]HardwareGrant
	ctx       context.Context
	cancel    context.CancelFunc
	expires   time.Time
	callsMu   sync.Mutex
	calls     map[string]context.CancelFunc
	seen      map[string]bool
}
type HardwareAI struct {
	mu     sync.Mutex
	leases map[string]*hardwareLease
}

func newHardwareAI() *HardwareAI { return &HardwareAI{leases: map[string]*hardwareLease{}} }
func (m *HardwareAI) issue(ctx context.Context, task, run string, grants map[string]HardwareGrant) (string, func()) {
	ctx, cancel := context.WithCancel(ctx)
	token := uid() + uid()
	snapshot := map[string]HardwareGrant{}
	for id, g := range grants {
		snapshot[id] = g
	}
	m.mu.Lock()
	m.leases[hash(token)] = &hardwareLease{task: task, run: run, grants: snapshot, ctx: ctx, cancel: cancel, expires: time.Now().Add(24 * time.Hour), calls: map[string]context.CancelFunc{}, seen: map[string]bool{}}
	m.mu.Unlock()
	return token, func() { cancel(); m.mu.Lock(); delete(m.leases, hash(token)); m.mu.Unlock() }
}
func (m *HardwareAI) lease(token string) *hardwareLease {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.leases[hash(token)]
	if l == nil || l.ctx.Err() != nil || time.Now().After(l.expires) {
		return nil
	}
	return l
}

// The overview exposes permissions, never run credentials.
func (m *HardwareAI) activeGrants() map[string]map[string]HardwareGrant {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]map[string]HardwareGrant{}
	for _, l := range m.leases {
		if l.ctx.Err() != nil || time.Now().After(l.expires) {
			continue
		}
		grants := map[string]HardwareGrant{}
		for id, g := range l.grants {
			grants[id] = g
		}
		out[l.task] = grants
	}
	return out
}

func effectiveHardwareGrant(c HardwareConfig, live, original HardwareGrant) HardwareGrant {
	live.Read = live.Read && original.Read
	live.Write = live.Read && live.Write && original.Write && !c.ReadOnly && c.Kind != "relay"
	live.Power = live.Read && live.Power && original.Power && !c.ReadOnly && c.Kind == "relay"
	return live
}
func (s *Store) hardwareGrants(task string) (map[string]HardwareGrant, error) {
	rows, err := s.Query("SELECT hardware_id,allow_read,allow_write,allow_power FROM hardware_ai_access WHERE task_id=?", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]HardwareGrant{}
	for rows.Next() {
		var id string
		var g HardwareGrant
		if err = rows.Scan(&id, &g.Read, &g.Write, &g.Power); err != nil {
			return nil, err
		}
		out[id] = g
	}
	return out, rows.Err()
}
func hardwareEndpoint(c Config, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("无法确定硬件服务端口")
	}
	base := ""
	if c.SSHHost != "" {
		base = c.Access.Tailscale
		if base == "" {
			base = c.Access.LAN
		}
	} else if c.Distro != "" {
		// Mirrored WSL can use IPv4 localhost even when Windows interface
		// addresses refuse loopback connections from the distribution.
		base = "http://" + net.JoinHostPort("127.0.0.1", port)
	} else {
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		base = "http://" + net.JoinHostPort(host, port)
	}
	if base == "" {
		return "", errors.New("此任务已开启 AI 硬件；请在设置的访问地址中填写执行环境可达的局域网或 Tailscale 地址")
	}
	return strings.TrimRight(base, "/") + "/mcp/hardware", nil
}
func (a *App) prepareHardwareAI(ctx context.Context, t Task, run string, c Config) (*HardwareRuntime, func(), error) {
	done := func() {}
	grants, err := a.store.hardwareGrants(t.ID)
	if err != nil {
		return nil, done, err
	}
	enabled := false
	for _, g := range grants {
		enabled = enabled || g.Read
	}
	if !enabled {
		return nil, done, nil
	}
	address, err := hardwareEndpoint(c, a.hardwareAddress)
	if err != nil {
		return nil, done, err
	}
	token, release := a.hardwareAI.issue(ctx, t.ID, run, grants)
	runtime := &HardwareRuntime{URL: address, Token: token}
	if c.Distro != "" && c.SSHHost == "" {
		for _, base := range []string{c.Access.LAN, c.Access.Tailscale} {
			if base == "" {
				continue
			}
			candidate := strings.TrimRight(base, "/") + "/mcp/hardware"
			if candidate != runtime.URL && (len(runtime.FallbackURLs) == 0 || runtime.FallbackURLs[0] != candidate) {
				runtime.FallbackURLs = append(runtime.FallbackURLs, candidate)
			}
		}
	}
	return runtime, release, nil
}

// Every call intersects the run's original grant with live grants and bindings.
// Revocation applies immediately; additions require the next run.
func (a *App) aiHardware(l *hardwareLease, id string) (HardwareConfig, HardwareGrant, error) {
	var c HardwareConfig
	var raw string
	var g HardwareGrant
	if l.ctx.Err() != nil {
		return c, g, errors.New("任务已停止")
	}
	t, err := a.store.task(l.task)
	if err != nil || t.Archived {
		return c, g, errors.New("任务不可用")
	}
	err = a.store.QueryRow(`SELECT h.config,p.allow_read,p.allow_write,p.allow_power FROM hardware h JOIN task_hardware t ON t.hardware_id=h.id JOIN hardware_ai_access p ON p.task_id=t.task_id AND p.hardware_id=h.id WHERE t.task_id=? AND h.id=?`, l.task, id).Scan(&raw, &g.Read, &g.Write, &g.Power)
	if err != nil {
		return c, HardwareGrant{}, errors.New("此设备未授权给当前任务")
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, g, err
	}
	g = effectiveHardwareGrant(c, g, l.grants[id])
	if !g.Read {
		return c, g, errors.New("此设备未授权给当前任务")
	}
	return c, g, nil
}
func (s *Server) hardwareAIRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/hardware-ai", s.secure(func(w http.ResponseWriter, r *http.Request) {
		g, err := s.app.store.hardwareGrants(r.PathValue("id"))
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, g)
	}))
	m.HandleFunc("PUT /api/tasks/{id}/hardware/{hid}/ai", s.secure(func(w http.ResponseWriter, r *http.Request) {
		c, err := s.hardwareConfig(r)
		if err != nil {
			fail(w, 404, "设备不属于此任务")
			return
		}
		t, err := s.app.store.task(r.PathValue("id"))
		if err != nil || t.Archived {
			fail(w, 409, "请先恢复任务")
			return
		}
		var g HardwareGrant
		if !body(w, r, &g) {
			return
		}
		if !g.Read {
			g.Write = false
			g.Power = false
		}
		if (g.Write && c.Kind == "relay") || (g.Power && c.Kind != "relay") || (c.ReadOnly && (g.Write || g.Power)) {
			fail(w, 400, "所选权限与设备用途或只读设置不匹配")
			return
		}
		previous, _ := s.app.store.hardwareGrants(t.ID)
		_, err = s.app.store.Exec("INSERT INTO hardware_ai_access VALUES(?,?,?,?,?) ON CONFLICT(task_id,hardware_id) DO UPDATE SET allow_read=excluded.allow_read,allow_write=excluded.allow_write,allow_power=excluded.allow_power", t.ID, c.ID, g.Read, g.Write, g.Power)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		old := previous[c.ID]
		if (old.Read && !g.Read) || (old.Write && !g.Write) || (old.Power && !g.Power) {
			s.app.hardwareAI.revokeDevice(t.ID, c.ID)
		}
		s.app.hardware.recordFor(c.ID, "status", []byte("AI 权限更新：读取="+boolText(g.Read)+"，串口发送="+boolText(g.Write)+"，电源="+boolText(g.Power)), t.ID)
		jsonOut(w, 200, g)
	}))
	for _, method := range []string{"POST", "GET", "DELETE"} {
		m.HandleFunc(method+" /mcp/hardware", s.hardwareMCP)
	}
}
func boolText(v bool) string {
	if v {
		return "允许"
	}
	return "关闭"
}
func (m *HardwareAI) revokeDevice(task, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, l := range m.leases {
		if l.task == task && l.grants[id].Read {
			l.cancel()
			delete(m.leases, key)
		}
	}
}
func (l *hardwareLease) startCall(id string, cancel context.CancelFunc) bool {
	l.callsMu.Lock()
	defer l.callsMu.Unlock()
	if l.seen[id] || len(l.seen) >= 4096 {
		return false
	}
	l.seen[id] = true
	l.calls[id] = cancel
	return true
}
func (l *hardwareLease) finishCall(id string) {
	l.callsMu.Lock()
	delete(l.calls, id)
	l.callsMu.Unlock()
}
func (l *hardwareLease) cancelCall(id string) {
	l.callsMu.Lock()
	cancel := l.calls[id]
	l.callsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
