type EngineDefinition={id:string;name:string;description:string;transport:string;runnable:boolean;targets:string[];capabilities:string[];credential_kinds:string[];install_description?:string;documentation_url?:string};
type EngineProfile={id:string;name:string;engine:string;environment_id:string;kind:string;reference:string;created:number;updated:number};
type EngineCatalog={engines:EngineDefinition[];profiles:EngineProfile[];active_profile:Record<string,string>};
type CodexSyncResult={environment_id:string;state:string;message:string};
type CodexSyncResponse={source_profile_id:string;results:CodexSyncResult[]};
type DetectedEngineTool={path:string;state:string;label:string};
type EngineEnvironmentStatus={environment_id:string;name:string;type:string;distro?:string;user?:string;host?:string;codex:DetectedEngineTool;claude:DetectedEngineTool;harness:DetectedEngineTool;kimi:DetectedEngineTool;mimo:DetectedEngineTool;message?:string};
type EngineStatusResponse={items:EngineEnvironmentStatus[];message?:string};
let engineCatalog:EngineCatalog|null=null;
let engineSettingsRequest=0;
function installEngineSettings(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 nav.insertAdjacentHTML('beforeend','<button type="button" data-settings="engines">AI 引擎</button>');
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-engines" class="settings-section hidden"><h3>引擎安装</h3><p>引擎负责实际干活，执行环境负责在哪里干活；账号/API 只保存目标环境里的 profile 引用，不把密钥写进Duo数据库。</p><div id="engine-catalog" class="engine-catalog"><p class="muted">正在读取引擎目录…</p></div><details class="engine-profile-editor"><summary>添加或更新账号/API 引用</summary><label for="engine-profile-id">配置 ID</label><input id="engine-profile-id" placeholder="例如 codex-main"><label for="engine-profile-name">显示名称</label><input id="engine-profile-name" placeholder="工作账号"><label for="engine-profile-engine">引擎</label><select id="engine-profile-engine"></select><label for="engine-profile-environment">执行环境</label><select id="engine-profile-environment"></select><label for="engine-profile-kind">类型</label><select id="engine-profile-kind"></select><label for="engine-profile-reference">外部引用</label><input id="engine-profile-reference" placeholder="目录路径或 native profile 名称"><p class="muted">这里不填写 API key；只填写目标环境可访问的配置目录、profile 名称或后续适配器约定的引用。</p><button type="button" id="engine-profile-save" class="primary">保存引用</button><p id="engine-profile-result" role="status"></p></details></section>`);
 button('engine-profile-save').onclick=()=>void saveEngineProfile();
 input('engine-profile-reference').nextElementSibling!.textContent='这里不填写 API key。native 继承目标环境默认配置，不指定命名 profile；配置目录引用必须是目标环境可访问的路径。';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="codex-sync-dialog"><form id="codex-sync-form"><h2>同步账号到环境</h2><p id="codex-sync-source" class="muted"></p><p>将所选账号的登录信息、API 地址及默认模型写入勾选环境的原生配置。每个目标先备份原文件，保留工具权限和 MCP 设置。完成后重新打开目标 CLI；已有 Duo 任务请通过“切换 AI”重新选择环境默认配置。</p><fieldset id="codex-sync-targets"></fieldset><p id="codex-sync-status" role="status"></p><div class="dialog-footer"><button type="button" id="codex-sync-cancel">取消</button><button type="submit" class="primary" id="codex-sync-submit">开始同步</button></div></form></dialog>`);
 element('root').insertAdjacentHTML('beforeend',`<dialog id="account-center-dialog"><h2>账号管理</h2><p class="muted">管理各环境中的账号与中转站配置。“设为新任务默认”只影响 Duo 新任务；“同步到环境”会写入所选环境的原生账号文件。</p><div id="account-center-list"></div><div id="account-center-editor"></div><div id="engine-account-status" role="status"></div><button type="button" id="engine-account-cancel" class="hidden">取消当前操作</button><p id="account-center-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-center-manage">添加账号或引用</button><button type="button" id="account-center-close">关闭</button></div></dialog>`);
 button('account-center-close').onclick=()=>element<HTMLDialogElement>('account-center-dialog').close();
 button('account-center-manage').onclick=()=>{element('engine-account-fields').querySelector('details')!.open=true;element('account-center-editor').scrollIntoView({block:'start'});input('engine-account-name').focus()};
 button('codex-sync-cancel').onclick=()=>element<HTMLDialogElement>('codex-sync-dialog').close();
 element('codex-sync-form').addEventListener('submit',e=>{e.preventDefault();void submitCodexSync()});
 installEngineOnboarding();
 const editor=element('account-center-editor');editor.append(element('settings-engines').querySelector('.engine-profile-editor')!);
 const accountFields=document.createElement('fieldset');accountFields.id='engine-account-fields';accountFields.innerHTML='<legend>添加账号 / API</legend><label for="engine-account-environment">保存账号的 Windows 环境</label><select id="engine-account-environment"></select><label for="engine-account-engine">引擎</label><select id="engine-account-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select>';accountFields.append(element('engine-setup-fields').querySelector('details')!);editor.prepend(accountFields);
 element('engine-account-engine').onchange=refreshAccountLogin;refreshAccountLogin();
 button('engine-account-cancel').onclick=button('engine-setup-cancel').onclick;
 const installEntry=document.createElement('button');installEntry.id='engines-open';installEntry.className='subtle';installEntry.textContent='引擎安装';element('settings-open').before(installEntry);installEntry.onclick=async()=>{await openSettings();if(element<HTMLDialogElement>('settings-dialog').open)showSettingsSection('engines')};
 engineCatalog=null;accountSyncBusy=false;
}
async function openAccountCenter(){
 const epoch=shellEpoch,dialog=element<HTMLDialogElement>('account-center-dialog');
 if(!dialog.open)dialog.showModal();
 element('account-center-status').textContent='正在读取账号配置…';
 try{const catalog=await api<EngineCatalog>('engines','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;engineCatalog=catalog;renderAccountCenter();populateEngineProfileForm();populateEngineOnboarding();void resumeEngineSetup();element('account-center-status').textContent=''}catch(e){if(shellCurrent(epoch))element('account-center-status').textContent=(e as Error).message}
}
function renderAccountCenter(){
 const list=element('account-center-list');
 if(!engineCatalog)return;
 const profiles=engineCatalog.profiles||[];
 if(!profiles.length){list.innerHTML='<p class="muted">还没有账号。可添加 Codex 登录、Codex/Claude API，或引用目标环境已有的账号目录。</p>';return}
 list.innerHTML=profiles.map(p=>{const engine=engineCatalog!.engines.find(e=>e.id===p.engine);return `<article class="engine-profile"><strong>${escapeHTML(engine?.name||p.engine)}</strong><span>${escapeHTML(p.name)} / ${escapeHTML(engineTargetName(p.environment_id))}</span><code>${escapeHTML(engineCredentialLabel(p.kind))}</code><div class="actions"><button type="button" data-account-activate="${escapeHTML(p.id)}">设为新任务默认</button>${['codex_home','claude_home'].includes(p.kind)?`<button type="button" data-account-sync="${escapeHTML(p.id)}">同步到环境</button>`:''}</div></article>`}).join('');
 list.querySelectorAll<HTMLButtonElement>('[data-account-activate]').forEach(b=>b.onclick=async()=>{await activateEngineProfile(b.dataset.accountActivate!);renderAccountCenter()});
 list.querySelectorAll<HTMLButtonElement>('[data-account-sync]').forEach(b=>b.onclick=()=>openCodexSync(b.dataset.accountSync!));
}
function engineTargetName(id:string){return settings.config.environments.find(e=>e.id===id)?.name||id}
let codexSyncProfileID='',accountSyncBusy=false;
function openCodexSync(profileID:string){
 const profile=engineCatalog?.profiles.find(p=>p.id===profileID);if(!profile||accountSyncBusy)return;codexSyncProfileID=profileID;
 element('codex-sync-targets').innerHTML=settings.config.environments.map(env=>`<label class="check-row"><input type="checkbox" data-codex-sync-target="${escapeHTML(env.id)}"> ${escapeHTML(env.name)}（${escapeHTML(env.type)}）</label>`).join('');
 element('codex-sync-source').textContent='来源账号：'+profile.name+'（'+profile.engine+' / '+engineTargetName(profile.environment_id)+'）';element('codex-sync-status').textContent='';button('codex-sync-submit').disabled=false;element<HTMLDialogElement>('codex-sync-dialog').showModal();
}
async function submitCodexSync(){
 if(accountSyncBusy)return;const epoch=shellEpoch;
 const ids=Array.from(document.querySelectorAll<HTMLInputElement>('[data-codex-sync-target]:checked')).map(x=>x.dataset.codexSyncTarget!).filter(Boolean);if(!codexSyncProfileID||!ids.length){element('codex-sync-status').textContent='请选择至少一个目标环境。';return}
 accountSyncBusy=true;button('codex-sync-submit').disabled=true;element<HTMLFieldSetElement>('codex-sync-targets').disabled=true;element('codex-sync-status').textContent='正在写入目标环境…';
 try{const result=await api<CodexSyncResponse>('account-sync','POST',{source_profile_id:codexSyncProfileID,environment_ids:ids});if(!shellCurrent(epoch))return;element('codex-sync-status').textContent=result.results.map(r=>`${engineTargetName(r.environment_id)}：${r.state==='done'?'已完成':'失败'}，${r.message}`).join('\n');button('codex-sync-submit').disabled=result.results.every(r=>r.state==='done')}catch(e){if(shellCurrent(epoch)){element('codex-sync-status').textContent=(e as Error).message;button('codex-sync-submit').disabled=false}}finally{if(shellCurrent(epoch)){accountSyncBusy=false;element<HTMLFieldSetElement>('codex-sync-targets').disabled=false}}
}
function engineCredentialLabel(kind:string){return ({native:'继承目标环境默认配置',dsh_home:'Harness 配置目录（DSH_HOME）',codex_home:'Codex 配置目录（CODEX_HOME）',claude_home:'Claude 配置目录（CLAUDE_CONFIG_DIR）',env_file:'环境文件引用（仅记录，尚未应用）'} as Record<string,string>)[kind]||kind}
function engineProfileActivationMessage(profile?:EngineProfile){
 if(profile?.kind==='env_file')return '引用已选中，但环境文件尚未应用到运行进程；请在目标环境中配置。';
 if(profile?.engine==='deepseek-harness')return profile.kind==='native'?'新建 Harness 任务将继承目标环境的默认配置；当前运行会话保持不变。':'新建 Harness 任务将使用该 DSH_HOME 配置目录；当前运行会话保持不变。';
 return '已设为新任务的默认账号/API；现有任务请通过“切换 AI”更换配置。';
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
  const active=Object.entries(engineCatalog!.active_profile).filter(([key])=>key.endsWith(':'+e.id)).map(([,value])=>value);
  return `<article class="engine-card"><header><div><strong>${escapeHTML(e.name)}</strong><small>${escapeHTML(e.transport)} · ${e.runnable?'可执行':'待接入适配器'}</small></div><span>${active.length?'已配置':'未配置'}</span></header><p>${escapeHTML(e.description)}</p><p class="engine-detected-status">默认环境：${escapeHTML(detectedEngineStatus(e.id))}</p><p class="muted">${escapeHTML(e.install_description||'')}</p><div class="engine-actions"><button type="button" data-engine-plan="${escapeHTML(e.id)}">查看安装/配置指南</button>${e.documentation_url?`<a href="${escapeHTML(e.documentation_url)}" target="_blank" rel="noopener noreferrer">官方文档 ↗</a>`:''}</div><pre class="engine-plan hidden" data-engine-plan-result="${escapeHTML(e.id)}"></pre></article>`;
 }).join('');
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-plan]').forEach(b=>b.onclick=()=>void showEnginePlan(b.dataset.enginePlan!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-activate]').forEach(b=>b.onclick=()=>void activateEngineProfile(b.dataset.engineActivate!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-sync]').forEach(b=>b.onclick=()=>openCodexSync(b.dataset.engineSync!));
}
async function loadEngineSettings(){const epoch=shellEpoch,request=++engineSettingsRequest;try{const catalog=await api<EngineCatalog>('engines','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==engineSettingsRequest)return;engineCatalog=catalog;renderEngineCatalog();renderAccountCenter();populateEngineProfileForm();populateEngineOnboarding();void resumeEngineSetup()}catch(e){if(shellCurrent(epoch)&&request===engineSettingsRequest)element('engine-catalog').textContent=(e as Error).message}}
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
 try{await api('engine-profiles','PUT',profile);if(!shellCurrent(epoch))return;invalidateModelCatalogs();element('engine-profile-result').textContent=profile.kind==='env_file'?'已保存引用；环境文件目前仅记录，不会应用到运行进程。':profile.engine==='deepseek-harness'?'已保存；设为新任务默认后，新建 Harness 任务使用该配置。':'已保存；可设为新任务默认；现有任务请通过“切换 AI”选择此配置。';await loadEngineSettings()}catch(e){if(shellCurrent(epoch))element('engine-profile-result').textContent=(e as Error).message}
}
async function activateEngineProfile(id:string){const epoch=shellEpoch;try{await api(`engine-profiles/${encodeURIComponent(id)}/activate`,'POST',{});if(!shellCurrent(epoch))return;invalidateModelCatalogs();notify(engineProfileActivationMessage(engineCatalog?.profiles.find(p=>p.id===id)));await loadEngineSettings()}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}}

type EngineSetupJob={id:string;action:string;engine:string;environment_id:string;state:string;message:string;url?:string;code?:string;profile_id?:string};
let engineSetupBusy=false,engineSetupJob:EngineSetupJob|null=null,engineSetupPolling=false;
let engineSetupAttempt:{signature:string;id:string}|null=null;
function installEngineOnboarding(){
 engineSetupBusy=false;engineSetupJob=null;engineSetupPolling=false;engineSetupAttempt=null;
 element('engine-catalog').insertAdjacentHTML('beforebegin',`<section class="engine-onboarding"><h4>检测与安装 CLI</h4><p>选择已保存的 Windows、WSL 或 SSH 环境安装 CLI。Windows 使用独立工具目录；WSL/SSH 使用目标环境的 npm 安装到用户目录，需已有 Node.js/npm。模型在云端使用。</p><fieldset id="engine-setup-fields"><label for="engine-setup-environment">执行环境</label><select id="engine-setup-environment"></select><div class="engine-actions"><select id="engine-setup-engine" aria-label="安装工具"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><button type="button" id="engine-status-refresh">检测所有环境</button><button type="button" id="engine-setup-install">一键安装工具</button></div><details><summary>登录或配置 API</summary><label for="engine-account-name">账号名称</label><input id="engine-account-name" maxlength="60" placeholder="例如 工作账号"><label for="engine-account-login">连接方式</label><select id="engine-account-login"><option value="chatgptDeviceCode">登录 ChatGPT 账号</option><option value="apiKey">API key</option></select><div id="engine-account-api" class="hidden"><label for="engine-account-key">API key</label><input id="engine-account-key" type="password" autocomplete="off"><label for="engine-account-url">API 地址（可选）</label><input id="engine-account-url" placeholder="留空使用 OpenAI 官方 API"><p class="muted">Codex 需要 Responses 协议；Claude Code 需要 Anthropic Messages 协议。密钥保存到独立的原生账号目录。</p></div><label for="engine-account-model">默认云端模型（可选）</label><input id="engine-account-model" maxlength="120" placeholder="留空使用工具默认模型"><button type="button" id="engine-account-add" class="primary">添加账号</button><p class="muted">每个账号单独保存。添加后可设为新任务默认，或通过任务中的“切换 AI”选择。</p></details></fieldset><div id="engine-setup-status" role="status"></div><pre id="engine-status-result" class="engine-plan hidden"></pre><button type="button" id="engine-setup-cancel" class="hidden">取消当前操作</button></section>`);
 button('engine-status-refresh').onclick=()=>void refreshEngineStatus();
 button('engine-setup-install').onclick=()=>void startEngineOnboarding('install');
 button('engine-account-add').onclick=()=>void startEngineOnboarding('account');
 element<HTMLSelectElement>('engine-account-login').onchange=()=>element('engine-account-api').classList.toggle('hidden',element<HTMLSelectElement>('engine-account-login').value!=='apiKey');
 button('engine-setup-cancel').onclick=async()=>{const job=engineSetupJob,epoch=shellEpoch;if(!job)return;try{await api('engine-setup/'+encodeURIComponent(job.id),'DELETE');if(shellCurrent(epoch)&&engineSetupJob===job)element(job.action==='account'?'engine-account-status':'engine-setup-status').textContent='正在取消…'}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}};
}
async function refreshEngineStatus(){
 const epoch=shellEpoch;const pre=element<HTMLPreElement>('engine-status-result');pre.classList.remove('hidden');pre.textContent='正在检测 Windows、WSL 和 SSH 环境…';button('engine-status-refresh').disabled=true;
 try{const result=await api<EngineStatusResponse>('engine-status','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;const show=(tool:DetectedEngineTool)=>tool.label||tool.state;pre.textContent=result.items.map(item=>{const target=[item.name,item.type,item.distro,item.user,item.host].filter(Boolean).join(' / ');return target+'\n  Codex: '+show(item.codex)+'\n  Claude: '+show(item.claude)+'\n  Harness: '+show(item.harness)+'\n  Kimi: '+show(item.kimi)+'\n  MiMo: '+show(item.mimo)+(item.message?'\n  '+item.message:'')}).join('\n')||'尚未配置执行环境。'}catch(e){if(shellCurrent(epoch))pre.textContent=(e as Error).message}finally{if(shellCurrent(epoch))button('engine-status-refresh').disabled=false}
}
function populateEngineOnboarding(){
 const select=element<HTMLSelectElement>('engine-setup-environment');if(!select)return;
 const previous=select.value,envs=settings.config.environments;
 const accountSelect=element<HTMLSelectElement>('engine-account-environment'),accountPrevious=accountSelect.value,windows=envs.filter(e=>e.type==='windows');accountSelect.innerHTML=windows.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');if(windows.some(e=>e.id===accountPrevious))accountSelect.value=accountPrevious;element<HTMLFieldSetElement>('engine-account-fields').disabled=engineSetupBusy||!windows.length;
 select.innerHTML=envs.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 if(envs.some(e=>e.id===previous))select.value=previous;else if(envs.some(e=>e.id===settings.config.default_environment))select.value=settings.config.default_environment;
 element<HTMLFieldSetElement>('engine-setup-fields').disabled=engineSetupBusy||!envs.length;
 if(!envs.length)element('engine-setup-status').textContent='请先添加 Windows、WSL 或 SSH 执行环境。';
}
function renderEngineSetup(job:EngineSetupJob){
 engineSetupJob=job;engineSetupBusy=job.state==='running'||job.state==='waiting';
 element<HTMLFieldSetElement>('engine-setup-fields').disabled=engineSetupBusy;
 element<HTMLFieldSetElement>('engine-account-fields').disabled=engineSetupBusy||!element<HTMLSelectElement>('engine-account-environment').value;
 button('engine-setup-cancel').classList.toggle('hidden',!engineSetupBusy||job.action!=='install');button('engine-account-cancel').classList.toggle('hidden',!engineSetupBusy||job.action!=='account');
 const status=element(job.action==='account'?'engine-account-status':'engine-setup-status');status.replaceChildren();const message=document.createElement('p');message.textContent=job.message;status.append(message);
 if(job.url){const link=document.createElement('a');link.href=job.url;link.target='_blank';link.rel='noopener noreferrer';link.textContent='打开登录页面 ↗';status.append(link)}
 if(job.code){const code=document.createElement('pre');code.textContent=job.code;status.append(code)}
}
async function startEngineOnboarding(action:'install'|'account'){
 if(engineSetupBusy)return;
 const login=element<HTMLSelectElement>('engine-account-login').value;
 const body={action,engine:action==='account'?element<HTMLSelectElement>('engine-account-engine').value:element<HTMLSelectElement>('engine-setup-engine').value,environment_id:element<HTMLSelectElement>(action==='account'?'engine-account-environment':'engine-setup-environment').value,...(action==='account'?{name:input('engine-account-name').value.trim(),login,api_key:login==='apiKey'?input('engine-account-key').value:'',base_url:login==='apiKey'?input('engine-account-url').value.trim():'',model:input('engine-account-model').value.trim()}:{})};
 const signature=JSON.stringify(body);if(!engineSetupAttempt||engineSetupAttempt.signature!==signature)engineSetupAttempt={signature,id:Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('')};
 const epoch=shellEpoch,status=element(action==='account'?'engine-account-status':'engine-setup-status');engineSetupBusy=true;element<HTMLFieldSetElement>('engine-setup-fields').disabled=true;element<HTMLFieldSetElement>('engine-account-fields').disabled=true;status.textContent='正在开始…';
 try{const job=await api<EngineSetupJob>('engine-setup','POST',{...body,id:engineSetupAttempt.id});if(!shellCurrent(epoch))return;input('engine-account-key').value='';engineSetupAttempt=null;renderEngineSetup(job);void pollEngineSetup(job.id,epoch)}catch(e){if(shellCurrent(epoch)){engineSetupBusy=false;populateEngineOnboarding();status.textContent=(e as Error).message}}
}
async function resumeEngineSetup(){
 if(engineSetupBusy||engineSetupPolling)return;const epoch=shellEpoch;
 try{const jobs=await api<EngineSetupJob[]>('engine-setup');if(!shellCurrent(epoch)||engineSetupBusy||!jobs.length)return;renderEngineSetup(jobs[0]);void pollEngineSetup(jobs[0].id,epoch)}catch{}
}
async function pollEngineSetup(id:string,epoch:number){
 if(engineSetupPolling)return;engineSetupPolling=true;
 try{
  while(shellCurrent(epoch)&&engineSetupJob?.id===id){
   const job=await api<EngineSetupJob>('engine-setup/'+encodeURIComponent(id),'GET',undefined,shellController.signal);if(!shellCurrent(epoch)||engineSetupJob?.id!==id)return;renderEngineSetup(job);
   if(job.state!=='running'&&job.state!=='waiting'){
    if(job.state==='done'){
     invalidateModelCatalogs();
     if(job.action==='install'){
      const fresh=await api<Settings>('settings','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;
      const key=job.engine==='codex'?'codex':job.engine==='claude'?'claude':'harness';
      const before=settings.config.environments.find(e=>e.id===job.environment_id),updated=fresh.config.environments.find(e=>e.id===job.environment_id),editing=editingEnvironments.find(e=>e.id===job.environment_id);
      if(updated&&before){if(editing&&editing[key]===before[key])editing[key]=updated[key]||'';before[key]=updated[key]||'';if(editingID===job.environment_id)loadEnvironmentEditor()}
     }
     await loadEngineSettings();
    }
    return;
   }
   await new Promise(resolve=>setTimeout(resolve,1500));
  }
 }catch(e){if(shellCurrent(epoch)&&engineSetupJob?.id===id){engineSetupBusy=false;populateEngineOnboarding();element(engineSetupJob.action==='account'?'engine-account-status':'engine-setup-status').textContent='读取状态失败；重新打开账号管理或引擎安装可继续查看。'+(e as Error).message}}
 finally{if(shellCurrent(epoch))engineSetupPolling=false}
}

function refreshAccountLogin(){
 const claude=element<HTMLSelectElement>('engine-account-engine').value==='claude',select=element<HTMLSelectElement>('engine-account-login');
 select.innerHTML=claude?'<option value="apiKey">API key / 中转站</option>':'<option value="chatgptDeviceCode">登录 ChatGPT 账号</option><option value="apiKey">API key / 中转站</option>';
 element('engine-account-api').classList.toggle('hidden',select.value!=='apiKey');input('engine-account-url').placeholder=claude?'留空使用 Anthropic 官方 API':'留空使用 OpenAI 官方 API';
}
