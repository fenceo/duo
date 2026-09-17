type EngineModel={id:string;name:string;reasoning_levels?:string[]|null;default_reasoning?:string};
type ModelListResponse={models:EngineModel[];modified:number;message?:string;source:string};
type ModelPickerTarget='create'|'task';
let createModels:EngineModel[]=[];
let taskPickerModels:EngineModel[]=[];
let taskPickerStatus='',taskModelRequest=0;
const modelsByEnv:Record<string,{models:EngineModel[];message:string;expires:number}>={};
const effortLabels:Record<string,string>={none:'关闭',minimal:'极低',low:'低',medium:'中',high:'高',xhigh:'很高',max:'最高',ultra:'Ultra（工具可能自动委派）'};
const defaultModelLabel='使用此工具的默认模型';
const modelPickers:Record<ModelPickerTarget,{root:string;button:string;label:string;menu:string;search:string;list:string}>={
 create:{root:'model-picker',button:'model-picker-button',label:'model-picker-label',menu:'model-menu',search:'model-search',list:'model-list'},
 task:{root:'task-model',button:'task-model-button',label:'task-model-label',menu:'task-model-menu',search:'task-model-search',list:'task-model-list'}
};
function taskEngineName(engine?:string){return engine==='claude'?'Claude Code':'Codex'}
function effortLevels(engine:string,model?:EngineModel):string[]{
 if(model&&Array.isArray(model.reasoning_levels))return model.reasoning_levels;
 return engine==='claude'?['low','medium','high','xhigh','max']:['low','medium','high','xhigh'];
}
function installExecution(){
 input('create-engine').onchange=()=>void loadCreateEnvironment(true);
 installModelPicker();
 element('setting-model').previousElementSibling!.textContent='Codex 默认模型（可留空）';
 element('setting-model').insertAdjacentHTML('afterend',`<label for="setting-claude">此环境中的 Claude Code 可执行文件</label><input id="setting-claude" placeholder="claude"><label for="setting-claude-model">Claude 默认模型（可留空）</label><input id="setting-claude-model" placeholder="例如 sonnet，或你的服务提供的模型 ID"><label for="setting-engine">默认 AI 工具（飞书新建也使用它）</label><select id="setting-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select><p class="muted">Claude 自动接受工作目录内的文件编辑；其他操作沿用该环境中的 Claude 权限设置。</p>`);
 button('check-codex').textContent='检查 Codex';
 button('check-codex').insertAdjacentHTML('afterend',' <button type="button" id="check-claude">检查 Claude</button>');
 button('check-claude').onclick=async()=>{button('check-claude').disabled=true;try{const r=await api('check','POST',{environment_id:editingID,engine:'claude'});element('check-result').textContent=(r.ok?'Claude 已配置\n':'Claude 检查失败\n')+r.output}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-claude').disabled=false}};
}
// Model choice follows the Codex IDE extension: click the current model, then
// filter the list or type a name that is not listed yet.
function installModelPicker(){
 for(const target of Object.keys(modelPickers) as ModelPickerTarget[]){
  const ids=modelPickers[target];
  if(!element(ids.root))continue;
  button(ids.button).onclick=()=>void toggleModelMenu(target);
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
 document.addEventListener('click',e=>{const node=e.target as HTMLElement;for(const target of Object.keys(modelPickers) as ModelPickerTarget[])if(!node.closest('#'+modelPickers[target].root))closeModelMenu(target)});
}
async function toggleModelMenu(target:ModelPickerTarget){
 const ids=modelPickers[target];if(!element(ids.root))return;
 if(!element(ids.menu).classList.contains('hidden')){closeModelMenu(target);return}
 closeModelMenu(target==='create'?'task':'create');
 input(ids.search).value='';
 if(target==='task'){
  if(!detail)return;
  const request=++taskModelRequest,key=modelCacheKey(detail.task),cached=modelsByEnv[key];
  taskPickerModels=cached?.models?.slice()||[];taskPickerStatus=cached?.message||'';
  if(cached?.expires>Date.now()&&taskPickerModels.length){
   renderModelMenu(target);element(ids.menu).classList.remove('hidden');button(ids.button).setAttribute('aria-expanded','true');input(ids.search).focus();
  }else{
   taskPickerStatus='正在读取模型列表…';
  }
  renderModelMenu(target);
  element(ids.menu).classList.remove('hidden');button(ids.button).setAttribute('aria-expanded','true');input(ids.search).focus();
  void loadTaskModels(request,detail.task);
  return;
 }
 renderModelMenu(target);
 element(ids.menu).classList.remove('hidden');
 button(ids.button).setAttribute('aria-expanded','true');
 input(ids.search).focus();
}
function closeModelMenu(target:ModelPickerTarget){
 const ids=modelPickers[target];if(!element(ids.root))return;
 element(ids.menu).classList.add('hidden');
 button(ids.button).setAttribute('aria-expanded','false');
}
function modelRow(value:string,name:string,hint:string,active:boolean,effort?:string){
 return `<button type="button" class="model-item${active?' selected':''}" role="option" aria-selected="${active}" data-model="${escapeHTML(value)}"${effort===undefined?'':` data-effort="${escapeHTML(effort)}"`}><span class="model-item-name">${escapeHTML(name)}</span>${hint?`<small>${escapeHTML(hint)}</small>`:''}${active?'<span class="model-item-check">✓</span>':''}</button>`;
}
function renderModelMenu(target:ModelPickerTarget){
 const ids=modelPickers[target],search=input(ids.search).value.trim(),filter=search.toLowerCase();
 const models=target==='task'?taskPickerModels:createModels;
 const selected=target==='task'?(detail?.task.model||''):input('create-model').value;
 const matches=models.filter(m=>!filter||m.id.toLowerCase().includes(filter)||m.name.toLowerCase().includes(filter));
 // Offer a free-form name only when nothing matches, so a partial word can not be
 // mistaken for a model that actually exists further down the list.
 const custom=!!filter&&matches.length===0;
 const rows:string[]=[];
 // The model id is what the CLI actually receives, so it stays the primary label;
 // a cockpit-style catalog may map an id to a different upstream alias.
 for(const m of matches)rows.push(modelRow(m.id,m.id,m.name===m.id?'':m.name,selected===m.id));
 const head=custom?modelRow('__custom__',`使用「${search}」`,'列表里没有这个模型，按此名称启动',selected==='__custom__'||selected===search):
  filter?'':modelRow('',defaultModelLabel,target==='task'?'沿用该环境的默认模型':'',!selected);
 const tail=target==='create'&&!filter?modelRow('__custom__','自定义模型…','输入列表里没有的名称',selected==='__custom__'):'';
 const body=head+(rows.join('')||(custom?'':'<p class="model-empty">没有匹配的模型</p>'))+tail;
 if(target==='create'){element(ids.list).innerHTML=body;return}
 const levels=effortLevels(detail?.task.engine||'codex',models.find(m=>m.id===selected));
 // "" clears the override and hands the choice back to the tool, so an accidental
 // pick stays reversible.
 const efforts=levels.length?`<div class="model-efforts"><small>推理强度</small><div>${['',...levels].map(v=>`<button type="button" class="model-effort${(detail?.task.reasoning_effort||'')===v?' selected':''}" data-effort="${escapeHTML(v)}">${escapeHTML(v===''?'工具默认':(effortLabels[v]||v))}</button>`).join('')}</div></div>`:'<p class="model-empty">此模型不提供推理强度</p>';
 const status=taskPickerStatus?`<p class="model-empty">${escapeHTML(taskPickerStatus)}</p>`:'';
 element(ids.list).innerHTML=body+status+efforts;
}
function modelCacheKey(task:Task){return (task.environment?.id||'')+':'+(task.engine||'codex')}
function mergeTaskModels(task:Task,list:EngineModel[]):EngineModel[]{
 const models=[...list],known=new Set(models.map(m=>m.id));
 const configured=task.engine==='claude'?task.environment.claude_model:task.environment.model;
 if(configured&&!known.has(configured)){models.unshift({id:configured,name:configured+'（环境默认）'});known.add(configured)}
 if(task.model&&!known.has(task.model))models.unshift({id:task.model,name:task.model+'（当前）'});
 return models;
}
async function loadTaskModels(request:number,task:Task){
 const engine=task.engine||'codex',key=modelCacheKey(task);
 try{
 const result=await api<ModelListResponse>('environments/'+task.environment.id+'/models?engine='+engine);
  if(request!==taskModelRequest||detail?.task.id!==task.id)return;
  const catalog=result.models||[],models=mergeTaskModels(task,catalog),message=result.message||result.source+(result.modified?' · '+new Date(result.modified).toLocaleString():'')||'';
  taskPickerModels=models;taskPickerStatus=message;
  if(catalog.length)modelsByEnv[key]={models,message,expires:Date.now()+30000};else delete modelsByEnv[key];
 }catch(e){
  if(request!==taskModelRequest||detail?.task.id!==task.id)return;
  delete modelsByEnv[key];
  taskPickerModels=mergeTaskModels(task,[]);
  taskPickerStatus=(e as Error).message+'。可直接输入模型名称后按 Enter。';
 }
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
 label.textContent=value==='__custom__'?(input('custom-model').value.trim()||'自定义模型…'):(value||defaultModelLabel);
}
function setCreateModelsLoading(){
 createModels=[];input('create-model').value='';input('custom-model').value='';input('custom-model').classList.add('hidden');
 element('model-picker-label').textContent='正在读取模型列表…';
 button('model-picker-button').disabled=true;closeModelMenu('create');updateReasoning();
}
function setCreateModels(models:EngineModel[],defaultModel:string){
 createModels=models;input('create-model').value=defaultModel||'';
 element('model-picker-label').textContent=defaultModel||defaultModelLabel;
 button('model-picker-button').disabled=false;updateReasoning();
}
function updateReasoning(){
 const engine=input('create-engine').value,id=input('create-model').value==='__custom__'?input('custom-model').value.trim():input('create-model').value;
 const model=createModels.find(m=>m.id===id),known=model?.reasoning_levels;
 const levels=known??effortLevels(engine);
 const previous=input('create-effort').value;
 input('create-effort').innerHTML='<option value="">工具默认'+(model?.default_reasoning?' · '+escapeHTML(effortLabels[model.default_reasoning]||model.default_reasoning):'')+'</option>'+levels.map(v=>`<option value="${escapeHTML(v)}">${escapeHTML(effortLabels[v]||v)} · ${escapeHTML(v)}</option>`).join('');
 input('create-effort').value=levels.includes(previous)?previous:'';
 input('create-effort').disabled=levels.length===0;
 element('effort-hint').textContent=known?.length===0?'此模型不提供推理强度选择。':known==null?'默认沿用工具设置；手动选择需模型支持。':'';
}
