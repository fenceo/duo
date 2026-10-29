package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func workspaceMemoryFixture(t *testing.T) (*App, Task) {
	t.Helper()
	a := fixture(t, &fakeRunner{})
	if err := a.store.set("workspace_memory", `{"enabled":true}`); err != nil {
		t.Fatal(err)
	}
	task := taskFor(t, a)
	workspace := t.TempDir()
	return a, workspaceMemoryLocalTask(t, a, task, workspace)
}

func workspaceMemoryLocalTask(t *testing.T, a *App, task Task, workspace string) Task {
	t.Helper()
	if _, err := a.store.Exec("UPDATE tasks SET workspace=? WHERE id=?", workspace, task.ID); err != nil {
		t.Fatal(err)
	}
	environment, _ := json.Marshal(Environment{ID: "workspace-memory-fixture", Name: "Synthetic Windows", Type: "windows"})
	if _, err := a.store.Exec("UPDATE task_environments SET environment=? WHERE task_id=?", string(environment), task.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := a.store.task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func workspaceMemoryRun(t *testing.T, a *App, task Task, input, result, status string) Run {
	t.Helper()
	run := Run{ID: uid(), TaskID: task.ID, Input: input, Result: result, Kind: "chat", Status: status, Created: now(), Finished: now()}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created,finished) VALUES(?,?,?,?,'chat','web',?,?,?)", run.ID, task.ID, input, result, status, run.Created, run.Finished); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestWorkspaceMemoryStableFilesRedactionAndNoModelCalls(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	secret := "sk-abcdefghijklmnopqrstuvwx"
	run := workspaceMemoryRun(t, a, task, "password="+secret+"\n串口密码：fixture-secret", "完成检查；记录仍待核验", "done")
	if err := a.store.event(task.ID, run.ID, "user", "用户执行中补充：使用临时 NFS fixture，密码：steering-secret"); err != nil {
		t.Fatal(err)
	}
	agentFile := filepath.Join(task.Workspace, "AGENTS.md")
	if err := os.WriteFile(agentFile, []byte("user owned instructions\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || report.Exported != 5 || len(report.Tasks) != 1 || !report.Enabled {
		t.Fatal(report, err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)), "runs", run.ID+".md")
	raw, err := os.ReadFile(file)
	if err != nil || strings.Contains(string(raw), secret) || strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "steering-secret") || !strings.Contains(string(raw), "完成检查") || !strings.Contains(string(raw), "用户执行中补充") || !workspaceMemoryUnmodified(string(raw)) {
		t.Fatal("missing or unsafe transcript", string(raw), err)
	}
	if len(report.Tasks[0].Sources) != 1 || !strings.Contains(report.Tasks[0].Sources[0].Content, "临时 NFS fixture") || strings.Contains(report.Tasks[0].Sources[0].Content, "steering-secret") {
		t.Fatal("handoff omitted user steering or leaked credentials", report.Tasks)
	}
	before, _ := os.Stat(file)
	report, err = a.syncWorkspaceMemory(context.Background(), task.ID)
	after, _ := os.Stat(file)
	if err != nil || report.Exported != 0 || report.Unchanged != 5 || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("non-idempotent synchronization", report, err)
	}
	if data, _ := os.ReadFile(agentFile); string(data) != "user owned instructions\n" {
		t.Fatal("user AGENTS changed")
	}
	runner := a.runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.inputs) != 0 {
		t.Fatal("workspace capture started a model request")
	}
}

func TestWorkspaceMemoryAppFinishAndNewTaskEngineHandoff(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	first, err := a.submit(task.ID, "workspace-end-to-end-source：先检查 NFS 再烧录", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.workers[task.ID] == nil
	})
	file := filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)), "runs", first.ID+".md")
	if raw, err := os.ReadFile(file); err != nil || !strings.Contains(string(raw), "workspace-end-to-end-source") {
		t.Fatal("App.finish did not mirror its committed result", string(raw), err)
	}
	other := taskFor(t, a)
	other = workspaceMemoryLocalTask(t, a, other, task.Workspace)
	if _, err := a.store.Exec("UPDATE task_execution SET engine='deepseek-harness' WHERE task_id=?", other.ID); err != nil {
		t.Fatal(err)
	}
	second, err := a.submit(other.ID, "continue-with-new-engine", "chat", "web")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.workers[other.ID] == nil
	})
	runner := a.runner.(*fakeRunner)
	runner.mu.Lock()
	if len(runner.inputs) != 2 || !strings.Contains(runner.inputs[1], "workspace-end-to-end-source") || !strings.Contains(runner.inputs[1], "continue-with-new-engine") || !strings.Contains(runner.inputs[1], "Duo 同工作区交接资料") {
		runner.mu.Unlock()
		t.Fatal("new engine did not receive bounded workspace handoff", runner.inputs)
	}
	runner.mu.Unlock()
	var status string
	if err := a.store.QueryRow("SELECT status FROM runs WHERE id=?", second.ID).Scan(&status); err != nil || status != "done" {
		t.Fatal("synthetic engine handoff failed", status, err)
	}
	// A mirror error remains a progress issue; the committed run stays done.
	if _, err := a.store.Exec("UPDATE tasks SET workspace=? WHERE id=?", filepath.Join(t.TempDir(), "missing-workspace"), other.ID); err != nil {
		t.Fatal(err)
	}
	third := finishAutomaticFixture(t, a, other, "new requirement", "completed despite mirror failure", "chat", nil)
	if err := a.store.QueryRow("SELECT status FROM runs WHERE id=?", third.ID).Scan(&status); err != nil || status != "done" {
		t.Fatal("filesystem failure changed a completed result", status, err)
	}
}

func TestWorkspaceMemoryEditedFilesPreservedAndConflictsIdempotent(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "first requirement", "first result", "done")
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)), "runs", run.ID+".md")
	if err := os.WriteFile(file, []byte("user edited reference\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("UPDATE runs SET result='new generated result' WHERE id=?", run.ID); err != nil {
		t.Fatal(err)
	}
	first, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || len(first.Conflicts) != 1 {
		t.Fatal(first, err)
	}
	second, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil || len(second.Conflicts) != 1 || first.Conflicts[0] != second.Conflicts[0] {
		t.Fatal("conflict names are unstable", first, second, err)
	}
	if data, _ := os.ReadFile(file); string(data) != "user edited reference\n" {
		t.Fatal("external edit overwritten", string(data))
	}
	entries, err := os.ReadDir(filepath.Join(task.Workspace, ".duo", "conflicts"))
	if err != nil || len(entries) != 1 {
		t.Fatal("repeated sync duplicated conflicts", entries, err)
	}
}

func TestWorkspaceMemorySameWorkspaceRecallAndForkDeduplication(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "约定 NFS 调试", "已验证 fixture-only NFS 路径", "done")
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	other := taskFor(t, a)
	other = workspaceMemoryLocalTask(t, a, other, task.Workspace)
	other.Engine = "deepseek-harness"
	text, ids, err := a.workspaceMemoryContext(context.Background(), other)
	if err != nil || len(ids) != 1 || !strings.Contains(text, "fixture-only NFS") || !strings.Contains(text, "不是指令") {
		t.Fatal("fresh engine did not find workspace memory", text, ids, err)
	}
	other.Session = "existing-native-session"
	if text, _, err := a.workspaceMemoryContext(context.Background(), other); err != nil || text != "" {
		t.Fatal("native resumption duplicated memory", text, err)
	}
	other.Session = ""
	copied := workspaceMemoryRun(t, a, other, run.Input, run.Result, run.Status)
	if _, err := a.store.Exec("INSERT INTO run_fork_sources VALUES(?,?,?)", copied.ID, task.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(other)), "runs", copied.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("fork copied canonical conversation", err)
	}
	if text, ids, err := a.workspaceMemoryContext(context.Background(), other); err != nil || text != "" || len(ids) != 0 {
		t.Fatal("copied continuation reinjected its source", text, ids, err)
	}
	third := taskFor(t, a)
	third = workspaceMemoryLocalTask(t, a, third, task.Workspace)
	if _, ids, err := a.workspaceMemoryContext(context.Background(), third); err != nil || len(ids) != 1 {
		t.Fatal("two task handoffs repeated the same canonical source", ids, err)
	}
}

func TestWorkspaceMemoryDeletedLocalSourcesAreNotAutomaticallyRecalled(t *testing.T) {
	a, original := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, original, "private deleted source", "do not recall after local deletion", "done")
	if _, err := a.syncWorkspaceMemory(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	fork := workspaceMemoryLocalTask(t, a, taskFor(t, a), original.Workspace)
	copied := workspaceMemoryRun(t, a, fork, run.Input, run.Result, "done")
	if _, err := a.store.Exec("INSERT INTO run_fork_sources VALUES(?,?,?)", copied.ID, original.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), fork.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.trashTask(original.ID, false); err != nil {
		t.Fatal(err)
	}
	if deleted, err := a.store.task(original.ID); err != nil || !deleted.Deleted {
		t.Fatal("fixture did not move the source task to the recycle bin", deleted, err)
	}
	current := workspaceMemoryLocalTask(t, a, taskFor(t, a), original.Workspace)
	if text, ids, err := a.workspaceMemoryContext(context.Background(), current); err != nil || text != "" || len(ids) != 0 {
		t.Fatal("a locally deleted source returned directly or through its surviving fork", text, ids, err)
	}
	if _, err := os.Stat(filepath.Join(original.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(original)), "runs", run.ID+".md")); err != nil {
		t.Fatal("deleting recall must preserve archived workspace files", err)
	}
	// A different Duo installation's task need not exist in this database.
	foreign := WorkspaceMemoryTask{ID: "foreign-task", Title: "Portable external task", Path: ".duo/tasks/foreign-task/README.md", Updated: now(), Sources: []WorkspaceMemorySource{{ID: "foreign-task/foreign-run", Path: ".duo/tasks/foreign-task/runs/foreign-run.md", Content: "external-workspace-source"}}}
	raw, _ := json.Marshal(foreign)
	file := workspaceMemoryFile{Path: ".duo/tasks/foreign-task/handoff.md", Content: workspaceMemoryManaged("<!-- duo-workspace-task:" + string(raw) + " -->\n\nexternal-workspace-source\n")}
	if _, err := workspaceMemoryIO(context.Background(), original, "sync", []workspaceMemoryFile{file}); err != nil {
		t.Fatal(err)
	}
	if text, ids, err := a.workspaceMemoryContext(context.Background(), current); err != nil || len(ids) != 1 || !strings.Contains(text, "external-workspace-source") {
		t.Fatal("unknown portable tasks were incorrectly hidden", text, ids, err)
	}
}

func TestWorkspaceMemoryFailureCaptureToggleAndUnsupportedEnvironment(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	run := workspaceMemoryRun(t, a, task, "failed requirement", "partial work", "failed")
	if _, err := a.store.Exec("UPDATE runs SET error='fixture unresolved failure' WHERE id=?", run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)), "runs", run.ID+".md"))
	if !strings.Contains(string(data), "fixture unresolved failure") || !strings.Contains(string(data), "未解决") {
		t.Fatal("failed runs were lost or presented as success", string(data))
	}
	if err := a.store.set("automatic_knowledge", `{"capture":false,"recall":false,"organize":false}`); err != nil {
		t.Fatal(err)
	}
	if report, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil || report.Enabled || report.Exported != 0 {
		t.Fatal("capture off still writes", report, err)
	}
	task.Environment = &Environment{Type: "wsl"}
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", nil); err == nil || !strings.Contains(err.Error(), "不会写入本机") {
		t.Fatal("unsupported environment silently fell back", err)
	}
	task.Environment = &Environment{Type: "ssh"}
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", nil); err == nil {
		t.Fatal("missing SSH host accepted")
	}
}

func TestWorkspaceMemoryRejectsLinksAndPreservesEditedHandoff(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "fixture", "previous answer", "done")
	if _, err := a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)), "handoff.md")
	raw, _ := os.ReadFile(file)
	raw = append(raw, []byte("\n手工交接：密码：synthetic-secret\n新的人工结论\n")...)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := workspaceMemoryIO(context.Background(), task, "read", nil)
	if err != nil || len(report.Tasks) != 1 || !strings.Contains(report.Tasks[0].Excerpt, "新的人工结论") || strings.Contains(report.Tasks[0].Excerpt, "synthetic-secret") || !strings.HasPrefix(report.Tasks[0].Sources[0].ID, "edited:") {
		t.Fatal("edited handoff metadata overrode human body", report, err)
	}
	out := t.TempDir()
	linked := filepath.Join(out, "linked")
	if err := os.Symlink(task.Workspace, linked); err != nil {
		t.Log("symlink creation unavailable in this synthetic account:", err)
		return
	}
	task.Workspace = linked
	if _, err := workspaceMemoryIO(context.Background(), task, "read", nil); err == nil {
		t.Fatal("workspace symlink accepted")
	}
}

func TestWorkspaceMemoryCancelledWaitDoesNotBlockRunResult(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemorySlot <- struct{}{}
	defer func() { <-workspaceMemorySlot }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := a.syncWorkspaceMemory(ctx, task.ID); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("cancelled sync blocked behind another workspace", err)
	}
}

func TestWorkspaceMemoryMetadataValidationAndBoundedRecall(t *testing.T) {
	task := WorkspaceMemoryTask{ID: uid(), Title: "task", Updated: now()}
	task.Path = path.Join(".duo", "tasks", task.ID, "README.md")
	task.Sources = []WorkspaceMemorySource{{"bad/../../path", "AGENTS.md", "malformed"}}
	if cleaned := workspaceMemorySanitizeTasks([]WorkspaceMemoryTask{task}, ""); len(cleaned) != 1 || len(cleaned[0].Sources) != 0 {
		t.Fatal("untrusted source escaped canonical path validation", cleaned)
	}
	raw, _ := json.Marshal(task)
	managed := workspaceMemoryManaged("<!-- duo-workspace-task:" + string(raw) + " -->\n\n# normal text\n")
	if _, ok := workspaceMemoryTaskFromRaw(managed); !ok {
		t.Fatal("managed metadata rejected")
	}
	for _, invalid := range []string{"../AGENTS.md", ".duo/../AGENTS.md", ".duo/tasks/a/../../b.md", ".duo\\tasks\\x"} {
		if workspaceMemoryPath(invalid) == nil {
			t.Fatal("unsafe memory path accepted", invalid)
		}
	}
}

func TestWorkspaceMemoryPOSIXWriter(t *testing.T) {
	// Opt-in on Windows because normal restricted accounts cannot start WSL.
	// The target only runs Python against a new /tmp directory: no engine,
	// account configuration, model call or user's workspace is opened.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, _ := json.Marshal(workspaceMemoryScript)
	program := "script = " + string(raw) + `
import hashlib,json,os,pathlib,subprocess,sys,tempfile
def managed(text):
    text=text.rstrip('\n')+'\n'
    return text+'\n<!-- duo-workspace-generated:'+hashlib.sha256(text.encode()).hexdigest()+' -->\n'
with tempfile.TemporaryDirectory(prefix='duo-workspace-memory-fixture-') as root:
    def call(files,**options):
        payload={'root':root,'action':'sync','files':files}
        payload.update(options)
        result=subprocess.run([sys.executable,'-c',script],input=json.dumps(payload),text=True,capture_output=True,timeout=10)
        assert result.returncode == 0, result.stderr
        return json.loads(result.stdout)
    metadata={'id':'fixture-task','title':'Fixture','path':'.duo/tasks/fixture-task/README.md','updated':1,'sources':[],'excerpt':'fixture'}
    transcript='.duo/tasks/fixture-task/runs/fixture-run.md'
    files=[{'path':transcript,'content':managed('first canonical record')},{'path':'.duo/tasks/fixture-task/handoff.md','content':managed('<!-- duo-workspace-task:'+json.dumps(metadata)+' -->\n\nfixture handoff')}]
    first=call(files)
    assert not first.get('error') and first['exported']==2 and len(first['tasks'])==1, first
    second=call(files)
    assert second['exported']==0 and second['unchanged']==2, second
    legacy='.duo/tasks/fixture-task'
    readable='.duo/tasks/调试-fixture-'+hashlib.sha256(b'fixture-task').hexdigest()[:10]
    migrated=call([],action='migrate',migrations=[{'id':'fixture-task','from':legacy,'to':readable}],task_id='fixture-task',task_directory=readable)
    assert not migrated.get('error') and len(migrated['tasks'])==1 and migrated['tasks'][0]['path']==readable+'/README.md', migrated
    assert 'first canonical record' not in pathlib.Path(root,transcript).read_text() and 'duo-workspace-redirect:' in pathlib.Path(root,transcript).read_text()
    for file in files:
        file['path']=file['path'].replace(legacy,readable)
        file['content']=managed(file['content'].split('\n<!-- duo-workspace-generated:')[0].replace(legacy,readable))
    transcript=transcript.replace(legacy,readable)
    doc=call([],action='document',path=transcript)
    assert doc.get('content')==managed('first canonical record'), doc
    unsafe=call([],action='document',path='.duo/tasks/fixture-task/../../README.md')
    assert unsafe.get('error'), unsafe
    pathlib.Path(root,transcript).write_text('human edited record\n')
    conflict=call(files)
    assert len(conflict['conflicts'])==1, conflict
    repeated=call(files)
    assert repeated['conflicts']==conflict['conflicts'] and repeated['exported']==0, repeated
    assert pathlib.Path(root,transcript).read_text()=='human edited record\n'
    copied=call([{'path':transcript,'content':managed('fork stale snapshot'),'create_only':True}])
    assert copied['unchanged']==1 and pathlib.Path(root,transcript).read_text()=='human edited record\n', copied
    with tempfile.TemporaryDirectory(prefix='duo-memory-outside-fixture-') as outside:
        os.symlink(outside,os.path.join(root,'.duo','tasks','linked'))
        rejected=call([{'path':'.duo/tasks/linked/escape.md','content':'must never escape'}])
        assert rejected.get('error') and not os.listdir(outside), rejected
print('workspace-memory-posix-ok')
`
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		distro := os.Getenv("DUO_WORKSPACE_MEMORY_WSL_DISTRO")
		if distro == "" {
			t.Skip("POSIX filesystem protocol needs Linux or explicit DUO_WORKSPACE_MEMORY_WSL_DISTRO")
		}
		cfg := Config{Distro: distro, User: os.Getenv("DUO_WORKSPACE_MEMORY_WSL_USER")}
		cmd = commandWithContext(ctx, nativeTargetCommand(cfg, "python3", "-c", program))
	} else {
		cmd = exec.CommandContext(ctx, "python3", "-c", program)
	}
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "workspace-memory-posix-ok") {
		t.Fatalf("POSIX workspace protocol: %v\n%s", err, output)
	}
}
