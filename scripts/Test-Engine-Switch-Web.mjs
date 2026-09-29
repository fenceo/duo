import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const f=await createWebShellFixture(),{ctx,document,environment}=f,node=id=>document.getElementById(id);
const flush=()=>new Promise(resolve=>setImmediate(resolve));
const source={id:'source',title:'Source',environment,workspace:'/fixture',engine:'codex',model:'model-a',reasoning_effort:'',session:'original-native',status:'done',binding:{revision:'original-binding'},mode:{id:'work',permission:'workspace',approval:'request',allow_network:true},pinned:false,archived:false};
const other={...source,id:'other',title:'Other <script>',engine:'claude',model:'model-b'};
const profiles={engines:[],active_profile:{[environment.id+':claude']:'account-b'},profiles:[{id:'account-b',name:'Account <b>',environment_id:environment.id,engine:'claude',kind:'claude_home',reference:'/fixture/account-b',updated:4}]};
const preview=id=>({source_task_id:id,source_engine:'codex',source_title:id,transferred_runs:27,transferred_knowledge:3,context:'Synthetic summary <unsafe>',context_truncated:true,fingerprint:'preview-'+id,archive_bytes:8192});
const detail=task=>({task,runs:[],events:[]});
const calls=[];
ctx.api=async(path,method='GET',body)=>{
 calls.push({path,method,body});
 if(path==='engines')return profiles;
 if(path.startsWith('environments/'))return {models:[{id:'model-a',name:'Model <x>'}],status:'ready',source:'synthetic native metadata',modified:0};
 if(path.includes('/continuation/preview'))return preview(path.split('/')[1]);
 if(path.endsWith('?recent=1'))return detail(path.includes('/other?')?other:source);
 throw Error('unexpected API '+path);
};
ctx.synthetic=detail(source);runInContext("chosen='source';detail=synthetic;tasks=[synthetic.task];renderTask()",ctx);node('message').value='unsent draft';
await ctx.openHandoff('other');await flush();
assert.equal(node('handoff-model').value,'model-b','task menu must read the selected source, not the current conversation');
assert.match(node('handoff-source').textContent,/Other <script>/);assert(!node('handoff-source').querySelector('script'));
assert.equal(node('handoff-context-mode').value,'full');
assert.match(node('handoff-preview-status').textContent,/27 轮/);assert.match(node('handoff-preview-status').textContent,/全文保留/);
assert.equal(node('handoff-preview').querySelector('unsafe'),null);
assert.equal(node('handoff-archive').getAttribute('href'),'/api/tasks/other/continuation/archive?mode=full');
assert(calls.every(x=>x.method==='GET'),'opening and model metadata never start model calls');
node('handoff-cancel').onclick();await ctx.openHandoff('source');await flush();
assert.equal(node('handoff-profile').value,'__current__');
assert(calls.some(x=>x.path.includes('&task_id=source')),'current-bound model list follows the task account');
node('handoff-engine').value='claude';node('handoff-engine').onchange();await flush();
assert.equal(node('handoff-profile').value,'account-b');
assert.equal(node('handoff-model').value,'','do not reuse another engine model ID');
assert(calls.some(x=>x.path.includes('&profile_id=account-b')));
assert.equal(node('handoff-profile').querySelector('b'),null);
assert.equal(node('handoff-mode').value,'work','use explicit target permission, independent of create-task state');

let finishSave;const api=ctx.api;
ctx.api=async(path,method,body)=>{if(method==='POST'){calls.push({path,method,body});return new Promise(resolve=>finishSave=resolve)}return api(path,method,body)};
const first=ctx.submitHandoff();const duplicate=ctx.submitHandoff();await flush();
assert.equal(calls.filter(x=>x.method==='POST').length,1);assert(node('handoff-submit').disabled);assert(node('handoff-cancel').disabled);
const sent=calls.find(x=>x.method==='POST');assert.equal(sent.path,'tasks/source/handoff');assert.equal(sent.body.profile_id,'account-b');assert.equal(sent.body.expected_profile.reference,'/fixture/account-b');assert.equal(sent.body.fingerprint,'preview-source');
finishSave({task:{...source,engine:'claude',model:'',session:'',binding:{revision:'switched',profile:profiles.profiles[0],history_id:'history'}},new_session:true});await Promise.all([first,duplicate]);
assert.equal(node('handoff-dialog').open,false);assert.equal(node('message').value,'unsent draft','switching must preserve the composer');
assert.equal(runInContext('detail.task.id',ctx),'source');assert.equal(runInContext('tasks.length',ctx),1,'no task fork');
assert.equal(runInContext('detail.task.engine',ctx),'claude');
runInContext("detail.runs=[{id:'old-codex',engine:'codex',kind:'chat',status:'done',result:'old reply'}]",ctx);
const oldReply=ctx.conversationEventNode({event:{seq:500,run_id:'old-codex',kind:'assistant',text:'old reply',created:1}});
assert.equal(oldReply.querySelector('.label').textContent,'Codex','historical replies keep their original engine after switching');
assert(!calls.some(x=>x.path.includes('activate')||x.path.includes('messages')));

ctx.api=api;await ctx.openHandoff('source');
let late;ctx.api=async(path,method,body)=>{if(path.endsWith('mode=full'))return new Promise(resolve=>late=resolve);return api(path,method,body)};
const outdated=ctx.loadHandoffPreview();node('handoff-context-mode').value='notes';await ctx.loadHandoffPreview();late({...preview('outdated'),context:'stale result'});await outdated;
assert.equal(node('handoff-preview').textContent,'Synthetic summary <unsafe>','late scope response cannot replace the reviewed preview');
assert.match(node('handoff-archive').getAttribute('href'),/mode=notes$/);
ctx.api=async(path,method,body)=>{if(method==='POST')throw Error('409 任务或配置已改变');return api(path,method,body)};
await ctx.submitHandoff();assert(node('handoff-dialog').open);assert.match(node('handoff-error').textContent,/409/);assert.equal(node('handoff-submit').disabled,false);

node('handoff-cancel').onclick();ctx.api=async(path,method,body)=>path.endsWith('?recent=1')?{...detail(source),runs:[{id:'active',status:'running'}]}:api(path,method,body);
await ctx.openHandoff('source');assert(node('handoff-submit').disabled);assert.match(node('handoff-error').textContent,/停止/);
// An old task requires an explicit original-account attestation; cancel is read-only.
node('handoff-cancel').onclick();
const legacy={...source,binding:undefined};
ctx.api=async(path,method,body)=>path.endsWith('?recent=1')?detail(legacy):api(path,method,body);
const beforeLegacy=calls.filter(x=>x.method==='POST').length;
await ctx.openHandoff('source');await flush();
assert.equal(node('handoff-legacy').hidden,false);
assert.equal(!!node('handoff-preserve-legacy').checked,false);
node('handoff-preserve-legacy').checked=true;node('handoff-preserve-legacy').onchange();
assert.match(node('handoff-submit').textContent,/保留原会话/);
node('handoff-cancel').onclick();
assert.equal(calls.filter(x=>x.method==='POST').length,beforeLegacy,'cancelling legacy migration must not write');
await ctx.openHandoff('source');await flush();
assert.equal(!!node('handoff-preserve-legacy').checked,false,'reopening cannot retain account consent');
node('handoff-preserve-legacy').checked=true;
node('handoff-profile').onchange();
assert.equal(node('handoff-preserve-legacy').checked,false,'profile changes require fresh consent');
node('handoff-preserve-legacy').checked=true;
node('handoff-workspace').value='/another';node('handoff-workspace').oninput();
assert.equal(node('handoff-preserve-legacy').checked,false);assert(node('handoff-legacy').hidden);
node('handoff-workspace').value=source.workspace;node('handoff-workspace').oninput();
node('handoff-preserve-legacy').checked=true;node('handoff-preserve-legacy').onchange();
ctx.api=async(path,method,body)=>{
 if(method==='POST'){
  assert.equal(body.preserve_legacy_session,true);assert.equal(body.expected_legacy_session,'original-native');
  assert.equal(body.profile_id,'');
  return {task:{...source,binding:{revision:'migrated'}},new_session:false};
 }
 return api(path,method,body);
};
await ctx.submitHandoff();assert.equal(node('handoff-dialog').open,false);
assert.equal(node('message').value,'unsent draft');
runInContext('authenticated=false;renewShellScope()',ctx);
console.log('PASS: same-task engine/API/model switch, correct source and profile metadata, full-history default, escaped preview, draft preservation, duplicate lock, stale scope guards, explicit permissions and no model calls.');
