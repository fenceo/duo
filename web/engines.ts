type EngineDefinition={id:string;name:string;description:string;transport:string;runnable:boolean;targets:string[];capabilities:string[];credential_kinds:string[];install_description?:string;documentation_url?:string};
type EngineProfile={id:string;name:string;engine:string;environment_id:string;kind:string;reference:string;created:number;updated:number};
type EngineCatalog={engines:EngineDefinition[];profiles:EngineProfile[];active_profile:Record<string,string>};
let engineCatalog:EngineCatalog|null=null;
let engineSettingsRequest=0;
function installEngineSettings(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 nav.insertAdjacentHTML('beforeend','<button type="button" data-settings="engines">AI 引擎</button>');
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-engines" class="settings-section hidden"><h3>AI 引擎与账号</h3><p>引擎负责实际干活，执行环境负责在哪里干活；账号/API 只保存目标环境里的 profile 引用，不把密钥写进Duo数据库。</p><div id="engine-catalog" class="engine-catalog"><p class="muted">正在读取引擎目录…</p></div><details class="engine-profile-editor"><summary>添加或更新账号/API 引用</summary><label for="engine-profile-id">配置 ID</label><input id="engine-profile-id" placeholder="例如 codex-main"><label for="engine-profile-name">显示名称</label><input id="engine-profile-name" placeholder="工作账号"><label for="engine-profile-engine">引擎</label><select id="engine-profile-engine"></select><label for="engine-profile-environment">执行环境</label><select id="engine-profile-environment"></select><label for="engine-profile-kind">类型</label><select id="engine-profile-kind"></select><label for="engine-profile-reference">外部引用</label><input id="engine-profile-reference" placeholder="目录路径或 native profile 名称"><p class="muted">这里不填写 API key；只填写目标环境可访问的配置目录、profile 名称或后续适配器约定的引用。</p><button type="button" id="engine-profile-save" class="primary">保存引用</button><p id="engine-profile-result" role="status"></p></details></section>`);
 button('engine-profile-save').onclick=()=>void saveEngineProfile();
 input('engine-profile-reference').nextElementSibling!.textContent='这里不填写 API key。native 继承目标环境默认配置，不指定命名 profile；配置目录引用必须是目标环境可访问的路径。';
}
function engineTargetName(id:string){return settings.config.environments.find(e=>e.id===id)?.name||id}
function engineCredentialLabel(kind:string){return ({native:'继承目标环境默认配置',dsh_home:'Harness 配置目录（DSH_HOME）',codex_home:'Codex 配置目录（CODEX_HOME）',claude_home:'Claude 配置目录（CLAUDE_CONFIG_DIR）',env_file:'环境文件引用（仅记录，尚未应用）'} as Record<string,string>)[kind]||kind}
function engineProfileActivationMessage(profile?:EngineProfile){
 if(profile?.kind==='env_file')return '引用已选中，但环境文件尚未应用到运行进程；请在目标环境中配置。';
 if(profile?.engine==='deepseek-harness')return profile.kind==='native'?'新建 Harness 任务将继承目标环境的默认配置；当前运行会话保持不变。':'新建 Harness 任务将使用该 DSH_HOME 配置目录；当前运行会话保持不变。';
 return '账号/API 配置已切换，下一次运行生效。';
}
function detectedEngineStatus(engine:string){
 const target=settings.config.environments.find(e=>e.id===settings.config.default_environment),item=target&&detectedEnvironments.find(x=>sameDetectedEnvironment(x.environment,target));
 if(!item)return '尚未检测目标环境';
 const tool=engine==='codex'?item.codex:engine==='claude'?item.claude:engine==='deepseek-harness'?item.harness:engine==='kimi'?item.kimi:item.mimo;
 return tool?.label||'未发现安装';
}
function renderEngineCatalog(){
 if(!engineCatalog)return;
 const profiles=engineCatalog.profiles;
 element('engine-catalog').innerHTML=engineCatalog.engines.map(e=>{
  const rows=profiles.filter(p=>p.engine===e.id).map(p=>`<div class="engine-profile"><span>${escapeHTML(p.name)} · ${escapeHTML(engineTargetName(p.environment_id))}</span><code>${escapeHTML(engineCredentialLabel(p.kind))}${p.kind==='native'?'':': '+escapeHTML(p.reference)}</code><button type="button" data-engine-activate="${escapeHTML(p.id)}">切换</button></div>`).join('');
  const active=Object.entries(engineCatalog!.active_profile).filter(([key])=>key.endsWith(':'+e.id)).map(([,value])=>value);
  return `<article class="engine-card"><header><div><strong>${escapeHTML(e.name)}</strong><small>${escapeHTML(e.transport)} · ${e.runnable?'可执行':'待接入适配器'}</small></div><span>${active.length?'已配置':'未配置'}</span></header><p>${escapeHTML(e.description)}</p><p class="engine-detected-status">默认环境：${escapeHTML(detectedEngineStatus(e.id))}</p><p class="muted">${escapeHTML(e.install_description||'')}</p><div class="engine-actions"><button type="button" data-engine-plan="${escapeHTML(e.id)}">查看安装/配置指南</button>${e.documentation_url?`<a href="${escapeHTML(e.documentation_url)}" target="_blank" rel="noopener noreferrer">官方文档 ↗</a>`:''}</div>${rows||'<p class="muted">还没有账号/API 引用。</p>'}<pre class="engine-plan hidden" data-engine-plan-result="${escapeHTML(e.id)}"></pre></article>`;
 }).join('');
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-plan]').forEach(b=>b.onclick=()=>void showEnginePlan(b.dataset.enginePlan!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-activate]').forEach(b=>b.onclick=()=>void activateEngineProfile(b.dataset.engineActivate!));
}
async function loadEngineSettings(){const epoch=shellEpoch,request=++engineSettingsRequest;try{const catalog=await api<EngineCatalog>('engines','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==engineSettingsRequest)return;engineCatalog=catalog;renderEngineCatalog();populateEngineProfileForm()}catch(e){if(shellCurrent(epoch)&&request===engineSettingsRequest)element('engine-catalog').textContent=(e as Error).message}}
function populateEngineProfileForm(){
 if(!engineCatalog)return;
 const engines=element<HTMLSelectElement>('engine-profile-engine'),envs=element<HTMLSelectElement>('engine-profile-environment');
 engines.innerHTML=engineCatalog.engines.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 envs.innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 const kinds=element<HTMLSelectElement>('engine-profile-kind'),reference=input('engine-profile-reference');
 const refreshReference=()=>{const native=kinds.value==='native';reference.disabled=native;reference.placeholder=kinds.value==='dsh_home'?'目标环境中的 Harness 配置目录绝对路径':native?'自动继承目标环境默认配置':'目标环境可访问的配置目录或外部引用';if(native)reference.value='default';else if(reference.value==='default')reference.value='';reference.title=native?'不是命名 profile；使用目标环境默认配置。':'不填写 API key。'};
 const refresh=()=>{const e=engineCatalog!.engines.find(x=>x.id===engines.value);kinds.innerHTML=(e?.credential_kinds||[]).filter(k=>e?.id!=='deepseek-harness'||k!=='env_file').map(k=>`<option value="${escapeHTML(k)}">${escapeHTML(engineCredentialLabel(k))}</option>`).join('');refreshReference()};engines.onchange=refresh;kinds.onchange=refreshReference;refresh();
}
async function showEnginePlan(engine:string){
 const env=settings.config.default_environment,pre=document.querySelector<HTMLElement>(`[data-engine-plan-result="${CSS.escape(engine)}"]`);if(!pre)return;pre.classList.remove('hidden');pre.textContent='正在读取计划…';
 try{const plan=await api<any>(`environments/${encodeURIComponent(env)}/engines/${encodeURIComponent(engine)}/install-plan`);pre.textContent=[plan.message,...(plan.steps||[]).map((s:any)=>'• '+s.description)].join('\n')}catch(e){pre.textContent=(e as Error).message}
}
async function saveEngineProfile(){
 const profile:EngineProfile={id:input('engine-profile-id').value.trim(),name:input('engine-profile-name').value.trim(),engine:element<HTMLSelectElement>('engine-profile-engine').value,environment_id:element<HTMLSelectElement>('engine-profile-environment').value,kind:element<HTMLSelectElement>('engine-profile-kind').value,reference:input('engine-profile-reference').value.trim(),created:0,updated:0};
 const epoch=shellEpoch;
 try{await api('engine-profiles','PUT',profile);if(!shellCurrent(epoch))return;invalidateModelCatalogs();element('engine-profile-result').textContent=profile.kind==='env_file'?'已保存引用；环境文件目前仅记录，不会应用到运行进程。':profile.engine==='deepseek-harness'?'已保存；点击对应配置的“切换”后，新建 Harness 任务使用该配置。':'已保存；点击对应配置的“切换”后对下一次运行生效。';await loadEngineSettings()}catch(e){if(shellCurrent(epoch))element('engine-profile-result').textContent=(e as Error).message}
}
async function activateEngineProfile(id:string){const epoch=shellEpoch;try{await api(`engine-profiles/${encodeURIComponent(id)}/activate`,'POST',{});if(!shellCurrent(epoch))return;invalidateModelCatalogs();notify(engineProfileActivationMessage(engineCatalog?.profiles.find(p=>p.id===id)));await loadEngineSettings()}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}}
