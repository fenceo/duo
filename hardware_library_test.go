package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHardwareOverviewShowsTaskGrantsAndActiveSnapshot(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, two := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	var c, spare HardwareConfig
	json.Unmarshal(request("/api/tasks/"+one.ID+"/hardware", "POST", HardwareConfig{Name: "shared", Protocol: "serial", Device: "COM991", Baud: 115200}, 200), &c)
	json.Unmarshal(request("/api/tasks/"+one.ID+"/hardware", "POST", HardwareConfig{Name: "spare", Protocol: "serial", Device: "COM992", Baud: 115200}, 200), &spare)
	request("/api/tasks/"+one.ID+"/hardware/"+spare.ID, "DELETE", map[string]string{}, 200)
	path1, path2 := "/api/tasks/"+one.ID+"/hardware/"+c.ID, "/api/tasks/"+two.ID+"/hardware/"+c.ID
	request(path2+"/attach", "POST", map[string]string{}, 200)
	request(path1+"/ai", "PUT", HardwareGrant{Read: true}, 200)
	runtime, release, err := a.prepareHardwareAI(context.Background(), one, "overview-run", Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	request(path1+"/ai", "PUT", HardwareGrant{Read: true, Write: true}, 200)
	view := func() map[string]HardwareOverview {
		t.Helper()
		raw := request("/api/hardware/overview", "GET", nil, 200)
		if bytes.Contains(raw, []byte(runtime.Token)) {
			t.Fatal("credential exposed in overview")
		}
		var list []HardwareOverview
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatal(err)
		}
		out := map[string]HardwareOverview{}
		for _, item := range list {
			out[item.Config.ID] = item
		}
		return out
	}
	list := view()
	if len(list) != 2 || len(list[spare.ID].Tasks) != 0 || list[c.ID].Connected || len(list[c.ID].Tasks) != 2 {
		t.Fatal(list)
	}
	for _, use := range list[c.ID].Tasks {
		if use.ID == one.ID {
			if !use.AI.Write || use.ActiveAI == nil || !use.ActiveAI.Read || use.ActiveAI.Write {
				t.Fatal("saved and active permissions conflated", use)
			}
		} else if use.AI.Read || use.ActiveAI != nil {
			t.Fatal("grant inherited by another task", use)
		}
	}
	request(path1+"/ai", "PUT", HardwareGrant{}, 200)
	for _, use := range view()[c.ID].Tasks {
		if use.ID == one.ID && (use.AI.Read || use.ActiveAI != nil) {
			t.Fatal("revoked grant still displayed", use)
		}
	}
	request(path2+"/ai", "PUT", HardwareGrant{Read: true}, 200)
	request("/api/tasks/"+two.ID+"/preferences", "PATCH", map[string]bool{"archived": true}, 200)
	for _, use := range view()[c.ID].Tasks {
		if use.ID == two.ID && (!use.Archived || !use.AI.Read || use.ActiveAI != nil) {
			t.Fatal("archive/config distinction missing", use)
		}
	}
	rr := httptest.NewRecorder()
	(&Server{app: a}).Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/hardware/overview", nil))
	if rr.Code != 401 {
		t.Fatal("unauthenticated overview", rr.Code)
	}
}

func TestHardwareLibraryReuseAndControl(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	one, two := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		c, e := listener.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	var c HardwareConfig
	json.Unmarshal(request("/api/tasks/"+one.ID+"/hardware", "POST", HardwareConfig{Name: "共享板卡", Protocol: "tcp", Host: host, Port: p}, 200), &c)
	path1, path2 := "/api/tasks/"+one.ID+"/hardware/"+c.ID, "/api/tasks/"+two.ID+"/hardware/"+c.ID
	request(path2+"/connect", "POST", map[string]string{}, 404)
	request(path2+"/attach", "POST", map[string]string{}, 200)
	request(path1+"/connect", "POST", map[string]string{}, 200)
	state := func(path string) map[string]any {
		var v map[string]any
		json.Unmarshal(request(path+"/events", "GET", nil, 200), &v)
		return v
	}
	first := state(path2)
	if first["controller_task"] != one.ID {
		t.Fatal(first)
	}
	var overview []HardwareOverview
	json.Unmarshal(request("/api/hardware/overview", "GET", nil, 200), &overview)
	if len(overview) != 1 || !overview[0].Connected || overview[0].ControllerTask != one.ID || overview[0].ControllerTitle != one.Title {
		t.Fatal("live owner missing", overview)
	}
	request(path2+"/send", "POST", map[string]string{"encoding": "hex", "data": "41", "connection_id": first["connection_id"].(string)}, 400)
	request(path2+"/claim", "POST", map[string]string{"connection_id": first["connection_id"].(string)}, 200)
	second := state(path1)
	if second["controller_task"] != two.ID || second["connection_id"] == first["connection_id"] {
		t.Fatal(second)
	}
	request(path1+"/send", "POST", map[string]string{"encoding": "hex", "data": "42", "connection_id": first["connection_id"].(string)}, 400)
	request(path2+"/send", "POST", map[string]string{"encoding": "hex", "data": "43", "connection_id": second["connection_id"].(string)}, 200)
	request(path1+"/disconnect", "POST", map[string]string{}, 400)
	request(path2, "DELETE", map[string]string{}, 409)
	request(path2+"/release", "POST", map[string]string{"connection_id": second["connection_id"].(string)}, 200)
	request(path2, "DELETE", map[string]string{}, 200)
	request(path2+"/events", "GET", nil, 404)
	var library []HardwareConfig
	json.Unmarshal(request("/api/hardware", "GET", nil, 200), &library)
	if len(library) != 1 || library[0].ID != c.ID {
		t.Fatal(library)
	}
	records, err := a.store.hardwareEvents(c.ID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range records {
		if e.Direction == "tx" && e.TaskID == two.ID && e.Hex == "43" {
			found = true
		}
	}
	if !found {
		t.Fatal("TX task attribution missing", records)
	}
}

func TestHardwareLegacyMigrationPreservesDetach(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Exec(`INSERT INTO tasks(id,title,workspace,model,created,updated) VALUES('t','old','/tmp','',0,0); INSERT INTO hardware VALUES('h','t','{"id":"h","name":"old","protocol":"serial","device":"COM5","baud":9600}'); INSERT INTO hardware_io(hardware_id,direction,data,created) VALUES('h','rx','41',0); DELETE FROM settings WHERE key='hardware_library_v1';`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	s.QueryRow("SELECT count(*) FROM task_hardware WHERE task_id='t' AND hardware_id='h'").Scan(&n)
	if n != 1 {
		t.Fatal("legacy binding missing")
	}
	ev, err := s.hardwareEvents("h", 0, false)
	if err != nil || len(ev) != 1 || ev[0].TaskID != "t" {
		t.Fatal(ev, err)
	}
	s.Exec("DELETE FROM task_hardware")
	s.Close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.QueryRow("SELECT count(*) FROM task_hardware").Scan(&n)
	if n != 0 {
		t.Fatal("detached device reattached on restart")
	}
}
