package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHardwareDefaultCapabilitiesReachNextRunOnly(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, other := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	cases := []struct {
		name            string
		readOnly, relay bool
		want            HardwareGrant
	}{
		{"console", false, false, HardwareGrant{Read: true, Write: true}},
		{"read-only console", true, false, HardwareGrant{Read: true}},
		{"relay", false, true, HardwareGrant{Read: true, Power: true}},
		{"read-only relay", true, true, HardwareGrant{Read: true}},
	}
	token, release := a.hardwareAI.issue(context.Background(), task.ID, "before-open", map[string]HardwareGrant{})
	defer release()
	old := a.hardwareAI.lease(token)
	for i, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			config := HardwareConfig{Name: item.name, Protocol: "serial", Device: "COM" + string(rune('A'+i)), Baud: 9600, ReadOnly: item.readOnly}
			if item.relay {
				config.Kind = "relay"
				config.Relay = &RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1}
			}
			var device HardwareConfig
			json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", config, 200), &device)
			path := "/api/tasks/" + task.ID + "/hardware/" + device.ID
			request("/api/tasks/"+other.ID+"/hardware/"+device.ID+"/attach", "POST", map[string]bool{}, 200)
			var grant HardwareGrant
			json.Unmarshal(request(path+"/ai-default", "POST", map[string]bool{}, 200), &grant)
			if grant != item.want {
				t.Fatalf("default = %+v, want %+v", grant, item.want)
			}
			request(path+"/ai-default", "POST", map[string]bool{}, 200)
			events, err := a.store.hardwareEvents(device.ID, 0, false)
			if err != nil || len(events) != 1 || events[0].TaskID != task.ID {
				t.Fatal("idempotent default should record exactly one task-attributed change", events, err)
			}
			if _, _, err := a.aiHardware(old, device.ID); err == nil {
				t.Fatal("new default widened a running turn")
			}
			runtime, done, err := a.prepareHardwareAI(context.Background(), task, "after-open", Config{})
			if err != nil || runtime == nil {
				t.Fatalf("default did not enable runner tools: %v, %+v", err, runtime)
			}
			defer done()
			if _, effective, err := a.aiHardware(a.hardwareAI.lease(runtime.Token), device.ID); err != nil || effective != item.want {
				t.Fatal("runner grant differs from saved default", effective, err)
			}
			otherGrants, err := a.store.hardwareGrants(other.ID)
			if err != nil || len(otherGrants) != 0 {
				t.Fatal("default leaked to another attached task", otherGrants, err)
			}
		})
	}
}

func TestHardwareDefaultPreservesRestrictionsAndRevocation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	var device HardwareConfig
	json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "console", Protocol: "serial", Device: "COM999", Baud: 9600}, 200), &device)
	path := "/api/tasks/" + task.ID + "/hardware/" + device.ID
	for _, saved := range []HardwareGrant{{Read: true}, {}} {
		request(path+"/ai", "PUT", saved, 200)
		for i := 0; i < 2; i++ {
			var grant HardwareGrant
			json.Unmarshal(request(path+"/ai-default", "POST", map[string]bool{}, 200), &grant)
			if grant != saved {
				t.Fatalf("reopening replaced deliberate grant: %+v -> %+v", saved, grant)
			}
		}
	}
	var overview []HardwareOverview
	json.Unmarshal(request("/api/hardware/overview", "GET", nil, 200), &overview)
	if len(overview) != 1 || !overview[0].Tasks[0].AIConfigured || overview[0].Tasks[0].AI.Read {
		t.Fatal("explicit revocation is indistinguishable from no preference", overview)
	}
}

func TestHardwareDefaultRequiresLiveTaskAndBinding(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task, other := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	var device HardwareConfig
	json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "console", Protocol: "serial", Device: "COM998", Baud: 9600}, 200), &device)
	path := "/api/tasks/" + task.ID + "/hardware/" + device.ID
	request("/api/tasks/"+other.ID+"/hardware/"+device.ID+"/ai-default", "POST", map[string]bool{}, 404)
	request("/api/tasks/"+task.ID+"/preferences", "PATCH", map[string]bool{"archived": true}, 200)
	request(path+"/ai-default", "POST", map[string]bool{}, 409)
	request("/api/tasks/"+task.ID+"/preferences", "PATCH", map[string]bool{"archived": false}, 200)
	if _, err := a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET deleted=1", task.ID); err != nil {
		t.Fatal(err)
	}
	request(path+"/ai-default", "POST", map[string]bool{}, 409)
	grants, err := a.store.hardwareGrants(task.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("unavailable task received a default", grants, err)
	}
	rr := httptest.NewRecorder()
	(&Server{app: a}).Handler().ServeHTTP(rr, httptest.NewRequest("POST", path+"/ai-default", nil))
	if rr.Code != 401 && rr.Code != 403 {
		t.Fatal("default route lacks authentication", rr.Code)
	}
}

func TestHardwareConnectPersistsDefaultEvenWhenDialFails(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	var device HardwareConfig
	json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "console", Protocol: "serial", Device: "COM997", Baud: 9600}, 200), &device)
	path := "/api/tasks/" + task.ID + "/hardware/" + device.ID
	// Force failure before dialing: the fixture never accesses a real port.
	a.hardware.mu.Lock()
	a.hardware.closed = true
	a.hardware.mu.Unlock()
	request(path+"/connect", "POST", map[string]bool{}, 400)
	grants, err := a.store.hardwareGrants(task.ID)
	if err != nil || grants[device.ID] != (HardwareGrant{Read: true, Write: true}) {
		t.Fatal("explicit connect did not enable the next AI turn", grants, err)
	}
	request(path+"/ai", "PUT", HardwareGrant{}, 200)
	request(path+"/connect", "POST", map[string]bool{}, 400)
	grants, err = a.store.hardwareGrants(task.ID)
	if err != nil || grants[device.ID] != (HardwareGrant{}) {
		t.Fatal("reconnecting overrode revocation", grants, err)
	}
}
