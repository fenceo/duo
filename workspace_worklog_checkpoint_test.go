package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceExecutionCheckpointIsIndependentAndIdempotent(t *testing.T) {
	base := "---\ntype: task_worklog\ntask_id: fixture-task\n---\n\n# Fixture\n\n"
	state := workspaceTaskStateBlock("## 当前任务状态\n\n目标待核验\n") + "\n\n## 进展记录\n\n人工正文保留。\n"
	content := base + state
	body := "## 执行快照\n\nrun: fixture-run\n阶段：连接中\n"
	updated, conflict := updateWorkspaceExecutionCheckpoint(content, &body)
	if conflict || !strings.Contains(updated, workspaceExecutionCheckpointStart) || !strings.Contains(updated, body) {
		t.Fatalf("checkpoint was not inserted: conflict=%v\n%s", conflict, updated)
	}
	if strings.Index(updated, workspaceExecutionCheckpointStart) > strings.Index(updated, workspaceTaskStateStart) {
		t.Fatal("checkpoint must precede task state for bounded handoff context")
	}
	unchanged, conflict := updateWorkspaceExecutionCheckpoint(updated, &body)
	if conflict || unchanged != updated {
		t.Fatal("same checkpoint should be idempotent")
	}
	next := strings.Replace(body, "连接中", "已连接，等待验证", 1)
	updated, conflict = updateWorkspaceExecutionCheckpoint(updated, &next)
	if conflict || !strings.Contains(updated, "等待验证") || strings.Contains(updated, "连接中") {
		t.Fatal("intact checkpoint was not replaced")
	}
	removed, conflict := updateWorkspaceExecutionCheckpoint(updated, new(string))
	if conflict || removed != content {
		t.Fatal("empty checkpoint did not remove only its owned region")
	}
	manual := strings.Replace(updated, "已连接，等待验证", "人工改写快照", 1)
	protected, conflict := updateWorkspaceExecutionCheckpoint(manual, &body)
	if !conflict || protected != manual {
		t.Fatal("manual checkpoint edits must be preserved")
	}
	newState := "## 当前任务状态\n\n新的已保存任务目标\n"
	stateUpdated, stateConflict := updateWorkspaceWorklogState(manual, newState)
	if stateConflict || !strings.Contains(stateUpdated, "人工改写快照") || !strings.Contains(stateUpdated, "新的已保存任务目标") {
		t.Fatal("a human-edited checkpoint must not invalidate independent task-state ownership")
	}
	malformed := updated + "\n" + workspaceExecutionCheckpointStart + "broken -->\n"
	if got, conflict := updateWorkspaceExecutionCheckpoint(malformed, &body); !conflict || got != malformed {
		t.Fatal("duplicate or malformed checkpoint markers must stop replacement")
	}
	crlf := strings.ReplaceAll(updated, "\n", "\r\n")
	crlfNext := strings.ReplaceAll(next, "\n", "\r\n")
	crlfOut, conflict := updateWorkspaceExecutionCheckpoint(crlf, &crlfNext)
	if conflict || !strings.Contains(crlfOut, "等待验证") || !strings.Contains(crlfOut, "\r\n") {
		t.Fatal("CRLF checkpoint update lost format")
	}
}

func TestWorkspaceExecutionCheckpointLocalIsolationAndTerminalPersistence(t *testing.T) {
	task := Task{ID: "checkpoint-task", Title: "原任务名称", Workspace: t.TempDir(), Environment: &Environment{Type: "windows"}}
	base := workspaceMemoryTaskBase(task)
	mainPath := path.Join(base, workspaceWorklogFilename)
	mainFile := filepath.Join(task.Workspace, filepath.FromSlash(mainPath))
	if err := os.MkdirAll(filepath.Dir(mainFile), 0700); err != nil {
		t.Fatal(err)
	}
	original := workspaceWorklogHeader(task, "") + "\n人工备注保留两个空格  \n"
	if err := os.WriteFile(mainFile, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	// A checkpoint must not rewrite the workspace index or a human source.
	indexFile := filepath.Join(task.Workspace, ".duo", "README.md")
	if err := os.WriteFile(indexFile, []byte("人工工作区索引\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	body := "## 执行快照\n\n<!-- duo:execution-source:" + task.ID + "/checkpoint-run -->\n\n轮次：checkpoint-run · 阶段：检查中\n\n仅作交接资料，尚未验证任务完成。\n"
	file := workspaceMemoryFile{Path: mainPath, Content: "不得替换已有文档", CheckpointOnly: true, ExecutionContent: &body, StateContent: "不得修改任务状态", Entries: []workspaceWorklogEntry{{ID: "invalid", Content: "不得添加时间记录"}}}
	task.Title = "新任务名称"
	report, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{file}})
	if err != nil || report.MainDocument != mainPath || report.Exported != 1 {
		t.Fatal("checkpoint did not find the established canonical directory", report, err)
	}
	during := readWorkspaceWorklog(t, task, mainPath)
	if !strings.Contains(during, body) || !strings.Contains(during, "人工备注保留两个空格  \n") || strings.Contains(during, "不得修改") || strings.Contains(during, "不得添加") || strings.Contains(during, "duo:worklog:") {
		t.Fatal("checkpoint altered terminal state/history or lost human text", during)
	}
	if index, err := os.ReadFile(indexFile); err != nil || string(index) != "人工工作区索引\n" {
		t.Fatal("checkpoint rewrote the workspace index", string(index), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(mainFile), "history")); !os.IsNotExist(err) {
		t.Fatal("checkpoint unexpectedly created/enumerated task history", err)
	}
	emptyPath := file
	emptyPath.Path = ""
	if repeat, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{emptyPath}}); err != nil || repeat.Exported != 0 || readWorkspaceWorklog(t, task, mainPath) != during {
		t.Fatal("empty checkpoint path did not resolve the canonical document", repeat, err)
	}
	invalid := file
	invalid.Path = path.Join(".duo/tasks/foreign-task", workspaceWorklogFilename)
	if _, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{invalid}}); err == nil {
		t.Fatal("checkpoint allowed another task's document")
	}
	if _, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{{Path: mainPath, Content: "forbidden generic write"}}}); err == nil {
		t.Fatal("checkpoint action allowed a generic file write")
	}
	clear := ""
	terminal := workspaceMemoryFile{Path: mainPath, Content: "unused", AppendWorklog: true, ExecutionContent: &clear, Entries: []workspaceWorklogEntry{{ID: "invalid", Content: "bad"}}}
	if _, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "sync", Files: []workspaceMemoryFile{terminal}}); err == nil || readWorkspaceWorklog(t, task, mainPath) != during {
		t.Fatal("an unpersisted terminal source removed the live checkpoint", err)
	}
	terminal.HistoryPending = true
	terminal.Entries = []workspaceWorklogEntry{{ID: task.ID + "/unrelated-old-run", Content: "### 已补全另一条旧记录", Details: true}}
	retained, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "sync", Files: []workspaceMemoryFile{terminal}})
	retainedText := readWorkspaceWorklog(t, task, mainPath)
	if err != nil || !strings.Contains(strings.Join(retained.Warnings, " "), "终态来源尚未写入") || !strings.Contains(retainedText, body) || !strings.Contains(retainedText, "已补全另一条旧记录") {
		t.Fatal("a different history batch retired a source that was not durably appended", retained, err, retainedText)
	}
	terminal.Entries = []workspaceWorklogEntry{{ID: task.ID + "/checkpoint-run", Content: "### 终态 failed\n\n检查失败，问题仍未解决。", Details: true}}
	terminal.HistoryPending = false
	if _, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "sync", Files: []workspaceMemoryFile{terminal}}); err != nil {
		t.Fatal(err)
	}
	completed := readWorkspaceWorklog(t, task, mainPath)
	if strings.Contains(completed, workspaceExecutionCheckpointStart) || strings.Count(completed, workspaceWorklogMarker(task.ID+"/checkpoint-run")) != 1 || !strings.Contains(completed, "检查失败，问题仍未解决") {
		t.Fatal("terminal append lost failure status, duplicated history or retained a live checkpoint", completed)
	}
	if _, err := workspaceMemoryIORequest(ctx, task, workspaceMemoryPayload{Action: "sync", Files: []workspaceMemoryFile{terminal}}); err != nil || readWorkspaceWorklog(t, task, mainPath) != completed {
		t.Fatal("terminal append was not idempotent", err)
	}
}

func TestWorkspaceExecutionCheckpointPOSIXProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	raw, _ := json.Marshal(workspaceMemoryScript)
	program := "script = " + string(raw) + `
import hashlib,json,os,pathlib,subprocess,sys,tempfile
script=script.replace('\r\n','\n')
def sha(text):
    return hashlib.sha256(text.encode('utf-8')).hexdigest()
with tempfile.TemporaryDirectory(prefix='duo-checkpoint-protocol-') as root:
    task='checkpoint-task'
    base='.duo/tasks/原任务-'+sha(task)[:10]
    path=base+'/任务记录.md'
    file=pathlib.Path(root,path)
    file.parent.mkdir(parents=True)
    state='## 当前任务状态\n\n原任务目标\n'
    original='---\ntype: task_worklog\n---\n\n# Fixture\n\n<!-- duo:task-state:'+sha(state)+' -->\n'+state+'<!-- /duo:task-state -->\n\n## 人工备注\n\n保留两个空格  \n'
    file.write_bytes(original.replace('\n','\r\n').encode('utf-8'))
    index=pathlib.Path(root,'.duo/README.md')
    index.write_text('人工索引\n',encoding='utf-8')
    def call(files, action='checkpoint', source=None):
        payload={'root':root,'action':action,'files':files,'task_id':task,'task_directory':'.duo/tasks/新名称-'+sha(task)[:10]}
        result=subprocess.run([sys.executable,'-c',source or script],input=json.dumps(payload),text=True,capture_output=True,timeout=10)
        assert result.returncode==0, result.stderr
        return json.loads(result.stdout)
    body='## 执行快照\n\n<!-- duo:execution-source:'+task+'/checkpoint-run -->\n\n轮次 checkpoint-run，等待验证。\n'
    item={'path':path,'content':'不得覆盖原文件','checkpoint_only':True,'execution_content':body,'entries':[{'id':'invalid','content':'不得追加'}],'state_content':'不得改变任务目标'}
    first=call([item])
    during=file.read_bytes()
    assert not first.get('error') and first['exported']==1 and first['main_document']==path, first
    assert '等待验证'.encode('utf-8') in during and '原任务目标'.encode('utf-8') in during and '不得追加'.encode('utf-8') not in during and b'<!-- duo:worklog:' not in during
    assert during.index(b'<!-- duo:execution-checkpoint:')<during.index(b'<!-- duo:task-state:')
    assert during.endswith('保留两个空格  \r\n'.encode('utf-8')) and index.read_text(encoding='utf-8')=='人工索引\n'
    repeat=call([item])
    assert not repeat.get('error') and repeat['exported']==0 and file.read_bytes()==during, repeat
    empty_path=call([dict(item,path='')])
    assert not empty_path.get('error') and empty_path['exported']==0 and empty_path['main_document']==path and file.read_bytes()==during, empty_path
    forbidden=call([{'path':path,'content':'generic write'}])
    assert forbidden.get('error') and file.read_bytes()==during, forbidden
    foreign=call([dict(item,path='.duo/tasks/foreign-task/任务记录.md')])
    assert foreign.get('error') and file.read_bytes()==during, foreign
    human=during.replace('等待验证'.encode('utf-8'),'人工修改'.encode('utf-8'))
    file.write_bytes(human)
    protected=call([dict(item,execution_content='')])
    assert not protected.get('error') and protected['warnings'] and file.read_bytes()==human, protected
    file.write_bytes(during)
    anchor='            if current != old:\n                raise DocumentConflict(CONFLICT_MESSAGE)'
    staged="            with open(os.path.join(root, relative), 'wb') as external:\n                external.write('external concurrent edit\\n'.encode('utf-8'))\n            current = read(relative)\n"
    assert script.count(anchor)==1, script.count(anchor)
    race=call([dict(item,execution_content='新快照')],source=script.replace(anchor,staged+anchor))
    assert race.get('error_code')=='conflict' and file.read_bytes()==b'external concurrent edit\n', race
    assert not list(file.parent.glob('.duo-*.tmp'))
    file.write_bytes(during)
    failed_terminal={'path':path,'content':'unused','append_worklog':True,'execution_content':'','entries':[{'id':'invalid','content':'invalid terminal'}]}
    invalid=call([failed_terminal],action='sync')
    assert invalid.get('error') and file.read_bytes()==during, invalid
    older=dict(failed_terminal,history_pending=True,entries=[{'id':task+'/unrelated-old-run','content':'另一条旧记录','details':True}])
    retained=call([older],action='sync')
    assert not retained.get('error') and any('终态来源尚未写入' in warning for warning in retained['warnings']) and b'duo:execution-checkpoint:' in file.read_bytes() and '另一条旧记录'.encode('utf-8') in file.read_bytes(), retained
    terminal=dict(failed_terminal,entries=[{'id':task+'/checkpoint-run','content':'### failed\n\n检查失败，未解决。','details':True}])
    done=call([terminal],action='sync')
    completed=file.read_bytes()
    assert not done.get('error') and b'duo:execution-checkpoint:' not in completed and completed.count(('<!-- duo:worklog:'+task+'/checkpoint-run -->').encode('utf-8'))==1, done
    repeated=call([terminal],action='sync')
    assert not repeated.get('error') and file.read_bytes()==completed, repeated
print('workspace-execution-checkpoint-posix-ok')
`
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		distro := os.Getenv("DUO_WORKSPACE_MEMORY_WSL_DISTRO")
		if distro == "" {
			t.Skip("POSIX filesystem protocol needs Linux or explicit DUO_WORKSPACE_MEMORY_WSL_DISTRO")
		}
		cfg := Config{Distro: distro, User: os.Getenv("DUO_WORKSPACE_MEMORY_WSL_USER")}
		cmd = commandWithContext(ctx, nativeTargetCommand(cfg, "python3", "-"))
	} else {
		cmd = exec.CommandContext(ctx, "python3", "-")
	}
	cmd.Stdin = strings.NewReader(program)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "workspace-execution-checkpoint-posix-ok") {
		t.Fatalf("POSIX checkpoint protocol: %v\n%s", err, output)
	}
}

func TestWorkspaceExecutionCheckpointDirectoryDiscoveryLimit(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		task := Task{ID: "limited-checkpoint-task", Title: "新任务名称", Workspace: t.TempDir(), Environment: &Environment{Type: "windows"}}
		oldTask := task
		oldTask.Title = "旧任务名称"
		base := workspaceMemoryTaskBase(oldTask)
		directory := filepath.Join(task.Workspace, ".duo", "tasks")
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 501; i++ {
			if err := os.Mkdir(filepath.Join(directory, fmt.Sprintf("foreign-%04d", i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		mainPath := path.Join(base, workspaceWorklogFilename)
		mainFile := filepath.Join(task.Workspace, filepath.FromSlash(mainPath))
		if err := os.MkdirAll(filepath.Dir(mainFile), 0700); err != nil {
			t.Fatal(err)
		}
		original := workspaceWorklogHeader(oldTask, "") + "\n人工原件保留。\n"
		if err := os.WriteFile(mainFile, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		body := "## 执行快照\n\n仍在检查，未验证任务完成。\n"
		checkpoint := func(current Task) (WorkspaceMemoryReport, error) {
			return workspaceMemoryIORequest(context.Background(), current, workspaceMemoryPayload{Action: "checkpoint", Files: []workspaceMemoryFile{{Content: workspaceWorklogHeader(current, ""), CheckpointOnly: true, ExecutionContent: &body}}})
		}
		report, err := checkpoint(task)
		if err == nil {
			if report.MainDocument != mainPath || !strings.Contains(readWorkspaceWorklog(t, task, mainPath), "人工原件保留") {
				t.Fatal("a confirmed existing directory was not used", report)
			}
		} else if !strings.Contains(err.Error(), "500") || readWorkspaceWorklog(t, task, mainPath) != original {
			t.Fatal("bounded discovery must preserve the original and fail closed", report, err)
		}
		if _, err := os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(task)))); !os.IsNotExist(err) {
			t.Fatal("renaming created a duplicate task directory beyond the discovery bound", err)
		}
		missing := task
		missing.ID = "missing-limited-checkpoint-task"
		if _, err := checkpoint(missing); err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatal("an unconfirmed directory was created after truncating discovery", err)
		}
		if _, err := os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(workspaceMemoryTaskBase(missing)))); !os.IsNotExist(err) {
			t.Fatal("an ambiguous checkpoint created a new directory", err)
		}
		known := task
		known.ID, known.Title = "known-limited-checkpoint-task", "已知原目录"
		knownMain := path.Join(workspaceMemoryTaskBase(known), workspaceWorklogFilename)
		knownFile := filepath.Join(task.Workspace, filepath.FromSlash(knownMain))
		if err := os.MkdirAll(filepath.Dir(knownFile), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(knownFile, []byte(workspaceWorklogHeader(known, "")), 0600); err != nil {
			t.Fatal(err)
		}
		if report, err := checkpoint(known); err != nil || report.MainDocument != knownMain || len(report.Warnings) != 0 || !strings.Contains(readWorkspaceWorklog(t, known, knownMain), body) {
			t.Fatal("a safe directly confirmed requested directory should still work beyond the discovery bound", report, err)
		}
	})
	t.Run("posix", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		raw, _ := json.Marshal(workspaceMemoryScript)
		program := "script = " + string(raw) + `
import hashlib,json,pathlib,subprocess,sys,tempfile
def sha(text):
    return hashlib.sha256(text.encode('utf-8')).hexdigest()
with tempfile.TemporaryDirectory(prefix='duo-checkpoint-directory-limit-') as root:
    task='limited-checkpoint-task'
    base='.duo/tasks/旧任务-'+sha(task)[:10]
    requested='.duo/tasks/新任务-'+sha(task)[:10]
    directory=pathlib.Path(root,'.duo/tasks')
    directory.mkdir(parents=True)
    for i in range(501):
        pathlib.Path(directory,f'foreign-{i:04d}').mkdir()
    original=pathlib.Path(root,base,'任务记录.md')
    original.parent.mkdir()
    original.write_text('# Original\n\n人工原件保留。\n',encoding='utf-8')
    body='## 执行快照\n\n仍在检查，未验证完成。\n'
    def call(identity, target):
        payload={'root':root,'action':'checkpoint','task_id':identity,'task_directory':target,'files':[{'path':'','content':'# New header\n','checkpoint_only':True,'execution_content':body}]}
        result=subprocess.run([sys.executable,'-c',script],input=json.dumps(payload),text=True,capture_output=True,timeout=10)
        assert result.returncode==0, result.stderr
        return json.loads(result.stdout)
    renamed=call(task,requested)
    if renamed.get('error'):
        assert '500' in renamed['error'] and original.read_text(encoding='utf-8')=='# Original\n\n人工原件保留。\n', renamed
    else:
        assert renamed['main_document']==base+'/任务记录.md' and '人工原件保留。' in original.read_text(encoding='utf-8'), renamed
    assert not pathlib.Path(root,requested).exists(), renamed
    missing='missing-limited-checkpoint-task'
    missing_base='.duo/tasks/未确认目录-'+sha(missing)[:10]
    rejected=call(missing,missing_base)
    assert rejected.get('error') and '500' in rejected['error'] and not pathlib.Path(root,missing_base).exists(), rejected
    known='known-limited-checkpoint-task'
    known_base='.duo/tasks/已知原目录-'+sha(known)[:10]
    known_file=pathlib.Path(root,known_base,'任务记录.md')
    known_file.parent.mkdir()
    known_file.write_text('# Known\n',encoding='utf-8')
    confirmed=call(known,known_base)
    assert not confirmed.get('error') and not confirmed['warnings'] and confirmed['main_document']==known_base+'/任务记录.md' and body in known_file.read_text(encoding='utf-8'), confirmed
print('workspace-execution-checkpoint-directory-limit-posix-ok')
`
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			distro := os.Getenv("DUO_WORKSPACE_MEMORY_WSL_DISTRO")
			if distro == "" {
				t.Skip("POSIX filesystem protocol needs Linux or explicit DUO_WORKSPACE_MEMORY_WSL_DISTRO")
			}
			cfg := Config{Distro: distro, User: os.Getenv("DUO_WORKSPACE_MEMORY_WSL_USER")}
			cmd = commandWithContext(ctx, nativeTargetCommand(cfg, "python3", "-"))
		} else {
			cmd = exec.CommandContext(ctx, "python3", "-")
		}
		cmd.Stdin = strings.NewReader(program)
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "workspace-execution-checkpoint-directory-limit-posix-ok") {
			t.Fatalf("POSIX checkpoint bounded directory discovery: %v\n%s", err, output)
		}
	})
}
