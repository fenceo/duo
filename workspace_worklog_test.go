package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func readWorkspaceWorklog(t *testing.T, task Task, file string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWorkspaceWorklogChronologicalAppendPreservesEditsAndSources(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	insert := func(id, input string, created int64, status string) Run {
		t.Helper()
		run := Run{ID: id, TaskID: task.ID, Input: input, Result: "原回复-" + input, Status: status, Kind: "chat", Created: created, Finished: created + 1}
		if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created,finished) VALUES(?,?,?,?,'chat','web',?,?,?)", id, task.ID, run.Input, run.Result, status, created, created+1); err != nil {
			t.Fatal(err)
		}
		return run
	}
	// Insertion order and lexical IDs both differ from chronological order.
	insert("middle-z", "同毫秒先入队", 20, "done")
	insert("middle-a", "同毫秒后入队", 20, "done")
	insert("latest", "较晚失败", 30, "failed")
	oldest := insert("oldest", "最早要求", 10, "done")
	memory := taskMemory{Summary: "最早结构化进展", Decisions: []string{"保留来源"}, OpenQuestions: []string{"尚未验证硬件"}, Insights: []memoryInsight{{Title: "合成结论", Conclusion: "回环有响应", Evidence: "合成回环收到 OK", Applicability: "仅合成环境"}}}
	raw, _ := json.Marshal(memory)
	if _, err := a.store.Exec("INSERT INTO task_memories(run_id,task_id,payload,updated) VALUES(?,?,?,?)", oldest.ID, task.ID, string(raw), 11); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("UPDATE runs SET error='合成清理失败' WHERE id='latest'"); err != nil {
		t.Fatal(err)
	}
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	main := report.MainDocument
	content := readWorkspaceWorklog(t, task, main)
	last := -1
	for _, id := range []string{"oldest", "middle-z", "middle-a", "latest"} {
		at := strings.Index(content, workspaceWorklogMarker(task.ID+"/"+id))
		if at <= last {
			t.Fatal("worklog is not chronological with rowid ties", id, content)
		}
		last = at
	}
	for _, want := range []string{"type: task_worklog", "## 任务目标", "## 当前计划", "## 备注", "## 进展记录", "最早结构化进展", "合成回环收到 OK", "尚未验证硬件", "尚未整理", "· failed", "合成清理失败", "runs/oldest.md"} {
		if !strings.Contains(content, want) {
			t.Fatal("missing human-readable worklog detail", want, content)
		}
	}
	content = strings.Replace(content, "## 备注\n\n", "## 备注\n\n人工计划：下一步检查电源。\n\n", 1) + "\n人工追加内容\n"
	// Windows editors commonly convert the whole Markdown file to CRLF.
	content = strings.ReplaceAll(content, "\n", "\r\n")
	if err := os.WriteFile(filepath.Join(task.Workspace, filepath.FromSlash(main)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	next := insert("next", "后续轮次", 40, "interrupted")
	if _, err := a.store.Exec("UPDATE tasks SET title='已改名任务' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	report, err = a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || report.MainDocument != main || len(report.Conflicts) != 0 {
		t.Fatal("edited primary document was moved or made a conflict", report, err)
	}
	after := readWorkspaceWorklog(t, task, main)
	// The explicit automatic state region may change; every byte outside it,
	// including CRLF human notes and prior milestones, must remain intact.
	oldState := strings.Index(content, workspaceTaskStateStart)
	oldEnd := strings.Index(content, workspaceTaskStateEnd) + len(workspaceTaskStateEnd)
	newEnd := strings.Index(after, workspaceTaskStateEnd) + len(workspaceTaskStateEnd)
	if !strings.HasPrefix(after, content[:oldState]) || !strings.HasPrefix(after[newEnd:], content[oldEnd:]) || strings.Count(after, workspaceWorklogMarker(task.ID+"/"+next.ID)) != 1 || !strings.Contains(after[:newEnd], "interrupted") {
		t.Fatal("human edits not preserved before new append", after)
	}
	for _, id := range []string{"oldest", "middle-z", "middle-a", "latest"} {
		if strings.Count(after, workspaceWorklogMarker(task.ID+"/"+id)) != 1 {
			t.Fatal("CRLF conversion duplicated source", id)
		}
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil || readWorkspaceWorklog(t, task, main) != after {
		t.Fatal("repeat sync duplicated or rewrote log", err)
	}
}

func TestWorkspaceWorklogRawRoundTripCASAndTaskBoundary(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "requirement", "result", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	main := report.MainDocument
	content := readWorkspaceWorklog(t, task, main) + "\n人工文本 password=synthetic-private 中文\n"
	if err := os.WriteFile(filepath.Join(task.Workspace, filepath.FromSlash(main)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: main})
	if err != nil || !read.Editable || read.Content != content || read.Revision != workspaceMemoryHash(content) {
		t.Fatal("primary MD must read original bytes for safe editing", read, err)
	}
	updated := content + "保存人工修改\n"
	saved, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "save_document", Path: main, Content: updated, Revision: read.Revision})
	if err != nil || saved.Revision != workspaceMemoryHash(updated) || readWorkspaceWorklog(t, task, main) != updated {
		t.Fatal(saved, err)
	}
	for _, revision := range []string{"", read.Revision, strings.Repeat("x", 64)} {
		conflict, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "save_document", Path: main, Content: "must not overwrite", Revision: revision})
		if !errors.Is(err, errWorkspaceDocumentConflict) || conflict.ErrorCode != "conflict" || readWorkspaceWorklog(t, task, main) != updated {
			t.Fatal("stale or absent revision accepted", conflict, err)
		}
	}
	other := workspaceMemoryLocalTask(t, a, taskFor(t, a), task.Workspace)
	for _, invalid := range []string{main, "AGENTS.md", ".duo/tasks/../任务记录.md", path.Join(workspaceMemoryTaskBase(other), "README.md")} {
		if _, err := workspaceMemoryIORequest(context.Background(), other, workspaceMemoryPayload{Action: "save_document", Path: invalid, Content: "overwrite", Revision: saved.Revision}); err == nil {
			t.Fatal("cross-task or unsupported path accepted", invalid)
		}
	}
	if _, err := workspaceMemoryIORequest(context.Background(), other, workspaceMemoryPayload{Action: "document", Path: main}); err == nil {
		t.Fatal("another task primary document exposed as editable")
	}
	legacy, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: path.Join(report.TaskDirectory, "README.md")})
	if err != nil || legacy.Editable || legacy.Revision != "" {
		t.Fatal("legacy generated record should be read-only", legacy, err)
	}
}

func TestWorkspaceWorklogLimitsAndManualOnlyDiscovery(t *testing.T) {
	_, task := workspaceMemoryFixture(t)
	main := path.Join(workspaceMemoryTaskBase(task), workspaceWorklogFilename)
	filename := filepath.Join(task.Workspace, filepath.FromSlash(main))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	manual := "# 人工创建的任务日志\n"
	if err := os.WriteFile(filename, []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := workspaceMemoryIO(context.Background(), task, "read", nil)
	if err != nil || report.MainDocument != main || len(report.Documents) != 1 || report.Documents[0].Path != main {
		t.Fatal("manual-only primary document not discoverable", report, err)
	}
	old := strings.Repeat("x", workspaceMemoryFileLimit-10)
	newText, limited, err := appendWorkspaceWorklog(old, true, "unused", []workspaceWorklogEntry{{ID: task.ID + "/fixture", Content: "cannot fit"}})
	if err != nil || !limited || newText != old {
		t.Fatal("append limit deleted old text or silently truncated", len(newText), limited, err)
	}
	if _, _, err := appendWorkspaceWorklog("", false, "header", []workspaceWorklogEntry{{ID: "bad/../source", Content: "unsafe"}}); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestWorkspaceWorklogRejectsInvalidTextAndPreservesOversizeDocument(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "fixture input", "fixture output", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(task.Workspace, filepath.FromSlash(report.MainDocument))
	for _, invalid := range []string{string([]byte{'#', ' ', 0xff, '\n'}), "# NUL\x00text\n"} {
		if err := os.WriteFile(filename, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "document", Path: report.MainDocument}); err == nil {
			t.Fatal("invalid Markdown was silently converted")
		}
		if _, err := workspaceMemoryIORequest(context.Background(), task, workspaceMemoryPayload{Action: "save_document", Path: report.MainDocument, Content: invalid, Revision: workspaceMemoryHash(invalid)}); err == nil {
			t.Fatal("invalid text was saved")
		}
		if readWorkspaceWorklog(t, task, report.MainDocument) != invalid {
			t.Fatal("invalid external bytes were overwritten")
		}
	}
	oversize := strings.Repeat("x", workspaceMemoryFileLimit+1)
	if err := os.WriteFile(filename, []byte(oversize), 0600); err != nil {
		t.Fatal(err)
	}
	report, err = a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || !strings.Contains(strings.Join(report.Warnings, " "), "超过 1 MiB") || readWorkspaceWorklog(t, task, report.MainDocument) != oversize {
		t.Fatal("oversized human log not preserved with a warning", report, err)
	}
}

func TestWorkspaceWorklogHTTPDirectEditingAndConflict(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "HTTP requirement", "HTTP result", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("worklog-http"), "worklog-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{app: a}).Handler()
	request := func(method, route string, v any, csrf bool) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(v)
		req := httptest.NewRequest(method, "http://127.0.0.1"+route, strings.NewReader(string(raw)))
		req.Header.Set("Origin", "http://127.0.0.1")
		req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "worklog-http"})
		if csrf {
			req.Header.Set("X-CSRF-Token", "worklog-csrf")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	route := "/api/tasks/" + task.ID + "/workspace-memory/document"
	response := request("GET", route+"?path="+report.MainDocument, nil, false)
	var document struct {
		Content  string `json:"content"`
		Revision string `json:"revision"`
		Editable bool   `json:"editable"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil || response.Code != 200 || !document.Editable {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	edit := map[string]string{"path": report.MainDocument, "revision": document.Revision, "content": document.Content + "\n直接编辑成功\n"}
	if response := request("PUT", route, edit, false); response.Code != 403 {
		t.Fatal("save did not enforce CSRF", response.Code, response.Body.String())
	}
	if response := request("PUT", route, edit, true); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := request("PUT", route, edit, true); response.Code != 409 {
		t.Fatal("stale editor did not receive conflict", response.Code, response.Body.String())
	}
	edit["path"] = path.Join(report.TaskDirectory, "README.md")
	if response := request("PUT", route, edit, true); response.Code != 400 {
		t.Fatal("legacy file became writable", response.Code, response.Body.String())
	}
}

func TestWorkspaceWorklogSnapshotDistinguishesEmptyFromMissing(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected string
		existed  bool
		current  string
		readErr  error
		want     bool
	}{
		{"empty file unchanged", "", true, "", nil, true},
		{"empty file externally deleted", "", true, "", os.ErrNotExist, false},
		{"missing file remains missing", "", false, "", os.ErrNotExist, true},
		{"empty file externally created", "", false, "", nil, false},
		{"external edit", "before", true, "after", nil, false},
		{"unreadable file", "before", true, "", os.ErrPermission, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := workspaceMemorySnapshotMatches(test.expected, test.existed, test.current, test.readErr); got != test.want {
				t.Fatal("write-before-CAS failed to distinguish content and existence", got, test.want)
			}
		})
	}
}
