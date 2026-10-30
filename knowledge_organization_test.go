package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func organizationVault(t *testing.T, a *App, c VaultConfig) {
	t.Helper()
	a.updating.Store(true) // Exercise explicit sync/timer calls without the background loop.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.set("knowledge_vault", string(raw)); err != nil {
		t.Fatal(err)
	}
}

func organizationMemory(content string) taskMemory {
	return taskMemory{KnowledgeCandidates: []knowledgeCandidate{{Layer: "project", ScopeKey: "合成平台", Title: "NFS 验证方法", Content: content, Evidence: "合成验证记录通过，未调用真实模型或设备。", Applicability: "仅合成回归环境", Tags: []string{"NFS"}}}}
}

func TestKnowledgeOrganizationRevisionAcceptIgnoreAndReharvest(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.updating.Store(true)
	task := taskFor(t, a)
	ctx := context.Background()
	firstRun := storeExtractionMemory(t, a, task, organizationMemory("初始步骤：先检查 NFS 服务可达。"))
	first, err := a.store.extractTaskKnowledge(ctx, task.ID, firstRun, nil)
	if err != nil || first.Created != 1 || first.Revisions != 0 {
		t.Fatal(first, err)
	}
	base := first.Items[0]
	base.Status = "verified"
	base, _, err = a.store.saveCurated(ctx, base, false)
	if err != nil {
		t.Fatal(err)
	}
	secondRun := storeExtractionMemory(t, a, task, organizationMemory("修订步骤：服务可达后，还需检查导出目录的读取权限。"))
	second, err := a.store.extractTaskKnowledge(ctx, task.ID, secondRun, nil)
	if err != nil || second.Created != 0 || second.Revisions != 1 {
		t.Fatal(second, err)
	}
	queued, err := a.store.curatedKnowledge(ctx, base.ID)
	if err != nil || queued.Content != base.Content || queued.Status != "verified" || len(queued.PendingRevisions) != 1 || queued.PendingRevisions[0].SourceID != "run:"+secondRun || queued.PendingRevisions[0].SourceTaskID != task.ID {
		t.Fatal("proposal overwrote the original or lost its source", queued, err)
	}
	pending := queued.PendingRevisions[0]
	a.updating.Store(false) // Authenticated mutation routes intentionally reject updates in progress.
	request := toolsClient(t, a)
	endpoint := "/api/library/curated/" + queued.ID + "/revisions/" + pending.Key
	request(endpoint, "POST", map[string]any{"revision": queued.Revision}, 400)
	request(endpoint, "POST", map[string]any{"revision": base.Revision, "accept": true}, 409)
	var accepted CuratedKnowledge
	if err = json.Unmarshal(request(endpoint, "POST", map[string]any{"revision": queued.Revision, "accept": true}, 200), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Content != pending.Content || accepted.Status != "observed" || len(accepted.PendingRevisions) != 0 || len(accepted.RevisionHistory) != 1 || accepted.RevisionHistory[0].SourceID != "run:"+secondRun || accepted.SourceID != "run:"+firstRun || len(accepted.ReviewKeys) != 1 {
		t.Fatal("accepted revision lost history, source or verification safety", accepted)
	}
	request(endpoint, "POST", map[string]any{"revision": accepted.Revision, "accept": true}, 409)
	repeat, err := a.store.extractTaskKnowledge(ctx, task.ID, "", nil)
	if err != nil || repeat.Created != 0 || repeat.Revisions != 0 || repeat.Duplicates != 2 {
		t.Fatal("accepted candidate was recreated", repeat, err)
	}
	thirdRun := storeExtractionMemory(t, a, task, organizationMemory("不采纳的修订：把读取测试改成无关的重装步骤。"))
	third, err := a.store.extractTaskKnowledge(ctx, task.ID, thirdRun, nil)
	if err != nil || third.Revisions != 1 {
		t.Fatal(third, err)
	}
	queued, _ = a.store.curatedKnowledge(ctx, base.ID)
	ignored, err := a.store.resolveKnowledgeRevision(ctx, queued.ID, queued.PendingRevisions[0].Key, queued.Revision, false)
	if err != nil || ignored.Content != accepted.Content || ignored.Status != accepted.Status || len(ignored.PendingRevisions) != 0 || len(ignored.RevisionHistory) != 1 || len(ignored.ReviewKeys) != 2 {
		t.Fatal("ignoring revision changed the original or dropped the tombstone", ignored, err)
	}
	repeat, err = a.store.extractTaskKnowledge(ctx, task.ID, "", nil)
	if err != nil || repeat.Created != 0 || repeat.Revisions != 0 || repeat.Duplicates != 3 {
		t.Fatal("ignored candidate was resurrected", repeat, err)
	}
	items, _ := a.store.curatedKnowledgeList(ctx, "", "")
	if len(items) != 1 {
		t.Fatal("same topic became multiple standalone notes", items)
	}
}

func TestKnowledgeOrganizationRevisionProtectsEditedBaseAndValidatesSources(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.updating.Store(true)
	ctx := context.Background()
	base := curatedFixture(t, a, "project", "合成平台", "verified")
	p := KnowledgeRevision{Key: hash("candidate"), Content: "需要审阅的新正文", Evidence: "合成测试依据", Applicability: "仅合成测试", SourceID: "run:" + uid(), SourceTaskID: uid()}
	queued, _, err := a.store.queueKnowledgeRevision(ctx, base.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	queued.Content = "用户在整理修订前已经手动更新正文。"
	edited, _, err := a.store.saveCurated(ctx, queued, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.resolveKnowledgeRevision(ctx, edited.ID, p.Key, edited.Revision, true); err == nil || !strings.Contains(err.Error(), "原正文已变化") {
		t.Fatal("revision replaced a changed baseline", err)
	}
	stored, _ := a.store.curatedKnowledge(ctx, edited.ID)
	if stored.Content != edited.Content || stored.Revision != edited.Revision || len(stored.PendingRevisions) != 1 {
		t.Fatal("rejected acceptance mutated the document", stored)
	}
	if _, err = a.store.resolveKnowledgeRevision(ctx, edited.ID, p.Key, edited.Revision, false); err != nil {
		t.Fatal("a changed baseline must still allow ignoring a proposal", err)
	}
	p.BaseHash = hash("base")
	for name, mutate := range map[string]func(*KnowledgeRevision){
		"non-hex key":      func(p *KnowledgeRevision) { p.Key = strings.Repeat("z", 64) },
		"non-hex hash":     func(p *KnowledgeRevision) { p.BaseHash = strings.Repeat("z", 64) },
		"source secret":    func(p *KnowledgeRevision) { p.SourceID = "password: synthetic-secret" },
		"task secret":      func(p *KnowledgeRevision) { p.SourceTaskID = "密码：合成凭据" },
		"oversized source": func(p *KnowledgeRevision) { p.SourceID = strings.Repeat("a", 1025) },
		"oversized task":   func(p *KnowledgeRevision) { p.SourceTaskID = strings.Repeat("a", 121) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := p
			mutate(&invalid)
			if err := validateKnowledgeRevisions([]KnowledgeRevision{invalid}); err == nil {
				t.Fatal("invalid nested revision metadata accepted")
			}
		})
	}
}

func TestKnowledgeOrganizationDirectDefaultsAndReadableCollisionPaths(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.updating.Store(true)
	if _, err := a.store.Exec("DELETE FROM settings WHERE key='knowledge_vault'"); err != nil {
		t.Fatal(err)
	}
	fresh := a.store.vaultConfig()
	if fresh.RootMode != "direct" || !fresh.HumanLayout || fresh.IncludeLegacy == nil || *fresh.IncludeLegacy || fresh.IncludeRuns || vaultDocumentDirectory(fresh) != fresh.Directory {
		t.Fatal("new defaults retain the legacy extra root or raw exports", fresh)
	}
	legacyRoot := t.TempDir()
	organizationVault(t, a, VaultConfig{Enabled: true, Directory: legacyRoot})
	legacy := a.store.vaultConfig()
	if legacy.RootMode != "" || legacy.HumanLayout || legacy.IncludeLegacy != nil || vaultDocumentDirectory(legacy) != filepath.Join(legacyRoot, "Duo") {
		t.Fatal("loading an existing config silently migrated its root", legacy)
	}
	c := VaultConfig{RootMode: "direct", HumanLayout: true, Directory: t.TempDir(), Enabled: true}
	first := curatedDocument(CuratedKnowledge{ID: uid(), Layer: "consensus", Title: "中文主题"})
	second := curatedDocument(CuratedKnowledge{ID: uid(), Layer: "consensus", Title: "中文主题"})
	third := curatedDocument(CuratedKnowledge{ID: uid(), Layer: "consensus", Title: "中文主题-" + hash(first.ID)[:8]})
	paths := curatedHumanPaths([]LibraryDocument{third, second, first}, c)
	seen := map[string]bool{}
	for id, p := range paths {
		if p == "" || seen[strings.ToLower(p)] {
			t.Fatal("readable filename collision", id, p, paths)
		}
		seen[strings.ToLower(p)] = true
	}
	for _, title := range []string{"README", "CON", "LPT1", strings.Repeat("甲", 69) + " .尾"} {
		name := knowledgeFilename(title)
		if strings.TrimRight(name, " .") != name || strings.EqualFold(name, "README") || strings.EqualFold(name, "CON") || strings.EqualFold(name, "LPT1") {
			t.Fatal("unportable filename after normalization", title, name)
		}
	}
	// Distinct labels can sanitize to one scope folder. Their landing-page
	// fallback names must not collide with a user-authored knowledge title.
	scope := "合成/平台"
	indexCollision := []CuratedKnowledge{
		{ID: uid(), Layer: "project", ScopeKey: scope, Title: "首页-" + hash(scope)[:8]},
		{ID: uid(), Layer: "project", ScopeKey: "合成:平台", Title: "基础配置"},
	}
	indexDocs := []LibraryDocument{curatedDocument(indexCollision[0]), curatedDocument(indexCollision[1])}
	indexPaths := curatedHumanPaths(indexDocs, c)
	for _, index := range humanCuratedIndexes(indexCollision, nil, c) {
		for _, formalPath := range indexPaths {
			if strings.EqualFold(index.Path, formalPath) {
				t.Fatal("a scope landing page collides with a formal note", index.Path)
			}
		}
	}
	a.updating.Store(false)
	request := toolsClient(t, a)
	request("/api/library/vault", "PUT", c, 200)
	var layers struct {
		Layers []struct {
			Directory string `json:"directory"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(request("/api/library/layers", "GET", nil, 200), &layers); err != nil {
		t.Fatal(err)
	}
	for _, layer := range layers.Layers {
		if strings.HasPrefix(layer.Directory, "Duo/") {
			t.Fatal("direct mode still advertises the legacy extra root", layer)
		}
	}
	for _, folder := range []string{"首页.md", "首页.md/项目", "README.md", "readme.md/项目"} {
		invalid := c
		invalid.ConsensusDirectory = folder
		if err := prepareKnowledgeFolders(&invalid); err == nil {
			t.Fatal("classification directory overlaps a managed root document", folder)
		}
	}
	c.RootMode = "invalid"
	request("/api/library/vault", "PUT", c, 400)
}

func TestKnowledgeOrganizationDirectExportsOnlyClassifiedNotesAndProtectsExternalFiles(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	storeExtractionMemory(t, a, task, organizationMemory("合成任务原始记录，不应变成笔记根中的对话镜像。"))
	libraryKnowledge(t, a, task, "旧版片段不再写入新笔记根", "observed")
	v := curatedFixture(t, a, "project", "合成平台", "verified")
	noLegacy := false
	c := VaultConfig{RootMode: "direct", HumanLayout: true, IncludeLegacy: &noLegacy, TaskFolders: true, IncludeAutomatic: true, IncludeRuns: true, Enabled: true, Directory: t.TempDir(), ConsensusDirectory: "约定", SystemDirectory: "学习", ProjectDirectory: "项目"}
	organizationVault(t, a, c)
	report := syncTestVault(t, a)
	if len(report.Conflicts) != 0 {
		t.Fatal(report)
	}
	for _, relative := range []string{"首页.md", "约定/README.md", "学习/README.md", "项目/README.md", "项目/合成平台/NFS 调试方式.md"} {
		if _, err := os.Stat(filepath.Join(c.Directory, filepath.FromSlash(relative))); err != nil {
			t.Fatal("readable classified file missing", relative, err)
		}
	}
	for _, name := range []string{"Duo", "tasks", "knowledge", "topics", "run"} {
		if _, err := os.Stat(filepath.Join(c.Directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("legacy fragments leaked into the direct root", name, err)
		}
	}
	path := filepath.Join(c.Directory, "项目", "合成平台", "NFS 调试方式.md")
	external := strings.Replace(readTestFile(t, path), "先挂载 /srv/nfs", "人工修订：先在 Obsidian 检查 /srv/nfs", 1)
	writeTestFile(t, path, external)
	v.Evidence = "Duo 内另一份并行依据"
	if _, _, err := a.store.saveCurated(context.Background(), v, false); err != nil {
		t.Fatal(err)
	}
	report = syncTestVault(t, a)
	if len(report.Conflicts) != 1 || readTestFile(t, path) != external {
		t.Fatal("parallel external and internal edits did not preserve the file", report)
	}
}

func TestKnowledgeOrganizationTimerUsesCandidatesAndPersistsIntervalState(t *testing.T) {
	runner := &fakeRunner{}
	a := fixture(t, runner)
	task := taskFor(t, a)
	noLegacy := false
	organizationVault(t, a, VaultConfig{RootMode: "direct", HumanLayout: true, IncludeLegacy: &noLegacy, Directory: t.TempDir(), Enabled: true})
	if err := a.store.set("knowledge_extraction", `{"enabled":false,"scheduled":true,"interval_hours":1}`); err != nil {
		t.Fatal(err)
	}
	storeExtractionMemory(t, a, task, organizationMemory("定时整理只读取这个已保存的候选。"))
	at := now()
	a.organizeKnowledgeDue(context.Background(), at)
	config := a.store.knowledgeExtractionConfig()
	items, _ := a.store.curatedKnowledgeList(context.Background(), "", "")
	if len(items) != 1 || config.LastOrganized != at || config.LastError != "" || !config.Scheduled || config.Enabled {
		t.Fatal("scheduled extraction or persisted success state failed", items, config)
	}
	storeExtractionMemory(t, a, task, organizationMemory("同主题的新候选，应等下一次定时整理再进入修订。"))
	a.organizeKnowledgeDue(context.Background(), at+1000)
	stored, _ := a.store.curatedKnowledge(context.Background(), items[0].ID)
	if len(stored.PendingRevisions) != 0 || a.store.knowledgeExtractionConfig().LastOrganized != at {
		t.Fatal("timer ignored its interval", stored)
	}
	next := at + int64(time.Hour/time.Millisecond)
	a.organizeKnowledgeDue(context.Background(), next)
	stored, _ = a.store.curatedKnowledge(context.Background(), items[0].ID)
	if len(stored.PendingRevisions) != 1 || a.store.knowledgeExtractionConfig().LastOrganized != next {
		t.Fatal("next due timer did not harvest the pending candidate", stored)
	}
	runner.mu.Lock()
	modelCalls := len(runner.inputs)
	runner.mu.Unlock()
	var runs int
	if err := a.store.QueryRow("SELECT count(*) FROM runs").Scan(&runs); err != nil || runs != 2 || modelCalls != 0 {
		t.Fatal("timer started a model or created a job", runs, modelCalls, err)
	}
	a.updating.Store(false)
	request := toolsClient(t, a)
	request("/api/library/extraction", "PUT", map[string]any{"scheduled": true, "interval_hours": 0}, 400)
	request("/api/library/extraction", "PUT", map[string]any{"scheduled": true, "interval_hours": 169}, 400)
	request("/api/library/extraction", "PUT", map[string]any{"scheduled": false, "interval_hours": 12}, 200)
	if config := a.store.knowledgeExtractionConfig(); config.Scheduled || config.IntervalHours != 12 || config.Enabled {
		t.Fatal("partial schedule API update changed the independent per-turn switch", config)
	}
}

func TestKnowledgeOrganizationResolvedRevisionClearsPortablePendingMetadata(t *testing.T) {
	for _, accept := range []bool{true, false} {
		name := "ignore"
		if accept {
			name = "accept"
		}
		t.Run(name, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			noLegacy := false
			c := VaultConfig{RootMode: "direct", HumanLayout: true, IncludeLegacy: &noLegacy, TaskFolders: true, Enabled: true, Directory: t.TempDir()}
			organizationVault(t, a, c)
			ctx := context.Background()
			base := curatedFixture(t, a, "project", "合成平台", "verified")
			p := KnowledgeRevision{Key: hash("portable candidate"), Content: "采用后应在笔记中显示的新正文。", Evidence: "合成同步验证通过", Applicability: "仅合成同步环境", SourceID: "run:" + uid(), SourceTaskID: uid()}
			queued, _, err := a.store.queueKnowledgeRevision(ctx, base.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			report := syncTestVault(t, a)
			if len(report.Conflicts) != 0 {
				t.Fatal(report)
			}
			paths, _ := a.store.curatedTrackedPaths(ctx)
			relative := paths["curated:"+base.ID]
			fullPath := filepath.Join(c.Directory, filepath.FromSlash(relative))
			raw := readTestFile(t, fullPath)
			if !strings.Contains(raw, "duo_pending_revisions:") {
				t.Fatal("queued revision was not exported", raw)
			}
			if _, err = a.store.resolveKnowledgeRevision(ctx, base.ID, p.Key, queued.Revision, accept); err != nil {
				t.Fatal(err)
			}
			report = syncTestVault(t, a)
			raw = readTestFile(t, fullPath)
			file, err := parseVaultFile(relative, raw)
			if err != nil || len(report.Conflicts) != 0 || strings.Contains(raw, "duo_pending_revisions:") || len(file.Document.PendingRevisions) != 0 || len(file.Document.ReviewKeys) != 1 {
				t.Fatal("resolved portable metadata remained pending or conflicted", report, file.Document, raw, err)
			}
			if accept && (len(file.Document.RevisionHistory) != 1 || file.Document.RevisionHistory[0].SourceID != p.SourceID || file.Document.Status != "observed" || file.Document.Content != p.Content) {
				t.Fatal("accepted portable history or verification state missing", file.Document)
			}
			if !accept && (len(file.Document.RevisionHistory) != 0 || file.Document.Content != base.Content || file.Document.Status != base.Status) {
				t.Fatal("ignoring changed the portable original", file.Document)
			}
			report = syncTestVault(t, a)
			if len(report.Conflicts) != 0 || report.Imported != 0 {
				t.Fatal("an idle scan re-imported a resolved proposal", report)
			}
		})
	}
}
