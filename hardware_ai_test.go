package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClaudeHardwareStartupAndAuthProgress(t *testing.T) {
	var stream claudeStream
	var events []string
	emit := func(kind, text string) { events = append(events, kind+":"+text) }
	stream.consume(`{"type":"system","subtype":"init","mcp_servers":[{"name":"jianzuo_hardware","status":"connected"}]}`, emit)
	stream.consume(`{"type":"system","subtype":"api_retry","attempt":1,"error_status":401,"error":"authentication_failed"}`, emit)
	if len(events) != 2 || !strings.Contains(events[0], "已连接任务硬件") || !strings.Contains(events[1], "认证或访问权限被拒绝") || stream.finished {
		t.Fatal(events, stream)
	}
	stream.consume(`{"type":"system","subtype":"init","mcp_servers":[{"name":"jianzuo_hardware","status":"failed"}]}`, emit)
	if !strings.Contains(events[2], "未连接任务硬件") {
		t.Fatal(events)
	}
}

func TestHardwareMCPAuthPermissionsAndRevocation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, two := taskFor(t, a), taskFor(t, a)
	admin := toolsClient(t, a)
	var c HardwareConfig
	json.Unmarshal(admin("/api/tasks/"+one.ID+"/hardware", "POST", HardwareConfig{Name: "fixture", Protocol: "serial", Device: "COM999", Baud: 115200}, 200), &c)
	path := "/api/tasks/" + one.ID + "/hardware/" + c.ID
	var g map[string]HardwareGrant
	json.Unmarshal(admin("/api/tasks/"+one.ID+"/hardware-ai", "GET", nil, 200), &g)
	if len(g) != 0 {
		t.Fatal("permissions enabled by default")
	}
	admin(path+"/ai", "PUT", HardwareGrant{Read: true}, 200)
	admin("/api/tasks/"+two.ID+"/hardware/"+c.ID+"/ai", "PUT", HardwareGrant{Read: true}, 404)
	runtime, release, err := a.prepareHardwareAI(context.Background(), one, "run-ai", Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	srv := httptest.NewServer((&Server{app: a}).Handler())
	defer srv.Close()
	rpc := func(token, origin, method string, id int, p any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": p})
		r, _ := http.NewRequest("POST", srv.URL+"/mcp/hardware", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var v map[string]any
		json.NewDecoder(res.Body).Decode(&v)
		if res.StatusCode != want {
			t.Fatal(res.StatusCode, v)
		}
		return v
	}
	rpc("bad", "", "tools/list", 1, nil, 401)
	rpc(runtime.Token, "https://untrusted.invalid", "tools/list", 1, nil, 403)
	init := rpc(runtime.Token, "", "initialize", 1, map[string]any{"protocolVersion": "2025-06-18"}, 200)
	if init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatal(init)
	}
	raw, _ := json.Marshal(rpc(runtime.Token, "", "tools/list", 2, nil, 200))
	if bytes.Contains(raw, []byte(`"hardware_send"`)) || bytes.Contains(raw, []byte(`"relay_power"`)) {
		t.Fatal("ungranted write tools advertised")
	}
	params := func(name, id string) map[string]any {
		return map[string]any{"name": name, "arguments": map[string]any{"device_id": id}}
	}
	denied := rpc(runtime.Token, "", "tools/call", 3, params("hardware_send", c.ID), 200)
	if denied["result"].(map[string]any)["isError"] != true {
		t.Fatal(denied)
	}
	list := rpc(runtime.Token, "", "tools/call", 4, map[string]any{"name": "hardware_devices", "arguments": map[string]any{}}, 200)
	raw, _ = json.Marshal(list)
	if !bytes.Contains(raw, []byte(c.ID)) {
		t.Fatal(list)
	}
	if rpc(runtime.Token, "", "tools/call", 4, params("hardware_read", c.ID), 200)["error"] == nil {
		t.Fatal("duplicate request accepted")
	}
	// New permissions do not silently widen the already running lease.
	admin(path+"/ai", "PUT", HardwareGrant{Read: true, Write: true}, 200)
	lease := a.hardwareAI.lease(runtime.Token)
	_, effective, e := a.aiHardware(lease, c.ID)
	if e != nil || effective.Write {
		t.Fatal(e, effective)
	}
	admin(path+"/ai", "PUT", HardwareGrant{}, 200)
	if lease.ctx.Err() == nil {
		t.Fatal("revocation did not cancel in-flight work")
	}
	rpc(runtime.Token, "", "tools/list", 5, nil, 401)
	// Removing a task binding also removes its persisted grant.
	admin(path, "DELETE", map[string]any{}, 200)
	grants, e := a.store.hardwareGrants(one.ID)
	if e != nil || len(grants) != 0 {
		t.Fatal(grants, e)
	}
}

func TestHardwareMCPSerialIOAndOwnership(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, two := taskFor(t, a), taskFor(t, a)
	admin := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := make(chan []byte, 4)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		for {
			b := make([]byte, 1024)
			n, e := c.Read(b)
			if n > 0 {
				wire <- append([]byte{}, b[:n]...)
				c.Write([]byte("ready 登录提示\n"))
			}
			if e != nil {
				return
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	var c HardwareConfig
	json.Unmarshal(admin("/api/tasks/"+one.ID+"/hardware", "POST", HardwareConfig{Name: "模拟串口", Protocol: "tcp", Host: host, Port: p}, 200), &c)
	path := "/api/tasks/" + one.ID + "/hardware/" + c.ID
	admin(path+"/ai", "PUT", HardwareGrant{Read: true, Write: true}, 200)
	token, release := a.hardwareAI.issue(context.Background(), one.ID, "r", map[string]HardwareGrant{c.ID: {Read: true, Write: true}})
	defer release()
	lease := a.hardwareAI.lease(token)
	call := func(name string, args map[string]any) (any, error) {
		b, _ := json.Marshal(args)
		return a.callHardwareTool(context.Background(), lease, name, b)
	}
	if _, err = call("hardware_connect", map[string]any{"device_id": c.ID}); err != nil {
		t.Fatal(err)
	}
	sent, err := call("hardware_send", map[string]any{"device_id": c.ID, "data": "uname -a", "newline": "cr"})
	if err != nil {
		t.Fatal(err)
	}
	if b := <-wire; string(b) != "uname -a\r" {
		t.Fatalf("wrong bytes %q", b)
	}
	result, err := call("hardware_read", map[string]any{"device_id": c.ID, "after": sent.(map[string]any)["cursor"], "contains": "登录提示", "wait_seconds": 2})
	if err != nil || result.(map[string]any)["matched"] != true {
		t.Fatal(result, err)
	}
	_, generation, _, _ := a.hardware.connectionStatus(c.ID)
	if err = a.hardware.control(c.ID, two.ID, generation, true); err != nil {
		t.Fatal(err)
	}
	if _, err = call("hardware_send", map[string]any{"device_id": c.ID, "data": "reboot"}); err == nil {
		t.Fatal("AI stole another task's device")
	}
	if _, err = call("hardware_connect", map[string]any{"device_id": c.ID}); err != nil {
		t.Fatal(err)
	}
	_, _, owner, _ := a.hardware.connectionStatus(c.ID)
	if owner != two.ID {
		t.Fatal("connect changed owner")
	}
	select {
	case b := <-wire:
		t.Fatal("unauthorized bytes", b)
	default:
	}
}

func TestHardwareMCPRelayScopesAndCancellation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	admin := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := make(chan []byte, 8)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		coil := byte(0)
		for {
			b := make([]byte, 4)
			if _, e = io.ReadFull(c, b); e != nil {
				return
			}
			wire <- b
			if b[2] == 3 {
				coil = 1
			}
			if b[2] == 2 {
				coil = 0
			}
			c.Write(relayFrame(1, coil))
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := HardwareConfig{Name: "电源模拟", Protocol: "tcp", Host: host, Port: p, Kind: "relay", Relay: &RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1}}
	json.Unmarshal(admin("/api/tasks/"+task.ID+"/hardware", "POST", c, 200), &c)
	path := "/api/tasks/" + task.ID + "/hardware/" + c.ID
	admin(path+"/ai", "PUT", HardwareGrant{Read: true}, 200)
	token, release := a.hardwareAI.issue(context.Background(), task.ID, "r", map[string]HardwareGrant{c.ID: {Read: true, Power: true}})
	defer release()
	lease := a.hardwareAI.lease(token)
	call := func(ctx context.Context, name, action string) (any, error) {
		b, _ := json.Marshal(map[string]any{"device_id": c.ID, "action": action})
		return a.callHardwareTool(ctx, lease, name, b)
	}
	if _, err = call(context.Background(), "hardware_connect", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = call(context.Background(), "relay_power", "on"); err == nil {
		t.Fatal("ungranted power")
	}
	if _, err = call(context.Background(), "relay_query", ""); err != nil {
		t.Fatal(err)
	}
	if b := <-wire; !bytes.Equal(b, relayFrame(1, 5)) {
		t.Fatal(b)
	}
	admin(path+"/ai", "PUT", HardwareGrant{Read: true, Power: true}, 200)
	ctx, cancel := context.WithCancel(lease.ctx)
	done := make(chan error, 1)
	go func() { _, e := call(ctx, "relay_power", "cycle"); done <- e }()
	if b := <-wire; !bytes.Equal(b, relayFrame(1, 2)) {
		t.Fatal(b)
	}
	cancel()
	if e := <-done; e == nil {
		t.Fatal("cycle ignored cancellation")
	}
	select {
	case b := <-wire:
		t.Fatal("powered on after cancellation", b)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestHardwareRunnerInjection(t *testing.T) {
	h := &HardwareRuntime{URL: "http://100.64.1.2:8789/mcp/hardware", Token: "must-not-appear-in-args"}
	for _, engine := range []string{"codex", "claude"} {
		for _, kind := range []string{"windows", "wsl", "ssh"} {
			c := Config{Codex: "codex", Claude: "claude", HardwareAI: h}
			if kind == "wsl" {
				c.Distro = "Ubuntu"
			}
			if kind == "ssh" {
				c.SSHHost = "example.local"
				c.User = "dev"
			}
			cmd, err := engineCommand(c, Task{Engine: engine, Workspace: "/tmp/project"})
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(cmd.Args, " ")
			if strings.Contains(args, h.Token) || !strings.Contains(args, "jianzuo_hardware") || !strings.Contains(args, h.URL) {
				t.Fatal(args)
			}
			if kind != "windows" && !strings.Contains(args, "Hardware MCP connection failed") {
				t.Fatal("missing remote probe")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := newHardwareAI()
	token, release := m.issue(ctx, "t", "r", map[string]HardwareGrant{})
	defer release()
	cancel()
	if m.lease(token) != nil {
		t.Fatal("stopped run token valid")
	}
	cfg := Config{Distro: "Ubuntu", Access: AccessConfig{LAN: "http://192.168.1.2:8789", Tailscale: "http://100.64.1.2:8789"}}
	endpoint, err := hardwareEndpoint(cfg, "0.0.0.0:8789")
	if err != nil || endpoint != "http://127.0.0.1:8789/mcp/hardware" {
		t.Fatal(endpoint, err)
	}
	if endpoint, err = hardwareEndpoint(Config{Distro: "Ubuntu"}, "0.0.0.0:9999"); err != nil || endpoint != "http://127.0.0.1:9999/mcp/hardware" {
		t.Fatal(endpoint, err)
	}
	cfg.SSHHost = "remote"
	endpoint, err = hardwareEndpoint(cfg, "0.0.0.0:8789")
	if err != nil || !strings.Contains(endpoint, "100.64.1.2") {
		t.Fatal(endpoint, err)
	}
}

func TestHardwareWSLRouteCandidates(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	admin := toolsClient(t, a)
	var device HardwareConfig
	json.Unmarshal(admin("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "route", Protocol: "serial", Device: "COM999", Baud: 115200}, 200), &device)
	admin("/api/tasks/"+task.ID+"/hardware/"+device.ID+"/ai", "PUT", HardwareGrant{Read: true}, 200)
	a.hardwareAddress = "0.0.0.0:8888"
	cfg := Config{Distro: "Ubuntu", Access: AccessConfig{LAN: "http://192.168.1.2:8888/", Tailscale: "http://100.64.1.2:8888"}}
	h, release, err := a.prepareHardwareAI(context.Background(), task, "routes", cfg)
	defer release()
	if err != nil || h.URL != "http://127.0.0.1:8888/mcp/hardware" || len(h.FallbackURLs) != 2 || h.FallbackURLs[0] != "http://192.168.1.2:8888/mcp/hardware" || h.FallbackURLs[1] != "http://100.64.1.2:8888/mcp/hardware" {
		t.Fatal(h, err)
	}
	cfg.SSHHost = "remote"
	h, releaseSSH, err := a.prepareHardwareAI(context.Background(), task, "ssh-route", cfg)
	defer releaseSSH()
	if err != nil || h.URL != "http://100.64.1.2:8888/mcp/hardware" || len(h.FallbackURLs) != 0 {
		t.Fatal(h, err)
	}
}
