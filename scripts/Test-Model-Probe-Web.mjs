import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

// Synthetic DOM and streaming HTTP only: no real CLI, prompts, credentials or data.
const source=await readFile(new URL('../web/execution.ts',import.meta.url),'utf8');
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const nodes=new Map(),requests=[],confirmations=[],metadata=[];
 const node=id=>{
  if(!nodes.has(id)){const classes=new Set();nodes.set(id,{value:'',textContent:'',innerHTML:'',disabled:false,attributes:{},setAttribute(k,v){this.attributes[k]=v},classList:{add(v){classes.add(v)},remove(v){classes.delete(v)},contains(v){return classes.has(v)},toggle(v,on){on?classes.add(v):classes.delete(v)}}})}
  return nodes.get(id);
 };
 const environment={id:'wsl',name:'WSL synthetic',type:'wsl',model:'default-model',harness_model:'gateway-default',harness_provider:'fixture-gateway',workspaces:['/tmp/probe']};
 const profiles={active_profile:{'wsl:codex':'account-one'},profiles:[{id:'account-one',name:'工作账号',engine:'codex',environment_id:'wsl',kind:'codex_home'}]};
 const f={nodes,node,requests,confirmations,metadata,environment,profiles,allowed:true,statuses:['available','unavailable','timeout'],hold:false,events:null,fail:0};
 const ctx=createContext({console,URLSearchParams,AbortController,TextDecoder,Date,modelRequest:0,shellEpoch:1,csrf:'fixture-csrf',creatingTask:true,createSubmitting:false,detail:null,settings:{config:{environments:[environment]}},element:node,input:node,button:node,escapeHTML:value=>String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),engineCredentialLabel:()=> 'Codex 配置目录',confirm:message=>{confirmations.push(message);return f.allowed},api:async(path,method,body)=>{metadata.push({path,method,body});return profiles},fetch:async(path,options)=>{
  requests.push({path,options,body:JSON.parse(options.body)});
  if(f.fail)return new Response(JSON.stringify({error:'fixture HTTP '+f.fail}),{status:f.fail,headers:{'Content-Type':'application/json'}});
  const encoder=new TextEncoder();let control;
  const stream=new ReadableStream({start(controller){control=controller},cancel(){f.streamCancelled=true}});
  const emit=event=>control.enqueue(encoder.encode(JSON.stringify(event)+'\n'));
  options.signal.addEventListener('abort',()=>{try{control.error(new DOMException('aborted','AbortError'))}catch{}},{once:true});
  f.events={emit,close:()=>control.close(),error:error=>control.error(error)};
  if(!f.hold){
   const models=JSON.parse(options.body).models;emit({type:'start',models});
   models.forEach((model,index)=>{emit({type:'model_start',model});emit({type:'result',result:{model,status:f.statuses[index%f.statuses.length],message:'fixture <safe>',duration_ms:10}})});
   emit({type:'done'});control.close();
  }
  return new Response(stream,{headers:{'Content-Type':'application/x-ndjson; charset=utf-8'}});
 }});
 runInContext(stripTypeScriptTypes(source,{mode:'transform'}),ctx);
 ctx.authenticated=true;ctx.shellCurrent=epoch=>epoch===ctx.shellEpoch;ctx.showLogin=()=>{f.loggedOut=true;ctx.shellEpoch++;ctx.invalidateModelTest()};
 f.ctx=ctx;f.value=code=>runInContext(code,ctx);
 node('create-environment').value='wsl';node('create-engine').value='codex';node('create-workspace').value='/tmp/probe';
 f.value("createModels=[{id:'model-a',name:'Model A'},{id:'model-b',name:'Model B'},{id:'model-c',name:'Model C'}];modelCatalogState.create.key=currentCreateCatalogContext().key");
 return f;
}
{
 const f=fixture();f.value("createModels.push({id:'model-a',name:'duplicate'},{id:'',name:'default placeholder'})");
 f.node('model-search').value='model-a';f.node('create-model').value='__custom__';f.node('custom-model').value='not-in-list';
 await f.ctx.testCreateModels();
 assert.equal(f.requests.length,1);assert.equal(f.confirmations.length,1);
 assert.deepEqual(f.requests[0].body,{engine:'codex',workspace:'/tmp/probe',models:['model-a','model-b','model-c'],expected_profile_id:'account-one'});
 assert.equal(f.requests[0].options.headers.Accept,'application/x-ndjson');assert.equal(f.requests[0].options.headers['X-CSRF-Token'],'fixture-csrf');
 assert.match(f.confirmations[0],/全部 3 个模型（不受搜索筛选影响）/);
 for(const value of ['model-a','model-b','model-c','WSL synthetic','Codex','工作账号','account-one','/tmp/probe','可能消耗模型额度','CLI 可能加载'])assert(f.confirmations[0].includes(value));
 assert.doesNotMatch(f.confirmations[0],/not-in-list/);
 assert.match(f.node('model-test-result').textContent,/3\/3 · 可用 1 · 不可用 1 · 超时 1/);
 f.node('model-search').value='';f.ctx.renderModelMenu('create');
 assert.match(f.node('model-list').innerHTML,/model-probe-available/);assert.match(f.node('model-list').innerHTML,/model-probe-unavailable/);assert.match(f.node('model-list').innerHTML,/model-probe-timeout/);assert.match(f.node('model-list').innerHTML,/&lt;safe&gt;/);
 assert.equal(f.node('test-models').textContent,'测试列表模型（3）');
 f.node('create-model').value='model-b';f.ctx.renderModelMenu('create');assert.match(f.node('model-list').innerHTML,/model-probe-available/,'selection does not erase list results');
}
{
 const f=fixture();f.allowed=false;await f.ctx.testCreateModels();assert.equal(f.requests.length,0);assert.equal(f.confirmations.length,1);assert.equal(f.node('model-test-result').textContent,'');
}
for(const kind of ['empty','too-many','invalid','submitting','loading','failed','wrong-context']){
 const f=fixture();
 if(kind==='empty')f.value('createModels=[]');
 if(kind==='too-many')f.value("createModels=Array.from({length:25},(_,i)=>({id:'model-'+i,name:''}))");
 if(kind==='invalid')f.value("createModels=[{id:'bad\\nmodel',name:''}]");
 if(kind==='submitting')f.ctx.createSubmitting=true;
 if(kind==='loading')f.value('modelCatalogState.create.loading=true');
 if(kind==='failed')f.value('modelCatalogState.create.failed=true');
 if(kind==='wrong-context')f.node('create-workspace').value='/changed';
 await f.ctx.testCreateModels();assert.equal(f.requests.length,0,kind+' no paid call');assert.equal(f.confirmations.length,0,kind+' no confirmation');assert.equal(f.metadata.length,0);
}
{
 const f=fixture();f.hold=true;const pending=f.ctx.testCreateModels();await flush();
 assert.equal(f.requests.length,1);assert.equal(f.node('test-models').disabled,true);assert.equal(f.node('reload-models').disabled,true);assert.match(f.node('model-list').innerHTML,/待测试/);
 await f.ctx.testCreateModels();assert.equal(f.requests.length,1,'double click creates no second request');
 const reads=f.metadata.length;await f.ctx.loadCreateModels();assert.equal(f.metadata.length,reads,'reopening during batch does not reread metadata');
 f.events.emit({type:'model_start',model:'model-a'});await flush();assert.match(f.node('model-list').innerHTML,/测试中/);
 f.events.emit({type:'result',result:{model:'model-a',status:'available',message:'OK',duration_ms:1}});await flush();
 f.events.emit({type:'model_start',model:'model-b'});await flush();f.ctx.stopModelTest('create');await pending;
 assert.equal(f.requests[0].options.signal.aborted,true);assert.equal(f.node('stop-model-test').classList.contains('hidden'),true);
 assert.match(f.node('model-test-result').textContent,/可用 1 · 已取消 2/);assert.doesNotMatch(f.node('model-test-result').textContent,/不可用/);assert.equal(f.node('test-models').disabled,false);
}
for(const failure of [409,500,'network','truncated']){
 const f=fixture();f.fail=typeof failure==='number'?failure:0;f.hold=typeof failure==='string';
 const pending=f.ctx.testCreateModels();await flush();
 if(failure==='network')f.events.error(new Error('fixture disconnected'));
 if(failure==='truncated')f.events.close();
 await pending;assert.match(f.node('model-test-result').textContent,/未验证 3/);assert.doesNotMatch(f.node('model-list').innerHTML,/model-probe-unavailable/);assert.equal(f.node('test-models').disabled,false);
}
{
 const f=fixture();f.fail=401;await f.ctx.testCreateModels();assert.equal(f.loggedOut,true);assert.equal(f.ctx.authenticated,false);assert.equal(f.node('model-test-result').textContent,'','current 401 returns to login without stale model results');
 const old=fixture();let resolve;old.ctx.fetch=()=>new Promise(yes=>{resolve=yes});
 const pending=old.ctx.streamModelProbes('wsl',{models:['a']},new AbortController().signal,()=>{});old.ctx.shellEpoch++;
 resolve(new Response(JSON.stringify({error:'old expired login'}),{status:401}));await assert.rejects(pending,error=>error.status===401);
 assert.equal(old.loggedOut,undefined,'stale 401 does not log out a new shell');
}
for(const changed of ['environment','engine','workspace','profile','shell','closed']){
 const f=fixture();f.hold=true;const pending=f.ctx.testCreateModels();await flush();
 if(changed==='environment')f.environment.user='new-user';
 if(changed==='engine')f.node('create-engine').value='claude';
 if(changed==='workspace')f.node('create-workspace').value='/different';
 if(changed==='profile')f.value('modelProfileRevision++');
 if(changed==='shell')f.ctx.shellEpoch++;
 if(changed==='closed')f.ctx.creatingTask=false;
 f.node('model-test-result').textContent='new context';f.events.emit({type:'result',result:{model:'model-a',status:'available'}});f.events.emit({type:'done'});f.events.close();await pending;
 assert.equal(f.node('model-test-result').textContent,'new context',changed+' stale response isolation');
}
{
 const f=fixture();f.hold=true;const pending=f.ctx.testCreateModels();await flush();f.ctx.invalidateModelTest();await pending;
 assert.equal(f.requests[0].options.signal.aborted,true);assert.equal(f.node('model-test-result').textContent,'');
}
{
 const f=fixture();f.ctx.creatingTask=false;f.ctx.detail={task:{id:'task-one',environment:f.environment,engine:'claude',workspace:'/task',model:'old-selection'}};
 f.value("taskPickerModels=[{id:'claude-one',name:'Claude one'},{id:'claude-two',name:'Claude two'}];modelCatalogState.task.key=taskCatalogContextKey(detail.task)");
 await f.ctx.testModelList('task');assert.deepEqual(f.requests[0].body,{engine:'claude',workspace:'/task',models:['claude-one','claude-two'],expected_profile_id:''});
 assert.match(f.node('task-model-list').innerHTML,/model-probe-available/);assert.equal(f.ctx.detail.task.model,'old-selection','testing does not change task model');assert(f.requests.every(request=>!request.path.includes('/tasks/')),'no conversation pollution');
 f.ctx.detail.task.engine='deepseek-harness';await f.ctx.testModelList('task');assert.equal(f.requests.length,1,'Harness live-session boundaries preserved');
}
const workflow=await readFile(new URL('../web/workflow.ts',import.meta.url),'utf8');
for(const field of ['distro','user','host','port','identity','claude','type','deleted']){
 const f=fixture();f.ctx.creatingTask=false;f.ctx.detail={task:{id:'old-task',environment:{...f.environment},engine:'claude',workspace:'/task',model:'old'}};
 f.value("taskPickerModels=[{id:'one',name:'one'}];modelCatalogState.task.key=taskCatalogContextKey(detail.task)");
 if(field==='deleted')f.ctx.settings.config.environments=[];else f.environment[field]=field==='port'?2222:'changed';
 f.ctx.renderModelMenu('task');assert.equal(f.node('task-test-models').disabled,true);assert.match(f.node('task-models-status').textContent,/任务保留旧执行环境/);
 await f.ctx.testModelList('task');assert.equal(f.requests.length,0,'old '+field+' snapshot must not test another current target');
}
{
 const f=fixture();f.value("createModels=Array.from({length:25},(_,i)=>({id:'m'+i,name:''}))");f.ctx.renderModelMenu('create');
 assert.equal(f.node('test-models').disabled,true);assert.match(f.node('models-status').textContent,/一次最多测试 24 项，本列表 25 项/);
}
assert.match(workflow,/modelFooter\.querySelector\('#model-catalog-actions'\)!\.append\(element\('reload-models'\),element\('test-models'\),element\('stop-model-test'\)\)/);
console.log('PASS: full unique catalog/account/cost confirmation, limits, streaming badges, cancellation, HTTP409/network unverified status, profile/context isolation and no task mutation. Synthetic only.');
