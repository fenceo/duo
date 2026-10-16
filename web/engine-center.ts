let engineCenterTab='codex';
let engineCenterEnvPicks:Record<string,Set<string>>={};

function engineCenterEnvCheckboxes(kind:string,engine:string):string{
 const selected=engineCenterEnvPicks[kind+':'+engine]||new Set(settings?.config?.environments?.map(e=>e.id)||[]);
 return (settings?.config?.environments||[]).map(env=>`<label class="check-row"><input type="checkbox" data-engine-center-env="${escapeHTML(kind+':'+engine+':'+env.id)}" ${selected.has(env.id)?'checked':''}> ${escapeHTML(env.name)}（${escapeHTML(env.type.toUpperCase())}）</label>`).join('');
}
function engineCenterSelectedEnvs(kind:string,engine:string):string[]{
 const prefix=kind+':'+engine+':';
 const ids:string[]=[];
 for(const key of Object.keys(engineCenterEnvPicks[kind+':'+engine]||{}))ids.push(key);
 // Prefer live checkbox state when mounted.
 const live=[...document.querySelectorAll<HTMLInputElement>('[data-engine-center-env^="'+CSS.escape(prefix)+'"]:checked')].map(x=>x.dataset.engineCenterEnv!.slice(prefix.length));
 return live.length?live:ids;
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
 const creds=(engineCatalog?.profiles||[]).filter(p=>p.engine===current.id);
 host.innerHTML=`<section class="engine-center-panel">
  <header><h3>${escapeHTML(current.name)}</h3><p class="muted">${escapeHTML(current.description||'')}</p><p class="muted">${current.runnable?'可用于 Duo 任务':'适配器尚未开放运行'}</p></header>
  <div class="engine-center-grid">
   <div class="engine-center-card"><h4>1 · 安装 CLI</h4><p class="muted">${escapeHTML(current.install_description||'')}</p><div class="engine-center-envs">${engineCenterEnvCheckboxes('install',current.id)}</div><div class="actions"><button type="button" data-engine-center-install="${escapeHTML(current.id)}" ${current.auto_install?'':'disabled'}>一键安装到勾选环境</button>${current.documentation_url?'<a href="'+escapeHTML(current.documentation_url)+'" target="_blank" rel="noopener noreferrer">文档 ↗</a>':''}</div></div>
   <div class="engine-center-card"><h4>2 · 账号 / API Key</h4><div id="engine-center-creds">${profiles.length?profiles.map(p=>`<div class="engine-center-cred"><strong>${escapeHTML(p.name)}</strong><small>${escapeHTML(engineCredentialLabel(p.kind))}</small></div>`).join(''):'<p class="muted">尚未添加账号或 Key。</p>'}</div><div class="actions"><button type="button" data-engine-center-add="${escapeHTML(current.id)}">＋ 添加账号 / Key</button><button type="button" data-engine-center-open-accounts>账号管理</button></div></div>
   <div class="engine-center-card"><h4>3 · 一键配置到环境</h4><p class="muted">把已添加的账号或 API Key 写入勾选环境的原生配置。</p><div class="engine-center-envs">${engineCenterEnvCheckboxes('configure',current.id)}</div><div class="actions"><button type="button" data-engine-center-configure="${escapeHTML(current.id)}">配置到勾选环境</button><button type="button" data-engine-center-api="${escapeHTML(current.id)}">配置 API</button></div></div>
  </div>
  <p id="engine-center-status" role="status"></p>
 </section>`;
 tabs?.querySelectorAll<HTMLButtonElement>('[data-engine-center-tab]').forEach(b=>b.onclick=()=>{engineCenterTab=b.dataset.engineCenterTab!;renderEngineCenterPanel()});
 host.querySelectorAll<HTMLInputElement>('[data-engine-center-env]').forEach(cb=>cb.onchange=()=>{
  const parts=cb.dataset.engineCenterEnv!.split(':');const kind=parts[0],engine=parts[1],env=parts.slice(2).join(':');
  const key=kind+':'+engine;if(!engineCenterEnvPicks[key])engineCenterEnvPicks[key]=new Set();
  if(cb.checked)engineCenterEnvPicks[key].add(env);else engineCenterEnvPicks[key].delete(env);
 });
 host.querySelector<HTMLButtonElement>('[data-engine-center-install]')?.addEventListener('click',()=>void engineCenterInstall(current.id));
 host.querySelector<HTMLButtonElement>('[data-engine-center-configure]')?.addEventListener('click',()=>void engineCenterConfigure(current.id));
 host.querySelector<HTMLButtonElement>('[data-engine-center-api]')?.addEventListener('click',()=>{
  const envs=settings?.config?.environments||[];if(!envs.length)return;
  void openEnvironmentAPI(current.id==='deepseek-harness'?'mimo':current.id,envs[0].id);
 });
 host.querySelector<HTMLButtonElement>('[data-engine-center-add]')?.addEventListener('click',()=>void openAccountEditor(false));
 host.querySelector<HTMLButtonElement>('[data-engine-center-open-accounts]')?.addEventListener('click',()=>void openAccountCenter());
 void engineCenterRefreshStatus(current.id);
}
async function engineCenterRefreshStatus(engine:string){
 try{
  const data=await api<EngineStatusResponse>('engine-status','GET');
  engineEnvironmentStatuses=data.items||[];engineStatusSignature=engineEnvironmentSignature();
  const host=element('engine-center-body');if(!host)return;
  const rows=(data.items||[]).map(item=>{
   const key=({codex:'codex',claude:'claude','deepseek-harness':'harness',kimi:'kimi',mimo:'mimo',opencode:'opencode',zcode:'zcode'} as Record<string,string>)[engine]||'codex';
   const tool=(item as unknown as Record<string,DetectedEngineTool>)[key];
   return `<div class="engine-center-status-row"><span>${escapeHTML(item.name)}</span><span class="detected-tool detected-tool-${escapeHTML(tool?.state||'unknown')}">${escapeHTML(tool?.label||'未检测')}</span></div>`;
  }).join('');
  const statusBox=host.querySelector('#engine-center-status');
  if(statusBox)statusBox.innerHTML=rows||'';
 }catch{/* status optional */}
}
async function engineCenterInstall(engine:string){
 const envs=engineCenterSelectedEnvs('install',engine);
 const status=element('engine-center-status');
 if(!envs.length){if(status)status.textContent='请勾选目标环境。';return}
 // Engine setup currently installs one target per job; run sequentially.
 for(const id of envs){
  try{const job=await api<EngineSetupJob>('engine-setup','POST',{action:'install',engine,environment_id:id,id:uidLike()});if(status)status.textContent=job.message||'安装已开始…'}
  catch(e){if(status)status.textContent=(e as Error).message}
 }
}
async function engineCenterConfigure(engine:string){
 const envs=engineCenterSelectedEnvs('configure',engine);
 const status=element('engine-center-status');
 if(!envs.length){if(status)status.textContent='请勾选目标环境。';return}
 if(!['claude','mimo','opencode'].includes(engine)){if(status)status.textContent='此引擎请在账号管理中切换账号，或使用「配置 API」。';return}
 try{
  const out=await api<{message:string}>('api-credentials/apply-engine','POST',{engine,environments:envs});
  if(status)status.textContent=out.message||'已配置。';
 }catch(e){if(status)status.textContent=(e as Error).message+'（可先在账号管理录入 Key，再回到此处一键配置）'}
}
function uidLike(){return 'job-'+Math.random().toString(36).slice(2,10)+Date.now().toString(36)}
function installEngineCenter(){
 element('root').insertAdjacentHTML('beforeend','<dialog id="engine-center-dialog" class="engine-center-dialog"><header class="engine-center-heading"><h2>环境与引擎</h2><button type="button" id="engine-center-close">关闭</button></header><p class="muted">默认 Windows；可检测 WSL、发现局域网 SSH 或手动添加。环境就绪后，新建任务、配置 API、切换账号都可选择对应环境。</p><section class="engine-center-envs-manage"><h3>执行环境</h3><div id="engine-center-env-list"></div><div class="actions"><button type="button" id="engine-center-detect">检测 WSL 与本机环境</button><button type="button" id="engine-center-discover-ssh">发现局域网 SSH</button><button type="button" id="engine-center-add-ssh">手动添加 SSH</button><button type="button" id="engine-center-add-wsl">添加 WSL</button></div></section><nav id="engine-center-tabs" class="engine-center-tabs"></nav><div id="engine-center-body"></div></dialog>');
 button('engine-center-close').onclick=()=>element<HTMLDialogElement>('engine-center-dialog').close();
 button('engine-center-detect').onclick=()=>void refreshEngineStatus();
 button('engine-center-discover-ssh').onclick=()=>void openDiscovery((endpoint)=>{void addEnvironment('ssh');setTimeout(()=>{input('setting-host').value=endpoint.host||'';input('setting-port').value=String(endpoint.port||22)},0)});
 button('engine-center-add-ssh').onclick=()=>void addEnvironment('ssh');
 button('engine-center-add-wsl').onclick=()=>void addEnvironment('wsl');
}
function renderEngineCenterEnvironments(){
 const host=element('engine-center-env-list');if(!host)return;
 const envs=settings?.config?.environments||[];
 host.innerHTML=envs.map(e=>`<div class="engine-center-env-row"><strong>${escapeHTML(e.name)}</strong><span>${escapeHTML(e.type.toUpperCase())}${e.distro?' · '+escapeHTML(e.distro):''}${e.host?' · '+escapeHTML(e.host):''}</span>${e.id===settings?.config?.default_environment?'<span class="task-state-mark task-state-ready">默认</span>':''}</div>`).join('')||'<p class="muted">暂无环境，请添加。</p>';
}
function showEngineCenter(){
 installEngineCenter();
 renderEngineCenterEnvironments();
 const dialog=element<HTMLDialogElement>('engine-center-dialog');
 if(dialog&&!dialog.open)dialog.showModal();
 void loadEngineSettings().then(()=>{renderEngineCenterEnvironments();renderEngineCenterPanel()});
}
