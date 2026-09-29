import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';

// LAN HTTP does not expose randomUUID; getRandomValues remains available.
const {ctx,document}=await createWebShellFixture();ctx.crypto={getRandomValues:array=>webcrypto.getRandomValues(array)};
const run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
run(`chosen='live-task';detail={task:{id:chosen,engine:'codex',workspace:'/fixture',environment:{name:'Fixture',type:'wsl'},title:'Live input',status:'running',archived:false,session:'native-fixture'},runs:[{id:'run-1',kind:'chat',status:'running'}],events:[],approvals:[],interaction:{run_id:'run-1',can_steer:true,can_interrupt:true,steering:false,interrupting:false}};tasks=[detail.task];renderTask();`);
ctx.poll=async()=>{};
const draft=text=>{el('message').value=text;run('drafts.set(chosen,input("message").value);updateComposerSendState()')};
draft('只检查失败测试');
assert.equal(el('live-steer').classList.contains('hidden'),false);
assert.equal(el('live-steer').disabled,false);assert.equal(el('live-interrupt').disabled,false);
assert.equal(el('send').textContent,'排队发送');

let finish;const calls=[];ctx.api=async(path,method,body)=>{calls.push({path,method,body});return new Promise(resolve=>finish=resolve)};
const sending=ctx.send(el('message').value,true,'steer');
assert.equal(el('live-steer').disabled,true);assert.equal(el('live-interrupt').disabled,true);
await ctx.send(el('message').value,true,'steer');assert.equal(calls.length,1);
assert.equal(calls[0].path,'tasks/live-task/steer');assert.equal(calls[0].body.expected_run_id,'run-1');
assert.equal(calls[0].body.mode_id,undefined,'steer does not apply selected next-turn permissions');
finish({accepted:true});await sending;assert.equal(el('message').value,'');

// Retry the exact request after a lost response; do not clear drafts or make
// an implicit queued turn when the native result is unknown.
draft('保留同一条引导');let requestID;let attempts=0;
ctx.api=async(path,method,body)=>{assert.match(path,/\/steer$/);attempts++;if(attempts===1){requestID=body.request_id;throw Error('network lost')}assert.equal(body.request_id,requestID);return {accepted:true}};
await ctx.send(el('message').value,true,'steer');assert.equal(el('message').value,'保留同一条引导');
await ctx.send(el('message').value,true,'steer');assert.equal(el('message').value,'');

draft('状态过期也不丢输入');let polls=0;ctx.poll=async()=>{polls++};ctx.api=async()=>{throw Object.assign(Error('old turn'),{status:409})};
await ctx.send(el('message').value,true,'interrupt');assert.equal(el('message').value,'状态过期也不丢输入');assert.equal(polls,1);

run('attachmentDrafts.set(chosen,[{id:"image-1",name:"fixture.png"}]);renderWorkflow()');
assert.equal(el('live-steer').disabled,true);assert.equal(el('live-interrupt').disabled,false);
ctx.api=async()=>{throw Error('steer with attachments must not reach API')};await ctx.send(el('message').value,true,'steer');
ctx.api=async(path,method,body)=>{assert.equal(path,'tasks/live-task/messages');assert.equal(body.delivery,'interrupt');assert.equal(body.expected_run_id,'run-1');assert.equal(body.attachment_ids[0],'image-1');return {id:'replacement'}};
await ctx.send(el('message').value,true,'interrupt');assert.equal(el('message').value,'');assert.equal(run('attachmentDrafts.get(chosen).length'),0);

// An accepted response cannot erase a newer draft or another task's input.
draft('来自 A 的引导');ctx.api=()=>new Promise(resolve=>finish=resolve);const late=ctx.send(el('message').value,true,'steer');
run("chosen='other-task';detail={task:{id:chosen,engine:'claude',status:'idle'},runs:[],events:[]}");el('message').value='B 的草稿';finish({accepted:true});await late;
assert.equal(el('message').value,'B 的草稿');
run('renderWorkflow()');assert(el('live-steer').classList.contains('hidden'));assert(el('live-interrupt').classList.contains('hidden'));

// Native options are an actual answer form, not an approval card. Polling keeps
// the selected option/input node, and nonblocking questions remain answerable.
run(`chosen='question-task';detail={task:{id:chosen,title:'Question',engine:'codex',workspace:'/fixture',environment:{name:'Fixture',type:'wsl'},status:'running'},runs:[{id:'q-run',status:'running'}],events:[],approvals:[{id:'q',task_id:chosen,run_id:'q-run',method:'item/tool/requestUserInput',params:{isBlocking:false,questions:[{id:'scope',header:'修改范围',question:'先做哪一部分？',isOther:true,options:[{label:'先修复',description:'只解决当前问题'},{label:'整体调整',description:'同时整理相关模块'}]}]}}]};renderWorkflow()`);
assert.match(el('codex-approvals-title').textContent,/问题/);
assert.match(el('codex-approval-list').textContent,/AI 可以继续工作/);
assert(!el('codex-approval-list').querySelector('.codex-approval-fields'));
const radio=el('codex-approval-list').querySelector('[data-answer="option"]');radio.checked=true;radio.setAttribute('checked','');
run('detail=JSON.parse(JSON.stringify(detail));renderWorkflow()');assert.equal(el('codex-approval-list').querySelector('[data-answer="option"]'),radio);
el('codex-approvals-expand').onclick();assert.equal(el('codex-requests-dialog').open,true);assert.equal(el('codex-approvals').parentElement,el('codex-requests-dialog'));
assert.equal(el('codex-approval-list').querySelector('[data-answer="option"]'),radio,'expanding must move the live form, preserving selection');
let answerSent;ctx.api=async(path,method,body)=>{if(method==='POST'){answerSent=body;return {ok:true}}return run('({...detail,approvals:[]})')};
el('codex-approval-list').querySelector('.codex-approval-actions button').onclick();
for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(answerSent.answers.scope.answers[0],'先修复');assert(el('codex-approvals').classList.contains('hidden'));
assert.equal(el('codex-requests-dialog').open,false,'resolved questions close the expanded view');assert.notEqual(el('codex-approvals').parentElement,el('codex-requests-dialog'));
console.log('PASS: live steering/interrupt/queue actions, confirmed delivery, retry identity, stale turn refresh, attachment guards, draft isolation, native choice answers and nonblocking question display. Synthetic only.');
