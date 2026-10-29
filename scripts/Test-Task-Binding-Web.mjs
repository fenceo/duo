import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {document,window,ctx}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx);
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
const loaded=run('JSON.parse(JSON.stringify(settings))');loaded.chat='synthetic-private-chat';
const calls=[];let settingsResponse=()=>Promise.resolve(loaded),mutation=()=>Promise.resolve({});
ctx.api=async(path,method='GET',body)=>{calls.push({path,method,body});if(path==='settings')return settingsResponse();if(path.endsWith('/bind'))return mutation();throw new Error('Unexpected synthetic request: '+path)};
ctx.poll=async()=>{};
function select(id){run(`chosen=${JSON.stringify(id)};selection++;detail={task:{id:chosen,title:'Synthetic task',engine:'codex',session:'fixture',status:'done',archived:false},chat:'',runs:[],events:[]};syncTaskDialogContext()`)}

// A settings read belongs to the selected task and must not overwrite an
// unrelated settings editor's unsaved data.
select('a');run("settings.config.workspaces=['unsaved-directory']");
let gate=deferred();settingsResponse=()=>gate.promise;
const pending=ctx.openBinding();select('b');gate.resolve(loaded);await pending;
assert(!node('bind-dialog').open,'late binding read cannot open over another task');
assert.equal(run('settings.config.workspaces[0]'),'unsaved-directory');

settingsResponse=()=>Promise.resolve(loaded);select('a');await ctx.openBinding();
assert(node('bind-dialog').open);assert.equal(node('bind').disabled,false);
assert.equal(run('settings.chat'),'','opening task binding must not replace the global settings object');
gate=deferred();mutation=()=>gate.promise;
const first=ctx.setBinding(true);await ctx.setBinding(true);
assert.equal(calls.filter(c=>c.method==='POST').length,1,'double click sends one binding mutation');
assert.equal(calls.at(-1).path,'tasks/a/bind');assert.equal(calls.at(-1).body.chat,'synthetic-private-chat');
select('b');assert(!node('bind-dialog').open);gate.resolve({});await first;
const before=calls.length;await ctx.setBinding(true);await ctx.setBinding(false);
assert.equal(calls.length,before,'stale dialog cannot bind or unbind the new task');

// Even returning to the same task invalidates the original selection token.
select('a');gate=deferred();settingsResponse=()=>gate.promise;
const old=ctx.openBinding();select('b');select('a');gate.resolve(loaded);await old;
assert(!node('bind-dialog').open,'returning to a task does not revive an old request');

// Native dialog close events are queued. A close from an older opening must
// not erase the target of a newly opened dialog.
settingsResponse=()=>Promise.resolve(loaded);await ctx.openBinding();
const dialog=node('bind-dialog'),close=dialog.close;
dialog.close=function(){this.open=false};dialog.close();await ctx.openBinding();
dialog.dispatchEvent(new window.Event('close'));await ctx.setBinding(false);
assert.equal(calls.at(-1).path,'tasks/a/bind');assert.equal(calls.at(-1).method,'DELETE');
// An older close arriving while the next opening is still loading must not
// cancel that opening either.
await ctx.openBinding();dialog.close();gate=deferred();settingsResponse=()=>gate.promise;
const reopening=ctx.openBinding();dialog.dispatchEvent(new window.Event('close'));gate.resolve(loaded);await reopening;
assert(dialog.open,'queued close must not cancel a newer binding read');
dialog.close=close;dialog.close();settingsResponse=()=>Promise.resolve(loaded);

// The setup action also captures the task, rather than using a later chosen ID.
await ctx.openBinding();node('bind-setup').onclick();
assert.equal(run('setupTask'),'a');assert(node('setup-dialog').open);node('setup-dialog').close();
await ctx.openBinding();select('b');node('bind-setup').onclick();
assert(!node('setup-dialog').open,'stale setup action cannot attach another task');

select('a');gate=deferred();settingsResponse=()=>gate.promise;
const staleShell=ctx.openBinding();run('authenticated=false;renewShellScope()');gate.resolve(loaded);await staleShell;
assert(!node('bind-dialog').open,'late binding read cannot cross login shells');
assert(calls.every(c=>c.path==='settings'||c.path.endsWith('/bind')));
console.log('PASS: task binding preserves settings drafts, captures its task/chat, blocks duplicate and stale mutations, isolates task/shell changes and queued close events. Synthetic only; no Feishu messages or model calls.');
