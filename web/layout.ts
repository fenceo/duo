type WorkspaceGroup={key:string;name:string;environment:string;path:string;tasks:Task[]};
const collapsedWorkspaces=new Set<string>();
try{const saved=JSON.parse(localStorage.getItem('jianzuo-folders-v1')||'[]');if(Array.isArray(saved))saved.filter(x=>typeof x==='string').forEach(x=>collapsedWorkspaces.add(x))}catch{}
function groupWorkspaceTasks(items:Task[]):WorkspaceGroup[]{
 const groups=new Map<string,WorkspaceGroup>();
 for(const task of [...items].sort((a,b)=>Number(b.pinned)-Number(a.pinned)||b.updated-a.updated)){
  const env=task.environment,path=task.workspace;
  // The environment identity is part of the key: two machines can have /work.
  const key=JSON.stringify([env?.id||'',env?.type||'',env?.host||'',env?.distro||'',env?.user||'',path]);
  let group=groups.get(key);if(!group){group={key,name:path.replace(/[\\/]+$/,'').split(/[\\/]/).at(-1)||path,environment:env?.name||'本机',path,tasks:[]};groups.set(key,group)}group.tasks.push(task);
 }
 return [...groups.values()];
}
function taskItemMenu(t:Task):string{
 const busy=!t.archived&&(t.status==='running'||t.status==='queued');
 return `<details class="task-item-menu"><summary aria-label="任务操作" title="任务操作">⋯</summary><div><button data-task-action="handoff" data-task-id="${escapeHTML(t.id)}">切换工具继续</button><button data-task-action="copy-link" data-task-id="${escapeHTML(t.id)}">复制任务链接</button><button data-task-action="rename" data-task-id="${escapeHTML(t.id)}">改名…</button><button data-task-action="pin" data-task-id="${escapeHTML(t.id)}">${t.pinned?'取消置顶':'置顶'}</button><button data-task-action="archive" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>${t.archived?'恢复任务':'归档'}</button><button class="danger" data-task-action="trash" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>删除会话…</button></div></details>`;
}
function workspaceTaskList(items:Task[]):string{
 return groupWorkspaceTasks(items).map(g=>`<section class="workspace-group"><button class="workspace-heading" data-workspace="${escapeHTML(g.key)}" aria-expanded="${!collapsedWorkspaces.has(g.key)}" title="${escapeHTML(g.environment+' · '+g.path)}"><span class="folder-arrow">${collapsedWorkspaces.has(g.key)?'›':'⌄'}</span><span class="folder-name">${escapeHTML(g.name)}</span><small>${escapeHTML(g.environment)}</small></button><div class="workspace-tasks ${collapsedWorkspaces.has(g.key)?'hidden':''}">${g.tasks.map(t=>`<div class="task-row"><button class="task ${t.id===chosen?'selected':''}" data-task="${escapeHTML(t.id)}" title="${escapeHTML(t.title)}"><strong>${t.pinned?'↑ ':''}${escapeHTML(t.title)}</strong><small class="task-state ${t.status==='running'||t.status==='queued'?'active':''}">${t.archived?'已归档':names[t.status]||escapeHTML(t.status)}</small></button>${taskItemMenu(t)}</div>`).join('')}</div></section>`).join('');
}
function installLayout(){
 let theme='light';try{theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{}document.documentElement.dataset.theme=theme;
 element('settings-open').insertAdjacentHTML('afterend','<button id="theme-toggle" class="subtle">外观</button>');
 const footer=element('settings-open').parentElement!;
 const utilities=document.createElement('div');utilities.className='header-utilities';utilities.append(element('task-actions'));
 const menu=document.createElement('details');menu.id='app-menu';menu.className='app-menu';
 menu.innerHTML='<summary id="app-menu-toggle" aria-label="工作台设置与工具" title="工作台设置与工具"><svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6"><path d="m9.5 3-.6 2-2 .9-2-.5-2 3.5 1.4 1.5v2.2L3 14.2l2 3.5 2-.5 2 .9.6 2h4.2l.6-2 2-.9 2 .5 2-3.5-1.4-1.5v-2.2L21 9l-2-3.5-2 .5-2-.9-.6-2Z"/><circle cx="12" cy="12" r="3"/></svg></summary><nav id="app-menu-items" aria-label="工作台工具"></nav>';
 for(const id of ['settings-open','theme-toggle','logout'])menu.querySelector('nav')!.append(element(id));
 utilities.append(menu);document.querySelector('.header')!.append(utilities);
 const brand=document.querySelector('#sidebar .brand')!;brand.innerHTML='<div class="logo" aria-label="Duo">D</div><strong>Duo</strong><button type="button" id="app-version" title="查看版本与更新">版本</button>';
 brand.append(element('sidebar-close'));button('sidebar-close').textContent='×';button('sidebar-close').setAttribute('aria-label','收起任务列表');
 button('app-version').textContent=appVersion?'v'+appVersion:'版本';button('app-version').onclick=async()=>{await openSettings();showSettingsSection('updates')};
 const connection=element('connection');connection.textContent='';connection.classList.add('hidden');connection.setAttribute('role','status');utilities.prepend(connection);footer.remove();
 menu.addEventListener('click',e=>{if((e.target as HTMLElement).closest('button')){menu.open=false;element('sidebar').classList.remove('open')}});
 listenWithShell(document,'click',e=>{if(!menu.contains(e.target as Node))menu.open=false});
 listenWithShell(document,'keydown',e=>{if((e as KeyboardEvent).key==='Escape'&&menu.open){menu.open=false;element('app-menu-toggle').focus()}});
 button('theme-toggle').onclick=()=>{const next=document.documentElement.dataset.theme==='light'?'dark':'light';document.documentElement.dataset.theme=next;try{localStorage.setItem('jianzuo-theme',next)}catch{}};
 const tabs=element('tabs');tabs.prepend(element('conversation-filter'));button('chat-tab').classList.add('hidden');
 for(const id of ['conversation-results','conversation-all'])button(id).addEventListener('click',()=>{if(matchMedia('(max-width:760px)').matches)switchTab('chat')});
 // Task knowledge is a primary tool, alongside files and the terminal.
 tabs.append(element('note-tab'));
 button('files-tab').textContent='文件';button('hardware-tab').textContent='硬件';
 const model=element('task-model');element('composer').querySelector('.composer-bottom')!.prepend(model);element('task-model-button').title='本任务使用的 AI 工具、模型和推理强度';
 element('bind-open').insertAdjacentHTML('beforebegin','<button id="task-handoff" title="把已保存的对话和知识交给新的 AI 工具">切换继续</button><button id="task-link-copy" title="复制当前 Duo 任务链接">复制链接</button>');button('task-handoff').onclick=()=>void openHandoff();button('task-link-copy').onclick=()=>void copyTaskLink();
 const sessionMenu=document.createElement('details');sessionMenu.id='task-session-menu';sessionMenu.className='session-menu';
 sessionMenu.innerHTML='<summary id="task-session-toggle" aria-label="会话信息与操作"><span id="task-session-label">会话</span><span aria-hidden="true">⌄</span></summary><div class="session-menu-content"><div class="session-info"><strong id="session-summary"></strong><p id="session-location"></p><p id="session-description"></p><p id="session-context" class="hidden"></p></div><div class="session-menu-actions"><button type="button" id="session-reset" class="hidden">新建会话</button></div></div>';
 element('task-actions').append(sessionMenu);
 const sessionActions=sessionMenu.querySelector('.session-menu-actions')!;
 for(const id of ['task-handoff','task-link-copy','bind-open','scratch-tab'])sessionActions.append(element(id));
 button('scratch-tab').textContent='本任务待办';button('session-reset').onclick=()=>void resetSession();
 sessionMenu.addEventListener('click',e=>{const target=(e.target as HTMLElement).closest('button');if(target&&!target.disabled)sessionMenu.open=false});
 listenWithShell(document,'click',e=>{if(!sessionMenu.contains(e.target as Node))sessionMenu.open=false});
 listenWithShell(document,'keydown',e=>{if((e as KeyboardEvent).key==='Escape'&&sessionMenu.open){sessionMenu.open=false;element('task-session-toggle').focus()}});
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
   if(kind==='handoff')void openHandoff(id);else if(kind==='copy-link')void copyTaskLink(id);else if(kind==='rename')void openTaskRename(id);else if(kind==='pin')void changeTaskPreference('pinned',id);else if(kind==='archive')void changeTaskPreference('archived',id);else if(kind==='trash')void trashCurrentTask(id);
   return;
  }
  const target=node.closest<HTMLElement>('[data-workspace]');if(!target)return;const key=target.dataset.workspace!;if(collapsedWorkspaces.has(key))collapsedWorkspaces.delete(key);else collapsedWorkspaces.add(key);try{localStorage.setItem('jianzuo-folders-v1',JSON.stringify([...collapsedWorkspaces]))}catch{}renderList()});
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
