//go:build windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWindowsTerminalInputResizeInterruptAndClose(t *testing.T) {
	dir := t.TempDir()
	p, err := startTaskTerminal(Task{Workspace: dir, Environment: &Environment{Type: "windows"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Write([]byte("Write-Output ('PTY_'+'READY'); [Console]::WriteLine((Get-Location).Path)\r"))
	waitTerminalText(t, p, "PTY_READY")
	if err = p.Resize(83, 27); err != nil {
		t.Fatal(err)
	}
	p.Write([]byte("[Console]::WriteLine(('SIZE_'+[Console]::WindowWidth)); Write-Output ('中文'+'成功')\r"))
	output := waitTerminalText(t, p, "中文成功")
	if !strings.Contains(output, "SIZE_83") {
		t.Fatalf("resize did not reach process: %s", output)
	}
	p.Write([]byte("Write-Output ('WAIT_'+'START'); Start-Sleep -Seconds 60\r"))
	waitTerminalText(t, p, "WAIT_START")
	p.Write([]byte{3})
	waitTerminalText(t, p, "PS ")
	p.Write([]byte("Write-Output ('AFTER_'+'INTERRUPT')\r"))
	waitTerminalText(t, p, "AFTER_INTERRUPT")
	p.Write([]byte("exit 7\r"))
	for {
		select {
		case _, ok := <-p.Output():
			if !ok {
				if code := <-p.Exit(); code != 7 {
					t.Fatalf("exit=%d", code)
				}
				return
			}
		case <-time.After(8 * time.Second):
			t.Fatal("terminal exit hung")
		}
	}
}

func TestTerminalSocketRoundTripAndCleanup(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	e := Environment{ID: "terminal-windows", Name: "Test", Type: "windows", Codex: "codex.exe", Workspaces: []string{t.TempDir()}}
	cfg := a.config.get()
	cfg.Environments = append(cfg.Environments, e)
	if err := a.config.save(cfg); err != nil {
		t.Fatal(err)
	}
	task, err := a.create("Terminal test", e.Workspaces[0], "", e.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("terminal-test"), "csrf", now()+60000)
	srv := httptest.NewServer((&Server{app: a}).Handler())
	defer srv.Close()
	request, _ := http.NewRequest("POST", srv.URL+"/api/tasks/"+task.ID+"/terminal", strings.NewReader("{}"))
	request.Header.Set("Origin", srv.URL)
	request.Header.Set("X-CSRF-Token", "csrf")
	request.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "terminal-test"})
	res, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct{ Ticket string }
	json.NewDecoder(res.Body).Decode(&ticket)
	res.Body.Close()
	if ticket.Ticket == "" {
		t.Fatal("no ticket")
	}
	h := http.Header{"Origin": []string{srv.URL}, "Cookie": []string{"jianzuo_session=terminal-test"}}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/" + ticket.Ticket
	ws, _, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, data, err := ws.ReadMessage()
	if err != nil || !strings.Contains(string(data), "ready") {
		t.Fatalf("not ready: %s %v", data, err)
	}
	if other, _, err := websocket.DefaultDialer.Dial(url, h); err == nil {
		other.Close()
		t.Fatal("ticket replay accepted")
	}
	archived := true
	if _, err := a.setTaskPreferences(task.ID, TaskPreferences{Archived: &archived}); err == nil {
		t.Fatal("archived a task with a live terminal")
	}
	ws.WriteJSON(map[string]any{"type": "resize", "cols": 91, "rows": 28})
	ws.WriteMessage(websocket.BinaryMessage, []byte("Write-Output ('SOCKET_'+'OK')\r"))
	var out strings.Builder
	for !strings.Contains(out.String(), "SOCKET_OK") {
		kind, b, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			out.Write(b)
			if strings.Contains(string(b), "\x1b[6n") {
				ws.WriteMessage(websocket.BinaryMessage, []byte("\x1b[1;1R"))
			}
		}
	}
	ws.Close()
	waitUntil(t, func() bool { a.terminals.mu.Lock(); defer a.terminals.mu.Unlock(); return len(a.terminals.links) == 0 })
	if _, err := a.setTaskPreferences(task.ID, TaskPreferences{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStartupFailureReleasesHandles(t *testing.T) {
	_, err := startWindowsPTY([]string{filepath.Join(t.TempDir(), "missing.exe")}, "")
	if err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestWSLTerminalOptIn(t *testing.T) {
	if os.Getenv("JIANZUO_TEST_WSL") == "" {
		t.Skip("set JIANZUO_TEST_WSL to a test distro")
	}
	p, err := startTaskTerminal(Task{Workspace: "/tmp", Environment: &Environment{Type: "wsl", Distro: os.Getenv("JIANZUO_TEST_WSL"), User: "dev"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Write([]byte("printf 'WSL_%s\\n' READY; pwd; tty\r"))
	waitTerminalText(t, p, "WSL_READY")
	if err = p.Resize(87, 29); err != nil {
		t.Fatal(err)
	}
	p.Write([]byte("stty size; printf '中文%s\\n' 成功\r"))
	out := waitTerminalText(t, p, "中文成功")
	if !strings.Contains(out, "29 87") {
		t.Fatalf("WSL resize: %s", out)
	}
	p.Write([]byte("printf 'WAIT_%s\\n' START; sleep 60\r"))
	waitTerminalText(t, p, "WAIT_START")
	p.Write([]byte{3})
	p.Write([]byte("printf 'AFTER_%s\\n' INTERRUPT\r"))
	waitTerminalText(t, p, "AFTER_INTERRUPT")

	p.Write([]byte("sleep 300 & printf 'CHILD:%s:END\\n' \"$!\"; printf 'PID_%s\\n' READY\r"))
	childOutput := waitTerminalText(t, p, "PID_READY")
	match := regexp.MustCompile(`CHILD:([0-9]+):END`).FindStringSubmatch(childOutput)
	if len(match) != 2 {
		t.Fatalf("no child pid: %s", childOutput)
	}
	childPID := match[1]
	p.Close()
	select {
	case <-p.Exit():
	case <-time.After(10 * time.Second):

		t.Fatal("WSL terminal cleanup hung")
	}
	check := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "wsl.exe"), "-d", os.Getenv("JIANZUO_TEST_WSL"), "-u", "dev", "--exec", "/bin/ps", "-p", childPID, "-o", "stat=")
	hideCommand(check)
	state, _ := check.Output()
	if text := strings.TrimSpace(string(state)); text != "" && !strings.HasPrefix(text, "Z") {
		t.Fatalf("WSL child remains after closing terminal: %s", text)
	}
}
