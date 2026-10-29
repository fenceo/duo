import assert from 'node:assert/strict';
import {runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

// Production initializers and a connected DOM; only HTTP responses are synthetic.
// No live Duo data, files, CLI, hardware or model calls are used.
const {ctx,document}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx),tick=()=>new Promise(r=>setImmediate(r));
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}};
const main=task=>`.duo/tasks/${task}-1234/任务记录.md`;
const state=task=>run(`chosen=${JSON.stringify(task)};selection++;creatingTask=false;detail={task:{id:chosen,title:'Synthetic task',engine:'codex',workspace:'C:/fixture',environment:{}},runs:[],events:[]};tasks=[detail.task];toolsTab='note'`);
const files=new Map(['one','two'].map(task=>[task,{path:main(task),content:'# '+task+'\n\n## 2026-10-29 10:00\n<script>fixture</script>\n原始正文',revision:'v1',editable:true}]));
let calls=[],saveGate=null,failConflict=false,readGate=null,missing=false,enabled=true,syncFails=false;
ctx.api=async(path,method='GET',body)=>{
 calls.push({path,method,body});
 const task=path.match(/^tasks\/([^/]+)\//)?.[1];
 if(path.endsWith('/workspace-memory')){
  if(method==='POST'&&syncFails)throw Error('synthetic sync unavailable');
  return {enabled,task_directory:`.duo/tasks/${task}-1234`,main_document:main(task),exported:method==='POST'?1:0,unchanged:0,tasks:[],documents:[...(missing&&method==='GET'?[]:[{path:main(task),kind:'main'}]),{path:`.duo/tasks/${task}-1234/runs/old.md`,title:'Historical source',kind:'run'}],conflicts:[],warnings:[]};
 }
 if(path.includes('/workspace-memory/document')){
  if(method==='PUT'){
   if(failConflict)throw Object.assign(Error('write conflict'),{status:409});
   if(saveGate)return saveGate.promise;
   const updated={...files.get(task),content:body.content,revision:'v2'};files.set(task,updated);return updated;
  }
  if(readGate)return readGate.promise;
  const relative=new URLSearchParams(path.split('?')[1]).get('path');
  return relative===main(task)?{...files.get(task)}:{path:relative,content:'# Read-only source',editable:false};
 }
 if(path.endsWith('/knowledge'))return [];
 throw Error('Unexpected synthetic request '+path);
};

assert.deepEqual(Array.from(document.querySelectorAll('.library-layers>button')).map(b=>b.dataset.libraryLayer),['consensus','system','project']);
assert(!node('library-history').open);assert(node('library-history').contains(document.querySelector('[data-library-layer=tasks]')));
assert.equal(document.querySelectorAll('#workspace-settings #library-open').length,1);
for(const id of ['library-consensus-open','library-system-open','library-project-open'])assert.equal(node(id),null);
assert.match(ctx.taskItemMenu({id:'one'}),/>任务记录<\/button>/);
assert(node('task-record-history').contains(node('knowledge-list')));assert(!node('task-record-history').open);

state('one');await ctx.openTaskRecords('one');await tick();await tick();
assert.equal(node('workspace-memory-title').textContent,main('one'),'task menu directly opens its primary Markdown');
assert.match(node('workspace-memory-content').textContent,/2026-10-29/);assert.equal(node('workspace-memory-content').querySelector('script'),null);
assert(!node('workspace-memory-content').classList.contains('hidden'));assert(node('workspace-memory-editor').classList.contains('hidden'));
node('workspace-memory-edit').onclick();node('workspace-memory-editor').value='# User draft\n\nAHA 风格连续记录';node('workspace-memory-editor').oninput();
assert(node('workspace-memory-save').disabled===false);
await ctx.loadWorkspaceMemory();assert.equal(node('workspace-memory-editor').value,'# User draft\n\nAHA 风格连续记录','automatic refresh preserves edits');
node('workspace-memory-read').onclick();assert.match(node('workspace-memory-content').textContent,/User draft/,'reading mode previews the unsaved draft safely');
node('workspace-memory-edit').onclick();
state('two');await ctx.loadWorkspaceMemory();assert.equal(node('workspace-memory-title').textContent,main('two'));assert(!node('workspace-memory-editor').value.includes('User draft'));
state('one');await ctx.loadWorkspaceMemory();assert.match(node('workspace-memory-editor').value,/User draft/,'returning to a task restores its own draft');
await ctx.readWorkspaceMemoryDocument(main('one'),true);assert.match(node('workspace-memory-document-state').textContent,/草稿尚未保存/);assert.match(node('workspace-memory-editor').value,/User draft/);

failConflict=true;await ctx.saveWorkspaceMemoryDocument();assert.match(node('workspace-memory-document-state').textContent,/草稿仍保留/);assert.match(node('workspace-memory-editor').value,/User draft/);assert(!node('workspace-memory-save').disabled);
failConflict=false;saveGate=deferred();calls=[];const pendingSave=ctx.saveWorkspaceMemoryDocument();await ctx.saveWorkspaceMemoryDocument();assert.equal(calls.filter(c=>c.method==='PUT').length,1,'double save is locked');
const payload=calls[0].body;assert.equal(payload.path,main('one'));assert.equal(payload.revision,'v1');assert.match(payload.content,/User draft/);
state('two');await ctx.loadWorkspaceMemory();const before=node('workspace-memory-title').textContent;
const saved={...files.get('one'),content:payload.content,revision:'v2'};files.set('one',saved);saveGate.resolve(saved);await pendingSave;saveGate=null;
assert.equal(node('workspace-memory-title').textContent,before,'late save cannot replace another task');
state('one');await ctx.loadWorkspaceMemory();assert.equal(node('workspace-memory-save').disabled,true);assert.match(node('workspace-memory-editor').value,/User draft/);

// Editing after a reload starts also wins over its late response.
readGate=deferred();const reread=ctx.readWorkspaceMemoryDocument(main('one'),true);node('workspace-memory-edit').onclick();node('workspace-memory-editor').value='# Newer local draft';node('workspace-memory-editor').oninput();
readGate.resolve({...saved,content:'# Stale server response'});await reread;readGate=null;assert.equal(node('workspace-memory-editor').value,'# Newer local draft');
files.set('one',{...saved,content:'# External latest',revision:'v3'});await ctx.discardWorkspaceMemoryDocument();assert.equal(node('workspace-memory-editor').value,'# External latest');assert(node('workspace-memory-save').disabled);
await ctx.readWorkspaceMemoryDocument('.duo/tasks/one-1234/runs/old.md');assert(node('workspace-memory-edit').classList.contains('hidden'));calls=[];await ctx.saveWorkspaceMemoryDocument();assert(!calls.length,'read-only sources never issue writes');

// Missing primary documents are initialized once without a model call.
missing=true;enabled=false;state('disabled');calls=[];await ctx.loadWorkspaceMemory();assert(!calls.some(c=>c.method==='POST'));assert.match(node('workspace-memory-state').textContent,/自动记录已关闭/);
enabled=true;state('first-open');files.set('first-open',{path:main('first-open'),content:'# Initial task',revision:'init',editable:true});calls=[];await ctx.loadWorkspaceMemory();assert.equal(calls.filter(c=>c.method==='POST').length,1);assert.equal(node('workspace-memory-title').textContent,main('first-open'));
state('retry');syncFails=true;calls=[];await ctx.loadWorkspaceMemory();assert.match(node('workspace-memory-state').textContent,/synthetic sync unavailable/);await ctx.loadWorkspaceMemory();assert.equal(calls.filter(c=>c.method==='POST').length,1,'failed initialization is not retried in a loop');
syncFails=false;files.set('retry',{path:main('retry'),content:'# Retried',revision:'retry',editable:true});await ctx.loadWorkspaceMemory(true);assert.equal(node('workspace-memory-title').textContent,main('retry'));

// A request from the previous login must not mutate the current UI.
missing=false;state('one');await ctx.loadWorkspaceMemory();readGate=deferred();const oldRead=ctx.readWorkspaceMemoryDocument(main('one'),true);run('shellEpoch++');node('workspace-memory-content').textContent='new login';readGate.resolve({...saved,content:'old private text'});await oldRead;assert.equal(node('workspace-memory-content').textContent,'new login');
console.log('PASS: single knowledge entry, three main layers and collapsed legacy materials; direct task Markdown opening, safe preview, edit/read modes, conditional writes, conflicts, save lock, per-task drafts, late responses, read-only sources, one-shot initialization and explicit retry. Synthetic only.');
