type HandoffPreview={source_task_id:string;source_engine:string;source_title:string;transferred_runs:number;transferred_knowledge:number;context:string;context_truncated:boolean;fingerprint:string;archive_bytes:number};
let handoffTask:Task|null=null,handoffCatalog:EngineCatalog|null=null,handoffPreview:HandoffPreview|null=null;
let handoffGeneration=0,handoffPreviewRequest=0,handoffModelRequest=0,handoffBusy=false;
let handoffController:AbortController|null=null,handoffModels:EngineModel[]=[];
let handoffSourceID='';
function handoffCurrent(generation:number,epoch:number){return generation===handoffGeneration&&shellCurrent(epoch)&&!!element<HTMLDialogElement>('handoff-dialog')?.open}
function ensureHandoffDialog(){
 if(element('handoff-dialog'))return;
 element('root').insertAdjacentHTML('beforeend',`<dialog id="handoff-dialog" aria-labelledby="handoff-title"><form id="handoff-form"><h2 id="handoff-title">切换 AI，继续当前任务</h2><p class="muted">聊天记录、任务知识和草稿保留。切换后发送下一条消息，目标 AI 才开始工作。</p><p id="handoff-source" class="muted"></p><fieldset id="handoff-fields" disabled><div class="handoff-grid"><div><label for="handoff-engine">AI 引擎</label><select id="handoff-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select></div><div><label for="handoff-profile">账号 / API 配置</label><select id="handoff-profile"></select></div><div><label for="handoff-model">模型</label><input id="handoff-model" list="handoff-models" placeholder="沿用该配置默认模型" maxlength="120"><datalist id="handoff-models"></datalist></div><div><label for="handoff-effort">推理强度</label><select id="handoff-effort"></select></div></div><p id="handoff-model-status" class="muted" role="status"></p><button type="button" id="handoff-model-refresh">重读模型列表</button><button type="button" id="handoff-settings">管理账号 / API</button><label for="handoff-mode">工作权限</label><select id="handoff-mode"></select><details class="handoff-advanced"><summary>执行位置与接续范围</summary><label for="handoff-environment">执行环境</label><select id="handoff-environment"></select><label for="handoff-workspace">工作目录</label><input id="handoff-workspace" required><label for="handoff-context-mode">交给新会话的资料</label><select id="handoff-context-mode"><option value="full">完整历史文本 + 接续摘要</option><option value="notes">仅任务笔记与共识</option></select></details><div id="handoff-legacy" hidden><label class="check-row"><input type="checkbox" id="handoff-preserve-legacy">我确认所选配置就是此任务原来的账号 / API，保留原会话继续</label><p class="muted">旧版本没有保存账号归属，请核对上方所选配置。无法确认或已更换账号时，请保持不勾选，通过新会话接续历史。</p></div><p id="handoff-route-hint" class="muted"></p><p id="handoff-preview-status" role="status"></p><details><summary>查看将交接的摘要</summary><pre id="handoff-preview" class="handoff-preview"></pre></details><a id="handoff-archive" target="_blank" rel="noopener noreferrer">查看完整脱敏文本 ↗</a><p class="muted">跨引擎或账号会建立新原生会话；图片等附件请按需重新提供。API 地址和密钥由所选配置目录管理，不会随历史复制。</p></fieldset><p id="handoff-error" class="error" role="alert"></p><div class="dialog-footer"><button type="button" id="handoff-cancel">取消</button><button type="button" id="handoff-refresh">刷新预览</button><button class="primary" id="handoff-submit" disabled>确认切换</button></div></form></dialog>`);
 element('handoff-form').onsubmit=e=>{e.preventDefault();void submitHandoff()};
 const dialog=element<HTMLDialogElement>('handoff-dialog');
 dialog.addEventListener('cancel',e=>{if(handoffBusy)e.preventDefault()});
 dialog.addEventListener('close',()=>{handoffGeneration++;handoffController?.abort();handoffTask=null;handoffPreview=null});
 disposeWithShell(()=>{handoffGeneration++;handoffController?.abort();handoffBusy=false;handoffTask=null;handoffPreview=null});
 button('handoff-cancel').onclick=()=>{if(!handoffBusy)dialog.close()};
 button('handoff-refresh').textContent='重读任务与预览';button('handoff-refresh').onclick=()=>void openHandoff(handoffSourceID);
 button('handoff-model-refresh').onclick=()=>void loadHandoffModels();
 button('handoff-settings').onclick=async()=>{dialog.close();await openAccountCenter()};
 input('handoff-preserve-legacy').onchange=()=>updateHandoffHint();
 input('handoff-mode').onchange=()=>resetLegacyConfirmation();
 input('handoff-engine').onchange=()=>updateHandoffTarget(true);
 input('handoff-environment').onchange=()=>{const env=settings.config.environments.find(v=>v.id===input('handoff-environment').value);if(env)input('handoff-workspace').value=env.id===handoffTask?.environment.id?handoffTask.workspace:env.workspaces[0]||'';updateHandoffTarget(true)};
 input('handoff-profile').onchange=()=>{input('handoff-model').value='';resetLegacyConfirmation();void loadHandoffModels()};
 input('handoff-workspace').oninput=()=>resetLegacyConfirmation();
 input('handoff-workspace').onchange=()=>{resetLegacyConfirmation();void loadHandoffModels()};
 input('handoff-model').oninput=()=>updateHandoffEffort();
 input('handoff-context-mode').onchange=()=>void loadHandoffPreview();
}
function updateHandoffTarget(reset:boolean){
 const task=handoffTask;if(!task||!handoffCatalog)return;
 const engine=input('handoff-engine').value,env=input('handoff-environment').value,same=engine===task.engine&&env===task.environment.id;
 const profiles=handoffCatalog.profiles.filter(p=>p.engine===engine&&p.environment_id===env&&p.kind!=='env_file');
 const active=profiles.find(p=>p.id===handoffCatalog!.active_profile[env+':'+engine]);
 input('handoff-profile').innerHTML=`<option value="__environment__">跟随环境账号 · ${escapeHTML(active?.name||'原生默认登录')}</option>`+(same&&task.binding&&task.binding.account_mode!=='environment'?`<option value="__current__">仅此任务 · ${escapeHTML(task.binding.profile?.name||'原生默认配置')}</option>`:'')+'<option value="">仅此任务 · 原生默认配置</option>'+profiles.map(p=>`<option value="${escapeHTML(p.id)}">仅此任务 · ${escapeHTML(p.name)}</option>`).join('');
 input('handoff-profile').value=same&&task.binding&&task.binding.account_mode!=='environment'?'__current__':'__environment__';
 const modes=workCatalog.modes.filter(m=>modeSupportsEngine(m,engine));
 input('handoff-mode').innerHTML=(same&&!task.binding?'<option value="__current__">原任务权限（保持不变）</option>':'')+modes.map(m=>`<option value="${escapeHTML(m.id)}">${escapeHTML(modeLabel(m))}</option>`).join('');
 input('handoff-mode').value=same&&!task.binding?'__current__':modes.some(m=>m.id===task.mode?.id)?task.mode!.id:modes.find(m=>m.id==='work')?.id||modes[0]?.id||'';
 if(reset)input('handoff-model').value=same?task.model:'';
 handoffModels=[];updateHandoffEffort();if(same)input('handoff-effort').value=task.reasoning_effort||'';
 resetLegacyConfirmation();void loadHandoffModels();
}
function resetLegacyConfirmation(){input('handoff-preserve-legacy').checked=false;updateHandoffHint()}
function updateHandoffHint(){
 const task=handoffTask;if(!task)return;
 const eligible=!task.binding&&!!task.session&&input('handoff-engine').value===task.engine&&input('handoff-environment').value===task.environment.id&&input('handoff-workspace').value===task.workspace&&input('handoff-mode').value==='__current__';
 element('handoff-legacy').hidden=!eligible;if(!eligible)input('handoff-preserve-legacy').checked=false;
 const preserve=eligible&&input('handoff-preserve-legacy').checked;
 button('handoff-submit').textContent=preserve?'确认原配置，保留原会话':'确认切换';
 const reuse=!!task.binding&&input('handoff-engine').value===task.engine&&input('handoff-profile').value==='__current__'&&input('handoff-environment').value===task.environment.id&&input('handoff-workspace').value===task.workspace;
 element('handoff-route-hint').textContent=preserve?'只补齐账号配置绑定，保留原生会话；下一条消息继续原上下文。':input('handoff-profile').value==='__environment__'?'每轮开始时使用此环境的当前账号；环境账号改变后自动以完整任务历史接续，正在执行的一轮保持原账号。':reuse?'仅此任务固定使用所选账号；只更换模型时沿用原会话，环境或 Harness 权限改变时通过新会话接续。':'仅此任务使用所选账号，不跟随环境账号切换；账号改变时通过新会话接续历史。';
}
function updateHandoffEffort(){
 const select=input('handoff-effort'),previous=select.value,levels=effortLevels(input('handoff-engine').value,handoffModels.find(m=>m.id===input('handoff-model').value));
 select.innerHTML='<option value="">沿用模型默认</option>'+levels.map(v=>`<option value="${escapeHTML(v)}">${escapeHTML(effortLabels[v]||v)}</option>`).join('');
 select.value=levels.includes(previous)?previous:'';
}
async function loadHandoffModels(){
 if(!handoffTask)return;
 const generation=handoffGeneration,epoch=shellEpoch,request=++handoffModelRequest,task=handoffTask;
 const engine=input('handoff-engine').value,env=input('handoff-environment').value,profile=input('handoff-profile').value,workspace=input('handoff-workspace').value;
 const url=modelsURL(env,engine,workspace)+(profile==='__current__'?'&task_id='+encodeURIComponent(task.id):profile==='__environment__'?'':'&profile_id='+encodeURIComponent(profile));
 element('handoff-model-status').textContent='正在读取所选配置的模型列表…';element('handoff-models').innerHTML='';
 try{const result=await api<ModelListResponse>(url,'GET',undefined,handoffController?.signal);if(!handoffCurrent(generation,epoch)||request!==handoffModelRequest)return;handoffModels=result.models||[];element('handoff-models').innerHTML=handoffModels.map(m=>`<option value="${escapeHTML(m.id)}">${escapeHTML(m.name)}</option>`).join('');element('handoff-model-status').textContent=catalogSummary(result)+'；也可填写模型 ID，是否可用以实际执行为准。';updateHandoffEffort()}
 catch(e){if(handoffCurrent(generation,epoch)&&request===handoffModelRequest){handoffModels=[];element('handoff-model-status').textContent='模型列表读取失败，可重试或填写模型 ID：'+(e as Error).message}}
}
async function loadHandoffPreview(){
 if(!handoffTask||handoffBusy)return;
 const task=handoffTask,generation=handoffGeneration,epoch=shellEpoch,request=++handoffPreviewRequest,mode=input('handoff-context-mode').value;
 handoffPreview=null;button('handoff-submit').disabled=true;element('handoff-error').textContent='';element('handoff-preview-status').textContent='正在整理本地接续资料…';element('handoff-preview').textContent='';
 element('handoff-archive').removeAttribute('href');
 try{const p=await api<HandoffPreview>('tasks/'+encodeURIComponent(task.id)+'/continuation/preview?mode='+encodeURIComponent(mode),'GET',undefined,handoffController?.signal);if(!handoffCurrent(generation,epoch)||request!==handoffPreviewRequest)return;handoffPreview=p;element('handoff-preview-status').textContent=`所选资料：${p.transferred_runs} 轮对话、${p.transferred_knowledge} 条笔记，全文 ${(p.archive_bytes/1024).toFixed(1)} KiB。${p.context_truncated?'摘要仅含重点，全文保留所选历史。':''}`;element('handoff-preview').textContent=p.context;element('handoff-archive').setAttribute('href','/api/tasks/'+encodeURIComponent(task.id)+'/continuation/archive?mode='+encodeURIComponent(mode));button('handoff-submit').disabled=false}
 catch(e){if(handoffCurrent(generation,epoch)&&request===handoffPreviewRequest){element('handoff-preview-status').textContent='接续资料读取失败';element('handoff-error').textContent=(e as Error).message}}
}
async function openHandoff(id=chosen,preferredProfileID=''){
 if(!id||handoffBusy)return;
 ensureHandoffDialog();handoffController?.abort();handoffController=new AbortController();handoffTask=null;handoffPreview=null;
 handoffSourceID=id;input('handoff-preserve-legacy').checked=false;element('handoff-legacy').hidden=true;button('handoff-submit').textContent='确认切换';
 const generation=++handoffGeneration,epoch=shellEpoch,dialog=element<HTMLDialogElement>('handoff-dialog');if(!dialog.open)dialog.showModal();
 element<HTMLFieldSetElement>('handoff-fields').disabled=true;button('handoff-submit').disabled=true;element('handoff-error').textContent='';element('handoff-source').textContent='正在读取任务和配置…';
 try{
  const [snapshot,catalog]=await Promise.all([api<Detail>('tasks/'+encodeURIComponent(id)+'?recent=1','GET',undefined,handoffController.signal),api<EngineCatalog>('engines','GET',undefined,handoffController.signal)]);
  if(!handoffCurrent(generation,epoch))return;
  handoffTask=snapshot.task;handoffCatalog=catalog;
  element('handoff-source').textContent=snapshot.task.title+' · '+taskEngineName(snapshot.task.engine)+(snapshot.task.binding?'':' · 旧会话尚未绑定账号配置：可确认原配置并保留原会话');
  if(snapshot.task.archived||snapshot.task.deleted||snapshot.runs.some(r=>r.status==='running'||r.status==='queued'))throw new Error('请先恢复任务，或等待执行结束 / 停止并取消排队后再切换。');
  input('handoff-environment').innerHTML=settings.config.environments.map(v=>`<option value="${escapeHTML(v.id)}">${escapeHTML(environmentOptionLabel(v))}</option>`).join('');input('handoff-environment').value=snapshot.task.environment.id;
  input('handoff-engine').value=snapshot.task.engine;input('handoff-workspace').value=snapshot.task.workspace;input('handoff-context-mode').value='full';
  if(preferredProfileID){
   const profile=catalog.profiles.find(p=>p.id===preferredProfileID),env=settings.config.environments.find(e=>e.id===profile?.environment_id);
   if(!profile||!env||profile.kind==='env_file'||!catalog.engines.some(e=>e.id===profile.engine&&e.runnable!==false))throw Error('所选账号或环境已不可用，请刷新账号列表。');
   input('handoff-environment').value=env.id;input('handoff-engine').value=profile.engine;input('handoff-workspace').value=env.id===snapshot.task.environment.id?snapshot.task.workspace:env.workspaces[0]||'';
  }
  element<HTMLFieldSetElement>('handoff-fields').disabled=false;updateHandoffTarget(true);
  if(preferredProfileID){
   const profile=catalog.profiles.find(p=>p.id===preferredProfileID)!,bound=snapshot.task.binding?.profile;
   const same=snapshot.task.binding?.account_mode!=='environment'&&bound?.id===profile.id&&bound.engine===profile.engine&&bound.environment_id===profile.environment_id&&bound.kind===profile.kind&&bound.reference===profile.reference;
   input('handoff-profile').value=same?'__current__':preferredProfileID;if(!same)input('handoff-model').value='';resetLegacyConfirmation();void loadHandoffModels();
  }
  await loadHandoffPreview();
 }catch(e){if(handoffCurrent(generation,epoch)){element('handoff-error').textContent=(e as Error).message;button('handoff-submit').disabled=true}}
}
async function submitHandoff(){
 if(!handoffTask||!handoffPreview||handoffBusy)return;
 const task=handoffTask,preview=handoffPreview,generation=handoffGeneration,epoch=shellEpoch,selectionAtStart=selection;
 const profileID=input('handoff-profile').value;
 const expectedProfileID=profileID==='__environment__'?handoffCatalog?.active_profile[input('handoff-environment').value+':'+input('handoff-engine').value]||'':profileID;
 const preserve=!task.binding&&!!task.session&&input('handoff-preserve-legacy').checked;
 const body={preserve_legacy_session:preserve,expected_legacy_session:preserve?task.session:undefined,confirm:true,expected_binding_revision:task.binding?.revision||'legacy',environment_id:input('handoff-environment').value,engine:input('handoff-engine').value,workspace:input('handoff-workspace').value,model:input('handoff-model').value,reasoning_effort:input('handoff-effort').value,mode_id:input('handoff-mode').value,profile_id:profileID,expected_profile:profileID==='__current__'?undefined:handoffCatalog?.profiles.find(p=>p.id===expectedProfileID),context_mode:input('handoff-context-mode').value,fingerprint:preview.fingerprint};
 handoffBusy=true;element<HTMLFieldSetElement>('handoff-fields').disabled=true;for(const id of ['handoff-submit','handoff-refresh','handoff-cancel'])button(id).disabled=true;element('handoff-error').textContent='';
 try{
  const result=await api<{task:Task;new_session:boolean}>('tasks/'+encodeURIComponent(task.id)+'/handoff','POST',body);
  if(!handoffCurrent(generation,epoch))return;
  handoffBusy=false;tasks=tasks.map(t=>t.id===result.task.id?result.task:t);
  if(chosen===task.id&&selection===selectionAtStart&&detail){detail.task=result.task;invalidateModelCatalogs();renderTask();renderList()}
  element<HTMLDialogElement>('handoff-dialog').close();notify(result.new_session?'已切换 AI。发送下一条消息时，将带上摘要和完整所选历史继续当前任务。':'模型与配置已更新，下一条消息继续原会话。');
 }catch(e){if(handoffCurrent(generation,epoch)){element('handoff-error').textContent=(e as Error).message+'；若提示预览过期，请刷新后重试。'}}
 finally{if(shellCurrent(epoch)){handoffBusy=false;if(element('handoff-dialog')){element<HTMLFieldSetElement>('handoff-fields').disabled=false;for(const id of ['handoff-refresh','handoff-cancel'])button(id).disabled=false;button('handoff-submit').disabled=!handoffPreview}}}
}
