import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
assert.equal(el('desktop-open').parentElement.id,'tabs','desktop sharing belongs to the current task toolbar');
assert(!el('app-menu-items').contains(el('desktop-open')),'desktop sharing is not a global settings item');
run(`chosen='desktop-task';detail={task:{id:chosen,title:'Fixture',engine:'codex'},runs:[],events:[]};tasks=[detail.task]`);
let captures=0,grants=0;ctx.api=async(path,method,body)=>{
 if(path==='desktop/targets')return [{id:'desktop',title:'整个桌面 <unsafe>'}];
 if(path==='desktop')return {active:false,control:false};
 if(path==='tasks/desktop-task/desktop'){grants++;assert.equal(method,'PUT');assert.equal(body.control,false);return {active:true,control:false,task_id:'desktop-task',target:'desktop'}}
 if(path.endsWith('/frame')){captures++;return {image:'synthetic'}}
 throw Error(path);
};
await ctx.openDesktopSharing();assert.equal(captures,0);assert(!el('desktop-allow-control').checked);assert.equal(el('desktop-target').textContent,'整个桌面 <unsafe>');
await ctx.startDesktopSharing();assert.equal(grants,1);assert.match(el('desktop-open').textContent,/接管/);assert.equal(captures,0,'grant does not automatically capture the real desktop');
await ctx.previewDesktop();assert.equal(captures,1);assert.match(el('desktop-preview-image').src,/data:image\/png;base64,/);
await ctx.stopDesktopSharing();assert(!el('desktop-preview-image').hasAttribute('src'));assert.equal(el('desktop-open').textContent,'桌面共享');
run(`desktopState={active:true,control:true,task_id:'other'};renderDesktopSharing()`);assert(el('desktop-start').disabled,'another task share cannot be silently stolen');assert(!el('desktop-stop').disabled,'takeover remains available');
ctx.api=async path=>{if(path==='desktop')return {active:true,control:true,task_id:'desktop-task'};throw Error('window list unavailable')};
await ctx.openDesktopSharing();assert(!el('desktop-stop').disabled,'revoke remains available when window listing fails');assert.match(el('desktop-status').textContent,/window list unavailable/);
// Task switching invalidates pending window lists without moving the AI grant.
let finishTargets,deletes=0;
const ownerState={active:true,control:false,task_id:'desktop-task',target:'desktop'};
ctx.api=async(path,method)=>{
 if(path==='desktop/targets')return new Promise(resolve=>finishTargets=resolve);
 if(path==='desktop'){if(method==='DELETE'){deletes++;return {active:false,control:false}}return ownerState}
 throw Error(path);
};
const pendingOpen=ctx.openDesktopSharing();
run(`chosen='second-task';detail={task:{id:chosen,title:'Second',engine:'codex'},runs:[],events:[]};syncDesktopTaskContext()`);
assert(!el('desktop-dialog').open);assert(!el('desktop-preview-image').hasAttribute('src'));assert.equal(el('desktop-target').textContent,'');
finishTargets([{id:'late-target',title:'Old task window'}]);await pendingOpen;
assert.equal(el('desktop-target').textContent,'','late task A result cannot populate task B');assert.equal(run('desktopState.task_id'),'desktop-task');assert.equal(deletes,0,'switching tasks preserves the original AI authorization');
run('detail=null;syncDesktopTaskContext()');assert(!el('desktop-quick-stop').classList.contains('hidden'),'loading or failed task detail keeps active-share emergency stop available');
run(`detail={task:{id:chosen,title:'Second',engine:'codex'},runs:[],events:[]};syncDesktopTaskContext()`);assert(el('desktop-quick-stop').classList.contains('hidden'),'loaded task has its desktop panel stop instead');
ctx.api=async(path,method)=>{
 if(path==='desktop/targets')return [{id:'desktop',title:'Synthetic desktop'}];
 if(path==='desktop'){if(method==='DELETE'){deletes++;return {active:false,control:false}}return ownerState}
 throw Error(path);
};
await ctx.openDesktopSharing();assert.match(el('desktop-open').textContent,/其他任务/);assert(el('desktop-start').disabled);assert(el('desktop-preview').disabled);assert(el('desktop-manual').disabled);assert(!el('desktop-stop').disabled,'another task can stop sharing without receiving its desktop grant');
await ctx.startDesktopSharing();assert.equal(grants,1);
run(`chosen='';detail=null;creatingTask=true;syncDesktopTaskContext()`);
assert(!el('desktop-dialog').open);assert(!el('desktop-quick-stop').classList.contains('hidden'),'create page keeps an emergency stop for the original task');
el('desktop-quick-stop').click();await new Promise(resolve=>setImmediate(resolve));assert.equal(deletes,1);assert(el('desktop-quick-stop').classList.contains('hidden'));
// A grant that completed after navigation remains attached to its source task
// and must still be visible to the emergency stop without reopening a preview.
run(`creatingTask=false;chosen='desktop-task';detail={task:{id:chosen,title:'Fixture',engine:'codex'},runs:[],events:[]}`);
let finishGrant;
ctx.api=async(path,method)=>{
 if(path==='desktop/targets')return [{id:'desktop',title:'Synthetic desktop'}];
 if(path==='desktop')return {active:false,control:false};
 if(path==='tasks/desktop-task/desktop')return new Promise(resolve=>finishGrant=resolve);
 throw Error(path);
};
await ctx.openDesktopSharing();const pendingGrant=ctx.startDesktopSharing();
run(`chosen='';detail=null;creatingTask=true;syncDesktopTaskContext()`);finishGrant(ownerState);await pendingGrant;
assert.equal(run('desktopState.task_id'),'desktop-task');assert(!el('desktop-dialog').open);assert(!el('desktop-preview-image').hasAttribute('src'));assert(!el('desktop-quick-stop').classList.contains('hidden'));
console.log('PASS: task toolbar desktop entry, explicit view-only sharing, no automatic screenshots, revoke, task-switch isolation, stale window/grant results and create-page emergency stop. Synthetic only.');
