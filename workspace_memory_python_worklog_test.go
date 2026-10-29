package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Exercise the actual POSIX file protocol, including human edits, external
// writers, renamed task directories and symlinks. All files live in isolated
// temporary directories; no engine, account or user workspace is opened.
func TestWorkspaceMemoryPOSIXWorklogAppendAndCAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	raw, _ := json.Marshal(workspaceMemoryScript)
	program := "script = " + string(raw) + `
import hashlib,json,os,pathlib,subprocess,sys,tempfile
def sha(text):
    return hashlib.sha256(text.encode('utf-8')).hexdigest()
with tempfile.TemporaryDirectory(prefix='duo-worklog-protocol-') as root:
    task='fixture-task'
    base='.duo/tasks/人工任务-'+sha(task)[:10]
    path=base+'/任务记录.md'
    def call(files=None, source=None, **options):
        payload={'root':root,'action':'sync','files':files or [],'task_id':task,'task_directory':base}
        payload.update(options)
        result=subprocess.run([sys.executable,'-c',source or script],input=json.dumps(payload),text=True,capture_output=True,timeout=10)
        assert result.returncode == 0, result.stderr
        return json.loads(result.stdout)
    def entry(identity, content):
        return {'id':identity,'content':content}
    def sync(entries, header='# 任务\n\n'):
        return call([{'path':path,'content':header,'append_worklog':True,'entries':entries}])
    first=sync([entry(task+'/run-one','## 2026-01-01 10:00\n\n首轮结果')])
    assert not first.get('error') and first['exported']==1 and first['main_document']==path, first
    current=pathlib.Path(root,path).read_text(encoding='utf-8')
    assert current.count('<!-- duo:worklog:'+task+'/run-one -->')==1, current
    doc=call(action='document',path=path)
    assert doc['editable'] is True and doc['content']==current and doc['revision']==sha(current), doc
    second=sync([entry(task+'/run-one','不得重写首轮')],header='# 不得覆盖标题\n')
    assert not second.get('error') and second['exported']==0 and second['unchanged']==1, second
    assert pathlib.Path(root,path).read_text(encoding='utf-8')==current
    human='手写开头；保留空白\n\n'+current+'\n手写末尾   \n'
    pathlib.Path(root,path).write_text(human,encoding='utf-8')
    appended=sync([entry(task+'/run-one','不得重复'),entry(task+'/run-two','## 2026-01-01 10:01\n\n次轮结果')])
    current=pathlib.Path(root,path).read_text(encoding='utf-8')
    assert not appended.get('error') and current.startswith(human) and current.count('首轮结果')==1 and '次轮结果' in current, appended
    stale=call(action='save_document',path=path,content='stale replacement',revision=doc['revision'])
    assert stale.get('error_code')=='conflict' and pathlib.Path(root,path).read_text(encoding='utf-8')==current, stale
    missing=call(action='save_document',path=path,content='missing revision')
    assert missing.get('error_code')=='conflict', missing
    edited=current+'\n人工保存内容\n'
    for bad_content in ('text\x00binary', 'invalid surrogate\ud800'):
        invalid_text=call(action='save_document',path=path,content=bad_content,revision=sha(current))
        assert invalid_text.get('error') and pathlib.Path(root,path).read_text(encoding='utf-8')==current, invalid_text
    saved=call(action='save_document',path=path,content=edited,revision=sha(current))
    assert not saved.get('error') and saved['revision']==sha(edited) and saved['editable'] is True, saved
    assert pathlib.Path(root,path).read_text(encoding='utf-8')==edited
    crlf=edited.replace('\n','\r\n')
    pathlib.Path(root,path).write_bytes(crlf.encode('utf-8'))
    windows_edit=sync([entry(task+'/run-one','must not duplicate'),entry(task+'/run-two','must not duplicate')])
    assert not windows_edit.get('error') and windows_edit['exported']==0 and pathlib.Path(root,path).read_bytes()==crlf.encode('utf-8'), windows_edit
    pathlib.Path(root,path).write_bytes(edited.encode('utf-8'))
    renamed='.duo/tasks/改名后-'+sha(task)[:10]
    # A new UI task name must still discover and edit the established directory.
    renamed_doc=call(action='document',path=path,task_directory=renamed)
    assert renamed_doc['main_document']==path and renamed_doc['editable'] is True, renamed_doc
    readme=pathlib.Path(root,base,'README.md')
    readme.write_text('# 旧索引\n',encoding='utf-8')
    old_doc=call(action='document',path=base+'/README.md')
    assert old_doc['editable'] is False, old_doc
    rejected=call(action='save_document',path=base+'/README.md',content='wrong target',revision=sha('# 旧索引\n'))
    assert rejected.get('error') and readme.read_text(encoding='utf-8')=='# 旧索引\n', rejected
    foreign=call(action='save_document',path='.duo/tasks/foreign-task/任务记录.md',content='wrong task',revision=sha(''))
    assert foreign.get('error'), foreign
    invalid=sync([entry('../escape/run','bad')])
    assert invalid.get('error') and pathlib.Path(root,path).read_text(encoding='utf-8')==edited, invalid
    traversal=call(action='document',path=base+'/../README.md')
    assert traversal.get('error'), traversal
    # A deterministic external write between staging and the final comparison
    # must win; only the temporary generated replacement is discarded.
    anchor='            if current != old:\n                raise DocumentConflict(CONFLICT_MESSAGE)'
    staged="            with open(os.path.join(root, relative), 'w', encoding='utf-8') as external:\n                external.write('external concurrent edit\\n')\n            current = read(relative)\n"
    race_script=script.replace(anchor,staged+anchor)
    assert race_script != script
    race=call(action='save_document',path=path,content=edited+'new save\n',revision=sha(edited),source=race_script)
    assert race.get('error_code')=='conflict' and pathlib.Path(root,path).read_text(encoding='utf-8')=='external concurrent edit\n', race
    assert not list(pathlib.Path(root,base).glob('.duo-*.tmp'))
    # Crossing the cap may not truncate old human text or skip ahead to a later
    # smaller entry. Exactly the already fitting chronological prefix is kept.
    large='x'*(1024*1024-200)
    pathlib.Path(root,path).write_text(large,encoding='utf-8')
    full=sync([entry(task+'/fits','small'),entry(task+'/too-large','y'*400),entry(task+'/later','must wait')])
    capped=pathlib.Path(root,path).read_text(encoding='utf-8')
    assert not full.get('error') and full['warnings'] and capped.startswith(large) and '<!-- duo:worklog:'+task+'/fits -->' in capped and 'must wait' not in capped, full
    oversized='z'*(1024*1024+1)
    pathlib.Path(root,path).write_text(oversized,encoding='utf-8')
    over=sync([entry(task+'/after-limit','not appended')])
    assert not over.get('error') and over['warnings'] and pathlib.Path(root,path).read_text(encoding='utf-8')==oversized, over
    pathlib.Path(root,path).write_text(edited,encoding='utf-8')
    save_over=call(action='save_document',path=path,content=oversized,revision=sha(edited))
    assert save_over.get('error') and pathlib.Path(root,path).read_text(encoding='utf-8')==edited, save_over
    with tempfile.TemporaryDirectory(prefix='duo-worklog-outside-') as outside:
        secret=pathlib.Path(outside,'outside.md')
        secret.write_text('outside must stay untouched',encoding='utf-8')
        pathlib.Path(root,path).unlink()
        os.symlink(str(secret),str(pathlib.Path(root,path)))
        symlink=call(action='save_document',path=path,content='escape',revision=sha('outside must stay untouched'))
        assert symlink.get('error') and secret.read_text(encoding='utf-8')=='outside must stay untouched', symlink
        follow=sync([entry(task+'/linked','escape')])
        assert follow.get('error') and secret.read_text(encoding='utf-8')=='outside must stay untouched', follow
        pathlib.Path(root,path).unlink()
        os.symlink(outside,str(pathlib.Path(root,renamed)))
        linked=call([{'path':renamed+'/任务记录.md','content':'escape','append_worklog':True,'entries':[]}],task_directory=renamed)
        assert linked.get('error') and not pathlib.Path(outside,'任务记录.md').exists(), linked
print('workspace-worklog-posix-ok')
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
	if err != nil || !strings.Contains(string(output), "workspace-worklog-posix-ok") {
		t.Fatalf("POSIX worklog protocol: %v\n%s", err, output)
	}
}
