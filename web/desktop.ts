type DesktopState={instance?:string;revision?:number;active:boolean;task_id?:string;target?:string;control:boolean;human_control?:boolean;expires?:number};
type DesktopTarget={id:string;title:string};
type DesktopFrame={id:string;image:string;bounds:{x:number;y:number;width:number;height:number}};
let desktopHumanToken='',desktopFrame:DesktopFrame|null=null,desktopPreviewTimer=0,desktopCapturing=false;
function clearDesktopHuman(){clearTimeout(desktopPreviewTimer);desktopHumanToken='';desktopFrame=null;desktopCapturing=false}
function renderDesktopHuman(){
 if(!button('desktop-manual'))return;
 const manual=!!desktopHumanToken,ready=manual&&!!desktopFrame&&(!desktopBusy||desktopCapturing);
 button('desktop-manual').textContent=manual?'结束手动控制':'我来操作';button('desktop-manual').disabled=!manual&&(desktopBusy||!desktopState.active||desktopState.task_id!==desktopDialogTask||desktopDialogTask!==chosen||!!detail?.task.archived);
 element('desktop-manual-tools').classList.toggle('hidden',!manual);
 element('desktop-manual-tools').querySelectorAll<HTMLButtonElement>('button').forEach(b=>b.disabled=!ready);
 const img=element<HTMLImageElement>('desktop-preview-image');img.classList.toggle('desktop-interactive',ready);img.setAttribute('aria-disabled',String(!ready));
}
function installDesktopHuman(){
 button('desktop-manual').onclick=()=>void toggleDesktopHuman();
 const dialog=element<HTMLDialogElement>('desktop-dialog'),img=element<HTMLImageElement>('desktop-preview-image');
 dialog.addEventListener('close',()=>{void endDesktopHuman();desktopRevision++;desktopBusy=false;desktopFrame=null;img.removeAttribute('src');img.classList.add('hidden')});
 dialog.addEventListener('cancel',event=>{if(desktopHumanToken){event.preventDefault();void endDesktopHuman()}});
 let pointerStart:{x:number;y:number}|null=null,dragged=false;
 img.onpointerdown=event=>{pointerStart={x:event.clientX,y:event.clientY};dragged=false};
 img.onpointermove=event=>{if(pointerStart&&event.buttons&&Math.hypot(event.clientX-pointerStart.x,event.clientY-pointerStart.y)>8)dragged=true};
 img.onclick=event=>{if(dragged){dragged=false;element('desktop-status').textContent='暂不支持拖拽，本次未发送点击。';return}if(event.button===0&&event.detail<=1)void desktopPointerAction(input('desktop-click-mode').value,event)};
 img.oncontextmenu=event=>{if(desktopHumanToken){event.preventDefault();void desktopPointerAction('right_click',event)}};
 img.addEventListener('wheel',event=>{if(desktopHumanToken){event.preventDefault();if(event.deltaY)void desktopPointerAction('scroll',event,-Math.sign(event.deltaY)*3)}},{passive:false});
 img.ondragstart=event=>event.preventDefault();
 img.onkeydown=event=>{
  if(!desktopHumanToken||event.isComposing)return;
  event.preventDefault();if(event.repeat)return;
  if(event.key==='Escape'){void endDesktopHuman();return}
  const named:Record<string,string>={Enter:'enter',Tab:'tab',Backspace:'backspace',Delete:'delete',ArrowUp:'up',ArrowDown:'down',ArrowLeft:'left',ArrowRight:'right',Home:'home',End:'end',PageUp:'pageup',PageDown:'pagedown',' ':'space'};
  if(event.key.length===1&&!event.ctrlKey&&!event.altKey&&!event.metaKey){void desktopHumanAction({action:'type',text:event.key});return}
  const key=named[event.key]||(/^[a-z0-9]$/i.test(event.key)?event.key.toLowerCase():'');if(!key)return;
  const mods=[event.ctrlKey?'ctrl':'',event.altKey?'alt':'',event.shiftKey?'shift':'',event.metaKey?'win':''].filter(Boolean);void desktopHumanAction({action:'key',key:[...mods,key].join('+')});
 };
 element('desktop-manual-tools').querySelectorAll<HTMLButtonElement>('[data-desktop-key]').forEach(b=>b.onclick=()=>void desktopHumanAction({action:'key',key:b.dataset.desktopKey}));
 button('desktop-text-send').onclick=()=>{const text=input('desktop-text').value;if(text)void desktopHumanAction({action:'type',text})};
 listenWithShell(document,'visibilitychange',()=>{if(document.hidden)void endDesktopHuman()});
 disposeWithShell(()=>{void endDesktopHuman();clearTimeout(desktopPreviewTimer)});
}
async function toggleDesktopHuman(){
 if(desktopHumanToken){await endDesktopHuman();return}
 if(desktopBusy||!desktopState.active||desktopState.task_id!==desktopDialogTask)return;
 const task=desktopDialogTask,epoch=shellEpoch,revision=++desktopRevision;desktopBusy=true;renderDesktopSharing();
 try{
  const result=await api<{token:string;state:DesktopState}>(`tasks/${encodeURIComponent(task)}/desktop/control`,'POST',{enabled:true});
  if(!shellCurrent(epoch)||revision!==desktopRevision||!element<HTMLDialogElement>('desktop-dialog').open){void api(`tasks/${encodeURIComponent(task)}/desktop/control`,'POST',{enabled:false,token:result.token}).catch(()=>{});return}
  desktopFrame=null;desktopBusy=false;renderDesktopSharing(result.state);desktopHumanToken=result.token;renderDesktopHuman();await previewDesktop();
 }catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function endDesktopHuman(){
 const task=desktopDialogTask,token=desktopHumanToken,epoch=shellEpoch;clearDesktopHuman();
 if(!token)return;
 const revision=++desktopRevision;desktopBusy=false;renderDesktopHuman();
 try{const state=await api<DesktopState>(`tasks/${encodeURIComponent(task)}/desktop/control`,'POST',{enabled:false,token});if(shellCurrent(epoch)&&revision===desktopRevision){renderDesktopSharing(state);element('desktop-status').textContent='手动控制已结束，AI 可重新观察后继续。'}}
 catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent='未确认结束手动控制，可停止共享；无刷新 60 秒后自动释放。'+(e as Error).message}
}
function desktopImagePoint(event:{clientX:number;clientY:number},img:HTMLImageElement,frame:DesktopFrame){
 const rect=img.getBoundingClientRect(),left=rect.left+img.clientLeft,top=rect.top+img.clientTop,width=img.clientWidth,height=img.clientHeight;
 if(width<=0||height<=0||event.clientX<left||event.clientY<top||event.clientX>=left+width||event.clientY>=top+height)return null;
 return {x:Math.min(frame.bounds.width-1,Math.floor((event.clientX-left)*frame.bounds.width/width)),y:Math.min(frame.bounds.height-1,Math.floor((event.clientY-top)*frame.bounds.height/height))};
}
async function desktopPointerAction(action:string,event:{clientX:number;clientY:number},delta?:number){
 if(!desktopHumanToken||!desktopFrame||desktopBusy&&!desktopCapturing)return;
 const point=desktopImagePoint(event,element<HTMLImageElement>('desktop-preview-image'),desktopFrame);if(!point)return;
 await desktopHumanAction({action,...point,...(delta===undefined?{}:{delta})});
}
async function desktopHumanAction(action:Record<string,unknown>){
 if(!desktopHumanToken||!desktopFrame||desktopBusy&&!desktopCapturing)return;
 const task=desktopDialogTask,token=desktopHumanToken,frame=desktopFrame.id,epoch=shellEpoch,revision=++desktopRevision;
 clearTimeout(desktopPreviewTimer);desktopFrame=null;desktopCapturing=false;desktopBusy=true;renderDesktopSharing();
 try{
  await api(`tasks/${encodeURIComponent(task)}/desktop/control/action`,'POST',{token,action:{...action,frame}});
  if(!shellCurrent(epoch)||revision!==desktopRevision)return;
  desktopBusy=false;await previewDesktop();
 }catch(e){if(shellCurrent(epoch)&&revision===desktopRevision){element('desktop-status').textContent='操作未确认，已暂停自动刷新；请检查实际桌面并刷新画面，不会自动重试。'+(e as Error).message}}
 finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function previewDesktop(){
 if(desktopBusy||!desktopState.active||desktopState.task_id!==desktopDialogTask)return;
 clearTimeout(desktopPreviewTimer);
 const task=desktopDialogTask,token=desktopHumanToken,epoch=shellEpoch,revision=++desktopRevision;desktopBusy=true;desktopCapturing=true;renderDesktopSharing();
 try{
  const frame=await api<DesktopFrame>(`tasks/${encodeURIComponent(task)}/desktop/${token?'control/frame':'frame'}`,'POST',token?{token}:{});
  if(!shellCurrent(epoch)||revision!==desktopRevision||desktopState.task_id!==task||token!==desktopHumanToken)return;
  const img=element<HTMLImageElement>('desktop-preview-image');img.src='data:image/png;base64,'+frame.image;
  if(typeof img.decode==='function')await img.decode();
  if(!shellCurrent(epoch)||revision!==desktopRevision||token!==desktopHumanToken)return;
  img.classList.remove('hidden');desktopFrame=frame;
  element('desktop-status').textContent=token?'手动控制已开启：可点击画面操作，约每秒刷新；AI 暂停桌面操作。':'当前是只读预览。点击“我来操作”后，可在画面上使用鼠标。';
  if(token&&!document.hidden&&element<HTMLDialogElement>('desktop-dialog').open)desktopPreviewTimer=setTimeout(()=>{void previewDesktop()},1200);
 }catch(e){if(shellCurrent(epoch)&&revision===desktopRevision){desktopFrame=null;element('desktop-status').textContent='画面读取失败，请手动刷新：'+(e as Error).message}}
 finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;desktopCapturing=false;renderDesktopSharing()}}
}
let desktopState:DesktopState={active:false,control:false},desktopDialogTask='',desktopBusy=false,desktopRevision=0;
function installDesktopSharing(){
 desktopState={active:false,control:false};desktopDialogTask='';desktopBusy=false;desktopRevision++;clearDesktopHuman();
 const entry=document.createElement('button');entry.type='button';entry.id='desktop-open';entry.className='subtle';entry.textContent='桌面共享';element('settings-open').before(entry);entry.onclick=()=>void openDesktopSharing();
 element('root').insertAdjacentHTML('beforeend',`<dialog id="desktop-dialog" class="desktop-dialog"><h2>共享服务电脑的桌面</h2><p>共享的是运行 Duo 的 Windows 桌面。选定窗口需保持前台；整个桌面可能包含其它应用内容。画面由当前任务的 AI 查看。</p><p id="desktop-owner" role="status"></p><div class="desktop-controls"><label for="desktop-target">共享范围</label><select id="desktop-target"></select><label class="check-row"><input type="checkbox" id="desktop-allow-control">允许 AI 操作真实键盘和鼠标</label><p class="muted">默认只查看。授权保持 30 分钟，重启服务后失效；可随时停止共享。开始共享后，从下一条任务消息生效。</p><div class="actions"><button type="button" id="desktop-start" class="primary">开始共享给当前任务</button><button type="button" id="desktop-preview">刷新画面</button><button type="button" id="desktop-manual">我来操作</button><button type="button" id="desktop-stop" class="danger">停止共享 / 接管</button></div></div><p id="desktop-status" role="status"></p><div id="desktop-manual-tools" class="hidden"><p>人工控制中，AI 暂停桌面操作。点击画面可定位，右键和滚轮直接发送；选择“双击”后点击一次。暂不支持拖拽。按 Escape 结束手动控制。</p><div class="actions"><select id="desktop-click-mode" aria-label="鼠标左键动作"><option value="click">单击</option><option value="double_click">双击</option></select><button type="button" data-desktop-key="enter">Enter</button><button type="button" data-desktop-key="tab">Tab</button><button type="button" data-desktop-key="ctrl+c">Ctrl+C</button><button type="button" data-desktop-key="alt+tab">Alt+Tab</button></div><label for="desktop-text">发送文字（支持中文）</label><div class="desktop-text-send"><textarea id="desktop-text" rows="2" maxlength="2000"></textarea><button type="button" id="desktop-text-send">发送文字</button></div></div><img id="desktop-preview-image" class="hidden" tabindex="0" draggable="false" alt="共享桌面预览；开启手动控制后可使用鼠标和键盘"><div class="dialog-footer"><button type="button" id="desktop-close">关闭面板</button></div></dialog>`);
 button('desktop-close').onclick=()=>element<HTMLDialogElement>('desktop-dialog').close();installDesktopHuman();
 button('desktop-start').onclick=()=>void startDesktopSharing();button('desktop-stop').onclick=()=>void stopDesktopSharing();button('desktop-preview').onclick=()=>void previewDesktop();
}
function renderDesktopSharing(state?:DesktopState){
 if(state&&state.instance===desktopState.instance&&(state.revision||0)<(desktopState.revision||0))state=undefined;
 if(state){if(desktopHumanToken&&(!state.active||!state.human_control||state.task_id!==desktopDialogTask||state.instance!==desktopState.instance))clearDesktopHuman();desktopState=state;if(!state.active){const img=element<HTMLImageElement>('desktop-preview-image');img?.removeAttribute('src');img?.classList.add('hidden')}}
 renderDesktopHuman();
 const active=desktopState.active;button('desktop-open').textContent=active?'桌面接管':'桌面共享';button('desktop-open').title=active?'桌面正在共享，打开面板停止共享或接管':'选择画面并授权当前任务查看或控制桌面';
 button('desktop-open').classList.toggle('desktop-sharing',active);
 const task=tasks.find(t=>t.id===desktopState.task_id),label=task?.title||desktopState.task_id||'';
 element('desktop-owner').textContent=active?'正在共享给 '+label+' · '+(desktopState.human_control?'用户正在手动控制 · AI 暂停':desktopState.control?'允许 AI 键鼠控制':'AI 仅查看'):'当前未共享桌面';
 button('desktop-stop').disabled=!active;button('desktop-preview').disabled=desktopBusy||!active||desktopState.task_id!==desktopDialogTask;
 button('desktop-start').disabled=desktopBusy||!desktopDialogTask||desktopDialogTask!==chosen||!['codex','claude'].includes(detail?.task.engine||'')||!!detail?.task.archived||active&&desktopState.task_id!==desktopDialogTask;
}
async function openDesktopSharing(){
 const epoch=shellEpoch,revision=++desktopRevision;desktopDialogTask=chosen;desktopBusy=true;desktopFrame=null;input('desktop-allow-control').checked=false;
 element('desktop-status').textContent='正在读取桌面和窗口列表…';const img=element<HTMLImageElement>('desktop-preview-image');img.removeAttribute('src');img.classList.add('hidden');element<HTMLDialogElement>('desktop-dialog').showModal();renderDesktopSharing();
 try{
  const [stateResult,targetsResult]=await Promise.allSettled([api<DesktopState>('desktop'),api<DesktopTarget[]>('desktop/targets')]);if(!shellCurrent(epoch)||revision!==desktopRevision)return;
  if(stateResult.status==='fulfilled'){desktopState=stateResult.value;renderDesktopSharing()}
  if(stateResult.status==='rejected')throw stateResult.reason;
  if(targetsResult.status==='rejected')throw targetsResult.reason;
  const state=stateResult.value,targets=targetsResult.value;
  element<HTMLSelectElement>('desktop-target').innerHTML=targets.map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');
	if(state.active&&state.task_id===desktopDialogTask){input('desktop-allow-control').checked=state.control;if(targets.some(t=>t.id===state.target))input('desktop-target').value=state.target!}
  desktopState=state;element('desktop-status').textContent=chosen?'请确认共享范围和是否允许键鼠操作。':'先选择一个 Codex 或 Claude 任务，才能开始共享。';
 }catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function startDesktopSharing(){
 if(desktopBusy||!desktopDialogTask||desktopDialogTask!==chosen)return;
 const task=desktopDialogTask,epoch=shellEpoch,revision=++desktopRevision;clearDesktopHuman();desktopFrame=null;desktopBusy=true;renderDesktopSharing();
 try{const state=await api<DesktopState>('tasks/'+encodeURIComponent(task)+'/desktop','PUT',{target:element<HTMLSelectElement>('desktop-target').value,control:input('desktop-allow-control').checked});if(!shellCurrent(epoch)||revision!==desktopRevision)return;renderDesktopSharing(state);element('desktop-status').textContent='已开始共享。发送下一条任务消息，AI 即可使用桌面工具。用户操作后，AI 必须重新观察画面。'}catch(e){if(shellCurrent(epoch)&&revision===desktopRevision)element('desktop-status').textContent=(e as Error).message}finally{if(shellCurrent(epoch)&&revision===desktopRevision){desktopBusy=false;renderDesktopSharing()}}
}
async function stopDesktopSharing(){
 const epoch=shellEpoch,revision=++desktopRevision;clearTimeout(desktopPreviewTimer);desktopFrame=null;button('desktop-stop').disabled=true;
 try{const state=await api<DesktopState>('desktop','DELETE');if(!shellCurrent(epoch)||revision!==desktopRevision)return;desktopBusy=false;renderDesktopSharing(state);element('desktop-status').textContent='共享已停止，AI 已失去桌面访问权限。'}catch(e){if(shellCurrent(epoch)&&revision===desktopRevision){element('desktop-status').textContent='停止失败，请重试：'+(e as Error).message;button('desktop-stop').disabled=false}}
}
