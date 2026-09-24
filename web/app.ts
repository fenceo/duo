/// <reference path="./codex-approvals.ts" />
type EnvironmentModel={id:string;name:string;engine?:string;reasoning_levels?:string[];default_reasoning?:string};
type Environment={id:string;name:string;type:"windows"|"wsl"|"ssh";distro:string;user:string;host:string;port:number;identity:string;codex:string;claude?:string;harness?:string;harness_model?:string;harness_provider?:string;claude_model?:string;default_engine?:string;model:string;model_cache:string;models?:EnvironmentModel[];workspaces:string[]};
type Task={mode?:WorkMode;deleted?:boolean;engine:string;reasoning_effort:string;pinned:boolean;archived:boolean;environment:Environment;id:string;title:string;workspace:string;model:string;session:string;status:string;updated:number};
type Run={started?:number;usage?:{input:number;output:number;cached:number;cache_write:number;total:number};mode?:WorkMode;attachments?:Attachment[];id:string;kind:string;status:string;result:string;error:string;source:string;created:number;finished?:number};
type EventRecord={seq:number;run_id:string;kind:string;text:string;created:number};
type EngineRuntime={state:'new'|'live'|'busy'|'closed';can_continue:boolean;reason:string};
type Detail={task:Task;runs:Run[];events:EventRecord[];approvals?:CodexPendingRequest[];chat:string;session_started?:number;runtime?:EngineRuntime};
type ContextFile={name:string;label:string};
type Knowledge={id:string;task_id:string;title:string;content:string;status:string;source:string;run_id:string;revision:number;created:number;updated:number};
type Configuration={access?:{lan:string;tailscale:string};environments:Environment[];default_environment:string;listen:string;distro:string;user:string;codex:string;model:string;workspaces:string[];feishu:{enabled:boolean;app_id:string;secret?:string;owner?:string}};
type Settings={config:Configuration;data_dir:string;data_switch_supported?:boolean;secret_configured:boolean;feishu_status:string;chat:string};
type HandoffPreview={source_task_id:string;source_engine:string;source_title:string;transferred_runs:number;transferred_knowledge:number;context:string;context_truncated:boolean};
const element=<T extends HTMLElement=HTMLElement>(id:string)=>document.getElementById(id) as T;
const input=(id:string)=>element<HTMLInputElement>(id);
const button=(id:string)=>element<HTMLButtonElement>(id);
const escapeHTML=(v:string)=>v.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]||c));
const names:Record<string,string>={idle:'待开始',queued:'排队中',running:'执行中',done:'等待下一步',failed:'执行失败',interrupted:'已停止'};
// Keep environment choices readable in the task form.  The type is part of
// the label (rather than an opaque ID), and SSH shows its configured endpoint
// so switching environments also makes it obvious where the directory lives.
function environmentTypeLabel(type:Environment['type']){
 return type==='windows'?'Windows':type==='wsl'?'WSL':'SSH';
}
function environmentOptionLabel(environment:Environment){
 const type=environmentTypeLabel(environment.type);
 const target=environment.type==='wsl'&&environment.distro?environment.distro:environment.type==='ssh'&&environment.host?environment.host:environment.name;
 return `${type} · ${target||environment.name}`;
}
function environmentWorkspaceHint(environment:Environment){
 return environment.type==='windows'?'Windows 绝对路径，例如 C:\\Users\\你\\work':environment.type==='wsl'?'WSL 绝对路径，例如 /home/你/work':'SSH 远端绝对路径，例如 /home/你/work';
}
let csrf='',tasks:Task[]=[],settings:Settings,chosen='',detail:Detail|null=null,knowledgeItems:Knowledge[]=[],knowledgeEditing:string|null=null;
let sequence=0,selection=0,dirty=false,sending=false,polling=false,authenticated=false,refreshList=0,lastList='',lastKnowledge='',noticeTimer:ReturnType<typeof setTimeout>;
let taskContext:ContextFile[]=[];
const drafts=new Map<string,string>();
let editingEnvironments:Environment[]=[],editingID="",modelRequest=0;
let createFiles:File[]=[],creatingTask=false,createReturnTask='',createPermission:'request'|'auto'|'full'|'read'='auto';
let createSubmitting=false,sessionResetTask='',settingsPolling=false;
// A mounted login/workbench shell owns its listeners and read-only requests.
// Mutating requests are not implicitly cancelled: the server may have accepted them.
let shellEpoch=0,shellController=new AbortController();
let shellCleanups:Array<()=>void>=[];
function shellCurrent(epoch:number){return epoch===shellEpoch}
function disposeWithShell(cleanup:()=>void){shellCleanups.push(cleanup)}
function listenWithShell(target:EventTarget,type:string,listener:EventListener,options:AddEventListenerOptions={}){
 target.addEventListener(type,listener,{...options,signal:shellController.signal});
}
function renewShellScope(){
 shellController.abort();
 for(const cleanup of shellCleanups.splice(0))try{cleanup()}catch{/* Continue releasing the remaining shell resources. */}
 shellController=new AbortController();shellEpoch++;selection++;modelRequest++;taskModelRequest++;modelTestRequest++;
 polling=false;settingsPolling=false;createSubmitting=false;sending=false;sessionResetTask='';
}
function notify(text:string){element('notice').textContent=text;element('notice').classList.add('show');clearTimeout(noticeTimer);noticeTimer=setTimeout(()=>element('notice').classList.remove('show'),6500)}
function taskLink(id=chosen,view='chat'){
 const url=new URL(location.href);url.search='';url.searchParams.set('task',id);if(view==='note')url.searchParams.set('view','note');url.hash='';return url.toString();
}
async function copyTaskLink(id=chosen){
 if(!id)return;
 const url=taskLink(id);
 try{
  if(navigator.clipboard?.writeText)await navigator.clipboard.writeText(url);
  else{const area=document.createElement('textarea');area.value=url;area.setAttribute('readonly','');area.style.position='fixed';area.style.opacity='0';document.body.append(area);area.select();document.execCommand('copy');area.remove()}
  notify('任务链接已复制；打开后需要在这台 Duo 登录。');
 }catch{notify('复制失败，请手动复制当前地址：'+url)}
}
let handoffPreviewSource='',handoffPreviewReady=false;
function ensureHandoffDialog(){
 if(element('handoff-dialog'))return;
 document.body.insertAdjacentHTML('beforeend',`<dialog id="handoff-dialog"><form id="handoff-form"><h2>切换工具并继续</h2><p>这会创建一个新的执行路线。原任务不会改变；发送给目标引擎的是经过脱敏的知识和对话快照，不包含原生会话或附件。</p><label for="handoff-environment">目标环境</label><select id="handoff-environment"></select><label for="handoff-engine">AI 工具</label><select id="handoff-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><label for="handoff-workspace">工作目录</label><input id="handoff-workspace" required><label for="handoff-model">模型（可留空，沿用目标工具默认）</label><input id="handoff-model" placeholder="目标环境中的模型 ID"><label for="handoff-context-mode">历史范围</label><select id="handoff-context-mode"><option value="recent">已验证知识 + 最近 8 条记录</option><option value="summary">仅已验证知识</option><option value="full">最近记录和知识（限长）</option></select><label for="handoff-message">本次继续要求</label><textarea id="handoff-message" rows="4" required>请先检查当前工作区，再根据历史上下文继续完成任务。</textarea><p id="handoff-preview-status" class="muted" role="status">正在读取脱敏预览…</p><pre id="handoff-preview" class="handoff-preview hidden"></pre><p id="handoff-error" class="error"></p><div class="dialog-footer"><button type="button" id="handoff-cancel">取消</button><button type="button" id="handoff-refresh">重新读取预览</button><button class="primary" id="handoff-submit" disabled>确认并开始</button></div></form></dialog>`);
 element('handoff-form').onsubmit=e=>{e.preventDefault();void submitHandoff()};button('handoff-cancel').onclick=()=>element<HTMLDialogElement>('handoff-dialog').close();button('handoff-refresh').onclick=()=>void loadHandoffPreview();element<HTMLSelectElement>('handoff-context-mode').onchange=()=>void loadHandoffPreview();
 const env=element<HTMLSelectElement>('handoff-environment');env.onchange=()=>{const target=settings.config.environments.find(v=>v.id===env.value);if(!target)return;element<HTMLInputElement>('handoff-workspace').value=target.workspaces[0]||'';updateHandoffModel()};element<HTMLSelectElement>('handoff-engine').onchange=updateHandoffModel;
}
function updateHandoffModel(){const env=settings.config.environments.find(v=>v.id===element<HTMLSelectElement>('handoff-environment').value);if(!env)return;const engine=element<HTMLSelectElement>('handoff-engine').value;element<HTMLInputElement>('handoff-model').value=engineDefaultModel(env,engine)}
async function loadHandoffPreview(){
 const source=handoffPreviewSource;if(!source)return;const status=element('handoff-preview-status'),pre=element('handoff-preview'),submit=button('handoff-submit');submit.disabled=true;status.textContent='正在生成本地脱敏预览…';pre.classList.add('hidden');element('handoff-error').textContent='';
 try{const mode=element<HTMLSelectElement>('handoff-context-mode').value;const p=await api<HandoffPreview>('tasks/'+source+'/continuation/preview?mode='+encodeURIComponent(mode));if(source!==handoffPreviewSource)return;handoffPreviewReady=true;status.textContent=`将转交 ${p.transferred_runs} 条对话、${p.transferred_knowledge} 条知识${p.context_truncated?'（已截断）':''}。请检查下面内容后确认。`;pre.textContent=p.context;pre.classList.remove('hidden');submit.disabled=false}catch(e){handoffPreviewReady=false;status.textContent='无法生成预览';element('handoff-error').textContent=(e as Error).message}
}
async function openHandoff(id=chosen){
 if(!id||!detail)return;ensureHandoffDialog();handoffPreviewSource=id;handoffPreviewReady=false;const env=element<HTMLSelectElement>('handoff-environment');env.innerHTML=settings.config.environments.map(v=>`<option value="${escapeHTML(v.id)}">${escapeHTML(environmentOptionLabel(v))}</option>`).join('');env.value=detail.task.environment?.id||settings.config.default_environment;element<HTMLSelectElement>('handoff-engine').value=detail.task.engine||'codex';element<HTMLInputElement>('handoff-workspace').value=detail.task.workspace||'';element<HTMLInputElement>('handoff-model').value=detail.task.model||'';element<HTMLTextAreaElement>('handoff-message').value='请先检查当前工作区，再根据历史上下文继续完成任务。';element<HTMLDialogElement>('handoff-dialog').showModal();await loadHandoffPreview();
}
async function submitHandoff(){
 if(!handoffPreviewSource||!handoffPreviewReady)return;const engine=element<HTMLSelectElement>('handoff-engine').value,mode=modeForPermission(createPermission,engine);if(!mode||!modeSupportsEngine(mode,engine)){element('handoff-error').textContent='当前权限模式不适用于目标工具，请调整设置后重试。';return}const submit=button('handoff-submit');submit.disabled=true;try{const result=await api<any>('tasks/'+handoffPreviewSource+'/handoff','POST',{confirm:true,environment_id:element<HTMLSelectElement>('handoff-environment').value,engine,workspace:element<HTMLInputElement>('handoff-workspace').value,model:element<HTMLInputElement>('handoff-model').value,reasoning_effort:'',mode_id:mode.id,message:element<HTMLTextAreaElement>('handoff-message').value,context_mode:element<HTMLSelectElement>('handoff-context-mode').value});element<HTMLDialogElement>('handoff-dialog').close();if(result.task){tasks.unshift(result.task);await choose(result.task.id);notify('已创建新的执行路线，原任务保持不变。')}}catch(e){element('handoff-error').textContent=(e as Error).message;submit.disabled=false}}
async function api<T=any>(path:string,method='GET',data?:unknown,signal?:AbortSignal):Promise<T>{
 const epoch=shellEpoch;
 const response=await fetch('/api/'+path,{method,credentials:'same-origin',cache:'no-store',signal,headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:data===undefined?undefined:JSON.stringify(data)});
 const result=await response.json().catch(()=>({error:'服务返回内容异常'}));
 if(!response.ok){if(response.status===401&&path!=='login'&&shellCurrent(epoch)){authenticated=false;showLogin()}throw Object.assign(new Error(result.error||'请求失败'),{status:response.status})}
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
 renewShellScope();
 closeHardwareView();closeDiscovery();closeTaskTerminals();resetConversation();resetCodexApprovals();chosen='';detail=null;authenticated=false;
 element('root').innerHTML=`<div class="login-shell"><form class="login" id="login"><div class="brand"><div class="logo">D</div><div><strong>Duo</strong><small>LOCAL TASK WORKSPACE</small></div></div><h1>回来，接着做。</h1><p>你的任务、执行过程和解决办法，<br>都留在这里。</p><label for="password">访问密码</label><input id="password" type="password" autocomplete="current-password" required placeholder="输入工作台密码"><p id="login-error" class="error"></p><button class="primary" id="login-submit">进入工作台 →</button></form></div>`;
 element('login').onsubmit=async e=>{e.preventDefault();const epoch=shellEpoch;button('login-submit').disabled=true;try{const r=await api('login','POST',{password:input('password').value});if(!shellCurrent(epoch))return;csrf=r.csrf;await boot()}catch(error){if(shellCurrent(epoch)&&element('login-error'))element('login-error').textContent=(error as Error).message}finally{if(shellCurrent(epoch)&&element('login-submit'))button('login-submit').disabled=false}};
}
async function boot(){
 const epoch=shellEpoch,signal=shellController.signal;
 const status=await api<{authenticated:boolean;csrf:string}>('auth','GET',undefined,signal);if(!shellCurrent(epoch))return;if(!status.authenticated){showLogin();return}csrf=status.csrf;
 const loaded=await Promise.all([api<Task[]>('tasks','GET',undefined,signal),api<Settings>('settings','GET',undefined,signal),api<WorkCatalog>('workbench','GET',undefined,signal)]);if(!shellCurrent(epoch))return;
 [tasks,settings,workCatalog]=loaded;authenticated=true;
 // renderShell renews the epoch before mounting feature modules. A synchronous
 // mount failure belongs to that new shell, not the earlier login/API epoch.
 try{renderShell();renderList()}catch(error){showShellFailure(error);return}
 const query=new URLSearchParams(location.search),id=query.get('task');if(id&&tasks.some(t=>t.id===id))await choose(id,query.get('view')==='note'?'note':'chat');
 if(query.get('settings')==='access'){await openSettings();document.querySelector<HTMLButtonElement>('[data-settings="access"]')?.click()}
}
function showShellFailure(error:unknown){
 authenticated=false;renewShellScope();
 const message=error instanceof Error?error.message.slice(0,500):'界面初始化异常，请重新加载。';
 element('root').innerHTML='<div class="login-shell"><section class="login" role="alert"><h1>工作台界面未能加载</h1><p>本机数据不会因此被清除。请重新加载；若仍失败，请把以下提示发给维护者。</p><p class="error">'+escapeHTML(message)+'</p><button type="button" id="shell-retry">重新加载</button></section></div>';
 button('shell-retry').onclick=()=>location.reload();
}
function renderShell(){
 renewShellScope();
 lastList='';
 element('root').innerHTML=`<div class="app"><aside class="sidebar" id="sidebar"><div class="brand"><div class="logo">D</div><div><strong>Duo</strong><small>LOCAL TASK WORKSPACE</small></div></div><button class="primary" id="new-task">＋ 新建任务</button><input id="search" placeholder="查找任务" aria-label="查找任务"><div class="task-list" id="task-list"></div><div class="sidebar-footer"><button class="subtle" id="settings-open">设置</button><button class="mobile-menu subtle" id="sidebar-close">收起</button><span id="connection">本机服务已连接</span><button class="subtle" id="logout">退出</button></div></aside><main><header class="header"><div class="actions"><button class="mobile-menu" id="menu" aria-label="展开任务列表">☰</button><div><h1 id="task-title">把事情做完，把经验留下。</h1><p id="task-workspace">独立工作台 · 本地 AI 工具</p></div></div><div class="actions hidden" id="task-actions"><button id="bind-open">飞书连接</button></div></header><nav class="tabs hidden" id="tabs"><button id="chat-tab" class="selected">对话与执行</button><button id="note-tab">任务知识</button><span class="model-picker" id="task-model"><button type="button" id="task-model-button" aria-expanded="false" aria-haspopup="listbox" title="本任务使用的 AI 工具、模型和推理强度"><span id="task-model-label"></span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="task-model-menu" role="listbox"><input id="task-model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="task-model-list" class="model-list"></div></div></span></nav><div id="session-banner" class="session-banner hidden"></div><section id="conversation" class="conversation"><div class="empty"><div class="eyebrow">ONE TASK. KEEP GOING.</div><h2>从一个具体目标开始。</h2><p>选好本地目录，把要求交给 AI 工具。<br>在网页或飞书继续同一个任务，<br>再把有用的解决办法留在任务里。</p><button class="primary" id="empty-new">创建一个任务 →</button></div></section><section id="notebook" class="notebook hidden"><div class="note-head"><div><h2>任务知识</h2><p id="note-status">一份任务，一份可复用的记录。</p></div><div class="actions"><button class="primary" id="knowledge-new">＋ 新建知识</button><button id="summarize">整理任务知识</button><button id="export-note">导出</button></div></div><nav class="task-views" id="knowledge-filter" aria-label="知识筛选"><button data-knowledge-filter="all" class="selected">全部</button><button data-knowledge-filter="observed">待验证</button><button data-knowledge-filter="verified">已验证</button><button data-knowledge-filter="stale">已过时</button></nav><div id="draft-banner" class="draft-banner hidden"><span id="draft-label">执行总结 · 未保存</span><div class="actions"><button id="adopt-draft">编辑后保存</button><button id="save-draft" class="primary">保存总结</button></div></div><details id="draft-preview" class="knowledge-preview hidden" open><summary>草稿预览</summary><div id="draft-content" class="content"></div></details><div id="knowledge-list" class="knowledge-list"></div></section><section id="composer-wrap" class="composer-wrap hidden"><div id="run-status" class="run-status"></div><form id="composer" class="composer"><textarea id="message" rows="2" aria-label="任务要求" placeholder="下一步，要做什么？"></textarea><div class="composer-bottom"><small>Enter 发送<br>Shift + Enter 换行</small><div class="actions"><button type="button" id="stop" class="hidden">停止</button><button class="primary" id="send">发送 ↑</button></div></div></form><div class="footnote">在服务所在电脑执行 · 保留所选工具的原生会话</div></section></main></div>
 <section id="create-page" class="create-page hidden" aria-labelledby="create-heading"><form id="create-form" class="create-form"><h2 id="create-heading">新建任务</h2><p>给一个具体的目标，其余在对话中继续。</p><label for="create-input">任务要求</label><textarea id="create-input" rows="4" required placeholder="描述希望完成的事情"></textarea><label for="create-environment">执行环境</label><select id="create-environment"></select><label for="create-workspace">工作目录</label><input id="create-workspace" list="workspace-options" required autocomplete="off" placeholder="输入该环境中已有目录的绝对路径"><datalist id="workspace-options"></datalist><p>可直接修改路径，也可选择常用或最近使用的目录。</p><label for="create-engine">AI 工具</label><select id="create-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option></select><label for="create-effort">推理强度</label><select id="create-effort"><option value="">工具默认</option></select><p class="muted" id="effort-hint"></p><label for="model-picker-button">模型</label><div class="model-picker" id="model-picker"><button type="button" id="model-picker-button" aria-expanded="false" aria-haspopup="listbox"><span id="model-picker-label">使用此工具的默认模型</span><span class="model-picker-caret">▾</span></button><div class="model-menu hidden" id="model-menu" role="listbox"><input id="model-search" class="model-search-input" placeholder="搜索或输入模型名称" autocomplete="off"><div id="model-list" class="model-list"></div></div></div><input id="create-model" type="hidden"><input id="custom-model" class="hidden" placeholder="输入自定义模型名称" aria-label="自定义模型"><p id="models-hint"></p><button type="button" id="reload-models">重读模型列表</button><button type="button" id="test-models">测试模型</button><p id="model-test-result" class="model-test-result" role="status"></p><p>AI 工具可在选定工作目录内读写文件。请填写所选环境中的已有目录，常用目录可在设置中管理。</p><p class="error" id="create-error"></p><div class="dialog-footer"><button type="button">取消</button><button class="primary" id="create-submit">创建并执行</button></div></form></section>
 <dialog id="settings-dialog"><form id="settings-form"><h2>工作台设置</h2><p>独立程序、独立数据。使用各环境中 Codex / Claude Code 的登录状态。</p><h3 class="section-title">执行环境</h3><p>任务保存自己的环境。这里的修改只影响之后新建的任务。</p><label for="default-environment">默认环境（飞书新建任务也使用它）</label><select id="default-environment"></select><label for="environment-picker">编辑环境</label><select id="environment-picker"></select><div class="actions environment-actions"><button type="button" id="add-wsl">新增 WSL</button><button type="button" id="add-windows">新增 Windows</button><button type="button" id="add-ssh">新增 SSH</button><button type="button" id="remove-environment" class="danger">删除环境</button></div><div class="form-grid"><div><label for="environment-name">环境名称</label><input id="environment-name"></div><div><label for="environment-type">执行方式</label><select id="environment-type"><option value="wsl">WSL</option><option value="windows">本机 Windows</option><option value="ssh">SSH · Linux 主机</option></select></div></div><div id="wsl-fields"><label for="setting-distro">WSL 发行版</label><input id="setting-distro"></div><div id="linux-user"><label for="setting-user">执行用户名（可留空使用默认用户）</label><input id="setting-user"></div><div id="ssh-fields"><div class="form-grid"><div><label for="setting-host">SSH 主机 / SSH 配置别名</label><input id="setting-host" placeholder="例如：192.168.50.20"></div><div><label for="setting-port">SSH 端口</label><input id="setting-port" type="number" min="1" max="65535"></div></div><label for="setting-identity">私钥文件（服务电脑上的路径，可留空）</label><input id="setting-identity"><p>支持密钥或 ssh-agent。请先在运行Duo的 Windows 用户下用 ssh 登录该主机，确认指纹并配置免密登录。远端需要所选 AI 工具和 Python 3。</p></div><label for="setting-codex">此环境中的 Codex 可执行文件</label><input id="setting-codex"><label for="setting-model">此环境的默认模型（可留空）</label><input id="setting-model"><label for="setting-workspaces">工作目录（每行一个绝对路径）</label><textarea id="setting-workspaces" rows="3"></textarea><p><button type="button" id="check-codex">检查已保存的当前环境</button></p><div id="check-result" class="settings-result"></div><h3 class="section-title">飞书私聊</h3><button type="button" id="setup-feishu">扫码创建并绑定机器人</button><p>首次使用可扫码自动创建，无需填写凭据。已有机器人也可使用下面的手工配置。</p><p>使用飞书自建应用的长连接。开启机器人，订阅 im.message.receive_v1，授予接收私聊消息和以机器人发送消息的权限。</p><p>若沿用原机器人，启用前先关闭它在其他程序中的连接。</p><label for="feishu-id">App ID</label><input id="feishu-id" autocomplete="off"><label for="feishu-secret">App Secret</label><input id="feishu-secret" type="password" autocomplete="new-password" placeholder="留空保留已保存的密钥"><label class="check-row"><input id="feishu-enabled" type="checkbox">启用飞书长连接</label><p id="feishu-state"></p><p>选中任务后直接发要求，每轮在同一张卡片中更新进展和结果。长结果可通过卡片的局域网或 Tailscale 入口查看。</p><p id="feishu-owner"></p><button type="button" id="pair-code">生成配对码</button><p id="pair-result" class="pair"></p><p>首次连接后，使用你的飞书向机器人发送配对命令。配对码十分钟有效，只有配对的账号可以操作任务。</p><p class="error" id="settings-error"></p><div class="dialog-footer"><button type="button" data-close="settings-dialog">关闭</button><button class="primary" id="settings-save">保存设置</button></div></form></dialog>
 <dialog id="bind-dialog"><h2>在飞书继续这个任务</h2><p id="bind-status"></p><p>连接后，从网页或飞书发来的要求进入同一个任务；该任务的完成结果会发送到此私聊。</p><div class="dialog-footer"><button data-close="bind-dialog">关闭</button><button id="bind-setup">扫码创建并绑定当前任务</button><button id="unbind">断开当前任务</button><button class="primary" id="bind">连接此任务</button></div></dialog>
 <dialog id="setup-dialog"><h2>扫码连接飞书</h2><p>确认后将创建“Duo助手”，绑定扫码账号，初始化“我的任务、整理知识”菜单并提交发布，最后发送一条绑定通知。已有机器人不会被修改。</p><p>请在飞书官方页面审阅并确认权限；企业审批可能影响发布。</p><div id="setup-display"><p>点击下方按钮生成二维码。</p></div><div class="dialog-footer"><button id="setup-close">关闭</button><button id="setup-cancel" class="hidden">取消等待</button><button class="primary" id="setup-start">生成飞书二维码</button></div></dialog><dialog id="knowledge-dialog"><form id="knowledge-form"><h2>任务知识</h2><label for="knowledge-title">标题</label><input id="knowledge-title" maxlength="120" placeholder="例如：继电器上电顺序与验证方法"><label for="knowledge-state">状态</label><select id="knowledge-state"><option value="observed">待验证</option><option value="verified">已验证</option><option value="stale">已过时</option></select><label for="knowledge-body">内容</label><textarea id="knowledge-body" rows="12" maxlength="200000" aria-label="知识内容" placeholder="结论、命令、解决办法和验证结果…"></textarea><p id="knowledge-error" class="error"></p><div class="dialog-footer"><button type="button" id="knowledge-cancel">取消</button><button class="primary" id="knowledge-save">保存知识</button></div></form></dialog>`;
 installTools();installWorkspace();installConversationFilter();installSettingsSections();installEngineSettings();installProductivity();installTerminal();installExecution();installDiscovery();installLayout();installEnvironmentDiscovery();installWorkflow();installCodexApprovals();installStickyBoard();installAppearance();installPanelLayout();installUpdates();
 button('new-task').onclick=showCreate;button('empty-new').onclick=showCreate;button('menu').onclick=()=>element('sidebar').classList.toggle('open');
 element('root').querySelectorAll<HTMLElement>('[data-close]').forEach(b=>b.onclick=()=>element<HTMLDialogElement>(b.dataset.close!).close());
 button('logout').onclick=async()=>{if(!mayLeave())return;const epoch=shellEpoch;try{await api('logout','POST',{});if(shellCurrent(epoch))showLogin()}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}};
 button('sidebar-close').onclick=()=>element('sidebar').classList.remove('open');button('settings-open').onclick=openSettings;button('chat-tab').onclick=()=>switchTab('chat');button('note-tab').onclick=()=>switchTab('note');
 input('create-environment').onchange=()=>void loadCreateEnvironment();button('reload-models').onclick=()=>void loadCreateModels(false,true);
 element('create-form').onsubmit=createTask;element('composer').onsubmit=e=>{e.preventDefault();void send(input('message').value,true)};
 input('message').oninput=()=>{drafts.set(chosen,input('message').value);renderWorkflow()};input('message').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();element<HTMLFormElement>('composer').requestSubmit()}};

  button('knowledge-new').onclick=()=>editKnowledge(null);button('export-note').onclick=()=>{location.href='/api/tasks/'+chosen+'/knowledge?download=1'};
 button('save-draft').onclick=()=>void saveRunKnowledge();
 button('summarize').onclick=async()=>{if(detail?.task.engine==='deepseek-harness'){notify(harnessKnowledgeHint);return}switchTab('note');button('summarize').disabled=true;try{await api('tasks/'+chosen+'/summarize','POST',{});notify('正在整理，完成后会显示草稿');await poll()}catch(e){notify((e as Error).message)}finally{if(button('summarize'))button('summarize').disabled=detail?.task.engine==='deepseek-harness'}};
 button('adopt-draft').onclick=()=>editKnowledgeFromRun();
 element('knowledge-filter').querySelectorAll<HTMLButtonElement>('button').forEach(b=>b.onclick=()=>{knowledgeFilter=b.dataset.knowledgeFilter!;renderKnowledgeList()});
 element('knowledge-form').onsubmit=saveKnowledge;button('knowledge-cancel').onclick=()=>element<HTMLDialogElement>('knowledge-dialog').close();element('knowledge-dialog').addEventListener('cancel',e=>{e.preventDefault();element<HTMLDialogElement>('knowledge-dialog').close()});
 element('conversation').addEventListener('click',e=>{const b=(e.target as HTMLElement).closest<HTMLElement>('[data-knowledge-run]');if(b)void saveRunKnowledge(b.dataset.knowledgeRun!)});
 input('environment-picker').onchange=()=>{storeEnvironmentEditor();editingID=input('environment-picker').value;loadEnvironmentEditor()};input('environment-type').onchange=showEnvironmentFields;button('add-wsl').onclick=()=>addEnvironment('wsl');button('add-windows').onclick=()=>addEnvironment('windows');button('add-ssh').onclick=()=>addEnvironment('ssh');button('remove-environment').onclick=removeEnvironment;
 element('settings-form').onsubmit=saveSettings;button('check-codex').onclick=async()=>{button('check-codex').disabled=true;try{const r=await api('check','POST',{environment_id:editingID});element('check-result').textContent=(r.ok?'检查通过\n':'检查失败\n')+r.output}catch(e){element('check-result').textContent=(e as Error).message}finally{button('check-codex').disabled=false}};
 button('pair-code').onclick=async()=>{try{const r=await api('feishu/pair','POST',{});element('pair-result').textContent='向机器人发送：\n/配对 '+r.code}catch(e){notify((e as Error).message)}};
 button('data-dir-switch').onclick=()=>void switchDataDirectory();button('workspace-export').onclick=()=>void downloadWorkspaceExport();button('workspace-import-file').onchange=()=>void chooseWorkspaceImport(input('workspace-import-file').files?.[0]||null);button('workspace-import').onclick=()=>void importWorkspaceData();
 button('setup-feishu').onclick=()=>openSetup('');button('bind-setup').onclick=()=>openSetup(chosen);button('setup-start').onclick=startSetup;button('setup-close').onclick=()=>element<HTMLDialogElement>('setup-dialog').close();button('setup-cancel').onclick=cancelSetup;button('bind-open').onclick=openBinding;button('bind').onclick=()=>setBinding(true);button('unbind').onclick=()=>setBinding(false);
}
function mayLeave(){if(createSubmitting){notify('正在创建任务，请等待提交完成。你的要求和附件会保留。');return false}return true}
function setCreatePageVisible(visible:boolean){
 element('create-page')?.classList.toggle('hidden',!visible);
 element('workspace')?.classList.toggle('create-mode',visible);
 if(visible)element('session-banner')?.classList.add('hidden');
 renderCodexApprovals(visible?null:detail);
}
function renderList(){
 if(!element('task-list'))return;
 const query=input('search').value.trim(),archived=tasks.filter(t=>t.archived).length;
 button('tasks-active').textContent='任务 · '+(tasks.length-archived);button('tasks-archived').textContent='已归档 · '+archived;
 for(const view of ['active','archived'])button('tasks-'+view).classList.toggle('selected',taskView===view&&!query);
 element('search-summary').classList.toggle('hidden',!query);button('search-clear').classList.toggle('hidden',!query);
 let html='';
 if(query){
  element('search-summary').textContent=searchError||(searchPending?'正在搜索全部任务…':`共 ${searchHits?.length||0} 条匹配 · 含归档`+(searchLimited?' · 最多 60 条，请缩小关键词':''));
  html=(searchHits||[]).map((h,i)=>`<button class="task search-hit" data-hit="${i}"><small>${({task:'任务',message:'对话',knowledge:'知识',scratch:'待办'} as Record<string,string>)[h.kind]}${h.archived?' · 已归档':''}</small><strong>${escapeHTML(h.title)}</strong><span>${escapeHTML(h.snippet)}</span></button>`).join('')||'<p class="muted">'+(searchPending?'读取中…':'没有匹配的内容。')+'</p>';
 }else html=workspaceTaskList(tasks.filter(t=>t.archived===(taskView==='archived')))||'<p class="muted">'+(taskView==='archived'?'没有已归档的任务。':'还没有任务。')+'</p>';
 if(html===lastList)return;lastList=html;element('task-list').innerHTML=html;
 element('task-list').querySelectorAll<HTMLElement>('[data-task]').forEach(b=>b.onclick=()=>void choose(b.dataset.task!));
 element('task-list').querySelectorAll<HTMLElement>('[data-hit]').forEach(b=>b.onclick=()=>{const hit=searchHits?.[Number(b.dataset.hit)];if(hit)void openSearchHit(hit)});
}
async function choose(id:string,view='chat'){
 if(!mayLeave())return;invalidateModelTest();creatingTask=false;createReturnTask='';setCreatePageVisible(false);chosen=id;const token=++selection;detail=null;dirty=false;sequence=0;resetConversation();resetCodexApprovals();void loadStickyBoard();taskContext=[];void loadTaskContext(id);
 element('conversation').innerHTML='<p id="loading" class="muted">正在读取任务记录…</p>';input('message').value=drafts.get(id)||'';knowledgeItems=[];knowledgeEditing=null;lastKnowledge='';renderKnowledgeList();
 for(const key of ['tabs','task-actions','composer-wrap','conversation-filter'])element(key).classList.remove('hidden');element('sidebar').classList.remove('open');switchTab('chat');renderList();
 history.replaceState(null,'','/?task='+id);
 try{const [d,k]=await Promise.all([api<Detail>('tasks/'+id),api<Knowledge[]>('tasks/'+id+'/knowledge')]);if(token!==selection)return;detail=d;storeKnowledge(k||[]);element('conversation').innerHTML='';appendEvents(d.events);renderTask();switchTab(view);}
 catch(e){if(token===selection)notify((e as Error).message)}
}
function switchTab(tab:string){
 if(creatingTask){invalidateModelTest('create');creatingTask=false;createReturnTask=''}
 setCreatePageVisible(false);
 toolsTab=tab;const open=tab!=='chat';
 if(chosen){const query=new URLSearchParams({task:chosen});if(tab==='note')query.set('view','note');history.replaceState(null,'','/?'+query)}
 element('workspace').classList.toggle('tool-open',open);element('workspace').classList.toggle('tool-full',open&&toolFullscreen);
 element('tool-dock').classList.toggle('hidden',!open);element('conversation').classList.remove('hidden');element('composer-wrap').classList.toggle('hidden',!chosen);
 for(const [key,panel] of [['note','notebook'],['scratch','scratch-panel'],['hardware','hardware-panel'],['files','files-panel'],['terminal','terminal-panel']]){element(panel).classList.toggle('hidden',tab!==key);button(key+'-tab').classList.toggle('selected',tab===key);button(key+'-tab').setAttribute('aria-pressed',String(tab===key))}
 button('chat-tab').classList.toggle('selected',!open);element('tool-title').textContent=({note:'任务知识',scratch:'待办',hardware:'硬件调试',files:'文件与改动',terminal:'终端'} as Record<string,string>)[tab]||'任务工具';
 renderTerminal();renderHardwareState();if(tab!=='hardware')cancelHardwareInput();if(tab==='files')void loadFiles();if(tab==='scratch')void loadScratch();if(tab==='hardware')void loadDevices();
}

let knowledgeFilter='all';
function knowledgeStateLabel(v:string){return v==='verified'?'已验证':v==='stale'?'已过时':'待验证'}
function knowledgeSourceLabel(v:string){return ({run:'来自执行记录',feishu:'来自飞书',organize:'整理生成',migrated:'历史任务知识'} as Record<string,string>)[v]||'手工记录'}
function knowledgeStampOf(list:Knowledge[]){return list.map(k=>k.id+':'+k.revision).join(',')}
function storeKnowledge(list:Knowledge[]){const stamp=knowledgeStampOf(list);if(stamp===lastKnowledge)return;lastKnowledge=stamp;knowledgeItems=list;renderKnowledgeList();applyConversationFilter()}
function knowledgeForRun(runId:string){return knowledgeItems.find(k=>k.run_id===runId)||null}
function latestKnowledgeRun(){return detail?.runs.filter(r=>r.kind==='knowledge'&&r.status==='done'&&r.result).at(-1)}
function renderKnowledgeList(){
 if(!element('knowledge-list'))return;
 element('knowledge-filter').querySelectorAll<HTMLButtonElement>('button').forEach(b=>{const on=b.dataset.knowledgeFilter===knowledgeFilter;b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))});
 const verified=knowledgeItems.filter(k=>k.status==='verified').length;
 element('note-status').textContent=knowledgeItems.length?`共 ${knowledgeItems.length} 条知识 · 已验证 ${verified} 条`:'一份任务，一份可复用的记录。';
 const items=knowledgeFilter==='all'?knowledgeItems:knowledgeItems.filter(k=>k.status===knowledgeFilter);
 element('knowledge-list').innerHTML=items.map(k=>`<article class="knowledge-card" data-knowledge="${escapeHTML(k.id)}" data-state="${escapeHTML(k.status)}"><header><button class="knowledge-title" data-knowledge-edit="${escapeHTML(k.id)}" title="编辑这条知识">${escapeHTML(k.title)}</button><span class="knowledge-state">${knowledgeStateLabel(k.status)}</span></header><div class="knowledge-body">${markdown(k.content)}</div><footer><span>${knowledgeSourceLabel(k.source)} · ${new Date(k.updated).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})}</span><span class="knowledge-actions"><button type="button" data-knowledge-state="${escapeHTML(k.id)}" title="切换验证状态">${k.status==='verified'?'标为待验证':'标为已验证'}</button><button type="button" data-knowledge-use="${escapeHTML(k.id)}" title="把内容和标题带入输入框">↗</button><button type="button" data-knowledge-delete="${escapeHTML(k.id)}" title="删除这条知识">×</button></span></footer></article>`).join('')||'<p class="muted">还没有沉淀知识。跑完一轮后点对话里的“沉淀为知识”，或点“整理任务知识”让 AI 总结这几轮。</p>';
 element('knowledge-list').querySelectorAll<HTMLElement>('[data-knowledge-edit]').forEach(b=>b.onclick=()=>editKnowledge(b.dataset.knowledgeEdit!));
 element('knowledge-list').querySelectorAll<HTMLElement>('[data-knowledge-state]').forEach(b=>b.onclick=()=>void toggleKnowledgeState(knowledgeItems.find(k=>k.id===b.dataset.knowledgeState)));
 element('knowledge-list').querySelectorAll<HTMLElement>('[data-knowledge-use]').forEach(b=>b.onclick=()=>useKnowledge(knowledgeItems.find(k=>k.id===b.dataset.knowledgeUse)));
 element('knowledge-list').querySelectorAll<HTMLElement>('[data-knowledge-delete]').forEach(b=>b.onclick=()=>void deleteKnowledge(knowledgeItems.find(k=>k.id===b.dataset.knowledgeDelete)));
}
function harnessSessionClosed(value:Detail|null=detail){return value?.task.engine==='deepseek-harness'&&value.runtime?.can_continue===false}
// Describe the real native session, not just the persisted conversation log.
function renderSessionBanner(){
 const banner=element('session-banner');
 if(!detail||detail.task.archived||(!detail.task.session&&!detail.runs.length)){banner.classList.add('hidden');banner.innerHTML='';return}
 const started=detail.session_started?new Date(detail.session_started).toLocaleString('zh-CN',{month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit',hour12:false}):'';
 const foreign=taskContext.length?`<span class="session-warning" title="${escapeHTML(taskContext.map(f=>f.label+' · '+f.name).join('\n'))}">工作目录有外部 AI 指令：${escapeHTML(taskContext.map(f=>f.name).join('、'))}</span>`:'';
 const harness=detail.task.engine==='deepseek-harness',closed=harnessSessionClosed(),busy=detail.runs.some(r=>r.status==='running'||r.status==='queued')||detail.runtime?.state==='busy';
 const title=harness?(closed?'运行会话已结束':detail.runtime?.state==='new'?'空白会话已就绪':detail.runtime?.state==='busy'?'Harness 正在执行':'Harness 连续对话'):detail.task.session?`正在续用${started?' '+started+' 开始的':''}历史会话`:'空白会话已就绪';
 const explanation=harness?(detail.runtime?.reason||harnessSessionHint):'聊天记录保留在当前任务中。新建空白会话后，AI 不会自动记得之前的对话。';
 banner.dataset.state=closed?'closed':busy?'busy':'live';
 const html=`<span class="session-text"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(explanation)}</span></span>${foreign}${detail.task.session?`<button type="button" id="session-reset" class="subtle"${busy||sessionResetTask===detail.task.id?' disabled':''}>${sessionResetTask===detail.task.id?'正在新建…':'新建空白会话'}</button>`:''}`;
 if(banner.innerHTML!==html)banner.innerHTML=html;
 banner.classList.remove('hidden');
 if(button('session-reset'))button('session-reset').onclick=()=>void resetSession();
}
async function resetSession(){
 if(!detail||!detail.task.session||sessionResetTask)return;
 if(detail.runs.some(r=>r.status==='running'||r.status==='queued')){notify('请先停止执行并取消排队，再新建空白会话。');return}
 if(!confirm('新建空白会话？任务记录和任务知识都会保留，未发送的草稿也保留。下一轮 AI 不会自动记得之前的对话，这不是恢复旧会话。'))return;
 const id=detail.task.id,token=++selection;sessionResetTask=id;renderTask();
 try{
  const task=await api<Task>('tasks/'+id+'/session/reset','POST',{});
  if(chosen!==id||selection!==token||!detail)return;
  detail.task=task;detail.session_started=0;
  if(task.engine==='deepseek-harness')detail.runtime={state:'new',can_continue:true,reason:'下一条要求将开启新的原生会话；旧记录不自动带入 AI 上下文。'};
  notify('空白会话已就绪；记录、知识和未发送的草稿均保留。');
 }catch(e){if(chosen===id)notify((e as Error).message)}finally{sessionResetTask='';if(chosen===id&&selection===token)renderTask()}
}
async function loadTaskContext(id:string){
 const token=selection;taskContext=[];
 try{const r=await api<{files:ContextFile[]}>('tasks/'+id+'/context');if(token!==selection)return;taskContext=r.files||[]}catch{}
 if(token===selection&&detail)renderSessionBanner();
}
function renderTask(){
 if(!detail)return;renderSessionBanner();const t=detail.task;element('task-title').textContent=t.title;element('task-workspace').textContent=(t.environment?.name?t.environment.name+' · ':'')+t.workspace;element('task-workspace').title=element('task-workspace').textContent;const modelLabel=[taskEngineName(t.engine),t.model||'默认模型',effortLabels[t.reasoning_effort]||''].filter(Boolean).join(' · ');element('task-model-label').textContent=modelLabel;element('task-model-button').title=modelLabel;
 const active=detail.runs.some(r=>['running','queued'].includes(r.status));const queued=detail.runs.filter(r=>r.status==='queued').length;const latest=detail.runs.at(-1);
 element('run-status').textContent=detail.approvals?.length?'等待你处理 '+detail.approvals.length+' 个 Codex 请求':active?'正在执行'+(queued?' · '+queued+' 条要求排队中':''):latest?.error||names[t.status]||t.status;
 element('run-status').classList.toggle('error',!active&&!!latest?.error);button('stop').classList.toggle('hidden',!active);button('send').disabled=sending;input('message').placeholder=active?'追加要求将排队，也可以停止当前执行…':'下一步，要做什么？';
 const knowledge=latestKnowledgeRun(),saved=knowledge?knowledgeForRun(knowledge.id):null,pending=!!knowledge&&(!saved||saved.content!==knowledge.result);element('draft-banner').classList.toggle('hidden',!pending);element('draft-preview').classList.toggle('hidden',!pending);button('note-tab').textContent='任务知识'+(knowledgeItems.length?' · '+knowledgeItems.length:'');button('summarize').disabled=active;
 if(knowledge&&pending){const preview=element('draft-content');if(preview.dataset.run!==knowledge.id){preview.innerHTML=markdown(knowledge.result);preview.dataset.run=knowledge.id}element('draft-label').textContent=saved?'这轮执行的总结已保存，草稿可编辑合并':'执行总结 · 未保存';button('save-draft').disabled=!!saved}
 input('message').disabled=t.archived;button('send').disabled=sending||t.archived;button('summarize').disabled=active||t.archived;if(t.archived)element('run-status').textContent='任务已归档，记录保留；恢复后可以继续执行。';
 if(harnessSessionClosed()){element('run-status').textContent='当前运行会话不可继续，请先新建空白会话。已输入的要求会保留。';element('run-status').classList.add('error');input('message').placeholder='可先写下要求，新建空白会话后再发送…'}
 const harness=t.engine==='deepseek-harness';button('summarize').disabled=active||t.archived||harness;button('summarize').title=harness?harnessKnowledgeHint:'根据任务记录生成知识草稿';
 let knowledgeHint=element('harness-knowledge-hint');if(!knowledgeHint){knowledgeHint=document.createElement('p');knowledgeHint.id='harness-knowledge-hint';knowledgeHint.className='muted';element('notebook').querySelector('.note-head')!.after(knowledgeHint)}knowledgeHint.textContent=harnessKnowledgeHint;knowledgeHint.classList.toggle('hidden',!harness);
 renderWorkflow();renderCodexApprovals(detail);renderTerminal();const i=tasks.findIndex(x=>x.id===t.id);if(i>=0)tasks[i]=t;renderList();
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
 if(!authenticated||polling||document.hidden||sessionResetTask===chosen&&!!chosen)return;polling=true;const epoch=shellEpoch,signal=shellController.signal,id=chosen,token=selection,approvalRevision=codexApprovalRevision;
 try{if(Date.now()-refreshList>4000){const loaded=await api<Task[]>('tasks','GET',undefined,signal);if(!shellCurrent(epoch))return;tasks=loaded;refreshList=Date.now();renderList()}
  if(id){const [d,k]=await Promise.all([api<Detail>('tasks/'+id+'?after='+sequence,'GET',undefined,signal),api<Knowledge[]>('tasks/'+id+'/knowledge','GET',undefined,signal)]);if(!shellCurrent(epoch)||token!==selection||approvalRevision!==codexApprovalRevision)return;detail=d;if(k)storeKnowledge(k);appendEvents(d.events);renderTask()}
  if(element('connection'))element('connection').textContent='本机服务已连接';
 }catch(e){if(shellCurrent(epoch)&&element('connection'))element('connection').textContent='连接中断，正在重试'}finally{if(shellCurrent(epoch))polling=false}
}
async function showCreate(){
 if(!mayLeave())return;
 invalidateModelTest();
 const epoch=shellEpoch;
 try{
  const loaded=await api<Settings>('settings','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;settings=loaded;
  if(!creatingTask)createReturnTask=chosen;
  creatingTask=true;chosen='';detail=null;selection++;createFiles=[];
  input('create-input').value='';input('create-files').value='';input('create-error').textContent='';
  element<HTMLSelectElement>('create-environment').innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(environmentOptionLabel(e))}</option>`).join('');
  input('create-environment').value=settings.config.default_environment;
  setCreatePermission('auto');renderCreateFiles();setCreateSubmitState('idle');
  setCreatePageVisible(true);element('sidebar').classList.remove('open');
  for(const id of ['tabs','task-actions','conversation','composer-wrap'])element(id).classList.add('hidden');
  element('workspace').classList.remove('tool-open','tool-full');element('tool-dock').classList.add('hidden');
  element('task-title').textContent='新建任务';element('task-workspace').textContent='选择环境和工作目录';element('task-workspace').title='';
  history.replaceState(null,'','/');renderList();input('create-input').focus();await loadCreateEnvironment();
  }catch(e){if(shellCurrent(epoch))notify((e as Error).message)}
}
function createTaskTitle(text:string){
 const line=text.split(/\r?\n/).map(v=>v.trim()).find(Boolean)||'新的任务';
 return line.length>180?line.slice(0,180):line;
}
function cancelCreate(){
 if(!mayLeave())return;
 invalidateModelTest('create');
 const id=createReturnTask;creatingTask=false;createReturnTask='';createFiles=[];input('create-files').value='';renderCreateFiles();setCreatePageVisible(false);
 if(id&&tasks.some(t=>t.id===id)){void choose(id);return}
 chosen='';detail=null;selection++;resetConversation();for(const name of ['tabs','task-actions','composer-wrap'])element(name).classList.add('hidden');element('conversation').classList.remove('hidden');element('task-title').textContent='今天，从哪件事开始？';element('task-workspace').textContent='Windows · WSL · SSH';history.replaceState(null,'','/');switchTab('chat');renderList();
}
async function loadCreateEnvironment(keepWorkspace=false){
 if(createSubmitting)return;
 const env=settings.config.environments.find(e=>e.id===input('create-environment').value);if(!env)return;
 if(!keepWorkspace)input('create-engine').value=env.default_engine||'codex';
 setCreatePermission(createPermission);
 input('create-effort').value='';
 const directories=[...new Set([...env.workspaces,...tasks.filter(t=>t.environment?.id===env.id).map(t=>t.workspace)])];const workspaceOptions=element<HTMLDataListElement>('workspace-options');if(workspaceOptions)workspaceOptions.innerHTML=directories.map(p=>`<option value="${escapeHTML(p)}"></option>`).join('');if(!keepWorkspace)input('create-workspace').value=env.workspaces[0]||'';
 input('create-workspace').placeholder=environmentWorkspaceHint(env);
 setCreateSubmitState('idle');await loadCreateModels(true);
}
async function createTask(e:Event){
 e.preventDefault();if(createSubmitting||button('create-submit').disabled||!creatingTask)return;
 const text=input('create-input').value,files=[...createFiles];if(!text.trim()&&!files.length){element('create-error').textContent='请先写下任务要求，或添加附件。';input('create-input').focus();return}
 const epoch=shellEpoch;invalidateModelTest('create');createSubmitting=true;modelRequest++;setCreateSubmitState('starting');let created='',uploadedCount=0;
 try{validateEngineAttachments(input('create-engine').value,files.length);validateAttachmentFiles(files);const mode=modeForPermission(createPermission,input('create-engine').value);if(!mode||!modeSupportsEngine(mode,input('create-engine').value))throw new Error('所选审批模式暂不可用，请刷新工作台或重新选择。');const r=await api('tasks','POST',{engine:input('create-engine').value,reasoning_effort:input('create-effort').value,environment_id:input('create-environment').value,title:createTaskTitle(text),workspace:input('create-workspace').value,model:input('create-model').value==='__custom__'?input('custom-model').value.trim():input('create-model').value,mode_id:mode.id});created=r.task.id;drafts.set(created,text);attachmentDrafts.set(created,[]);
  if(!shellCurrent(epoch)){if(files.length)pendingUploadFiles.set(created,files);return}tasks.unshift(r.task);
  for(const file of files){const attachment=await uploadTaskFile(created,file);attachmentDrafts.get(created)!.push(attachment);uploadedCount++;if(!shellCurrent(epoch)){if(uploadedCount<files.length)pendingUploadFiles.set(created,files.slice(uploadedCount));return}}
  if(text.trim()||files.length){await api('tasks/'+created+'/messages','POST',{content:text.trim()||'请查看这些附件。',mode_id:mode.id,attachment_ids:attachmentDrafts.get(created)!.map(f=>f.id)});drafts.delete(created);attachmentDrafts.delete(created)}
  if(!shellCurrent(epoch))return;dirty=false;creatingTask=false;createReturnTask='';createFiles=[];input('create-files').value='';renderCreateFiles();setCreatePageVisible(false);createSubmitting=false;setCreateSubmitState('idle');await choose(created);
 }catch(error){if(created&&uploadedCount<files.length)pendingUploadFiles.set(created,files.slice(uploadedCount));if(!shellCurrent(epoch))return;if(created){dirty=false;creatingTask=false;createReturnTask='';createFiles=[];input('create-files').value='';renderCreateFiles();setCreatePageVisible(false);createSubmitting=false;setCreateSubmitState('idle');await choose(created);notify('任务已创建，提交未完成；要求和未上传附件已保留，请检查记录后再发送：'+(error as Error).message)}else{element('create-error').textContent=(error as Error).message}}finally{if(shellCurrent(epoch)){createSubmitting=false;setCreateSubmitState('idle')}}
}
async function send(text:string,clear:boolean){
 if(harnessSessionClosed()||sessionResetTask===chosen&&!!chosen){notify('请先新建空白会话，再发送要求；输入内容会保留。');return}
 if(pendingUploadFiles.get(chosen)?.length){notify('请先重试上传或移除待上传附件，避免遗漏文件。');return}
 const files=[...(attachmentDrafts.get(chosen)||[])];if(!chosen||!detail||sending||uploadingTasks.has(chosen)||(!text.trim()&&!files.length))return;try{validateEngineAttachments(detail.task.engine,files.length)}catch(e){notify((e as Error).message);return}const epoch=shellEpoch,id=chosen,original=input('message').value,mode=selectedMessageMode();sending=true;renderTask();
 try{await api('tasks/'+id+'/messages','POST',{content:text.trim()||'请查看这些附件。',mode_id:mode,attachment_ids:files.map(f=>f.id)});attachmentDrafts.set(id,(attachmentDrafts.get(id)||[]).filter(f=>!files.some(sent=>sent.id===f.id)));if(clear){if(drafts.get(id)===original)drafts.delete(id);if(shellCurrent(epoch)&&chosen===id&&input('message').value===original)input('message').value=''}if(shellCurrent(epoch))await poll()}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}finally{if(shellCurrent(epoch)){sending=false;if(chosen===id)renderTask()}}
}
async function loadKnowledge(){const id=chosen;try{const items=await api<Knowledge[]>('tasks/'+id+'/knowledge');if(id!==chosen)return;storeKnowledge(items||[])}catch(e){notify((e as Error).message)}}
function editKnowledge(id:string|null){
 const item=id?knowledgeItems.find(k=>k.id===id)||null:null;knowledgeEditing=item?.id||null;
 input('knowledge-title').value=item?.title||'';input('knowledge-body').value=item?.content||'';input('knowledge-state').value=item?.status||'observed';
 element('knowledge-error').textContent='';element<HTMLDialogElement>('knowledge-dialog').showModal();input('knowledge-title').focus();
}
function editKnowledgeFromRun(){
 const r=latestKnowledgeRun();if(!r)return;const saved=knowledgeForRun(r.id),stamp=new Date(r.created).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false});
 knowledgeEditing=saved?.id||null;
 input('knowledge-title').value=saved?.title||('执行总结 · '+stamp);input('knowledge-body').value=r.result;input('knowledge-state').value=saved?.status||'observed';
 element('knowledge-error').textContent='';element<HTMLDialogElement>('knowledge-dialog').showModal();input('knowledge-title').focus();
}
async function saveKnowledge(e:Event){
 e.preventDefault();const item=knowledgeEditing?knowledgeItems.find(k=>k.id===knowledgeEditing)||null:null,id=chosen;button('knowledge-save').disabled=true;
 try{
  const payload={title:input('knowledge-title').value.trim(),content:input('knowledge-body').value,status:input('knowledge-state').value,source:item?.source||'manual',revision:item?.revision||0};
  if(item)await api('tasks/'+id+'/knowledge/'+item.id,'PUT',payload);else await api<Knowledge>('tasks/'+id+'/knowledge','POST',payload);
  if(id!==chosen)return;element<HTMLDialogElement>('knowledge-dialog').close();knowledgeEditing=null;await loadKnowledge();notify(item?'知识已更新':'知识已保存');
 }catch(error){element('knowledge-error').textContent=(error as Error).message}finally{if(element('knowledge-save'))button('knowledge-save').disabled=false}
}
async function saveRunKnowledge(runId?:string){
 const r=runId?detail?.runs.find(x=>x.id===runId):latestKnowledgeRun();if(!r)return;
 try{const k=await api<Knowledge>('tasks/'+chosen+'/knowledge/from-run','POST',{run_id:r.id});await loadKnowledge();notify('已沉淀到任务知识：'+k.title)}
 catch(e){notify((e as Error).message)}
}
async function toggleKnowledgeState(k:Knowledge|undefined){
 if(!k)return;const next=k.status==='verified'?'observed':'verified';
 try{await api('tasks/'+k.task_id+'/knowledge/'+k.id,'PUT',{title:k.title,content:k.content,status:next,source:k.source,revision:k.revision});await loadKnowledge()}catch(e){notify((e as Error).message)}
}
async function deleteKnowledge(k:Knowledge|undefined){
 if(!k||!confirm('删除这条知识？'))return;
 try{await api('tasks/'+k.task_id+'/knowledge/'+k.id,'DELETE',{revision:k.revision});await loadKnowledge();notify('知识已删除')}catch(e){notify((e as Error).message)}
}
function useKnowledge(k:Knowledge|undefined){if(!k)return;bringToChat(k.title+'\n\n'+k.content);renderWorkflow();element('sidebar').classList.remove('open')}
async function openSettings(){
 const epoch=shellEpoch;
 element('settings-saved').textContent='';
 try{const loaded=await api<Settings>('settings','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;settings=loaded;const c=settings.config;loadAccessSettings();loadDataSettings();editingEnvironments=JSON.parse(JSON.stringify(c.environments));editingID=c.default_environment;environmentPickers(c.default_environment);loadEnvironmentEditor();input('feishu-id').value=c.feishu.app_id;input('feishu-secret').value='';input('feishu-secret').placeholder=settings.secret_configured?'已保存，留空保持不变':'填写自建应用 App Secret';input('feishu-enabled').checked=c.feishu.enabled;updateFeishuStatus();element('check-result').textContent='';element('settings-error').textContent='';element<HTMLDialogElement>('settings-dialog').showModal()}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}
}
function loadDataSettings(){
 const current=element('data-dir-current');if(!current)return;
 current.textContent=settings.data_dir||'未能读取当前数据目录';input('data-dir-target').value='';
 const supported=settings.data_switch_supported!==false;button('data-dir-switch').disabled=!supported;
 element('data-dir-supported').textContent=supported?'当前通过 Duo 启动器运行，可自动安全重启。':'当前是直接运行服务，需通过 Duo.exe 启动后才能切换。';
 resetWorkspaceImport();
}
let workspaceImportFile:File|null=null,workspaceImportBytes:ArrayBuffer|null=null,workspaceTransferBusy=false;
function resetWorkspaceImport(){
 workspaceImportFile=null;workspaceImportBytes=null;workspaceTransferBusy=false;
 const file=input('workspace-import-file');if(file)file.value='';
 const importButton=button('workspace-import');if(importButton)importButton.disabled=true;
 const name=element('workspace-import-file-name');if(name)name.textContent='尚未选择文件';
 const preview=element('workspace-import-preview');if(preview)preview.textContent='';
 const result=element('workspace-transfer-result');if(result)result.textContent='';
}
async function downloadWorkspaceExport(){
 if(workspaceTransferBusy)return;const epoch=shellEpoch;workspaceTransferBusy=true;button('workspace-export').disabled=true;element('workspace-transfer-result').textContent='正在生成脱敏数据包…';
 try{
  const response=await fetch('/api/workspace/export',{credentials:'same-origin',cache:'no-store',signal:shellController.signal});
  if(!response.ok){if(response.status===401&&shellCurrent(epoch)){showLogin();return}const body=await response.json().catch(()=>({error:'导出失败'}));throw Object.assign(new Error(body.error||'导出失败'),{status:response.status})}
  const blob=await response.blob();if(!shellCurrent(epoch))return;
  const link=document.createElement('a'),url=URL.createObjectURL(blob),stamp=new Date().toISOString().replace(/[^0-9]/g,'').slice(0,14);link.href=url;link.download='duo-workspace-'+stamp+'.zip';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);
  element('workspace-transfer-result').textContent='已下载脱敏数据包。它不含密码、凭据或执行环境配置，请妥善保管。';
 }catch(error){if(shellCurrent(epoch))element('workspace-transfer-result').textContent=(error as Error).message||'导出失败'}finally{if(shellCurrent(epoch)){workspaceTransferBusy=false;button('workspace-export').disabled=false}}
}
async function chooseWorkspaceImport(file:File|null){
 workspaceImportFile=file;workspaceImportBytes=null;const importButton=button('workspace-import');importButton.disabled=true;
 const name=element('workspace-import-file-name'),preview=element('workspace-import-preview');if(!file){name.textContent='尚未选择文件';preview.textContent='';return}
 name.textContent=file.name+' · '+Math.ceil(file.size/1024)+' KB';
 try{
  if(file.size>64*1024*1024)throw new Error('导入 ZIP 超过 64 MiB，请先拆分附件或使用完整数据目录切换。');
  const header=new Uint8Array(await file.slice(0,4).arrayBuffer());if(header.length<4||header[0]!==0x50||header[1]!==0x4b)throw new Error('请选择 Duo 导出的 ZIP 文件。');
  const bytes=await file.arrayBuffer();if(workspaceImportFile!==file)return;workspaceImportBytes=bytes;preview.textContent='已读取脱敏 ZIP（'+Math.ceil(file.size/1024)+' KB），将由服务校验内容。未执行导入。';importButton.disabled=false;
 }catch(error){preview.textContent=(error as Error).message||'无法读取导入包';workspaceImportFile=null;workspaceImportBytes=null}
}
async function importWorkspaceData(){
 if(workspaceTransferBusy||!workspaceImportFile||!workspaceImportBytes)return;
 const strategy:string=input('workspace-import-strategy').value;
 if(strategy==='replace'){element('workspace-transfer-result').textContent='当前版本暂不支持覆盖导入，请选择“追加”。当前数据不会改变。';return}
 const title=strategy==='replace'?'覆盖导入':'追加导入';
 const warning=strategy==='replace'?'会以导入包中的同 ID 记录覆盖当前对应记录，未包含的当前记录保留。':'只新增导入包中的记录，当前任务、对话和知识不会被删除。';
 if(!confirm(title+'将写入当前工作区数据。\n'+warning+'\n密码、凭据、执行环境配置和当前数据目录不会被导入。建议先下载备份。继续吗？'))return;
 const epoch=shellEpoch;workspaceTransferBusy=true;button('workspace-import').disabled=true;button('workspace-export').disabled=true;element('workspace-transfer-result').textContent='正在校验并导入…请不要关闭窗口。';
 try{
  const response=await fetch('/api/workspace/import',{method:'POST',credentials:'same-origin',cache:'no-store',signal:shellController.signal,headers:{'Content-Type':'application/zip','X-CSRF-Token':csrf},body:workspaceImportBytes});
  const result=await response.json().catch(()=>({error:'服务返回内容异常'}));if(!response.ok){if(response.status===401&&shellCurrent(epoch)){showLogin();return}throw Object.assign(new Error(result.error||'导入失败'),{status:response.status});}
  if(!shellCurrent(epoch))return;element('workspace-transfer-result').textContent='导入完成，正在刷新任务列表…';setTimeout(()=>{if(shellCurrent(epoch))location.reload()},250);
 }catch(error){if(shellCurrent(epoch)){element('workspace-transfer-result').textContent=(error as Error).message||'导入失败';workspaceTransferBusy=false;button('workspace-export').disabled=false;button('workspace-import').disabled=!workspaceImportBytes}}
}
async function switchDataDirectory(){
 const target=input('data-dir-target').value.trim();if(!target){element('data-dir-result').textContent='请填写已有数据目录的绝对路径。';return}
 if(!settings.data_switch_supported){element('data-dir-result').textContent='当前启动方式不支持自动切换，请先通过 Duo.exe 启动。';return}
 if(!confirm('将停止当前空闲服务并载入此已有数据目录。不会复制、合并或删除任何数据；原目录会保留。目标目录必须已完整复制并正常退出 Duo。继续吗？'))return;
 const expected=settings.data_dir;button('data-dir-switch').disabled=true;element('data-dir-result').textContent='正在校验并安全重启…请不要关闭窗口。';
 try{await api('data/switch','POST',{data_dir:target,expected_current:expected,confirm:true});element('data-dir-result').textContent='已接受切换请求，正在载入新目录…';
  const started=Date.now();let sawDown=false;while(Date.now()-started<60000){await new Promise(resolve=>setTimeout(resolve,500));try{const response=await fetch('/healthz',{cache:'no-store'});if(response.ok&&sawDown){location.reload();return}}catch{sawDown=true}}
  throw new Error('等待新数据目录启动超时；原目录应仍保留，请查看托盘状态后重试。');
 }catch(e){element('data-dir-result').textContent=(e as Error).message;button('data-dir-switch').disabled=false}
}
function environmentPickers(defaultID?:string){
 const preferred=defaultID||input('default-environment').value||settings.config.default_environment;
 const options=editingEnvironments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${e.type.toUpperCase()}</option>`).join('');input('environment-picker').innerHTML=options;input('environment-picker').value=editingID;input('default-environment').innerHTML=options;input('default-environment').value=editingEnvironments.some(e=>e.id===preferred)?preferred:editingEnvironments[0].id;button('remove-environment').disabled=editingEnvironments.length<2;
}
function loadEnvironmentEditor(){
 const e=editingEnvironments.find(e=>e.id===editingID);if(!e)return;
 input('environment-name').value=e.name;input('environment-type').value=e.type;input('setting-distro').value=e.distro||'';input('setting-user').value=e.user||'';input('setting-host').value=e.host||'';input('setting-port').value=String(e.port||22);input('setting-identity').value=e.identity||'';input('setting-claude').value=e.claude||'';input('setting-claude-model').value=e.claude_model||'';input('setting-harness').value=e.harness||(e.type==='windows'?'dsh.cmd':'dsh');input('setting-harness-model').value=e.harness_model||'deepseek-flash';input('setting-harness-provider').value=e.harness_provider||'deepseek-official';input('setting-engine').value=e.default_engine||'codex';input('setting-codex').value=e.codex;input('setting-model').value=e.model;input('setting-workspaces').value=e.workspaces.join('\n');element('check-result').textContent='';showEnvironmentFields();renderConfiguredModels?.();
}
function showEnvironmentFields(){
 const type=input('environment-type').value;element('wsl-fields').classList.toggle('hidden',type!=='wsl');element('ssh-fields').classList.toggle('hidden',type!=='ssh');element('linux-user').classList.toggle('hidden',type==='windows');input('setting-port').disabled=type!=='ssh';
}
function storeEnvironmentEditor(){
 const e=editingEnvironments.find(e=>e.id===editingID);if(!e)return;
 Object.assign(e,{name:input('environment-name').value.trim(),type:input('environment-type').value,distro:input('setting-distro').value.trim(),user:input('setting-user').value.trim(),host:input('setting-host').value.trim(),port:Number(input('setting-port').value)||22,identity:input('setting-identity').value.trim(),claude:input('setting-claude').value.trim(),claude_model:input('setting-claude-model').value.trim(),harness:input('setting-harness').value.trim(),harness_model:input('setting-harness-model').value.trim(),harness_provider:input('setting-harness-provider').value.trim(),default_engine:input('setting-engine').value,codex:input('setting-codex').value.trim(),model:input('setting-model').value.trim(),workspaces:input('setting-workspaces').value.split('\n').map(s=>s.trim()).filter(Boolean)});
}
function addEnvironment(type:Environment['type']){
 storeEnvironmentEditor();const id='env_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,6);const e:Environment={id,name:type==='ssh'?'新 SSH 环境':type==='wsl'?'新 WSL 环境':'新 Windows 环境',type,distro:'',user:'',host:'',port:22,identity:'',codex:type==='windows'?'codex.exe':'codex',harness:type==='windows'?'dsh.cmd':'dsh',harness_model:'deepseek-flash',harness_provider:'deepseek-official',model:'',model_cache:'',models:[],workspaces:[]};editingEnvironments.push(e);editingID=id;environmentPickers();loadEnvironmentEditor();input('environment-name').focus();
}
function removeEnvironment(){
 if(editingEnvironments.length<2)return;if(!confirm('删除这个环境配置？已有任务仍使用各自保存的环境。'))return;editingEnvironments=editingEnvironments.filter(e=>e.id!==editingID);editingID=editingEnvironments[0].id;environmentPickers();loadEnvironmentEditor();
}
function updateFeishuStatus(){element('feishu-state').textContent='状态：'+settings.feishu_status;element('feishu-owner').textContent=settings.config.feishu.owner?'已配对你的飞书账号':'尚未配对';button('pair-code').disabled=!!settings.config.feishu.owner;button('setup-feishu').disabled=!!settings.config.feishu.app_id||settings.secret_configured}
async function saveSettings(e:Event){
 e.preventDefault();const epoch=shellEpoch;button('settings-save').disabled=true;element('settings-saved').textContent='';
 storeEnvironmentEditor();
 const c:Configuration={...settings.config,access:{lan:input("access-lan").value.trim(),tailscale:input("access-tailscale").value.trim()},environments:editingEnvironments,default_environment:input('default-environment').value,feishu:{enabled:input('feishu-enabled').checked,app_id:input('feishu-id').value.trim(),secret:input('feishu-secret').value.trim()}};
 try{const saved=await api<Settings>('settings','PUT',c);if(!shellCurrent(epoch))return;settings=saved;invalidateModelCatalogs();input('feishu-secret').value='';updateFeishuStatus();element('settings-error').textContent='';element('settings-saved').textContent='设置已保存';notify('设置已保存')}catch(e){if(shellCurrent(epoch))element('settings-error').textContent=(e as Error).message}finally{if(shellCurrent(epoch))button('settings-save').disabled=false}
}
async function pollSettingsStatus(){
 if(!authenticated||settingsPolling||!element<HTMLDialogElement>('settings-dialog')?.open)return;
 const epoch=shellEpoch;settingsPolling=true;
 try{const latest=await api<Settings>('settings','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||!element<HTMLDialogElement>('settings-dialog')?.open)return;
  // Status refresh must not replace the editable settings snapshot with an older GET.
  settings={...settings,feishu_status:latest.feishu_status,secret_configured:latest.secret_configured,chat:latest.chat,config:{...settings.config,feishu:{...settings.config.feishu,owner:latest.config.feishu.owner}}};updateFeishuStatus();
 }catch{/* A later status refresh can retry without interrupting the editor. */}finally{if(shellCurrent(epoch))settingsPolling=false}
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
setInterval(()=>void pollSettingsStatus(),5000);
const initialShellEpoch=shellEpoch;
void boot().catch(e=>{if(!shellCurrent(initialShellEpoch))return;element('root').innerHTML='<div class="empty"><h2>暂时无法连接工作台</h2><p>'+escapeHTML(e.message)+'</p><button id="retry">重新连接</button></div>';button('retry').onclick=()=>location.reload()});

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
