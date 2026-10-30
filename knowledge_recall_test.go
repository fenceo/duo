package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestKnowledgeLookupRelevantBoundedAndRetiredExcluded(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Session = "ongoing-native-session"
	for _, spec := range []struct{ title, status string }{{"NFS挂载排查", "verified"}, {"NFS旧方法", "stale"}, {"显示驱动排查", "observed"}} {
		v := CuratedKnowledge{Layer: "system", ScopeKey: "Linux", Title: spec.title, Content: strings.Repeat(spec.title+"检查目录权限。", 600), Status: spec.status, Applicability: "合成环境", Evidence: "合成测试资料", Tags: []string{spec.title}}
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载排查")
	if err != nil || len(ids) != 1 || !strings.Contains(text, "NFS挂载排查") || strings.Contains(text, "NFS旧方法") || strings.Contains(text, "显示驱动") || !strings.Contains(text, `"truncated":true`) || len([]rune(text)) > 2600 {
		t.Fatal("unexpected lookup", len(text), ids, err)
	}
	if text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "无关问题"); err != nil || text != "" {
		t.Fatal("unrelated note injected", text, err)
	}
	if err = a.store.set("automatic_knowledge", `{"capture":true,"recall":false,"organize":true}`); err != nil {
		t.Fatal(err)
	}
	if text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载排查"); err != nil || text != "" {
		t.Fatal("opt out ignored", text, err)
	}
}

func TestKnowledgeLookupCandidatesStayUnverified(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	v := CuratedKnowledge{Layer: "project", ScopeKey: "375平台", Title: "DDR排查", Content: "历史线索，不是当前结果。", Status: "observed", Applicability: "原项目条件", Evidence: "仅历史回复"}
	if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
		t.Fatal(err)
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "检查375平台的DDR")
	if err != nil || len(ids) != 1 || !strings.Contains(text, `"status":"observed"`) || !strings.Contains(text, "只可作为调查线索") {
		t.Fatal(text, ids, err)
	}
}

func TestKnowledgeLookupKeepsProjectsScopedAndAllowsExplicitReferences(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Title = "Duo优化"
	for _, scope := range []string{"Duo", "T33平台", "375平台"} {
		v := CuratedKnowledge{Layer: "project", ScopeKey: scope, Title: scope + " AI知识架构", Content: "AI检测和知识架构的历史资料。", Status: "observed", Evidence: "合成历史依据", Applicability: "仅原项目", Tags: []string{"AI", "架构"}}
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "优化Duo的AI知识架构")
	if err != nil || len(ids) != 1 || strings.Contains(text, "T33") || strings.Contains(text, "375") || !strings.Contains(text, `"scope":"Duo"`) {
		t.Fatal("unrelated project leaked", text, ids, err)
	}
	text, ids, err = a.store.knowledgeLookupContext(context.Background(), task, "参考375平台的AI知识架构")
	if err != nil || len(ids) != 1 || !strings.Contains(text, `"scope":"375平台"`) {
		t.Fatal("explicit cross-project reference lost", text, ids, err)
	}
	text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "AI 架构")
	if err != nil || text != "" {
		t.Fatal("generic words alone must not cause recall", text, err)
	}
}

func TestKnowledgeLookupUsesActualSourceWorkspace(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	run := knowledgeRecallSourceRun(t, a, task, true)
	v := CuratedKnowledge{Layer: "project", ScopeKey: "与目录名不同的项目代号", Title: "NFS挂载方法", Content: "历史方法。", Status: "observed", Evidence: "合成资料", Applicability: "同工作区", SourceTaskID: task.ID, SourceID: "run:" + run}
	if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
		t.Fatal(err)
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载方法")
	if err != nil || len(ids) != 1 || !strings.Contains(text, v.Title) {
		t.Fatal("same workspace note not recalled", text, ids, err)
	}
	// Moving the source task must not move historical knowledge with it.
	if _, err = a.store.Exec("UPDATE tasks SET workspace=? WHERE id=?", task.Workspace+"-different", task.ID); err != nil {
		t.Fatal(err)
	}
	if text, ids, err = a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载方法"); err != nil || len(ids) != 1 {
		t.Fatal("historical workspace identity followed mutable task", text, ids, err)
	}
	task.Workspace += "-different"
	if text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载方法"); err != nil || text != "" {
		t.Fatal("other workspace note recalled implicitly", text, err)
	}
}

func TestKnowledgeWorkspaceAndScopeIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		a, b Task
		want bool
	}{
		{"windows casing", Task{Workspace: `C:\work\Duo\`, Environment: &Environment{Type: "windows"}}, Task{Workspace: "c:/WORK/duo", Environment: &Environment{Type: "windows"}}, true},
		{"unknown environment", Task{Workspace: "/repo"}, Task{Workspace: "/repo"}, false},
		{"wsl is not windows", Task{Workspace: "/repo"}, Task{Workspace: "/repo", Environment: &Environment{Type: "wsl", Distro: "Ubuntu"}}, false},
		{"wsl casing", Task{Workspace: "/repo", Environment: &Environment{Type: "wsl", Distro: "Ubuntu"}}, Task{Workspace: "/Repo", Environment: &Environment{Type: "wsl", Distro: "Ubuntu"}}, false},
		{"posix backslash is a filename", Task{Workspace: `/repo/a\b`, Environment: &Environment{Type: "wsl", Distro: "Ubuntu"}}, Task{Workspace: "/repo/a/b", Environment: &Environment{Type: "wsl", Distro: "Ubuntu"}}, false},
		{"ssh host", Task{Workspace: "/repo", Environment: &Environment{Type: "ssh", Host: "a"}}, Task{Workspace: "/repo", Environment: &Environment{Type: "ssh", Host: "b"}}, false},
		{"ssh default port", Task{Workspace: "/repo", Environment: &Environment{Type: "ssh", Host: "a"}}, Task{Workspace: "/repo", Environment: &Environment{Type: "ssh", Host: "A", Port: 22}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := knowledgeSameWorkspace(test.a, test.b); got != test.want {
				t.Fatalf("same workspace=%v want %v", got, test.want)
			}
		})
	}
	if knowledgeScopeMentioned("Duo", "Duolingo") || !knowledgeScopeMentioned("375平台", "375调试") || knowledgeScopeMentioned("AI", "AI") {
		t.Fatal("scope boundary or generic scope check failed")
	}
}

func knowledgeRecallSourceRun(t *testing.T, a *App, task Task, snapshot bool) string {
	t.Helper()
	run := uid()
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES(?,?,'synthetic question','chat','web','done','合成资料',?,?)", run, task.ID, now(), now()); err != nil {
		t.Fatal(err)
	}
	if snapshot {
		if err = saveRunExecution(tx, run, task); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestKnowledgeLookupProjectAliasScoresAndLegacyRunsStayConservative(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	run := knowledgeRecallSourceRun(t, a, task, false)
	v := CuratedKnowledge{Layer: "project", ScopeKey: "375平台", Title: "DDR排查", Content: "历史方法。", Status: "observed", Evidence: "合成资料", Applicability: "原项目", SourceTaskID: task.ID, SourceID: "run:" + run}
	if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
		t.Fatal(err)
	}
	if text, _, err := a.store.knowledgeLookupContext(context.Background(), task, "DDR排查"); err != nil || text != "" {
		t.Fatal("legacy source used current task address", text, err)
	}
	if text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "375调试怎么继续"); err != nil || len(ids) != 1 || !strings.Contains(text, v.Title) {
		t.Fatal("normalized project alias not scored", text, ids, err)
	}
}

func TestKnowledgeWorkspaceSnapshotSurvivesForkWithoutReassignment(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	original := taskFor(t, a)
	run := knowledgeRecallSourceRun(t, a, original, true)
	var raw string
	if err := a.store.QueryRow("SELECT snapshot FROM run_execution WHERE run_id=?", run).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot RunExecution
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || len(snapshot.WorkspaceKey) != 64 || strings.Trim(snapshot.WorkspaceKey, "0123456789abcdef") != "" || strings.Contains(snapshot.WorkspaceKey, original.Workspace) {
		t.Fatal("workspace snapshot is not an opaque identity", snapshot.WorkspaceKey, err)
	}
	if _, err := a.store.Exec("UPDATE tasks SET workspace=? WHERE id=?", original.Workspace+"-moved", original.ID); err != nil {
		t.Fatal(err)
	}
	branch, err := a.forkTask(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := a.store.runs(branch.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	v := CuratedKnowledge{Layer: "project", ScopeKey: "与任务名不同的代号", Title: "NFS挂载方法", Content: "历史方法。", Status: "observed", Evidence: "合成资料", Applicability: "原工作区", SourceTaskID: branch.ID, SourceID: "run:" + runs[0].ID}
	if _, _, err = a.store.saveCurated(context.Background(), v, true); err != nil {
		t.Fatal(err)
	}
	if text, _, err := a.store.knowledgeLookupContext(context.Background(), branch, v.Title); err != nil || text != "" {
		t.Fatal("fork reassigned historical run to its current workspace", text, err)
	}
	if text, ids, err := a.store.knowledgeLookupContext(context.Background(), original, v.Title); err != nil || len(ids) != 1 {
		t.Fatal("fork lost original workspace identity", text, ids, err)
	}
}
