package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func extractionMemory() taskMemory {
	evidence := "合成记录中的 NFS 挂载检查通过；这是历史记录，仍需核对设备条件。"
	return taskMemory{Summary: "记录可复用方法", KnowledgeCandidates: []knowledgeCandidate{
		{Layer: "consensus", Title: "NFS 调试约定", Content: "在测试网络中先挂载 /srv/nfs，再按设备管理配置调试。", Evidence: evidence, Applicability: "仅限已授权测试网络", Tags: []string{"nfs", "调试"}},
		{Layer: "system", ScopeKey: "网络", Title: "NFS 挂载检查", Content: "挂载成功后检查目录是否可读，区分服务可达与文件权限问题。", Evidence: evidence, Applicability: "NFS 学习资料，历史结论待复核", Tags: []string{"nfs"}},
		{Layer: "project", ScopeKey: "测试板卡", Title: "板卡调试步骤", Content: "按此板卡的配置引用选择串口，再核对 NFS 路径。", Evidence: evidence, Applicability: "仅限当前测试板卡"},
		{Layer: "project", ScopeKey: "测试板卡", Title: "板卡故障分类", Content: "先区分串口连接与网络挂载失败，再检查各自的设备配置。", Evidence: evidence, Applicability: "仅限当前测试板卡"},
	}}
}

func storeExtractionMemory(t *testing.T, a *App, task Task, memory taskMemory) string {
	t.Helper()
	run := uid()
	result := "历史资料整理：\n"
	for _, item := range memory.KnowledgeCandidates {
		result += item.Evidence + "\n"
	}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,'提取合成记录','chat','web','done',?,?,?)", run, task.ID, result, now(), now()); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(memory)
	if _, err := a.store.Exec("INSERT INTO task_memories VALUES(?,?,?,?)", run, task.ID, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestKnowledgeExtractionClassifiedCandidatesDeduplicateAndNeverSelfVerify(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	memory := extractionMemory()
	run := storeExtractionMemory(t, a, task, memory)
	report, err := a.store.extractTaskKnowledge(context.Background(), task.ID, run, nil)
	if err != nil || report.Created != 4 || report.Duplicates != 0 || len(report.Warnings) != 0 {
		t.Fatal(report, err)
	}
	for _, item := range report.Items {
		if item.Status != "observed" || item.SourceID != "run:"+run || item.SourceTaskID != task.ID || item.SourceHash == "" || len(item.ExtractionKey) != 64 || len(item.Tags) == 0 {
			t.Fatal("missing candidate provenance or unsafe status", item)
		}
	}
	if !strings.Contains(report.Items[0].Content, "/srv/nfs") {
		t.Fatal("meaningful NFS command path removed")
	}
	// Title changes and surrounding whitespace are not another knowledge item.
	memory.KnowledgeCandidates[0].Title = "另一个标题"
	memory.KnowledgeCandidates[0].Content = " \n 在测试网络中先挂载 /srv/nfs，再按设备管理配置调试。 \n"
	storeExtractionMemory(t, a, task, memory)
	repeat, err := a.store.extractTaskKnowledge(context.Background(), task.ID, "", nil)
	if err != nil || repeat.Created != 0 || repeat.Duplicates != 8 {
		t.Fatal("repeated extraction duplicated candidates", repeat, err)
	}
	// Edits and explicit retirement must win over later automated extraction.
	first := report.Items[0]
	first.Content, first.Status = "用户重新整理过的调试流程。", "stale"
	if _, _, err = a.store.saveCurated(context.Background(), first, false); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("UPDATE curated_knowledge SET deleted=1 WHERE id=?", report.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	repeat, err = a.store.extractTaskKnowledge(context.Background(), task.ID, "", nil)
	if err != nil || repeat.Created != 0 || repeat.Duplicates != 8 {
		t.Fatal("edited or deleted candidate resurrected", repeat, err)
	}
	stored, err := a.store.curatedKnowledge(context.Background(), first.ID)
	if err != nil || stored.Content != first.Content || stored.Status != "stale" {
		t.Fatal("automation rewrote user edit", stored, err)
	}
}

func TestKnowledgeExtractionRejectsUngroundedSecretsAndUnstructuredProse(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	memory := extractionMemory()
	memory.KnowledgeCandidates[0].Evidence = "并未出现在回复中的句子"
	result := extractionMemory().KnowledgeCandidates[0].Evidence
	valid := validKnowledgeCandidates(memory.KnowledgeCandidates, result)
	if len(valid) != 3 {
		t.Fatal("accepted ungrounded evidence", valid)
	}
	memory.KnowledgeCandidates[0].Evidence = result
	memory.KnowledgeCandidates[1].Content = "password: forbidden-fixture-value"
	memory.KnowledgeCandidates[2].Layer = "verified"
	memory.KnowledgeCandidates[3].Applicability = ""
	valid = validKnowledgeCandidates(memory.KnowledgeCandidates, result)
	if len(valid) != 1 || valid[0].Layer != "consensus" {
		t.Fatal("accepted unsafe model fields", valid)
	}
	// A normal full answer is not automatically guessed to be a consensus.
	storeExtractionMemory(t, a, task, taskMemory{Summary: "这只是普通摘要", Decisions: []string{"可能试试重新安装"}})
	report, err := a.store.extractTaskKnowledge(context.Background(), task.ID, "", nil)
	if err != nil || report.Created != 0 || len(report.Warnings) == 0 {
		t.Fatal(report, err)
	}
}

func TestKnowledgeExtractionAPIDirectoryGateLayersAndLegacyInsight(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	storeExtractionMemory(t, a, task, extractionMemory())
	request := toolsClient(t, a)
	if reply := request("/api/library/extract", "POST", map[string]any{"task_id": task.ID}, 409); !strings.Contains(string(reply), `"needs_directory":true`) {
		t.Fatal(string(reply))
	}
	setTestVault(t, a, t.TempDir(), false)
	var report KnowledgeExtractionReport
	json.Unmarshal(request("/api/library/extract", "POST", map[string]any{"task_id": task.ID, "layers": []string{"system"}}, 200), &report)
	if report.Created != 1 || len(report.Items) != 1 || report.Items[0].Layer != "system" {
		t.Fatal(report)
	}
	request("/api/library/extract", "POST", map[string]any{"layers": []string{"not-a-layer"}}, 400)
	request("/api/library/extract", "POST", map[string]any{"task_id": "missing-task"}, 404)
	request("/api/library/extraction", "PUT", map[string]bool{"enabled": false}, 200)
	if a.store.knowledgeExtractionEnabled() {
		t.Fatal("automatic extraction opt-out ignored")
	}
	request("/api/library/extraction", "PUT", map[string]any{}, 400)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/library/extraction"},
		{"PUT", "/api/library/extraction"},
		{"POST", "/api/library/extract"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		// Pass the outer origin check so this exercises route authentication.
		req.Header.Set("Origin", "http://"+req.Host)
		(&Server{app: a}).Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatal("unauthenticated extraction access", route.method, route.path, w.Code)
		}
	}
	// Legacy evidence-backed insights can be backfilled as project candidates.
	legacy := taskMemory{Summary: "旧资料", Insights: []memoryInsight{{Title: "合成串口方法", Question: "如何检查串口？", Conclusion: "先执行回环检查。", Evidence: "合成串口回环通过。", Applicability: "仅合成设备", Tags: []string{"串口"}}}}
	run := storeExtractionMemory(t, a, task, legacy)
	a.store.Exec("UPDATE runs SET result='合成串口回环通过。' WHERE id=?", run)
	backfill, err := a.store.extractTaskKnowledge(context.Background(), task.ID, run, nil)
	if err != nil || backfill.Created != 1 || backfill.Items[0].Layer != "project" || backfill.Items[0].Status != "observed" {
		t.Fatal(backfill, err)
	}
}

func TestKnowledgeFoldersCreateMigrateAndPreserveCandidateMetadata(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	run := storeExtractionMemory(t, a, task, extractionMemory())
	report, err := a.store.extractTaskKnowledge(context.Background(), task.ID, run, []string{"consensus"})
	if err != nil || report.Created != 1 {
		t.Fatal(report, err)
	}
	dir := t.TempDir()
	request := toolsClient(t, a)
	config := VaultConfig{Enabled: true, Directory: dir, ConsensusDirectory: "平台共识", SystemDirectory: "学习/系统知识", ProjectDirectory: "项目知识"}
	request("/api/library/vault", "PUT", config, 200)
	sync := syncTestVault(t, a)
	if len(sync.Conflicts) != 0 {
		t.Fatal(sync)
	}
	for _, folder := range []string{"平台共识", "学习/系统知识", "项目知识"} {
		if info, err := os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(folder))); err != nil || !info.IsDir() {
			t.Fatal("layer folder missing", folder, err)
		}
	}
	item := report.Items[0]
	firstPath := configuredCuratedPath(config, curatedDocument(item))
	raw := readTestFile(t, filepath.Join(dir, "Duo", filepath.FromSlash(firstPath)))
	file, err := parseVaultFile(firstPath, raw)
	if err != nil || file.Document.ExtractionKey != item.ExtractionKey || len(file.Document.Tags) == 0 || !strings.Contains(raw, "tags:") {
		t.Fatal("candidate metadata or Obsidian tags missing", file, err)
	}
	config.ConsensusDirectory = "长期/平台约定"
	request("/api/library/vault", "PUT", config, 200)
	sync = syncTestVault(t, a)
	if len(sync.Conflicts) != 0 {
		t.Fatal(sync)
	}
	newPath := configuredCuratedPath(config, curatedDocument(item))
	if _, err = os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(newPath))); err != nil {
		t.Fatal("custom folder migration missing", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "Duo", filepath.FromSlash(firstPath))); !os.IsNotExist(err) {
		t.Fatal("unchanged old candidate file not relocated", err)
	}
	docs, err := a.store.curatedDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, doc := range docs {
		if doc.ID == "curated-index:consensus" {
			found = doc.Path == "长期/平台约定/README.md" && strings.Contains(doc.Content, ".md)")
		}
	}
	if !found {
		t.Fatal("custom layer reading index not updated")
	}
}

func TestKnowledgeFoldersRejectEscapesAndCollisions(t *testing.T) {
	for _, folder := range []string{"../outside", "/absolute", "C:/absolute", "tasks/shared", "knowledge", "system", "CON", "shared/../other", "shared.", ".hidden", "a\nline", "AUX.txt"} {
		c := VaultConfig{ConsensusDirectory: folder}
		if err := prepareKnowledgeFolders(&c); err == nil {
			t.Fatal("unsafe or overlapping layer folder accepted", folder)
		}
	}
	c := VaultConfig{ConsensusDirectory: "知识", SystemDirectory: "知识/系统"}
	if err := prepareKnowledgeFolders(&c); err == nil {
		t.Fatal("nested layer folders accepted")
	}
	c = VaultConfig{ConsensusDirectory: "平台\\共识", SystemDirectory: "系统资料", ProjectDirectory: "项目资料"}
	if err := prepareKnowledgeFolders(&c); err != nil || c.ConsensusDirectory != "平台/共识" {
		t.Fatal(c, err)
	}
}
