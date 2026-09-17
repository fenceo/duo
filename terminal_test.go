package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTerminalCommandsAndOrigin(t *testing.T) {
	workspace := "/tmp/a'b; echo unsafe"
	args, dir, err := terminalArgs(Task{Workspace: workspace, Environment: &Environment{Type: "ssh", Host: "host", User: "dev", Port: 2222, Identity: "C:\\keys\\a b"}})
	if err != nil || dir != "" || args[len(args)-1] != `cd -- '/tmp/a'"'"'b; echo unsafe' && export TERM=xterm-256color && exec "${SHELL:-/bin/sh}" -i` {
		t.Fatalf("SSH quoting: %#v %v", args, err)
	}
	if !strings.Contains(strings.Join(args, "|"), "StrictHostKeyChecking=yes") || !strings.Contains(strings.Join(args, "|"), "|--|dev@host|") {
		t.Fatal(args)
	}
	args, _, err = terminalArgs(Task{Workspace: workspace, Environment: &Environment{Type: "wsl", Distro: "Ubuntu-22.04", User: "dev"}})
	if err != nil || args[6] != workspace {
		t.Fatalf("WSL workspace not a separate argument: %#v %v", args, err)
	}
	for _, tc := range []struct {
		origin       string
		secure, want bool
	}{
		{"http://localhost:8789", false, true}, {"https://localhost:8789", false, false}, {"http://evil.invalid", false, false}, {"", false, false}, {"null", false, false}, {"http://localhost:8789/path", false, false}, {"https://localhost:8789", true, true},
	} {
		r := httptest.NewRequest("GET", "http://localhost:8789/api/terminal/x", nil)
		if tc.secure {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Origin", tc.origin)
		if terminalOrigin(r) != tc.want {
			t.Errorf("origin %q accepted=%v", tc.origin, !tc.want)
		}
	}
}

func TestTerminalEnvironmentOverrideLeavesTaskUnchanged(t *testing.T) {
	original := Task{ID: "task", Workspace: "/home/dev/work", Environment: &Environment{ID: "wsl", Type: "wsl", Name: "WSL", Distro: "Ubuntu", Workspaces: []string{"/home/dev/work"}}}
	c := Config{Environments: []Environment{{ID: "board", Type: "ssh", Name: "板卡", Host: "192.168.50.2", User: "root", Port: 22, Workspaces: []string{"/root"}}}}
	selected, err := terminalExecution(original, c, "board", "/tmp/a'b")
	if err != nil || selected.Environment.ID != "board" || selected.Workspace != "/tmp/a'b" {
		t.Fatal(selected, err)
	}
	if original.Environment.ID != "wsl" || original.Workspace != "/home/dev/work" {
		t.Fatal("task was mutated")
	}
	selected, err = terminalExecution(original, c, "", "")
	if err != nil || selected.Environment.ID != "wsl" {
		t.Fatal(selected, err)
	}
	for _, v := range []struct{ env, path string }{{"missing", "/tmp"}, {"board", "relative"}, {"board", "/tmp\ncommand"}} {
		if _, err = terminalExecution(original, c, v.env, v.path); err == nil {
			t.Fatal("invalid terminal target accepted", v)
		}
	}
}

// Socket authentication must be checked before any OS process is launched.
func TestTerminalTicketSecurity(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	srv := httptest.NewServer((&Server{app: a}).Handler())
	defer srv.Close()
	for _, owner := range []string{"one", "two"} {
		if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash(owner), "csrf-"+owner, now()+60000); err != nil {
			t.Fatal(err)
		}
	}
	request := func(owner, csrf, id string, want int) string {
		t.Helper()
		r, _ := http.NewRequest("POST", srv.URL+"/api/tasks/"+id+"/terminal", strings.NewReader("{}"))
		r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: owner})
		r.Header.Set("Origin", srv.URL)
		r.Header.Set("X-CSRF-Token", csrf)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("ticket status %d, want %d", resp.StatusCode, want)
		}
		var data map[string]string
		json.NewDecoder(resp.Body).Decode(&data)
		return data["ticket"]
	}
	request("", "", task.ID, 401)
	request("one", "bad", task.ID, 403)
	request("one", "csrf-one", "missing", 404)
	ticket := request("one", "csrf-one", task.ID, 201)
	for _, test := range []struct{ owner, origin string }{{"two", srv.URL}, {"one", "http://evil.invalid"}, {"one", ""}} {
		h := http.Header{"Origin": []string{test.origin}, "Cookie": []string{"jianzuo_session=" + test.owner}}
		c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/terminal/"+ticket, h)
		if err == nil {
			c.Close()
			t.Fatal("unauthorized socket connected")
		}
		if resp == nil || resp.StatusCode != 403 {
			t.Fatalf("unexpected socket response: %v %v", resp, err)
		}
		resp.Body.Close()
	}
	a.terminals.mu.Lock()
	if len(a.terminals.links) != 0 || len(a.terminals.tickets) != 1 {
		t.Fatal("rejected socket consumed ticket or launched process")
	}
	old := a.terminals.tickets[ticket]
	old.Expires = time.Now().Add(-time.Minute)
	a.terminals.tickets[ticket] = old
	a.terminals.mu.Unlock()
	h := http.Header{"Origin": []string{srv.URL}, "Cookie": []string{"jianzuo_session=one"}}
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/terminal/"+ticket, h)
	if err == nil || resp.StatusCode != 403 {
		t.Fatal("expired ticket accepted")
	}
	resp.Body.Close()
	a.terminals.closeOwner("one")
	archived := true
	if _, err := a.setTaskPreferences(task.ID, TaskPreferences{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	request("one", "csrf-one", task.ID, 409)
}

func waitTerminalText(t *testing.T, p taskPTY, marker string) string {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	var buf bytes.Buffer
	for {
		select {
		case chunk, ok := <-p.Output():
			if !ok {
				t.Fatalf("terminal exited before %q: %s", marker, buf.String())
			}
			buf.Write(chunk)
			if bytes.Contains(chunk, []byte("\x1b[6n")) {
				p.Write([]byte("\x1b[1;1R"))
			}
			if strings.Contains(buf.String(), marker) {
				return buf.String()
			}
		case <-deadline.C:
			t.Fatalf("terminal timed out waiting for %q: %s", marker, buf.String())
		}
	}
}
