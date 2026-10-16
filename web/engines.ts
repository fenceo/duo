type EngineDefinition={id:string;name:string;description:string;transport:string;runnable:boolean;auto_install?:boolean;targets:string[];capabilities:string[];credential_kinds:string[];install_description?:string;documentation_url?:string};
type EngineProfile={id:string;name:string;engine:string;environment_id:string;kind:string;reference:string;created:number;updated:number};
type EngineCatalog={engines:EngineDefinition[];profiles:EngineProfile[];active_profile:Record<string,string>;accounts?:Record<string,CodexAccountInfo>;organization?:Record<string,AccountOrganization>};
type CodexSyncResult={environment_id:string;state:string;message:string};
type CodexSyncResponse={source_profile_id:string;results:CodexSyncResult[]};
type DetectedEngineTool={path:string;state:string;label:string;version?:string};
type EngineEnvironmentStatus={environment_id:string;name:string;type:string;distro?:string;user?:string;host?:string;codex:DetectedEngineTool;claude:DetectedEngineTool;harness:DetectedEngineTool;kimi:DetectedEngineTool;mimo:DetectedEngineTool;opencode:DetectedEngineTool;zcode:DetectedEngineTool;message?:string};
type EngineStatusResponse={items:EngineEnvironmentStatus[];discovered?:DetectedEnvironment[];message?:string};
let engineCatalog:EngineCatalog|null=null;
let engineSettingsRequest=0;
function installEngineSettings(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 const envTab=nav.querySelector('[data-settings="environment"]');
 if(envTab)envTab.textContent='环境与引擎';
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-engines" class="settings-section hidden"><h3>环境与引擎</h3><p>统一发现本机 Windows / WSL，并检查已保存的 Windows / WSL / SSH 引擎。账号与 API 在独立的“账号管理”中配置。</p><div id="engine-catalog" class="engine-catalog"><p class="muted">正在读取引擎目录…</p></div><details class="engine-profile-editor"><summary>添加或更新账号/API 引用</summary><label for="engine-profile-id">配置 ID</label><input id="engine-profile-id" placeholder="例如 codex-main"><label for="engine-profile-name">显示名称</label><input id="engine-profile-name" placeholder="工作账号"><label for="engine-profile-engine">引擎</label><select id="engine-profile-engine"></select><label for="engine-profile-environment">执行环境</label><select id="engine-profile-environment"></select><label for="engine-profile-kind">类型</label><select id="engine-profile-kind"></select><label for="engine-profile-reference">外部引用</label><input id="engine-profile-reference" placeholder="目录路径或 native profile 名称"><p class="muted">这里不填写 API key；只填写目标环境可访问的配置目录、profile 名称或后续适配器约定的引用。</p><button type="button" id="engine-profile-save" class="primary">保存引用</button><p id="engine-profile-result" role="status"></p></details></section>`);
 button('engine-profile-save').onclick=()=>void saveEngineProfile();
 input('engine-profile-reference').nextElementSibling!.textContent='这里不填写 API key。native 继承目标环境默认配置，不指定命名 profile；配置目录引用必须是目标环境可访问的路径。';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="codex-sync-dialog"><form id="codex-sync-form"><h2>切换账号到环境</h2><p id="codex-sync-source" class="muted"></p><p>将所选账号的登录信息、API 地址及默认模型写入勾选环境的原生配置。每个目标先备份原文件，保留工具权限和 MCP 设置。只修改目标环境的原生登录，不修改对话或任务。已运行的 CLI 是否立即读取由工具决定，必要时重新打开。</p><fieldset id="codex-sync-targets"></fieldset><p id="codex-sync-status" role="status"></p><div class="dialog-footer"><button type="button" id="codex-sync-cancel">取消</button><button type="submit" class="primary" id="codex-sync-submit">确认切换</button></div></form></dialog>`);
 installAccountCenter();
 button('codex-sync-cancel').onclick=()=>element<HTMLDialogElement>('codex-sync-dialog').close();
 element('codex-sync-form').addEventListener('submit',e=>{e.preventDefault();void submitCodexSync()});
 installEngineOnboarding();
 const editor=element('account-center-editor');editor.append(element('settings-engines').querySelector('.engine-profile-editor')!);
 const accountFields=document.createElement('fieldset');accountFields.id='engine-account-fields';accountFields.innerHTML='<legend>添加账号 / API</legend><label for="engine-account-environment">保存账号的环境</label><select id="engine-account-environment"></select><label for="engine-account-engine">引擎</label><select id="engine-account-engine"><option value="codex">Codex</option></select>';accountFields.append(element('engine-setup-fields').querySelector('details')!);editor.prepend(accountFields);
 element('engine-account-engine').onchange=()=>{input('engine-account-key').value='';refreshAccountLogin()};refreshAccountLogin();
 button('engine-account-cancel').onclick=button('engine-setup-cancel').onclick;
 // Sidebar entry is owned by layout.ts (single 环境与引擎 button).
 installAccountImport();engineCatalog=null;accountSyncBusy=false;engineEnvironmentStatuses=[];engineDiscoveredEnvironments=[];engineStatusSignature='';engineStatusRequest=0;engineStatusBusy=false;
 installHarnessAPI();installEnvironmentAPI();
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
 try{const result=await api<CodexSyncResponse>('account-sync','POST',{source_profile_id:codexSyncProfileID,environment_ids:ids});if(!shellCurrent(epoch))return;element('codex-sync-status').textContent=result.results.map(r=>`${engineTargetName(r.environment_id)}：${r.state==='done'?'已完成':'失败'}，${r.message}`).join('\n');button('codex-sync-submit').disabled=result.results.every(r=>r.state==='done');invalidateModelCatalogs();await loadEngineSettings()}catch(e){if(shellCurrent(epoch)){element('codex-sync-status').textContent=(e as Error).message;button('codex-sync-submit').disabled=false}}finally{if(shellCurrent(epoch)){accountSyncBusy=false;element<HTMLFieldSetElement>('codex-sync-targets').disabled=false}}
}
function engineCredentialLabel(kind:string){return ({native:'继承目标环境默认配置',mimo_config:'MiMo API 配置目录（保留原生会话目录）',dsh_home:'Harness 配置目录（DSH_HOME）',codex_home:'Codex 配置目录（CODEX_HOME）',claude_home:'Claude 配置目录（CLAUDE_CONFIG_DIR）',env_file:'环境文件引用（仅记录，尚未应用）'} as Record<string,string>)[kind]||kind}
function engineProfileActivationMessage(profile?:EngineProfile){
 if(profile?.kind==='env_file')return '引用已选中，但环境文件尚未应用到运行进程；请在目标环境中配置。';
 return '已更新此环境的引擎配置；任务与会话保持不变。';
}
let engineDiscoveredEnvironments:DetectedEnvironment[]=[];
let engineEnvironmentStatuses:EngineEnvironmentStatus[]=[],engineStatusSignature='',engineStatusRequest=0,engineStatusBusy=false;
function engineEnvironmentSignature(){return JSON.stringify(settings.config.environments)}
function installUnifiedEngineCenter(){
 const section=element('settings-environment'),editor=document.createElement('details');editor.id='engine-environment-editor';editor.className='environment-advanced';editor.innerHTML='<summary>管理环境、连接与工作目录</summary>';
 while(section.firstChild)editor.append(section.firstChild);
 const engines=element('settings-engines');engines.classList.remove('settings-section','hidden');section.append(engines,editor);
 element('engine-setup-fields').append(element('engine-catalog'));
}
function renderEngineCatalog(){
 if(!engineCatalog)return;
 const current=engineStatusSignature===engineEnvironmentSignature();
 element('engine-catalog').innerHTML=settings.config.environments.map(env=>{
  const status=current?engineEnvironmentStatuses.find(s=>s.environment_id===env.id):undefined;
  const rows=engineCatalog!.engines.filter(e=>e.targets.includes(env.type)).map(engine=>{
   const toolKey=({codex:'codex',claude:'claude','deepseek-harness':'harness',kimi:'kimi',mimo:'mimo',opencode:'opencode',zcode:'zcode'} as Record<string,'codex'|'claude'|'harness'|'kimi'|'mimo'|'opencode'|'zcode'>)[engine.id];
   const tool=status?.[toolKey];
   const state=tool?.state||'unknown',installed=!!tool?.path&&state!=='missing',missing=state==='missing';
   const label=installed?'已安装'+(tool?.version?' · v'+tool.version:''):missing?'未安装':engineStatusBusy?'检测中…':'未完成检测';
   return '<div class="engine-environment-row"><div><strong>'+escapeHTML(engine.name)+'</strong><span class="detected-tool detected-tool-'+escapeHTML(state)+'">'+escapeHTML(label)+'</span><small>'+escapeHTML((installed?tool!.label:engine.runnable?'可用于 Duo 任务':'')+(!engine.runnable?' · Duo 适配器尚未接入':''))+'</small>'+(tool?.path?'<code>'+escapeHTML(tool.path)+'</code>':'')+'</div><div class="engine-actions">'+(engine.id==='deepseek-harness'?'<button type="button" data-harness-api="'+escapeHTML(env.id)+'" '+(engineSetupBusy||harnessAPIBusy?'disabled':'')+'>配置 API</button>':'')+(['claude','mimo','opencode'].includes(engine.id)?'<button type="button" data-engine-api="'+escapeHTML(engine.id)+'" data-engine-api-environment="'+escapeHTML(env.id)+'" '+(engineSetupBusy||environmentAPIBusy?'disabled':'')+'>配置 API</button>':'')+(missing&&engine.auto_install?'<button type="button" data-install-engine="'+escapeHTML(engine.id)+'" data-install-environment="'+escapeHTML(env.id)+'">一键安装</button>':'')+(missing&&!engine.auto_install&&engine.documentation_url?'<a class="engine-actions-link" href="'+escapeHTML(engine.documentation_url)+'" target="_blank" rel="noopener noreferrer">安装指引 ↗</a>':'')+(installed&&engine.auto_install?'<button type="button" data-update-engine="'+escapeHTML(engine.id)+'" data-update-environment="'+escapeHTML(env.id)+'" title="安装官方最新 CLI，验证后切换；保留原安装。该环境的引擎须无执行或排队任务。">更新 CLI</button>':'')+(engine.documentation_url?'<a href="'+escapeHTML(engine.documentation_url)+'" target="_blank" rel="noopener noreferrer">文档 ↗</a>':'')+'</div></div>';
  }).join('');
  return '<article class="engine-environment"><header><div><h4>'+escapeHTML(env.name)+'</h4><small>'+escapeHTML([env.type,env.distro,env.user,env.host].filter(Boolean).join(' / '))+'</small></div><button type="button" data-edit-engine-environment="'+escapeHTML(env.id)+'">环境配置</button></header>'+rows+(status?.message?'<p class="muted">'+escapeHTML(status.message)+'</p>':'')+'</article>';
 }).join('')||'<p>点击检测发现本机环境，或在下方手动添加环境。</p>';
 if(current)element('engine-catalog').insertAdjacentHTML('beforeend',engineDiscoveredEnvironments.map((item,index)=>{
  const env=item.environment,draft=editingEnvironments.find(e=>sameDetectedEnvironment(e,env));
  return '<article class="engine-environment engine-discovered"><header><div><h4>'+escapeHTML(env.name)+'</h4><small>'+escapeHTML([env.type,env.distro,env.user].filter(Boolean).join(' / '))+' · '+(draft?'待保存':'新发现')+'</small></div><button type="button" data-add-engine-environment="'+index+'" '+(engineStatusBusy?'disabled':'')+'>'+(draft?'编辑待保存环境':'添加环境')+'</button></header><div class="detected-tools">'+[['Codex',item.codex],['Claude Code',item.claude],['Harness',item.harness],['Kimi',item.kimi],['MiMo',item.mimo],['OpenCode',item.opencode],['ZCode',item.zcode]].map(([name,tool])=>'<span class="detected-tool">'+escapeHTML(String(name))+'：'+escapeHTML((tool as DetectedTool|undefined)?.label||'未完成检测')+'</span>').join('')+'</div>'+(item.message?'<p class="muted">'+escapeHTML(item.message)+'</p>':'')+'<p class="muted">添加并保存环境后，可在此安装未安装的 CLI。</p></article>';
 }).join(''));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-add-engine-environment]').forEach(b=>b.onclick=()=>addScannedEnvironment(Number(b.dataset.addEngineEnvironment)));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-install-engine]').forEach(b=>b.onclick=()=>void startEngineOnboarding('install',{engine:b.dataset.installEngine!,environment_id:b.dataset.installEnvironment!}));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-engine-api]').forEach(b=>b.onclick=()=>void openEnvironmentAPI(b.dataset.engineApi!,b.dataset.engineApiEnvironment!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-harness-api]').forEach(b=>b.onclick=()=>void openHarnessAPI(b.dataset.harnessApi!));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-update-engine]').forEach(b=>b.onclick=()=>void startEngineOnboarding('update',{engine:b.dataset.updateEngine!,environment_id:b.dataset.updateEnvironment!}));
 element('engine-catalog').querySelectorAll<HTMLButtonElement>('[data-edit-engine-environment]').forEach(b=>b.onclick=()=>{storeEnvironmentEditor();editingID=b.dataset.editEngineEnvironment!;environmentPickers();loadEnvironmentEditor();const editor=element<HTMLDetailsElement>('engine-environment-editor');editor.open=true;editor.scrollIntoView({block:'start'})});
}
async function openEngineCenter(){showEngineCenter()}
async function loadEngineSettings(){const epoch=shellEpoch,request=++engineSettingsRequest;try{const catalog=await api<EngineCatalog>('engines','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==engineSettingsRequest)return;engineCatalog=catalog;renderEngineCatalog();renderAccountCenter();populateEngineProfileForm();populateEngineOnboarding();void resumeEngineSetup();if(catalog.accounts&&element<HTMLDialogElement>('account-center-dialog').open)void refreshCodexAccounts(false,true)}catch(e){if(shellCurrent(epoch)&&request===engineSettingsRequest)element('engine-catalog').textContent=(e as Error).message}}
function populateEngineProfileForm(){
 if(!engineCatalog)return;
 const engines=element<HTMLSelectElement>('engine-profile-engine'),envs=element<HTMLSelectElement>('engine-profile-environment');
 engines.innerHTML=engineCatalog.engines.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 envs.innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');
 const kinds=element<HTMLSelectElement>('engine-profile-kind'),reference=input('engine-profile-reference');
 const refreshReference=()=>{const native=kinds.value==='native';reference.disabled=native;reference.placeholder=kinds.value==='dsh_home'?'目标环境中的 Harness 配置目录绝对路径':native?'自动继承目标环境默认配置':'目标环境可访问的配置目录或外部引用';if(native)reference.value='default';else if(reference.value==='default')reference.value='';reference.title=native?'不是命名 profile；使用目标环境默认配置。':'不填写 API key。'};
 const refresh=()=>{const e=engineCatalog!.engines.find(x=>x.id===engines.value);kinds.innerHTML=(e?.credential_kinds||[]).filter(k=>e?.id!=='deepseek-harness'||k!=='env_file').map(k=>`<option value="${escapeHTML(k)}">${escapeHTML(engineCredentialLabel(k))}</option>`).join('');refreshReference()};engines.onchange=refresh;kinds.onchange=refreshReference;refresh();
}
async function saveEngineProfile(){
 const profile:EngineProfile={id:input('engine-profile-id').value.trim(),name:input('engine-profile-name').value.trim(),engine:element<HTMLSelectElement>('engine-profile-engine').value,environment_id:element<HTMLSelectElement>('engine-profile-environment').value,kind:element<HTMLSelectElement>('engine-profile-kind').value,reference:input('engine-profile-reference').value.trim(),created:0,updated:0};
 const epoch=shellEpoch;
 try{await api('engine-profiles','PUT',profile);if(!shellCurrent(epoch))return;invalidateModelCatalogs();element('engine-profile-result').textContent=profile.kind==='env_file'?'已保存引用；环境文件目前仅记录，不会应用到运行进程。':'已保存；可在账号卡片选择“切换到环境”。';await loadEngineSettings()}catch(e){if(shellCurrent(epoch))element('engine-profile-result').textContent=(e as Error).message}
}
async function activateEngineProfile(id:string){
 if(accountCenterBusy)return;const epoch=shellEpoch;setAccountMutationBusy(true);
 try{await api(`engine-profiles/${encodeURIComponent(id)}/activate`,'POST',{});if(!shellCurrent(epoch))return;invalidateModelCatalogs();notify(engineProfileActivationMessage(engineCatalog?.profiles.find(p=>p.id===id)));await loadEngineSettings()}
 catch(e){if(shellCurrent(epoch))notify((e as Error).message)}
 finally{if(shellCurrent(epoch))setAccountMutationBusy(false)}
}

type EngineSetupJob={id:string;action:string;engine:string;environment_id:string;state:string;message:string;version?:string;url?:string;code?:string;profile_id?:string};
let engineSetupBusy=false,engineSetupJob:EngineSetupJob|null=null,engineSetupPolling=false;
let engineSetupAttempt:{signature:string;id:string}|null=null;
function installEngineOnboarding(){
 engineSetupBusy=false;engineSetupJob=null;engineSetupPolling=false;engineSetupAttempt=null;
 element('engine-catalog').insertAdjacentHTML('beforebegin',`<section class="engine-onboarding"><p>一次检测，统一显示环境与 CLI 状态。未保存的新环境可直接添加；SSH 只检查已配置的连接，首次检测可能启动 WSL。</p><fieldset id="engine-setup-fields"><button type="button" id="engine-status-refresh">检测环境与引擎</button><details><summary>登录或配置 API</summary><label for="engine-account-name">账号名称</label><input id="engine-account-name" maxlength="60" placeholder="例如 工作账号"><label for="engine-account-login">连接方式</label><select id="engine-account-login"><option value="chatgptDeviceCode">登录 ChatGPT 账号</option><option value="apiKey">API key</option></select><div id="engine-account-api" class="hidden"><label for="engine-account-key">API key</label><input id="engine-account-key" type="password" autocomplete="off"><label for="engine-account-url">API 地址（可选）</label><input id="engine-account-url" placeholder="留空使用 OpenAI 官方 API"><p class="muted">Codex 需要 Responses 协议；Claude Code 需要 Anthropic Messages 协议。密钥保存到独立的原生账号目录。</p></div><label for="engine-account-model">默认云端模型（可选）</label><input id="engine-account-model" maxlength="120" placeholder="留空使用工具默认模型"><button type="button" id="engine-account-add" class="primary">添加账号</button><p class="muted">每个账号单独保存。添加后可选择目标环境，切换该环境的原生登录。</p></details></fieldset><div id="engine-setup-status" role="status"></div><pre id="engine-status-result" class="engine-plan hidden"></pre><button type="button" id="engine-setup-cancel" class="hidden">取消当前操作</button></section>`);
 button('engine-status-refresh').onclick=()=>void refreshEngineStatus();
 button('engine-account-add').onclick=()=>void startEngineOnboarding('account');
 element<HTMLSelectElement>('engine-account-login').onchange=()=>element('engine-account-api').classList.toggle('hidden',element<HTMLSelectElement>('engine-account-login').value!=='apiKey');
 button('engine-setup-cancel').onclick=async()=>{const job=engineSetupJob,epoch=shellEpoch;if(!job)return;try{await api('engine-setup/'+encodeURIComponent(job.id),'DELETE');if(shellCurrent(epoch)&&engineSetupJob===job)element(job.action==='account'?'engine-account-status':'engine-setup-status').textContent='正在取消…'}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}};
}
async function refreshEngineStatus(){
 const signature=engineEnvironmentSignature();if(engineStatusBusy&&engineStatusSignature===signature)return;
 const epoch=shellEpoch,request=++engineStatusRequest;engineStatusBusy=true;engineStatusSignature=signature;engineEnvironmentStatuses=[];engineDiscoveredEnvironments=[];
 const pre=element('engine-status-result');pre.classList.remove('hidden');pre.textContent='正在发现 Windows / WSL 并检测已保存的环境与引擎…';button('engine-status-refresh').disabled=true;renderEngineCatalog();
 try{const result=await api<EngineStatusResponse>('environments/scan','POST',{},shellController.signal);if(!shellCurrent(epoch)||request!==engineStatusRequest)return;if(signature!==engineEnvironmentSignature()){pre.textContent='环境配置已更改，请重新检测。';return}engineEnvironmentStatuses=result.items;engineDiscoveredEnvironments=result.discovered||[];pre.textContent=result.message||'检测完成。';}
 catch(e){if(shellCurrent(epoch)&&request===engineStatusRequest)pre.textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===engineStatusRequest){engineStatusBusy=false;button('engine-status-refresh').disabled=false;renderEngineCatalog()}}
}
function populateEngineOnboarding(){
 const envs=settings.config.environments;
 const accountSelect=element<HTMLSelectElement>('engine-account-environment'),previous=accountSelect.value,targets=envs.filter(e=>['windows','wsl','ssh'].includes(e.type));accountSelect.innerHTML=targets.map(e=>'<option value="'+escapeHTML(e.id)+'">'+escapeHTML(e.name)+' · '+escapeHTML(e.type.toUpperCase())+'</option>').join('');if(targets.some(e=>e.id===previous))accountSelect.value=previous;
 element<HTMLFieldSetElement>('engine-account-fields').disabled=engineSetupBusy||!targets.length;
 element<HTMLFieldSetElement>('engine-setup-fields').disabled=engineSetupBusy||!envs.length;
 populateAccountImport();
 if(!envs.length)element('engine-setup-status').textContent='请先添加 Windows、WSL 或 SSH 执行环境。';
}
function renderEngineSetup(job:EngineSetupJob){
 engineSetupJob=job;engineSetupBusy=job.state==='running'||job.state==='waiting';
 element<HTMLFieldSetElement>('engine-setup-fields').disabled=engineSetupBusy;
 element<HTMLFieldSetElement>('engine-account-fields').disabled=engineSetupBusy||!element<HTMLSelectElement>('engine-account-environment').value;
 element<HTMLFieldSetElement>('account-import-fields').disabled=engineSetupBusy||accountImportBusy||!input('account-import-destination').value;
 button('engine-setup-cancel').classList.toggle('hidden',!engineSetupBusy||!['install','update'].includes(job.action));button('engine-account-cancel').classList.toggle('hidden',!engineSetupBusy||job.action!=='account');
 const status=element(job.action==='account'?'engine-account-status':'engine-setup-status');status.replaceChildren();const message=document.createElement('p');message.textContent=job.message;status.append(message);
 if(job.url){const link=document.createElement('a');link.href=job.url;link.target='_blank';link.rel='noopener noreferrer';link.textContent='打开登录页面 ↗';status.append(link)}
 if(job.code){const code=document.createElement('pre');code.textContent=job.code;status.append(code)}
}
async function startEngineOnboarding(action:'install'|'update'|'account',target?:{engine:string;environment_id:string}){
 if(engineSetupBusy||environmentAPIBusy||accountImportBusy||accountSyncBusy||harnessAPIBusy||action!=='account'&&!target)return;
 if(action!=='account'&&(engineStatusBusy||engineStatusSignature!==engineEnvironmentSignature())){element('engine-setup-status').textContent='请等待当前环境检测完成，再安装或更新。';return}
 const login=element<HTMLSelectElement>('engine-account-login').value;
 const body={action,engine:action==='account'?element<HTMLSelectElement>('engine-account-engine').value:target!.engine,environment_id:action==='account'?element<HTMLSelectElement>('engine-account-environment').value:target!.environment_id,...(action==='account'?{name:input('engine-account-name').value.trim(),login,api_key:login==='apiKey'?input('engine-account-key').value:'',base_url:login==='apiKey'?input('engine-account-url').value.trim():'',model:input('engine-account-model').value.trim(),...(['claude','mimo'].includes(element<HTMLSelectElement>('engine-account-engine').value)?{api_protocol:input('engine-account-protocol').value,...(element<HTMLSelectElement>('engine-account-engine').value==='claude'?{auth_mode:input('engine-account-auth').value}:{})}:{})}:{})};
 if(action==='update')Object.assign(body,{expected_path:settings.config.environments.find(e=>e.id===target!.environment_id)?.[target!.engine==='codex'?'codex':target!.engine==='claude'?'claude':target!.engine==='mimo'?'mimo':target!.engine==='opencode'?'opencode':target!.engine==='zcode'?'zcode':target!.engine==='kimi'?'kimi':'harness']||''});
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
     if(job.action==='install'||job.action==='update'){
      // Keep setup and settings writes locked until the new path and its
      // predecessor chain have reached the editable settings snapshot.
      engineSetupBusy=true;element<HTMLFieldSetElement>('engine-setup-fields').disabled=true;element<HTMLFieldSetElement>('engine-account-fields').disabled=true;
      const fresh=await api<Settings>('settings','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;
      storeEnvironmentEditor();
      const key=job.engine==='codex'?'codex':job.engine==='claude'?'claude':job.engine==='mimo'?'mimo':job.engine==='opencode'?'opencode':job.engine==='zcode'?'zcode':job.engine==='kimi'?'kimi':'harness';
      const before=settings.config.environments.find(e=>e.id===job.environment_id),updated=fresh.config.environments.find(e=>e.id===job.environment_id),editing=editingEnvironments.find(e=>e.id===job.environment_id);
      if(updated&&before){if(editing){if(editing[key]===before[key])editing[key]=updated[key]||'';editing.managed_tools=updated.managed_tools}before[key]=updated[key]||'';before.managed_tools=updated.managed_tools;if(editingID===job.environment_id)loadEnvironmentEditor()}
      renderEngineSetup(job);
     }
     await loadEngineSettings();if(job.action==='install'||job.action==='update')void refreshEngineStatus();
    }
    return;
   }
   await new Promise(resolve=>setTimeout(resolve,1500));
  }
 }catch(e){if(shellCurrent(epoch)&&engineSetupJob?.id===id){engineSetupBusy=false;populateEngineOnboarding();element(engineSetupJob.action==='account'?'engine-account-status':'engine-setup-status').textContent='读取状态失败；重新打开账号管理或引擎安装可继续查看。'+(e as Error).message}}
 finally{if(shellCurrent(epoch))engineSetupPolling=false}
}

function refreshAccountLogin(){
 const claude=['claude','mimo'].includes(element<HTMLSelectElement>('engine-account-engine').value),select=element<HTMLSelectElement>('engine-account-login');
 select.innerHTML=claude?'<option value="apiKey">API key / 中转站</option>':'<option value="chatgptDeviceCode">登录 ChatGPT 账号</option><option value="apiKey">API key / 中转站</option>';
 element('engine-account-api').classList.toggle('hidden',select.value!=='apiKey');input('engine-account-url').placeholder=claude?'留空使用 Anthropic 官方 API':'留空使用 OpenAI 官方 API';

}

type HarnessAPIView={environment_id:string;target:string;api:string;base_url:string;model:string;key_configured:boolean;models?:{id:string;name?:string}[];model_inputs?:Record<string,string[]>;discovery_status?:string;message?:string};
type ClaudeRoleModels={mode:'uniform'|'custom'|'native';opus:string;sonnet:string;haiku:string;fable?:string;subagent:string};
type EnvironmentAPIView={environment_id:string;target:string;engine:string;api_protocol:string;base_url:string;model:string;auth_mode:string;key_configured:boolean;models:EngineModel[];message?:string;mimo_config?:string;claude_api_config?:string;api_scope?:'duo'|'environment';claude_roles?:ClaudeRoleModels};
let environmentAPIView:EnvironmentAPIView|null=null,environmentAPIRequest=0,environmentAPIBusy=false;
function installEnvironmentAPI(){
 element('root').insertAdjacentHTML('beforeend',`<dialog id="environment-api-dialog"><form id="environment-api-form"><h2 id="environment-api-title">API 配置</h2><p id="environment-api-target" class="muted"></p><fieldset id="environment-api-fields"><label for="environment-api-provider">服务预设</label><select id="environment-api-provider"><option value="custom">自定义 API</option><option value="anthropic">Anthropic 官方 API</option><option value="mimo">MiMo 官方 API（按量付费）</option><option value="mimo-cn">MiMo Token Plan · 中国</option><option value="mimo-sgp">MiMo Token Plan · 新加坡</option><option value="mimo-ams">MiMo Token Plan · 欧洲</option></select><label for="environment-api-protocol">接口协议</label><select id="environment-api-protocol"><option value="anthropic-messages">Anthropic Messages 兼容</option><option value="openai-chat">OpenAI Chat Completions 兼容</option></select><label for="environment-api-url">API 根地址</label><input id="environment-api-url" type="url" maxlength="2000"><div id="environment-api-auth-wrap"><label for="environment-api-auth">认证请求头</label><select id="environment-api-auth"><option value="api-key">API Key · x-api-key</option><option value="auth-token">Bearer Token · Authorization</option></select></div><label for="environment-api-model">默认模型 ID</label><input id="environment-api-model" maxlength="120" list="environment-api-models"><datalist id="environment-api-models"></datalist><label for="environment-api-key">API key</label><input id="environment-api-key" type="password" maxlength="16000" autocomplete="off"><p id="environment-api-key-note" class="muted"></p><button type="button" id="environment-api-discover">验证并读取模型</button></fieldset><p id="environment-api-hint" class="muted"></p><p id="environment-api-status" role="status"></p><div class="dialog-footer"><button type="button" id="environment-api-close">关闭</button><button type="submit" id="environment-api-save" class="primary">保存并读取模型</button></div></form></dialog>`);
 const planWrap=document.createElement('div');planWrap.id='environment-api-mimo-wrap';planWrap.className='hidden';planWrap.innerHTML='<label for="environment-api-mimo-plan">MiMo 套餐 / 区域</label><select id="environment-api-mimo-plan"><option value="mimo">按量付费</option><option value="mimo-cn">Token Plan · 中国</option><option value="mimo-sgp">Token Plan · 新加坡</option><option value="mimo-ams">Token Plan · 欧洲</option></select>';input('environment-api-provider').after(planWrap);
 const scope=document.createElement('div');scope.id='environment-api-claude-scope';scope.className='hidden';scope.innerHTML='<label for="environment-api-scope">生效范围</label><select id="environment-api-scope"><option value="duo">仅 Duo 生效</option><option value="environment">整个环境生效</option></select><p id="environment-api-scope-hint" class="muted"></p>';element('environment-api-fields').prepend(scope);
 const roles=document.createElement('details');roles.id='environment-api-claude-roles';roles.className='hidden';roles.innerHTML='<summary>模型角色配置</summary><label for="environment-api-role-mode">角色使用方式</label><select id="environment-api-role-mode"><option value="uniform">统一使用主模型</option><option value="custom">分别配置角色</option><option value="native">使用原生角色默认值</option></select><div id="environment-api-role-fields">'+[['opus','Opus'],['sonnet','Sonnet'],['haiku','Haiku'],['fable','Fable（可选）'],['subagent','子代理（可选）']].map(([id,label])=>'<label for="environment-api-role-'+id+'">'+label+' 模型 ID</label><input id="environment-api-role-'+id+'" maxlength="120" list="environment-api-models" autocomplete="off">').join('')+'<p class="muted">Opus / Sonnet / Haiku 留空跟随主模型；主模型也为空则使用原生默认。Fable 留空用原生默认，子代理留空遵循 Claude 的代理规则。每个模型都需所选服务支持。</p></div>';input('environment-api-model').after(roles);
 input('environment-api-scope').onchange=updateEnvironmentAPIScopeHint;input('environment-api-role-mode').onchange=updateEnvironmentAPIRoleFields;
 button('environment-api-close').onclick=()=>{if(!environmentAPIBusy)element<HTMLDialogElement>('environment-api-dialog').close()};
 element('environment-api-dialog').addEventListener('cancel',e=>{if(environmentAPIBusy)e.preventDefault()});
 element('environment-api-dialog').addEventListener('close',()=>{environmentAPIRequest++;environmentAPIView=null;input('environment-api-key').value='';element('environment-api-models').replaceChildren()});
 element('environment-api-form').onsubmit=e=>{e.preventDefault();void submitEnvironmentAPI(true)};
 button('environment-api-discover').onclick=()=>void submitEnvironmentAPI(false);
 input('environment-api-provider').onchange=applyEnvironmentAPIPreset;
 input('environment-api-mimo-plan').onchange=applyEnvironmentAPIPreset;
 input('environment-api-protocol').onchange=()=>{if(input('environment-api-provider').value!=='custom')applyEnvironmentAPIPreset();else {clearEnvironmentAPIModels();updateEnvironmentAPIKeyNote()}};
 for(const id of ['environment-api-url','environment-api-key','environment-api-auth'])input(id).addEventListener('input',()=>{clearEnvironmentAPIModels();updateEnvironmentAPIKeyNote()});
 disposeWithShell(()=>{environmentAPIRequest++;environmentAPIView=null;environmentAPIBusy=false;input('environment-api-key').value=''});
}
function clearEnvironmentAPIModels(){element('environment-api-models').replaceChildren();element('environment-api-status').textContent=''}
function updateEnvironmentAPIScopeHint(){element('environment-api-scope-hint').textContent=input('environment-api-scope').value==='duo'?'保存为 Duo 专用配置，仅 Duo 启动的 Claude 加载；保留原生登录、权限和历史目录。':'更新目标环境的原生 API 配置，其他 Claude 会话也会使用；原文件保留备份。'}
function updateEnvironmentAPIRoleFields(){element('environment-api-role-fields').classList.toggle('hidden',input('environment-api-role-mode').value!=='custom')}
function resetEnvironmentAPIClaudeRoles(){input('environment-api-role-mode').value='native';for(const id of ['opus','sonnet','haiku','fable','subagent'])input('environment-api-role-'+id).value='';element<HTMLDetailsElement>('environment-api-claude-roles').open=false;updateEnvironmentAPIRoleFields()}
function environmentAPIPreset(engine:string,base:string){
 const url=base.replace(/\/$/,'');
 if(engine==='claude'&&['','https://api.anthropic.com','https://api.anthropic.com/v1'].includes(url))return 'anthropic';
 if(engine==='claude'&&url==='https://api.z.ai/api/anthropic')return 'zai';
 if(engine==='mimo'&&url==='https://api.anthropic.com/v1')return 'anthropic';
 for(const preset of ['mimo','mimo-cn','mimo-sgp','mimo-ams']){const root=preset==='mimo'?'https://api.xiaomimimo.com':'https://token-plan-'+preset.slice(5)+'.xiaomimimo.com';if(url===root+(engine==='claude'?'/anthropic':'/v1'))return preset}
 return 'custom';
}
function configureEnvironmentAPIFields(engine:string){
 const claude=engine==='claude';
 element('environment-api-claude-scope').classList.toggle('hidden',!claude);element('environment-api-claude-roles').classList.toggle('hidden',!claude);input('environment-api-scope').value='duo';updateEnvironmentAPIScopeHint();resetEnvironmentAPIClaudeRoles();
 input('environment-api-provider').innerHTML=claude?'<option value="anthropic">Anthropic 官方 API</option><option value="zai">Z.ai · GLM（Anthropic 兼容）</option><option value="custom">自定义兼容 API</option><option value="mimo">MiMo 兼容 API</option>':'<option value="deepseek">DeepSeek 官方 API</option><option value="moonshot">Moonshot · Kimi</option><option value="minimax">MiniMax 官方 API</option><option value="zai">Z.ai · GLM</option><option value="mimo">MiMo 官方 API</option><option value="custom">自定义第三方 API</option><option value="anthropic">Anthropic Messages API</option>';
 input('environment-api-mimo-plan').value='mimo';element('environment-api-mimo-wrap').classList.toggle('hidden',claude);
 input('environment-api-protocol').innerHTML=claude?'<option value="anthropic-messages">Anthropic Messages 兼容</option>':'<option value="openai-chat">OpenAI Chat Completions 兼容</option><option value="anthropic-messages">Anthropic Messages 兼容</option>';
 input('environment-api-protocol').value=claude?'anthropic-messages':'openai-chat';input('environment-api-protocol').disabled=claude;
 input('environment-api-auth').value='api-key';element('environment-api-auth-wrap').classList.toggle('hidden',!claude);
 input('environment-api-url').required=!claude;input('environment-api-model').required=!claude;
 input('environment-api-url').placeholder=claude?'留空使用 Anthropic 官方 API':'https://api.xiaomimimo.com/v1';
 input('environment-api-model').placeholder=claude?'留空使用 Claude Code 默认模型，或填写服务模型 ID':'读取后选择，或填写服务提供的模型 ID';
 element('environment-api-hint').textContent=claude?'这是所选环境的 Claude Code 配置。默认使用 Anthropic 官方 API；MiMo 等第三方服务需使用 Messages 兼容接口。保存时保留 Claude 权限、MCP 和会话。':'这是所选环境的 MiMo Code 配置。MiMo 模型使用 OpenAI Chat Completions（/v1）；其他模型可用兼容协议。Token Plan Key 需匹配套餐区域地址。保留原生权限与会话目录。';
}
function updateEnvironmentAPIKeyNote(){
 const view=environmentAPIView;if(!view)return;
 const same=input('environment-api-url').value.trim().replace(/\/$/,'')===view.base_url.replace(/\/$/,'')&&input('environment-api-protocol').value===view.api_protocol&&(view.engine!=='claude'||input('environment-api-auth').value===(view.auth_mode==='auth-token'?'auth-token':'api-key'));
 const keep=view.key_configured&&same;input('environment-api-key').required=!keep;input('environment-api-key').placeholder=keep?'留空保留已保存的 Key':'填写当前服务的 API key';
 element('environment-api-key-note').textContent=keep?'Key 已配置，不回显；留空保留，填写新 Key 替换。':view.key_configured?'服务地址、协议或认证方式已改变，请填写当前服务的 Key。':'首次保存需要填写 Key。';
}
function applyEnvironmentAPIPreset(){
 const view=environmentAPIView;if(!view)return;
 clearEnvironmentAPIModels();const provider=input('environment-api-provider').value,preset=provider==='mimo'?input('environment-api-mimo-plan').value:provider,claude=view.engine==='claude';element('environment-api-mimo-wrap').classList.toggle('hidden',provider!=='mimo');if(preset==='custom'){if(claude&&!view.key_configured&&input('environment-api-role-mode').value==='native'){input('environment-api-role-mode').value='uniform';updateEnvironmentAPIRoleFields()}updateEnvironmentAPIKeyNote();return}
 const before=[input('environment-api-url').value,input('environment-api-protocol').value,input('environment-api-auth').value].join('\n');
 if(preset==='anthropic'){input('environment-api-protocol').value='anthropic-messages';input('environment-api-url').value=claude?'':'https://api.anthropic.com/v1';input('environment-api-auth').value='api-key'}
 else if(preset==='zai'){input('environment-api-protocol').value='anthropic-messages';input('environment-api-url').value='https://api.z.ai/api/anthropic';input('environment-api-auth').value='auth-token'}
 else if(preset==='deepseek'){input('environment-api-protocol').value='openai-chat';input('environment-api-url').value='https://api.deepseek.com/v1';input('environment-api-auth').value='api-key'}
 else if(preset==='moonshot'){input('environment-api-protocol').value='openai-chat';input('environment-api-url').value='https://api.moonshot.cn/v1';input('environment-api-auth').value='api-key'}
 else if(preset==='minimax'){input('environment-api-protocol').value='openai-chat';input('environment-api-url').value='https://api.minimaxi.com/v1';input('environment-api-auth').value='auth-token'}
 else {const base=preset==='mimo'?'https://api.xiaomimimo.com':'https://token-plan-'+preset.slice(5)+'.xiaomimimo.com';if(!claude)input('environment-api-protocol').value='openai-chat';input('environment-api-url').value=base+(claude?'/anthropic':'/v1');input('environment-api-auth').value='auth-token'}
 if(before!==[input('environment-api-url').value,input('environment-api-protocol').value,input('environment-api-auth').value].join('\n')){input('environment-api-model').value='';input('environment-api-key').value='';resetEnvironmentAPIClaudeRoles();input('environment-api-role-mode').value=preset==='anthropic'?'native':'uniform';updateEnvironmentAPIRoleFields()}
 updateEnvironmentAPIKeyNote();
}
function renderEnvironmentAPI(view:EnvironmentAPIView){
 environmentAPIView=view;const claude=view.engine==='claude';
 configureEnvironmentAPIFields(view.engine);
 if(claude){input('environment-api-scope').value=view.api_scope||(view.key_configured?'environment':'duo');if(!view.key_configured&&view.api_scope==='environment')input('environment-api-scope').value='duo';updateEnvironmentAPIScopeHint();const roles=view.claude_roles;input('environment-api-role-mode').value=roles?.mode||(view.base_url&&view.model?'uniform':'native');for(const id of ['opus','sonnet','haiku','fable','subagent'] as const)input('environment-api-role-'+id).value=roles?.[id]||'';updateEnvironmentAPIRoleFields()}
 element('environment-api-title').textContent=taskEngineName(view.engine)+' API 配置';
 input('environment-api-protocol').value=view.api_protocol;input('environment-api-auth').value=view.auth_mode==='auth-token'?'auth-token':'api-key';
 element<HTMLSelectElement>('environment-api-protocol').disabled=claude;element('environment-api-auth-wrap').classList.toggle('hidden',!claude);
 input('environment-api-url').value=view.base_url;input('environment-api-url').required=!claude;input('environment-api-model').value=view.model;input('environment-api-model').required=!claude;
 input('environment-api-key').value='';input('environment-api-key').required=!view.key_configured;input('environment-api-key').placeholder=view.key_configured?'留空保留已保存的 Key':'填写服务商 API key';
 element('environment-api-key-note').textContent=view.key_configured?'Key 已配置，不回显；留空保留，填写新 Key 替换。改变服务地址或协议时需要重新填写 Key。':'首次保存需要填写 Key。';
 const preset=environmentAPIPreset(view.engine,view.base_url),mimo=preset.startsWith('mimo');input('environment-api-provider').value=mimo?'mimo':preset;input('environment-api-mimo-plan').value=mimo?preset:'mimo';element('environment-api-mimo-wrap').classList.toggle('hidden',!mimo);updateEnvironmentAPIKeyNote();
 renderEnvironmentAPIModels(view.models||[]);
 element<HTMLFieldSetElement>('environment-api-fields').disabled=false;
 // The protocol remains fixed for Claude even after the enclosing fieldset enables.
 element<HTMLSelectElement>('environment-api-protocol').disabled=claude;
 button('environment-api-save').disabled=false;
}
function renderEnvironmentAPIModels(models:{id:string;name?:string}[]){element('environment-api-models').innerHTML=models.map(m=>'<option value="'+escapeHTML(m.id)+'">'+escapeHTML(m.name||m.id)+'</option>').join('')}
async function openEnvironmentAPI(engine:string,environmentID:string){
 if(!['claude','mimo','opencode'].includes(engine)||environmentAPIBusy||engineSetupBusy||harnessAPIBusy||accountImportBusy||accountSyncBusy)return;
 const env=settings.config.environments.find(e=>e.id===environmentID);if(!env)return;storeEnvironmentEditor();
 const epoch=shellEpoch,request=++environmentAPIRequest;environmentAPIView=null;
 const dialog=element<HTMLDialogElement>('environment-api-dialog');
 element('environment-api-title').textContent=taskEngineName(engine)+' API 配置';element('environment-api-target').textContent='目标环境：'+env.name+'（'+[env.type,env.distro,env.user,env.host].filter(Boolean).join(' / ')+ '）';
 configureEnvironmentAPIFields(engine);input('environment-api-provider').value=engine==='claude'?'anthropic':'mimo';
 input('environment-api-key').value='';input('environment-api-url').value='';input('environment-api-model').value='';clearEnvironmentAPIModels();
 input('environment-api-key').required=true;input('environment-api-key').placeholder='读取目标配置后再填写 Key';element('environment-api-key-note').textContent='正在读取当前引擎配置；不复用其他引擎的 Key。';
 element<HTMLFieldSetElement>('environment-api-fields').disabled=true;button('environment-api-save').disabled=true;button('environment-api-close').disabled=false;element('environment-api-status').textContent='正在读取目标环境配置…';
 if(!dialog.open)dialog.showModal();
 try{const view=await api<EnvironmentAPIView>('environments/'+encodeURIComponent(environmentID)+'/engines/'+engine+'/api','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==environmentAPIRequest||!dialog.open)return;if(view.engine!==engine||view.environment_id!==environmentID)throw new Error('配置响应与所选引擎或环境不一致，请重新打开窗口。');renderEnvironmentAPI(view);element('environment-api-status').textContent='可验证并读取模型；保存时自动读取。不发送推理请求。'}
 catch(e){if(shellCurrent(epoch)&&request===environmentAPIRequest&&dialog.open)element('environment-api-status').textContent=(e as Error).message}
}
async function submitEnvironmentAPI(save:boolean){
 const view=environmentAPIView;if(!view||environmentAPIBusy||engineSetupBusy)return;
 const epoch=shellEpoch,request=environmentAPIRequest,dialog=element<HTMLDialogElement>('environment-api-dialog');
 const roleMode=input('environment-api-role-mode').value;
 const body={target:view.target,api_protocol:input('environment-api-protocol').value,base_url:input('environment-api-url').value.trim(),model:input('environment-api-model').value.trim(),api_key:input('environment-api-key').value,...(view.engine==='claude'?{auth_mode:input('environment-api-auth').value,api_scope:input('environment-api-scope').value,claude_roles:{mode:roleMode,...Object.fromEntries(['opus','sonnet','haiku','fable','subagent'].map(id=>[id,roleMode==='custom'?input('environment-api-role-'+id).value.trim():'']))}}:{})};
 environmentAPIBusy=true;element<HTMLFieldSetElement>('environment-api-fields').disabled=true;button('environment-api-save').disabled=true;button('environment-api-close').disabled=true;clearEnvironmentAPIModels();element('environment-api-status').textContent=save?'正在验证并保存到目标环境…':'正在从目标环境验证认证并读取模型…';renderEngineCatalog();
 try{
  const result=await api<EnvironmentAPIView|HarnessDiscovery>('environments/'+encodeURIComponent(view.environment_id)+'/engines/'+view.engine+'/api'+(save?'':'/models'),save?'PUT':'POST',body,shellController.signal);
  if(!shellCurrent(epoch)||request!==environmentAPIRequest||!dialog.open)return;
  if(save){const out=result as EnvironmentAPIView;if(out.engine!==view.engine||out.environment_id!==view.environment_id)throw new Error('保存响应与所选引擎或环境不一致，请重新打开窗口核对。');renderEnvironmentAPI(out);const saved=settings.config.environments.find(e=>e.id===view.environment_id),draft=editingEnvironments.find(e=>e.id===view.environment_id);if(saved){const values:Partial<Environment>=view.engine==='mimo'?{mimo_config:out.mimo_config,mimo_model:'duo-mimo/'+out.model}:{claude_model:out.model,claude_api_config:out.claude_api_config||''};for(const key of Object.keys(values) as ('mimo_config'|'mimo_model'|'claude_model'|'claude_api_config')[]){if(draft&&draft[key]===saved[key])draft[key]=values[key];saved[key]=values[key]}if(editingID===saved.id)loadEnvironmentEditor()}invalidateModelCatalogs()}
  else {renderEnvironmentAPIModels(result.models);if(!input('environment-api-model').value&&result.models.length)input('environment-api-model').value=result.models[0].id}
  element('environment-api-status').textContent=result.message||'配置已保存并生效。';
 }catch(e){if(shellCurrent(epoch)&&request===environmentAPIRequest&&dialog.open)element('environment-api-status').textContent=(e as Error).message}
 finally{body.api_key='';if(shellCurrent(epoch)){environmentAPIBusy=false;if(request===environmentAPIRequest&&dialog.open){element<HTMLFieldSetElement>('environment-api-fields').disabled=false;element<HTMLSelectElement>('environment-api-protocol').disabled=view.engine==='claude';button('environment-api-save').disabled=false;button('environment-api-close').disabled=false}renderEngineCatalog()}}
}
type HarnessDiscovery={status:string;models:{id:string;name?:string}[];message:string};
let harnessAPIView:HarnessAPIView|null=null,harnessAPIRequest=0,harnessAPIBusy=false;
function installHarnessAPI(){
 harnessAPIView=null;harnessAPIRequest=0;harnessAPIBusy=false;
 element('settings-engines').querySelector('p')!.textContent='统一检测 Windows / WSL / SSH 环境与引擎。Harness、Claude Code 和 MiMo Code 可在对应环境单独配置 API；Codex 账号在“账号管理”中配置。';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="harness-api-dialog"><form id="harness-api-form"><h2>Harness API 配置</h2><p id="harness-api-target" class="muted"></p><fieldset id="harness-api-fields"><label for="harness-api-protocol">API 协议</label><select id="harness-api-protocol"><option value="openai-completions">OpenAI Chat Completions（DeepSeek / 兼容 API）</option><option value="openai-responses">OpenAI Responses</option><option value="anthropic-messages">Anthropic Messages</option></select><label for="harness-api-url">API 根地址</label><input id="harness-api-url" type="url" maxlength="2000" required placeholder="https://api.deepseek.com"><p class="muted">填写服务商提供的根地址，例如 https://api.deepseek.com 或 https://api.openai.com/v1，不要填写 /chat/completions。</p><label for="harness-api-model">模型 ID</label><input id="harness-api-model" maxlength="120" required placeholder="例如 deepseek-chat"><label for="harness-api-key">API key</label><input id="harness-api-key" type="password" maxlength="16000" autocomplete="off"><p id="harness-api-key-note" class="muted"></p></fieldset><p class="muted">配置保存到所选用户的默认 Harness 原生目录；Key 单独保存，不回显。Duo 通过 ACP 调用 dsh CLI，由 Harness 访问模型 API。新启动的 Harness 读取此配置；独立账号目录仍使用各自配置。</p><p id="harness-api-status" role="status"></p><div class="dialog-footer"><button type="button" id="harness-api-close">关闭</button><button type="submit" id="harness-api-save" class="primary">保存配置</button></div></form></dialog>`);
 button('harness-api-close').onclick=()=>{if(!harnessAPIBusy)element<HTMLDialogElement>('harness-api-dialog').close()};
 element('harness-api-dialog').addEventListener('cancel',e=>{if(harnessAPIBusy)e.preventDefault()});
 element('harness-api-dialog').addEventListener('close',()=>{harnessAPIRequest++;harnessAPIView=null;input('harness-api-key').value=''});
 element('harness-api-form').addEventListener('submit',e=>{e.preventDefault();void saveHarnessAPI()});
 input('harness-api-model').insertAdjacentHTML('afterend','<select id="harness-api-model-list" aria-label="已读取的模型"><option value="">先验证并读取模型，或手动填写 ID</option></select>');
 element('harness-api-key-note').insertAdjacentHTML('afterend','<button type="button" id="harness-api-discover">验证并读取模型</button><p class="muted">只请求模型列表，不发送推理请求。接口认证成功不代表每个模型均可调用；保存时也会自动读取。</p>');
 button('harness-api-save').textContent='保存并读取模型';
 element('harness-api-model-list').insertAdjacentHTML('afterend','<label for="harness-api-input">所选模型输入能力</label><select id="harness-api-input"><option value="">原生默认 / 保留已有设置</option><option value="text">仅文字</option><option value="image">文字和图片</option></select><p class="muted">仅为上方模型保存能力声明。请按服务商说明选择；声明支持图片不能让文字模型识图，读取模型列表也不会验证图片能力。</p>');
 element<HTMLSelectElement>('harness-api-model-list').onchange=()=>{const value=element<HTMLSelectElement>('harness-api-model-list').value;if(value){input('harness-api-model').value=value;renderHarnessAPIInput()}};
 input('harness-api-model').addEventListener('input',()=>renderHarnessAPIInput());
 button('harness-api-discover').onclick=()=>void readHarnessAPIModels();
 for(const id of ['harness-api-protocol','harness-api-url','harness-api-key'])element(id).addEventListener('input',()=>{renderHarnessAPIModels([]);if(id!=='harness-api-key')renderHarnessAPIInput()});
 disposeWithShell(()=>{input('harness-api-key').value='';harnessAPIView=null;harnessAPIRequest++;harnessAPIBusy=false});
}
function renderHarnessAPI(view:HarnessAPIView){
 harnessAPIView=view;element<HTMLSelectElement>('harness-api-protocol').value=view.api;input('harness-api-url').value=view.base_url;input('harness-api-model').value=view.model;
 input('harness-api-key').value='';input('harness-api-key').required=!view.key_configured;input('harness-api-key').placeholder=view.key_configured?'留空保留已保存的 Key':'填写服务商 API key';
 element('harness-api-key-note').textContent=view.key_configured?'已配置 Key；留空保留，填写新 Key 则替换。':'尚未配置 Key；首次保存需要填写。';
 element<HTMLFieldSetElement>('harness-api-fields').disabled=false;button('harness-api-save').disabled=false;
 renderHarnessAPIModels(view.models||[]);
 renderHarnessAPIInput();
}
function renderHarnessAPIInput(){
 const view=harnessAPIView,sameEndpoint=view&&element<HTMLSelectElement>('harness-api-protocol').value===view.api&&input('harness-api-url').value.trim().replace(/\/+$/,'')===view.base_url.replace(/\/+$/,'');
 const values=sameEndpoint?view.model_inputs?.[input('harness-api-model').value.trim()]:undefined;
 element<HTMLSelectElement>('harness-api-input').value=Array.isArray(values)?(values.includes('image')?'image':values.includes('text')?'text':''):'';
}
function renderHarnessAPIModels(models:{id:string;name?:string}[]){
 const select=element<HTMLSelectElement>('harness-api-model-list');
 select.innerHTML='<option value="">'+(models.length?'选择模型（'+models.length+' 个）':'先验证并读取模型，或手动填写 ID')+'</option>'+models.map(m=>'<option value="'+escapeHTML(m.id)+'">'+escapeHTML(m.name&&m.name!==m.id?m.name+' · '+m.id:m.id)+'</option>').join('');
 select.value=models.some(m=>m.id===input('harness-api-model').value)?input('harness-api-model').value:'';
}
async function readHarnessAPIModels(){
 const view=harnessAPIView;if(!view||harnessAPIBusy||engineSetupBusy)return;
 const epoch=shellEpoch,request=harnessAPIRequest,dialog=element<HTMLDialogElement>('harness-api-dialog');
 const payload={target:view.target,api:element<HTMLSelectElement>('harness-api-protocol').value,base_url:input('harness-api-url').value.trim(),api_key:input('harness-api-key').value};
 harnessAPIBusy=true;element<HTMLFieldSetElement>('harness-api-fields').disabled=true;button('harness-api-save').disabled=true;button('harness-api-close').disabled=true;
 element('harness-api-status').textContent='正在从目标环境验证认证并读取模型…';renderHarnessAPIModels([]);
 try{
  const result=await api<HarnessDiscovery>('environments/'+encodeURIComponent(view.environment_id)+'/harness-api/models','POST',payload,shellController.signal);
  if(!shellCurrent(epoch)||request!==harnessAPIRequest||!dialog.open)return;
  if(result.status==='ready'){
   if(!result.models.some(m=>m.id===input('harness-api-model').value)&&result.models.length){input('harness-api-model').value=result.models[0].id;renderHarnessAPIInput()}
   renderHarnessAPIModels(result.models);
  }
  element('harness-api-status').textContent=result.message+(result.status==='ready'?' 尚未保存，确认模型后点击“保存并读取模型”。':'');
 }catch(e){if(shellCurrent(epoch)&&request===harnessAPIRequest&&dialog.open)element('harness-api-status').textContent=(e as Error).message}
 finally{
  payload.api_key='';
  if(shellCurrent(epoch)){harnessAPIBusy=false;if(request===harnessAPIRequest&&dialog.open){element<HTMLFieldSetElement>('harness-api-fields').disabled=false;button('harness-api-save').disabled=false;button('harness-api-close').disabled=false}renderEngineCatalog()}
 }
}
async function openHarnessAPI(environmentID:string){
 if(harnessAPIBusy||environmentAPIBusy||engineSetupBusy||accountImportBusy||accountSyncBusy)return;
 const env=settings.config.environments.find(e=>e.id===environmentID);if(!env)return;
 storeEnvironmentEditor();
 const epoch=shellEpoch,request=++harnessAPIRequest;harnessAPIView=null;
 const dialog=element<HTMLDialogElement>('harness-api-dialog');
 element('harness-api-target').textContent='目标环境：'+env.name+'（'+[env.type,env.distro,env.user,env.host].filter(Boolean).join(' / ')+ '）';
 input('harness-api-key').value='';input('harness-api-url').value='';input('harness-api-model').value='';element('harness-api-key-note').textContent='';
 element<HTMLSelectElement>('harness-api-input').value='';
 renderHarnessAPIModels([]);
 element<HTMLFieldSetElement>('harness-api-fields').disabled=true;button('harness-api-save').disabled=true;button('harness-api-close').disabled=false;element('harness-api-status').textContent='正在读取目标环境配置…';
 if(!dialog.open)dialog.showModal();
 try{
  const view=await api<HarnessAPIView>('environments/'+encodeURIComponent(environmentID)+'/harness-api','GET',undefined,shellController.signal);
  if(!shellCurrent(epoch)||request!==harnessAPIRequest||!dialog.open)return;
  renderHarnessAPI(view);element('harness-api-status').textContent='可验证认证并读取模型；保存时自动读取。不发送推理请求、不消耗模型额度。';
 }catch(e){if(shellCurrent(epoch)&&request===harnessAPIRequest&&dialog.open)element('harness-api-status').textContent=(e as Error).message}
}
async function saveHarnessAPI(){
 const view=harnessAPIView;if(!view||harnessAPIBusy||engineSetupBusy)return;
 const epoch=shellEpoch,request=harnessAPIRequest,dialog=element<HTMLDialogElement>('harness-api-dialog');
 const payload={target:view.target,api:element<HTMLSelectElement>('harness-api-protocol').value,base_url:input('harness-api-url').value.trim(),model:input('harness-api-model').value.trim(),api_key:input('harness-api-key').value};
 const capability=element<HTMLSelectElement>('harness-api-input').value;
 const configured=capability?{...payload,input:capability==='image'?['text','image']:['text']}:payload;
 harnessAPIBusy=true;element<HTMLFieldSetElement>('harness-api-fields').disabled=true;button('harness-api-save').disabled=true;button('harness-api-close').disabled=true;element('harness-api-status').textContent='正在验证、读取模型并保存到目标环境…';
 try{
  const result=await api<HarnessAPIView>('environments/'+encodeURIComponent(view.environment_id)+'/harness-api','PUT',configured,shellController.signal);
  if(!shellCurrent(epoch)||request!==harnessAPIRequest||!dialog.open)return;
  renderHarnessAPI(result);element('harness-api-status').textContent=result.message||'API 配置已保存。';
  // Merge only saved route metadata into the settings draft. Keep unrelated
  // environment edits and current task/session state intact.
  const saved=settings.config.environments.find(e=>e.id===view.environment_id),draft=editingEnvironments.find(e=>e.id===view.environment_id);
  if(saved){for(const key of ['harness_provider','harness_model'] as const){const value=key==='harness_provider'?'duo-api':result.model;if(draft&&draft[key]===saved[key])draft[key]=value;saved[key]=value}if(editingID===saved.id)loadEnvironmentEditor()}
  invalidateModelCatalogs();renderEngineCatalog();
 }catch(e){if(shellCurrent(epoch)&&request===harnessAPIRequest&&dialog.open)element('harness-api-status').textContent=(e as Error).message}
 finally{
  // A key is never retained in an application-level retry object.
  payload.api_key='';
  configured.api_key='';
  if(shellCurrent(epoch)){harnessAPIBusy=false;if(request===harnessAPIRequest&&dialog.open){element<HTMLFieldSetElement>('harness-api-fields').disabled=false;button('harness-api-save').disabled=false;button('harness-api-close').disabled=false}renderEngineCatalog()}
 }
}
