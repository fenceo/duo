package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTaskModelCatalogFollowsOnlyVerifiedCLIUpgrade(t *testing.T) {
	for _, scenario := range []string{"verified", "manual-path", "unrecorded-path", "changed-target", "changed-current-path"} {
		t.Run(scenario, func(t *testing.T) {
			env, selected := modelFixtureEnvironment(t)
			t.Setenv("CODEX_HOME", selected["CODEX_HOME"])
			newPath := env.Codex // The test binary only accepts metadata RPCs.
			oldPath := filepath.Join(t.TempDir(), "retired-codex.exe")
			env.Codex = oldPath
			env.Name = "Isolated catalog upgrade fixture"
			a := fixture(t, &fakeRunner{})
			config := a.config.get()
			config.Environments, config.DefaultEnvironment = []Environment{env}, env.ID
			if err := a.config.save(config); err != nil {
				t.Fatal(err)
			}
			task, err := a.createWithExecution("catalog only", env.Workspaces[0], "", "codex", "", env.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := a.store.task(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			config = a.config.get()
			expected, _ := json.Marshal(config)
			current := &config.Environments[0]
			current.Codex = newPath
			current.ManagedTools = map[string]ManagedEngineTool{"codex": {Path: newPath, Version: "1.2.3", PreviousPaths: []string{oldPath}}}
			switch scenario {
			case "manual-path":
				current.ManagedTools = nil
			case "unrecorded-path":
				current.ManagedTools["codex"] = ManagedEngineTool{Path: newPath, PreviousPaths: []string{"another-codex.exe"}}
			case "changed-target":
				// The existing task stays local. A changed target must never
				// launch the replacement or connect to this synthetic SSH host.
				current.Type, current.Host = "ssh", "unused.invalid"
				current.Workspaces = []string{"/tmp/unused"}
			case "changed-current-path":
				current.Codex = filepath.Join(t.TempDir(), "manual-codex.exe")
			}
			if err := a.config.saveChecked(config, expected); err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("catalog-upgrade"), "catalog-csrf", now()+60000); err != nil {
				t.Fatal(err)
			}
			handler := (&Server{app: a}).Handler()
			request := httptest.NewRequest("GET", "http://127.0.0.1/api/environments/"+env.ID+"/models?engine=codex&task_id="+task.ID, nil)
			request.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "catalog-upgrade"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var list ModelList
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil {
				t.Fatal(response.Code, response.Body.String())
			}
			if scenario == "verified" {
				if list.Status != "ready" || list.DefaultModel != "model-selected-profile" || len(list.Models) != 2 || list.Models[0].Origin != "native" {
					t.Fatalf("catalog did not use the verified replacement: %#v", list)
				}
			} else {
				for _, model := range list.Models {
					if model.Origin == "native" {
						t.Fatalf("catalog followed an untrusted replacement: %#v", list)
					}
				}
				if list.Status == "ready" {
					t.Fatalf("untrusted replacement was launched: %#v", list)
				}
			}
			after, err := a.store.task(task.ID)
			if err != nil || !reflect.DeepEqual(before.Environment, after.Environment) || before.Session != after.Session || before.Model != after.Model || !reflect.DeepEqual(before.Binding, after.Binding) {
				t.Fatal("catalog changed the task snapshot or native session", err)
			}
		})
	}
}
