package main

import (
	"bytes"
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

func toolsClient(t *testing.T, a *App) func(string, string, any, int) []byte {
	t.Helper()
	srv := httptest.NewServer((&Server{app: a}).Handler())
	t.Cleanup(srv.Close)
	a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("tools-test"), "csrf-test", now()+60000)
	client := &http.Client{Timeout: 5 * time.Second}
	return func(path, method string, v any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "tools-test"})
		r.Header.Set("Origin", srv.URL)
		r.Header.Set("X-CSRF-Token", "csrf-test")
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, data)
		}
		return data
	}
}
func TestScratchRevisionAndTaskIsolation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	other := taskFor(t, a)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/scratch"
	var n Scratch
	json.Unmarshal(request(base, "POST", Scratch{Content: "中文便签"}, 201), &n)
	request(base+"/"+n.ID, "PUT", Scratch{Content: "新版", Revision: n.Revision}, 200)
	request(base+"/"+n.ID, "PUT", Scratch{Content: "旧版覆盖", Revision: n.Revision}, 409)
	request("/api/tasks/"+other.ID+"/scratch/"+n.ID, "DELETE", Scratch{Revision: 2}, 409)
	if !strings.Contains(string(request(base, "GET", nil, 200)), "新版") {
		t.Fatal("note lost")
	}
	request(base+"/"+n.ID, "DELETE", Scratch{Revision: 2}, 200)
	if string(bytes.TrimSpace(request(base, "GET", nil, 200))) != "[]" {
		t.Fatal("delete failed")
	}
}
func TestHardwareTCPRoundTripReadOnlyAndTaskIsolation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	other := taskFor(t, a)
	request := toolsClient(t, a)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	config := HardwareConfig{Name: "模拟板", Protocol: "tcp", Host: host, Port: p}
	base := "/api/tasks/" + task.ID + "/hardware"
	json.Unmarshal(request(base, "POST", config, 200), &config)
	path := base + "/" + config.ID
	request("/api/tasks/"+other.ID+"/hardware/"+config.ID+"/connect", "POST", map[string]string{}, 404)
	request(path+"/connect", "POST", map[string]string{}, 200)
	request(path, "PUT", config, 409)
	request(path+"/send", "POST", map[string]string{"encoding": "hex", "data": "00 ff 41"}, 200)
	deadline := time.Now().Add(3 * time.Second)
	received := false
	for time.Now().Before(deadline) {
		var state struct {
			Events []HardwareEvent `json:"events"`
		}
		json.Unmarshal(request(path+"/events", "GET", nil, 200), &state)
		for _, ev := range state.Events {
			if ev.Direction == "rx" && ev.Hex == "00ff41" {
				received = true
			}
		}
		if received {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !received {
		t.Fatal("binary RX missing")
	}
	request(path+"/disconnect", "POST", map[string]string{}, 200)
	config.ReadOnly = true
	request(path, "PUT", config, 200)
	request(path+"/connect", "POST", map[string]string{}, 200)
	request(path+"/send", "POST", map[string]string{"encoding": "text", "data": "must not send"}, 400)
	request(path+"/disconnect", "POST", map[string]string{}, 200)
	request(path, "DELETE", map[string]string{}, 200)
	request(path+"/events", "GET", nil, 404)
}
func TestHardwareAuthRequired(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	s := (&Server{app: a}).Handler()
	for _, path := range []string{"/api/hardware/ports", "/api/tasks/x/hardware", "/api/tasks/x/scratch"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("test-cookie"), "secret-csrf", now()+60000)
	r := httptest.NewRequest("POST", "/api/tasks/x/hardware/y/connect", strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "test-cookie"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
}
func TestTelnetFragmentedNegotiation(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan bool, 1)
	go func() {
		b.SetDeadline(time.Now().Add(2 * time.Second))
		b.Write([]byte{255})
		b.Write([]byte{251})
		b.Write([]byte{1})
		reply := make([]byte, 3)
		io.ReadFull(b, reply)
		done <- bytes.Equal(reply, []byte{255, 254, 1})
		b.Write([]byte{'O', 'K', 255, 255})
	}()
	ts := &telnetStream{Conn: a}
	buf := make([]byte, 10)
	n, e := ts.Read(buf)
	if e != nil || !bytes.Equal(buf[:n], []byte{'O', 'K', 255}) {
		t.Fatalf("%x %v", buf[:n], e)
	}
	if !<-done {
		t.Fatal("negotiation mismatch")
	}
}
