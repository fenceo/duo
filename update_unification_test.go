package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ownedUpdateFixture(t *testing.T, installed bool) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range portableArchiveFiles {
		if err := os.WriteFile(filepath.Join(root, name), testPEFile(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if installed {
		if err := os.WriteFile(filepath.Join(root, ".jianzuo-install"), []byte("Jianzuo installer ownership v1\n"+root), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestInstallationModeUsesOwnershipNotVersion(t *testing.T) {
	for _, installed := range []bool{false, true} {
		root := ownedUpdateFixture(t, installed)
		want := "portable"
		if installed {
			want = "installed"
		}
		got, err := installationMode(root, filepath.Join(root, "duo-service.exe"))
		if err != nil || got != want {
			t.Fatal(got, err)
		}
		if mode, err := installationMode(root, filepath.Join(t.TempDir(), "duo-service.exe")); err == nil || mode != "unmanaged" {
			t.Fatal(mode, err)
		}
	}
	for name, marker := range map[string]string{
		"empty": "", "relative": "Jianzuo installer ownership v1\n.",
		"other root": "Jianzuo installer ownership v1\nC:\\other", "unsupported schema": "Jianzuo installer ownership v2\nC:\\other",
		"extra record": "Jianzuo installer ownership v1\nC:\\other\nignore",
	} {
		t.Run(name, func(t *testing.T) {
			root := ownedUpdateFixture(t, false)
			if err := os.WriteFile(filepath.Join(root, ".jianzuo-install"), []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			if mode, err := installationMode(root, filepath.Join(root, "duo-service.exe")); err == nil || mode != "unmanaged" {
				t.Fatal("bad ownership fell back to portable", mode, err)
			}
		})
	}
	t.Run("legacy UTF-8 BOM", func(t *testing.T) {
		root := ownedUpdateFixture(t, false)
		if err := os.WriteFile(filepath.Join(root, ".jianzuo-install"), []byte("\ufeffJianzuo installer ownership v1\n"+root), 0600); err != nil {
			t.Fatal(err)
		}
		if mode, err := installationMode(root, filepath.Join(root, "duo-service.exe")); err != nil || mode != "installed" {
			t.Fatal(mode, err)
		}
	})
	t.Run("missing ownership with uninstaller", func(t *testing.T) {
		root := ownedUpdateFixture(t, false)
		if err := os.WriteFile(filepath.Join(root, "卸载简作.exe"), testPEFile(), 0600); err != nil {
			t.Fatal(err)
		}
		if mode, err := installationMode(root, filepath.Join(root, "duo-service.exe")); err == nil || mode != "unmanaged" {
			t.Fatal(mode, err)
		}
	})
	t.Run("directory marker", func(t *testing.T) {
		root := ownedUpdateFixture(t, false)
		if err := os.Mkdir(filepath.Join(root, ".jianzuo-install"), 0700); err != nil {
			t.Fatal(err)
		}
		if mode, err := installationMode(root, filepath.Join(root, "duo-service.exe")); err == nil || mode != "unmanaged" {
			t.Fatal(mode, err)
		}
	})
}

func TestReleaseSelectsModeSpecificAsset(t *testing.T) {
	var release map[string]any
	if err := json.Unmarshal([]byte(releaseFixture("v99.0.0")), &release); err != nil {
		t.Fatal(err)
	}
	assets := release["assets"].([]any)
	for _, name := range []string{installerAssetName, installerAssetName + ".sha256"} {
		assets = append(assets, map[string]any{"name": name, "state": "uploaded", "size": 2048, "browser_download_url": "https://github.com/owner/jianzuo/releases/download/v99.0.0/" + name, "digest": "sha256:" + strings.Repeat("b", 64)})
	}
	release["assets"] = assets
	raw, _ := json.Marshal(release)
	info, err := testUpdateChecker(t, 200, string(raw)).fetch(context.Background(), "owner/jianzuo")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"installed", "portable", "unmanaged"} {
		got := selectUpdateAsset(info, mode)
		wantName, wantKind := installerAssetName, "installer"
		if mode == "portable" {
			wantName, wantKind = portableAssetName, "portable"
		}
		if !strings.HasSuffix(got.DownloadURL, wantName) || got.ChecksumURL != got.DownloadURL+".sha256" || got.PackageKind != wantKind || got.InstallationMode != mode {
			t.Fatal(got)
		}
		if got.InstallerDownloadURL == "" || got.PortableDownloadURL == "" {
			t.Fatal("manual alternatives missing", got)
		}
	}
	legacy, err := testUpdateChecker(t, 200, releaseFixture("v0.18.1")).fetch(context.Background(), "owner/jianzuo")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectUpdateAsset(legacy, "installed"); got.DownloadURL != "" {
		t.Fatal("installed silently fell back to ZIP", got)
	}
	if got := selectUpdateAsset(legacy, "portable"); got.DownloadURL == "" {
		t.Fatal("legacy portable release unsupported")
	}
	for release, want := range map[string]bool{"v0.18.1": false, "v0.19.0": true, "v0.20.0": true, "v0.19.0-beta": false} {
		if got := innoReleaseSupported(release); got != want {
			t.Fatal(release, got)
		}
	}
}

func TestInstalledUpdateValidatesAndPropagatesInstallerFailure(t *testing.T) {
	for _, result := range []error{nil, errors.New("synthetic exit 5")} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			root := ownedUpdateFixture(t, true)
			stage, err := os.MkdirTemp(root, ".jianzuo-update-test-")
			if err != nil {
				t.Fatal(err)
			}
			dataDir := t.TempDir()
			installer := filepath.Join(stage, installerAssetName)
			if err = os.WriteFile(installer, append(testPEFile(), []byte("Inno Setup Setup Data (6.7.1)")...), 0600); err != nil {
				t.Fatal(err)
			}
			digest, _ := fileSHA256(installer)
			plan := updatePlan{Mode: "installed", Version: "v0.19.0", Digest: digest}
			raw, _ := json.Marshal(plan)
			if err = os.WriteFile(filepath.Join(stage, "update-plan.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			called := 0
			execute := func(path string, args []string, directory string) error {
				called++
				if path != installer || directory != root || !reflect.DeepEqual(args, []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/UPDATE=1", "/NOLAUNCH", "/DIR=" + root, "/DATADIR=" + dataDir, "/LOG=" + filepath.Join(dataDir, "update-installer.log")}) {
					t.Fatal(path, args, directory)
				}
				return result
			}
			err = installStagedProgram(dataDir, root, stage, "v0.19.0", execute)
			if called != 1 || (err == nil) != (result == nil) {
				t.Fatal(called, err)
			}
			if result != nil && (!errors.Is(err, result) || !strings.Contains(err.Error(), "未自动回退")) {
				t.Fatal("failure reported as success/rollback", err)
			}
			if err = os.WriteFile(installer, append(testPEFile(), []byte("Inno Setup Setup Data changed")...), 0600); err != nil {
				t.Fatal(err)
			}
			if err = installStagedProgram(dataDir, root, stage, "v0.19.0", execute); err == nil || called != 1 {
				t.Fatal("tampered package executed", err)
			}
			if err = os.WriteFile(installer, testPEFile(), 0600); err != nil {
				t.Fatal(err)
			}
			if err = validateInnoInstaller(installer); err == nil {
				t.Fatal("legacy PE accepted as Inno")
			}
		})
	}
}

func TestUpdateBusyAndAdmissionGate(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, kind := range []string{"AI", "terminal", "hardware", "queued"} {
		t.Run(kind, func(t *testing.T) {
			switch kind {
			case "AI":
				a.workers["synthetic"] = &worker{}
			case "terminal":
				a.terminals.links["synthetic"] = terminalLink{}
			case "hardware":
				a.hardware.links["synthetic"] = &hardwareLink{}
			case "queued":
				_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,created) VALUES(?,?,?,?,?,?,?)", "synthetic", task.ID, "test", "chat", "web", "queued", now())
				if err != nil {
					t.Fatal(err)
				}
			}
			release, err := a.beginUpdate()
			delete(a.workers, "synthetic")
			delete(a.terminals.links, "synthetic")
			delete(a.hardware.links, "synthetic")
			_, _ = a.store.Exec("DELETE FROM runs WHERE id='synthetic'")
			if release != nil || !errors.Is(err, errUpdateBusy) || a.updating.Load() {
				t.Fatal(release != nil, err)
			}
		})
	}
	release, err := a.beginUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.submit(task.ID, "new Feishu task", "chat", "feishu"); !errors.Is(err, errUpdateBusy) {
		t.Fatal("Feishu bypassed gate", err)
	}
	if err = a.hardware.connectOwned(HardwareConfig{ID: "fake", Protocol: "tcp", Host: "127.0.0.1", Port: 1}, "", ""); !errors.Is(err, errUpdateBusy) {
		t.Fatal("hardware bypassed gate", err)
	}
	if _, err = a.beginUpdate(); !errors.Is(err, errUpdateBusy) {
		t.Fatal("double update", err)
	}
	if !a.terminals.updating || !a.hardware.updating {
		t.Fatal("not all admissions gated")
	}
	release()
	if a.updating.Load() || a.terminals.updating || a.hardware.updating {
		t.Fatal("failed preparation did not release gates")
	}
	release, err = a.beginUpdate()
	if err != nil {
		t.Fatal("gate not reusable", err)
	}
	release()
}

func TestUpdateHTTPAuthenticationAndPreparationGate(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	if _, err := a.store.Exec("INSERT INTO sessions(token,csrf,expires) VALUES(?,?,?)", hash("update-test-session"), "update-test-csrf", now()+int64(time.Hour/time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: a}
	handler := s.Handler()
	request := func(method, path, cookie, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader("{}"))
		req.Header.Set("Origin", "http://127.0.0.1")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: cookie})
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		return result
	}
	for _, methodPath := range [][2]string{{"GET", "/api/updates"}, {"POST", "/api/updates/install"}} {
		if got := request(methodPath[0], methodPath[1], "", ""); got.Code != 401 {
			t.Fatal(got.Code, got.Body.String())
		}
	}
	if got := request("POST", "/api/updates/install", "update-test-session", ""); got.Code != 403 {
		t.Fatal(got.Code)
	}
	release, err := a.beginUpdate()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, path := range []string{"/api/updates/install", "/api/tasks/" + task.ID + "/terminal", "/api/tasks"} {
		if got := request("POST", path, "update-test-session", "update-test-csrf"); got.Code != 409 {
			t.Fatal(path, got.Code, got.Body.String())
		}
	}
	if got := request("GET", "/api/updates", "update-test-session", ""); got.Code != 200 {
		t.Fatal("read blocked", got.Code, got.Body.String())
	}
	if got := request("GET", "/api/terminal/synthetic", "update-test-session", ""); got.Code != 409 {
		t.Fatal("WebSocket admission not blocked", got.Code, got.Body.String())
	}
	health := request("GET", "/healthz", "", "")
	var status map[string]string
	if err := json.Unmarshal(health.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	releaseVersion := strings.TrimSuffix(version, "-portable")
	if health.Code != 200 || status["app"] != "jianzuo" || status["version"] != releaseVersion+"-portable" || status["release_version"] != releaseVersion {
		t.Fatal("legacy updater protocol or canonical version regressed", status)
	}
}

func TestUpdateStageRejectsBoundaryEscape(t *testing.T) {
	root := ownedUpdateFixture(t, true)
	for _, stage := range []string{root, filepath.Join(root, "other"), filepath.Join(t.TempDir(), ".jianzuo-update-outside")} {
		if err := validateUpdateStage(root, stage); err == nil {
			t.Fatal("invalid stage accepted", stage)
		}
	}
	stage, err := os.MkdirTemp(root, ".jianzuo-update-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err = validateUpdateStage(root, stage); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	link := filepath.Join(root, ".jianzuo-update-link")
	if err = os.Symlink(target, link); err != nil {
		t.Log("symlink creation unavailable; boundary checks still covered")
		return
	}
	if err = validateUpdateStage(root, link); err == nil {
		t.Fatal("symlink stage accepted")
	}
}

func TestFailedUpdatePreparationReleasesAdmissions(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	if err := a.store.set("update_repository", "owner/jianzuo"); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: a}
	if _, err := s.prepareUpdate(context.Background(), testUpdateChecker(t, 500, "")); err == nil {
		t.Fatal("network failure ignored")
	}
	if a.updating.Load() || a.terminals.updating || a.hardware.updating {
		t.Fatal("preparation failure left admissions locked")
	}
	if !s.updateMu.TryLock() {
		t.Fatal("preparation failure left request lock held")
	}
	s.updateMu.Unlock()
	if _, err := s.prepareUpdate(context.Background(), testUpdateChecker(t, 404, "")); err == nil {
		t.Fatal("missing release accepted")
	}
	if a.updating.Load() {
		t.Fatal("missing release left admissions locked")
	}
}

func TestStartedPortableHealthFailureDoesNotRollbackFiles(t *testing.T) {
	root := ownedUpdateFixture(t, false)
	dataDir := t.TempDir()
	stage, err := os.MkdirTemp(root, ".jianzuo-update-test-")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.MkdirTemp(root, ".jianzuo-update-backup-")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range portableArchiveFiles {
		if err = os.WriteFile(filepath.Join(root, name), []byte("new program"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(backup, name), []byte("old program"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(dataDir, "database-sentinel"), []byte("new data"), 0600); err != nil {
		t.Fatal(err)
	}
	err = finishPortableUpdate(dataDir, stage, backup, "v0.19.0", nil, func() error { return errors.New("synthetic health timeout") })
	if err == nil || !strings.Contains(err.Error(), "未强制停止或回退") {
		t.Fatal(err)
	}
	for _, name := range portableArchiveFiles {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil || string(raw) != "new program" {
			t.Fatal("new running program was rolled back", name, readErr)
		}
		raw, readErr = os.ReadFile(filepath.Join(backup, name))
		if readErr != nil || string(raw) != "old program" {
			t.Fatal("recovery backup lost", name, readErr)
		}
	}
	if _, err = os.Stat(stage); err != nil {
		t.Fatal("diagnostic stage removed", err)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "database-sentinel"))
	if err != nil || string(raw) != "new data" {
		t.Fatal("data modified", err)
	}
}
