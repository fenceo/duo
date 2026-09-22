type EngineDefinition={id:string;name:string;description:string;transport:string;runnable:boolean;targets:string[];capabilities:string[];credential_kinds:string[];install_description?:string;documentation_url?:string};
type EngineProfile={id:string;name:string;engine:string;environment_id:string;kind:string;reference:string;created:number;updated:number};
type EngineCatalog={engines:EngineDefinition[];profiles:EngineProfile[];active_profile:Record<string,string>};
let engineCatalog:EngineCatalog|null=null;
function installEngineSettings(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 nav.insertAdjacentHTML('beforeend','<button type="button" data-settings="engines">AI 引擎</button>');
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-engines" class="settings-section hidden"><h3>AI 引擎与账号</h3><p>引擎负责实际干活，执行环境负责在哪里干活；账号/API 只保存目标环境里的 profile 引用，不把密钥写进简作数据库。</p><div id="engine-catalog" class="engine-catalog"><p class="muted">正在读取引擎目录…</p></div><details class="engine-profile-editor"><summary>添加或更新账号/API 引用</summary><label for="engine-profile-id">配置 ID</label><input id="engine-profile-id" placeholder="例如 codex-main"><label for="engine-profile-name">显示名称</label><input id="engine-profile-name" placeholder="工作账号"><label for="engine-profile-engine">引擎</label><select id="engine-profile-engine"></select><label for="engine-profile-environment">执行环境</label><select id="engine-profile-environment"></select><label for="engine-profile-kind">类型</label><select id="engine-profile-kind"></select><label for="engine-profile-reference">外部引用</label><input id="engine-profile-reference" placeholder="目录路径或 native profile 名称"><p class="muted">这里不填写 API key；只填写目标环境可访问的配置目录、profile 名称或后续适配器约定的引用。</p><button type="button" id="engine-profile-save" class="primary">保存引用</button><p id="engine-profile-result" role="status"></p></details></section>`);
 nav.querySelectorAll<HTMLButtonElement>('button').forEach(b=>b.addEventListener('click',()=>{if(b.dataset.settings==='engines'){nav.querySelectorAll('button').forEach(x=>x.classList.toggle('selected',x===b));for(const id of ['environment','feishu','access','engines','updates']){const section=document.getElementById('settings-'+id);if(section)section.classList.toggle('hidden',id!=='engines')};button('settings-save').classList.add('hidden');void loadEngineSettings()}}));
 button('engine-profile-save').onclick=()=>void saveEngineProfile();
}
function engineTargetName(id:string){return settings.config.environments.find(e=>e.id===id)?.name||id}
function renderEngineCatalog(){
 if(!engineCatalog)return;
 const profiles=engineCatalog.profiles;
 element('engine-catalog').innerHTML=engineCatalog.engines.map(e=>{
  const rows=profiles.filter(p=>p.engine===e.id).map(p=>`<div class="engine-profile"><span>${escapeHTML(p.name)} · ${escapeHTML(engineTargetName(p.environment_id))}</span><code>${escapeHTML(p.kind)}: ${escapeHTML(p.reference)}</code><button type="button" data-engine-activate="${escapeHTML(p.id)}">切换</button></div>`).join('');
  const active=Object.entries(engineCatalog!.active_profile).filter(([key])=>key.endsWith(':'+e.id)).map(([,value])=>value);
  return `<article class="engine-card"><header><div><strong>${escapeHTML(e.name)}</strong><small>${escapeHTML(e.transport)} · ${e.runnable?'可执行':'待接入适配器'}</small></div><span>${active.length?'已配置':'未配置'}</span></header><p>${escapeHTML(e.description)}</p><p class="muted">${escapeHTML(e.install_description||'')}</p><div class="engine-actions"><button type="button" data-engine-plan="${escapeHTML(e.id)}">查看安装/检查计划</button>${e.documentation_url?`<a href="${escapeHTML(e.documentation_url)}" target="_blank" rel="noopener noreferrer">官方文档 ↗</a>`:''}</div>${rows||'<p class="muted">还没有账号/API 引用。</p>'}<pre class="engine-plan hidden" data-engine-plan-result="${escapeHTML(e.id)}"></pre></article>`;
 }).join('');
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-plan]').forEach(b=>b.onclick=()=>void showEnginePlan(b.dataset.enginePlan!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-activate]').forEach(b=>b.onclick=()=>void activateEngineProfile(b.dataset.engineActivate!));
}
async function loadEngineSettings(){try{engineCatalog=await api<EngineCatalog>('engines');renderEngineCatalog();populateEngineProfileForm()}catch(e){element('engine-catalog').textContent=(e as Error).message}}
function populateEngineProfileForm(){
 if(!engineCatalog)return;
 const engines=element<HTMLSelectElement>('engine-profile-engine'),envs=element<HTMLSelectElement>('engine-profile-environment');
 engines.innerHTML=engineCatalog.engines.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 envs.innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 const refresh=()=>{const e=engineCatalog!.engines.find(x=>x.id===engines.value);element<HTMLSelectElement>('engine-profile-kind').innerHTML=(e?.credential_kinds||[]).map(k=>`<option value="${escapeHTML(k)}">${escapeHTML(k)}</option>`).join('')};engines.onchange=refresh;refresh();
}
async function showEnginePlan(engine:string){
 const env=settings.config.default_environment,pre=document.querySelector<HTMLElement>(`[data-engine-plan-result="${CSS.escape(engine)}"]`);if(!pre)return;pre.classList.remove('hidden');pre.textContent='正在读取计划…';
 try{const plan=await api<any>(`environments/${encodeURIComponent(env)}/engines/${encodeURIComponent(engine)}/install-plan`);pre.textContent=[plan.message,...(plan.steps||[]).map((s:any)=>'• '+s.description)].join('\n')}catch(e){pre.textContent=(e as Error).message}
}
async function saveEngineProfile(){
 const profile:EngineProfile={id:input('engine-profile-id').value.trim(),name:input('engine-profile-name').value.trim(),engine:element<HTMLSelectElement>('engine-profile-engine').value,environment_id:element<HTMLSelectElement>('engine-profile-environment').value,kind:element<HTMLSelectElement>('engine-profile-kind').value,reference:input('engine-profile-reference').value.trim(),created:0,updated:0};
 try{await api('engine-profiles','PUT',profile);element('engine-profile-result').textContent='已保存；点击对应配置的“切换”后对下一次运行生效。';await loadEngineSettings()}catch(e){element('engine-profile-result').textContent=(e as Error).message}
}
async function activateEngineProfile(id:string){try{await api(`engine-profiles/${encodeURIComponent(id)}/activate`,'POST',{});notify('账号/API 配置已切换，下一次运行生效。');await loadEngineSettings()}catch(e){notify((e as Error).message)}}
