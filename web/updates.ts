type UpdateInfo={current:string;repository:string;state:string;message:string;latest?:string;published?:string;checked?:number;notes?:string;releases_url?:string;release_url?:string;download_url?:string;checksum_url?:string;digest?:string;size?:number;install_supported?:boolean;install_message?:string;installation_mode?:'installed'|'portable'|'unmanaged';package_kind?:'installer'|'portable';installer_download_url?:string;installer_checksum_url?:string;portable_download_url?:string;portable_checksum_url?:string};
type UpdateInstallResult={state:string;version:string;message:string};
type UpdatePhase='idle'|'checking'|'preparing'|'reconnecting'|'timeout'|'failed'|'complete';
let updateLoading=false,updateInformation:UpdateInfo|null=null,updatePhase:UpdatePhase='idle',updateExpectedVersion='';
function installUpdates(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 nav.insertAdjacentHTML('beforeend','<button type="button" data-settings="updates">版本更新</button>');
 element('settings-error').insertAdjacentHTML('beforebegin','<section id="settings-updates" class="settings-section hidden"><div class="update-version"><div><small>当前版本</small><strong id="update-current">正在读取…</strong><span id="update-mode" class="update-mode">正在识别安装方式</span></div><div class="update-actions"><button type="button" id="update-install" class="primary hidden">下载并安装更新</button><button type="button" id="update-check">检查更新</button></div></div><ol class="update-steps" aria-label="更新流程"><li data-update-step="checking">检查版本</li><li data-update-step="preparing">下载与校验</li><li data-update-step="reconnecting">安装与重连</li><li data-update-step="complete">确认新版</li></ol><p id="update-result" role="status" aria-live="polite"></p><button type="button" id="update-reconnect" class="hidden">重新连接并确认版本</button><p id="update-support" class="update-support"></p><div id="update-links" class="update-links"></div><details id="update-notes" class="hidden"><summary>更新说明</summary><pre id="update-notes-content"></pre></details><details class="update-source"><summary>更新来源</summary><label for="update-repository">GitHub 公开仓库</label><input id="update-repository" placeholder="用户名/仓库" autocomplete="off"><p>留空使用官方仓库。只查询正式发布版本，不上传任务或账号信息。</p><button type="button" id="update-source-save">保存来源</button></details><p class="update-help">更新会短暂重启Duo，保留数据、任务记录、知识和配置。请先等待 AI 任务结束、关闭终端会话并完成硬件操作；有正在进行的工作时不会强停更新。安装版继续使用安装包升级，便携版保留便携更新方式。</p></section>');
 button('update-check').onclick=checkNewVersion;button('update-install').onclick=installNewVersion;button('update-source-save').onclick=saveUpdateSource;button('update-reconnect').onclick=()=>void reconnectAfterUpdate();
 setUpdateBusy(updateLoading);
}
function safeUpdateLink(url:string|undefined,repo:string):string{if(!url)return '';try{const u=new URL(url),prefix=('/'+repo+'/releases').toLowerCase(),path=u.pathname.toLowerCase();return u.protocol==='https:'&&u.host==='github.com'&&!u.username&&!u.password&&!u.search&&!u.hash&&(path===prefix||path.startsWith(prefix+'/'))?u.href:''}catch{return ''}}
function updateModeLabel(v:UpdateInfo):string{return v.installation_mode==='installed'?'Windows 安装版':v.installation_mode==='portable'?'Windows 便携版':'独立运行 · 手动安装'}
function updateLinks(v:UpdateInfo):Array<[string,string|undefined]>{
 const portable=v.package_kind==='portable'||v.installation_mode==='portable';
 const links:Array<[string,string|undefined]>=[];
 if(v.download_url)links.push([portable?'手动下载便携版 ZIP':'手动下载安装包 EXE',v.download_url]);
 else if(v.installer_download_url)links.push(['下载安装包 EXE',v.installer_download_url]);
 if(v.installer_download_url&&v.installer_download_url!==v.download_url)links.push(['安装版 EXE（推荐）',v.installer_download_url]);
 if(v.portable_download_url&&v.portable_download_url!==v.download_url)links.push(['便携版 ZIP',v.portable_download_url]);
 if(v.checksum_url)links.push(['SHA-256 校验文件',v.checksum_url]);
 links.push(['版本页面',v.release_url||v.releases_url]);return links;
}
function updateCanInstall(){const v=updateInformation;return !!(v?.state==='available'&&v.install_supported&&safeUpdateLink(v.download_url,v.repository)&&!updateExpectedVersion)}
function setUpdateBusy(busy:boolean){
 updateLoading=busy;
 for(const id of ['update-check','update-source-save','update-reconnect']){const b=document.getElementById(id) as HTMLButtonElement|null;if(b)b.disabled=busy||(id==='update-source-save'&&!!updateExpectedVersion)}
 const install=document.getElementById('update-install') as HTMLButtonElement|null;if(install)install.disabled=busy||!updateCanInstall();
 const repository=document.getElementById('update-repository') as HTMLInputElement|null;if(repository)repository.disabled=busy||!!updateExpectedVersion;
}
function setUpdatePhase(phase:UpdatePhase,message:string,error=false){
 updatePhase=phase;
 const result=document.getElementById('update-result');if(result){result.textContent=message;result.classList.toggle('error',error)}
 document.querySelectorAll<HTMLElement>('[data-update-step]').forEach(step=>{const active=step.dataset.updateStep===phase||(phase==='timeout'&&step.dataset.updateStep==='reconnecting');step.classList.toggle('active',active);if(active)step.setAttribute('aria-current','step');else step.removeAttribute('aria-current')});
 const retry=document.getElementById('update-reconnect');if(retry)retry.classList.toggle('hidden',phase!=='timeout');
 const section=document.getElementById('settings-updates');if(section)section.setAttribute('aria-busy',String(['checking','preparing','reconnecting'].includes(phase)));
}
function renderUpdateInformation(v:UpdateInfo){
 updateInformation=v;if(!document.getElementById('settings-updates'))return;
 element('update-current').textContent=v.current;element('update-mode').textContent=updateModeLabel(v);input('update-repository').value=v.repository;
 if(!updateExpectedVersion)setUpdatePhase('idle',v.message+(v.latest?' · '+v.latest:'')+(v.checked?' · '+new Date(v.checked).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'}):''));
 element('update-links').innerHTML=updateLinks(v).map(([label,url])=>{const href=safeUpdateLink(url,v.repository);return href?'<a target="_blank" rel="noopener noreferrer" href="'+escapeHTML(href)+'">'+escapeHTML(label)+' ↗</a>':''}).join('');
 element('update-notes').classList.toggle('hidden',!v.notes);element('update-notes-content').textContent=v.notes||'';
 element('update-support').textContent=v.install_message||(v.installation_mode==='installed'?'使用安装包原位升级，保留现有安装位置和数据目录。':v.installation_mode==='portable'?'在当前目录更新便携程序，数据目录保持不变。':'此运行方式暂不支持自动安装，请下载 Windows 安装包或查看版本页面。');
 const install=button('update-install');install.classList.toggle('hidden',v.state!=='available');install.title=v.install_supported?'':v.install_message||'自动安装暂不可用，请手动下载。';
 setUpdateBusy(updateLoading);
}
function updateFailure(e:unknown){
 const error=e as Error&{status?:number},message=error?.message||'连接失败，请稍后重试。';
 const prefix=error.status===401?'登录已过期，请重新登录后再试。':error.status===403?'请求未获授权，请刷新页面重新登录后再试。':error.status===409?'当前不能更新：请等待任务结束、关闭终端会话并完成硬件操作后重试。':'';
 setUpdatePhase(updateExpectedVersion?'timeout':'failed',prefix+(prefix?' ':'')+message,true);
}
async function loadUpdateInformation(){if(updateLoading)return;setUpdateBusy(true);try{renderUpdateInformation(await api<UpdateInfo>('updates'))}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
async function saveUpdateSource(){if(updateLoading||updateExpectedVersion)return;setUpdateBusy(true);try{renderUpdateInformation(await api<UpdateInfo>('updates','PUT',{repository:input('update-repository').value}));notify('更新来源已保存')}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
async function checkNewVersion(){if(updateLoading)return;setUpdateBusy(true);if(!updateExpectedVersion)setUpdatePhase('checking','正在检查 GitHub 正式发布版本…');try{renderUpdateInformation(await api<UpdateInfo>('updates/check','POST',{}))}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
function normalizedUpdateVersion(value:string):string{return /^v?\d+\.\d+\.\d+(?:-portable)?$/.test(value)?value.replace(/^v/,'').replace(/-portable$/,''):''}
async function waitForUpdatedService(expected:string,timeoutMs=180000,intervalMs=2000):Promise<{matched:boolean;lastVersion:string;status?:number}>{
 const target=normalizedUpdateVersion(expected);if(!target)return {matched:false,lastVersion:''};
 const deadline=Date.now()+timeoutMs;let lastVersion='';
 while(Date.now()<deadline){
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),Math.min(4000,Math.max(1,deadline-Date.now())));
  try{
   const response=await fetch('/healthz',{credentials:'same-origin',cache:'no-store',signal:controller.signal});
   if(response.status===401||response.status===403)return {matched:false,lastVersion,status:response.status};
   if(response.ok){const health=await response.json();if(health.app==='jianzuo'&&typeof health.version==='string'){lastVersion=health.version;if(normalizedUpdateVersion(lastVersion)===target)return {matched:true,lastVersion}}}
  }catch{/* Only a matching health response confirms success after a restart. */}
  finally{clearTimeout(timer)}
  if(Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,Math.min(intervalMs,deadline-Date.now())));
 }
 return {matched:false,lastVersion};
}
async function reconnectAfterUpdate(){
 if(updateLoading||!updateExpectedVersion)return;
 setUpdateBusy(true);setUpdatePhase('reconnecting','更新已进入安装与重连等待，正在确认 '+updateExpectedVersion+'。服务可能短暂离线；确认新版前不会显示成功。');
 try{
  const result=await waitForUpdatedService(updateExpectedVersion);
  if(result.matched){updateExpectedVersion='';setUpdatePhase('complete','已连接新版 '+result.lastVersion+'，正在刷新工作台…');location.reload();return}
  const reason=result.status===401?'访问服务需要重新登录。':result.status===403?'当前连接没有访问权限，请检查登录或代理设置。':result.lastVersion?'服务仍返回版本 '+result.lastVersion+'。':'服务暂未恢复连接。';
  setUpdatePhase('timeout',reason+' 尚未确认更新成功；可再次连接，或在服务电脑上启动Duo并查看数据目录中的 update.log。请勿重复安装或删除数据。',true);
 }catch(e){setUpdatePhase('timeout','暂时无法确认更新结果。可重新连接，或查看数据目录中的 update.log；不要重复安装。',true)}
 finally{if(updatePhase!=='complete')setUpdateBusy(false)}
}
async function installNewVersion(){
 if(updateLoading||!updateCanInstall())return;
 const expected=normalizedUpdateVersion(updateInformation?.latest||'');
 if(!expected){updateFailure(new Error('缺少可验证的目标版本，请重新检查更新。'));return}
 if(!confirm('下载并安装 '+expected+'？\n更新会短暂重启Duo并保留数据。请先等待 AI 任务结束、关闭终端会话并完成硬件操作；更新不会强停这些工作。'))return;
 setUpdateBusy(true);setUpdatePhase('preparing','正在下载、校验并准备更新。完成前不会开始安装；请勿关闭服务电脑。');
 try{
  const result=await api<UpdateInstallResult>('updates/install','POST',{});
  if(result.state!=='scheduled'||!normalizedUpdateVersion(result.version))throw Object.assign(new Error('服务未返回有效的安装安排，请重新检查更新。'),{status:502});
  updateExpectedVersion=result.version;
 }catch(e){
  if(!(e as {status?:number})?.status){updateExpectedVersion=expected;setUpdatePhase('reconnecting','安装请求的连接已中断，正在确认服务版本；尚未确认更新成功。')}
  else{updateFailure(e);setUpdateBusy(false);return}
 }
 setUpdateBusy(false);await reconnectAfterUpdate();
}
