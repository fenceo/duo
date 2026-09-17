type UpdateInfo={current:string;repository:string;state:string;message:string;latest?:string;published?:string;checked?:number;notes?:string;releases_url?:string;release_url?:string;download_url?:string;checksum_url?:string;digest?:string;size?:number;install_supported?:boolean;install_message?:string};
type UpdateInstallResult={state:string;version:string;message:string};
let updateLoading=false,updateInformation:UpdateInfo|null=null;
function installUpdates(){
 const form=element('settings-form'),nav=form.querySelector('.settings-nav')!;
 nav.insertAdjacentHTML('beforeend','<button type="button" data-settings="updates">版本更新</button>');
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-updates" class="settings-section hidden"><div class="update-version"><div><small>当前版本</small><strong id="update-current">正在读取…</strong></div><div class="update-actions"><button type="button" id="update-install" class="primary hidden">下载并自动安装</button><button type="button" id="update-check">检查更新</button></div></div><p id="update-result" role="status"></p><div id="update-links" class="update-links"></div><details id="update-notes" class="hidden"><summary>更新说明</summary><pre id="update-notes-content"></pre></details><details class="update-source"><summary>更新来源</summary><label for="update-repository">GitHub 公开仓库</label><input id="update-repository" placeholder="用户名/jianzuo" autocomplete="off"><p>留空使用官方仓库。只查询正式发布版本，不上传任务或账号信息。</p><button type="button" id="update-source-save">保存来源</button></details><p class="update-help">自动安装只替换程序和说明文件，不改动 data 数据目录；任务结束前请先停止正在执行的任务。也可继续使用手动下载并替换。</p></section>`);
 nav.querySelectorAll<HTMLButtonElement>('button').forEach(b=>b.onclick=()=>{const page=b.dataset.settings!;nav.querySelectorAll('button').forEach(x=>x.classList.toggle('selected',x===b));for(const id of ['environment','feishu','access','updates'])element('settings-'+id).classList.toggle('hidden',id!==page);button('settings-save').classList.toggle('hidden',page==='updates');if(page==='updates')void loadUpdateInformation()});
 button('update-check').onclick=checkNewVersion;button('update-install').onclick=installNewVersion;button('update-source-save').onclick=saveUpdateSource;
}
function safeUpdateLink(url:string|undefined,repo:string):string{if(!url)return '';try{const u=new URL(url),prefix=`/${repo}/releases`.toLowerCase(),path=u.pathname.toLowerCase();return u.protocol==='https:'&&u.host==='github.com'&&!u.username&&!u.password&&!u.search&&!u.hash&&(path===prefix||path.startsWith(prefix+'/'))?u.href:''}catch{return ''}}
function setUpdateBusy(busy:boolean){
 updateLoading=busy;
 for(const id of ['update-check','update-source-save'])button(id).disabled=busy;
 button('update-install').disabled=busy||!updateInformation||updateInformation.state!=='available'||!updateInformation.install_supported||!updateInformation.download_url;
 input('update-repository').disabled=busy;
}
function renderUpdateInformation(v:UpdateInfo){
 updateInformation=v;element('update-current').textContent=v.current;input('update-repository').value=v.repository;
 element('update-result').classList.remove('error');element('update-result').textContent=v.message+(v.latest?' '+v.latest:'')+(v.checked?' · '+new Date(v.checked).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'}):'');
 const links:[[string,string|undefined],...Array<[string,string|undefined]>]=[['版本页面',v.release_url||v.releases_url]];if(v.download_url)links.unshift(['手动下载便携版'+(v.size?' · '+(v.size/1024/1024).toFixed(1)+' MB':''),v.download_url]);if(v.checksum_url)links.push(['校验文件',v.checksum_url]);
 element('update-links').innerHTML=links.map(([label,url])=>{const href=safeUpdateLink(url,v.repository);return href?`<a target="_blank" rel="noopener noreferrer" href="${escapeHTML(href)}">${escapeHTML(label)} ↗</a>`:''}).join('');
 element('update-notes').classList.toggle('hidden',!v.notes);element('update-notes-content').textContent=v.notes||'';
 const install=button('update-install'),canInstall=!!(v.install_supported&&v.download_url);
 install.classList.toggle('hidden',v.state!=='available');install.disabled=updateLoading||!canInstall;install.title=canInstall?'':v.install_message||'自动安装暂不可用，请使用手动下载。';
}
function updateFailure(e:unknown){element('update-result').textContent=(e as Error).message;element('update-result').classList.add('error')}
async function loadUpdateInformation(){if(updateLoading)return;setUpdateBusy(true);try{renderUpdateInformation(await api<UpdateInfo>('updates'))}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
async function saveUpdateSource(){if(updateLoading)return;setUpdateBusy(true);try{renderUpdateInformation(await api<UpdateInfo>('updates','PUT',{repository:input('update-repository').value}));notify('更新来源已保存')}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
async function checkNewVersion(){if(updateLoading)return;setUpdateBusy(true);element('update-result').classList.remove('error');element('update-result').textContent='正在连接 GitHub…';try{renderUpdateInformation(await api<UpdateInfo>('updates/check','POST',{}))}catch(e){updateFailure(e)}finally{setUpdateBusy(false)}}
async function installNewVersion(){
 if(updateLoading)return;
 if(!confirm('自动安装会停止正在运行的任务，然后替换程序并重启简作。是否继续？'))return;
 setUpdateBusy(true);element('update-result').classList.remove('error');element('update-result').textContent='正在下载、校验并准备更新，请勿关闭电脑…';
 try{
  const result=await api<UpdateInstallResult>('updates/install','POST',{});
  element('update-result').textContent=`已安排更新到 ${result.version}。简作即将退出，替换完成后会自动重启。`;
  notify('已安排自动更新，简作即将重启');
 }catch(e){updateFailure(e);setUpdateBusy(false)}
}
