import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

// Synthetic DOM and streaming HTTP only: no real CLI, prompts, credentials or data.
const source=await readFile(new URL('../web/execution.ts',import.meta.url),'utf8');
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(){
 const nodes=new Map(),requests=[],confirmations=[],metadata=[],timers=new Map();let clock=1000,timerID=0;
 const node=id=>{
  if(!nodes.has(id)){const classes=new Set();nodes.set(id,{value:'',textContent:'',innerHTML:'',disabled:false,attributes:{},setAttribute(k,v){this.attributes[k]=v},classList:{add(v){classes.add(v)},remove(v){classes.delete(v)},contains(v){return classes.has(v)},toggle(v,on){on?classes.add(v):classes.delete(v)}}})}
  return nodes.get(id);
 };
 const environment={id:'wsl',name:'WSL synthetic',type:'wsl',model:'default-model',harness_model:'gateway-default',harness_provider:'fixture-gateway',workspaces:['/tmp/probe']};
 const profiles={active_profile:{'wsl:codex':'account-one'},profiles:[{id:'account-one',name:'工作账号',engine:'codex',environment_id:'wsl',kind:'codex_home'}]};
 const f={nodes,node,requests,confirmations,metadata,environment,profiles,timers,advance(ms){clock+=ms;for(const callback of [...timers.values()])callback()},allowed:true,statuses:['available','unavailable','timeout'],hold:false,events:null,fail:0};
 const ctx=createContext({console,URLSearchParams,AbortController,TextDecoder,Date:class extends Date{static now(){return clock}},setInterval(callback){timers.set(++timerID,callback);return timerID},clearInterval(id){timers.delete(id)},modelRequest:0,shellEpoch:1,csrf:'fixture-csrf',creatingTask:true,createSubmitting:false,detail:null,settings:{config:{environments:[environment]}},element:node,input:node,button:node,escapeHTML:value=>String(value).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),engineCredentialLabel:()=> 'Codex 配置目录',confirm:message=>{confirmations.push(message);return f.allowed},api:async(path,method,body)=>{metadata.push({path,method,body});return profiles},fetch:async(path,options)=>{
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
{
 const f=fixture();f.node('create-engine').value='mimo';
 f.value("createModels=[{id:'duo-mimo/model-a',name:'A'},{id:'duo-mimo/model-b',name:'B'}];modelCatalogState.create.key=currentCreateCatalogContext().key;modelCatalogState.create.apiTarget='fixture-api-target';modelCatalogState.create.apiEndpoint='https://fixture.example/v1'");
 f.ctx.syncModelTestButton();assert.equal(f.node('test-models').disabled,false);assert.match(f.node('test-models').textContent,/测试 API 模型/);
 await f.ctx.testCreateModels();assert.equal(f.requests.length,1);assert.equal(f.requests[0].body.expected_api_target,'fixture-api-target');
 assert.equal(f.requests[0].body.engine,'mimo');assert.match(f.confirmations[0],/不创建 CLI 会话/);assert.match(f.confirmations[0],/不读取项目指令/);assert.match(f.confirmations[0],/https:\/\/fixture.example\/v1/);
 f.value("modelCatalogState.create.apiTarget=''");f.ctx.syncModelTestButton();assert.equal(f.node('test-models').disabled,true);assert.match(f.node('test-models').title,/重新读取模型目录/);
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
}
{
 const f=fixture();f.ctx.creatingTask=false;
 f.profiles.active_profile['wsl:deepseek-harness']='harness-account';
 f.profiles.profiles.push({id:'harness-account',name:'Harness API',engine:'deepseek-harness',environment_id:'wsl',kind:'dsh_home'});
 f.ctx.detail={task:{id:'harness-task',environment:{...f.environment},engine:'deepseek-harness',workspace:'/task',model:'selected-model',session:'original-native-session',mode:{id:'harness:workspace',approval:'request'},binding:{revision:'harness-revision'}}};
 const before=JSON.stringify(f.ctx.detail.task);
 f.value("taskPickerModels=[{id:'harness-one',name:'One'},{id:'harness-two',name:'Two'}];modelCatalogState.task.key=taskCatalogContextKey(detail.task)");
 f.ctx.renderModelMenu('task');assert.equal(f.node('task-test-models').disabled,false);
 await f.ctx.testModelList('task');
 assert.equal(f.requests.length,1,'Harness task testing must issue a request');
 assert.deepEqual(f.requests[0].body,{engine:'deepseek-harness',workspace:'/task',models:['harness-one','harness-two'],expected_profile_id:'harness-account',task_id:'harness-task',binding_revision:'harness-revision'});
 for(const expected of ['DeepSeek Harness','Harness API','fixture-gateway','/task','可能消耗模型额度'])assert(f.confirmations[0].includes(expected));
 assert.match(f.node('task-model-list').innerHTML,/model-probe-available/);
 assert.equal(JSON.stringify(f.ctx.detail.task),before,'model probe must preserve the conversation, route, permission and binding');
 assert(f.requests.every(request=>!request.path.includes('/tasks/')),'no new task turn or native-session edit');
 f.ctx.settings.config.environments[0].harness_provider='different-provider';f.ctx.renderModelMenu('task');
 assert.equal(f.node('task-test-models').disabled,true);await f.ctx.testModelList('task');assert.equal(f.requests.length,1,'changed Harness provider cannot silently test another environment');
}
for(const accountMode of ['environment','pinned']){
 const f=fixture();f.ctx.creatingTask=false;
 f.ctx.detail={task:{id:'bound',environment:f.environment,engine:'codex',workspace:'/task',model:'selected',binding:{revision:'binding-version',account_mode:accountMode,profile:{id:'old-account',name:'Old account',kind:'codex_home'}}}};
 f.value("taskPickerModels=[{id:'one',name:'one'}];modelCatalogState.task.key=taskCatalogContextKey(detail.task)");
 await f.ctx.testModelList('task');
 assert.equal(f.requests[0].body.expected_profile_id,'account-one');
 assert.equal(f.requests[0].body.binding_revision,'binding-version');
 assert.equal(f.requests[0].body.task_id,'bound');
 assert.equal(f.ctx.detail.task.binding.profile.id,'old-account','metadata must not rewrite native session ownership');
}
{
 const f=fixture();f.ctx.creatingTask=false;f.ctx.detail={task:{id:'duo-claude',environment:f.environment,engine:'claude',workspace:'/task',model:'main'}};
 f.value("taskPickerModels=[{id:'main',name:'Main'},{id:'fast',name:'Fast'}];modelCatalogState.task.key=taskCatalogContextKey(detail.task);modelCatalogState.task.apiTarget='scope-revision';modelCatalogState.task.apiEndpoint='https://duo-claude.example'");
 await f.ctx.testModelList('task');assert.equal(f.requests[0].body.expected_api_target,'scope-revision');assert(f.confirmations[0].includes('Duo 专用 Claude API'));assert(f.confirmations[0].includes('https://duo-claude.example'));
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
assert.match(workflow,/modelFooter\.querySelector\('#model-catalog-actions'\)!\.append\(element\('reload-models'\),element\('test-models'\),element\('stop-model-test'\),element\('model-latency-controls'\)\)/);

for(const choice of ['selected','custom','default']){
 const f=fixture();f.node('model-search').value='model-a';f.node('create-model').value=choice==='selected'?'model-b':choice==='custom'?'__custom__':'';f.node('custom-model').value='typed-model';f.value("modelCatalogState.create.defaultModel='native-default'");f.node('model-test-timeout').value='300';
 await f.ctx.testModelList('create','latency');
 assert.equal(f.requests.length,1);assert.deepEqual(f.requests[0].body.models,[choice==='selected'?'model-b':choice==='custom'?'typed-model':'native-default']);assert.equal(f.requests[0].body.mode,'latency');assert.equal(f.requests[0].body.timeout_seconds,300);
 for(const text of ['仅测试当前选定的一个模型','等待上限：300 秒','CLI 启动及重试','独立的新测试会话','不恢复当前任务','可能消耗模型额度'])assert(f.confirmations[0].includes(text));
 assert.match(f.node('model-test-result').textContent,/服务未提供首段回复计时/,'older streams without a first-response metric must remain honest');assert.equal(f.timers.size,0);
}
for(const kind of ['unresolved-default','invalid-model','invalid-timeout','mimo']){
 const f=fixture();f.node('create-model').value=kind==='invalid-model'?'bad\nmodel':kind==='unresolved-default'?'':'model-b';
 if(kind==='invalid-timeout')f.node('model-test-timeout').value='0';
 if(kind==='mimo'){f.node('create-engine').value='mimo';f.value('modelCatalogState.create.key=currentCreateCatalogContext().key')}
 await f.ctx.testModelList('create','latency');assert.equal(f.requests.length,0);assert.equal(f.confirmations.length,0);assert.equal(f.timers.size,0);
 if(kind==='unresolved-default')assert.match(f.node('model-test-result').textContent,/不会猜测默认模型/,'environment.model does not resolve the native default');
 if(kind==='mimo'){f.ctx.syncModelTestButton();assert(f.node('test-model-latency').disabled);assert.match(f.node('test-model-latency').title,/非流式/)}
}
for(const status of ['available','unavailable','timeout']){
 const f=fixture();f.hold=true;f.node('create-model').value='model-b';const pending=f.ctx.testModelList('create','latency');await flush();
 assert.equal(f.requests[0].body.timeout_seconds,180);assert(f.node('test-model-latency').disabled);assert(f.node('model-test-timeout').disabled);assert(!f.node('stop-model-test').disabled);
 f.advance(12000);assert.match(f.node('model-test-result').textContent,/已等待 12 秒/);assert.match(f.node('model-test-result').textContent,/正在等待首段回复/);
 f.events.emit({type:'model_start',model:'model-b'});f.events.emit({type:'heartbeat',model:'model-b',elapsed_ms:13000});await flush();assert.match(f.node('model-test-result').textContent,/正在等待首段回复/);
 f.events.emit({type:'model_response',model:'model-b',first_response_ms:1420,timing_source:'stream_text'});await flush();assert.match(f.node('model-test-result').textContent,/首段回复 1\.42 秒/);
 f.events.emit({type:'result',result:{model:'model-b',status,message:'fixture',duration_ms:180000}});f.events.emit({type:'done'});f.events.close();await pending;
 assert.match(f.node('model-test-result').textContent,/首段回复 1\.42 秒/);assert.match(f.node('model-test-result').textContent,/总耗时 180\.00 秒/);assert.doesNotMatch(f.node('model-test-result').textContent,/首段回复 180/);assert.equal(f.timers.size,0);assert(!f.node('model-test-timeout').disabled);
}
{
 const f=fixture();f.hold=true;f.node('create-model').value='model-a';const pending=f.ctx.testModelList('create','latency');await flush();
 f.events.emit({type:'result',result:{model:'model-a',status:'timeout',message:'fixture',duration_ms:180000,timeout_seconds:180}});f.events.emit({type:'done'});f.events.close();await pending;
 assert.match(f.node('model-test-result').textContent,/未收到首段回复/);assert.match(f.node('model-test-result').textContent,/总耗时 180\.00 秒/);assert.equal(f.timers.size,0);
}
{
 const f=fixture();f.hold=true;f.node('create-model').value='model-a';const pending=f.ctx.testModelList('create','latency');await flush();
 f.events.emit({type:'model_response',model:'model-a',first_response_ms:0,timing_source:'completed_text'});await flush();f.ctx.stopModelTest('create');await pending;
 assert.match(f.node('model-test-result').textContent,/首段回复 0\.00 秒（从完成结果观测）/);assert.match(f.node('model-test-result').textContent,/已取消 1/);assert.equal(f.timers.size,0);
}
for(const changed of ['environment','engine','workspace','profile','shell','closed','invalidate']){
 const f=fixture();f.hold=true;f.node('create-model').value='model-a';const pending=f.ctx.testModelList('create','latency');await flush();
 if(changed==='environment')f.environment.user='changed';if(changed==='engine')f.node('create-engine').value='claude';if(changed==='workspace')f.node('create-workspace').value='/changed';if(changed==='profile')f.value('modelProfileRevision++');if(changed==='shell')f.ctx.shellEpoch++;if(changed==='closed')f.ctx.creatingTask=false;if(changed==='invalidate')f.ctx.invalidateModelTest();
 f.node('model-test-result').textContent='fresh context';f.advance(1000);await pending;
 assert.equal(f.timers.size,0,changed+' timer cleanup');assert(f.requests[0].options.signal.aborted);assert.equal(f.node('model-test-result').textContent,'fresh context');
}
for(const altered of ['none','host','manual-current','unrecognized-previous']){
 const f=fixture();f.ctx.creatingTask=false;f.environment.codex='/tools/new';f.environment.managed_tools={codex:{path:'/tools/new',version:'1.2.3',previous_paths:['/tools/old']}};
 f.ctx.detail={task:{id:'upgraded-task',environment:{...f.environment,codex:'/tools/old'},engine:'codex',workspace:'/task',model:'picked',session:'preserved',binding:{revision:'preserved-revision'}}};
 if(altered==='host')f.ctx.detail.task.environment.host='other';if(altered==='manual-current')f.environment.codex='/tools/manual';if(altered==='unrecognized-previous')f.ctx.detail.task.environment.codex='/tools/custom';
 f.value("modelCatalogState.task.key=taskCatalogContextKey(detail.task);modelCatalogState.task.defaultModel='native-default'");
 const before=JSON.stringify(f.ctx.detail.task);await f.ctx.testModelList('task','latency');assert.equal(f.requests.length,altered==='none'?1:0,altered+' managed update compatibility');assert.equal(JSON.stringify(f.ctx.detail.task),before);
 if(altered==='none'){assert.deepEqual(f.requests[0].body.models,['picked']);assert.equal(f.requests[0].body.task_id,'upgraded-task');assert.equal(f.requests[0].body.binding_revision,'preserved-revision')}
}
console.log('PASS: list probes and one-model latency, explicit native default, cost confirmation, first-vs-total timing, waits/cancel/stale timer cleanup, MiMo limits, managed CLI snapshots and no task mutation. Synthetic only.');
