let accountCenterEngine='',accountCenterBusy=false,accountCenterRequest=0,accountEditID='';
let accountCenterView='cards',accountCenterPrivate=false;
type CodexAccountInfo={email?:string;display_name?:string;account_id?:string;plan?:string;auth_type?:string;provider?:string;base_url?:string;subscription_until?:string;state:string;message?:string;identity_source?:string;identity_updated?:number;quota_updated?:number;attempted?:number;limits?:{name:string;used_percent:number;window_minutes:number;resets_at:number}[]};
type AccountOrganization={note:string;tags:string[]};
let accountInfoLoading=new Set<string>(),accountSelected=new Set<string>(),accountVisibleIDs:string[]=[],accountBatchBusy=false,accountOrganizationID='';
const accountStateLabels:Record<string,string>={unknown:'未读取',local:'本地身份',ready:'额度已更新',api:'API / 中转站',error:'刷新失败',login_required:'需要登录'};

function installCodexAccountInfo(){
 accountInfoLoading=new Set();accountSelected=new Set();accountVisibleIDs=[];accountBatchBusy=false;
 element('account-sort').insertAdjacentHTML('beforeend','<option value="quota">剩余额度优先</option>');
 element('account-center-dialog').querySelector('.account-center-toolbar')!.insertAdjacentHTML('beforeend','<select id="account-plan-filter" aria-label="按套餐筛选"><option value="">全部套餐</option></select><select id="account-state-filter" aria-label="按账号状态筛选"><option value="">全部状态</option>'+Object.entries(accountStateLabels).map(([v,l])=>`<option value="${v}">${l}</option>`).join('')+'</select><select id="account-tag-filter" aria-label="按标签筛选"><option value="">全部标签</option></select>');
 for(const id of ['account-plan-filter','account-state-filter','account-tag-filter'])element(id).onchange=renderAccountCenter;
 element('account-center-dialog').querySelector('.account-center-commandbar')!.insertAdjacentHTML('afterend','<div class="account-batchbar"><label><input type="checkbox" id="account-select-all"> 选择当前结果</label><span id="account-selected-count"></span><button type="button" id="account-refresh-info">刷新 Codex 账号</button><span id="account-info-progress" role="status"></span></div>');
 input('account-select-all').onchange=()=>{for(const id of accountVisibleIDs){if(input('account-select-all').checked)accountSelected.add(id);else accountSelected.delete(id)}renderAccountCenter()};
 button('account-refresh-info').onclick=()=>void refreshCodexAccounts(true);
 element('root').insertAdjacentHTML('beforeend','<dialog id="account-organization-dialog"><form id="account-organization-form"><h2>账号备注与标签</h2><label for="account-organization-note">备注</label><textarea id="account-organization-note" maxlength="1000" placeholder="例如用途、所属团队；不要填写密码或令牌"></textarea><label for="account-organization-tags">标签（逗号分隔，最多 12 个）</label><input id="account-organization-tags" maxlength="380"><p id="account-organization-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-organization-cancel">取消</button><button type="submit" id="account-organization-save" class="primary">保存</button></div></form></dialog>');
 button('account-organization-cancel').onclick=()=>{if(!button('account-organization-save').disabled)element<HTMLDialogElement>('account-organization-dialog').close()};
 element('account-organization-dialog').addEventListener('cancel',e=>{if(button('account-organization-save').disabled)e.preventDefault()});
 element('account-organization-form').onsubmit=e=>{e.preventDefault();void saveAccountOrganization()};
 disposeWithShell(()=>{accountInfoLoading.clear();accountSelected.clear();accountBatchBusy=false});
}
function updateAccountInfoFilters(){
 const options=(id:string,values:string[],label:string)=>{const e=input(id),before=e.value;e.innerHTML=`<option value="">${label}</option>`+Array.from(new Set(values)).filter(Boolean).sort().map(v=>`<option value="${escapeHTML(v)}">${escapeHTML(v)}</option>`).join('');e.value=values.includes(before)?before:''};
 options('account-plan-filter',Object.values(engineCatalog?.accounts||{}).map(a=>a.auth_type==='apikey'?'API':a.plan||''),'全部套餐');
 options('account-tag-filter',Object.values(engineCatalog?.organization||{}).flatMap(a=>a.tags||[]),'全部标签');
}
function accountSearchValues(p:EngineProfile){const i=engineCatalog?.accounts?.[p.id],o=engineCatalog?.organization?.[p.id];return [p.name||'',i?.email||'',i?.display_name||'',i?.account_id||'',i?.provider||'',o?.note||'',...(o?.tags||[]),taskEngineName(p.engine),engineTargetName(p.environment_id)]}
function accountInfoMatches(p:EngineProfile){const i=engineCatalog?.accounts?.[p.id],o=engineCatalog?.organization?.[p.id],plan=input('account-plan-filter').value,state=input('account-state-filter').value,tag=input('account-tag-filter').value;return (!plan||plan===(i?.auth_type==='apikey'?'API':i?.plan))&&(!state||state===(i?.state||'unknown'))&&(!tag||o?.tags?.includes(tag))}
function accountRemaining(p:EngineProfile){const i=engineCatalog?.accounts?.[p.id];if(i?.state!=='ready'||!i.limits?.length)return -1;return Math.min(...i.limits.map(l=>100-l.used_percent))}
function accountMask(s:string){if(!accountCenterPrivate)return s;const chars=Array.from(s);return chars.length?chars[0]+'•••'+(chars.length>2?chars.at(-1):''):'•••'}
function accountIdentityHTML(p:EngineProfile){
 const i=engineCatalog?.accounts?.[p.id],actual=i?.email||i?.display_name,alias=p.name||'';
 return (actual&&alias&&actual!==alias?`<p class="account-alias">备注名：${escapeHTML(accountMask(alias))}</p>`:'')+(i?.plan?`<span class="account-plan">${escapeHTML(i.plan.toUpperCase())}</span>`:'');
}
function accountInfoHTML(p:EngineProfile){
 const i=engineCatalog?.accounts?.[p.id],o=engineCatalog?.organization?.[p.id];let html='';
 if(p.engine==='codex'){
  html=`<section class="codex-account-info"><span class="account-state account-state-${escapeHTML(i?.state||'unknown')}">${escapeHTML(accountStateLabels[i?.state||'unknown']||'账号信息')}</span>`;
  if(i?.account_id)html+=`<p class="account-identity-id">账号 ID：${escapeHTML(accountMask(i.account_id))}</p>`;
  if(i?.auth_type==='apikey')html+=`<p>${escapeHTML(i.provider||'API 服务')}</p><code>${escapeHTML(i.base_url||'服务地址由目标环境配置')}</code>`;
  if(i?.limits?.length){
   const stale=i.state!=='ready'||!i.quota_updated||Date.now()-i.quota_updated>5*60*1000;
   if(stale)html+='<p class="account-quota-stale">上次额度快照，请刷新确认</p>';
   html+=i.limits.map(l=>{const minutes=l.window_minutes,window=minutes%10080===0?(minutes/10080)+' 周':minutes%1440===0?(minutes/1440)+' 天':minutes%60===0?(minutes/60)+' 小时':minutes+' 分钟',remaining=Math.max(0,Math.min(100,100-l.used_percent));return `<div class="account-quota"><div><strong>${escapeHTML(l.name)} · ${window}</strong><span>剩余 ${Math.round(remaining)}%</span></div><progress max="100" value="${remaining}" aria-label="${escapeHTML(l.name)} ${window}剩余额度"></progress><small>${l.resets_at>0?'重置：'+escapeHTML(new Date(l.resets_at*1000).toLocaleString('zh-CN')):'未提供重置时间'}</small></div>`}).join('');
  }
  if(i?.subscription_until)html+=`<p>本地订阅截止：${escapeHTML(new Date(i.subscription_until).toLocaleDateString('zh-CN'))}</p>`;
  html+=`<p class="account-info-message">${escapeHTML(i?.message||'正在等待身份读取；也可点击刷新账号。')}</p>`;
  if(i?.quota_updated)html+=`<small>额度更新：${escapeHTML(new Date(i.quota_updated).toLocaleString('zh-CN'))}</small>`;
  html+='</section>';
 }
 if(o?.tags?.length)html+='<div class="account-tags">'+o.tags.map(tag=>`<span>${escapeHTML(tag)}</span>`).join('')+'</div>';
 if(o?.note)html+=`<p class="account-note">${escapeHTML(accountCenterPrivate?'备注已隐藏':o.note)}</p>`;
 return html;
}
function updateAccountBatchControls(){
 const profiles=engineCatalog?.profiles||[];accountSelected=new Set(Array.from(accountSelected).filter(id=>profiles.some(p=>p.id===id)));
 input('account-select-all').checked=accountVisibleIDs.length>0&&accountVisibleIDs.every(id=>accountSelected.has(id));
 input('account-select-all').indeterminate=accountVisibleIDs.some(id=>accountSelected.has(id))&&!input('account-select-all').checked;
 element('account-selected-count').textContent=accountSelected.size?`已选择 ${accountSelected.size} 个`:'';
 button('account-refresh-info').textContent=accountSelected.size?'刷新选中的 Codex 账号':'刷新 Codex 账号';button('account-refresh-info').disabled=accountBatchBusy;
}
function bindAccountInfoActions(){
 element('account-center-list').querySelectorAll<HTMLInputElement>('[data-account-select]').forEach(e=>e.onchange=()=>{if(e.checked)accountSelected.add(e.dataset.accountSelect!);else accountSelected.delete(e.dataset.accountSelect!);updateAccountBatchControls()});
 element('account-center-list').querySelectorAll<HTMLButtonElement>('[data-account-refresh]').forEach(e=>e.onclick=()=>void refreshCodexAccount(e.dataset.accountRefresh!,true));
 element('account-center-list').querySelectorAll<HTMLButtonElement>('[data-account-organize]').forEach(e=>e.onclick=()=>{accountOrganizationID=e.dataset.accountOrganize!;const value=engineCatalog?.organization?.[accountOrganizationID];input('account-organization-note').value=value?.note||'';input('account-organization-tags').value=(value?.tags||[]).join(', ');element('account-organization-status').textContent='';element<HTMLDialogElement>('account-organization-dialog').showModal()});
}
function accountRouteSignature(p:EngineProfile){return JSON.stringify([p.id,p.created,p.environment_id,p.engine,p.kind,p.reference,settings.config.environments.find(e=>e.id===p.environment_id)])}
async function refreshCodexAccount(id:string,online:boolean){
 const profile=engineCatalog?.profiles.find(p=>p.id===id);if(!profile||profile.engine!=='codex'||accountInfoLoading.has(id))return;
 const epoch=shellEpoch,signature=accountRouteSignature(profile);accountInfoLoading.add(id);renderAccountCenter();
 try{
  const info=await api<CodexAccountInfo>('engine-profiles/'+encodeURIComponent(id)+'/account/refresh','POST',{online},shellController.signal);
  const current=engineCatalog?.profiles.find(p=>p.id===id);if(!shellCurrent(epoch)||!current||accountRouteSignature(current)!==signature)return;
  engineCatalog!.accounts??={};engineCatalog!.accounts[id]=info;
 }catch(e){if(shellCurrent(epoch)){const current=engineCatalog?.profiles.find(p=>p.id===id);if(current&&accountRouteSignature(current)===signature){engineCatalog!.accounts??={};engineCatalog!.accounts[id]={...(engineCatalog!.accounts[id]||{}),state:'error',message:(e as Error).message}}}}
 finally{if(shellCurrent(epoch)){accountInfoLoading.delete(id);renderAccountCenter()}}
}
async function refreshCodexAccounts(online:boolean,missingOnly=false){
 if(accountBatchBusy||!engineCatalog)return;const epoch=shellEpoch;
 const profiles=engineCatalog.profiles.filter(p=>p.engine==='codex'&&(!online||!accountSelected.size||accountSelected.has(p.id))&&(!missingOnly||!engineCatalog!.accounts?.[p.id]?.identity_updated||Date.now()-(engineCatalog!.accounts?.[p.id]?.identity_updated||0)>60000));
 if(!profiles.length)return;accountBatchBusy=true;updateAccountBatchControls();let index=0,finished=0;
 const worker=async()=>{while(index<profiles.length&&shellCurrent(epoch)){const p=profiles[index++];await refreshCodexAccount(p.id,online);finished++;if(shellCurrent(epoch))element('account-info-progress').textContent=`${online?'刷新账号':'读取身份'} ${finished} / ${profiles.length}`}};
 try{await Promise.all(online?[worker()]:[worker(),worker()])}finally{if(shellCurrent(epoch)){accountBatchBusy=false;updateAccountBatchControls()}}
}
async function saveAccountOrganization(){
 if(button('account-organization-save').disabled)return;const id=accountOrganizationID,epoch=shellEpoch;
 const body={note:input('account-organization-note').value.trim(),tags:input('account-organization-tags').value.split(/[,，]/).map(v=>v.trim()).filter(Boolean)};
 button('account-organization-save').disabled=true;
 try{const result=await api<AccountOrganization>('engine-profiles/'+encodeURIComponent(id)+'/organization','PATCH',body);if(!shellCurrent(epoch))return;engineCatalog!.organization??={};engineCatalog!.organization[id]=result;element<HTMLDialogElement>('account-organization-dialog').close();renderAccountCenter()}
 catch(e){if(shellCurrent(epoch))element('account-organization-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch))button('account-organization-save').disabled=false}
}

function installAccountCenter(){
 accountCenterEngine='';accountCenterBusy=false;accountEditID='';accountCenterRequest++;
 accountCenterView=localStorage.getItem('duo.accountView')==='list'?'list':'cards';
 accountCenterPrivate=localStorage.getItem('duo.accountPrivate')==='1';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="account-center-dialog" aria-labelledby="account-center-title"><header class="account-center-heading"><div><h2 id="account-center-title">账号管理</h2><p>集中管理账号与 API 配置，按引擎和环境快速切换。</p></div><button type="button" id="account-center-close">关闭</button></header><nav id="account-engine-tabs" class="account-engine-tabs" aria-label="账号引擎"></nav><div class="account-center-toolbar"><input id="account-search" type="search" placeholder="搜索账号名称、引擎或环境" aria-label="搜索账号"><select id="account-environment-filter" aria-label="按环境筛选"><option value="">全部环境</option></select><select id="account-default-filter" aria-label="按默认状态筛选"><option value="">全部账号</option><option value="default">环境当前账号</option><option value="other">其他账号</option></select><select id="account-sort" aria-label="账号排序"><option value="updated">最近更新</option><option value="created">创建时间</option><option value="name">名称排序</option></select><div class="account-view-controls"><button type="button" id="account-view-cards" aria-pressed="true">卡片</button><button type="button" id="account-view-list" aria-pressed="false">列表</button><button type="button" id="account-privacy" aria-pressed="false">隐藏名称</button></div></div><div class="account-center-commandbar"><p id="account-center-summary" role="status"></p><div class="actions"><button type="button" id="account-center-refresh">刷新列表</button><button type="button" id="account-center-import">导入账号</button><button type="button" id="account-center-manage" class="primary">＋ 添加账号</button></div></div><p class="account-center-hint">切换环境账号：此环境同一引擎的跟随任务从下一轮统一生效。“仅此任务使用”可单独指定；“同步到环境”同时写入 Windows / WSL / SSH 的原生登录。</p><div id="account-center-list"></div><details id="account-center-editor-wrap"><summary>添加、导入与配置引用</summary><div id="account-center-editor"></div></details><div id="engine-account-status" role="status"></div><button type="button" id="engine-account-cancel" class="hidden">取消当前操作</button><p id="account-center-status" role="status"></p></dialog><dialog id="account-edit-dialog" aria-labelledby="account-edit-title"><form id="account-edit-form"><h2 id="account-edit-title">重命名账号</h2><label for="account-edit-name">账号名称</label><input id="account-edit-name" maxlength="60" required autocomplete="off"><p id="account-edit-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-edit-cancel">取消</button><button type="submit" id="account-edit-save" class="primary">保存名称</button></div></form></dialog><dialog id="account-remove-dialog" aria-labelledby="account-remove-title"><h2 id="account-remove-title">移除账号引用</h2><p id="account-remove-description"></p><p>仅从 Duo 账号列表移除，保留原生账号文件和已有任务；若它是环境当前账号，跟随任务下一轮恢复使用原生默认登录；单独指定账号的任务继续使用原目录。</p><p id="account-remove-status" role="status"></p><div class="dialog-footer"><button type="button" id="account-remove-cancel">取消</button><button type="button" id="account-remove-confirm" class="danger">移除引用</button></div></dialog>`);
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
 installCodexAccountInfo();
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
  engineCatalog=catalog;renderAccountCenter();populateEngineProfileForm();populateEngineOnboarding();void resumeEngineSetup();element('account-center-status').textContent='';if(catalog.accounts)void refreshCodexAccounts(false,true);
 }catch(e){if(shellCurrent(epoch)&&request===accountCenterRequest)element('account-center-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===accountCenterRequest)button('account-center-refresh').disabled=false}
}
function accountIsDefault(profile:EngineProfile){return engineCatalog?.active_profile[profile.environment_id+':'+profile.engine]===profile.id}
function accountVisibleName(profile:EngineProfile){
 const info=engineCatalog?.accounts?.[profile.id],label=info?.email||info?.display_name||profile.name||'未命名账号';
 if(!accountCenterPrivate)return label;
 const name=Array.from(label);return name.length?name[0]+'•••'+(name.length>2?name[name.length-1]:''):'•••';
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
 updateAccountInfoFilters();
 const query=input('account-search').value.trim().toLocaleLowerCase(),filter=input('account-default-filter').value,sort=input('account-sort').value;
 const visible=profiles.filter(p=>(!accountCenterEngine||p.engine===accountCenterEngine)&&(!select.value||p.environment_id===select.value)&&(!filter||accountIsDefault(p)===(filter==='default'))&&accountInfoMatches(p)&&(!query||accountSearchValues(p).some(v=>v.toLocaleLowerCase().includes(query))));
 accountVisibleIDs=visible.map(p=>p.id);
 visible.sort((a,b)=>sort==='quota'?accountRemaining(b)-accountRemaining(a):sort==='name'?accountVisibleName(a).localeCompare(accountVisibleName(b),'zh-CN'):(sort==='created'?(b.created||0)-(a.created||0):(b.updated||0)-(a.updated||0))||(a.name||a.id).localeCompare(b.name||b.id,'zh-CN'));
 element('account-center-summary').textContent=`显示 ${visible.length} / ${profiles.length} 个账号 · ${profiles.filter(accountIsDefault).length} 个环境当前账号`;
 for(const view of ['cards','list'])button('account-view-'+view).setAttribute('aria-pressed',String(accountCenterView===view));
 button('account-privacy').setAttribute('aria-pressed',String(accountCenterPrivate));button('account-privacy').textContent=accountCenterPrivate?'显示名称':'隐藏名称';
 updateAccountBatchControls();
 const list=element('account-center-list');list.className='account-grid'+(accountCenterView==='list'?' account-list-view':'');
 list.innerHTML=visible.map(p=>{
  const engine=engines.find(e=>e.id===p.engine),env=settings.config.environments.find(e=>e.id===p.environment_id),active=accountIsDefault(p),disabled=accountCenterBusy?'disabled':'';
  const available=!!env&&engine?.runnable!==false&&p.kind!=='env_file';
  const kind=p.kind==='native'?'环境默认登录':p.kind==='env_file'?'环境文件引用':'账号 / API 配置';
  const changed=p.updated||p.created;const stamp=changed?new Date(changed).toLocaleString('zh-CN',{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}):'时间未记录';
  return `<article class="account-card ${active?'account-card-active':''}" data-account-card="${escapeHTML(p.id)}"><header><label class="account-pick"><input type="checkbox" data-account-select="${escapeHTML(p.id)}" aria-label="选择账号" ${accountSelected.has(p.id)?'checked':''}></label><span class="account-engine-badge">${escapeHTML(engine?.name||p.engine)}</span>${active?'<span class="account-default-badge">环境当前账号</span>':''}</header><h3>${escapeHTML(accountVisibleName(p))}</h3>${accountIdentityHTML(p)}<div class="account-card-meta"><span>${escapeHTML(env?.type?.toUpperCase()||'环境已移除')}</span><strong>${escapeHTML(engineTargetName(p.environment_id))}</strong></div><p class="account-kind">${kind}</p>${accountInfoHTML(p)}<details class="account-reference"><summary>配置位置</summary><code>${escapeHTML(p.kind==='native'?'继承目标环境的原生配置':p.reference||'未记录配置位置')}</code></details><p class="account-updated">更新于 ${escapeHTML(stamp)}</p><div class="account-card-actions">${p.engine==='codex'?`<button type="button" data-account-refresh="${escapeHTML(p.id)}" ${accountInfoLoading.has(p.id)?'disabled':''}>${accountInfoLoading.has(p.id)?'刷新中…':'刷新账号'}</button>`:''}<button type="button" data-account-organize="${escapeHTML(p.id)}">备注 / 标签</button><button type="button" data-account-activate="${escapeHTML(p.id)}" class="${active?'':'primary'}" ${disabled} ${active||!available?'disabled':''}>${active?'环境当前账号':'切换环境账号'}</button>${chosen&&available?`<button type="button" data-account-task="${escapeHTML(p.id)}" ${disabled}>仅此任务使用</button>`:''}${['codex_home','claude_home'].includes(p.kind)?`<button type="button" data-account-sync="${escapeHTML(p.id)}" ${disabled}>同步到环境</button>`:''}<button type="button" data-account-rename="${escapeHTML(p.id)}" ${disabled}>重命名</button><button type="button" data-account-remove="${escapeHTML(p.id)}" ${disabled}>移除</button></div></article>`;
 }).join('')||`<div class="account-empty"><strong>${profiles.length?'没有匹配的账号':'还没有添加账号'}</strong><p>${profiles.length?'调整搜索内容或筛选条件。':'添加 Codex 登录、配置 API，或从本地账号和 Cockpit Tools JSON 导入。'}</p></div>`;
 bindAccountInfoActions();
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
