package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineBindingModelCatalogUsesTaskAccount(t *testing.T) {
	a, task := switchFixture(t, &fakeRunner{})
	native := t.TempDir()
	t.Setenv("CODEX_HOME", native)
	if err := os.WriteFile(filepath.Join(native, "models_cache.json"), []byte(`{"models":[{"slug":"model-native","display_name":"Native"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := []EngineCredentialProfile{}
	for _, id := range []string{"a", "b"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(`{"models":[{"slug":"model-`+id+`","display_name":"Model"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, EngineCredentialProfile{ID: id, Name: id, EnvironmentID: task.Environment.ID, Engine: task.Engine, Kind: "codex_home", Reference: dir})
	}
	a.store.saveEngineProfiles(profiles)
	a.store.activateEngineProfile(task.Environment.ID, "codex", "a")
	bound, err := a.createWithExecution("bound", task.Workspace, "", "codex", "", task.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	following, err := a.createWithExecution("following", task.Workspace, "", "codex", "", task.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound.Binding.AccountMode = "pinned"
	bound.Binding.Profile = &profiles[0]
	saveTestBinding(t, a, bound)
	a.store.activateEngineProfile(task.Environment.ID, "codex", "b")
	if _, err = a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("switch-http"), "switch-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{app: a}).Handler()
	for _, test := range []struct{ query, want string }{{"&task_id=" + bound.ID, "model-native"}, {"&task_id=" + following.ID, "model-native"}, {"&profile_id=a", "model-a"}, {"&profile_id=b", "model-b"}, {"", "model-native"}} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/environments/"+task.Environment.ID+"/models?engine=codex"+test.query, nil)
		req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "switch-http"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var list ModelList
		if err = json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || len(list.Models) != 1 || list.Models[0].ID != test.want {
			t.Fatal("model picker followed wrong account", test.query, response.Code, response.Body.String())
		}
	}
}
