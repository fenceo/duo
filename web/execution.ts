type EngineModel={id:string;name:string;reasoning_levels?:string[]|null;default_reasoning?:string;origin?:'native'|'cache'|'configured';engine?:string};
type ModelListResponse={models:EngineModel[];modified:number;message?:string;source:string;status?:'ready'|'fallback'|'empty';default_model?:string};
type ModelProbeResult={model:string;status:'available'|'unavailable'|'timeout';message:string;duration_ms:number};
type ModelProbeResponse={engine:string;workspace:string;results:ModelProbeResult[]};
type ModelProbeStatus=ModelProbeResult['status']|'pending'|'testing'|'cancelled'|'unverified';
type ModelProbeRow={status:ModelProbeStatus;message?:string;duration_ms?:number};
type ModelProbeState={key:string;models:string[];rows:Record<string,ModelProbeRow>;summary:string;busy:boolean;controller?:AbortController};
type ModelPickerTarget='create'|'task';
let createModels:EngineModel[]=[];
let createResolvedDefault='';
let taskPickerModels:EngineModel[]=[];
let taskModelRequest=0,modelTestRequest=0,modelProfileRevision=0;
let modelTestBusy=false;
const modelProbeStates:Record<ModelPickerTarget,ModelProbeState>={create:{key:'',models:[],rows:{},summary:'',busy:false},task:{key:'',models:[],rows:{},summary:'',busy:false}};
type ModelCatalogState={loading:boolean;key:string;summary:string;details:string;failed:boolean};
const modelCatalogState:Record<ModelPickerTarget,ModelCatalogState>={create:{loading:false,key:'',summary:'',details:'',failed:false},task:{loading:false,key:'',summary:'',details:'',failed:false}};
const modelCatalogControllers:Partial<Record<ModelPickerTarget,AbortController>>={};
const effortLabels:Record<string,string>={off:'关闭',none:'关闭',minimal:'极低',low:'低',medium:'中',high:'高',xhigh:'很高',max:'最高',ultra:'Ultra（工具可能自动委派）'};
const defaultModelLabel='使用此工具的默认模型';
const modelPickers:Record<ModelPickerTarget,{root:string;button:string;label:string;menu:string;search:string;list:string}>={
 create:{root:'model-picker',button:'model-picker-button',label:'model-picker-label',menu:'model-menu',search:'model-search',list:'model-list'},
 task:{root:'task-model',button:'task-model-button',label:'task-model-label',menu:'task-model-menu',search:'task-model-search',list:'task-model-list'}
};
function taskEngineName(engine?:string){return engine==='claude'?'Claude Code':engine==='deepseek-harness'?'DeepSeek Harness':'Codex'}
function engineDefaultModel(env:Environment,engine:string){return engine==='claude'?(env.claude_model||''):engine==='deepseek-harness'?(env.harness_model||'deepseek-flash'):env.model}
const harnessSessionHint='Harness 仅在同一个运行进程中连续对话；闲置 30 分钟、停止任务或重启服务后不能恢复原生上下文。可新建空白会话，旧记录仍保留但不会自动带入 AI 上下文。更换模型、provider 或权限请新建任务。';
const harnessKnowledgeHint='Harness 暂不支持自动整理任务知识，请使用 Codex 任务整理；仍可手动新增、编辑和导出笔记。';
function effortLevels(engine:string,model?:EngineModel):string[]{
 if(engine==='deepseek-harness')return (model?.reasoning_levels||[]).filter(level=>['off','low','high','max'].includes(level));
 if(model&&Array.isArray(model.reasoning_levels))return model.reasoning_levels;
 return engine==='claude'?['low','medium','high','xhigh','max']:['low','medium','high','xhigh'];
}
function installExecution(){
 disposeWithShell(()=>{modelCatalogControllers.create?.abort();modelCatalogControllers.task?.abort();invalidateModelTest()});
 input('create-engine').onchange=()=>void loadCreateEnvironment(true);
 button('test-models').onclick=()=>void testCreateModels();
 input('create-workspace').addEventListener('input',()=>{invalidateModelTest();modelCatalogControllers.create?.abort();modelRequest++;modelCatalogState.create.key='';createModels=[];createResolvedDefault='';if(!element('model-menu').classList.contains('hidden')){modelCatalogState.create.loading=false;modelCatalogState.create.summary='目录已改变，离开目录输入框后自动读取。';renderModelMenu('create')}});
 input('create-workspace').addEventListener('change',()=>void loadCreateModels());
 installModelPicker();
 element('setting-model').previousElementSibling!.textContent='Codex 默认模型（可留空）';
 element('setting-model').insertAdjacentHTML('afterend',`<label for="setting-claude">此环境中的 Claude Code 可执行文件</label><input id="setting-claude" placeholder="claude"><label for="setting-claude-model">Claude 默认模型（可留空）</label><input id="setting-claude-model" placeholder="例如 sonnet，或你的服务提供的模型 ID"><label for="setting-harness">此环境中的 DeepSeek Harness 可执行文件</label><input id="setting-harness" placeholder="Windows: dsh.cmd；WSL / SSH: dsh"><label for="setting-harness-model">Harness 默认模型 ID</label><input id="setting-harness-model" placeholder="deepseek-flash"><label for="setting-harness-provider">Harness provider ID</label><input id="setting-harness-provider" placeholder="deepseek-official"><p class="muted">通过 Harness SDK JSON-RPC 执行；模型 ID 和 provider 必须存在于目标环境的 Harness 配置中，登录和密钥在该环境配置。${harnessSessionHint}</p><label for="setting-engine">默认 AI 工具（飞书新建也使用它）</label><select id="setting-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><p class="muted">Claude 自动接受工作目录内的文件编辑；其他操作沿用该环境中的 Claude 权限设置。</p>`);
 button('check-codex').textContent='检查 Codex';
 button('check-codex').insertAdjacentHTML('afterend',' <button type="button" id="check-claude">检查 Claude</button>');
 button('check-claude').onclick=async()=>{button('check-claude').disabled=true;try{const r=await api('check','POST',{environment_id:editingID,engine:'claude'});element('check-result').textContent=(r.ok?'Claude 已配置\n':'Claude 检查失败\n')+r.output}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-claude').disabled=false}};
 button('check-claude').insertAdjacentHTML('afterend',' <button type="button" id="check-harness">检查 Harness</button>');
 button('check-harness').onclick=async()=>{button('check-harness').disabled=true;try{const r=await api('check','POST',{environment_id:editingID,engine:'deepseek-harness'});element('check-result').textContent=(r.ok?'Harness 基础检查通过（不代表模型调用成功）\n':'Harness 检查失败\n')+r.output+'\n检查使用已保存的环境配置；可在新建任务中测试模型调用。'}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-harness').disabled=false}};
}
function modelListIDs(target:ModelPickerTarget){return [...new Set((target==='create'?createModels:taskPickerModels).map(model=>model.id.trim()).filter(id=>id&&id!=='__custom__'))]}
function taskModelProbeMismatch(){
 if(!detail)return '';
 const previous=detail.task.environment,current=settings.config.environments.find(env=>env.id===previous.id),engine=detail.task.engine||'codex';
 const identity=(env:Environment)=>[env.type,env.distro||'',env.user||'',env.host||'',env.port||0,env.identity||'',engine==='claude'?(env.claude||'claude'):engine==='deepseek-harness'?(env.harness||'dsh'):(env.codex||'codex'),engine==='deepseek-harness'?(env.harness_provider||'deepseek-official'):''];
 return !current||JSON.stringify(identity(previous))!==JSON.stringify(identity(current))?'任务保留旧执行环境，请在新建任务中测试当前配置。':'';
}
function modelListProbeContext(target:ModelPickerTarget){
 if(target==='create'){if(!creatingTask)return null;return currentCreateCatalogContext()}
 if(creatingTask||!detail||detail.task.engine==='deepseek-harness'||taskModelProbeMismatch())return null;
 const task=detail.task,environment=settings.config.environments.find(env=>env.id===task.environment.id)||task.environment;
 return {environment,engine:task.engine||'codex',workspace:task.workspace,key:taskCatalogContextKey(task)};
}
function syncModelTestButton(){
 for(const target of ['create','task'] as ModelPickerTarget[]){
  const prefix=target==='create'?'':'task-',control=button(prefix+'test-models'),stop=button(prefix+'stop-model-test'),state=modelProbeStates[target],count=modelListIDs(target).length;
  if(control){control.textContent='测试列表模型'+(count?'（'+count+'）':'');control.disabled=modelTestBusy||modelCatalogState[target].loading||modelCatalogState[target].failed||count===0||count>24||(target==='create'&&createSubmitting)||(target==='task'&&!!taskModelProbeMismatch());control.title=count>24?'每次最多测试 24 个模型；请精简该环境的模型配置':'逐个测试完整列表，不受搜索筛选影响'}
  if(stop){stop.classList.toggle('hidden',!state.busy);stop.disabled=!!state.controller?.signal.aborted}
 }
}
function invalidateModelTest(target?:ModelPickerTarget){
 modelTestRequest++;
 for(const pick of target?[target]:['create','task'] as ModelPickerTarget[]){
  modelProbeStates[pick].controller?.abort();modelProbeStates[pick]={key:'',models:[],rows:{},summary:'',busy:false};
  const result=element((pick==='create'?'':'task-')+'model-test-result');if(result)result.textContent='';
 }
 syncModelTestButton();
}
function modelProbeStillCurrent(target:ModelPickerTarget,state:ModelProbeState){return modelProbeStates[target]===state&&modelListProbeContext(target)?.key===state.key}
function modelProbeSummary(state:ModelProbeState){
 const rows=Object.values(state.rows),count=(status:ModelProbeStatus)=>rows.filter(row=>row.status===status).length;
 const completed=count('available')+count('unavailable')+count('timeout');
 return `${state.busy?'测试中':'测试结果'} ${completed}/${state.models.length} · 可用 ${count('available')}${count('unavailable')?' · 不可用 '+count('unavailable'):''}${count('timeout')?' · 超时 '+count('timeout'):''}${count('cancelled')?' · 已取消 '+count('cancelled'):''}${count('unverified')?' · 未验证 '+count('unverified'):''}`;
}
function renderModelProbe(target:ModelPickerTarget){
 const state=modelProbeStates[target],result=element((target==='create'?'':'task-')+'model-test-result');
 if(result)result.textContent=state.models.length?[modelProbeSummary(state),state.summary].filter(Boolean).join('\n'):state.summary;
 syncModelTestButton();renderModelMenu(target);
}
function stopModelTest(target:ModelPickerTarget){
 const state=modelProbeStates[target];if(!state.busy)return;
 state.summary='正在停止；已完成的结果保留，尚未完成的不判为不可用。';state.controller?.abort();renderModelProbe(target);
}
async function testCreateModels(){return testModelList('create')}
async function testModelList(target:ModelPickerTarget){
 const context=modelListProbeContext(target),catalog=modelCatalogState[target];
 if(!context||modelTestBusy||catalog.loading||catalog.failed||(target==='create'&&createSubmitting))return;
 if(catalog.key!==context.key){modelProbeStates[target].summary='目标配置已改变，请先重读模型列表。';renderModelProbe(target);return}
 const models=modelListIDs(target);
 if(!models.length||models.length>24||models.some(id=>id.length>120||/[\0\r\n]/.test(id))){modelProbeStates[target].summary=!models.length?'列表为空，请先重读模型列表；不会猜测默认模型或自动测试其他模型。':'每次最多测试 24 个有效模型 ID，请调整配置后重读列表。';renderModelProbe(target);return}
 const state:ModelProbeState={key:context.key,models,rows:{},summary:'正在读取当前账号/API 配置…',busy:true,controller:new AbortController()};
 modelProbeStates[target]=state;modelTestBusy=true;renderModelProbe(target);
 try{
  const profiles=await api<EngineCatalog>('engines','GET',undefined,state.controller!.signal);
  if(!modelProbeStillCurrent(target,state)||state.controller!.signal.aborted)return;
  const profileID=profiles.active_profile[context.environment.id+':'+context.engine]||'',profile=profiles.profiles.find(item=>item.id===profileID);
  if(profileID&&!profile)throw new Error('当前账号/API 引用无法确认，请刷新配置后重试。');
  const account=profile?`${profile.name}（${profile.id}；${engineCredentialLabel(profile.kind)}）`:'目标环境 CLI 的原生默认配置';
  const provider=context.engine==='deepseek-harness'?(context.environment.harness_provider||'deepseek-official'):'沿用该 CLI 的原生配置';
  if(!confirm(`将逐个测试列表中的全部 ${models.length} 个模型（不受搜索筛选影响）：\n${models.map((id,index)=>`${index+1}. ${id}`).join('\n')}\n\n环境：${context.environment.name}\nAI 工具：${taskEngineName(context.engine)}\n账号/API：${account}\nProvider：${provider}\n目录：${context.workspace}\n\n每个模型发送一条最小测试请求，不附加当前任务对话或附件；沿用目标引擎配置，CLI 可能加载该目录的项目指令，可能消耗模型额度。可以停止剩余测试，已发出的请求仍可能计费。是否继续？`)){state.models=[];state.summary='';return}
  if(!modelProbeStillCurrent(target,state)||state.controller!.signal.aborted)return;
  state.rows=Object.fromEntries(models.map(id=>[id,{status:'pending' as const}]));state.summary='正在建立测试连接…';renderModelProbe(target);
  await streamModelProbes(context.environment.id,{engine:context.engine,workspace:context.workspace,models,expected_profile_id:profileID},state.controller!.signal,event=>{
   if(!modelProbeStillCurrent(target,state))return;
   if(event.type==='model_start'&&typeof event.model==='string'&&Object.hasOwn(state.rows,event.model))state.rows[event.model]={status:'testing'};
   if(event.type==='result'){
    const result=event.result as ModelProbeResult;
    if(result&&typeof result.model==='string'&&Object.hasOwn(state.rows,result.model)&&['available','unavailable','timeout'].includes(result.status))state.rows[result.model]=result;
   }
   state.summary='结果仅代表本次最小调用，不保证后续额度或长任务、工具能力。';renderModelProbe(target);
  });
  if(Object.values(state.rows).some(row=>row.status==='pending'||row.status==='testing'))throw new Error('测试连接已结束，但部分模型未返回结果。');
 }catch(e){
  if(!modelProbeStillCurrent(target,state))return;
  const cancelled=state.controller!.signal.aborted;
  for(const row of Object.values(state.rows))if(row.status==='pending'||row.status==='testing')row.status=cancelled?'cancelled':'unverified';
  state.summary=cancelled?'已停止测试；已发出的请求仍可能计费。':'测试未完成：'+(e as Error).message+' 未返回结果的模型不判为不可用。';
 }finally{
  state.busy=false;modelTestBusy=false;
  if(modelProbeStillCurrent(target,state))renderModelProbe(target);else syncModelTestButton();
 }
}
async function streamModelProbes(environmentID:string,body:Record<string,unknown>,signal:AbortSignal,onEvent:(event:any)=>void){
 const epoch=shellEpoch;
 const response=await fetch('/api/environments/'+encodeURIComponent(environmentID)+'/models/test',{method:'POST',credentials:'same-origin',cache:'no-store',signal,headers:{'Content-Type':'application/json','Accept':'application/x-ndjson','X-CSRF-Token':csrf},body:JSON.stringify(body)});
 if(!response.ok){const error=await response.json().catch(()=>({error:'测试请求失败'}));if(response.status===401&&shellCurrent(epoch)){authenticated=false;showLogin()}throw Object.assign(new Error(error.error||'测试请求失败'),{status:response.status})}
 if(!response.body||!response.headers.get('content-type')?.includes('application/x-ndjson'))throw new Error('服务未提供逐模型测试流，请升级服务后再试。');
 const reader=response.body.getReader(),decoder=new TextDecoder();let pending='',done=false;
 const line=(value:string)=>{if(!value.trim())return;const event=JSON.parse(value);if(event.type==='done')done=true;onEvent(event)};
 try{
  for(;;){const part=await reader.read();pending+=decoder.decode(part.value,{stream:!part.done});if(pending.length>1024*1024)throw new Error('模型测试响应过大。');let boundary;while((boundary=pending.indexOf('\n'))>=0){line(pending.slice(0,boundary));pending=pending.slice(boundary+1)}if(part.done)break}
  if(pending.trim())line(pending);if(!done)throw new Error('测试连接中断，请重试。');
 }finally{await reader.cancel().catch(()=>{});reader.releaseLock()}
}
function catalogContextKey(environment:Environment,engine:string,workspace:string){return JSON.stringify([shellEpoch,modelProfileRevision,environment,engine,workspace.trim()])}
function currentCreateCatalogContext(){const environment=settings.config.environments.find(env=>env.id===input('create-environment').value);if(!environment)return null;const engine=input('create-engine').value,workspace=input('create-workspace').value.trim();return {environment,engine,workspace,key:catalogContextKey(environment,engine,workspace)}}
function modelsURL(environmentID:string,engine:string,workspace:string,refresh=false){const query=new URLSearchParams({engine,workspace});if(refresh)query.set('refresh','1');return 'environments/'+encodeURIComponent(environmentID)+'/models?'+query}
function catalogSummary(result:ModelListResponse){return result.models?.length?(result.status==='fallback'?'已读取配置/缓存中的 ':'已读取 ')+result.models.length+' 个模型':'该环境的此引擎尚未发现模型'}
function catalogDetails(result:ModelListResponse){return [result.message,result.source?'来源：'+result.source:'',result.modified?'更新：'+new Date(result.modified).toLocaleString():'','列表表示已发现或已配置，不代表调用已验证。'].filter(Boolean).join('\n')}
function renderModelCatalogStatus(target:ModelPickerTarget){
 const state=modelCatalogState[target],prefix=target==='create'?'':'task-';
 const status=element(prefix+'models-status'),details=element(prefix+'models-hint'),context=element(prefix+'models-context');
 if(status){status.textContent=state.loading?'正在读取目标环境的模型…':state.summary;status.classList.toggle('error',state.failed);status.setAttribute('aria-busy',String(state.loading))}
 const count=modelListIDs(target).length;if(status&&count>24&&!state.loading)status.textContent+='\n一次最多测试 24 项，本列表 '+count+' 项；未发起测试。请调整模型配置后重读。';
 if(status&&target==='task'&&taskModelProbeMismatch())status.textContent+='\n'+taskModelProbeMismatch();
 if(details)details.textContent=state.details;
 if(context){const env=target==='create'?settings.config.environments.find(item=>item.id===input('create-environment').value):detail?.task.environment;context.textContent=(env?.name||'未选择环境')+' · '+taskEngineName(target==='create'?input('create-engine').value:detail?.task.engine)}
 const reload=button(prefix+'reload-models');if(reload){reload.disabled=state.loading||modelTestBusy||(target==='create'&&createSubmitting);reload.textContent=state.loading?'读取中…':'重读模型列表'}
 element(modelPickers[target].list)?.setAttribute('aria-busy',String(state.loading));
}
async function loadCreateModels(reset=false,refresh=false){
 if(!creatingTask||createSubmitting)return;
 const target=currentCreateCatalogContext();if(!target)return;
 const state=modelCatalogState.create;
 if(modelProbeStates.create.busy&&modelProbeStates.create.key===target.key)return;
 if(!reset&&!refresh&&state.loading&&state.key===target.key)return;
 if(state.key!==target.key||refresh)invalidateModelTest('create');
 if(state.key!==target.key){createModels=[];createResolvedDefault=''}
 const request=++modelRequest,defaultModel=engineDefaultModel(target.environment,target.engine);
 modelCatalogControllers.create?.abort();const controller=new AbortController();modelCatalogControllers.create=controller;
 state.key=target.key;state.loading=true;state.failed=false;state.summary='';state.details='';
 if(reset){setCreateModelsLoading();setCreateModels(defaultModel?[{id:defaultModel,name:defaultModel+'（环境默认）',origin:'configured'}]:[],defaultModel)}
 renderModelMenu('create');
 try{
  const result=await api<ModelListResponse>(modelsURL(target.environment.id,target.engine,target.workspace,refresh),'GET',undefined,controller.signal);
  if(request!==modelRequest||!creatingTask||createSubmitting||currentCreateCatalogContext()?.key!==target.key)return;
  const models=result.models||[],harnessCatalog=target.engine==='deepseek-harness'&&result.status==='ready';
  const fallback=harnessCatalog?(result.default_model||''):(defaultModel||result.default_model||'');
  if(harnessCatalog&&input('create-model').value===defaultModel&&!models.some(model=>model.id===defaultModel))input('create-model').value=fallback;
  if(fallback&&!models.some(model=>model.id===fallback))models.unshift({id:fallback,name:fallback+'（环境默认）',origin:'configured'});
  createModels=models;
  createResolvedDefault=fallback;
  state.summary=catalogSummary(result);state.details=catalogDetails(result);
 }catch(e){
  if(request!==modelRequest||!creatingTask||createSubmitting||currentCreateCatalogContext()?.key!==target.key)return;
  state.failed=true;state.summary='读取失败；可重试、沿用默认或输入模型 ID';state.details=(e as Error).message;
 }finally{
  if(request===modelRequest&&creatingTask&&!createSubmitting&&currentCreateCatalogContext()?.key===target.key){state.loading=false;updateCreateModelLabel();updateReasoning();renderModelMenu('create');syncModelTestButton()}
 }
}
function invalidateModelCatalogs(){
 modelCatalogControllers.create?.abort();modelCatalogControllers.task?.abort();
 modelProfileRevision++;modelRequest++;taskModelRequest++;invalidateModelTest();
 for(const target of ['create','task'] as ModelPickerTarget[]){Object.assign(modelCatalogState[target],{key:'',loading:false,summary:'配置已改变，请重新读取模型',details:'',failed:false})}
 createModels=[];taskPickerModels=[];createResolvedDefault='';
 if(creatingTask)void loadCreateModels(true);
 else if(detail&&!element('task-model-menu').classList.contains('hidden'))void refreshTaskModels();
}
// Model choice follows the Codex IDE extension: click the current model, then
// filter the list or type a name that is not listed yet.
function installModelPicker(){
 element('task-model-menu').insertAdjacentHTML('beforeend','<div class="model-catalog-footer"><p id="task-models-context" class="model-context"></p><p id="task-models-status" class="model-catalog-status" role="status"></p><div class="model-catalog-actions"><button id="task-reload-models" type="button">重读模型列表</button><button id="task-test-models" type="button">测试列表模型</button><button id="task-stop-model-test" type="button" class="hidden">停止测试</button></div><p id="task-model-test-result" class="model-test-result" role="status"></p><details class="model-catalog-details"><summary>来源与诊断</summary><p id="task-models-hint"></p></details></div>');
 button('test-models').insertAdjacentHTML('afterend','<button id="stop-model-test" type="button" class="hidden">停止测试</button>');
 button('stop-model-test').onclick=()=>stopModelTest('create');button('task-stop-model-test').onclick=()=>stopModelTest('task');
 button('task-test-models').onclick=()=>void testModelList('task');
 button('task-reload-models').onclick=()=>void refreshTaskModels(true);
 for(const target of Object.keys(modelPickers) as ModelPickerTarget[]){
  const ids=modelPickers[target];
  if(!element(ids.root))continue;
  button(ids.button).onclick=()=>void toggleModelMenu(target);
  button(ids.button).setAttribute('aria-haspopup','dialog');button(ids.button).setAttribute('aria-controls',ids.menu);
  element(ids.menu).setAttribute('role','dialog');element(ids.menu).setAttribute('aria-label','选择模型');
  element(ids.list).setAttribute('role','listbox');element(ids.list).setAttribute('aria-label','已配置的模型');
  input(ids.search).setAttribute('aria-label','搜索模型或输入自定义模型 ID');
  element(ids.menu).onkeydown=e=>{
   if(e.key==='Escape'){e.preventDefault();closeModelMenu(target);button(ids.button).focus()}
   else if(e.key==='ArrowDown'||e.key==='ArrowUp'){
    const options=Array.from(element(ids.list).querySelectorAll<HTMLButtonElement>('.model-item')),current=options.indexOf(document.activeElement as HTMLButtonElement);
    if(options.length){e.preventDefault();options[current<0?(e.key==='ArrowDown'?0:options.length-1):(current+(e.key==='ArrowDown'?1:-1)+options.length)%options.length].focus()}
   }
  };
  input(ids.search).oninput=()=>renderModelMenu(target);
  input(ids.search).onkeydown=e=>{
   if(e.key==='Escape'){e.preventDefault();closeModelMenu(target);button(ids.button).focus()}
   else if(e.key==='Enter'){e.preventDefault();element(ids.list).querySelector<HTMLButtonElement>('.model-item')?.click()}
  };
  element(ids.list).onclick=e=>{
   const node=e.target as HTMLElement;
   const effort=node.closest<HTMLElement>('[data-effort]');
   if(target==='task'&&effort){void chooseTaskEffort(effort.dataset.effort||'');return}
   const item=node.closest<HTMLElement>('[data-model]');
   if(!item)return;
   if(target==='create')void chooseCreateModel(item.dataset.model||'');
   else void chooseTaskModel(item.dataset.model==='__custom__'?input(ids.search).value.trim():item.dataset.model||'');
  };
 }
 input('custom-model').oninput=()=>{updateCreateModelLabel();updateReasoning()};
 listenWithShell(document,'click',e=>{const node=e.target as HTMLElement;for(const target of Object.keys(modelPickers) as ModelPickerTarget[])if(!node.closest('#'+modelPickers[target].root))closeModelMenu(target)});
}
async function toggleModelMenu(target:ModelPickerTarget){
 const ids=modelPickers[target];if(!element(ids.root))return;
 if(!element(ids.menu).classList.contains('hidden')){closeModelMenu(target);return}
 closeModelMenu(target==='create'?'task':'create');
 input(ids.search).value='';
 if(target==='task'){
  if(!detail)return;
  if(detail.task.engine==='deepseek-harness'){notify(harnessSessionHint);return}
  element(ids.menu).classList.remove('hidden');button(ids.button).setAttribute('aria-expanded','true');input(ids.search).focus();
  void refreshTaskModels();
  return;
 }
 renderModelMenu(target);
 element(ids.menu).classList.remove('hidden');
 button(ids.button).setAttribute('aria-expanded','true');
 input(ids.search).focus();
 void loadCreateModels();
}
function closeModelMenu(target:ModelPickerTarget){
 const ids=modelPickers[target];if(!element(ids.root))return;
 element(ids.menu).classList.add('hidden');
 button(ids.button).setAttribute('aria-expanded','false');
}
function modelRow(value:string,name:string,hint:string,active:boolean,effort?:string,probe?:ModelProbeRow){
 const labels:Record<ModelProbeStatus,string>={pending:'待测试',testing:'测试中',available:'可用',unavailable:'不可用',timeout:'超时',cancelled:'已取消',unverified:'未验证'};
 const badge=probe?`<span class="model-probe-badge model-probe-${probe.status}" title="${escapeHTML([labels[probe.status],probe.message,probe.duration_ms?`${(probe.duration_ms/1000).toFixed(1)} 秒`:''].filter(Boolean).join(' · '))}">${labels[probe.status]}</span>`:'';
 return `<button type="button" class="model-item${active?' selected':''}" role="option" aria-selected="${active}" data-model="${escapeHTML(value)}"${effort===undefined?'':` data-effort="${escapeHTML(effort)}"`}><span class="model-item-name">${escapeHTML(name)}</span>${hint?`<small>${escapeHTML(hint)}</small>`:''}${badge}${active?'<span class="model-item-check">✓</span>':''}</button>`;
}
function renderModelMenu(target:ModelPickerTarget){
 renderModelCatalogStatus(target);
 syncModelTestButton();
 const ids=modelPickers[target],search=input(ids.search).value.trim(),filter=search.toLowerCase();
 const models=target==='task'?taskPickerModels:createModels;
 const selected=target==='task'?(detail?.task.model||''):input('create-model').value;
 const matches=models.filter(m=>!filter||m.id.toLowerCase().includes(filter)||(m.name||'').toLowerCase().includes(filter));
 // Offer a free-form name only when nothing matches, so a partial word can not be
 // mistaken for a model that actually exists further down the list.
 const custom=!!filter&&matches.length===0;
 const rows:string[]=[];
 // The model id is what the CLI actually receives, so it stays the primary label;
 // a cockpit-style catalog may map an id to a different upstream alias.
 const probes=modelProbeStates[target],currentProbe=probes.key===modelListProbeContext(target)?.key;
 for(const m of matches)rows.push(modelRow(m.id,m.id,[m.name===m.id?'':m.name,m.origin==='configured'?'已配置':m.origin==='cache'?'缓存':''].filter(Boolean).join(' · '),selected===m.id,undefined,currentProbe?probes.rows[m.id.trim()]:undefined));
 const head=custom?modelRow('__custom__',`使用「${search}」`,'列表里没有这个模型，按此名称启动',selected==='__custom__'||selected===search):
  filter?'':modelRow('',defaultModelLabel,target==='task'?'沿用该环境的默认模型':createResolvedDefault?'当前默认：'+createResolvedDefault:'',!selected);
 const tail=target==='create'&&!filter?modelRow('__custom__','自定义模型…','输入列表里没有的名称',selected==='__custom__'):'';
 const empty=modelCatalogState[target].loading?'正在读取模型列表…':filter?'没有匹配的模型':'暂无模型；可使用工具默认或输入自定义 ID';
 const body=head+(rows.join('')||(custom?'':`<p class="model-empty">${empty}</p>`))+tail;
 if(target==='create'){element(ids.list).innerHTML=body;return}
 const levels=effortLevels(detail?.task.engine||'codex',models.find(m=>m.id===selected));
 // "" clears the override and hands the choice back to the tool, so an accidental
 // pick stays reversible.
 const efforts=levels.length?`<div class="model-efforts"><small>推理强度</small><div>${['',...levels].map(v=>`<button type="button" class="model-effort${(detail?.task.reasoning_effort||'')===v?' selected':''}" data-effort="${escapeHTML(v)}">${escapeHTML(v===''?'工具默认':(effortLabels[v]||v))}</button>`).join('')}</div></div>`:'<p class="model-empty">此模型不提供推理强度</p>';
 element(ids.list).innerHTML=body+efforts;
}
function taskCatalogContextKey(task:Task){return task.id+':'+catalogContextKey(settings.config.environments.find(env=>env.id===task.environment.id)||task.environment,task.engine||'codex',task.workspace)}
function mergeTaskModels(task:Task,list:EngineModel[]):EngineModel[]{
 const models=[...list],known=new Set(models.map(m=>m.id));
 const configured=engineDefaultModel(task.environment,task.engine||'codex');
 if(configured&&!known.has(configured)){models.unshift({id:configured,name:configured+'（环境默认）'});known.add(configured)}
 if(task.model&&!known.has(task.model))models.unshift({id:task.model,name:task.model+'（当前）'});
 return models;
}
async function refreshTaskModels(refresh=false){
 if(!detail||detail.task.engine==='deepseek-harness')return;
 const task=detail.task,key=taskCatalogContextKey(task),state=modelCatalogState.task;
 if(modelProbeStates.task.busy&&modelProbeStates.task.key===key)return;
 if(state.loading&&state.key===key&&!refresh)return;
 if(state.key!==key||refresh)invalidateModelTest('task');
 const request=++taskModelRequest;
 modelCatalogControllers.task?.abort();const controller=new AbortController();modelCatalogControllers.task=controller;
 taskPickerModels=mergeTaskModels(task,[]);Object.assign(state,{key,loading:true,summary:'',details:'',failed:false});renderModelMenu('task');
 try{
  const result=await api<ModelListResponse>(modelsURL(task.environment.id,task.engine||'codex',task.workspace,refresh),'GET',undefined,controller.signal);
  if(request!==taskModelRequest||!detail||taskCatalogContextKey(detail.task)!==key)return;
  taskPickerModels=mergeTaskModels(task,result.models||[]);state.summary=catalogSummary(result);state.details=catalogDetails(result);
 }catch(e){
  if(request!==taskModelRequest||!detail||taskCatalogContextKey(detail.task)!==key)return;
  state.failed=true;state.summary='读取失败；可刷新或输入模型 ID';state.details=(e as Error).message;
 }
 if(request!==taskModelRequest||!detail||taskCatalogContextKey(detail.task)!==key)return;
 state.loading=false;
 if(!element('task-model-menu').classList.contains('hidden'))renderModelMenu('task');
}
async function chooseTaskModel(id:string){
 closeModelMenu('task');
 if(!chosen||!detail)return;
 await applyTaskModel(id,undefined);
}
async function chooseTaskEffort(effort:string){
 closeModelMenu('task');
 if(!chosen||!detail)return;
 await applyTaskModel(detail.task.model,effort);
}
async function applyTaskModel(model:string,effort:string|undefined){
 if(detail?.task.engine==='deepseek-harness'){notify(harnessSessionHint);return}
 const id=chosen,body:Record<string,string>={model:model==='__custom__'?'':model};
 if(effort!==undefined)body.reasoning_effort=effort;
 try{await api('tasks/'+id,'PATCH',body);if(id===chosen)await poll();notify('已更新此任务'+(effort===undefined?'的模型':'的推理强度'))}
 catch(e){notify((e as Error).message)}
}
async function chooseCreateModel(value:string){
 closeModelMenu('create');
 const custom=input('custom-model'),typed=input('model-search').value.trim();
 if(value==='__custom__'){
  input('create-model').value='__custom__';
  if(typed&&!createModels.some(m=>m.id===typed))custom.value=typed;
  custom.classList.remove('hidden');custom.focus();
 }else{
  input('create-model').value=value;custom.classList.add('hidden');
 }
 updateCreateModelLabel();updateReasoning();
}
function updateCreateModelLabel(){
 const value=input('create-model').value,label=element('model-picker-label');
 if(!label)return;
 label.textContent=value==='__custom__'?(input('custom-model').value.trim()||'自定义模型…'):(value||(createResolvedDefault?'默认 · '+createResolvedDefault:defaultModelLabel));
}
function setCreateModelsLoading(){
 invalidateModelTest('create');
 createModels=[];createResolvedDefault='';input('create-model').value='';input('custom-model').value='';input('custom-model').classList.add('hidden');
 element('model-picker-label').textContent=defaultModelLabel;
 button('model-picker-button').disabled=false;element('model-test-result').textContent='';updateReasoning();
}
function setCreateModels(models:EngineModel[],defaultModel:string){
 createModels=models;input('create-model').value=defaultModel||'';
 element('model-picker-label').textContent=defaultModel||defaultModelLabel;
 button('model-picker-button').disabled=false;syncModelTestButton();element('model-test-result').textContent='';updateReasoning();
}
function updateReasoning(){
 const engine=input('create-engine').value,id=input('create-model').value==='__custom__'?input('custom-model').value.trim():input('create-model').value;
 const model=createModels.find(m=>m.id===id),known=model?.reasoning_levels;
 const levels=effortLevels(engine,model);
 const previous=input('create-effort').value;
 input('create-effort').innerHTML='<option value="">工具默认'+(model?.default_reasoning?' · '+escapeHTML(effortLabels[model.default_reasoning]||model.default_reasoning):'')+'</option>'+levels.map(v=>`<option value="${escapeHTML(v)}">${escapeHTML(effortLabels[v]||v)} · ${escapeHTML(v)}</option>`).join('');
 input('create-effort').value=levels.includes(previous)?previous:'';
 input('create-effort').disabled=levels.length===0;
 element('effort-hint').textContent=engine==='deepseek-harness'?(levels.length?'仅显示此模型配置已声明的推理强度。':'未声明可选推理强度，使用工具默认。')+harnessSessionHint:known?.length===0?'此模型不提供推理强度选择。':known==null?'默认沿用工具设置；手动选择需模型支持。':'';
}
