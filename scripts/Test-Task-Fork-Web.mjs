import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const workflow=await readFile(new URL('../web/workflow.ts',import.meta.url),'utf8');
const layout=await readFile(new URL('../web/layout.ts',import.meta.url),'utf8');
const source=workflow.slice(workflow.indexOf('const forkingTasks='),workflow.indexOf('function modeOptions('));
const menu=layout.slice(0,layout.indexOf('function workspaceTaskList('));
function fixture(){
 const calls=[],notices=[],copies=[],node={value:'unsent draft'};
 const ctx=createContext({Set,chosen:'source',selection:1,shellEpoch:0,detail:{task:{id:'source'}},tasks:[{id:'source',status:'done'}],taskView:'active',drafts:new Map(),
  mayLeave:()=>true,input:()=>node,shellCurrent:epoch=>ctx.shellEpoch===epoch,notify:text=>notices.push(text),renderList(){},escapeHTML:value=>value,
  api:async(path,method)=>{calls.push({path,method});return {id:'branch'}},
  choose:async id=>{ctx.chosen=id;ctx.selection++;ctx.detail={task:{id}}},
  navigator:{clipboard:{writeText:async value=>copies.push(value)}},
 });
 runInContext(stripTypeScriptTypes(source+'\n'+menu,{mode:'transform'}),ctx);
 return {ctx,calls,notices,copies,node};
}
{
 const {ctx,calls,node}=fixture();await ctx.forkTask('source');
 assert.equal(calls.length,1);assert.equal(calls[0].path,'tasks/source/fork');
 assert.equal(ctx.chosen,'branch');assert.equal(ctx.drafts.get('source'),'unsent draft');assert.equal(node.value,'');
}
{
 const {ctx,calls}=fixture();let resolve;ctx.api=async(path,method)=>{calls.push({path,method});return new Promise(r=>resolve=r)};
 const first=ctx.forkTask('source');await ctx.forkTask('source');assert.equal(calls.length,1,'double-click must not create two branches');
 ctx.chosen='other';ctx.selection++;resolve({id:'branch'});await first;
 assert.equal(ctx.chosen,'other','late result must not take user away from another task');assert(ctx.tasks.some(t=>t.id==='branch'));
}
{
 const {ctx,calls}=fixture();let resolve;ctx.api=()=>new Promise(r=>resolve=r);
 const pending=ctx.forkTask('source');ctx.shellEpoch++;ctx.chosen='new-login';resolve({id:'old-login-branch'});await pending;
 assert.equal(ctx.chosen,'new-login');assert(!ctx.tasks.some(t=>t.id==='old-login-branch'));
}
{
 const {ctx,calls}=fixture();ctx.tasks[0].status='running';await ctx.forkTask('source');assert.equal(calls.length,0);
 const html=ctx.taskItemMenu({id:'source',status:'running',archived:false});
 assert.match(html,/data-task-action="fork"[^>]*disabled/);assert.match(html,/data-task-action="reopen"/);
}
{
 const {ctx,calls}=fixture();await ctx.reopenTask('source');assert.equal(calls.length,0,'reopening must not submit, fork or reset a native session');
 assert.equal(ctx.chosen,'source');assert.equal(ctx.drafts.get('source'),'unsent draft');
}
{
 const {ctx,copies,calls}=fixture();ctx.api=async(path,method)=>{calls.push({path,method});return {command:'exact reopen command'}};
 await ctx.copyReopenTaskCommand('source');assert.equal(calls[0].path,'tasks/source/open-command');assert.deepEqual(copies,['exact reopen command']);
}
{
 const {ctx,copies}=fixture();let resolve;ctx.api=()=>new Promise(r=>resolve=r);
 const pending=ctx.copyReopenTaskCommand('source');ctx.shellEpoch++;resolve({command:'old login command'});await pending;
 assert.deepEqual(copies,[],'a stale response must not write an old task command to clipboard');
}
console.log('Task fork/reopen web regression passed');
