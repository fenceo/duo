import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {ctx,document}=await createWebShellFixture(),node=id=>document.getElementById(id);
runInContext(`workCatalog={modes:[
 {id:'work',name:'请求批准',permission:'workspace',approval:'request',builtin:true,allow_network:true},
 {id:'codex:auto',name:'自动审批',permission:'workspace',approval:'auto',builtin:true,allow_network:true},
 {id:'plan',name:'只读分析',permission:'read',approval:'never',builtin:true,allow_network:false},
 {id:'harness:read',name:'只读·可联网',permission:'read',approval:'never',builtin:true,allow_network:true},
 {id:'harness:workspace',name:'Harness 工作区权限',permission:'workspace',approval:'request',builtin:true,allow_network:true},
 {id:'full',name:'完全访问',permission:'full',approval:'never',builtin:true,allow_network:true}],commands:[]};`,ctx);
node('create-engine').value='deepseek-harness';
ctx.modeOptions(node('create-mode'));
ctx.setCreatePermission('auto');
const controls=[...node('create-permission-options').querySelectorAll('button')],control=value=>controls.find(n=>n.dataset.createPermission===value);
assert.equal(node('create-mode').value,'harness:workspace');
assert.equal(control('auto').classList.contains('hidden'),true);
assert.equal(control('read').classList.contains('hidden'),true);
assert.equal(control('request').disabled,false);
assert.equal(control('request').textContent,'工作区权限');
assert.match(node('create-permission-hint').textContent,/需要提升权限时询问/);
ctx.setCreatePermission('full');assert.equal(node('create-mode').value,'full');
node('create-engine').value='codex';ctx.modeOptions(node('create-mode'));ctx.setCreatePermission('auto');
assert.equal(control('auto').classList.contains('hidden'),false);assert.equal(control('auto').disabled,false);assert.equal(control('read').classList.contains('hidden'),false);assert.equal(node('create-mode').value,'codex:auto');
node('create-engine').value='deepseek-harness';ctx.modeOptions(node('create-mode'));ctx.setCreatePermission('read');assert.equal(node('create-mode').value,'harness:workspace','hidden old read selection resets to the official default');
assert.deepEqual(Array.from(ctx.modesForEngine('deepseek-harness'),m=>m.id),['harness:workspace','full']);
assert.equal(ctx.modeSupportsEngine({id:'harness:workspace',permission:'workspace',approval:'request'},'claude'),false);

const task={id:'harness-task',engine:'deepseek-harness',workspace:'/fixture',environment:{name:'Synthetic WSL',type:'wsl',distro:'Synthetic',user:'fixture'},mode:{id:'harness:workspace',permission:'workspace',approval:'request'}},params={sessionId:'native',toolCall:{toolCallId:'call',title:'bash <unsafe>',rawInput:{command:'printf <test>',justification:'fixture'}},options:[{optionId:'native-allow',kind:'allow_once'},{optionId:'native-reject',kind:'reject_once'},{optionId:'permanent',kind:'allow_always'}]},request={id:'permission',task_id:task.id,run_id:'run',method:'harness/requestPermission',params,created:1};
ctx.synthetic={task,runs:[],events:[],approvals:[request]};runInContext("chosen=synthetic.task.id;creatingTask=false;detail=synthetic;renderCodexApprovals(detail)",ctx);
assert.match(node('codex-approvals-title').textContent,/Harness 等待处理/);
assert.equal(node('codex-approval-list').querySelector('unsafe'),null);
assert.match(node('codex-approval-list').textContent,/printf <test>/);
assert.deepEqual(Array.from(ctx.codexApprovalDecisions(request)),['accept','decline','cancel']);
assert.equal(node('codex-approval-list').querySelectorAll('.codex-approval-actions button').length,3);
assert.match(node('codex-approval-list').textContent,/取消请求/);
assert.throws(()=>ctx.codexDecisionBody(request,'acceptForSession'));
assert.deepEqual(Array.from(ctx.codexApprovalDecisions({...request,params:{...params,options:[{optionId:'permanent',kind:'allow_always'}]}})),['cancel']);
let posts=0;ctx.api=async(path,method,body)=>{if(method==='POST'){posts++;assert.equal(path,'tasks/harness-task/approvals/permission');assert.equal(body.decision,'accept');return {ok:true}}return {task,runs:[],events:[],approvals:[]}};
const card=runInContext('Array.from(codexApprovalCards.values())[0]',ctx);await ctx.submitCodexApproval(card,{decision:'accept'});await ctx.submitCodexApproval(card,{decision:'accept'});assert.equal(posts,1);assert.equal(node('codex-approvals').classList.contains('hidden'),true);
runInContext('authenticated=false;renewShellScope()',ctx);
console.log('PASS: official Harness presets, hidden Codex-only controls, engine switching, exact tool details, one-time actions and duplicate-submit isolation. Synthetic only.');
