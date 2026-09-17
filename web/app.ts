type Environment={id:string;name:string;type:"windows"|"wsl"|"ssh";distro:string;user:string;host:string;port:number;identity:string;codex:string;claude?:string;claude_model?:string;default_engine?:string;model:string;model_cache:string;workspaces:string[]};
type Task={mode?:WorkMode;deleted?:boolean;engine:string;reasoning_effort:string;pinned:boolean;archived:boolean;environment:Environment;id:string;title:string;workspace:string;model:string;session:string;status:string;updated:number};
type Run={started?:number;usage?:{input:number;output:number;cached:number;cache_write:number;total:number};mode?:WorkMode;attachments?:Attachment[];id:string;kind:string;status:string;result:string;error:string;source:string;created:number;finished?:number};
type EventRecord={seq:number;run_id:string;kind:string;text:string;created:number};
type Detail={task:Task;runs:Run[];events:EventRecord[];chat:string};
type Note={content:string;revision:number;updated:number};
type Configuration={access?:{lan:string;tailscale:string};environments:Environment[];default_environment:string;listen:string;distro:string;user:string;codex:string;model:string;workspaces:string[];feishu:{enabled:boolean;app_id:string;secret?:string;owner?:string}};
type Settings={config:Configuration;secret_configured:boolean;feishu_status:string;chat:string};
const element=<T extends HTMLElement=HTMLElement>(id:string)=>document.getElementById(id) as T;
const input=(id:string)=>element<HTMLInputElement>(id);
const button=(id:string)=>element<HTMLButtonElement>(id);
const escapeHTML=(v:string)=>v.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]||c));
const names:Record<string,string>={idle:'待开始',queued:'排队中',running:'执行中',done:'等待下一步',failed:'执行失败',interrupted:'已停止'};
let csrf='',tasks:Task[]=[],settings:Settings,chosen='',detail:Detail|null=null,note:Note={content:'',revision:0,updated:0};
let sequence=0,selection=0,dirty=false,sending=false,polling=false,authenticated=false,refreshList=0,lastList='',noticeTimer:ReturnType<typeof setTimeout>;
const drafts=new Map<string,string>();
let editingEnvironments:Environment[]=[],editingID="",modelRequest=0;
function notify(text:string){element('notice').textContent=text;element('notice').classList.add('show');clearTimeout(noticeTimer);noticeTimer=setTimeout(()=>element('notice').classList.remove('show'),6500)}
async function api<T=any>(path:string,method='GET',data?:unknown):Promise<T>{
 const response=await fetch('/api/'+path,{method,credentials:'same-origin',cache:'no-store',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:data===undefined?undefined:JSON.stringify(data)});
 const result=await response.json().catch(()=>({error:'服务返回内容异常'}));
 if(!response.ok){if(response.status===401&&path!=='login'){authenticated=false;showLogin()}throw new Error(result.error||'请求失败')}
 return result;
}
function markdown(text:string):string{
 const chunks=text.split(/```[^\n]*\n([\s\S]*?)```/g);
 return chunks.map((part,i)=>i%2?'<pre><code>'+escapeHTML(part)+'</code></pre>':escapeHTML(part).split(/\n\s*\n/).map(block=>{
  const inline=(s:string)=>s.replace(/`([^`]+)`/g,'<code>$1</code>').replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>');
  if(/^#{1,3} /.test(block)){const level=Math.min(3,block.match(/^#+/)![0].length);return `<h${level}>${inline(block.replace(/^#+ /,''))}</h${level}>`}
  if(block.split('\n').every(l=>/^[-*] /.test(l)))return '<ul>'+block.split('\n').map(l=>'<li>'+inline(l.slice(2))+'</li>').join('')+'</ul>';
  return '<p>'+inline(block).replace(/\n/g,'<br>')+'</p>';
 }).join('')).join('');
}
function showLogin(){
 closeHardwareView();closeDiscovery();closeTaskTerminals();resetConversation();chosen='';detail=null;authenticated=false;
 element('root').innerHTML=`<div class="login-shell"><form class="login" id="login"><div class="brand"><div class="logo">简</div><div><strong>简作</strong><small>LOCAL TASK WORKSPACE</small></div></div><h1>回来，接着做。</h1><p>你的任务、执行过程和解决办法，<br>都留在这里。</p><label for="password">访问密码</label><input id="password" type="password" autocomplete="current-password" required placeholder="输入工作台密码"><p id="login-error" class="error"></p><button class="primary" id="login-submit">进入工作台 →</button></form></div>`;
 element('login').onsubmit=async e=>{e.preventDefault();button('login-submit').disabled=true;try{const r=await api('login','POST',{password:input('password').value});csrf=r.csrf;await boot()}catch(error){element('login-error').textContent=(error as Error).message}finally{if(element('login-submit'))button('login-submit').disabled=false}};
}
async function boot(){
 const status=await api<{authenticated:boolean;csrf:string}>('auth');if(!status.authenticated){showLogin();return}csrf=status.csrf;
 [tasks,settings,workCatalog]=await Promise.all([api<Task[]>('tasks'),api<Settings>('settings'),api<WorkCatalog>('workbench')]);authenticated=true;renderShell();renderList();
 const query=new URLSearchParams(location.search),id=query.get('task');if(id&&tasks.some(t=>t.id===id))await choose(id,query.get('view')==='note'?'note':'chat');
 if(query.get('settings')==='access'){await openSettings();document.querySelector<HTMLButtonElement>('[data-settings="access"]')?.click()}
}
function renderShell(){
 lastList='';
 element('root').innerHTML=`<div class="app"><aside class="sidebar" id="sidebar"><div class="brand"><div class="logo">简</div><div><strong>简作</strong><small>LOCAL TASK WORKSPACE</small></div></div><button class="primary" id="new-task">＋ 新建任务</button><input id="search" placeholder="查找任务" aria-label="查找任务"><div class="task-list" id="task-list"></div><div class="sidebar-footer"><button class="subtle" id="settings-open">设置</button><button class="mobile-menu subtle" id="sidebar-close">收起</button><span id="connection">本机服务已连接</span><button class="subtle" id="logout">退出</button></div></aside><main><header class="header"><div class="actions"><button class="mobile-menu" id="menu" aria-label="展开任务列表">☰</button><div><h1 id="task-title">把事情做完，把经验留下。</h1><p id="task-workspace">独立工作台 · 本地 AI 工具</p></div></div><div class="actions hidden" id="task-actions"><button id="rename">改名</button><button id="bind-open">飞书连接</button></div></header><nav class="tabs hidden" id="tabs"><button id="chat-tab" class="selected">对话与执行</button><button id="note-tab">任务知识</button><span class="model-picker" id="task-model"><button type="button" id="task-model-button" aria-expanded="false" aria-haspopup="listbox" title="本任务使用的 AI 工具、模型和推理强度"><span id="task-model-label"></span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="task-model-menu" role="listbox"><input id="task-model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="task-model-list" class="model-list"></div></div></span></nav><section id="conversation" class="conversation"><div class="empty"><div class="eyebrow">ONE TASK. KEEP GOING.</div><h2>从一个具体目标开始。</h2><p>选好本地目录，把要求交给 AI 工具。<br>在网页或飞书继续同一个任务，<br>再把有用的解决办法留在任务里。</p><button class="primary" id="empty-new">创建一个任务 →</button></div></section><section id="notebook" class="notebook hidden"><div class="note-head"><div><h2>任务知识</h2><p id="note-status">一份任务，一份可复用的记录。</p></div><div class="actions"><button id="summarize">整理任务知识</button><button id="export-note">导出</button><button class="primary" id="save-note">保存</button></div></div><div id="draft-banner" class="draft-banner hidden"><span id="draft-label">知识草稿 · 未保存</span><div class="actions"><button id="adopt-draft">编辑草稿</button><button id="save-draft" class="primary">保存草稿</button></div></div><details id="draft-preview" class="knowledge-preview hidden" open><summary>草稿预览</summary><div id="draft-content" class="content"></div></details><textarea id="note-content" aria-label="任务知识内容" placeholder="# 任务知识&#10;&#10;## 问题&#10;&#10;## 解决办法&#10;&#10;## 验证结果&#10;&#10;## 未解决事项"></textarea><small class="muted">Markdown 格式 · 整理只生成草稿，保存由你决定。</small></section><section id="composer-wrap" class="composer-wrap hidden"><div id="run-status" class="run-status"></div><form id="composer" class="composer"><textarea id="message" rows="2" aria-label="任务要求" placeholder="下一步，要做什么？"></textarea><div class="composer-bottom"><small>Enter 发送<br>Shift + Enter 换行</small><div class="actions"><button type="button" id="stop" class="hidden">停止</button><button class="primary" id="send">发送 ↑</button></div></div></form><div class="footnote">在服务所在电脑执行 · 保留所选工具的原生会话</div></section></main></div>
 <dialog id="create-dialog"><form id="create-form"><h2>新建任务</h2><p>给一个具体的目标，其余在对话中继续。</p><label for="create-title">任务名称</label><input id="create-title" required maxlength="180" placeholder="例如：检查设备启动日志"><label for="create-input">任务要求</label><textarea id="create-input" rows="4" required placeholder="描述希望完成的事情"></textarea><label for="create-environment">执行环境</label><select id="create-environment"></select><label for="create-workspace">工作目录</label><input id="create-workspace" list="workspace-options" required autocomplete="off" placeholder="输入该环境中已有目录的绝对路径"><datalist id="workspace-options"></datalist><p>可直接修改路径，也可选择常用或最近使用的目录。</p><label for="create-engine">AI 工具</label><select id="create-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select><label for="create-effort">推理强度</label><select id="create-effort"><option value="">工具默认</option></select><p class="muted" id="effort-hint"></p><label for="model-picker-button">模型</label><div class="model-picker" id="model-picker"><button type="button" id="model-picker-button" aria-expanded="false" aria-haspopup="listbox"><span id="model-picker-label">使用此工具的默认模型</span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="model-menu" role="listbox"><input id="model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="model-list" class="model-list"></div></div></div><input id="create-model" type="hidden"><input id="custom-model" class="hidden" placeholder="输入自定义模型名称" aria-label="自定义模型"><p id="models-hint"></p><button type="button" id="reload-models">重读模型列表</button><p>AI 工具可在选定工作目录内读写文件。请填写所选环境中的已有目录，常用目录可在设置中管理。</p><p class="error" id="create-error"></p><div class="dialog-footer"><button type="button" data-close="create-dialog">取消</button><button class="primary" id="create-submit">创建并执行</button></div></form></dialog>
 <dialog id="settings-dialog"><form id="settings-form"><h2>工作台设置</h2><p>独立程序、独立数据。使用各环境中 Codex / Claude Code 的登录状态。</p><h3 class="section-title">执行环境</h3><p>任务保存自己的环境。这里的修改只影响之后新建的任务。</p><label for="default-environment">默认环境（飞书新建任务也使用它）</label><select id="default-environment"></select><label for="environment-picker">编辑环境</label><select id="environment-picker"></select><div class="actions environment-actions"><button type="button" id="add-wsl">新增 WSL</button><button type="button" id="add-windows">新增 Windows</button><button type="button" id="add-ssh">新增 SSH</button><button type="button" id="remove-environment" class="danger">删除环境</button></div><div class="form-grid"><div><label for="environment-name">环境名称</label><input id="environment-name"></div><div><label for="environment-type">执行方式</label><select id="environment-type"><option value="wsl">WSL</option><option value="windows">本机 Windows</option><option value="ssh">SSH · Linux 主机</option></select></div></div><div id="wsl-fields"><label for="setting-distro">WSL 发行版</label><input id="setting-distro"></div><div id="linux-user"><label for="setting-user">执行用户名（可留空使用默认用户）</label><input id="setting-user"></div><div id="ssh-fields"><div class="form-grid"><div><label for="setting-host">SSH 主机 / SSH 配置别名</label><input id="setting-host" placeholder="例如：192.168.50.20"></div><div><label for="setting-port">SSH 端口</label><input id="setting-port" type="number" min="1" max="65535"></div></div><label for="setting-identity">私钥文件（服务电脑上的路径，可留空）</label><input id="setting-identity"><p>支持密钥或 ssh-agent。请先在运行简作的 Windows 用户下用 ssh 登录该主机，确认指纹并配置免密登录。远端需要所选 AI 工具和 Python 3。</p></div><label for="setting-codex">此环境中的 Codex 可执行文件</label><input id="setting-codex"><label for="setting-model">此环境的默认模型（可留空）</label><input id="setting-model"><label for="setting-workspaces">工作目录（每行一个绝对路径）</label><textarea id="setting-workspaces" rows="3"></textarea><p><button type="button" id="check-codex">检查已保存的当前环境</button></p><div id="check-result" class="settings-result"></div><h3 class="section-title">飞书私聊</h3><button type="button" id="setup-feishu">扫码创建并绑定机器人</button><p>首次使用可扫码自动创建，无需填写凭据。已有机器人也可使用下面的手工配置。</p><p>使用飞书自建应用的长连接。开启机器人，订阅 im.message.receive_v1，授予接收私聊消息和以机器人发送消息的权限。</p><p>若沿用原机器人，启用前先关闭它在其他程序中的连接。</p><label for="feishu-id">App ID</label><input id="feishu-id" autocomplete="off"><label for="feishu-secret">App Secret</label><input id="feishu-secret" type="password" autocomplete="new-password" placeholder="留空保留已保存的密钥"><label class="check-row"><input id="feishu-enabled" type="checkbox">启用飞书长连接</label><p id="feishu-state"></p><p>选中任务后直接发要求，每轮在同一张卡片中更新进展和结果。长结果可通过卡片的局域网或 Tailscale 入口查看。</p><p id="feishu-owner"></p><button type="button" id="pair-code">生成配对码</button><p id="pair-result" class="pair"></p><p>首次连接后，使用你的飞书向机器人发送配对命令。配对码十分钟有效，只有配对的账号可以操作任务。</p><p class="error" id="settings-error"></p><div class="dialog-footer"><button type="button" data-close="settings-dialog">关闭</button><button class="primary" id="settings-save">保存设置</button></div></form></dialog>
 <dialog id="bind-dialog"><h2>在飞书继续这个任务</h2><p id="bind-status"></p><p>连接后，从网页或飞书发来的要求进入同一个任务；该任务的完成结果会发送到此私聊。</p><div class="dialog-footer"><button data-close="bind-dialog">关闭</button><button id="bind-setup">扫码创建并绑定当前任务</button><button id="unbind">断开当前任务</button><button class="primary" id="bind">连接此任务</button></div></dialog>
 <dialog id="setup-dialog"><h2>扫码连接飞书</h2><p>确认后将创建“简作助手”，绑定扫码账号，初始化“我的任务、整理知识”菜单并提交发布，最后发送一条绑定通知。已有机器人不会被修改。</p><p>请在飞书官方页面审阅并确认权限；企业审批可能影响发布。</p><div id="setup-display"><p>点击下方按钮生成二维码。</p></div><div class="dialog-footer"><button id="setup-close">关闭</button><button id="setup-cancel" class="hidden">取消等待</button><button class="primary" id="setup-start">生成飞书二维码</button></div></dialog>`;
 installTools();installWorkspace();installConversationFilter();installSettingsSections();installProductivity();installTerminal();installExecution();installDiscovery();installLayout();installEnvironmentDiscovery();installWorkflow();installStickyBoard();installAppearance();installPanelLayout();installUpdates();
 button('new-task').onclick=showCreate;button('empty-new').onclick=showCreate;button('menu').onclick=()=>element('sidebar').classList.toggle('open');
 element('root').querySelectorAll<HTMLElement>('[data-close]').forEach(b=>b.onclick=()=>element<HTMLDialogElement>(b.dataset.close!).close());
 button('logout').onclick=async()=>{if(!mayLeave())return;try{await api('logout','POST',{});showLogin()}catch(e){notify((e as Error).message)}};
 button('sidebar-close').onclick=()=>element('sidebar').classList.remove('open');button('settings-open').onclick=openSettings;button('chat-tab').onclick=()=>switchTab('chat');button('note-tab').onclick=()=>switchTab('note');
 input('create-environment').onchange=()=>void loadCreateEnvironment();button('reload-models').onclick=()=>loadCreateEnvironment(true);
 element('create-form').onsubmit=createTask;element('composer').onsubmit=e=>{e.preventDefault();void send(input('message').value,true)};
 input('message').oninput=()=>{drafts.set(chosen,input('message').value);renderWorkflow()};input('message').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();element<HTMLFormElement>('composer').requestSubmit()}};

 button('rename').onclick=openTaskRename;
 input('note-content').oninput=()=>{dirty=true;element('note-status').textContent='有未保存的修改'};button('save-note').onclick=saveNote;
 button('export-note').onclick=()=>{if(dirty){notify('请先保存修改，再导出。');return}location.href='/api/tasks/'+chosen+'/note?download=1'};
 button('save-draft').onclick=()=>void saveKnowledgeDraft();
 button('summarize').onclick=async()=>{switchTab('note');button('summarize').disabled=true;try{await api('tasks/'+chosen+'/summarize','POST',{});notify('正在整理，完成后会显示草稿');await poll()}catch(e){notify((e as Error).message)}finally{if(button('summarize'))button('summarize').disabled=false}};
 button('adopt-draft').onclick=()=>{const r=latestKnowledge();if(!r)return;if(dirty&&!confirm('用整理结果替换当前未保存的草稿？'))return;input('note-content').value=r.result;dirty=true;element('note-status').textContent='已采用整理草稿，请检查后保存'};
 input('environment-picker').onchange=()=>{storeEnvironmentEditor();editingID=input('environment-picker').value;loadEnvironmentEditor()};input('environment-type').onchange=showEnvironmentFields;button('add-wsl').onclick=()=>addEnvironment('wsl');button('add-windows').onclick=()=>addEnvironment('windows');button('add-ssh').onclick=()=>addEnvironment('ssh');button('remove-environment').onclick=removeEnvironment;
 element('settings-form').onsubmit=saveSettings;button('check-codex').onclick=async()=>{button('check-codex').disabled=true;try{const r=await api('check','POST',{environment_id:editingID});element('check-result').textContent=(r.ok?'检查通过\n':'检查失败\n')+r.output}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-codex').disabled=false}};
 button('pair-code').onclick=async()=>{try{const r=await api('feishu/pair','POST',{});element('pair-result').textContent='向机器人发送：\n/配对 '+r.code}catch(e){notify((e as Error).message)}};
 button('setup-feishu').onclick=()=>openSetup('');button('bind-setup').onclick=()=>openSetup(chosen);button('setup-start').onclick=startSetup;button('setup-close').onclick=()=>element<HTMLDialogElement>('setup-dialog').close();button('setup-cancel').onclick=cancelSetup;button('bind-open').onclick=openBinding;button('bind').onclick=()=>setBinding(true);button('unbind').onclick=()=>setBinding(false);
}
function mayLeave(){return !dirty||confirm('任务知识尚未保存。确定放弃未保存的修改？')}
function renderList(){
 if(!element('task-list'))return;
 const query=input('search').value.trim(),archived=tasks.filter(t=>t.archived).length;
 button('tasks-active').textContent='任务 · '+(tasks.length-archived);button('tasks-archived').textContent='已归档 · '+archived;
 for(const view of ['active','archived'])button('tasks-'+view).classList.toggle('selected',taskView===view&&!query);
 element('search-summary').classList.toggle('hidden',!query);button('search-clear').classList.toggle('hidden',!query);
 let html='';
 if(query){
  element('search-summary').textContent=searchError||(searchPending?'正在搜索全部任务…':`共 ${searchHits?.length||0} 条匹配 · 含归档`+(searchLimited?' · 最多 60 条，请缩小关键词':''));
  html=(searchHits||[]).map((h,i)=>`<button class="task search-hit" data-hit="${i}"><small>${({task:'任务',message:'对话',note:'知识',scratch:'便签'} as Record<string,string>)[h.kind]}${h.archived?' · 已归档':''}</small><strong>${escapeHTML(h.title)}</strong><span>${escapeHTML(h.snippet)}</span></button>`).join('')||'<p class="muted">'+(searchPending?'读取中…':'没有匹配的内容。')+'</p>';
 }else html=workspaceTaskList(tasks.filter(t=>t.archived===(taskView==='archived')))||'<p class="muted">'+(taskView==='archived'?'没有已归档的任务。':'还没有任务。')+'</p>';
 if(html===lastList)return;lastList=html;element('task-list').innerHTML=html;
 element('task-list').querySelectorAll<HTMLElement>('[data-task]').forEach(b=>b.onclick=()=>void choose(b.dataset.task!));
 element('task-list').querySelectorAll<HTMLElement>('[data-hit]').forEach(b=>b.onclick=()=>{const hit=searchHits?.[Number(b.dataset.hit)];if(hit)void openSearchHit(hit)});
}
async function choose(id:string,view='chat'){
 if(!mayLeave())return;chosen=id;const token=++selection;detail=null;dirty=false;sequence=0;resetConversation();void loadStickyBoard();
 element('conversation').innerHTML='<p id="loading" class="muted">正在读取任务记录…</p>';input('message').value=drafts.get(id)||'';input('note-content').value='';input('note-content').disabled=true;button('save-note').disabled=true;
 for(const key of ['tabs','task-actions','composer-wrap','conversation-filter'])element(key).classList.remove('hidden');element('sidebar').classList.remove('open');switchTab('chat');renderList();
 history.replaceState(null,'','/?task='+id);
 try{const [d,n]=await Promise.all([api<Detail>('tasks/'+id),api<Note>('tasks/'+id+'/note')]);if(token!==selection)return;detail=d;note=n;element('conversation').innerHTML='';appendEvents(d.events);input('note-content').value=n.content;input('note-content').disabled=false;button('save-note').disabled=false;element('note-status').textContent=n.revision?'已保存 · 版本 '+n.revision:'尚未保存知识';renderTask();switchTab(view);}
 catch(e){if(token===selection)notify((e as Error).message)}
}
function switchTab(tab:string){
 toolsTab=tab;const open=tab!=='chat';
 if(chosen){const query=new URLSearchParams({task:chosen});if(tab==='note')query.set('view','note');history.replaceState(null,'','/?'+query)}
 element('workspace').classList.toggle('tool-open',open);element('workspace').classList.toggle('tool-full',open&&toolFullscreen);
 element('tool-dock').classList.toggle('hidden',!open);element('conversation').classList.remove('hidden');element('composer-wrap').classList.toggle('hidden',!chosen);
 for(const [key,panel] of [['note','notebook'],['scratch','scratch-panel'],['hardware','hardware-panel'],['files','files-panel'],['terminal','terminal-panel']]){element(panel).classList.toggle('hidden',tab!==key);button(key+'-tab').classList.toggle('selected',tab===key);button(key+'-tab').setAttribute('aria-pressed',String(tab===key))}
 button('chat-tab').classList.toggle('selected',!open);element('tool-title').textContent=({note:'任务知识',scratch:'便签',hardware:'硬件调试',files:'文件与改动',terminal:'终端'} as Record<string,string>)[tab]||'任务工具';
 renderTerminal();renderHardwareState();if(tab!=='hardware')cancelHardwareInput();if(tab==='files')void loadFiles();if(tab==='scratch')void loadScratch();if(tab==='hardware')void loadDevices();
}

function latestKnowledge(){return detail?.runs.filter(r=>r.kind==='knowledge'&&r.status==='done'&&r.result).at(-1)}
function renderTask(){
 if(!detail)return;const t=detail.task;element('task-title').textContent=t.title;element('task-workspace').textContent=(t.environment?.name?t.environment.name+' · ':'')+t.workspace;element('task-workspace').title=element('task-workspace').textContent;const modelLabel=[taskEngineName(t.engine),t.model||'默认模型',effortLabels[t.reasoning_effort]||''].filter(Boolean).join(' · ');element('task-model-label').textContent=modelLabel;element('task-model-button').title=modelLabel;
 const active=detail.runs.some(r=>['running','queued'].includes(r.status));const queued=detail.runs.filter(r=>r.status==='queued').length;const latest=detail.runs.at(-1);
 element('run-status').textContent=active?'正在执行'+(queued?' · '+queued+' 条要求排队中':''):latest?.error||names[t.status]||t.status;
 element('run-status').classList.toggle('error',!active&&!!latest?.error);button('stop').classList.toggle('hidden',!active);button('send').disabled=sending;input('message').placeholder=active?'追加要求将排队，也可以停止当前执行…':'下一步，要做什么？';
 const knowledge=latestKnowledge(),pending=!!knowledge&&knowledge.result!==note.content;element('draft-banner').classList.toggle('hidden',!pending);element('draft-preview').classList.toggle('hidden',!pending);button('note-tab').textContent=pending?'任务知识 · 草稿':'任务知识';button('summarize').disabled=active;
 if(knowledge&&pending){const preview=element('draft-content');if(preview.dataset.run!==knowledge.id){preview.innerHTML=markdown(knowledge.result);preview.dataset.run=knowledge.id}const changed=note.updated>knowledge.created;element('draft-label').textContent=changed?'知识已更新，可编辑合并草稿':'知识草稿 · 未保存';button('save-draft').disabled=changed}
 button('task-pin').textContent=t.pinned?'取消置顶':'置顶';button('task-pin').disabled=false;button('task-archive').textContent=t.archived?'恢复任务':'归档';button('task-archive').disabled=active;input('message').disabled=t.archived;button('send').disabled=sending||t.archived;button('summarize').disabled=active||t.archived;if(t.archived)element('run-status').textContent='任务已归档，记录保留；恢复后可以继续执行。';
 renderWorkflow();renderTerminal();const i=tasks.findIndex(x=>x.id===t.id);if(i>=0)tasks[i]=t;renderList();
}
function appendEvents(events:EventRecord[]){
 const container=element('conversation'),nearBottom=container.scrollHeight-container.scrollTop-container.clientHeight<100;
 for(const ev of events){if(ev.seq<=sequence)continue;sequence=ev.seq;const node=document.createElement('div');node.dataset.event=String(ev.seq);
  if(ev.kind==='user'||ev.kind==='assistant'){node.className='message '+ev.kind;node.innerHTML='<div class="label">'+(ev.kind==='user'?'你':taskEngineName(detail?.task.engine))+'</div><div class="content">'+(ev.kind==='user'?escapeHTML(ev.text):markdown(ev.text))+'</div>'}
  else if(ev.kind==='tool'||ev.kind==='log'){node.className='log';node.innerHTML='<details><summary>'+escapeHTML(ev.text.split('\n')[0].slice(0,200))+'</summary><pre>'+escapeHTML(ev.text)+'</pre></details>'}
  else {node.className='progress'+(ev.kind==='error'?' error':'');node.textContent=ev.kind==='status'?'本轮执行 · '+(names[ev.text.trim()]||ev.text):ev.text}
  container.append(node);conversationItems.push({event:ev,node});
 }
 applyConversationFilter();
 if(nearBottom)container.scrollTop=container.scrollHeight;
}
async function poll(){
 if(!authenticated||polling||document.hidden)return;polling=true;const id=chosen,token=selection;
 try{if(Date.now()-refreshList>4000){tasks=await api<Task[]>('tasks');refreshList=Date.now();renderList()}
  if(id){const [d,n]=await Promise.all([api<Detail>('tasks/'+id+'?after='+sequence),api<Note>('tasks/'+id+'/note')]);if(token!==selection)return;detail=d;if(!dirty&&n.revision>note.revision){note=n;input('note-content').value=n.content;element('note-status').textContent='已保存 · 版本 '+n.revision}appendEvents(d.events);renderTask()}
  if(element('connection'))element('connection').textContent='本机服务已连接';
 }catch(e){if(element('connection'))element('connection').textContent='连接中断，正在重试'}finally{polling=false}
}
async function showCreate(){
 try{settings=await api<Settings>('settings');input('create-title').value='';input('create-input').value='';input('create-files').value='';element<HTMLSelectElement>('create-environment').innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${escapeHTML(e.type.toUpperCase())}</option>`).join('');input('create-environment').value=settings.config.default_environment;element('create-error').textContent='';element<HTMLDialogElement>('create-dialog').showModal();input('create-title').focus();await loadCreateEnvironment()}catch(e){notify((e as Error).message)}
}
async function loadCreateEnvironment(keepWorkspace=false){
 const token=++modelRequest,env=settings.config.environments.find(e=>e.id===input('create-environment').value);if(!env)return;
 if(!keepWorkspace)input('create-engine').value=env.default_engine||'codex';
 const engine=input('create-engine').value,defaultModel=engine==='claude'?(env.claude_model||''):env.model;
 input('create-effort').value='';
 const directories=[...new Set([...env.workspaces,...tasks.filter(t=>t.environment?.id===env.id).map(t=>t.workspace)])];element('workspace-options').innerHTML=directories.map(p=>`<option value="${escapeHTML(p)}"></option>`).join('');if(!keepWorkspace)input('create-workspace').value=env.workspaces[0]||'';setCreateModelsLoading();button('create-submit').disabled=true;element('models-hint').textContent='读取 '+env.name+' 的模型列表…';
 let models:EngineModel[]=[],hint='';
 try{const result=await api<{models:EngineModel[];modified:number;message?:string;source:string}>('environments/'+env.id+'/models?engine='+engine);models=result.models||[];hint=result.message||result.source+(result.modified?' · '+new Date(result.modified).toLocaleString():'')}
 catch(e){hint=(e as Error).message+'。可以选择默认模型或手动指定。'}
 if(token!==modelRequest)return;
 if(defaultModel&&!models.some(m=>m.id===defaultModel))models.push({id:defaultModel,name:defaultModel+'（配置的默认模型）'});createModels=models;
 setCreateModels(models,defaultModel);button('create-submit').disabled=false;element('models-hint').textContent=hint;
}
async function createTask(e:Event){
 e.preventDefault();if(!mayLeave())return;button('create-submit').disabled=true;let created='';const text=input('create-input').value,files=Array.from(input('create-files').files||[]);
 try{validateAttachmentFiles(files);const r=await api('tasks','POST',{engine:input('create-engine').value,reasoning_effort:input('create-effort').value,environment_id:input('create-environment').value,title:input('create-title').value,workspace:input('create-workspace').value,model:input('create-model').value==='__custom__'?input('custom-model').value.trim():input('create-model').value,mode_id:input('create-mode').value});created=r.task.id;tasks.unshift(r.task);drafts.set(created,text);attachmentDrafts.set(created,[]);
  for(const file of files){const attachment=await uploadTaskFile(created,file);attachmentDrafts.get(created)!.push(attachment)}
  if(text.trim()||files.length){await api('tasks/'+created+'/messages','POST',{content:text.trim()||'请查看这些附件。',mode_id:input('create-mode').value,attachment_ids:attachmentDrafts.get(created)!.map(f=>f.id)});drafts.delete(created);attachmentDrafts.delete(created)}
  dirty=false;element<HTMLDialogElement>('create-dialog').close();await choose(created);
 }catch(error){if(created){dirty=false;element<HTMLDialogElement>('create-dialog').close();await choose(created);notify('任务已创建，尚未开始：'+(error as Error).message)}else element('create-error').textContent=(error as Error).message}finally{button('create-submit').disabled=false}
}
async function send(text:string,clear:boolean){
 const files=[...(attachmentDrafts.get(chosen)||[])];if(!chosen||!detail||sending||uploadingTasks.has(chosen)||(!text.trim()&&!files.length))return;const id=chosen,original=input('message').value,mode=selectedMessageMode();sending=true;renderTask();
 try{await api('tasks/'+id+'/messages','POST',{content:text.trim()||'请查看这些附件。',mode_id:mode,attachment_ids:files.map(f=>f.id)});attachmentDrafts.set(id,(attachmentDrafts.get(id)||[]).filter(f=>!files.some(sent=>sent.id===f.id)));if(clear&&chosen===id&&input('message').value===original){input('message').value='';drafts.delete(id)}await poll()}catch(e){notify((e as Error).message)}finally{sending=false;if(chosen===id)renderTask()}
}
async function saveNote(){
 const id=chosen,content=input('note-content').value;button('save-note').disabled=true;
 try{const saved=await api<Note>('tasks/'+id+'/note','PUT',{content,revision:note.revision});if(id!==chosen)return;note=saved;dirty=input('note-content').value!==content;element('note-status').textContent=dirty?'有未保存的修改':'已保存 · 版本 '+note.revision;renderTask();notify('任务知识已保存')}
 catch(e){notify((e as Error).message)}finally{if(button('save-note'))button('save-note').disabled=false}
}
async function saveKnowledgeDraft(){
 const r=latestKnowledge(),id=chosen;if(!r)return;
 if(dirty&&!confirm('保存整理结果并替换当前未保存的编辑？'))return;
 const before=input('note-content').value;button('save-draft').disabled=true;
 try{const saved=await api<Note>('tasks/'+id+'/note/adopt','POST',{run_id:r.id,revision:note.revision});if(id!==chosen)return;note=saved;dirty=input('note-content').value!==before;if(!dirty)input('note-content').value=saved.content;element('note-status').textContent=dirty?'有未保存的修改':'已保存 · 版本 '+saved.revision;renderTask();notify('知识已保存')}
 catch(e){notify((e as Error).message)}finally{if(chosen===id)renderTask()}
}
async function openSettings(){
 element('settings-saved').textContent='';
 try{settings=await api<Settings>('settings');const c=settings.config;loadAccessSettings();editingEnvironments=JSON.parse(JSON.stringify(c.environments));editingID=c.default_environment;environmentPickers(c.default_environment);loadEnvironmentEditor();input('feishu-id').value=c.feishu.app_id;input('feishu-secret').value='';input('feishu-secret').placeholder=settings.secret_configured?'已保存，留空保持不变':'填写自建应用 App Secret';input('feishu-enabled').checked=c.feishu.enabled;updateFeishuStatus();element('check-result').textContent='';element('settings-error').textContent='';element<HTMLDialogElement>('settings-dialog').showModal()}catch(e){notify((e as Error).message)}
}
function environmentPickers(defaultID?:string){
 const preferred=defaultID||input('default-environment').value||settings.config.default_environment;
 const options=editingEnvironments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${e.type.toUpperCase()}</option>`).join('');input('environment-picker').innerHTML=options;input('environment-picker').value=editingID;input('default-environment').innerHTML=options;input('default-environment').value=editingEnvironments.some(e=>e.id===preferred)?preferred:editingEnvironments[0].id;button('remove-environment').disabled=editingEnvironments.length<2;
}
function loadEnvironmentEditor(){
 const e=editingEnvironments.find(e=>e.id===editingID);if(!e)return;
 input('environment-name').value=e.name;input('environment-type').value=e.type;input('setting-distro').value=e.distro||'';input('setting-user').value=e.user||'';input('setting-host').value=e.host||'';input('setting-port').value=String(e.port||22);input('setting-identity').value=e.identity||'';input('setting-claude').value=e.claude||'';input('setting-claude-model').value=e.claude_model||'';input('setting-engine').value=e.default_engine||'codex';input('setting-codex').value=e.codex;input('setting-model').value=e.model;input('setting-workspaces').value=e.workspaces.join('\n');element('check-result').textContent='';showEnvironmentFields();
}
function showEnvironmentFields(){
 const type=input('environment-type').value;element('wsl-fields').classList.toggle('hidden',type!=='wsl');element('ssh-fields').classList.toggle('hidden',type!=='ssh');element('linux-user').classList.toggle('hidden',type==='windows');input('setting-port').disabled=type!=='ssh';
}
function storeEnvironmentEditor(){
 const e=editingEnvironments.find(e=>e.id===editingID);if(!e)return;
 Object.assign(e,{name:input('environment-name').value.trim(),type:input('environment-type').value,distro:input('setting-distro').value.trim(),user:input('setting-user').value.trim(),host:input('setting-host').value.trim(),port:Number(input('setting-port').value)||22,identity:input('setting-identity').value.trim(),claude:input('setting-claude').value.trim(),claude_model:input('setting-claude-model').value.trim(),default_engine:input('setting-engine').value,codex:input('setting-codex').value.trim(),model:input('setting-model').value.trim(),workspaces:input('setting-workspaces').value.split('\n').map(s=>s.trim()).filter(Boolean)});
}
function addEnvironment(type:Environment['type']){
 storeEnvironmentEditor();const id='env_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,6);const e:Environment={id,name:type==='ssh'?'新 SSH 环境':type==='wsl'?'新 WSL 环境':'新 Windows 环境',type,distro:'',user:'',host:'',port:22,identity:'',codex:type==='windows'?'codex.exe':'codex',model:'',model_cache:'',workspaces:[]};editingEnvironments.push(e);editingID=id;environmentPickers();loadEnvironmentEditor();input('environment-name').focus();
}
function removeEnvironment(){
 if(editingEnvironments.length<2)return;if(!confirm('删除这个环境配置？已有任务仍使用各自保存的环境。'))return;editingEnvironments=editingEnvironments.filter(e=>e.id!==editingID);editingID=editingEnvironments[0].id;environmentPickers();loadEnvironmentEditor();
}
function updateFeishuStatus(){element('feishu-state').textContent='状态：'+settings.feishu_status;element('feishu-owner').textContent=settings.config.feishu.owner?'已配对你的飞书账号':'尚未配对';button('pair-code').disabled=!!settings.config.feishu.owner;button('setup-feishu').disabled=!!settings.config.feishu.app_id||settings.secret_configured}
async function saveSettings(e:Event){
 e.preventDefault();button('settings-save').disabled=true;element('settings-saved').textContent='';
 storeEnvironmentEditor();
 const c:Configuration={...settings.config,access:{lan:input("access-lan").value.trim(),tailscale:input("access-tailscale").value.trim()},environments:editingEnvironments,default_environment:input('default-environment').value,feishu:{enabled:input('feishu-enabled').checked,app_id:input('feishu-id').value.trim(),secret:input('feishu-secret').value.trim()}};
 try{settings=await api<Settings>('settings','PUT',c);input('feishu-secret').value='';updateFeishuStatus();element('settings-error').textContent='';element('settings-saved').textContent='设置已保存';notify('设置已保存')}catch(e){element('settings-error').textContent=(e as Error).message}finally{button('settings-save').disabled=false}
}
async function openBinding(){
 try{settings=await api<Settings>('settings');element('bind-status').textContent=settings.chat?'已配对你的飞书私聊。'+(detail?.chat?'当前任务已连接。':'点击连接后可继续此任务。'):'可扫码创建机器人并绑定此任务，或到设置配对已有应用。';button('bind-setup').classList.toggle('hidden',!!settings.config.feishu.app_id||settings.secret_configured);button('bind').disabled=!settings.chat;button('unbind').disabled=!detail?.chat;element<HTMLDialogElement>('bind-dialog').showModal()}catch(e){notify((e as Error).message)}
}
async function setBinding(bind:boolean){
 try{await api('tasks/'+chosen+'/bind',bind?'POST':'DELETE',bind?{chat:settings.chat}:{});element<HTMLDialogElement>('bind-dialog').close();notify(bind?'已连接到此任务':'已断开此任务');await poll()}catch(e){notify((e as Error).message)}
}
window.addEventListener('beforeunload',e=>{if(dirty){e.preventDefault();e.returnValue=''}});
document.addEventListener('visibilitychange',()=>{if(!document.hidden)void poll()});
setInterval(()=>void poll(),1000);
setInterval(async()=>{if(!authenticated||!element<HTMLDialogElement>('settings-dialog')?.open)return;try{settings=await api<Settings>('settings');updateFeishuStatus()}catch{}},5000);
void boot().catch(e=>{element('root').innerHTML='<div class="empty"><h2>暂时无法连接工作台</h2><p>'+escapeHTML(e.message)+'</p><button id="retry">重新连接</button></div>';button('retry').onclick=()=>location.reload()});

type SetupState={id:string;phase:string;message:string;qr?:string;url?:string;expires:number;task_id?:string;menu:string;connection:string};
let setupID=sessionStorage.getItem('feishu-setup')||'',setupTask='',setupPolling=false,setupApplied='';
function openSetup(task:string){
 setupTask=task;element<HTMLDialogElement>('bind-dialog').close();element<HTMLDialogElement>('settings-dialog').close();element<HTMLDialogElement>('setup-dialog').showModal();
 if(setupID)void pollSetup();
}
function renderSetup(s:SetupState){
 const busy=['starting','scanning','configuring'].includes(s.phase);
 element('setup-display').innerHTML=`<p role="status">${escapeHTML(s.message)}</p>${s.qr?`<img class="feishu-qr" src="${escapeHTML(s.qr)}" alt="使用飞书扫描此二维码创建机器人"><p>有效至 ${new Date(s.expires).toLocaleTimeString()}</p><a href="${escapeHTML(s.url||'')}" target="_blank" rel="noreferrer noopener">在当前设备打开飞书授权</a>`:''}<p>菜单：${escapeHTML(s.menu)}</p><p>连接：${escapeHTML(s.connection)}</p>`;
 button('setup-start').classList.toggle('hidden',busy||s.phase==='complete'||s.phase==='partial');button('setup-start').textContent='重新生成二维码';button('setup-cancel').classList.toggle('hidden',!['starting','scanning'].includes(s.phase));
}
async function startSetup(){
 button('setup-start').disabled=true;
 try{const s=await api<SetupState>('feishu/setup','POST',{task_id:setupTask});setupID=s.id;sessionStorage.setItem('feishu-setup',s.id);renderSetup(s)}catch(e){element('setup-display').textContent=(e as Error).message}finally{button('setup-start').disabled=false}
}
async function cancelSetup(){try{await api('feishu/setup/'+setupID+'/cancel','POST',{});await pollSetup()}catch(e){notify((e as Error).message)}}
async function pollSetup(){
 if(!authenticated||!setupID||setupPolling||!element<HTMLDialogElement>('setup-dialog')?.open)return;setupPolling=true;
 try{const s=await api<SetupState>('feishu/setup/'+setupID);renderSetup(s);if(['complete','partial'].includes(s.phase)&&setupApplied!==s.id){setupApplied=s.id;settings=await api<Settings>('settings');input('feishu-id').value=settings.config.feishu.app_id;input('feishu-secret').value='';input('feishu-enabled').checked=settings.config.feishu.enabled;updateFeishuStatus();await poll()}}
 catch(e){element('setup-display').textContent=(e as Error).message;button('setup-start').classList.remove('hidden');button('setup-cancel').classList.add('hidden');setupID='';sessionStorage.removeItem('feishu-setup')}
 finally{setupPolling=false}
}
setInterval(()=>void pollSetup(),2000);
