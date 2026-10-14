import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const flush=()=>new Promise(resolve=>setImmediate(resolve));
function gate(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
async function fixture(engine='codex'){
 const {ctx,document,environment}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
 const source={id:'recovery-source',title:'Recovery fixture',environment,workspace:'/fixture',engine,model:'model-a',reasoning_effort:'',session:'old-native-session',status:'done',binding:{revision:'old-binding'},mode:{id:'work',permission:'workspace',approval:'request',allow_network:true},pinned:false,archived:false};
 const history=[{id:'preserved-run',kind:'chat',engine,status:'done',input:'old question',result:'old reply'}],calls=[],notices=[];
 const preview=mode=>({source_task_id:source.id,source_engine:engine,source_title:source.title,transferred_runs:mode==='brief'?3:mode==='notes'?0:27,transferred_knowledge:2,context:mode+' <local summary>',context_truncated:mode!=='notes',fingerprint:'fingerprint-'+mode,archive_bytes:mode==='brief'?0:8192});
 const f={ctx,document,environment,source,history,calls,notices,run,el,preview,previewGates:new Map(),saveGate:null};
 ctx.notify=message=>notices.push(message);
 ctx.api=async(path,method='GET',body,signal)=>{
  calls.push({path,method,body,signal});
  if(path.endsWith('?recent=1'))return {task:source,runs:history,events:[]};
  if(path.startsWith('environments/'))return {models:[{id:'model-a',name:'Model A'}],status:'ready',source:'synthetic metadata',modified:0};
  if(path.includes('/continuation/preview')){const mode=new URL('https://fixture.invalid/'+path).searchParams.get('mode');return f.previewGates.get(mode)?.promise||preview(mode)}
  if(method==='POST'&&path.endsWith('/handoff'))return f.saveGate?.promise||{task:{...source,model:body.model,session:body.restart_session?'':source.session},new_session:!!body.restart_session};
  throw Error('Unexpected fixture request: '+path);
 };
 ctx.fixtureDetail={task:{...source},runs:[...history],events:[]};run("chosen='recovery-source';detail=fixtureDetail;tasks=[fixtureDetail.task];renderTask()");el('message').value='unsent draft';
 await ctx.openHandoff(source.id);await flush();return f;
}

{
 const f=await fixture();
 assert(!f.el('handoff-recovery').classList.contains('hidden'));assert(!f.el('handoff-restart-session').checked);assert.equal(f.el('handoff-context-mode').value,'full');assert(!f.el('handoff-context-mode').disabled);
 assert.match(f.el('handoff-route-hint').textContent,/继续原会话/);assert(f.calls.every(call=>call.method==='GET'));
 await f.ctx.submitHandoff();const sent=f.calls.find(call=>call.method==='POST');assert(sent);assert(!Object.hasOwn(sent.body,'restart_session'),'ordinary model-only handoff must not request a new native session');
 assert.equal(f.run('detail.task.session'),'old-native-session');assert.equal(f.el('message').value,'unsent draft');assert.match(f.notices.at(-1),/继续原会话/);
}
{
 const f=await fixture(),brief=gate();f.previewGates.set('brief',brief);
 const before=f.run('JSON.stringify(detail)');f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();
 assert.equal(f.el('handoff-context-mode').value,'brief');assert(f.el('handoff-context-mode').disabled);assert.equal(f.el('handoff-context-mode').querySelectorAll('option').length,1);assert(f.el('handoff-preview-details').open);assert(f.el('handoff-submit').disabled);
 assert.match(f.el('handoff-recovery-hint').textContent,/最多 3 轮成功回复/);assert.match(f.el('handoff-recovery-hint').textContent,/可能遗漏细节/);assert.match(f.el('handoff-recovery-hint').textContent,/确认不会调用模型/);
 assert(f.el('handoff-archive').classList.contains('hidden'));assert.equal(f.el('handoff-archive').getAttribute('href'),null);
 await f.ctx.submitHandoff();assert(!f.calls.some(call=>call.method==='POST'),'must wait for the brief preview');
 brief.resolve(f.preview('brief'));await flush();assert(!f.el('handoff-submit').disabled);assert.equal(f.el('handoff-preview').textContent,'brief <local summary>');assert(!f.el('handoff-preview').querySelector('local'));assert.equal(f.run('JSON.stringify(detail)'),before);
 f.saveGate=gate();const first=f.ctx.submitHandoff(),duplicate=f.ctx.submitHandoff();await flush();
 assert.equal(f.calls.filter(call=>call.method==='POST').length,1);const sent=f.calls.find(call=>call.method==='POST');
 assert.equal(sent.body.restart_session,true);assert.equal(sent.body.context_mode,'brief');assert.equal(sent.body.fingerprint,'fingerprint-brief');assert.equal(sent.body.expected_binding_revision,'old-binding');assert.equal(sent.body.engine,'codex');
 assert(f.el('handoff-fields').disabled);assert(f.el('handoff-cancel').disabled);f.el('handoff-cancel').onclick();assert(f.el('handoff-dialog').open);
 f.saveGate.resolve({task:{...f.source,session:'',binding:{revision:'recovered-binding'}},new_session:true});await Promise.all([first,duplicate]);
 assert(!f.el('handoff-dialog').open);assert.equal(f.run('detail.task.session'),'');assert.equal(f.run('detail.runs[0].id'),'preserved-run');assert.equal(f.el('message').value,'unsent draft');assert.match(f.notices.at(-1),/精简摘要/);assert.doesNotMatch(f.notices.at(-1),/完整/);
 assert(f.calls.filter(call=>call.method!=='GET').every(call=>call.path.endsWith('/handoff')),'recovery never sends a model prompt, summary job, or task message');
 assert(!f.calls.some(call=>call.path.includes('/archive')),'recovery does not fetch or attach a full archive');
}
{
 const f=await fixture(),lateBrief=gate();f.previewGates.set('brief',lateBrief);
 f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();
 f.el('handoff-restart-session').checked=false;f.el('handoff-restart-session').onchange();await flush();
 lateBrief.resolve({...f.preview('brief'),context:'stale recovery preview'});await flush();
 assert.equal(f.el('handoff-context-mode').value,'full');assert.equal(f.el('handoff-preview').textContent,'full <local summary>');assert.match(f.el('handoff-archive').getAttribute('href'),/mode=full$/);
 const lateFull=gate();f.previewGates.set('full',lateFull);const old=f.ctx.loadHandoffPreview();f.previewGates.delete('brief');f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();await flush();
 lateFull.resolve({...f.preview('full'),context:'stale full archive'});await old;
 assert.equal(f.el('handoff-preview').textContent,'brief <local summary>');assert(f.el('handoff-archive').classList.contains('hidden'));assert.equal(f.el('handoff-archive').getAttribute('href'),null);
 // An engine change explicitly clears recovery and requires the matching
 // ordinary preview. Returning to Codex must not silently re-enable it.
 f.previewGates.delete('full');f.el('handoff-engine').value='claude';f.el('handoff-engine').onchange();await flush();
 assert(!f.el('handoff-restart-session').checked);assert(f.el('handoff-recovery').classList.contains('hidden'));assert.equal(f.el('handoff-context-mode').value,'full');assert(!f.el('handoff-context-mode').disabled);
 f.el('handoff-engine').value='codex';f.el('handoff-engine').onchange();await flush();assert(!f.el('handoff-restart-session').checked);assert(!f.el('handoff-recovery').classList.contains('hidden'));
 f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();await flush();await f.ctx.openHandoff(f.source.id);await flush();
 assert(!f.el('handoff-restart-session').checked);assert.equal(f.el('handoff-context-mode').value,'full');assert(f.calls.every(call=>call.method==='GET'),'refresh resets recovery without initiating work');
}
{
 const f=await fixture();f.el('handoff-context-mode').value='notes';f.el('handoff-context-mode').onchange();await flush();
 f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();await flush();f.el('handoff-restart-session').checked=false;f.el('handoff-restart-session').onchange();await flush();
 assert.equal(f.el('handoff-context-mode').value,'notes','unchecking restores the prior reviewed scope');
 f.el('handoff-engine').value='claude';f.el('handoff-engine').onchange();await flush();f.saveGate=gate();const pending=f.ctx.submitHandoff();
 f.saveGate.resolve({task:{...f.source,engine:'claude',session:''},new_session:true});await pending;assert.match(f.notices.at(-1),/任务笔记与共识/);assert.doesNotMatch(f.notices.at(-1),/完整/);
}
for(const action of ['cancel','logout']){
 const f=await fixture(),late=gate();f.previewGates.set('brief',late);f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();
 const request=f.calls.at(-1);if(action==='cancel')f.el('handoff-cancel').onclick();else f.run('renewShellScope()');
 f.el('handoff-preview').textContent='new context';late.resolve(f.preview('brief'));await flush();assert(request.signal.aborted);assert.equal(f.el('handoff-preview').textContent,'new context');assert.equal(f.run('handoffPreview'),null);assert(!f.calls.some(call=>call.method==='POST'));
}
{
 const f=await fixture(),failure=gate();f.previewGates.set('brief',failure);f.el('handoff-restart-session').checked=true;f.el('handoff-restart-session').onchange();failure.reject(Error('fixture preview failure'));await flush();
 assert(f.el('handoff-submit').disabled);assert.match(f.el('handoff-error').textContent,/fixture preview failure/);await f.ctx.submitHandoff();assert(!f.calls.some(call=>call.method==='POST'));
 f.previewGates.delete('brief');await f.ctx.loadHandoffPreview();assert(!f.el('handoff-submit').disabled);
 // A scope/checkbox change without its DOM event is still rejected by the
 // preview signature check, so a full-history fingerprint cannot recover.
 f.el('handoff-restart-session').checked=false;await f.ctx.submitHandoff();assert(!f.calls.some(call=>call.method==='POST'));assert.match(f.el('handoff-error').textContent,/范围已改变/);
}
{
 const f=await fixture('claude');assert(f.el('handoff-recovery').classList.contains('hidden'));f.el('handoff-engine').value='codex';f.el('handoff-engine').onchange();assert(f.el('handoff-recovery').classList.contains('hidden'));
}
console.log('PASS: explicit Codex recovery, brief-only reviewed fingerprint, unchanged unchecked handoff, no model calls/archive, stale scope/engine/refresh/cancel guards, duplicate lock, preserved history/drafts and accurate scope notices. Synthetic only.');
