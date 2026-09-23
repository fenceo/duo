package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Each browser connection owns a terminal. Tickets do not start processes and
// cannot be reused by another login session. Output is never written to disk.
type terminalTicket struct {
	Owner, Task string
	Expires     time.Time
	Execution   *Task
}
type terminalLink struct {
	Owner  string
	Task   string             `json:"task_id"`
	Conn   *websocket.Conn    `json:"-"`
	Cancel context.CancelFunc `json:"-"`
}
type Terminals struct {
	mu       sync.Mutex
	tickets  map[string]terminalTicket
	links    map[string]terminalLink
	closed   bool
	updating bool
	wg       sync.WaitGroup
}

func newTerminals() *Terminals {
	return &Terminals{tickets: map[string]terminalTicket{}, links: map[string]terminalLink{}}
}
func (m *Terminals) closeOwner(owner string) {
	m.mu.Lock()
	var links []terminalLink
	for id, ticket := range m.tickets {
		if ticket.Owner == owner {
			delete(m.tickets, id)
		}
	}
	for _, link := range m.links {
		if link.Owner == owner {
			links = append(links, link)
		}
	}
	m.mu.Unlock()
	for _, link := range links {
		link.Cancel()
		link.Conn.Close()
	}
}
func (m *Terminals) close() {
	m.mu.Lock()
	m.closed = true
	var links []terminalLink
	for _, link := range m.links {
		links = append(links, link)
	}
	m.tickets = map[string]terminalTicket{}
	m.mu.Unlock()
	for _, link := range links {
		link.Cancel()
		link.Conn.Close()
	}
	m.wg.Wait()
}
func terminalOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func (s *Server) terminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/terminals", s.secure(func(w http.ResponseWriter, r *http.Request) {
		m := s.app.terminals
		m.mu.Lock()
		items := []map[string]string{}
		for _, link := range m.links {
			items = append(items, map[string]string{"task_id": link.Task})
		}
		m.mu.Unlock()
		jsonOut(w, 200, items)
	}))
	mux.HandleFunc("POST /api/tasks/{id}/terminal", s.secure(func(w http.ResponseWriter, r *http.Request) {
		t, err := s.app.store.task(r.PathValue("id"))
		if err != nil {
			fail(w, 404, "任务不存在")
			return
		}
		if t.Archived {
			fail(w, 409, "请先恢复任务，再打开终端")
			return
		}
		var options struct {
			EnvironmentID string `json:"environment_id"`
			Workspace     string `json:"workspace"`
		}
		if !body(w, r, &options) {
			return
		}
		execution, err := terminalExecution(t, s.app.config.get(), options.EnvironmentID, options.Workspace)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		owner, _ := s.identity(r)
		m := s.app.terminals
		m.mu.Lock()
		defer m.mu.Unlock()
		for id, ticket := range m.tickets {
			if time.Now().After(ticket.Expires) {
				delete(m.tickets, id)
			}
		}
		if m.updating {
			fail(w, 409, errUpdateBusy.Error())
			return
		}
		if m.closed || len(m.links) >= 8 || len(m.tickets) >= 32 {
			fail(w, 409, "终端已达上限，请先关闭不用的终端")
			return
		}
		id := uid() + uid()
		m.tickets[id] = terminalTicket{Owner: owner, Task: t.ID, Expires: time.Now().Add(time.Minute), Execution: &execution}
		jsonOut(w, 201, map[string]string{"ticket": id})
	}))
	mux.HandleFunc("GET /api/terminal/{ticket}", s.secure(s.terminalSocket))
}

func (s *Server) terminalSocket(w http.ResponseWriter, r *http.Request) {
	if !terminalOrigin(r) {
		fail(w, 403, "不允许跨站终端连接")
		return
	}
	owner, _ := s.identity(r)
	id := r.PathValue("ticket")
	m := s.app.terminals
	m.mu.Lock()
	if m.updating {
		m.mu.Unlock()
		fail(w, 409, errUpdateBusy.Error())
		return
	}
	ticket, ok := m.tickets[id]
	if !ok || ticket.Owner != owner || time.Now().After(ticket.Expires) {
		m.mu.Unlock()
		fail(w, 403, "终端连接已失效，请重新打开")
		return
	}
	delete(m.tickets, id)
	task, err := s.app.store.task(ticket.Task)
	if err != nil || task.Archived {
		m.mu.Unlock()
		fail(w, 409, "任务不存在或已归档")
		return
	}
	if m.closed || len(m.links) >= 8 {
		m.mu.Unlock()
		fail(w, 409, "终端已达上限")
		return
	}
	up := websocket.Upgrader{CheckOrigin: terminalOrigin, HandshakeTimeout: 5 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 16384}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.app.ctx)
	defer cancel()
	m.links[id] = terminalLink{Owner: owner, Task: task.ID, Conn: c, Cancel: cancel}
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { c.Close(); m.mu.Lock(); delete(m.links, id); m.mu.Unlock(); m.wg.Done() }()
	writeJSON := func(v any) error { c.SetWriteDeadline(time.Now().Add(10 * time.Second)); return c.WriteJSON(v) }
	if ticket.Execution != nil {
		task = *ticket.Execution
	}
	pty, err := startTaskTerminal(task)
	if err != nil {
		_ = writeJSON(map[string]any{"type": "error", "message": "无法打开终端：" + err.Error()})
		return
	}
	defer pty.Close()
	if writeJSON(map[string]string{"type": "ready"}) != nil {
		return
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		c.SetReadLimit(65536)
		c.SetReadDeadline(time.Now().Add(65 * time.Second))
		c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(65 * time.Second)) })
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				if _, err = pty.Write(data); err != nil {
					return
				}
			} else if kind == websocket.TextMessage {
				var v struct {
					Type       string
					Cols, Rows int
				}
				if json.Unmarshal(data, &v) != nil || v.Type != "resize" || v.Cols < 2 || v.Cols > 500 || v.Rows < 2 || v.Rows > 200 {
					return
				}
				if pty.Resize(v.Cols, v.Rows) != nil {
					return
				}
			} else {
				return
			}
		}
	}()
	// Closing the socket and process also releases a blocked read/input write.
	defer func() { c.Close(); pty.Close(); <-readDone }()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case data, ok := <-pty.Output():
			if !ok {
				code := <-pty.Exit()
				_ = writeJSON(map[string]any{"type": "exit", "code": code})
				return
			}
			c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.WriteMessage(websocket.BinaryMessage, data) != nil {
				return
			}
		case <-readDone:
			return
		case <-ctx.Done():
			return
		case <-tick.C:
			if current, _ := s.identity(r); current != owner {
				return
			}
			if c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		}
	}
}

type taskPTY interface {
	Write([]byte) (int, error)
	Resize(int, int) error
	Output() <-chan []byte
	Exit() <-chan uint32
	Close()
}

func terminalExecution(t Task, c Config, environmentID, workspace string) (Task, error) {
	if environmentID != "" {
		e, err := c.environment(environmentID)
		if err != nil {
			return t, err
		}
		t.Environment = &e
		if workspace == "" {
			workspace = e.Workspaces[0]
		}
	}
	if workspace != "" {
		t.Workspace = strings.TrimSpace(workspace)
	}
	if t.Environment == nil {
		return t, errors.New("任务没有执行环境")
	}
	valid := strings.HasPrefix(t.Workspace, "/")
	if t.Environment.Type == "windows" {
		valid = filepath.IsAbs(t.Workspace)
	}
	if !valid || len(t.Workspace) > 4096 || strings.ContainsAny(t.Workspace, "\x00\r\n") {
		return t, errors.New("请填写所选环境中的绝对工作目录")
	}
	return t, nil
}

func terminalArgs(t Task) ([]string, string, error) {
	if t.Environment == nil {
		return nil, "", errors.New("任务没有执行环境，请新建任务")
	}
	e := t.Environment
	if strings.ContainsAny(t.Workspace, "\x00\r\n") {
		return nil, "", errors.New("工作目录无效")
	}
	switch e.Type {
	case "windows":
		return []string{"powershell.exe", "-NoLogo", "-NoProfile", "-NoExit", "-Command", "Set-PSReadLineOption -HistorySaveStyle SaveNothing -ErrorAction SilentlyContinue"}, t.Workspace, nil
	case "wsl":
		args := []string{"wsl.exe", "-d", e.Distro}
		if e.User != "" {
			args = append(args, "-u", e.User)
		}
		args = append(args, "--cd", t.Workspace, "--exec", "/bin/sh", "-c", `export TERM=xterm-256color; exec "${SHELL:-/bin/sh}" -i`)
		return args, "", nil
	case "ssh":
		args := []string{"ssh.exe", "-tt", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
		if e.Port > 0 {
			args = append(args, "-p", strconv.Itoa(e.Port))
		}
		if e.Identity != "" {
			args = append(args, "-i", e.Identity)
		}
		host := e.Host
		if e.User != "" {
			host = e.User + "@" + host
		}
		args = append(args, "--", host, "cd -- "+posixQuote(t.Workspace)+` && export TERM=xterm-256color && exec "${SHELL:-/bin/sh}" -i`)
		return args, "", nil
	default:
		return nil, "", errors.New("不支持的执行环境")
	}
}
