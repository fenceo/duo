package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func curatedFixture(t *testing.T, a *App, layer, scope, status string) CuratedKnowledge {
	t.Helper()
	v := CuratedKnowledge{Layer: layer, ScopeKey: scope, Title: "NFS 调试方式", Content: "先挂载 /srv/nfs，再读取 <workspace>/日志；按设备配置取得凭据。", Status: status, Applicability: "仅适用测试板卡与已授权网络", Evidence: "合成测试确认挂载与读取成功", CredentialRef: "设备管理中的串口配置", Tags: []string{"nfs"}}
	v, _, err := a.store.saveCurated(context.Background(), v, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCuratedPromotionRequiresExplicitVerificationAndDeduplicatesSources(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "先使用测试网络执行 NFS 验证", "observed")
	d, err := a.store.libraryDocument(context.Background(), "knowledge:"+k.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := toolsClient(t, a)
	v := curatedWrite{CuratedKnowledge: CuratedKnowledge{Layer: "consensus", Title: "NFS 通用调试", SourceID: d.ID, SourceHash: d.Hash, Applicability: "测试网络", Evidence: "来自合成任务记录", Status: "verified"}}
	request("/api/library/curated", "POST", v, 400)
	v.Status = "CONFIRMED"
	request("/api/library/curated", "POST", v, 400)
	v.Status = "verified"
	v.ConfirmVerified = true
	var one CuratedKnowledge
	if err = json.Unmarshal(request("/api/library/curated", "POST", v, 201), &one); err != nil || one.Status != "verified" || one.SourceTaskID != task.ID || one.Content != k.Content {
		t.Fatal(one, err)
	}
	var duplicate CuratedKnowledge
	json.Unmarshal(request("/api/library/curated", "POST", v, 200), &duplicate)
	if duplicate.ID != one.ID || duplicate.Revision != 1 {
		t.Fatal("same source created another curated entry", duplicate)
	}
	changed := v
	changed.Content = "重复提升时用户新写的流程，不应假装已保存"
	if response := request("/api/library/curated", "POST", changed, 409); !strings.Contains(string(response), "请编辑已有条目") {
		t.Fatal("duplicate promotion did not explain the unsaved content", string(response))
	}
	changed = v
	changed.Status = "observed"
	request("/api/library/curated", "POST", changed, 409)
	changed = v
	changed.Applicability = "新的适用范围"
	request("/api/library/curated", "POST", changed, 409)
	stored, err := a.store.curatedKnowledge(context.Background(), one.ID)
	if err != nil || stored.Content != one.Content || stored.Status != one.Status || stored.Revision != 1 || stored.Applicability != one.Applicability {
		t.Fatal("rejected promotion changed the existing entry", stored, err)
	}
	changed = v
	changed.Title = "  " + v.Title + "  "
	request("/api/library/curated", "POST", changed, 200)
	v.SourceHash = "outdated"
	request("/api/library/curated", "POST", v, 409)
	v.CuratedKnowledge = one
	v.Content = "新验证后的流程"
	request("/api/library/curated/"+one.ID, "PUT", v, 200)
	request("/api/library/curated/"+one.ID, "PUT", v, 409)
	request("/api/library/curated/"+one.ID, "DELETE", map[string]int{"revision": 1}, 409)
	request("/api/library/curated/"+one.ID, "DELETE", map[string]int{"revision": 2}, 200)
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		(&Server{app: a}).Handler().ServeHTTP(w, httptest.NewRequest(method, "/api/library/curated", strings.NewReader(`{}`)))
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("unauthenticated curated access: %s %d", method, w.Code)
		}
	}
}

func TestCuratedPreservesProcedurePathsAndRejectsCredentials(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	v := curatedFixture(t, a, "project", "开发板A", "observed")
	if !strings.Contains(v.Content, "/srv/nfs") {
		t.Fatal("NFS command path was redacted", v.Content)
	}
	_, reference, _, err := a.store.libraryReference(context.Background(), "curated:"+v.ID, "")
	if err != nil || !strings.Contains(reference, "/srv/nfs") || !strings.Contains(reference, v.Applicability) || !strings.Contains(reference, v.Evidence) || !strings.Contains(reference, "待验证") {
		t.Fatal("formal reference omitted scope/status/evidence", reference, err)
	}
	for _, secret := range []string{"password: secret-example", "密码：测试密码", "api_key=not-for-markdown", "Bearer test-access-token-123456789"} {
		copy := v
		copy.Content = secret
		if _, _, err := a.store.saveCurated(context.Background(), copy, false); err == nil {
			t.Fatal("plaintext credential accepted", secret)
		}
	}
	v.Status, v.Evidence = "verified", ""
	if _, _, err := a.store.saveCurated(context.Background(), v, false); err == nil {
		t.Fatal("verification accepted without evidence")
	}
}

func TestCuratedRecallOnlyConfirmedPlatformKnowledgeInFreshSession(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	one := curatedFixture(t, a, "consensus", "", "verified")
	curatedFixture(t, a, "consensus", "", "observed")
	curatedFixture(t, a, "consensus", "", "stale")
	curatedFixture(t, a, "system", "网络", "verified")
	text, ids, err := a.store.curatedContext(context.Background(), task)
	if err != nil || len(ids) != 1 || ids[0] != "curated:"+one.ID || !strings.Contains(text, "不授予") || !strings.Contains(text, "/srv/nfs") {
		t.Fatal(text, ids, err)
	}
	task.Session = "already-has-context"
	if text, _, err = a.store.curatedContext(context.Background(), task); err != nil || text != "" {
		t.Fatal("duplicated consensus on native resume", text, err)
	}
	task.Session = ""
	a.store.set("automatic_knowledge", `{"capture":true,"recall":false,"organize":true}`)
	if text, _, err = a.store.curatedContext(context.Background(), task); err != nil || text != "" {
		t.Fatal("recall opt out ignored", text, err)
	}
}

func TestCuratedVaultExternalEditsDowngradeAndConflictsPreserveBothSides(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	v := curatedFixture(t, a, "consensus", "", "verified")
	dir := t.TempDir()
	setTestVault(t, a, dir, true)
	report := syncTestVault(t, a)
	if report.Exported < 1 || len(report.Conflicts) != 0 {
		t.Fatal(report)
	}
	file := filepath.Join(dir, "Duo", filepath.FromSlash(curatedPath(curatedDocument(v))))
	raw := readTestFile(t, file)
	if !strings.Contains(raw, "/srv/nfs") || !strings.Contains(raw, "duo_layer: consensus") {
		t.Fatal(raw)
	}
	writeTestFile(t, file, strings.Replace(raw, "先挂载 /srv/nfs", "先在 Obsidian 挂载 /srv/nfs", 1))
	report = syncTestVault(t, a)
	v, _ = a.store.curatedKnowledge(context.Background(), v.ID)
	if report.Imported != 1 || v.Status != "observed" || !strings.Contains(v.Content, "Obsidian") {
		t.Fatal(report, v)
	}
	if !strings.Contains(readTestFile(t, file), "status: observed") {
		t.Fatal("Obsidian still marks external edit verified")
	}
	if text, _, err := a.store.curatedContext(context.Background(), taskFor(t, a)); err != nil || text != "" {
		t.Fatal("external edit retained verified recall", text, err)
	}
	results, err := a.store.searchLibrary(context.Background(), "NFS", "", "", false, LibrarySearchOptions{Layer: "consensus"})
	if err != nil || len(results.Documents) != 1 {
		t.Fatal("Vault mirror duplicated curated result", results, err)
	}
	// Changing metadata on both sides must conflict just like changing正文.
	raw = readTestFile(t, file)
	writeTestFile(t, file, strings.Replace(raw, "合成测试确认挂载与读取成功", "外部的验证依据", 1))
	v.Evidence = "Duo 内的并行依据"
	if _, _, err = a.store.saveCurated(context.Background(), v, false); err != nil {
		t.Fatal(err)
	}
	report = syncTestVault(t, a)
	v, _ = a.store.curatedKnowledge(context.Background(), v.ID)
	if len(report.Conflicts) != 1 || v.Evidence != "Duo 内的并行依据" || !strings.Contains(readTestFile(t, file), "外部的验证依据") {
		t.Fatal(report, v)
	}
}

func TestCuratedVaultImportsOnAnotherComputerAndKeepsTombstones(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	v := curatedFixture(t, a, "project", "烧录与调试", "verified")
	dir := t.TempDir()
	setTestVault(t, a, dir, true)
	syncTestVault(t, a)
	b := fixture(t, &fakeRunner{})
	setTestVault(t, b, dir, true)
	report := syncTestVault(t, b)
	copy, err := b.store.curatedKnowledge(context.Background(), v.ID)
	if err != nil || report.Imported != 1 || copy.Status != "observed" || copy.Layer != "project" || copy.ScopeKey != "烧录与调试" || copy.Content != v.Content {
		t.Fatal(report, copy, err)
	}
	if _, err = b.store.Exec("UPDATE curated_knowledge SET deleted=1 WHERE id=?", v.ID); err != nil {
		t.Fatal(err)
	}
	syncTestVault(t, b)
	_, err = b.store.curatedKnowledge(context.Background(), v.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted curated knowledge resurrected", err)
	}
	result, err := b.store.searchLibrary(context.Background(), "NFS", "", "", false, LibrarySearchOptions{Layer: "project"})
	if err != nil || len(result.Documents) != 0 {
		t.Fatal("deleted mirror remained in search", result, err)
	}
}

func TestCuratedArchiveRoundTripRemapsSourcesAndRequiresConfirmation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	k := libraryKnowledge(t, a, task, "挂载 NFS 后执行调试", "observed")
	v := curatedFixture(t, a, "consensus", "", "verified")
	v.SourceID, v.SourceTaskID, v.SourceHash = "knowledge:"+k.ID, task.ID, "snapshot-hash"
	// Provenance is immutable on edit; set it for this synthetic fixture at create.
	v.ID = ""
	v, _, err := a.store.saveCurated(context.Background(), v, true)
	if err != nil {
		t.Fatal(err)
	}
	insight := v
	insight.ID, insight.SourceID, insight.SourceHash = "", "notebook:knowledge/old-insight-run-2.md", "insight-snapshot-hash"
	if _, _, err = a.store.saveCurated(context.Background(), insight, true); err != nil {
		t.Fatal(err)
	}
	archive, attachments, err := a.store.workspaceArchiveSnapshot()
	if err != nil || len(archive.Curated) != 3 {
		t.Fatal(archive.Curated, err)
	}
	raw, err := buildWorkspaceZip(archive, attachments)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readWorkspaceZip(raw)
	if err != nil || len(decoded.Archive.Curated) != 3 || !strings.Contains(decoded.Archive.Curated[0].Content, "/srv/nfs") {
		t.Fatal(decoded.Archive.Curated, err)
	}
	b := fixture(t, &fakeRunner{})
	tx, err := b.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = importArchiveCurated(tx, decoded.Archive.Curated, map[string]string{task.ID: "new-task"}, map[string]string{"old-insight-run": "new-insight-run"}, map[string]string{k.ID: "new-knowledge"})
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	items, _ := b.store.curatedKnowledgeList(context.Background(), "", "")
	if len(items) != 3 {
		t.Fatal(items)
	}
	for _, item := range items {
		if item.ID == v.ID || item.Status != "observed" {
			t.Fatal("import overwrote identity or silently kept verification", item)
		}
		switch item.SourceID {
		case "":
		case "knowledge:new-knowledge":
			if item.SourceTaskID != "new-task" || item.SourceHash != "snapshot-hash" {
				t.Fatal("archive source mapping lost", item)
			}
		case "notebook:knowledge/new-insight-run-2.md":
			var internal string
			if err = b.store.QueryRow("SELECT source_id FROM curated_knowledge WHERE id=?", item.ID).Scan(&internal); err != nil || internal != "insight:new-insight-run-2" || item.SourceHash != "insight-snapshot-hash" || item.SourceTaskID != "new-task" {
				t.Fatal("archive insight mapping lost", item, internal, err)
			}
		default:
			t.Fatal("archive source retained an unmapped old identity", item)
		}
	}
	archive.Curated[0].Content = "password: forbidden-export"
	if _, err = buildWorkspaceZip(archive, nil); err == nil {
		t.Fatal("archive accepted curated plaintext credentials")
	}
}

func TestCuratedSearchFindsScopeAndEvidenceAndSurvivesSourceDeletion(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	v := CuratedKnowledge{Layer: "project", ScopeKey: "Vega 硬件平台", Title: "调试流程", Content: "按目标板配置执行", Applicability: "仅适用 EVT1", Evidence: "合成串口回环成功", SourceTaskID: task.ID, Status: "observed"}
	v, _, err := a.store.saveCurated(context.Background(), v, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET deleted=1", task.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"Vega", "EVT1", "回环"} {
		results, err := a.store.searchLibrary(context.Background(), query, "", "curated", false, LibrarySearchOptions{Source: "manual", Layer: "project"})
		if err != nil || len(results.Documents) != 1 || results.Documents[0].ID != "curated:"+v.ID {
			t.Fatal(query, results, err)
		}
	}
	if d, err := a.store.libraryDocument(context.Background(), "curated:"+v.ID); err != nil || d.SourceTaskID != task.ID {
		t.Fatal("formal knowledge disappeared with its source task", d, err)
	}
}

func TestCuratedVaultRenameKeepsOneFileAndIndexesActualPath(t *testing.T) {
	for _, folders := range []bool{false, true} {
		t.Run(map[bool]string{false: "tracked", true: "safe-move"}[folders], func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			v := curatedFixture(t, a, "project", "旧项目", "observed")
			dir := t.TempDir()
			setTestVault(t, a, dir, true)
			c := a.store.vaultConfig()
			c.TaskFolders = folders
			config, _ := json.Marshal(c)
			a.store.set("knowledge_vault", string(config))
			syncTestVault(t, a)
			old := curatedPath(curatedDocument(v))
			v.Title, v.ScopeKey = "新的调试文档", "新项目"
			v, _, err := a.store.saveCurated(context.Background(), v, false)
			if err != nil {
				t.Fatal(err)
			}
			report := syncTestVault(t, a)
			if len(report.Conflicts) != 0 {
				t.Fatal(report)
			}
			doc, err := a.store.libraryDocument(context.Background(), "curated:"+v.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := old
			if folders {
				want = curatedPath(curatedDocument(v))
				if _, err = os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(old))); !os.IsNotExist(err) {
					t.Fatal("safe move left a duplicate old file", err)
				}
			}
			if doc.Path != want {
				t.Fatal("citation does not follow tracked file", doc.Path, want)
			}
			index := readTestFile(t, filepath.Join(dir, "Duo", "projects", "README.md"))
			if !strings.Contains(index, strings.ReplaceAll(notebookLink("projects/README.md", want), " ", "%20")) {
				t.Fatal("reading index has a stale renamed path", index)
			}
		})
	}
}

func TestCuratedCustomFolderParentChangeMovesTrackedPathWithoutTaskFolders(t *testing.T) {
	for _, layer := range []string{"consensus", "system", "project"} {
		t.Run(layer, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			v := curatedFixture(t, a, layer, "合成范围", "observed")
			dir := t.TempDir()
			setTestVault(t, a, dir, true)
			c := a.store.vaultConfig()
			c.TaskFolders = false
			folder := &c.ConsensusDirectory
			if layer == "system" {
				folder = &c.SystemDirectory
			}
			if layer == "project" {
				folder = &c.ProjectDirectory
			}
			*folder = "资料/原分类"
			config, _ := json.Marshal(c)
			if err := a.store.set("knowledge_vault", string(config)); err != nil {
				t.Fatal(err)
			}
			syncTestVault(t, a)
			old := configuredCuratedPath(c, curatedDocument(v))
			*folder = "资料"
			config, _ = json.Marshal(c)
			if err := a.store.set("knowledge_vault", string(config)); err != nil {
				t.Fatal(err)
			}
			report := syncTestVault(t, a)
			if len(report.Conflicts) != 0 {
				t.Fatal(report)
			}
			want := configuredCuratedPath(c, curatedDocument(v))
			doc, err := a.store.libraryDocument(context.Background(), "curated:"+v.ID)
			if err != nil || doc.Path != want {
				t.Fatal("custom parent-folder change did not update citation", doc, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(old))); !os.IsNotExist(err) {
				t.Fatal("old file was duplicated", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(want))); err != nil {
				t.Fatal("new file is missing", err)
			}
		})
	}
}

func TestCuratedReadingLinksWorkBeforeVaultSync(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	v := curatedFixture(t, a, "consensus", "", "observed")
	index, err := a.store.libraryDocument(context.Background(), "document:consensus/README.md")
	if err != nil || index.Kind != "index" || !strings.Contains(index.Content, v.Title) {
		t.Fatal(index, err)
	}
	d, err := a.store.libraryDocument(context.Background(), "document:"+curatedPath(curatedDocument(v)))
	if err != nil || d.ID != "curated:"+v.ID || d.Content != v.Content {
		t.Fatal("local reading link requires Vault mirroring", d, err)
	}
}

func TestCuratedPromotionDeduplicatesForkOrigins(t *testing.T) {
	for _, forkFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "original-first", true: "fork-first"}[forkFirst], func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			original, fork := taskFor(t, a), taskFor(t, a)
			originalRun, forkRun := uid(), uid()
			for run, task := range map[string]string{originalRun: original.ID, forkRun: fork.ID} {
				if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,'合成调试','chat','web','done','相同的已完成记录',1,2)", run, task); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.store.Exec("INSERT INTO run_fork_sources(run_id,source_task_id,source_run_id) VALUES(?,?,?)", forkRun, original.ID, originalRun); err != nil {
				t.Fatal(err)
			}
			one, err := a.store.libraryDocument(context.Background(), "run:"+originalRun)
			if err != nil {
				t.Fatal(err)
			}
			two, err := a.store.libraryDocument(context.Background(), "run:"+forkRun)
			if err != nil {
				t.Fatal(err)
			}
			if forkFirst {
				one, two = two, one
			}
			request := toolsClient(t, a)
			v := curatedWrite{CuratedKnowledge: CuratedKnowledge{Layer: "consensus", Title: "可复用的调试流程", Content: "合成记录只提升一次", SourceID: one.ID, SourceHash: one.Hash}}
			var saved CuratedKnowledge
			if err = json.Unmarshal(request("/api/library/curated", "POST", v, 201), &saved); err != nil {
				t.Fatal(err)
			}
			v.SourceID, v.SourceHash = two.ID, two.Hash
			request("/api/library/curated", "POST", v, 409)
			items, err := a.store.curatedKnowledgeList(context.Background(), "consensus", "")
			if err != nil || len(items) != 1 || items[0].ID != saved.ID || items[0].SourceID != one.ID || items[0].SourceHash != one.Hash || items[0].Revision != 1 {
				t.Fatal("fork duplicated knowledge or rewrote literal provenance", items, err)
			}
			var internal string
			if err = a.store.QueryRow("SELECT source_id FROM curated_knowledge WHERE id=?", saved.ID).Scan(&internal); err != nil || internal != "run:"+originalRun {
				t.Fatal("internal origin was not canonical", internal, err)
			}
		})
	}
}
