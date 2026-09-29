import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {ctx,document,window}=await createWebShellFixture();
const run=code=>runInContext(code,ctx),tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}};
function page(first,last,{before='r'+first,older=first>0,task='fixture',sequence=100}={}){
 const runs=[],events=[],records={};
 for(let i=first;i<=last;i++){
  const id='r'+i,answer='final '+i;
  runs.push({id,input:'request '+i,kind:'chat',status:'done',result:answer,error:'',created:i+1,finished:i+2});
  events.push({seq:i*10+1,run_id:id,kind:'user',text:'request '+i,created:i+1},{seq:i*10+2,run_id:id,kind:'assistant',text:'progress '+i,created:i+1},{seq:i*10+3,run_id:id,kind:'tool',text:'tool preview '+i,truncated:true,created:i+1},{seq:i*10+4,run_id:id,kind:'assistant',text:answer,created:i+1});
  records[id]={before:i*10+1,has_older:false};
 }
 return {task:{id:task,title:'Synthetic history',workspace:'<workspace>',environment:{type:'windows',name:'Fixture'},engine:'codex',model:'fixture',reasoning_effort:'',session:'',status:'done',updated:1,archived:false},runs,events,approvals:[],chat:'',conversation:{before,has_older:older,sequence,has_more:false,records}};
}
const initial=page(5,9),previous=page(0,4),calls=[];
const initialGate=deferred(),knowledgeGate=deferred();
const originalAPI=ctx.api;
ctx.api=async(path,...args)=>{
 calls.push(path);
 if(path==='tasks/fixture?recent=1')return initialGate.promise;
 if(path==='tasks/fixture/knowledge?summary=1')return knowledgeGate.promise;
 if(path==='tasks/fixture/context')return {files:[]};
 if(path==='tasks/fixture?recent=1&before=r5')return structuredClone(previous);
 return originalAPI(path,...args);
};
const loading=ctx.choose('fixture');
initialGate.resolve(structuredClone(initial));await loading;
const container=document.getElementById('conversation');
assert.equal(container.querySelectorAll('.conversation-turn').length,5);
assert.match(container.textContent,/final 9/,'conversation renders before the slow knowledge response');
assert.equal(run('sequence'),100,'live polling starts at the snapshot high-water mark');
assert(calls.includes('tasks/fixture/knowledge?summary=1'));
assert(!calls.includes('tasks/fixture/knowledge'),'opening chat does not fetch knowledge bodies');
assert.match(container.textContent,/progress 9/,'intermediate replies are on by default');
assert.equal(container.querySelector('.log'),null,'tools are off by default');
const final=container.querySelector('[data-event="94"]'),input=container.querySelector('[data-event="91"]');
const callCount=calls.length;
ctx.setConversationFilter({tools:false,process:false});
assert.equal(container.querySelector('.turn-records [data-event]'),null,'unchecked process really disappears');
assert.strictEqual(container.querySelector('[data-event="94"]'),final);
assert.strictEqual(container.querySelector('[data-event="91"]'),input);
assert.equal(calls.length,callCount,'display changes never send a run or reload history');
await ctx.loadOlderConversation();
assert.equal(container.querySelectorAll('.conversation-turn').length,10);
assert.equal(container.querySelector('.conversation-turn').dataset.run,'r0','older turns prepend chronologically');
assert.equal(run('sequence'),100,'loading history does not rewind the live cursor');
document.getElementById('conversation-recent').click();
assert.equal(container.querySelectorAll('.conversation-turn').length,5);
assert.strictEqual(container.querySelector('[data-event="94"]'),final,'collapsing old rounds preserves recent reply identity');
await ctx.loadOlderConversation();
assert.equal(calls.filter(path=>path.includes('&before=')).length,1,'cached history expands without another request');

ctx.setConversationFilter({tools:true,process:false});
assert.equal(container.querySelectorAll('.log pre').length,0,'tool previews do not fetch or mount full output');
const toolGate=deferred();
ctx.api=async(path,...args)=>{calls.push(path);if(path==='tasks/fixture?event=93')return toolGate.promise;return originalAPI(path,...args)};
const tool=container.querySelector('[data-event="93"] details');tool.open=true;tool.dispatchEvent(new window.Event('toggle'));tool.dispatchEvent(new window.Event('toggle'));
assert.equal(calls.filter(path=>path==='tasks/fixture?event=93').length,1,'duplicate toggles share the in-flight full record request');
tool.open=false;tool.dispatchEvent(new window.Event('toggle'));
toolGate.resolve({seq:93,run_id:'r9',kind:'tool',text:'complete synthetic output\n'+'.'.repeat(10000)});await tick();
assert.equal(tool.querySelector('pre'),null,'closed tool stays closed after a late full response');
tool.open=true;tool.dispatchEvent(new window.Event('toggle'));await tick();assert.match(tool.querySelector('pre').textContent,/complete synthetic output/);
assert.equal(calls.filter(path=>path==='tasks/fixture?event=93').length,1,'full record is cached for reopening');

// Per-turn history preserves current run state and the already explored cursor.
const oldRecords={...structuredClone(initial),runs:[{...initial.runs[4],status:'running'}],events:[{seq:89,run_id:'r9',kind:'assistant',text:'earlier progress',created:10}],conversation:{before:'',has_older:false,sequence:100,has_more:false,records:{r9:{before:89,has_older:false}}}};
run('conversationRecordPages.set("r9",{before:91,has_older:true})');
ctx.api=async path=>{calls.push(path);assert.equal(path,'tasks/fixture?recent=1&run=r9&before_event=91');return oldRecords};
await ctx.loadOlderConversationRecords('r9',run('conversationTurns.get("r9")'));
assert.equal(run('detail.runs.find(r=>r.id==="r9").status'),'done');assert.equal(run('sequence'),100);
assert.equal(run('conversationItems.get(89).event.text'),'earlier progress');
ctx.repeatedPage=structuredClone(initial);ctx.repeatedPage.conversation.records.r9={before:91,has_older:true};
run('receiveConversationDetail(repeatedPage,"older")');
assert.equal(run('conversationRecordPages.get("r9").before'),89,'revisiting a round cannot rewind its loaded-history boundary');
assert.equal(run('conversationRecordPages.get("r9").has_older'),false);

// Network failures preserve recent content and leave a retryable old-page action.
run('conversationHistory.expanded=true;conversationHistory.hasOlder=true;conversationHistory.before="r0"');
ctx.api=async()=>{throw new Error('fixture offline')};
await ctx.loadOlderConversation();
assert.match(document.getElementById('conversation-history').textContent,/读取失败.*fixture offline/);
assert.strictEqual(container.querySelector('[data-event="94"]'),final);
assert.equal(document.getElementById('conversation-older').disabled,false);

// A pending request from a previous task cannot repopulate the new task or move
// its live cursor, even if it finishes after the new task's initial response.
const stale=deferred();ctx.api=async()=>stale.promise;
const oldLoad=ctx.loadOlderConversation();
ctx.other=page(0,0,{task:'other',sequence:700,older:false});
run("chosen='other';selection++;resetConversation();element('conversation').replaceChildren();receiveConversationDetail(other);");
stale.resolve(previous);await oldLoad;
knowledgeGate.resolve([{id:'old-note',run_id:'r9',revision:1,source:'auto',content:''}]);await tick();
assert.equal(run('sequence'),700);assert.equal(run('detail.task.id'),'other');assert.equal(container.querySelectorAll('.conversation-turn').length,1);
assert.equal(run('knowledgeItems.length'),0,'late knowledge metadata stays isolated too');

// Explicitly viewing an old search hit must not advance the live cursor.
ctx.hitPage=previous;run('conversationHistory.expanded=true;receiveConversationDetail(hitPage,"records")');
ctx.setConversationFilter({tools:false,process:false});
const hit=ctx.revealConversationEvent(2);assert(hit?.isConnected);assert.equal(run('sequence'),700);
assert.equal(run('conversationFilter.tools'),false);assert.equal(run('conversationFilter.process'),false);
// A slow history page cannot roll a completed live run back to running.
ctx.stalePage=structuredClone(previous);ctx.stalePage.runs[0].status='running';
run('receiveConversationDetail(stalePage,"records")');
assert.equal(run('detail.runs.find(r=>r.id==="r0").status'),'done');
run('authenticated=false;renewShellScope()');
console.log('PASS: latest-five load, knowledge-free first paint, display-only checkboxes, ordered lazy history, stable live cursor, full tools on demand, retry and stale task isolation. Synthetic only.');
