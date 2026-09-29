type QuickNote={id:string;content:string;done:boolean};
type QuickNoteBoard={revision:number;color:string;items:QuickNote[]};
let quickNotes:QuickNoteBoard={revision:0,color:'neutral',items:[]};
let stickyLoading=false,stickyReady=false,stickyDirty=false,stickySaving=false,stickyConflict=false,stickyError='',stickySaved='',stickyRequest=0,stickyGeneration=0,stickySequence=0;
let stickyTimer:ReturnType<typeof setTimeout>;
const stickyColors=[['neutral','默认'],['yellow','奶黄'],['blue','浅蓝'],['green','浅绿'],['purple','淡紫']];
type Appearance={theme:'light'|'dark'|'system';accent:string;font:number;sidebar:number;width:number;tool:number;notes:boolean;compact:boolean;path:boolean};
const defaultAppearance:Appearance={theme:'light',accent:'blue',font:14,sidebar:248,width:820,tool:42,notes:true,compact:false,path:true};
let appearance:Appearance={...defaultAppearance};
function parseAppearance(raw:string|null):Appearance{
 try{const v=JSON.parse(raw||'{}');return {theme:['light','dark','system'].includes(v.theme)?v.theme:'light',accent:['blue','green','purple'].includes(v.accent)?v.accent:'blue',font:Math.max(12,Math.min(18,Number(v.font)||14)),sidebar:Math.max(180,Math.min(420,Number(v.sidebar)||248)),width:Math.max(640,Math.min(1100,Number(v.width)||820)),tool:Math.max(22,Math.min(75,Number(v.tool)||42)),notes:v.notes!==false,compact:v.compact===true,path:v.path!==false}}catch{return {...defaultAppearance}}
}
function installStickyBoard(){
 element('task-list').insertAdjacentHTML('afterend',`<section id="sticky-board" class="sticky-board quick-notes" data-collapsed="false" aria-label="共用便签"><header><strong>便签</strong><span id="sticky-count"></span><button id="sticky-expand" type="button" aria-expanded="true" aria-controls="sticky-body" aria-label="收起便签" title="收起便签">⌄</button></header><div id="sticky-body"><div id="sticky-palette" role="group" aria-label="便签颜色">${stickyColors.map(([color,label])=>`<button type="button" data-sticky-color="${color}" aria-label="${label}" title="${label}" aria-pressed="false"><span></span></button>`).join('')}</div><div id="sticky-list" class="sticky-list"></div><button id="sticky-new" type="button">＋ 添加事项</button><footer class="sticky-save"><span id="sticky-save-status" role="status" aria-live="polite">正在读取…</span><button id="sticky-retry" type="button" class="hidden">重试</button><button id="sticky-reload" type="button" class="hidden">重新加载</button><button id="sticky-undo" type="button" class="hidden">撤销删除</button></footer></div></section>`);
 button('sticky-expand').onclick=()=>{
  const board=element('sticky-board'),collapsed=board.dataset.collapsed!=='true';board.dataset.collapsed=String(collapsed);element('sticky-body').classList.toggle('hidden',collapsed);
  button('sticky-expand').setAttribute('aria-expanded',String(!collapsed));button('sticky-expand').setAttribute('aria-label',collapsed?'展开便签':'收起便签');button('sticky-expand').title=collapsed?'展开便签':'收起便签';button('sticky-expand').textContent=collapsed?'›':'⌄';
 };
 element('sticky-palette').querySelectorAll<HTMLButtonElement>('button').forEach(b=>b.onclick=()=>{if(!stickyReady)return;quickNotes.color=b.dataset.stickyColor!;touchSticky();syncStickyState()});
 button('sticky-new').onclick=addQuickNote;
 button('sticky-retry').onclick=()=>{if(stickyReady){stickyError='';void saveQuickNotes()}else void loadStickyBoard()};
 button('sticky-reload').onclick=()=>{if(!stickyDirty||confirm('重新加载会放弃便签中尚未保存的修改。请先复制需要保留的内容，是否继续？'))void loadStickyBoard(true)};
 button('sticky-undo').onclick=()=>{if(!stickyDeleted)return;quickNotes.items.splice(Math.min(stickyDeleted.index,quickNotes.items.length),0,stickyDeleted.note);stickyDeleted=null;touchSticky();renderStickyBoard()};
 disposeWithShell(()=>{clearTimeout(stickyTimer);stickyRequest++;stickyLoading=false});
 if(typeof ResizeObserver!=='undefined'){
  let width=0,timer:ReturnType<typeof setTimeout>;const observer=new ResizeObserver(entries=>{const next=entries[0]?.contentRect.width||0;if(next>0&&next!==width){width=next;clearTimeout(timer);timer=setTimeout(()=>fitQuickNotes(),120)}});
  observer.observe(element('sticky-list'));disposeWithShell(()=>{observer.disconnect();clearTimeout(timer)});
 }
 // A temporary logout may interrupt a save; keep its draft in memory.
 if(stickyDirty){stickyError='便签草稿尚未保存，请重试';renderStickyBoard()}else {stickyReady=false;void loadStickyBoard()}
}
function stickyPayload(){return {color:quickNotes.color,items:quickNotes.items.filter(n=>n.content.trim()).map(n=>({...n}))}}
function stickyFingerprint(){return JSON.stringify(stickyPayload())}
function blankQuickNote():QuickNote{return {id:'n_'+Date.now().toString(36)+'_'+(++stickySequence)+'_'+Math.random().toString(36).slice(2,9),content:'',done:false}}
function touchSticky(){
 stickyGeneration++;stickyDirty=stickyFingerprint()!==stickySaved;
 clearTimeout(stickyTimer);if(!stickyError&&!stickyConflict)stickyTimer=setTimeout(()=>void saveQuickNotes(),600);syncStickyState();
}
async function loadStickyBoard(force=false){
 if(!authenticated||!element('sticky-list')||stickyLoading||stickySaving||!force&&(stickyDirty||!!element('sticky-list').contains(document.activeElement)))return;
 stickyLoading=true;const request=++stickyRequest,epoch=shellEpoch,generation=stickyGeneration;
 try{
  const board=await api<QuickNoteBoard>('sticky');if(request!==stickyRequest||!shellCurrent(epoch)||generation!==stickyGeneration||!force&&element('sticky-list').contains(document.activeElement))return;
  const changed=!stickyReady||JSON.stringify({color:board.color,items:board.items})!==stickySaved;
  if(changed||force)quickNotes=board;else quickNotes.revision=board.revision;
  stickyReady=true;stickyDirty=false;stickyError='';stickyConflict=false;stickySaved=stickyFingerprint();
  if(changed||force)renderStickyBoard();else syncStickyState();
 }catch(e){if(request===stickyRequest&&shellCurrent(epoch)){stickyError='读取失败：'+(e as Error).message;syncStickyState()}}
 finally{if(request===stickyRequest)stickyLoading=false}
}
async function saveQuickNotes(){
 clearTimeout(stickyTimer);if(!stickyReady||!stickyDirty||stickySaving||stickyConflict||!authenticated)return;
 const epoch=shellEpoch,snapshot=stickyPayload(),fingerprint=JSON.stringify(snapshot);stickySaving=true;stickyError='';syncStickyState();
 let saved=false;
 try{
  const board=await api<QuickNoteBoard>('sticky','PUT',{...snapshot,revision:quickNotes.revision});
  quickNotes.revision=board.revision;stickySaved=fingerprint;stickyDirty=stickyFingerprint()!==stickySaved;saved=true;
 }catch(e){
  // If the response was lost, a retry can observe the identical committed board
  // without treating it as somebody else's edit or writing it twice.
  if((e as {status?:number}).status===409){
   try{const board=await api<QuickNoteBoard>('sticky');if(JSON.stringify({color:board.color,items:board.items})===fingerprint){quickNotes.revision=board.revision;stickySaved=fingerprint;stickyDirty=stickyFingerprint()!==stickySaved;saved=true}else stickyConflict=true}catch{stickyConflict=true}
  }
  if(!saved)stickyError=stickyConflict?'其他窗口已修改，草稿保留；请复制需要的内容后重新加载。':'未保存：'+(e as Error).message;
 }finally{
  stickySaving=false;if(shellCurrent(epoch)){syncStickyState();if(saved&&stickyDirty)stickyTimer=setTimeout(()=>void saveQuickNotes(),600)}
 }
}
function syncStickyState(){
 const board=element('sticky-board');if(!board)return;board.dataset.color=quickNotes.color;
 const count=quickNotes.items.filter(n=>n.content.trim()&&!n.done).length;element('sticky-count').textContent=count?count+' 项':'';
 element('sticky-palette').querySelectorAll<HTMLButtonElement>('button').forEach(b=>{b.disabled=!stickyReady;b.setAttribute('aria-pressed',String(b.dataset.stickyColor===quickNotes.color))});
 button('sticky-new').disabled=!stickyReady||quickNotes.items.length>=500;
 const status=element('sticky-save-status');status.textContent=stickyError||(stickySaving?'保存中…':stickyDirty?'待保存…':stickyReady?'已保存 · 共用便签':'正在读取…');status.classList.toggle('error',!!stickyError);
 button('sticky-retry').classList.toggle('hidden',!stickyError||stickyConflict);button('sticky-retry').disabled=stickySaving;
 button('sticky-reload').classList.toggle('hidden',!stickyConflict);button('sticky-reload').disabled=stickySaving;
 button('sticky-undo').classList.toggle('hidden',!stickyDeleted);
}
let stickyDeleted:{note:QuickNote;index:number}|null=null;
function addQuickNote(){
 if(!stickyReady)return;let note=quickNotes.items.find(n=>!n.content.trim());
 if(!note){if(quickNotes.items.length>=500){notify('便签最多 500 条，请先整理已有内容。');return}note=blankQuickNote();quickNotes.items.push(note)}
 renderStickyBoard();const field=element<HTMLTextAreaElement>('sticky-input-'+note.id);field.focus();field.scrollIntoView?.({block:'nearest'});
}
function fitQuickNote(field:HTMLTextAreaElement){field.style.height='auto';if(Number.isFinite(field.scrollHeight))field.style.height=Math.max(32,Math.min(96,field.scrollHeight))+'px'}
function fitQuickNotes(){const fields=Array.from(element('sticky-list').querySelectorAll<HTMLTextAreaElement>('textarea'));fields.forEach(field=>field.style.height='auto');const heights=fields.map(field=>Math.max(32,Math.min(96,field.scrollHeight)));fields.forEach((field,i)=>{if(Number.isFinite(heights[i]))field.style.height=heights[i]+'px'})}
function renderStickyBoard(){
 const list=element('sticky-list');if(!list)return;
 if(stickyReady&&!quickNotes.items.length)quickNotes.items.push(blankQuickNote());
 list.innerHTML=quickNotes.items.map(n=>`<div class="quick-note" data-note="${escapeHTML(n.id)}" data-done="${n.done}"><input type="checkbox" data-note-done="${escapeHTML(n.id)}" aria-label="标记便签完成" ${n.done?'checked':''}><textarea id="sticky-input-${escapeHTML(n.id)}" data-note-input="${escapeHTML(n.id)}" rows="1" maxlength="8000" aria-label="便签内容" placeholder="写下备忘…">${escapeHTML(n.content)}</textarea><button type="button" data-note-delete="${escapeHTML(n.id)}" aria-label="删除这条便签" title="删除">×</button></div>`).join('');
 list.querySelectorAll<HTMLTextAreaElement>('[data-note-input]').forEach(field=>{
  field.value=quickNotes.items.find(n=>n.id===field.dataset.noteInput)?.content||'';
  let composing=false;const update=()=>{const note=quickNotes.items.find(n=>n.id===field.dataset.noteInput);if(!note)return;note.content=field.value;fitQuickNote(field);touchSticky();if(composing)clearTimeout(stickyTimer)};
  field.addEventListener('compositionstart',()=>{composing=true;clearTimeout(stickyTimer)});field.addEventListener('compositionend',()=>{composing=false;update()});field.oninput=update;
  field.onblur=()=>{if(!composing&&!stickyError)void saveQuickNotes()};
  field.onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing&&!composing){e.preventDefault();if(!stickyError)void saveQuickNotes();addQuickNote()}};fitQuickNote(field);
 });
 list.querySelectorAll<HTMLInputElement>('[data-note-done]').forEach(box=>box.onchange=()=>{const note=quickNotes.items.find(n=>n.id===box.dataset.noteDone);if(!note)return;note.done=box.checked;box.closest<HTMLElement>('.quick-note')!.dataset.done=String(note.done);touchSticky()});
 list.querySelectorAll<HTMLButtonElement>('[data-note-delete]').forEach(b=>b.onclick=()=>{const index=quickNotes.items.findIndex(n=>n.id===b.dataset.noteDelete);if(index<0)return;const [note]=quickNotes.items.splice(index,1);stickyDeleted=note.content.trim()?{note,index}:null;touchSticky();renderStickyBoard();const target=list.querySelector<HTMLTextAreaElement>('[data-note-input]');target?.focus()});
 syncStickyState();
}
setInterval(()=>{if(authenticated&&!document.hidden&&element('sticky-board'))void loadStickyBoard()},5000);
function applyAppearance(value:Appearance){const root=document.documentElement;root.dataset.theme=value.theme==='system'?(matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light'):value.theme;root.dataset.accent=value.accent;root.dataset.density=value.compact?'compact':'normal';root.style.setProperty('--chat-font',value.font+'px');root.style.setProperty('--sidebar-width',value.sidebar+'px');if(element('sidebar'))element('sidebar').dataset.narrow=String(value.sidebar<220);root.style.setProperty('--chat-width',value.width+'px');element('workspace')?.style.setProperty('--tool-width',value.tool+'%');element('sticky-board')?.classList.toggle('hidden',!value.notes);element('task-workspace')?.classList.toggle('hidden',!value.path)}
function installAppearance(){try{appearance=parseAppearance(localStorage.getItem('jianzuo-appearance-v1'));if(!localStorage.getItem('jianzuo-appearance-v1')&&localStorage.getItem('jianzuo-theme')==='dark')appearance.theme='dark'}catch{}applyAppearance(appearance);
 element('root').insertAdjacentHTML('beforeend',`<dialog id="appearance-dialog"><form id="appearance-form"><h2>调整界面</h2><div class="form-grid"><div><label for="appearance-theme">主题</label><select id="appearance-theme"><option value="light">浅色</option><option value="dark">深色</option><option value="system">跟随系统</option></select></div><div><label for="appearance-accent">强调色</label><select id="appearance-accent"><option value="blue">蓝色</option><option value="green">绿色</option><option value="purple">紫色</option></select></div></div><label for="appearance-font">对话字号</label><input id="appearance-font" type="range" min="12" max="18" step="1"><label for="appearance-sidebar">任务侧栏宽度</label><input id="appearance-sidebar" type="range" min="180" max="420" step="5"><label for="appearance-tool">左右工具栏宽度</label><input id="appearance-tool" type="range" min="22" max="75" step="1"><label for="appearance-width">对话内容宽度</label><input id="appearance-width" type="range" min="640" max="1100" step="20"><label class="check-row"><input id="appearance-notes" type="checkbox">常驻便签</label><label class="check-row"><input id="appearance-compact" type="checkbox">紧凑布局</label><label class="check-row"><input id="appearance-path" type="checkbox">显示任务工作路径</label><p>即时预览，保存到当前浏览器。</p><div class="dialog-footer"><button type="button" id="appearance-layout">恢复默认布局</button><button type="button" id="appearance-reset">恢复默认</button><button type="button" id="appearance-cancel">取消</button><button class="primary">保存</button></div></form></dialog>`);
 button('theme-toggle').onclick=()=>{fillAppearance(appearance);element<HTMLDialogElement>('appearance-dialog').showModal()};element('appearance-form').oninput=()=>applyAppearance(readAppearance());element('appearance-form').onsubmit=e=>{e.preventDefault();appearance=readAppearance();try{localStorage.setItem('jianzuo-appearance-v1',JSON.stringify(appearance));localStorage.setItem('jianzuo-theme',appearance.theme)}catch{}applyAppearance(appearance);element<HTMLDialogElement>('appearance-dialog').close()};button('appearance-layout').onclick=()=>{resetPanelLayout();fillAppearance(appearance)};button('appearance-reset').onclick=()=>{fillAppearance(defaultAppearance);applyAppearance(defaultAppearance)};button('appearance-cancel').onclick=()=>{applyAppearance(appearance);element<HTMLDialogElement>('appearance-dialog').close()};element('appearance-dialog').addEventListener('cancel',()=>applyAppearance(appearance));
}
function fillAppearance(v:Appearance){for(const key of ['theme','accent','font','sidebar','width','tool'] as const)input('appearance-'+key).value=String(v[key]);for(const key of ['notes','compact','path'] as const)input('appearance-'+key).checked=v[key]}
function readAppearance():Appearance{return parseAppearance(JSON.stringify({theme:input('appearance-theme').value,accent:input('appearance-accent').value,font:input('appearance-font').value,sidebar:input('appearance-sidebar').value,tool:input('appearance-tool').value,width:input('appearance-width').value,notes:input('appearance-notes').checked,compact:input('appearance-compact').checked,path:input('appearance-path').checked}))}
