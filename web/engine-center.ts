let engineCenterTab='codex';
let engineCenterEnvPicks:Record<string,Set<string>>={};
let engineCenterSelectedCred='';

function engineCenterCredList(engine:string,profiles:EngineProfile[]):string{
 const rows=profiles.map(p=>`<button type="button" class="engine-center-cred ${engineCenterSelectedCred===p.id?'is-selected':''}" data-engine-center-cred="${escapeHTML(p.id)}"><strong>${escapeHTML(p.name)}</strong><small>${escapeHTML(engineCredentialLabel(p.kind))}</small></button>`).join('');
 const hint=engineCenterSelectedCred?'<p class="muted">已选用：'+escapeHTML(profiles.find(p=>p.id===engineCenterSelectedCred)?.name||'')+'</p>':'<p class="muted">点选一条 Key/账号，再配置到环境。</p>';
 return (rows||'<p class="muted">尚未添加账号或 Key。</p>')+hint;
}

function engineCenterEnvCheckboxes(kind:string,engine:string):string{
 const selected=engineCenterEnvPicks[kind+':'+engine]||new Set(settings?.config?.environments?.map(e=>e.id)||[]);
 return (settings?.config?.environments||[]).map(env=>{
  const tool=engineCenterEnvTool(engine,env);
  const installed=!!tool?.path&&tool.state!=='missing';
  const disabled=kind==='install'&&installed;
  return `<label class="check-row"><input type="checkbox" data-engine-center-env="${escapeHTML(kind+':'+engine+':'+env.id)}" ${selected.has(env.id)&&!disabled?'checked':''} ${disabled?'disabled':''}> ${escapeHTML(env.name)}<span class="engine-center-env-state">${installed?'已安装':'未安装'}</span></label>`;
 }).join('');
}
function engineCenterEnvTool(engine:string,env:{id:string}):DetectedEngineTool|undefined{
 const key=({codex:'codex',claude:'claude','deepseek-harness':'harness',kimi:'kimi',mimo:'mimo',opencode:'opencode',zcode:'zcode'} as Record<string,string>)[engine]||'codex';
 const item=(engineEnvironmentStatuses||[]).find(s=>s.environment_id===env.id);
 return item?((item as unknown as Record<string,DetectedEngineTool>)[key]):undefined;
}
function engineCenterSelectedEnvs(kind:string,engine:string):string[]{
 const prefix=kind+':'+engine+':';
 const live=[...document.querySelectorAll<HTMLInputElement>('[data-engine-center-env^="'+CSS.escape(prefix)+'"]:checked')].map(x=>x.dataset.engineCenterEnv!.slice(prefix.length));
 return live;
}
const engineCenterKeyPresets:Record<string,{url:string;protocol:string;auth:string}>={
 deepseek:{url:'https://api.deepseek.com/v1',protocol:'openai-chat',auth:'api-key'},
 zai:{url:'https://api.z.ai/api/anthropic',protocol:'anthropic-messages',auth:'auth-token'},
 moonshot:{url:'https://api.moonshot.cn/v1',protocol:'openai-chat',auth:'api-key'},
 minimax:{url:'https://api.minimaxi.com/v1',protocol:'openai-chat',auth:'auth-token'},
 mimo:{url:'https://api.xiaomimimo.com/v1',protocol:'openai-chat',auth:'auth-token'},
 custom:{url:'',protocol:'openai-chat',auth:'api-key'},
};
function engineCenterAddKeyDialog():string{
 return `<dialog id="engine-center-key-dialog" class="engine-center-key-dialog">
  <h3>添加 API Key</h3>
  <div class="engine-center-key-form">
   <label>名称 <input id="engine-center-key-name" maxlength="60" placeholder="例如 DeepSeek 工作 Key"></label>
   <label>服务预设 <select id="engine-center-key-provider">
    <option value="deepseek">DeepSeek</option><option value="zai">Z.ai · GLM</option><option value="moonshot">Moonshot · Kimi</option>
    <option value="minimax">MiniMax</option><option value="mimo">MiMo</option><option value="custom">自定义第三方</option>
   </select></label>
   <label>API 根地址 <input id="engine-center-key-url" type="url" placeholder="选择预设后自动填写"></label>
   <label>API key <input id="engine-center-key-secret" type="password" maxlength="16000" autocomplete="off"></label>
   <div class="engine-center-key-models">
    <button type="button" id="engine-center-key-detect">检测可用模型</button>
    <label>模型 <select id="engine-center-key-model-select"><option value="">（先检测或手动填写）</option></select></label>
    <label>或手动填写模型 ID <input id="engine-center-key-model" maxlength="120" placeholder="例如 deepseek-v4.1-flash"></label>
   </div>
   <p id="engine-center-key-status" class="muted" role="status"></p>
  </div>
  <div class="dialog-footer"><button type="button" id="engine-center-key-cancel">取消</button><button type="button" class="primary" id="engine-center-key-save">保存 Key</button></div>
 </dialog>`;
}
function openEngineCenterKeyDialog(engine:string){
 const dialog=element<HTMLDialogElement>('engine-center-key-dialog');
 if(!dialog)return;
 input('engine-center-key-name').value='';input('engine-center-key-secret').value='';input('engine-center-key-model').value='';
 const select=element<HTMLSelectElement>('engine-center-key-model-select');if(select)select.innerHTML='<option value="">（先检测或手动填写）</option>';
 const provider=input('engine-center-key-provider');
 provider.value='deepseek';
 const applyPreset=()=>{const p=engineCenterKeyPresets[provider.value]||engineCenterKeyPresets.custom;input('engine-center-key-url').value=p.url};
 provider.onchange=applyPreset;applyPreset();
 element('engine-center-key-status').textContent='';
 dialog.showModal();
 button('engine-center-key-save').onclick=()=>void engineCenterSaveKey(engine);
 button('engine-center-key-cancel').onclick=()=>dialog.close();
 button('engine-center-key-detect').onclick=()=>void engineCenterDetectModels(engine);
}
async function engineCenterDetectModels(engine:string){
 const status=element('engine-center-key-status');
 const key=input('engine-center-key-secret').value;
 const url=input('engine-center-key-url').value.trim();
 if(!key){if(status)status.textContent='请先填写 API key。';return}
 const envs=settings?.config?.environments||[];if(!envs.length){if(status)status.textContent='请先添加执行环境。';return}
 const target=({codex:'claude',claude:'claude','deepseek-harness':'mimo',mimo:'mimo',opencode:'opencode',kimi:'claude',zcode:'claude'} as Record<string,string>)[engine]||'claude';
 const provider=input('engine-center-key-provider').value;
 const preset=engineCenterKeyPresets[provider]||engineCenterKeyPresets.custom;
 if(status)status.textContent='正在检测可用模型…';
 try{
  const view=await api<{target:string}>('environments/'+encodeURIComponent(envs[0].id)+'/engines/'+target+'/api','GET');
  const result=await api<{models:{id:string;name?:string}[];message?:string}>('environments/'+encodeURIComponent(envs[0].id)+'/engines/'+target+'/api/models','POST',{
   target:view.target||'',engine:target,api_protocol:preset.protocol,base_url:url,api_key:key,auth_mode:preset.auth,model:'',
  });
  const select=element<HTMLSelectElement>('engine-center-key-model-select');
  const models=result.models||[];
  select.innerHTML='<option value="">（选择模型）</option>'+models.map(m=>`<option value="${escapeHTML(m.id)}">${escapeHTML(m.name||m.id)}</option>`).join('');
  if(status)status.textContent=models.length?('检测到 '+models.length+' 个模型，请选择。'+(result.message?' '+result.message:'')):(result.message||'未检测到模型，可手动填写模型 ID。');
 }catch(e){if(status)status.textContent='检测失败：'+(e as Error).message+'；可手动填写模型 ID。'}
}
function renderEngineCenterPanel(){
 const host=element('engine-center-body');
 if(!host)return;
 const engines=engineCatalog?.engines||[];
 const current=engines.find(e=>e.id===engineCenterTab)||engines[0];
 if(!current){host.innerHTML='<p class="muted">正在读取引擎目录…</p>';return}
 engineCenterTab=current.id;
 const tabs=element('engine-center-tabs');
 if(tabs)tabs.innerHTML=engines.map(e=>`<button type="button" data-engine-center-tab="${escapeHTML(e.id)}" class="${e.id===current.id?'selected':''}">${escapeHTML(e.name)}</button>`).join('');
 const profiles=(engineCatalog?.profiles||[]).filter(p=>p.engine===current.id);
 host.innerHTML=`<section class="engine-center-panel">
  <header class="engine-center-panel-head">
   <div class="engine-center-panel-title"><h3>${escapeHTML(current.name)}</h3><span class="engine-center-badge ${current.runnable?'is-ready':'is-pending'}">${current.runnable?'可运行':'未开放'}</span></div>
   <p class="muted">${escapeHTML(current.description||'')}</p>
  </header>
  <ol class="engine-center-steps">
   <li class="engine-center-step">
    <div class="engine-center-step-head"><span class="engine-center-step-num">1</span><h4>安装 CLI</h4></div>
    <p class="muted">${escapeHTML(current.install_description||'')}</p>
    <div class="engine-center-envs">${engineCenterEnvCheckboxes('install',current.id)}</div>
    <div class="actions"><button type="button" class="primary" data-engine-center-install="${escapeHTML(current.id)}" ${current.auto_install?'':'disabled'}>一键安装到勾选环境</button>${current.documentation_url?'<a href="'+escapeHTML(current.documentation_url)+'" target="_blank" rel="noopener noreferrer">文档 ↗</a>':''}</div>
   </li>
   <li class="engine-center-step">
    <div class="engine-center-step-head"><span class="engine-center-step-num">2</span><h4>账号 / API Key → 配置到环境</h4></div>
    <p class="muted">添加或点选 Key，勾选环境后一键写入该引擎的原生配置。列表不回显 Key。</p>
    <div id="engine-center-creds" class="engine-center-creds">${engineCenterCredList(current.id,profiles)}</div>
    <div class="engine-center-envs">${engineCenterEnvCheckboxes('configure',current.id)}</div>
    <div class="actions"><button type="button" data-engine-center-add="${escapeHTML(current.id)}">＋ 添加 API Key</button><button type="button" class="primary" data-engine-center-configure="${escapeHTML(current.id)}">配置到勾选环境</button><button type="button" data-engine-center-open-accounts>账号管理</button></div>
   </li>
  </ol>
  <p id="engine-center-status" class="engine-center-status" role="status"></p>
 </section>`;
 tabs?.querySelectorAll<HTMLButtonElement>('[data-engine-center-tab]').forEach(b=>b.onclick=()=>{engineCenterTab=b.dataset.engineCenterTab!;renderEngineCenterPanel()});
 host.querySelectorAll<HTMLInputElement>('[data-engine-center-env]').forEach(cb=>cb.onchange=()=>{
  const parts=cb.dataset.engineCenterEnv!.split(':');const kind=parts[0],engine=parts[1],env=parts.slice(2).join(':');
  const key=kind+':'+engine;if(!engineCenterEnvPicks[key])engineCenterEnvPicks[key]=new Set();
  if(cb.checked)engineCenterEnvPicks[key].add(env);else engineCenterEnvPicks[key].delete(env);
 });
 host.querySelector<HTMLButtonElement>('[data-engine-center-install]')?.addEventListener('click',()=>void engineCenterInstall(current.id));
 host.querySelector<HTMLButtonElement>('[data-engine-center-configure]')?.addEventListener('click',()=>void engineCenterConfigure(current.id));
 host.querySelectorAll<HTMLButtonElement>('[data-engine-center-cred]').forEach(b=>b.addEventListener('click',()=>{engineCenterSelectedCred=b.dataset.engineCenterCred!;renderEngineCenterPanel()}));
 host.querySelector<HTMLButtonElement>('[data-engine-center-add]')?.addEventListener('click',()=>openEngineCenterKeyDialog(current.id));
 host.querySelector<HTMLButtonElement>('[data-engine-center-open-accounts]')?.addEventListener('click',()=>void openAccountCenter());
 void engineCenterRefreshStatus(current.id);
}
async function engineCenterSaveKey(engine:string){
 const status=element('engine-center-key-status');
 const name=input('engine-center-key-name').value.trim(),provider=input('engine-center-key-provider').value;
 const preset=engineCenterKeyPresets[provider]||engineCenterKeyPresets.custom;
 const model=input('engine-center-key-model').value.trim()||input('engine-center-key-model-select').value;
 const body={name,provider,api_protocol:preset.protocol,base_url:input('engine-center-key-url').value.trim()||preset.url,auth_mode:preset.auth,api_key:input('engine-center-key-secret').value,model};
 try{
  const saved=await api<{id:string}>('api-credentials','PUT',body);
  engineCenterSelectedCred=saved.id||'';
  element<HTMLDialogElement>('engine-center-key-dialog')?.close();
  if(status)status.textContent='Key 已保存，可勾选环境后配置。';
  renderEngineCenterPanel();
 }catch(e){if(status)status.textContent=(e as Error).message}
}
async function engineCenterRefreshStatus(engine:string){
 try{
  const data=await api<EngineStatusResponse>('engine-status','GET');
  engineEnvironmentStatuses=data.items||[];engineStatusSignature=engineEnvironmentSignature();
  renderEngineCenterPanel();
 }catch{/* status optional */}
}
async function engineCenterInstall(engine:string){
 const envs=engineCenterSelectedEnvs('install',engine);
 const status=element('engine-center-status');
 if(!envs.length){if(status)status.textContent='请勾选未安装的目标环境。';return}
 for(const id of envs){
  try{const job=await api<EngineSetupJob>('engine-setup','POST',{action:'install',engine,environment_id:id,id:uidLike()});if(status)status.textContent=job.message||'安装已开始…'}
  catch(e){if(status)status.textContent=(e as Error).message}
 }
}
async function engineCenterConfigure(engine:string){
 const envs=engineCenterSelectedEnvs('configure',engine);
 const status=element('engine-center-status');
 if(!envs.length){if(status)status.textContent='请勾选目标环境。';return}
 if(!['claude','mimo','opencode'].includes(engine)){if(status)status.textContent='此引擎请在账号管理中切换账号，或用「配置 API」。';return}
 try{
  const out=await api<{message:string}>('api-credentials/apply-engine','POST',{engine,environments:envs});
  if(status)status.textContent=out.message||'已配置。';
 }catch(e){if(status)status.textContent=(e as Error).message+'（可先添加 API Key，再回到此处一键配置）'}
}
function uidLike(){return 'job-'+Math.random().toString(36).slice(2,10)+Date.now().toString(36)}
function installEngineCenter(){
 element('root').insertAdjacentHTML('beforeend',`<dialog id="engine-center-dialog" class="engine-center-dialog">
  <div class="engine-center-shell">
   <header class="engine-center-heading">
    <div><h2>环境与引擎</h2><p class="muted">默认 Windows；可检测 WSL、发现局域网 SSH 或手动添加。环境就绪后，新建任务、配置 API、切换账号都可选择对应环境。</p></div>
    <button type="button" id="engine-center-close" class="engine-center-close">关闭</button>
   </header>
   <div class="engine-center-layout">
    <aside class="engine-center-side">
     <div class="engine-center-side-head"><h3>执行环境</h3><div class="engine-center-side-actions">
      <button type="button" id="engine-center-detect" title="检测本机 Windows 与 WSL">检测</button>
      <button type="button" id="engine-center-add-wsl">＋ WSL</button>
      <button type="button" id="engine-center-add-ssh">＋ SSH</button>
      <button type="button" id="engine-center-discover-ssh" title="扫描局域网 SSH">发现</button>
     </div></div>
     <div id="engine-center-env-list" class="engine-center-env-list"></div>
    </aside>
    <main class="engine-center-main">
     <nav id="engine-center-tabs" class="engine-center-tabs"></nav>
     <div id="engine-center-body"></div>
    </main>
   </div>
  </div>
 </dialog>
 ${engineCenterAddKeyDialog()}`);
 button('engine-center-close').onclick=()=>element<HTMLDialogElement>('engine-center-dialog').close();
 button('engine-center-detect').onclick=()=>void refreshEngineStatus();
 button('engine-center-discover-ssh').onclick=()=>void openDiscovery((endpoint)=>{void addEnvironment('ssh');setTimeout(()=>{input('setting-host').value=endpoint.host||'';input('setting-port').value=String(endpoint.port||22)},0)});
 button('engine-center-add-ssh').onclick=()=>void addEnvironment('ssh');
 button('engine-center-add-wsl').onclick=()=>void addEnvironment('wsl');
}
function renderEngineCenterEnvironments(){
 const host=element('engine-center-env-list');if(!host)return;
 const envs=settings?.config?.environments||[];
 host.innerHTML=envs.map(e=>`<div class="engine-center-env-card ${e.id===settings?.config?.default_environment?'is-default':''}">
  <div class="engine-center-env-name"><strong>${escapeHTML(e.name)}</strong>${e.id===settings?.config?.default_environment?'<span class="engine-center-env-tag">默认</span>':''}</div>
  <small>${escapeHTML(e.type.toUpperCase())}${e.distro?' · '+escapeHTML(e.distro):''}${e.host?' · '+escapeHTML(e.host):''}${e.user?' · '+escapeHTML(e.user):''}</small>
 </div>`).join('')||'<p class="muted">暂无环境，请添加。</p>';
}
function showEngineCenter(){
 installEngineCenter();
 renderEngineCenterEnvironments();
 const dialog=element<HTMLDialogElement>('engine-center-dialog');
 if(dialog&&!dialog.open)dialog.showModal();
 void loadEngineSettings().then(()=>{renderEngineCenterEnvironments();renderEngineCenterPanel()});
}
async function openEngineCenter(){showEngineCenter()}
