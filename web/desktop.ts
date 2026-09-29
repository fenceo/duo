type DesktopState={active:boolean;task_id?:string;target?:string;control:boolean;expires?:number};
type DesktopTarget={id:string;title:string};
let desktopState:DesktopState={active:false,control:false},desktopDialogTask='',desktopBusy=false,desktopRevision=0;
function installDesktopSharing(){
 desktopState={active:false,control:false};desktopDialogTask='';desktopBusy=false;desktopRevision++;
 const entry=document.createElement('button');entry.type='button';entry.id='desktop-open';entry.className='subtle';entry.textContent='桌面共享';element('settings-open').before(entry);entry.onclick=()=>void openDesktopSharing();
 element('root').insertAdjacentHTML('beforeend',`<dialog id="desktop-dialog" class="desktop-dialog"><h2>共享服务电脑的桌面</h2><p>共享的是运行 Duo 的 Windows 桌面。选定窗口需保持前台；整个桌面可能包含其它应用内容。画面由当前任务的 AI 查看。</p><p id="desktop-owner" role="status"></p><div class="desktop-controls"><label for="desktop-target">共享范围</label><select id="desktop-target"></select><label class="check-row"><input type="checkbox" id="desktop-allow-control">允许 AI 操作真实键盘和鼠标</label><p class="muted">默认只查看。授权保持 30 分钟，重启服务后失效；可随时停止共享。开始共享后，从下一条任务消息生效。</p><div class="actions"><button type="button" id="desktop-start" class="primary">开始共享给当前任务</button><button type="button" id="desktop-preview">刷新画面</button><button type="button" id="desktop-stop" class="danger">停止共享 / 接管</button></div></div><p id="desktop-status" role="status"></p><img id="desktop-preview-image" class="hidden" alt="用户授权共享的桌面画面"><div class="dialog-footer"><button type="button" id="desktop-close">关闭面板</button></div></dialog>`);
 button('desktop-close').onclick=()=>element<HTMLDialogElement>('desktop-dialog').close();
 button('desktop-start').onclick=()=>void startDesktopSharing();button('desktop-stop').onclick=()=>void stopDesktopSharing();button('desktop-preview').onclick=()=>void previewDesktop();
}
function renderDesktopSharing(state?:DesktopState){
 if(state){desktopState=state;if(!state.active){const img=element<HTMLImageElement>('desktop-preview-image');img?.removeAttribute('src');img?.classList.add('hidden')}}
 const active=desktopState.active;button('desktop-open').textContent=active?'桌面共享中 · 接管':'桌面共享';
 button('desktop-open').classList.toggle('desktop-sharing',active);
 const task=tasks.find(t=>t.id===desktopState.task_id),label=task?.title||desktopState.task_id||'';
 element('desktop-owner').textContent=active?'正在共享给 '+label+' · '+(desktopState.control?'允许键鼠控制':'仅查看'):'当前未共享桌面';
 button('desktop-stop').disabled=!active;button('desktop-preview').disabled=desktopBusy||!active||desktopState.task_id!==desktopDialogTask;
 button('desktop-start').disabled=desktopBusy||!desktopDialogTask||desktopDialogTask!==chosen||!['codex','claude'].includes(detail?.task.engine||'')||!!detail?.task.archived||active&&desktopState.task_id!==desktopDialogTask;
}
async function openDesktopSharing(){
 const epoch=shellEpoch,revision=++desktopRevision;desktopDialogTask=chosen;desktopBusy=true;input('desktop-allow-control').checked=false;
 element('desktop-status').textContent='正在读取桌面和窗口列表…';const img=element<HTMLImageElement>('desktop-preview-image');img.removeAttribute('src');img.classList.add('hidden');element<HTMLDialogElement>('desktop-dialog').showModal();renderDesktopSharing();
 try{
  const [stateResult,targetsResult]=await Promise.allSettled([api<DesktopState>('desktop'),api<DesktopTarget[]>('desktop/targets')]);if(!shellCurrent(epoch)||revision!==desktopRevision)return;
  if(stateResult.status==='fulfilled'){desktopState=stateResult.value;renderDesktopSharing()}
  if(stateResult.status==='rejected')throw stateResult.reason;
  if(targetsResult.status==='rejected')throw targetsResult.reason;
  const state=stateResult.value,targets=targetsResult.value;
  element<HTMLSelectElement>('desktop-target').innerHTML=targets.map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');
  desktopState=state;element('desktop-status').textContent=chosen?'请确认共享范围和是否允许键鼠操作。':'先选择一个 Codex 或 Claude 任务，才能开始共享。';
 }catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function startDesktopSharing(){
 if(desktopBusy||!desktopDialogTask||desktopDialogTask!==chosen)return;
 const task=desktopDialogTask,epoch=shellEpoch,revision=++desktopRevision;desktopBusy=true;renderDesktopSharing();
 try{const state=await api<DesktopState>('tasks/'+encodeURIComponent(task)+'/desktop','PUT',{target:element<HTMLSelectElement>('desktop-target').value,control:input('desktop-allow-control').checked});if(!shellCurrent(epoch)||revision!==desktopRevision)return;renderDesktopSharing(state);element('desktop-status').textContent='已开始共享。发送下一条任务消息，AI 即可使用桌面工具。用户操作后，AI 必须重新观察画面。'}catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function stopDesktopSharing(){
 const epoch=shellEpoch,revision=++desktopRevision;button('desktop-stop').disabled=true;
 try{await api('desktop','DELETE');if(!shellCurrent(epoch)||revision!==desktopRevision)return;desktopBusy=false;renderDesktopSharing({active:false,control:false});element('desktop-status').textContent='共享已停止，AI 已失去桌面访问权限。'}catch(e){if(shellCurrent(epoch)&&revision===desktopRevision){element('desktop-status').textContent='停止失败，请重试：'+(e as Error).message;button('desktop-stop').disabled=false}}
}
async function previewDesktop(){
 if(desktopBusy||!desktopState.active||desktopState.task_id!==desktopDialogTask)return;
 const task=desktopDialogTask,epoch=shellEpoch,revision=++desktopRevision;desktopBusy=true;renderDesktopSharing();
 try{const frame=await api<{image:string}>('tasks/'+encodeURIComponent(task)+'/desktop/frame','POST',{});if(!shellCurrent(epoch)||revision!==desktopRevision||desktopState.task_id!==task)return;const img=element<HTMLImageElement>('desktop-preview-image');img.src='data:image/png;base64,'+frame.image;img.classList.remove('hidden');element('desktop-status').textContent='画面已刷新；此预览不会连续录屏。'}catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
