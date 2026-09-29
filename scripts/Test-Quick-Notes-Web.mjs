import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {document,window,ctx}=await createWebShellFixture();
const node=id=>document.getElementById(id),state=code=>runInContext(code,ctx);
const tick=async()=>{for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve))};
assert.equal(node('sticky-expand').getAttribute('aria-expanded'),'true');
assert.equal(node('sticky-board').parentElement,node('sidebar'));
assert(!node('workspace-open'));assert(!node('sidebar-footer'));
assert.equal(node('library-open').parentElement,node('app-menu-items'));
for(const id of ['settings-open','theme-toggle','logout']){assert.equal(node(id).parentElement,node('app-menu-items'));assert.equal(node(id).closest('details'),null)}
assert.match(node('app-version').textContent,/^v/);
assert.equal(node('scratch-tab').parentElement,node('task-actions'),'existing task todos remain directly accessible');
assert(!node('scratch-tab').classList.contains('hidden'));
assert.equal(state('chosen'),'','shared notes work without a selected task');
assert.equal(node('connection').textContent,'');assert(node('connection').classList.contains('hidden'));

let server={revision:0,color:'neutral',items:[]},writes=[],deferred=null,offline=false;
const originalAPI=ctx.api;
ctx.api=async(path,method='GET',data)=>{
 if(path!=='sticky')return originalAPI(path,method,data);
 if(offline)throw new Error('synthetic offline');
 if(method==='GET')return structuredClone(server);
 writes.push(structuredClone(data));
 if(data.revision!==server.revision)throw Object.assign(new Error('changed elsewhere'),{status:409});
 if(deferred)return await new Promise(resolve=>{deferred.resolve=()=>{server={...structuredClone(data),revision:server.revision+1};resolve(structuredClone(server))}});
 server={...structuredClone(data),revision:server.revision+1};return structuredClone(server);
};
const timers=new Map();let timerID=0;
ctx.setTimeout=fn=>{timers.set(++timerID,fn);return timerID};ctx.clearTimeout=id=>timers.delete(id);
let field=document.querySelector('[data-note-input]');
assert(field,'empty board always offers direct input');
await ctx.loadStickyBoard();
assert.equal(document.querySelector('[data-note-input]'),field,'unchanged polling keeps the empty input usable');
field.value='先做这件事';field.oninput();
assert.equal(state('quickNotes.items[0].content'),field.value);
assert.equal(writes.length,0,'typing is debounced');assert.equal(timers.size,1);
field.dispatchEvent(new window.Event('compositionstart'));assert.equal(timers.size,0);
field.value='中文输入中';field.oninput();assert.equal(timers.size,0,'IME composition never autosaves a partial input');
let prevented=false;field.onkeydown({key:'Enter',shiftKey:false,isComposing:true,preventDefault(){prevented=true}});assert(!prevented);
field.dispatchEvent(new window.Event('compositionend'));assert.equal(timers.size,1);
deferred={};const saving=ctx.saveQuickNotes();await ctx.saveQuickNotes();assert.equal(writes.length,1,'duplicate saves are locked');
field.value='保存过程中继续输入';field.oninput();deferred.resolve();await saving;deferred=null;
assert.equal(document.querySelector('[data-note-input]'),field,'save acknowledgement preserves focus and the input node');
assert.equal(field.value,'保存过程中继续输入');assert.equal(state('stickyDirty'),true);
await ctx.saveQuickNotes();assert.equal(server.items[0].content,field.value);assert.equal(writes.at(-1).revision,1);assert.equal(state('stickyDirty'),false);
state("chosen='another-task'");await ctx.loadStickyBoard();assert.equal(field.value,server.items[0].content);
const done=document.querySelector('[data-note-done]');done.checked=true;done.onchange();await ctx.saveQuickNotes();assert.equal(server.items[0].done,true);
document.querySelector('[data-sticky-color="blue"]').onclick();await ctx.saveQuickNotes();assert.equal(server.color,'blue');
document.querySelector('[data-note-delete]').onclick();assert(!node('sticky-undo').classList.contains('hidden'));node('sticky-undo').onclick();await ctx.saveQuickNotes();assert.equal(server.items.length,1,'delete can be undone without losing note content');
field=document.querySelector('[data-note-input]');
offline=true;field.value='断网保留的草稿';field.oninput();await ctx.saveQuickNotes();assert.equal(field.value,'断网保留的草稿');assert.match(node('sticky-save-status').textContent,/未保存/);assert(!node('sticky-retry').classList.contains('hidden'));
offline=false;node('sticky-retry').onclick();await tick();assert.equal(server.items[0].content,'断网保留的草稿');assert.equal(state('stickyDirty'),false);
server.revision++;server.items[0].content='另一个窗口的修改';field.value='我的本地草稿';field.oninput();await ctx.saveQuickNotes();
assert.equal(field.value,'我的本地草稿');assert.equal(server.items[0].content,'另一个窗口的修改');assert.equal(state('stickyConflict'),true);assert(!node('sticky-reload').classList.contains('hidden'));
ctx.confirm=()=>false;node('sticky-reload').onclick();await tick();assert.equal(field.value,'我的本地草稿');
ctx.confirm=()=>true;node('sticky-reload').onclick();await tick();assert.equal(document.querySelector('[data-note-input]').value,'另一个窗口的修改');
// A lost response to a successful save is recoverable without duplicate items.
field=document.querySelector('[data-note-input]');field.value='已提交但响应丢失';field.oninput();
server={...JSON.parse(state('JSON.stringify(stickyPayload())')),revision:server.revision+1};
await ctx.saveQuickNotes();assert.equal(state('stickyDirty'),false);assert.equal(state('stickyConflict'),false);
// Untrusted text stays text, including markup that resembles event handlers.
server={revision:server.revision+1,color:'neutral',items:[{id:'unsafe',content:'<img src=x onerror=alert(1)>',done:false}]};await ctx.loadStickyBoard(true);
assert(!node('sticky-list').querySelector('img'));assert.match(node('sticky-list').querySelector('textarea').value,/<img/);
node('sticky-expand').onclick();assert.equal(node('sticky-expand').getAttribute('aria-expanded'),'false');await ctx.loadStickyBoard();assert(node('sticky-body').classList.contains('hidden'));
node('sticky-expand').onclick();assert.equal(node('sticky-expand').getAttribute('aria-expanded'),'true');
// A late read cannot replace edits made while it was in flight.
let releaseRead;const savedAPI=ctx.api;ctx.api=path=>path==='sticky'?new Promise(resolve=>releaseRead=resolve):savedAPI(path);
const loading=ctx.loadStickyBoard();field=document.querySelector('[data-note-input]');field.value='读取途中输入';field.oninput();releaseRead({revision:99,color:'yellow',items:[]});await loading;assert.equal(field.value,'读取途中输入');assert.equal(state('quickNotes.items[0].content'),'读取途中输入');
state('authenticated=false;renewShellScope()');
console.log('PASS: shared inline notes, debounce/IME, save locks, draft retention, completion/color/delete undo, conflict and lost-response recovery, escaped text, late-read isolation and compact sidebar routes. No model calls.');
