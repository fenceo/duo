let accountCenterEngine='',accountCenterBusy=false,accountCenterRequest=0,accountEditID='';
let accountCenterView='cards',accountCenterPrivate=false;

function installAccountCenter(){
 accountCenterEngine='';accountCenterBusy=false;accountEditID='';accountCenterRequest++;
 accountCenterView=localStorage.getItem('duo.accountView')==='list'?'list':'cards';
 accountCenterPrivate=localStorage.getItem('duo.accountPrivate')==='1';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="account-center-dialog" aria-labelledby="account-center-title"><header class="account-center-heading"><div><h2 id="account-center-title">账号管理</h2><p>集中管理账号与 API 配置，按引擎和环境快速切换。</p></div><button type="button" id="account-center-close">关闭</button></header><nav id="account-engine-tabs" class="account-engine-tabs" aria-label="账号引擎"></nav><div class="account-center-toolbar"><input id="account-search" type="search" placeholder="搜索账号名称、引擎或环境" aria-label="搜索账号"><select id="account-environment-filter" aria-label="按环境筛选"><option value="">全部环境</option></select><select id="account-default-filter" aria-label="按默认状态筛选"><option value="">全部账号</option><option value="default">新任务默认</option><option value="other">其他账号</option></select><select id="account-sort" aria-label="账号排序"><option value="updated">最近更新</option><option value="created">创建时间</option><option value="name">名称排序</option></select><div class="account-view-controls"><button type="button" id="account-view-cards" aria-pressed="true">卡片</button><button type="button" id="account-view-list" aria-pressed="false">列表</button><button type="button" id="account-privacy" aria-pressed="false">隐藏名称</button></div></div><div class="account-center-commandbar"><p id="account-center-summary" role="status"></p><div class="actions"><button type="button" id="account-center-refresh">刷新列表</button><button type="button" id="account-center-import">导入账号</button><button type="button" id="account-center-manage" class="primary">＋ 添加账号</button></div></div><p class="account-center-hint">设为默认用于 Duo 新任务；“当前任务使用”打开切换预览；“同步到环境”切换 Windows / WSL / SSH 的原生登录。</p><div id="account-center-list"></div><details id="account-center-editor-wrap"><summary>添加、导入与配置引用</summary><div id="account-center-editor"></div></details><div id="engine-account-status" role="status"></div><button type="button" id="engine-account-cancel" class="hidden">取消当前操作</button><p id="account-center-status" role="status"></p></dialog><dialog id="account-edit-dialog" aria-labelledby="account-edit-title"><form id="account-edit-form"><h2 id="account-edit-title">重命名账号</h2><label for="account-edit-name">账号名称</label><input id="account-edit-name" maxlength="60" required autocomplete="off"><p id="account-edit-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-edit-cancel">取消</button><button type="submit" id="account-edit-save" class="primary">保存名称</button></div></form></dialog><dialog id="account-remove-dialog" aria-labelledby="account-remove-title"><h2 id="account-remove-title">移除账号引用</h2><p id="account-remove-description"></p><p>仅从 Duo 账号列表移除，保留原生账号文件和已有任务；若它是新任务默认，将恢复使用环境默认配置。</p><p id="account-remove-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-remove-cancel">取消</button><button type="button" id="account-remove-confirm" class="danger">移除引用</button></div></dialog>`);
 button('account-center-close').onclick=()=>element<HTMLDialogElement>('account-center-dialog').close();
 button('account-center-refresh').onclick=()=>void refreshAccountCenter();
 button('account-center-manage').onclick=()=>openAccountEditor(false);
 button('account-center-import').onclick=()=>openAccountEditor(true);
 input('account-search').oninput=renderAccountCenter;
 for(const id of ['account-environment-filter','account-default-filter','account-sort'])element(id).onchange=renderAccountCenter;
 for(const view of ['cards','list'])button('account-view-'+view).onclick=()=>{accountCenterView=view;localStorage.setItem('duo.accountView',view);renderAccountCenter()};
 button('account-privacy').onclick=()=>{accountCenterPrivate=!accountCenterPrivate;localStorage.setItem('duo.accountPrivate',accountCenterPrivate?'1':'0');renderAccountCenter()};
 button('account-edit-cancel').onclick=()=>{if(!accountCenterBusy)element<HTMLDialogElement>('account-edit-dialog').close()};
 button('account-remove-cancel').onclick=()=>{if(!accountCenterBusy)element<HTMLDialogElement>('account-remove-dialog').close()};
 for(const id of ['account-edit-dialog','account-remove-dialog'])element(id).addEventListener('cancel',e=>{if(accountCenterBusy)e.preventDefault()});
 element('account-edit-form').onsubmit=e=>{e.preventDefault();void renameAccount()};
 button('account-remove-confirm').onclick=()=>void removeAccountReference();
 disposeWithShell(()=>{accountCenterRequest++;accountCenterBusy=false;accountEditID=''});
}
function openAccountEditor(importing:boolean){
 element<HTMLDetailsElement>('account-center-editor-wrap').open=true;
 const target=importing?element('account-center-editor').querySelector<HTMLDetailsElement>('.account-import')!:element('engine-account-fields').querySelector<HTMLDetailsElement>('details')!;
 target.open=true;target.scrollIntoView({block:'start'});input(importing?'account-import-source':'engine-account-name').focus();
}
async function openAccountCenter(){
 const dialog=element<HTMLDialogElement>('account-center-dialog');if(!dialog.open)dialog.showModal();
 await refreshAccountCenter();
}
async function refreshAccountCenter(){
 const epoch=shellEpoch,request=++accountCenterRequest,catalogRequest=++engineSettingsRequest;
 element('account-center-status').textContent='正在读取账号配置…';button('account-center-refresh').disabled=true;
 try{
  const catalog=await api<EngineCatalog>('engines','GET',undefined,shellController.signal);
  if(!shellCurrent(epoch)||request!==accountCenterRequest||catalogRequest!==engineSettingsRequest)return;
  engineCatalog=catalog;renderAccountCenter();populateEngineProfileForm();populateEngineOnboarding();void resumeEngineSetup();element('account-center-status').textContent='';
 }catch(e){if(shellCurrent(epoch)&&request===accountCenterRequest)element('account-center-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===accountCenterRequest)button('account-center-refresh').disabled=false}
}
function accountIsDefault(profile:EngineProfile){return engineCatalog?.active_profile[profile.environment_id+':'+profile.engine]===profile.id}
function accountVisibleName(profile:EngineProfile){
 if(!accountCenterPrivate)return profile.name;
 const name=Array.from(profile.name);return name.length?name[0]+'•••'+(name.length>2?name[name.length-1]:''):'•••';
}
function renderAccountCenter(){
 if(!engineCatalog)return;
 const profiles=engineCatalog.profiles||[],engines=engineCatalog.engines;
 if(accountCenterEngine&&!engines.some(e=>e.id===accountCenterEngine))accountCenterEngine='';
 element('account-engine-tabs').innerHTML=[{id:'',name:'全部'},...engines.filter(e=>e.runnable!==false||profiles.some(p=>p.engine===e.id))].map(e=>`<button type="button" data-account-engine="${escapeHTML(e.id)}" aria-pressed="${accountCenterEngine===e.id}" class="${accountCenterEngine===e.id?'selected':''}">${escapeHTML(e.name)} <span>${profiles.filter(p=>!e.id||p.engine===e.id).length}</span></button>`).join('');
 element('account-engine-tabs').querySelectorAll<HTMLButtonElement>('[data-account-engine]').forEach(b=>b.onclick=()=>{accountCenterEngine=b.dataset.accountEngine||'';renderAccountCenter()});
 const select=input('account-environment-filter'),previous=select.value;
 const envIDs=Array.from(new Set(profiles.map(p=>p.environment_id)));
 select.innerHTML='<option value="">全部环境</option>'+envIDs.map(id=>`<option value="${escapeHTML(id)}">${escapeHTML(engineTargetName(id))}</option>`).join('');select.value=envIDs.includes(previous)?previous:'';
 const query=input('account-search').value.trim().toLocaleLowerCase(),filter=input('account-default-filter').value,sort=input('account-sort').value;
 const visible=profiles.filter(p=>(!accountCenterEngine||p.engine===accountCenterEngine)&&(!select.value||p.environment_id===select.value)&&(!filter||accountIsDefault(p)===(filter==='default'))&&(!query||[p.name,taskEngineName(p.engine),engineTargetName(p.environment_id)].some(v=>v.toLocaleLowerCase().includes(query))));
 visible.sort((a,b)=>sort==='name'?a.name.localeCompare(b.name,'zh-CN'):(sort==='created'?(b.created||0)-(a.created||0):(b.updated||0)-(a.updated||0))||a.name.localeCompare(b.name,'zh-CN'));
 element('account-center-summary').textContent=`显示 ${visible.length} / ${profiles.length} 个账号 · ${profiles.filter(accountIsDefault).length} 个新任务默认`;
 for(const view of ['cards','list'])button('account-view-'+view).setAttribute('aria-pressed',String(accountCenterView===view));
 button('account-privacy').setAttribute('aria-pressed',String(accountCenterPrivate));button('account-privacy').textContent=accountCenterPrivate?'显示名称':'隐藏名称';
 const list=element('account-center-list');list.className='account-grid'+(accountCenterView==='list'?' account-list-view':'');
 list.innerHTML=visible.map(p=>{
  const engine=engines.find(e=>e.id===p.engine),env=settings.config.environments.find(e=>e.id===p.environment_id),active=accountIsDefault(p),disabled=accountCenterBusy?'disabled':'';
  const available=!!env&&engine?.runnable!==false&&p.kind!=='env_file';
  const kind=p.kind==='native'?'环境默认登录':p.kind==='env_file'?'环境文件引用':'账号 / API 配置';
  const changed=p.updated||p.created;const stamp=changed?new Date(changed).toLocaleString('zh-CN',{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}):'时间未记录';
  return `<article class="account-card ${active?'account-card-active':''}" data-account-card="${escapeHTML(p.id)}"><header><span class="account-engine-badge">${escapeHTML(engine?.name||p.engine)}</span>${active?'<span class="account-default-badge">新任务默认</span>':''}</header><h3>${escapeHTML(accountVisibleName(p))}</h3><div class="account-card-meta"><span>${escapeHTML(env?.type.toUpperCase()||'环境已移除')}</span><strong>${escapeHTML(engineTargetName(p.environment_id))}</strong></div><p class="account-kind">${kind}</p><details class="account-reference"><summary>配置位置</summary><code>${escapeHTML(p.kind==='native'?'继承目标环境的原生配置':p.reference)}</code></details><p class="account-updated">更新于 ${escapeHTML(stamp)}</p><div class="account-card-actions"><button type="button" data-account-activate="${escapeHTML(p.id)}" class="${active?'':'primary'}" ${disabled} ${active||!available?'disabled':''}>${active?'当前默认':'设为默认'}</button>${chosen&&available?`<button type="button" data-account-task="${escapeHTML(p.id)}" ${disabled}>当前任务使用</button>`:''}${['codex_home','claude_home'].includes(p.kind)?`<button type="button" data-account-sync="${escapeHTML(p.id)}" ${disabled}>同步到环境</button>`:''}<button type="button" data-account-rename="${escapeHTML(p.id)}" ${disabled}>重命名</button><button type="button" data-account-remove="${escapeHTML(p.id)}" ${disabled}>移除</button></div></article>`;
 }).join('')||`<div class="account-empty"><strong>${profiles.length?'没有匹配的账号':'还没有添加账号'}</strong><p>${profiles.length?'调整搜索内容或筛选条件。':'添加 Codex 登录、配置 API，或从本地账号和 Cockpit Tools JSON 导入。'}</p></div>`;
 list.querySelectorAll<HTMLButtonElement>('[data-account-activate]').forEach(b=>b.onclick=()=>void activateEngineProfile(b.dataset.accountActivate!));
 list.querySelectorAll<HTMLButtonElement>('[data-account-sync]').forEach(b=>b.onclick=()=>openCodexSync(b.dataset.accountSync!));
 list.querySelectorAll<HTMLButtonElement>('[data-account-task]').forEach(b=>b.onclick=()=>{if(accountCenterBusy)return;const profile=b.dataset.accountTask!;element<HTMLDialogElement>('account-center-dialog').close();void openHandoff(chosen,profile)});
 list.querySelectorAll<HTMLButtonElement>('[data-account-rename]').forEach(b=>b.onclick=()=>{
  if(accountCenterBusy)return;const p=profiles.find(p=>p.id===b.dataset.accountRename);if(!p)return;accountEditID=p.id;input('account-edit-name').value=p.name;element('account-edit-status').textContent='';element<HTMLDialogElement>('account-edit-dialog').showModal();input('account-edit-name').focus();
 });
 list.querySelectorAll<HTMLButtonElement>('[data-account-remove]').forEach(b=>b.onclick=()=>{
  if(accountCenterBusy)return;const p=profiles.find(p=>p.id===b.dataset.accountRemove);if(!p)return;accountEditID=p.id;element('account-remove-description').textContent='移除「'+accountVisibleName(p)+'」？';element('account-remove-status').textContent='';element<HTMLDialogElement>('account-remove-dialog').showModal();
 });
}
function setAccountMutationBusy(busy:boolean){
 accountCenterBusy=busy;renderAccountCenter();
 for(const id of ['account-edit-save','account-edit-cancel','account-remove-confirm','account-remove-cancel'])button(id).disabled=busy;
}
async function renameAccount(){
 if(accountCenterBusy)return;const profile=engineCatalog?.profiles.find(p=>p.id===accountEditID),name=input('account-edit-name').value.trim();if(!profile||!name)return;
 const epoch=shellEpoch;setAccountMutationBusy(true);element('account-edit-status').textContent='正在保存…';
 try{await api('engine-profiles/'+encodeURIComponent(profile.id),'PATCH',{name,expected_updated:profile.updated});if(!shellCurrent(epoch))return;element<HTMLDialogElement>('account-edit-dialog').close();await loadEngineSettings()}
 catch(e){if(shellCurrent(epoch))element('account-edit-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch))setAccountMutationBusy(false)}
}
async function removeAccountReference(){
 if(accountCenterBusy||!accountEditID)return;const epoch=shellEpoch,id=accountEditID;setAccountMutationBusy(true);element('account-remove-status').textContent='正在移除…';
 try{await api('engine-profiles/'+encodeURIComponent(id),'DELETE',{});if(!shellCurrent(epoch))return;element<HTMLDialogElement>('account-remove-dialog').close();invalidateModelCatalogs();await loadEngineSettings()}
 catch(e){if(shellCurrent(epoch))element('account-remove-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch))setAccountMutationBusy(false)}
}
