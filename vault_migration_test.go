package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func vaultMigrationHTTPClient(t *testing.T, a *App) func(string, string, any, int) []byte {
	t.Helper()
	server := httptest.NewServer((&Server{app: a}).Handler())
	t.Cleanup(server.Close)
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("migration-test"), "migration-csrf", now()+120000); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 30 * time.Second} // Full-copy hash checks exceed the ordinary 5 s fixture budget on Windows.
	return func(route, method string, value any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(value)
		r, _ := http.NewRequest(method, server.URL+route, bytes.NewReader(b))
		r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "migration-test"})
		r.Header.Set("Origin", server.URL)
		r.Header.Set("X-CSRF-Token", "migration-csrf")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		result, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, route, response.StatusCode, result)
		}
		return result
	}
}

func vaultMigrationFixture(t *testing.T) (*App, VaultMigrationRequest, CuratedKnowledge) {
	t.Helper()
	a := fixture(t, &fakeRunner{})
	a.updating.Store(true) // Explicit sync owns fixture counters and baselines.
	c := VaultConfig{Directory: t.TempDir(), Enabled: true, RootMode: "direct", HumanLayout: true, TaskFolders: true}
	legacy := false
	c.IncludeLegacy = &legacy
	b, _ := json.Marshal(c)
	if _, err := a.store.Exec("UPDATE settings SET value=? WHERE key='knowledge_vault'", string(b)); err != nil {
		t.Fatal(err)
	}
	v, _, err := a.store.saveCurated(context.Background(), CuratedKnowledge{
		Layer: "project", ScopeKey: "合成项目", Title: "合成 NFS 调试", Content: "先挂载 /srv/nfs，然后验证合成日志。",
		Status: "verified", Evidence: "合成回归确认", Applicability: "隔离合成设备", Tags: []string{"nfs", "回归"},
		SourceID: "run:portable-origin", SourceHash: hash("portable source snapshot"), SourceTaskID: "portable-source-task",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	v.Content += "\n人工补充适用范围。"
	v, _, err = a.store.saveCurated(context.Background(), v, false)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := a.syncVault(context.Background()); err != nil || len(report.Conflicts) > 0 {
		t.Fatal(report, err)
	}
	return a, VaultMigrationRequest{Directory: t.TempDir(), RootMode: "direct"}, v
}

func vaultMigrationPath(t *testing.T, a *App, v CuratedKnowledge) string {
	t.Helper()
	var rel string
	if err := a.store.QueryRow("SELECT path FROM library_links WHERE id=?", "curated:"+v.ID).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestVaultMigrationPreservesIdentityProvenanceManualFilesAndSource(t *testing.T) {
	a, request, original := vaultMigrationFixture(t)
	ctx := context.Background()
	source := vaultDocumentDirectory(a.store.vaultConfig())
	rel := vaultMigrationPath(t, a, original)
	manual := filepath.Join(source, "人工手册", "图片说明.md")
	if err := os.MkdirAll(filepath.Dir(manual), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manual, []byte("# 人工维护\n\n![图](board.png)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(manual), "board.png"), []byte{1, 2, 3, 4}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "conflicts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "conflicts", "手工合并.md"), []byte("保留冲突双方证据"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.vaultMigrationPreview(ctx, request)
	if err != nil || !p.Ready || p.FileCount < 7 || p.CopyCount != p.FileCount {
		t.Fatal(p, err)
	}
	if _, err = os.Stat(filepath.Join(request.Directory, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatal("preview wrote target", err)
	}
	request.Confirm, request.Fingerprint = true, p.Fingerprint
	result, err := a.migrateVault(ctx, request)
	if err != nil || !result.Completed || result.Copied != p.FileCount {
		t.Fatal(result, err)
	}
	stored, err := a.store.curatedKnowledge(ctx, original.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatal("identity or revision/provenance changed", stored, original, err)
	}
	if got := a.store.vaultConfig().Directory; got != request.Directory {
		t.Fatal("new root not committed", got)
	}
	for _, file := range p.Files {
		old, oldErr := os.ReadFile(filepath.Join(source, filepath.FromSlash(file.Path)))
		copied, copyErr := os.ReadFile(filepath.Join(request.Directory, filepath.FromSlash(file.Path)))
		if oldErr != nil || copyErr != nil || string(old) != string(copied) {
			t.Fatal("source/target bytes differ", file.Path, oldErr, copyErr)
		}
	}
	if current := vaultMigrationPath(t, a, stored); current != rel {
		t.Fatal("tracked path changed", current, rel)
	}
	if report, err := a.syncVault(ctx); err != nil || len(report.Conflicts) > 0 || report.Imported != 0 {
		t.Fatal("migration manufactured a second identity/import", report, err)
	}
}

func TestVaultMigrationRejectsDirtySourcesAndPendingExports(t *testing.T) {
	for _, side := range []string{"external", "database"} {
		t.Run(side, func(t *testing.T) {
			a, request, v := vaultMigrationFixture(t)
			oldRoot := a.store.vaultConfig().Directory
			if side == "external" {
				name := filepath.Join(oldRoot, filepath.FromSlash(vaultMigrationPath(t, a, v)))
				b, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(name, append(b, []byte("\n人工最新证据\n")...), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				v.Content += "\nDuo 内的新修改"
				if _, _, err := a.store.saveCurated(context.Background(), v, false); err != nil {
					t.Fatal(err)
				}
			}
			p, err := a.vaultMigrationPreview(context.Background(), request)
			if err != nil || p.Ready || !p.SourceDirty {
				t.Fatal(p, err)
			}
			request.Confirm, request.Fingerprint = true, p.Fingerprint
			if _, err = a.migrateVault(context.Background(), request); err == nil {
				t.Fatal("dirty source accepted")
			}
			if a.store.vaultConfig().Directory != oldRoot {
				t.Fatal("rejected migration changed root")
			}
		})
	}
}

func TestVaultMigrationRejectsStalePreviewAndTargetConflictAndResumes(t *testing.T) {
	a, request, v := vaultMigrationFixture(t)
	ctx := context.Background()
	p, err := a.vaultMigrationPreview(ctx, request)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	request.Confirm, request.Fingerprint = true, p.Fingerprint
	file := p.Files[0]
	if err = copyVaultMigrationFile(ctx, p.SourceDirectory, p.TargetDirectory, file); err != nil {
		t.Fatal(err)
	}
	if _, err = a.migrateVault(ctx, request); err == nil {
		t.Fatal("target changed after preview but accepted")
	}
	p, err = a.vaultMigrationPreview(ctx, request)
	if err != nil || !p.Ready || p.IdenticalCount != 1 {
		t.Fatal("safe partial attempt cannot resume", p, err)
	}
	conflicting := filepath.Join(request.Directory, filepath.FromSlash(vaultMigrationPath(t, a, v)))
	if err = os.MkdirAll(filepath.Dir(conflicting), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(conflicting, []byte("人工目标文件，不能覆盖"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = a.vaultMigrationPreview(ctx, request)
	if err != nil || p.Ready || len(p.Conflicts) != 1 {
		t.Fatal("target conflict not shown", p, err)
	}
	request.Fingerprint = p.Fingerprint
	if _, err = a.migrateVault(ctx, request); err == nil {
		t.Fatal("conflicting migration accepted")
	}
	b, _ := os.ReadFile(conflicting)
	if string(b) != "人工目标文件，不能覆盖" {
		t.Fatal("target manual file overwritten")
	}
}

func TestVaultMigrationSettingsStaleOverlapAndHTTPConfirmation(t *testing.T) {
	a, request, _ := vaultMigrationFixture(t)
	ctx := context.Background()
	p, err := a.vaultMigrationPreview(ctx, request)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	a.updating.Store(false) // HTTP mutations use the ordinary secure guard.
	client := vaultMigrationHTTPClient(t, a)
	client("/api/library/vault/migration", "POST", request, 400)
	request.Confirm, request.Fingerprint = true, p.Fingerprint
	c := a.store.vaultConfig()
	c.IncludeRuns = true
	b, _ := json.Marshal(c)
	if _, err = a.store.Exec("UPDATE settings SET value=? WHERE key='knowledge_vault'", string(b)); err != nil {
		t.Fatal(err)
	}
	client("/api/library/vault/migration", "POST", request, 409)
	if a.store.vaultConfig().Directory == request.Directory {
		t.Fatal("stale settings accepted")
	}
	request.Directory = filepath.Join(c.Directory, "child")
	if err = os.Mkdir(request.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = a.vaultMigrationPreview(ctx, request); err == nil {
		t.Fatal("overlapping roots accepted")
	}
	volumeRoot := filepath.VolumeName(c.Directory) + string(filepath.Separator)
	if !vaultMigrationOverlap(volumeRoot, c.Directory) || !vaultMigrationOverlap(c.Directory, volumeRoot) {
		t.Fatal("drive root containment not detected", volumeRoot, c.Directory)
	}
	for _, route := range []string{"/api/library/vault/migration/preview", "/api/library/vault/migration"} {
		w := httptest.NewRecorder()
		(&Server{app: a}).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`)))
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthenticated migration accepted", route, w.Code)
		}
		r := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "migration-test"})
		w = httptest.NewRecorder()
		(&Server{app: a}).Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("migration accepted without CSRF", route, w.Code)
		}
	}
}

func TestVaultMigrationHTTPPreviewCommitAndFailureRecovery(t *testing.T) {
	a, request, _ := vaultMigrationFixture(t)
	a.updating.Store(false)
	client := vaultMigrationHTTPClient(t, a)
	var p VaultMigrationPreview
	if err := json.Unmarshal(client("/api/library/vault/migration/preview", "POST", request, 200), &p); err != nil || !p.Ready || p.Fingerprint == "" {
		t.Fatal(p, err)
	}
	request.Confirm, request.Fingerprint = true, p.Fingerprint
	if err := json.Unmarshal(client("/api/library/vault/migration", "POST", request, 200), &p); err != nil || !p.Completed || p.Config == nil || p.Config.Directory != request.Directory {
		t.Fatal(p, err)
	}
	// A cancelled copy leaves no published partial document. The same source
	// and target still work on the next attempt; no active-root change is needed.
	source, target := t.TempDir(), t.TempDir()
	content := "合成中止恢复证据"
	if err := os.WriteFile(filepath.Join(source, "recover.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	item := VaultMigrationFile{Path: "recover.md", Size: int64(len(content)), Hash: hash(content)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyVaultMigrationFile(ctx, source, target, item); err == nil {
		t.Fatal("cancelled copy succeeded")
	}
	if _, err := os.Stat(filepath.Join(target, item.Path)); !os.IsNotExist(err) {
		t.Fatal("cancelled copy published a partial file", err)
	}
	if err := copyVaultMigrationFile(context.Background(), source, target, item); err != nil {
		t.Fatal("cancelled copy could not be retried", err)
	}
	if err := os.WriteFile(filepath.Join(target, item.Path), []byte("目标人工新编辑"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyVaultMigrationFile(context.Background(), source, target, item); err == nil {
		t.Fatal("copy primitive overwrote a concurrently created target")
	}
	if b, err := os.ReadFile(filepath.Join(target, item.Path)); err != nil || string(b) != "目标人工新编辑" {
		t.Fatal("copy primitive lost concurrent target content", string(b), err)
	}
}

func TestVaultMigrationRejectsUnrelatedTargets(t *testing.T) {
	a, request, _ := vaultMigrationFixture(t)
	if err := os.WriteFile(filepath.Join(request.Directory, "已有人工内容.md"), []byte("保留目标人工笔记"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.vaultMigrationPreview(context.Background(), request)
	if err != nil || p.Ready || len(p.Conflicts) != 1 {
		t.Fatal("unrelated target was silently merged", p, err)
	}
}

func TestVaultMigrationRejectsLinkedPaths(t *testing.T) {
	a, request, _ := vaultMigrationFixture(t)
	outside := t.TempDir()
	link := filepath.Join(a.store.vaultConfig().Directory, "linked-directory")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("Windows account cannot create test symlinks", err)
	}
	if _, err := a.vaultMigrationPreview(context.Background(), request); err == nil {
		t.Fatal("linked source path accepted")
	}
}
