function taskItemMenu(t:Task):string{
 const busy=!t.archived&&(t.status==='running'||t.status==='queued');
 return `<details class="task-item-menu"><summary aria-label="任务操作" title="任务操作">⋯</summary><div><button data-task-action="handoff" data-task-id="${escapeHTML(t.id)}">切换 AI 继续</button><button data-task-action="session-info" data-task-id="${escapeHTML(t.id)}">会话与外部指令</button><button data-task-action="copy-link" data-task-id="${escapeHTML(t.id)}">复制任务链接</button><button data-task-action="rename" data-task-id="${escapeHTML(t.id)}">改名…</button><button data-task-action="pin" data-task-id="${escapeHTML(t.id)}">${t.pinned?'取消置顶':'置顶'}</button><button data-task-action="archive" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>${t.archived?'恢复任务':'归档'}</button><button class="danger" data-task-action="trash" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>删除会话…</button></div></details>`;
}
function workspaceTaskList(items:Task[]):string{
 return [...items].sort((a,b)=>Number(b.pinned)-Number(a.pinned)||b.updated-a.updated).map(t=>{
  const env=t.environment,environment=env?.name||'本机',location=[environment,env?.type,env?.distro,env?.host,env?.user,t.workspace].filter(Boolean).join(' · ');
  const compact=env?.type==='windows'?'Win':env?.type==='wsl'?'WSL':env?.type==='ssh'?'SSH':environment;
  const folder=t.workspace.replace(/[\\/]+$/,'').split(/[\\/]/).at(-1)||t.workspace;
  return `<div class="task-row"><button class="task ${t.id===chosen?'selected':''}" data-task="${escapeHTML(t.id)}" title="${escapeHTML(t.title+'\n'+location)}" aria-label="${escapeHTML(t.title+'，'+location)}"><strong>${t.pinned?'↑ ':''}${escapeHTML(t.title)}</strong><span class="task-path" aria-hidden="true">${escapeHTML(folder)}</span><small class="task-environment"><span class="task-environment-name">${escapeHTML(environment)}</span><span class="task-environment-short" aria-hidden="true">${escapeHTML(compact)}</span></small></button>${taskItemMenu(t)}</div>`;
 }).join('');
}
function installLayout(){
 let theme='light';try{theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{}document.documentElement.dataset.theme=theme;
 element('settings-open').insertAdjacentHTML('afterend','<button id="theme-toggle" class="subtle">外观</button>');
 const footer=element('settings-open').parentElement!;
 const utilities=document.createElement('div');utilities.className='header-utilities';utilities.append(element('task-actions'));
 const globalActions=document.createElement('nav');globalActions.id='app-menu-items';globalActions.className='header-global-actions';globalActions.setAttribute('aria-label','工作台设置与工具');
 for(const id of ['settings-open','theme-toggle','logout']){const control=element(id);control.classList.remove('subtle');globalActions.append(control)}
 utilities.append(globalActions);document.querySelector('.header')!.append(utilities);
 const brand=document.querySelector('#sidebar .brand')!;brand.innerHTML='<div class="logo" aria-label="Duo">D</div><strong>Duo</strong><button type="button" id="app-version" title="查看版本与更新">版本</button>';
 brand.append(element('sidebar-close'));button('sidebar-close').textContent='×';button('sidebar-close').setAttribute('aria-label','收起任务列表');
 button('app-version').textContent=appVersion?'v'+appVersion:'版本';button('app-version').onclick=async()=>{await openSettings();showSettingsSection('updates')};
 const connection=element('connection');connection.textContent='';connection.classList.add('hidden');connection.setAttribute('role','status');utilities.prepend(connection);footer.remove();
 globalActions.addEventListener('click',e=>{if((e.target as HTMLElement).closest('button'))element('sidebar').classList.remove('open')});
 button('theme-toggle').onclick=()=>{const next=document.documentElement.dataset.theme==='light'?'dark':'light';document.documentElement.dataset.theme=next;try{localStorage.setItem('jianzuo-theme',next)}catch{}};
 const tabs=element('tabs');tabs.prepend(element('conversation-filter'));element('conversation-filter').prepend(button('chat-tab'));button('chat-tab').textContent='对话';
 // Task knowledge is a primary tool, alongside files and the terminal.
 tabs.append(element('note-tab'));
 button('files-tab').textContent='文件';button('hardware-tab').textContent='硬件';
 const model=element('task-model');element('composer').querySelector('.composer-bottom')!.prepend(model);element('task-model-button').title='本任务使用的 AI 工具、模型和推理强度';
 element('bind-open').insertAdjacentHTML('beforebegin','<button id="task-handoff" title="切换引擎、账号/API 和模型，接续当前任务">切换 AI</button>');button('task-handoff').onclick=()=>void openHandoff();
 const sessionActions=element('task-actions');sessionActions.classList.add('header-session-actions');sessionActions.setAttribute('role','group');sessionActions.setAttribute('aria-label','当前会话操作');
 sessionActions.insertAdjacentHTML('afterbegin','<button type="button" id="session-reset" class="hidden">新建会话</button>');
 for(const id of ['task-handoff','bind-open','scratch-tab'])sessionActions.append(element(id));
 element('root').insertAdjacentHTML('beforeend','<dialog id="session-info-dialog" aria-labelledby="session-info-title"><h2 id="session-info-title">会话信息</h2><div class="session-info"><strong id="session-summary"></strong><p id="session-location"></p><p id="session-description"></p><p id="session-context" class="hidden"></p></div><div class="dialog-footer"><button type="button" id="session-info-close">关闭</button></div></dialog>');
 button('scratch-tab').textContent='本任务待办';button('session-reset').onclick=()=>void resetSession();
 button('session-info-close').onclick=()=>element<HTMLDialogElement>('session-info-dialog').close();
 element('composer').querySelector('.composer-bottom small')!.remove();input('message').title='Enter 发送，Shift + Enter 换行';
 element('composer-wrap').querySelector('.footnote')!.remove();
 element('task-list').addEventListener('click',e=>{
  const node=e.target as HTMLElement;
  const menu=node.closest<HTMLDetailsElement>('.task-item-menu');
  if(menu){
   const action=node.closest<HTMLButtonElement>('[data-task-action]');
   if(!action)return;
   menu.removeAttribute('open');
   if(action.disabled)return;
   const id=action.dataset.taskId||'',kind=action.dataset.taskAction;
   if(kind==='handoff')void openHandoff(id);else if(kind==='session-info')void openSessionInfo(id);else if(kind==='copy-link')void copyTaskLink(id);else if(kind==='rename')void openTaskRename(id);else if(kind==='pin')void changeTaskPreference('pinned',id);else if(kind==='archive')void changeTaskPreference('archived',id);else if(kind==='trash')void trashCurrentTask(id);
   return;
  }
 });
 // 任务菜单挂在可滚动列表里会被裁切，打开时改成贴合按钮的固定定位。
 const closeTaskMenus=()=>element('task-list').querySelectorAll<HTMLDetailsElement>('.task-item-menu[open]').forEach(d=>d.removeAttribute('open'));
 element('task-list').addEventListener('toggle',e=>{
  const details=e.target as HTMLDetailsElement;if(!details.classList.contains('task-item-menu'))return;
  const panel=details.querySelector<HTMLElement>('div');if(!panel)return;
  if(!details.open){panel.classList.remove('shown');return}
  const box=details.getBoundingClientRect(),height=panel.offsetHeight,width=panel.offsetWidth;
  const top=box.bottom+height+12>window.innerHeight&&box.top>height+12?box.top-height-6:box.bottom+6;
  panel.style.left=Math.round(Math.max(8,Math.min(window.innerWidth-width-8,box.right-width)))+'px';panel.style.top=Math.round(top)+'px';panel.classList.add('shown');
 },true);
 element('task-list').addEventListener('scroll',closeTaskMenus,{passive:true});
 listenWithShell(document,'click',e=>{if(!(e.target as HTMLElement).closest('.task-item-menu'))closeTaskMenus()});
 listenWithShell(document,'keydown',e=>{if((e as KeyboardEvent).key==='Escape')closeTaskMenus()});
 element('task-title').textContent='今天，从哪件事开始？';element('task-workspace').textContent='Windows · WSL · SSH';
 element('conversation').querySelector('.empty')!.innerHTML='<div class="empty-mark">D</div><h2>一件事，一个任务。</h2><p>把目标交给 Codex、Claude Code 或 DeepSeek Harness，<br>在网页和飞书继续，留下可复用的经验。</p><button class="primary" id="empty-new">＋ 新建任务</button>';
}
// Apply before authentication to avoid a dark login screen flashing first.
try{document.documentElement.dataset.theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{document.documentElement.dataset.theme='light'}

async function openSessionInfo(id=chosen){if(!id)return;if(chosen!==id)await choose(id);if(chosen===id&&detail){renderSessionBanner();element<HTMLDialogElement>('session-info-dialog').showModal()}}
