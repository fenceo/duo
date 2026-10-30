package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type workspaceProjectImportResult struct {
	Tasks             int      `json:"tasks"`
	KnowledgeProjects int      `json:"knowledge_projects"`
	ProjectTasks      int      `json:"project_tasks"`
	Warnings          []string `json:"warnings"`
}

func TestWorkspaceKnowledgeProjectZipAndHTTPImportPreserveStableScope(t *testing.T) {
	source := fixture(t, &fakeRunner{})
	one, two := taskFor(t, source), taskFor(t, source)
	project, err := source.store.setTaskKnowledgeProject(context.Background(), one.ID, "", "375平台", []string{"375调试", "T31-375"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.store.setTaskKnowledgeProject(context.Background(), two.ID, project.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	archive, _, err := source.store.workspaceArchiveSnapshot()
	if err != nil || len(archive.Tasks) != 2 {
		t.Fatal(archive.Tasks, err)
	}
	raw, err := buildWorkspaceZip(archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	index, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Tasks []struct {
			Project map[string]json.RawMessage `json:"knowledge_project"`
		} `json:"tasks"`
	}
	if err = json.Unmarshal(index, &exported); err != nil {
		t.Fatal(err)
	}
	for _, task := range exported.Tasks {
		if len(task.Project) != 3 || task.Project["id"] == nil || task.Project["name"] == nil || task.Project["aliases"] == nil {
			t.Fatal("project exported runtime fields or omitted labels", task.Project)
		}
	}
	decoded, err := readWorkspaceZip(raw)
	if err != nil || len(decoded.Archive.Tasks) != 2 || decoded.Archive.Tasks[0].KnowledgeProject == nil || decoded.Archive.Tasks[0].KnowledgeProject.ID != project.ID {
		t.Fatal("project identity was not preserved in ZIP", err)
	}
	destination := fixture(t, &fakeRunner{})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		(&Server{app: destination}).workspaceImport(w, httptest.NewRequest("POST", "/api/workspace/import", bytes.NewReader(raw)))
		var report workspaceProjectImportResult
		if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil || w.Code != http.StatusCreated || report.KnowledgeProjects != 1 || report.ProjectTasks != 2 || len(report.Warnings) != 0 {
			t.Fatal("project archive import failed", w.Code, w.Body.String(), err)
		}
	}
	tasks, err := destination.store.tasks()
	if err != nil || len(tasks) != 4 {
		t.Fatal("workspace import was not additive", tasks, err)
	}
	for _, task := range tasks {
		selected, err := destination.store.taskKnowledgeProject(context.Background(), task.ID)
		if err != nil || selected == nil || selected.ID != project.ID || selected.Name != project.Name || strings.Join(selected.Aliases, ",") != strings.Join(project.Aliases, ",") {
			t.Fatal("import lost stable project scope", selected, err)
		}
	}
	projects, err := destination.store.knowledgeProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatal("repeat import duplicated project identities", projects, err)
	}
}

func TestWorkspaceKnowledgeProjectImportReconcilesWithoutRelabellingLocalData(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	duoTask, t33Task, importedTask := taskFor(t, a), taskFor(t, a), taskFor(t, a)
	duo, err := a.store.setTaskKnowledgeProject(context.Background(), duoTask.ID, "", "Duo", []string{"jianzuo"})
	if err != nil {
		t.Fatal(err)
	}
	t33, err := a.store.setTaskKnowledgeProject(context.Background(), t33Task.ID, "", "t33-64M", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, task string
		project    KnowledgeProject
		wantID     string
		warning    string
	}{
		{"same normalized name", importedTask.ID, KnowledgeProject{ID: "external-duo", Name: "DUO项目", Aliases: []string{"jianzuo"}}, duo.ID, ""},
		{"do not broaden aliases", importedTask.ID, KnowledgeProject{ID: "external-alias", Name: "Duo", Aliases: []string{"new-alias"}}, duo.ID, "未自动合入"},
		{"ambiguous names", importedTask.ID, KnowledgeProject{ID: "external-ambiguous", Name: "Cross", Aliases: []string{"Duo", "t33-64M"}}, duo.ID, "多个已有项目"},
		{"existing assignment wins", duoTask.ID, KnowledgeProject{ID: t33.ID, Name: t33.Name, Aliases: t33.Aliases}, duo.ID, "未改写任务已有"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := a.store.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			report, err := importWorkspaceKnowledgeProjects(tx, []workspaceArchiveTask{{ID: "incoming", KnowledgeProject: &test.project}}, map[string]string{"incoming": test.task})
			if err != nil {
				t.Fatal(err)
			}
			if test.warning != "" && !strings.Contains(strings.Join(report.Warnings, "\n"), test.warning) || test.warning == "" && len(report.Warnings) != 0 {
				t.Fatal("unexpected import warnings", report)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			selected, err := a.store.taskKnowledgeProject(context.Background(), test.task)
			if err != nil || selected == nil || selected.ID != test.wantID {
				t.Fatal("import relabelled existing task project", selected, err)
			}
		})
	}
	// A name conflict is reconciled by labels, but an unrelated reused opaque
	// ID must receive a new ID rather than changing the existing project.
	incoming := KnowledgeProject{ID: duo.ID, Name: "Vega", Aliases: []string{}}
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	report, err := importWorkspaceKnowledgeProjects(tx, []workspaceArchiveTask{{ID: "new", KnowledgeProject: &incoming}}, map[string]string{"new": t33Task.ID})
	if err != nil || !strings.Contains(strings.Join(report.Warnings, "\n"), "已分配新标识") || !strings.Contains(strings.Join(report.Warnings, "\n"), "未改写任务已有") {
		tx.Rollback()
		t.Fatal("opaque identity collision not reported", report, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	projects, err := a.store.knowledgeProjects(context.Background())
	if err != nil || len(projects) != 3 {
		t.Fatal(projects, err)
	}
	for _, project := range projects {
		if project.ID == duo.ID && (project.Name != "Duo" || strings.Join(project.Aliases, ",") != "jianzuo") || project.Name == "Vega" && project.ID == duo.ID {
			t.Fatal("local project metadata was overwritten", project)
		}
	}
	selected, err := a.store.taskKnowledgeProject(context.Background(), t33Task.ID)
	if err != nil || selected.ID != t33.ID {
		t.Fatal("local t33 binding changed", selected, err)
	}
}

func TestWorkspaceKnowledgeProjectLegacyArchiveAndValidationBounds(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	existing, err := a.store.setTaskKnowledgeProject(context.Background(), task.ID, "", "Duo", nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := workspaceArchive{Protocol: workspaceArchiveProtocol, Kind: workspaceArchiveKind, Tasks: []workspaceArchiveTask{{ID: "old-task", Title: "legacy"}}}
	raw, err := buildWorkspaceZip(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	(&Server{app: a}).workspaceImport(w, httptest.NewRequest("POST", "/api/workspace/import", bytes.NewReader(raw)))
	if w.Code != http.StatusCreated {
		t.Fatal("legacy archive import changed", w.Code, w.Body.String())
	}
	all, err := a.store.tasks()
	if err != nil || len(all) != 2 {
		t.Fatal(all, err)
	}
	for _, current := range all {
		selected, err := a.store.taskKnowledgeProject(context.Background(), current.ID)
		if err != nil || current.ID == task.ID && (selected == nil || selected.ID != existing.ID) || current.ID != task.ID && selected != nil {
			t.Fatal("legacy import assigned or changed project", selected, err)
		}
	}
	for _, project := range []KnowledgeProject{
		{ID: "bad/id", Name: "Duo"},
		{ID: "safe", Name: strings.Repeat("字", 81)},
		{ID: "safe", Name: "Duo", Aliases: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
		{ID: "safe", Name: "password=synthetic"},
		{ID: "safe", Name: "Duo", Aliases: []string{"密码：synthetic"}},
		{ID: "safe", Name: `C:\private\project`},
	} {
		if _, err := buildWorkspaceZip(workspaceArchive{Tasks: []workspaceArchiveTask{{KnowledgeProject: &project}}}, nil); err == nil {
			t.Fatal("unsafe or oversized project exported", project)
		}
	}
	conflicting := []workspaceArchiveTask{{KnowledgeProject: &KnowledgeProject{ID: "same", Name: "Duo"}}, {KnowledgeProject: &KnowledgeProject{ID: "same", Name: "Vega"}}}
	if err := validateWorkspaceKnowledgeProjects(conflicting); err == nil {
		t.Fatal("inconsistent project identity accepted")
	}
	oversized := []workspaceArchiveTask{}
	for i := 0; i <= workspaceMaxKnowledgeProjects; i++ {
		oversized = append(oversized, workspaceArchiveTask{KnowledgeProject: &KnowledgeProject{ID: fmt.Sprintf("project-%d", i), Name: fmt.Sprintf("Project-%d", i)}})
	}
	if err := validateWorkspaceKnowledgeProjects(oversized); err == nil {
		t.Fatal("project count bound ignored")
	}
}

func TestWorkspaceKnowledgeProjectImportFailureRollsBackTasksAndRegistry(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	if _, err := a.store.Exec("CREATE TRIGGER fail_project_import BEFORE INSERT ON task_knowledge_projects BEGIN SELECT RAISE(ABORT,'synthetic binding write failure'); END"); err != nil {
		t.Fatal(err)
	}
	archive := workspaceArchive{Protocol: workspaceArchiveProtocol, Kind: workspaceArchiveKind, Tasks: []workspaceArchiveTask{{ID: "incoming", Title: "Imported", KnowledgeProject: &KnowledgeProject{ID: "imported-project", Name: "Imported project"}}}}
	raw, err := buildWorkspaceZip(archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	(&Server{app: a}).workspaceImport(w, httptest.NewRequest("POST", "/api/workspace/import", bytes.NewReader(raw)))
	if w.Code != http.StatusBadRequest {
		t.Fatal("failed binding did not fail import", w.Code, w.Body.String())
	}
	for _, table := range []string{"tasks", "knowledge_projects", "task_knowledge_projects"} {
		var count int
		if err := a.store.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed import left partial project data", table, count, err)
		}
	}
}
