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
 for(const id of ['tool-resizer','dock-left-resizer','dock-right-resizer'])document.getElementById(id)?.setAttribute('aria-valuenow',String(Math.round(appearance.tool)));
 const slider=document.getElementById('appearance-tool') as HTMLInputElement|null;if(slider)slider.value=String(appearance.tool);
 if(save)persistPanelSizes();
}
function installDockResizers(){
 const workspace=element('workspace');if(!workspace)return;
 for(const side of ['left','right'] as const){
  const zone=element('dock-'+side);if(!zone)continue;
  const id='dock-'+side+'-resizer';
  let handle=document.getElementById(id);
  if(!handle){handle=panelHandle(id,'调整'+(side==='left'?'左侧':'右侧')+'工具栏宽度','拖动调整这一栏的宽度','vertical');zone.append(handle)}
  handle.classList.add('panel-resizer');
  handle.setAttribute('aria-valuemin',String(toolLimits.min));
  handle.setAttribute('aria-valuemax',String(toolLimits.max));
  handle.setAttribute('aria-valuenow',String(Math.round(appearance.tool)));
  draggablePanel(handle,'x',{
   move:event=>{const rect=workspace.getBoundingClientRect();if(!rect.width)return;setToolWidth((side==='left'?event.clientX-rect.left:rect.right-event.clientX)/rect.width*100,false)},
   end:()=>persistPanelSizes(),
  });
  handle.ondblclick=()=>setToolWidth(defaultAppearance.tool,true);
  handle.onkeydown=event=>{
   const grow=side==='left'?1:-1,step=event.shiftKey?4:2;
   if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();setToolWidth(appearance.tool+(event.key==='ArrowRight'?grow:-grow)*step,true)}
   else if(event.key==='Home'||event.key==='Enter'){event.preventDefault();setToolWidth(defaultAppearance.tool,true)}
  };
 }
 document.getElementById('tool-resizer')?.classList.add('hidden');
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
 for(const [id,value] of [['sidebar-resizer',appearance.sidebar],['dock-left-resizer',appearance.tool],['dock-right-resizer',appearance.tool]] as const)document.getElementById(id)?.setAttribute('aria-valuenow',String(Math.round(value)));
 input('appearance-sidebar').value=String(appearance.sidebar);
 input('appearance-tool').value=String(appearance.tool);
 dockLayout={...defaultDockLayout};applyDockLayout(true);
 notify('面板布局已恢复默认。');
}

/* Free three-column layout: every panel can live in the task column, either tool column, or under the conversation. */
type DockSide='sidebar'|'left'|'right'|'bottom';
type DockPanel='sticky'|'tools';
const dockPanelIDs:Record<DockPanel,string>={sticky:'sticky-board',tools:'tool-dock'};
const dockPanelLabels:Record<DockPanel,string>={sticky:'便签栏',tools:'工具面板'};
const dockSideLabels:Record<DockSide,string>={sidebar:'任务栏',left:'左栏',right:'右栏',bottom:'对话下方'};
const dockPanelSides:Record<DockPanel,DockSide[]>={sticky:['sidebar','left','right','bottom'],tools:['left','right']};
const dockZoneSides:Record<string,DockSide>={sidebar:'sidebar','dock-left':'left','dock-right':'right','dock-bottom':'bottom'};
const defaultDockLayout:Record<DockPanel,DockSide>={sticky:'sidebar',tools:'right'};
let dockLayout:Record<DockPanel,DockSide>={...defaultDockLayout},dockDrag:DockPanel|''='';
const dockNarrow=matchMedia('(max-width:760px)');
function parseDockLayout(raw:string|null):Record<DockPanel,DockSide>{
 const value:Record<string,unknown>=(()=>{try{const parsed=JSON.parse(raw||'{}');return parsed&&typeof parsed==='object'?parsed:{}}catch{return {}}})();
 const out:Record<DockPanel,DockSide>={...defaultDockLayout};
 for(const panel of ['sticky','tools'] as DockPanel[]){const side=value[panel] as DockSide;if(dockPanelSides[panel].includes(side))out[panel]=side}
 return out;
}
function loadDockLayout():Record<DockPanel,DockSide>{try{return parseDockLayout(localStorage.getItem('jianzuo-dock-layout-v1'))}catch{return {...defaultDockLayout}}}
function persistDockLayout(){try{localStorage.setItem('jianzuo-dock-layout-v1',JSON.stringify(dockLayout))}catch{}}
function dockZoneID(side:DockSide){return side==='sidebar'?'sidebar':'dock-'+side}
function effectiveDockSide(panel:DockPanel):DockSide{const side=dockLayout[panel];if(panel==='sticky'&&side!=='bottom'&&dockNarrow.matches)return 'bottom';return side}
function syncDocks(){
 const visible=(zone:HTMLElement)=>Array.from(zone.children).some(node=>{const child=node as HTMLElement;return !child.classList.contains('panel-resizer')&&!child.classList.contains('hidden')&&!child.hidden});
 for(const side of ['left','right','bottom'] as DockSide[]){const zone=element(dockZoneID(side));if(zone)zone.classList.toggle('hidden',!visible(zone))}
}
function applyDockLayout(save=false){
 for(const panel of ['sticky','tools'] as DockPanel[]){
  const node=element(dockPanelIDs[panel]),zone=element(dockZoneID(effectiveDockSide(panel)));
  if(!node||!zone)continue;
  if(zone.id==='sidebar'){const footer=element('sidebar-footer');footer?zone.insertBefore(node,footer):zone.append(node)}
  else zone.append(node);
 }
 for(const side of ['sidebar','left','right','bottom'] as DockSide[]){
  const zone=element(dockZoneID(side));if(!zone)continue;
  const panels=(['sticky','tools'] as DockPanel[]).filter(panel=>effectiveDockSide(panel)===side);
  if(panels.length)zone.dataset.dockPanels=panels.join(' ');else delete zone.dataset.dockPanels;
 }
 document.getElementById('tool-resizer')?.classList.add('hidden');
 syncDocks();syncDockSelects();
 if(save)persistDockLayout();
}
function syncDockSelects(){
 for(const panel of ['sticky','tools'] as DockPanel[]){
  const select=document.getElementById('appearance-'+panel+'-zone') as HTMLSelectElement|null;
  if(select)select.value=dockLayout[panel];
 }
}
function moveDockPanel(panel:DockPanel,side:DockSide){
 if(!dockPanelSides[panel].includes(side))return;
 dockLayout={...dockLayout,[panel]:side};applyDockLayout(true);
 notify(dockPanelLabels[panel]+'已移到'+dockSideLabels[side]+'。');
}
function cycleDockSide(panel:DockPanel){const sides=dockPanelSides[panel];moveDockPanel(panel,sides[(sides.indexOf(dockLayout[panel])+1)%sides.length])}
function dockGrip(panel:DockPanel,host:Element|null){
 if(!host)return null;
 let grip=host.querySelector<HTMLElement>('[data-dock-grip]');
 if(!grip){grip=document.createElement('span');grip.className='dock-grip';grip.dataset.dockGrip=panel;grip.draggable=true;grip.tabIndex=0;grip.textContent='⠿';host.prepend(grip)}
 grip.setAttribute('aria-label','移动'+dockPanelLabels[panel]);
 grip.title='拖动到目标栏 · 双击按顺序换栏 · 回车依次切换';
 return grip;
}
function installDockDrag(){
 const markTargets=(panel:DockPanel|'')=>{
  for(const zone of document.querySelectorAll<HTMLElement>('[data-dock-zone]')){
   const side=dockZoneSides[zone.id];
   zone.classList.toggle('dock-target',!!panel&&!!side&&dockPanelSides[panel].includes(side));
  }
 };
 const hosts:[DockPanel,Element|null][]=[['sticky',element('sticky-board')?.querySelector('header')||null],['tools',element('tool-dock')?.querySelector('.tool-dock-head')||null]];
 for(const [panel,host] of hosts){
  const grip=dockGrip(panel,host);if(!grip||grip.dataset.dockBound)continue;
  grip.dataset.dockBound='1';
  grip.addEventListener('dragstart',event=>{dockDrag=panel;document.body.classList.add('dock-dragging');markTargets(panel);event.dataTransfer?.setData('text/plain',panel);if(event.dataTransfer)event.dataTransfer.effectAllowed='move'});
  grip.addEventListener('dragend',()=>{dockDrag='';document.body.classList.remove('dock-dragging');markTargets('');document.querySelectorAll('.dock-over').forEach(zone=>zone.classList.remove('dock-over'))});
  grip.addEventListener('dblclick',()=>cycleDockSide(panel));
  grip.addEventListener('keydown',event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();cycleDockSide(panel)}});
 }
 for(const zone of document.querySelectorAll<HTMLElement>('[data-dock-zone]')){
  if(zone.dataset.dockBound)continue;zone.dataset.dockBound='1';
  zone.addEventListener('dragover',event=>{
   const side=dockZoneSides[zone.id];if(!dockDrag||!side||!dockPanelSides[dockDrag].includes(side))return;
   event.preventDefault();if(event.dataTransfer)event.dataTransfer.dropEffect='move';zone.classList.add('dock-over');
  });
  zone.addEventListener('dragleave',()=>zone.classList.remove('dock-over'));
  zone.addEventListener('drop',event=>{
   event.preventDefault();zone.classList.remove('dock-over');
   const side=dockZoneSides[zone.id];if(dockDrag&&side)moveDockPanel(dockDrag,side);
  });
 }
}
function installDockUI(){
 for(const [id,side] of Object.entries(dockZoneSides)){const zone=element(id);if(zone)zone.dataset.dockZone=side}
 const form=element('appearance-form');
 if(form&&!document.getElementById('appearance-sticky-zone')){
  const section=document.createElement('div');section.className='dock-layout-section';
  const options=(panel:DockPanel)=>dockPanelSides[panel].map(side=>`<option value="${side}">${dockSideLabels[side]}</option>`).join('');
  section.innerHTML=`<h3 class="section-title">面板位置</h3><div class="form-grid"><div><label for="appearance-sticky-zone">便签栏</label><select id="appearance-sticky-zone">${options('sticky')}</select></div><div><label for="appearance-tools-zone">工具面板</label><select id="appearance-tools-zone">${options('tools')}</select></div></div><p>拖动面板标题左侧的 ⠿ 手柄可以直接换栏，双击手柄按顺序切换。窄窗口沿用单栏布局。</p>`;
  const footer=form.querySelector('.dialog-footer');form.insertBefore(section,footer);
  for(const panel of ['sticky','tools'] as DockPanel[]){
   const select=element<HTMLSelectElement>('appearance-'+panel+'-zone');
   select.onchange=()=>moveDockPanel(panel,select.value as DockSide);
  }
 }
 installDockDrag();syncDockSelects();
}
function installDockLayout(){
 dockLayout=loadDockLayout();installDockUI();applyDockLayout(false);
 for(const id of ['sticky-board','tool-dock']){
  const node=element(id);
  new MutationObserver(()=>syncDocks()).observe(node,{attributes:true,attributeFilter:['class']});
 }
 dockNarrow.addEventListener('change',()=>applyDockLayout(false));
}
function installPanelLayout(){
 installSidebarResizer();installDockResizers();installStickyResizer();installHardwareResizer();installDockLayout();
}
