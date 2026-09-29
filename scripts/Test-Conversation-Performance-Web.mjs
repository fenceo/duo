import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {conversationStressDetail} from './Conversation-Performance-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {ctx,document,window}=await createWebShellFixture();
ctx.fixtureDetail=conversationStressDetail();
const run=code=>runInContext(code,ctx);
const start=performance.now();
run("chosen=fixtureDetail.task.id;detail=fixtureDetail;tasks=[detail.task];resetConversation();element('conversation').replaceChildren();appendEvents(detail.events);renderTask();");
const mountMs=performance.now()-start;
const conversation=document.getElementById('conversation');
const mountedNodes=conversation.querySelectorAll('*').length;
const pollStart=performance.now();
for(let i=0;i<5;i++)ctx.appendEvents([]);
const idlePollMs=(performance.now()-pollStart)/5;
let workflowCalls=0;
const workflow=ctx.renderWorkflow;ctx.renderWorkflow=()=>{workflowCalls++;return workflow()};
const composer=document.getElementById('message'),inputStart=performance.now();
for(let i=0;i<20;i++){composer.value='模拟中文输入 '+i;composer.dispatchEvent(new window.Event('input'))}
const inputMs=(performance.now()-inputStart)/20;
console.log(JSON.stringify({events:ctx.fixtureDetail.events.length,mountedNodes,mountMs,idlePollMs,inputMs,workflowCalls}));
assert(mountedNodes<800,'closed execution records must not mount thousands of hidden nodes');
assert.equal(workflowCalls,0,'typing must not refresh workflow, attachments or approval forms');
assert.equal(run('drafts.get(chosen)'),composer.value,'typing keeps the complete draft');
assert.equal(document.getElementById('send').disabled,false);

// Unchanged polling must not mutate history or force synchronous layout, even
// if the server returned fresh objects instead of sharing the previous arrays.
let writes=0,layoutReads=0;
const restore=[];
for(const node of [conversation,...conversation.querySelectorAll('*')]){
 const set=node.setAttribute.bind(node);node.setAttribute=(...args)=>{writes++;return set(...args)};restore.push(()=>{node.setAttribute=set});
 for(const property of ['scrollHeight','clientHeight','scrollTop']){
  const original=Object.getOwnPropertyDescriptor(node,property);
  Object.defineProperty(node,property,{configurable:true,get(){layoutReads++;return 0},set(){writes++}});
  restore.push(()=>{if(original)Object.defineProperty(node,property,original);else delete node[property]});
 }
}
run('detail=JSON.parse(JSON.stringify(detail));');
ctx.appendEvents([]);
assert.equal(writes,0,'an idle poll must not rewrite event attributes or scroll position');
assert.equal(layoutReads,0,'an idle poll must not force conversation layout');
restore.forEach(fn=>fn());

composer.value='';composer.dispatchEvent(new window.Event('input'));assert.equal(document.getElementById('send').disabled,true);
run("attachmentDrafts.set(chosen,[{id:'file',name:'fixture.txt'}]);updateComposerSendState()");assert.equal(document.getElementById('send').disabled,false,'attachment-only sends remain available');
run("pendingUploadFiles.set(chosen,[{}]);updateComposerSendState()");assert.equal(document.getElementById('send').disabled,true,'a failed upload cannot be silently omitted');
run("pendingUploadFiles.clear();attachmentDrafts.clear();sending=true;updateComposerSendState()");assert.equal(document.getElementById('send').disabled,true);
run('sending=false;detail.task.archived=true;');composer.value='draft';composer.dispatchEvent(new window.Event('input'));assert.equal(document.getElementById('send').disabled,true);
run('detail.task.archived=false;');
let commands=0,submissions=0;const openCommands=ctx.openCommands;ctx.openCommands=()=>commands++;
composer.value='/';const composing=new window.Event('input');composing.isComposing=true;composer.dispatchEvent(composing);assert.equal(commands,0,'IME composition must not open the command menu');
composer.dispatchEvent(new window.Event('input'));assert.equal(commands,1,'slash opens the command menu exactly once');ctx.openCommands=openCommands;
document.getElementById('composer').requestSubmit=()=>submissions++;
const enter=(isComposing,shiftKey)=>{const event=new window.Event('keydown');event.key='Enter';event.isComposing=isComposing;event.shiftKey=shiftKey;composer.dispatchEvent(event)};
enter(true,false);enter(false,true);assert.equal(submissions,0,'IME Enter and Shift+Enter must not submit');enter(false,false);assert.equal(submissions,1);

const first=conversation.querySelector('.conversation-turn'),trace=first.querySelector('.turn-process'),records=first.querySelector('.turn-records');
trace.open=true;trace.dispatchEvent(new window.Event('toggle'));
assert.equal(records.querySelectorAll('[data-event]').length,100,'opening a large trace mounts one page');
assert.equal(records.querySelectorAll('pre').length,0,'closed tool details must not parse or mount large output');
const disclosure=records.querySelector('.log details');disclosure.open=true;disclosure.dispatchEvent(new window.Event('toggle'));
assert.equal(disclosure.querySelector('pre').textContent,run('conversationItems.get(Number('+JSON.stringify(disclosure.parentElement.dataset.event)+')).event.text'),'full tool output is available on demand');
disclosure.open=false;disclosure.dispatchEvent(new window.Event('toggle'));assert.equal(disclosure.querySelector('pre'),null);
// Linkedom has no layout; the real browser fixture checks geometry separately.
for(const item of records.children)item.getBoundingClientRect=()=>({top:0});
records.querySelector('.conversation-more').click();assert.equal(records.querySelectorAll('[data-event]').length,198,'older records can all be retrieved');
trace.open=false;trace.dispatchEvent(new window.Event('toggle'));assert.equal(records.childElementCount,0,'closing a trace releases its live DOM');

// A search hit outside the last page is materialized without changing filters
// or rendering every intervening event; full text and DOM identity survive polls.
const hit=ctx.revealConversationEvent(2);assert(hit.isConnected);assert.equal(hit.dataset.searchMatch,'true');
assert.equal(run('conversationFilter.tools'),false);assert.equal(run('conversationFilter.process'),false);
ctx.appendEvents([]);assert.strictEqual(ctx.revealConversationEvent(2),hit);
// New output changes its own turn without reconstructing previous messages.
const earlier=conversation.querySelector('[data-event="200"]');
ctx.appendEvents([{seq:4801,run_id:'run-23',kind:'tool',text:'new synthetic tool output'}]);
assert.strictEqual(conversation.querySelector('[data-event="200"]'),earlier);
assert.match(conversation.lastElementChild.querySelector('.turn-process summary').textContent,/199 条/);
ctx.appendEvents([{seq:4801,run_id:'run-23',kind:'tool',text:'duplicate must be ignored'}]);
assert.match(conversation.lastElementChild.querySelector('.turn-process summary').textContent,/199 条/);

// Completion can promote a previously received assistant event with no new
// event, and a changed run result must reclassify the earlier match correctly.
run("resetConversation();sequence=0;element('conversation').replaceChildren();detail.runs=[{id:'active',status:'running',result:'',created:1}];");
ctx.appendEvents([{seq:1,run_id:'active',kind:'user',text:'simulate'},{seq:2,run_id:'active',kind:'assistant',text:'first result'},{seq:3,run_id:'active',kind:'assistant',text:'last result'},{seq:4,run_id:'active',kind:'error',text:'visible error'}]);
assert.equal(conversation.querySelectorAll('.turn-output .message').length,0);
assert.match(conversation.querySelector('.turn-output').textContent,/visible error/);
run("detail.runs[0].status='done';detail.runs[0].result='last result';detail.runs[0].finished=2;");ctx.appendEvents([]);
assert.equal(conversation.querySelector('.turn-output .message').dataset.event,'3');
run("detail.runs[0].result='first result';");ctx.appendEvents([]);
assert.equal(conversation.querySelector('.turn-output .message').dataset.event,'2');
ctx.setConversationFilter({tools:true,process:true});assert.match(conversation.querySelector('.turn-records').textContent,/last result/);
ctx.setConversationFilter({tools:false,process:false});assert.equal(conversation.querySelector('.turn-records').childElementCount,0);

const output=conversation.querySelector('.turn-output .message');
ctx.storeKnowledge([{id:'knowledge',run_id:'active',revision:1,source:'auto',title:'fixture',content:'safe fixture',status:'observed',updated:1}]);
assert.strictEqual(conversation.querySelector('.turn-output .message'),output,'knowledge updates preserve message DOM');
assert.match(conversation.querySelector('.turn-footer').textContent,/已自动记录/);
run("detail.runs[0].attachments=[{id:'attachment',name:'fixture.txt'}]");ctx.appendEvents([]);assert.match(conversation.querySelector('.turn-attachments').textContent,/fixture.txt/);
run("resetConversation();sequence=0;element('conversation').replaceChildren();detail.runs=[];");
ctx.appendEvents([{seq:1,run_id:'',kind:'assistant',text:'legacy message'},{seq:2,run_id:'',kind:'error',text:'legacy error'}]);
assert.match(conversation.textContent,/legacy message/);assert.match(conversation.textContent,/legacy error/);
assert.equal(ctx.revealConversationEvent(4800),null,'reset must release previous task indexes');
run('authenticated=false;renewShellScope()');
console.log('PASS: bounded long-chat DOM, cheap typing and idle polling, paged traces, lazy full output, search, completion without new events, errors, knowledge, attachments and task reset.');
