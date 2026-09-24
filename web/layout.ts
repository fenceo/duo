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
 return `<details class="task-item-menu"><summary aria-label="任务操作" title="任务操作">⋯</summary><div><button data-task-action="copy-link" data-task-id="${escapeHTML(t.id)}">复制任务链接</button><button data-task-action="rename" data-task-id="${escapeHTML(t.id)}">改名…</button><button data-task-action="pin" data-task-id="${escapeHTML(t.id)}">${t.pinned?'取消置顶':'置顶'}</button><button data-task-action="archive" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>${t.archived?'恢复任务':'归档'}</button><button class="danger" data-task-action="trash" data-task-id="${escapeHTML(t.id)}"${busy?' disabled':''}>删除会话…</button></div></details>`;
}
function workspaceTaskList(items:Task[]):string{
 return groupWorkspaceTasks(items).map(g=>`<section class="workspace-group"><button class="workspace-heading" data-workspace="${escapeHTML(g.key)}" aria-expanded="${!collapsedWorkspaces.has(g.key)}" title="${escapeHTML(g.environment+' · '+g.path)}"><span class="folder-arrow">${collapsedWorkspaces.has(g.key)?'›':'⌄'}</span><span class="folder-name">${escapeHTML(g.name)}</span><small>${escapeHTML(g.environment)}</small></button><div class="workspace-tasks ${collapsedWorkspaces.has(g.key)?'hidden':''}">${g.tasks.map(t=>`<div class="task-row"><button class="task ${t.id===chosen?'selected':''}" data-task="${escapeHTML(t.id)}" title="${escapeHTML(t.title)}"><strong>${t.pinned?'↑ ':''}${escapeHTML(t.title)}</strong><small class="task-state ${t.status==='running'||t.status==='queued'?'active':''}">${t.archived?'已归档':names[t.status]||escapeHTML(t.status)}</small></button>${taskItemMenu(t)}</div>`).join('')}</div></section>`).join('');
}
function installLayout(){
 let theme='light';try{theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{}document.documentElement.dataset.theme=theme;
 element('settings-open').insertAdjacentHTML('afterend','<button id="theme-toggle" class="subtle" title="切换浅色 / 深色外观">外观</button>');
 const footer=element('settings-open').parentElement!;footer.id='sidebar-footer';
 const utilities=document.createElement('nav');utilities.className='sidebar-utilities';utilities.setAttribute('aria-label','工作台设置');
 for(const id of ['settings-open','theme-toggle','sidebar-close','logout'])utilities.append(element(id));
 footer.prepend(utilities);element('connection').setAttribute('role','status');
 button('theme-toggle').onclick=()=>{const next=document.documentElement.dataset.theme==='light'?'dark':'light';document.documentElement.dataset.theme=next;try{localStorage.setItem('jianzuo-theme',next)}catch{}};
 const tabs=element('tabs');tabs.prepend(element('conversation-filter'));button('chat-tab').classList.add('hidden');
 for(const id of ['conversation-results','conversation-all'])button(id).addEventListener('click',()=>{if(matchMedia('(max-width:760px)').matches)switchTab('chat')});
 // Keep frequent tools one click away; notes and knowledge live in the same dock.
 element('task-model').insertAdjacentHTML('beforebegin','<details class="tools-menu"><summary>更多</summary><div id="more-tools"></div></details>');
 for(const id of ['note-tab','scratch-tab'])element('more-tools').append(element(id));
 element('more-tools').addEventListener('click',()=>element('more-tools').parentElement!.removeAttribute('open'));
 button('files-tab').textContent='文件';button('hardware-tab').textContent='硬件';
 const model=element('task-model');element('composer').querySelector('.composer-bottom')!.prepend(model);element('task-model-button').title='本任务使用的 AI 工具、模型和推理强度';
 element('bind-open').insertAdjacentHTML('beforebegin','<button id="task-link-copy" title="复制当前 Duo 任务链接">复制链接</button>');button('task-link-copy').onclick=()=>void copyTaskLink();
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
   if(kind==='copy-link')void copyTaskLink(id);else if(kind==='rename')void openTaskRename(id);else if(kind==='pin')void changeTaskPreference('pinned',id);else if(kind==='archive')void changeTaskPreference('archived',id);else if(kind==='trash')void trashCurrentTask(id);
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
