package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeExplicitProjectOverridesMutableTitleWorkspaceAndKeepsSystemReuse(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Title = "T33 调试"
	project, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "Duo", []string{"jianzuo"})
	if err != nil || project == nil {
		t.Fatal(project, err)
	}
	run := knowledgeRecallSourceRun(t, a, task, true)
	for _, spec := range []struct{ layer, scope, title string }{
		{"project", "Duo", "Duo NFS 挂载"},
		{"project", "jianzuo", "jianzuo NFS 挂载"},
		{"project", "t33-64M", "t33 NFS 挂载"},
		{"system", "Linux", "通用 NFS 挂载"},
	} {
		v := CuratedKnowledge{Layer: spec.layer, ScopeKey: spec.scope, Title: spec.title, Content: "NFS 挂载的历史方法。", Status: "observed", Evidence: "合成资料", Applicability: "仅合成环境", SourceID: "run:" + run, SourceTaskID: task.ID}
		if _, _, err = a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	// Renaming/moving/switching engines must not change the explicit project.
	task.Title, task.Workspace, task.Engine = "无项目名的任务", task.Workspace+"-moved", "harness"
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "NFS 挂载")
	if err != nil || len(ids) != 3 || strings.Contains(text, `"scope":"t33-64M"`) || !strings.Contains(text, `"scope":"Linux"`) || !strings.Contains(text, `"scope":"jianzuo"`) {
		t.Fatal("project identity, alias, or system reuse failed", text, ids, err)
	}
	text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "参考 t33-64M 的 NFS 挂载")
	if err != nil || !strings.Contains(text, `"scope":"t33-64M"`) {
		t.Fatal("explicit cross-project reuse blocked", text, err)
	}
}

func TestKnowledgeRecallExcludesInjectedRetrievalAndFalseWordMatches(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Title = "Duo 优化"
	for _, v := range []CuratedKnowledge{
		{Layer: "project", ScopeKey: "t33-64M", Title: "t33 架构", Content: "AI 架构资料", Status: "observed", Evidence: "合成资料", Applicability: "t33"},
		{Layer: "system", ScopeKey: "网络", Title: "SSE", Content: "事件传输历史", Status: "observed", Evidence: "合成资料", Applicability: "通用"},
	} {
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, question := range []string{
		"【Duo 相关知识检索】\n[{\"scope\":\"t33-64M\",\"content\":\"AI 架构\"}]\n【相关知识结束】\n现在的架构已经实现我的需求了吗",
		"【Duo相关知识检索】t33-64M 架构【相关知识结束】\n你好",
		"你好【Duo 相关知识检索】t33-64M 架构",
		"processes",
	} {
		if text, _, err := a.store.knowledgeLookupContext(context.Background(), task, question); err != nil || text != "" {
			t.Fatal("generated context or partial ASCII term caused recall", question, text, err)
		}
	}
	if text, _, err := a.store.knowledgeLookupContext(context.Background(), task, "SSE"); err != nil || !strings.Contains(text, "SSE") {
		t.Fatal("exact system term was blocked", text, err)
	}
}

func TestKnowledgeInferredProjectDoesNotAdmitOtherProjectFromSharedWorkspace(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Title = "Duo 调试"
	run := knowledgeRecallSourceRun(t, a, task, true)
	for _, scope := range []string{"Duo", "t33-64M"} {
		v := CuratedKnowledge{Layer: "project", ScopeKey: scope, Title: scope + " NFS 挂载", Content: "NFS 挂载资料", Status: "observed", Evidence: "合成资料", Applicability: "原项目", SourceID: "run:" + run, SourceTaskID: task.ID}
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "NFS 挂载")
	if err != nil || len(ids) != 1 || strings.Contains(text, "t33-64M") {
		t.Fatal("shared-workspace origin bypassed inferred project", text, ids, err)
	}
}

func TestKnowledgeExplicitCrossProjectRegisteredAliasesAndBoundaries(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	current, other := taskFor(t, a), taskFor(t, a)
	if _, err := a.store.setTaskKnowledgeProject(context.Background(), current.ID, "", "Duo", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.setTaskKnowledgeProject(context.Background(), other.ID, "", "t33-64M", []string{"375", "T31-Lab"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"t33-64M", "T31-Lab"} {
		v := CuratedKnowledge{Layer: "project", ScopeKey: scope, Title: "NFS 挂载方法 " + scope, Content: "NFS 挂载的项目历史方法。", Status: "observed", Evidence: "合成资料", Applicability: "仅对应项目"}
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, question := range []string{"参考 375 的 NFS 挂载", "参考 t31-lab 的 NFS 挂载", "参考 t33-64M 的 NFS 挂载"} {
		text, ids, err := a.store.knowledgeLookupContext(context.Background(), current, question)
		if err != nil || len(ids) != 2 || !strings.Contains(text, `"scope":"t33-64M"`) || !strings.Contains(text, `"scope":"T31-Lab"`) {
			t.Fatal("registered cross-project alias did not admit canonical and alias scopes", question, text, ids, err)
		}
	}
	for _, question := range []string{
		"NFS 挂载",
		"参考 3375 的 NFS 挂载",
		"参考 t31-laboratory 的 NFS 挂载",
		"【Duo 相关知识检索】375 NFS 挂载【相关知识结束】\nNFS 挂载",
	} {
		text, ids, err := a.store.knowledgeLookupContext(context.Background(), current, question)
		if err != nil || len(ids) != 0 || text != "" {
			t.Fatal("implicit, partial or injected alias bypassed project isolation", question, text, ids, err)
		}
	}
}

func TestKnowledgeProjectRegistryReusesIdentityRejectsAmbiguityAndKeepsHistoricalForks(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	duo, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "Duo", []string{"jianzuo"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "DUO", nil)
	if err != nil || duo.ID != again.ID || len(again.Aliases) != 1 {
		t.Fatal("same name did not reuse stable project", again, err)
	}
	for _, spec := range []struct {
		name    string
		aliases []string
	}{
		{"Duo平台", nil}, {"other", []string{"jianzuo"}}, {"AI", nil}, {"secret", []string{"密码：example"}},
	} {
		if _, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", spec.name, spec.aliases); err == nil {
			t.Fatal("ambiguous, generic or secret project accepted", spec)
		}
	}
	run := knowledgeRecallSourceRun(t, a, task, true)
	t33, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "t33-64M", nil)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := a.forkTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := a.store.taskKnowledgeProject(context.Background(), branch.ID)
	if err != nil || selected == nil || selected.ID != t33.ID {
		t.Fatal("fork did not inherit current project", selected, err)
	}
	runs, err := a.store.runs(branch.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	for _, id := range []string{run, runs[0].ID} {
		var raw string
		var snapshot RunExecution
		if err := a.store.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || snapshot.ProjectID != duo.ID {
			t.Fatal("historical project identity reassigned", snapshot, err)
		}
	}
}

func TestKnowledgeProjectHTTPRequiresAuthCSRFAndIdleTask(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	if _, err := a.store.Exec("INSERT INTO sessions VALUES(?,?,?)", hash("knowledge-project-http"), "knowledge-project-csrf", now()+60000); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{app: a}).Handler()
	call := func(method, raw string, authenticated, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/tasks/"+task.ID+"/knowledge/project", strings.NewReader(raw))
		req.Header.Set("Origin", "http://"+req.Host)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "knowledge-project-http"})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", "knowledge-project-csrf")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", "", false, false); w.Code != http.StatusUnauthorized {
		t.Fatal("project metadata exposed without auth", w.Code)
	}
	if w := call("PUT", `{"name":"Duo"}`, true, false); w.Code != http.StatusForbidden {
		t.Fatal("project update ignored CSRF", w.Code)
	}
	w := call("PUT", `{"name":"Duo","aliases":["jianzuo"]}`, true, true)
	var response knowledgeProjectSelection
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Project == nil || len(response.Projects) != 1 {
		t.Fatal("project assignment failed", w.Code, w.Body.String(), err)
	}
	if _, err := a.store.Exec("UPDATE tasks SET status='running' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if w := call("PUT", `{"project_id":""}`, true, true); w.Code != http.StatusConflict {
		t.Fatal("running task scope changed", w.Code, w.Body.String())
	}
	if _, err := a.store.Exec("UPDATE tasks SET status='idle' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	w = call("PUT", `{"project_id":""}`, true, true)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Project != nil || len(response.Projects) != 1 {
		t.Fatal("clear lost registry or kept explicit assignment", w.Code, w.Body.String(), err)
	}
}
