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
function workspaceTaskList(items:Task[]):string{
 return groupWorkspaceTasks(items).map(g=>`<section class="workspace-group"><button class="workspace-heading" data-workspace="${escapeHTML(g.key)}" aria-expanded="${!collapsedWorkspaces.has(g.key)}" title="${escapeHTML(g.environment+' · '+g.path)}"><span class="folder-arrow">${collapsedWorkspaces.has(g.key)?'›':'⌄'}</span><span class="folder-name">${escapeHTML(g.name)}</span><small>${escapeHTML(g.environment)}</small></button><div class="workspace-tasks ${collapsedWorkspaces.has(g.key)?'hidden':''}">${g.tasks.map(t=>`<button class="task ${t.id===chosen?'selected':''}" data-task="${escapeHTML(t.id)}" title="${escapeHTML(t.title)}"><strong>${t.pinned?'↑ ':''}${escapeHTML(t.title)}</strong><small class="task-state ${t.status==='running'||t.status==='queued'?'active':''}">${t.archived?'已归档':names[t.status]||escapeHTML(t.status)}</small></button>`).join('')}</div></section>`).join('');
}
function installLayout(){
 let theme='light';try{theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{}document.documentElement.dataset.theme=theme;
 element('settings-open').insertAdjacentHTML('afterend','<button id="theme-toggle" class="subtle" title="切换浅色 / 深色外观">外观</button>');
 button('theme-toggle').onclick=()=>{const next=document.documentElement.dataset.theme==='light'?'dark':'light';document.documentElement.dataset.theme=next;try{localStorage.setItem('jianzuo-theme',next)}catch{}};
 element('task-actions').insertAdjacentHTML('beforeend','<details class="task-menu"><summary aria-label="任务操作">⋯</summary><div id="task-menu-items"></div></details>');
 for(const id of ['rename','task-pin','task-archive','bind-open'])element('task-menu-items').append(element(id));
 element('task-menu-items').addEventListener('click',()=>{element<HTMLDetailsElement>('task-menu-items').parentElement!.removeAttribute('open')});
 const tabs=element('tabs');tabs.prepend(element('conversation-filter'));button('chat-tab').classList.add('hidden');
 for(const id of ['conversation-results','conversation-all'])button(id).addEventListener('click',()=>{if(matchMedia('(max-width:760px)').matches)switchTab('chat')});
 // Keep frequent tools one click away; notes and knowledge live in the same dock.
 element('task-model').insertAdjacentHTML('beforebegin','<details class="tools-menu"><summary>更多</summary><div id="more-tools"></div></details>');
 for(const id of ['note-tab','scratch-tab'])element('more-tools').append(element(id));
 element('more-tools').addEventListener('click',()=>element('more-tools').parentElement!.removeAttribute('open'));
 button('files-tab').textContent='文件';button('hardware-tab').textContent='硬件';
 const model=element('task-model');element('composer').querySelector('.composer-bottom')!.prepend(model);model.title='本任务使用的 AI 工具、模型和推理强度';
 element('composer').querySelector('.composer-bottom small')!.remove();input('message').title='Enter 发送，Shift + Enter 换行';
 element('composer-wrap').querySelector('.footnote')!.remove();
 element('task-list').addEventListener('click',e=>{const target=(e.target as HTMLElement).closest<HTMLElement>('[data-workspace]');if(!target)return;const key=target.dataset.workspace!;if(collapsedWorkspaces.has(key))collapsedWorkspaces.delete(key);else collapsedWorkspaces.add(key);try{localStorage.setItem('jianzuo-folders-v1',JSON.stringify([...collapsedWorkspaces]))}catch{}renderList()});
 element('task-title').textContent='今天，从哪件事开始？';element('task-workspace').textContent='Windows · WSL · SSH';
 element('conversation').querySelector('.empty')!.innerHTML='<div class="empty-mark">简</div><h2>一件事，一个任务。</h2><p>把目标交给 Codex 或 Claude，<br>在网页和飞书继续，留下可复用的经验。</p><button class="primary" id="empty-new">＋ 新建任务</button>';
}
// Apply before authentication to avoid a dark login screen flashing first.
try{document.documentElement.dataset.theme=localStorage.getItem('jianzuo-theme')==='dark'?'dark':'light'}catch{document.documentElement.dataset.theme='light'}
