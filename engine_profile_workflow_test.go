package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeletingInactiveEngineProfileKeepsActiveAccount(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	profiles := []EngineCredentialProfile{
		{ID: "a", EnvironmentID: "default", Engine: "codex", Kind: "codex_home", Reference: "/synthetic/a"},
		{ID: "b", EnvironmentID: "default", Engine: "codex", Kind: "codex_home", Reference: "/synthetic/b"},
	}
	if err := a.store.saveEngineProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	if err := a.store.activateEngineProfile("default", "codex", "a"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.removeEngineProfile("b"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.activeEngineProfile("default", "codex"); got != "a" {
		t.Fatal("deleting inactive account changed active account", got)
	}
	if got := a.store.engineProfiles(); len(got) != 1 || got[0].ID != "a" {
		t.Fatal(got)
	}
	if err := a.store.removeEngineProfile("b"); !errors.Is(err, errEngineProfileNotFound) {
		t.Fatal(err)
	}
	if err := a.store.removeEngineProfile("a"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.activeEngineProfile("default", "codex"); got != "" {
		t.Fatal("deleted active reference remained", got)
	}
}

func TestModelCatalogHTTPUsesSelectedAccountHome(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	env := Environment{ID: "win", Name: "fixture", Type: "windows", Codex: "not-run.exe", Workspaces: []string{t.TempDir()}}
	c := a.config.get()
	c.Environments = []Environment{env}
	c.DefaultEnvironment = env.ID
	if err := a.config.save(c); err != nil {
		t.Fatal(err)
	}
	profiles := []EngineCredentialProfile{}
	for _, id := range []string{"a", "b"} {
		home := t.TempDir()
		raw := []byte(`{"models":[{"slug":"model-` + id + `","display_name":"Model ` + id + `"}]}`)
		if err := os.WriteFile(filepath.Join(home, "models_cache.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, EngineCredentialProfile{ID: id, EnvironmentID: env.ID, Engine: "codex", Kind: "codex_home", Reference: home})
	}
	if err := a.store.saveEngineProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("fixture-session"), "fixture-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{app: a}).Handler()
	for _, id := range []string{"a", "b"} {
		if err := a.store.activateEngineProfile(env.ID, "codex", id); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/environments/win/models?engine=codex", nil)
		req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "fixture-session"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var list ModelList
		if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || len(list.Models) != 1 || list.Models[0].ID != "model-"+id {
			t.Fatal("model list differs from execution account", response.Code, response.Body.String())
		}
	}
}

func TestModelProbeAndUpdateAdmissionsAreMutuallyExclusive(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	if err := a.store.set("update_repository", "owner/jianzuo"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("fixture-session"), "fixture-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: a}
	handler := s.Handler()
	probeRequest := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/environments/default/models/test", strings.NewReader(`{"engine":"codex","models":["synthetic-not-called"]}`))
		req.Header.Set("Origin", "http://127.0.0.1")
		req.Header.Set("X-CSRF-Token", "fixture-csrf")
		req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "fixture-session"})
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		return result
	}
	// A live probe blocks both a second probe and update preparation without
	// issuing any model or release request.
	s.modelProbeMu.Lock()
	_, err := s.prepareUpdate(context.Background(), newUpdateChecker())
	response := probeRequest()
	s.modelProbeMu.Unlock()
	if !errors.Is(err, errUpdateBusy) || response.Code != 409 || a.updating.Load() {
		t.Fatal(err, response.Code, response.Body.String())
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	checker := newUpdateChecker()
	checker.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-proceed
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	done := make(chan error, 1)
	go func() { _, err := s.prepareUpdate(context.Background(), checker); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation deadlocked")
	}
	response = probeRequest()
	close(proceed)
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not release")
	}
	if err == nil || response.Code != 409 || a.updating.Load() {
		t.Fatal(err, response.Code, response.Body.String())
	}
	if !s.modelProbeMu.TryLock() {
		t.Fatal("model probe lock leaked after failed update")
	}
	s.modelProbeMu.Unlock()
}

func TestDuoReleaseSourceMigratesLegacyDefault(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	if err := a.store.set("update_repository", "fenceo/jianzuo"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.updateRepository(); got != "fenceo/duo" {
		t.Fatal(got)
	}
	if err := a.store.set("update_repository", "custom/project"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.updateRepository(); got != "custom/project" {
		t.Fatal("custom source overwritten", got)
	}
}
