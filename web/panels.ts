/* Manual layout: drag a panel edge with the mouse, double-click it to go back to the default size. */
type PanelLimits={min:number;max:number};
const sidebarLimits:PanelLimits={min:180,max:420};
const toolLimits:PanelLimits={min:22,max:75};
const stickyLimits:PanelLimits={min:80,max:900};
const hardwareLimits:PanelLimits={min:96,max:900};
function clampPanel(value:number,limits:PanelLimits):number{return Math.max(limits.min,Math.min(limits.max,value))}
function readPanelSize(key:string,limits:PanelLimits):number{
 try{const value=Number(localStorage.getItem(key));if(Number.isFinite(value)&&value>=limits.min&&value<=limits.max)return Math.round(value)}catch{}
 return 0;
}
function writePanelSize(key:string,value:number){try{localStorage.setItem(key,String(Math.round(value)))}catch{}}
function clearPanelSize(key:string){try{localStorage.removeItem(key)}catch{}}
function persistPanelSizes(){try{localStorage.setItem('jianzuo-appearance-v1',JSON.stringify(appearance));localStorage.setItem('jianzuo-theme',appearance.theme)}catch{}}
function draggablePanel(handle:HTMLElement,axis:'x'|'y',callbacks:{start?:()=>void;move:(event:PointerEvent)=>void;end?:()=>void}){
 handle.addEventListener('pointerdown',event=>{
  if(event.button!==0||event.defaultPrevented)return;
  event.preventDefault();
  try{handle.setPointerCapture(event.pointerId)}catch{}
  handle.classList.add('dragging');
  document.body.classList.add(axis==='x'?'resizing-x':'resizing-y');
  callbacks.start?.();
 });
 handle.addEventListener('pointermove',event=>{if(handle.hasPointerCapture(event.pointerId))callbacks.move(event)});
 const finish=(event:PointerEvent)=>{
  if(!handle.hasPointerCapture(event.pointerId))return;
  try{handle.releasePointerCapture(event.pointerId)}catch{}
  handle.classList.remove('dragging');
  document.body.classList.remove('resizing-x','resizing-y');
  callbacks.end?.();
 };
 handle.addEventListener('pointerup',finish);
 handle.addEventListener('pointercancel',finish);
 handle.addEventListener('lostpointercapture',finish);
}
function panelHandle(id:string,label:string,hint:string,orientation:'vertical'|'horizontal'):HTMLElement{
 const handle=document.createElement('div');
 handle.id=id;handle.className='panel-resizer';
 handle.tabIndex=0;
 handle.setAttribute('role','separator');
 handle.setAttribute('aria-orientation',orientation);
 handle.setAttribute('aria-label',label);
 handle.title=hint+' · 方向键微调 · 双击恢复默认';
 return handle;
}
function setSidebarWidth(value:number,save:boolean){
 appearance.sidebar=Math.round(clampPanel(value,sidebarLimits));
 applyAppearance(appearance);
 document.getElementById('sidebar-resizer')?.setAttribute('aria-valuenow',String(appearance.sidebar));
 input('appearance-sidebar').value=String(appearance.sidebar);
 if(save)persistPanelSizes();
}
function installSidebarResizer(){
 const sidebar=element('sidebar');if(!sidebar)return;
 let handle=document.getElementById('sidebar-resizer');
 if(!handle){handle=panelHandle('sidebar-resizer','调整任务侧栏宽度','拖动调整任务侧栏宽度','vertical');sidebar.append(handle)}
 handle.setAttribute('aria-valuemin',String(sidebarLimits.min));
 handle.setAttribute('aria-valuemax',String(sidebarLimits.max));
 handle.setAttribute('aria-valuenow',String(Math.round(appearance.sidebar)));
 draggablePanel(handle,'x',{
  move:event=>setSidebarWidth(event.clientX-sidebar.getBoundingClientRect().left,false),
  end:()=>persistPanelSizes(),
 });
 handle.ondblclick=()=>setSidebarWidth(defaultAppearance.sidebar,true);
 handle.onkeydown=event=>{
  const step=event.shiftKey?24:8;
  if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();setSidebarWidth(appearance.sidebar+(event.key==='ArrowLeft'?-step:step),true)}
  else if(event.key==='Home'||event.key==='Enter'){event.preventDefault();setSidebarWidth(defaultAppearance.sidebar,true)}
 };
}
function setToolWidth(value:number,save:boolean){
 appearance.tool=Math.round(clampPanel(value,toolLimits)*10)/10;
 applyAppearance(appearance);
 document.getElementById('tool-resizer')?.setAttribute('aria-valuenow',String(Math.round(appearance.tool)));
 input('appearance-tool').value=String(appearance.tool);
 if(save)persistPanelSizes();
}
function installToolResizer(){
 const workspace=element('workspace'),handle=document.getElementById('tool-resizer');
 if(!workspace||!handle)return;
 handle.classList.add('panel-resizer');
 handle.title='拖动调整工具面板宽度 · 方向键微调 · 双击恢复默认';
 handle.setAttribute('aria-valuemin',String(toolLimits.min));
 handle.setAttribute('aria-valuemax',String(toolLimits.max));
 handle.setAttribute('aria-valuenow',String(Math.round(appearance.tool)));
 draggablePanel(handle,'x',{
  move:event=>{const rect=workspace.getBoundingClientRect();if(rect.width)setToolWidth((rect.right-event.clientX)/rect.width*100,false)},
  end:()=>persistPanelSizes(),
 });
 handle.ondblclick=()=>setToolWidth(defaultAppearance.tool,true);
 handle.onkeydown=event=>{
  if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();setToolWidth(appearance.tool+(event.key==='ArrowLeft'?2:-2),true)}
  else if(event.key==='Home'||event.key==='Enter'){event.preventDefault();setToolWidth(defaultAppearance.tool,true)}
 };
}
function installStickyResizer(){
 const board=element('sticky-board');if(!board)return;
 let handle=document.getElementById('sticky-resizer');
 if(!handle){handle=panelHandle('sticky-resizer','调整便签板高度','上下拖动调整便签板高度','horizontal');board.prepend(handle)}
 const ceiling=()=>Math.max(160,Math.round(innerHeight*0.55));
 const apply=(value:number,save:boolean)=>{
  const height=Math.round(clampPanel(value,{min:stickyLimits.min,max:ceiling()}));
  board.dataset.sized='1';board.style.setProperty('--sticky-height',height+'px');
  handle!.setAttribute('aria-valuenow',String(height));
  if(save)writePanelSize('jianzuo-sticky-height',height);
 };
 const saved=readPanelSize('jianzuo-sticky-height',stickyLimits);
 if(saved)apply(saved,false);
 let bottom=0;
 draggablePanel(handle,'y',{
  start:()=>{bottom=board.getBoundingClientRect().bottom},
  move:event=>apply(bottom-event.clientY,false),
  end:()=>{if(board.dataset.sized)writePanelSize('jianzuo-sticky-height',Math.round(board.getBoundingClientRect().height))},
 });
 handle.ondblclick=()=>{delete board.dataset.sized;board.style.removeProperty('--sticky-height');clearPanelSize('jianzuo-sticky-height');notify('便签板高度已恢复默认。')};
 handle.onkeydown=event=>{
  const step=event.shiftKey?40:16,current=Math.round(board.getBoundingClientRect().height);
  if(event.key==='ArrowUp'||event.key==='ArrowDown'){event.preventDefault();apply(current+(event.key==='ArrowUp'?step:-step),true)}
  else if(event.key==='Home'||event.key==='Enter'){event.preventDefault();handle!.ondblclick?.(new MouseEvent('dblclick'))}
 };
}
function installHardwareResizer(){
 const panel=element('hardware-panel'),details=element<HTMLDetailsElement>('hardware-send-details');
 if(!panel||!details)return;
 let handle=document.getElementById('hardware-resizer');
 if(!handle){handle=panelHandle('hardware-resizer','调整发送区高度','上下拖动调整发送区高度','horizontal');details.before(handle)}
 const sync=()=>handle!.classList.toggle('hidden',details.classList.contains('hidden')||!details.open);
 details.addEventListener('toggle',sync);
 new MutationObserver(sync).observe(details,{attributes:true,attributeFilter:['class']});
 sync();
 const apply=(value:number,save:boolean)=>{
  const limit=Math.max(hardwareLimits.min,Math.round(panel.getBoundingClientRect().height-190));
  const height=Math.round(clampPanel(value,{min:hardwareLimits.min,max:limit}));
  panel.dataset.sized='1';panel.style.setProperty('--hardware-height',height+'px');
  handle!.setAttribute('aria-valuenow',String(height));
  if(save)writePanelSize('jianzuo-hardware-height',height);
 };
 const saved=readPanelSize('jianzuo-hardware-height',hardwareLimits);
 if(saved)apply(saved,false);
 let bottom=0;
 draggablePanel(handle,'y',{
  start:()=>{bottom=panel.getBoundingClientRect().bottom},
  move:event=>apply(bottom-event.clientY,false),
  end:()=>{if(details.open)writePanelSize('jianzuo-hardware-height',Math.round(details.getBoundingClientRect().height))},
 });
 handle.ondblclick=()=>{delete panel.dataset.sized;panel.style.removeProperty('--hardware-height');clearPanelSize('jianzuo-hardware-height');notify('发送区高度已恢复默认。')};
 handle.onkeydown=event=>{
  const step=event.shiftKey?40:16,current=Math.round(details.getBoundingClientRect().height);
  if(event.key==='ArrowUp'||event.key==='ArrowDown'){event.preventDefault();apply(current+(event.key==='ArrowUp'?step:-step),true)}
  else if(event.key==='Home'||event.key==='Enter'){event.preventDefault();handle!.ondblclick?.(new MouseEvent('dblclick'))}
 };
}
function resetPanelLayout(){
 const board=element('sticky-board'),panel=element('hardware-panel');
 if(board){delete board.dataset.sized;board.style.removeProperty('--sticky-height')}
 if(panel){delete panel.dataset.sized;panel.style.removeProperty('--hardware-height')}
 clearPanelSize('jianzuo-sticky-height');clearPanelSize('jianzuo-hardware-height');
 appearance.sidebar=defaultAppearance.sidebar;appearance.tool=defaultAppearance.tool;
 applyAppearance(appearance);persistPanelSizes();
 for(const [id,value] of [['sidebar-resizer',appearance.sidebar],['tool-resizer',appearance.tool]] as const)document.getElementById(id)?.setAttribute('aria-valuenow',String(Math.round(value)));
 input('appearance-sidebar').value=String(appearance.sidebar);
 input('appearance-tool').value=String(appearance.tool);
 notify('面板布局已恢复默认。');
}
function installPanelLayout(){installSidebarResizer();installToolResizer();installStickyResizer();installHardwareResizer()}
