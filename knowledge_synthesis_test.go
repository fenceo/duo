package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addSynthesisSource(t *testing.T, a *App, task Task, at int, status, answer string) string {
	t.Helper()
	id := uid()
	_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,error,created,finished) VALUES(?,? ,?,'chat','web',?,?,?, ?,?)", id, task.ID, fmt.Sprintf("original-request-%02d", at), status, answer, map[bool]string{true: "historical failure"}[status == "failed"], at, at+1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestKnowledgeSynthesisBatchesAllTerminalHistoryAndRetries(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 21; i++ {
		status := []string{"done", "failed", "interrupted"}[i%3]
		ids = append(ids, addSynthesisSource(t, a, task, i, status, fmt.Sprintf("conclusion-%02d", i)))
	}
	addSynthesisSource(t, a, task, 22, "running", "not yet terminal")
	preview, err := a.store.knowledgeSynthesisPreview(ctx, task.ID)
	if err != nil || preview.Remaining != 21 || len(preview.SourceIDs) != 16 || preview.SourceIDs[0] != ids[0] || preview.SourceIDs[15] != ids[15] {
		t.Fatal("historical selection must be oldest-first including failed/stopped", preview, err)
	}
	sources, err := a.store.knowledgeSynthesisSources(ctx, task)
	if err != nil || len(sources) != 16 || sources[1].Status != "failed" || sources[1].Error == "" || !strings.HasSuffix(sources[0].Origin, "/"+ids[0]) {
		t.Fatal(sources, err)
	}
	run := addSynthesisSource(t, a, task, 23, "done", "extraction result")
	_, _ = a.store.Exec("UPDATE runs SET kind='knowledge' WHERE id=?", run)
	input, err := a.knowledgeSynthesisInput(ctx, task, run, "SYNTHESIS REQUEST")
	if err != nil || !strings.Contains(input, "conclusion-00") || !strings.Contains(input, "historical failure") || strings.Contains(input, "conclusion-20") {
		t.Fatal("batch snapshot incorrect", err, input)
	}
	if err := a.store.acknowledgeKnowledgeSynthesis(ctx, task.ID, run, nil); err == nil {
		t.Fatal("invalid model response consumed sources")
	}
	again, _ := a.store.knowledgeSynthesisPreview(ctx, task.ID)
	if again.Remaining != 21 || again.SourceIDs[0] != ids[0] {
		t.Fatal("failure must leave source queue intact", again)
	}
	memory := &taskMemory{Summary: "本批无新增知识"}
	if err := a.store.acknowledgeKnowledgeSynthesis(ctx, task.ID, run, memory); err != nil {
		t.Fatal(err)
	}
	if err := a.store.acknowledgeKnowledgeSynthesis(ctx, task.ID, run, memory); err != nil {
		t.Fatal("ack must be idempotent", err)
	}
	next, _ := a.store.knowledgeSynthesisPreview(ctx, task.ID)
	if next.Remaining != 5 || next.SourceIDs[0] != ids[16] {
		t.Fatal("next batch must continue beyond saved sources", next)
	}
	// An older run finishing late is not lost behind a timestamp cursor.
	_, _ = a.store.Exec("UPDATE runs SET status='failed' WHERE task_id=? AND status='running'", task.ID)
	next, _ = a.store.knowledgeSynthesisPreview(ctx, task.ID)
	if next.Remaining != 6 {
		t.Fatal("late terminal source was skipped", next)
	}
}

func TestKnowledgeSynthesisPreservesBothEndsAndRejectsInvalidCandidates(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	answer := "BEGINNING " + strings.Repeat("中", 14000) + "\nFINAL-CONCLUSION"
	addSynthesisSource(t, a, task, 1, "done", answer)
	items, err := a.store.knowledgeSynthesisSources(context.Background(), task)
	if err != nil || len(items) != 1 || !items[0].Partial || !strings.Contains(items[0].Result, "BEGINNING") || !strings.Contains(items[0].Result, "FINAL-CONCLUSION") || len([]rune(items[0].Result)) > 3500 {
		t.Fatal("head/tail source budget lost conclusion", err, items)
	}
	run := uid()
	reply := "没有新增知识。\n" + memoryMarker(run) + "\n{\"summary\":\"没有新增知识\",\"knowledge_candidates\":[]}\n<!-- /duo-memory:" + run + " -->"
	if !validKnowledgeSynthesisResponse(reply, run) {
		t.Fatal("explicit empty candidates are a successful no-op")
	}
	invalid := strings.Replace(reply, `"knowledge_candidates":[]`, `"knowledge_candidates":[{"layer":"system","scope_key":"测试","title":"不可信","content":"新结论","evidence":"reply does not contain this","applicability":"未验证"}]`, 1)
	if validKnowledgeSynthesisResponse(invalid, run) || validKnowledgeSynthesisResponse("unstructured prose", run) {
		t.Fatal("silently rejected candidates must not advance progress")
	}
	if validKnowledgeSynthesisResponse(strings.Replace(reply, `,"knowledge_candidates":[]`, "", 1), run) || validKnowledgeSynthesisResponse(strings.Replace(reply, `"knowledge_candidates":[]`, `"knowledge_candidates":null`, 1), run) {
		t.Fatal("missing/null candidate field is not an explicit empty result")
	}
}

func TestKnowledgeSynthesisClippedTailsCannotExposeMemoryOrCredentialSuffixes(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	id := addSynthesisSource(t, a, task, 1, "done", "visible-source")
	answer := "Visible fact\n" + memoryMarker(id) + "\n{\"summary\":\"" + strings.Repeat("PRIVATE-METADATA", 600) + "\"}"
	_, _ = a.store.Exec("UPDATE runs SET result=? WHERE id=?", answer, id)
	secret := "TAIL-SECRET-" + strings.Repeat("x", 6000) + "END-OF-SECRET"
	addSynthesisSource(t, a, task, 2, "done", "Beginning\npassword="+secret+"\nSafe final fact")
	_, _ = a.store.Exec("UPDATE runs SET input=? WHERE id=?", "Request\napi_key="+secret+"\nSafe final input", id)
	items, err := a.store.knowledgeSynthesisSources(context.Background(), task)
	if err != nil || len(items) != 2 {
		t.Fatal(err, items)
	}
	raw, _ := json.Marshal(items)
	text := string(raw)
	for _, bad := range []string{"PRIVATE-METADATA", "TAIL-SECRET", "END-OF-SECRET", strings.Repeat("x", 100)} {
		if strings.Contains(text, bad) {
			t.Fatal("clipped context leaked reserved/credential text", bad)
		}
	}
	if !strings.Contains(text, "Safe final fact") || !strings.Contains(text, "Safe final input") || !strings.Contains(text, "Visible fact") {
		t.Fatal("safe complete boundary lines lost", text)
	}
}

func TestKnowledgeSynthesisIncludesEditableDocumentAndStableProject(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	addSynthesisSource(t, a, task, 1, "done", "source fact")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n## 人工补充\nMANUAL-DOC-FACT\n密码：synthetic-secret\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	project, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "Duo", []string{"jianzuo"})
	if err != nil {
		t.Fatal(err)
	}
	run := addSynthesisSource(t, a, task, 2, "done", "extract")
	_, _ = a.store.Exec("UPDATE runs SET kind='knowledge' WHERE id=?", run)
	input, err := a.knowledgeSynthesisInput(context.Background(), task, run, "request")
	if err != nil || !strings.Contains(input, "MANUAL-DOC-FACT") || !strings.Contains(input, project.ID) || !strings.Contains(input, "jianzuo") || strings.Contains(input, "synthetic-secret") {
		t.Fatal("task document/project/redaction missing", err, input)
	}
}

func TestKnowledgeSynthesisAPINoSourcesDoesNotQueue(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	raw, _ := json.Marshal(VaultConfig{Enabled: true, Directory: t.TempDir()})
	_ = a.store.set("knowledge_vault", string(raw))
	request := toolsClient(t, a)
	endpoint := "/api/tasks/" + task.ID + "/knowledge/extract"
	request(endpoint, "GET", nil, 200)
	request(endpoint, "POST", map[string]bool{"confirm": true}, 409)
	var count int
	_ = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=?", task.ID).Scan(&count)
	if count != 0 {
		t.Fatal("no-op queued a real request")
	}
}
