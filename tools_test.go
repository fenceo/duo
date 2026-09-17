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
func TestScratchTodoLifecycleAndValidation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/scratch"
	// A todo needs a handle even when only details were typed.
	var made Scratch
	json.Unmarshal(request(base, "POST", Scratch{Content: "更换串口线\n再测一次", Due: "2026-09-20"}, 201), &made)
	if made.Status != "todo" || made.Title != "更换串口线" || made.Due != "2026-09-20" || made.DoneAt != 0 {
		t.Fatal(made)
	}
	request(base, "POST", Scratch{Title: "坏日期", Due: "20-09-2026"}, 400)
	request(base, "POST", Scratch{Title: strings.Repeat("长", 121)}, 400)
	request(base, "POST", Scratch{Color: "chartreuse", Content: "颜色不对"}, 400)
	// Start work, finish it, then edit the finished todo: the first completion time sticks.
	request(base+"/"+made.ID, "PUT", Scratch{Title: made.Title, Content: made.Content, Status: "doing", Due: made.Due, Revision: 1}, 200)
	list, _ := a.store.scratchList(task.ID)
	if len(list) != 1 || list[0].Status != "doing" || list[0].DoneAt != 0 {
		t.Fatal(list)
	}
	request(base+"/"+made.ID, "PUT", Scratch{Title: made.Title, Content: made.Content, Status: "done", Revision: 2}, 200)
	list, _ = a.store.scratchList(task.ID)
	if len(list) != 1 || list[0].Status != "done" || list[0].DoneAt == 0 {
		t.Fatal(list)
	}
	finished := list[0].DoneAt
	request(base+"/"+made.ID, "PUT", Scratch{Title: made.Title, Content: "补充说明", Status: "done", Due: made.Due, Revision: 3}, 200)
	list, _ = a.store.scratchList(task.ID)
	if len(list) != 1 || list[0].DoneAt != finished || list[0].Content != "补充说明" {
		t.Fatal("completion time changed on edit", list)
	}
	// Reopening clears the completion time; a stale revision cannot overwrite it.
	request(base+"/"+made.ID, "PUT", Scratch{Title: made.Title, Content: "补充说明", Status: "todo", Revision: 4}, 200)
	list, _ = a.store.scratchList(task.ID)
	if len(list) != 1 || list[0].Status != "todo" || list[0].DoneAt != 0 {
		t.Fatal(list)
	}
	request(base+"/"+made.ID, "PUT", Scratch{Title: made.Title, Content: "并发覆盖", Revision: 4}, 409)
}
func TestScratchListOrdersOpenWorkFirst(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	stamp := now()
	for _, s := range []Scratch{
		{ID: "later", TaskID: task.ID, Title: "稍后", Status: "todo", Due: "2026-12-01", Revision: 1, Updated: stamp},
		{ID: "done", TaskID: task.ID, Title: "完成", Status: "done", Revision: 1, Updated: stamp},
		{ID: "soon", TaskID: task.ID, Title: "尽快", Status: "todo", Due: "2026-09-20", Revision: 1, Updated: stamp},
		{ID: "nowork", TaskID: task.ID, Title: "无日期", Status: "todo", Revision: 1, Updated: stamp},
		{ID: "active", TaskID: task.ID, Title: "进行中", Status: "doing", Revision: 1, Updated: stamp},
	} {
		if e := a.store.writeScratch(s, true); e != nil {
			t.Fatal(e)
		}
	}
	list, err := a.store.scratchList(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, n := range list {
		order = append(order, n.ID)
	}
	if strings.Join(order, ",") != "active,soon,later,nowork,done" {
		t.Fatal(order)
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
