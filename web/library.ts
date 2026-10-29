type LibraryDocument={scope_key?:string;source_id?:string;source_hash?:string;source_task_id?:string;applicability?:string;evidence?:string;credential_ref?:string;tags?:string[];layer?:string;generated?:boolean;content?:string;id:string;kind:string;source?:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type LibraryReference={document?:LibraryDocument;reference:string;preview?:string;truncated:boolean};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean;include_automatic:boolean;task_folders?:boolean;consensus_directory?:string;system_directory?:string;project_directory?:string};
type KnowledgeExtractionConfig={enabled:boolean;needs_directory?:boolean};
type KnowledgeExtractionReport={created:number;duplicates:number;skipped:number;items?:CuratedKnowledge[];warnings?:string[]};
type AutomaticKnowledgeConfig={capture:boolean;recall:boolean;organize?:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
type VaultSettingsResponse={config:VaultConfig;report:VaultReport;document_directory:string};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
let libraryLoading=false,libraryBrowseStarted=false,libraryNextOffset=0,libraryHasMore=false,libraryPreviewRequest=0,libraryOverviewRequest=0,libraryCitationRequest=0,libraryCitationBusy=false;
let libraryPreviewID='',libraryPreviewText='',libraryPreviewLimit=20000;
let automaticSettingsRequest=0,automaticSettingsReady=false,automaticSettingsSaving=false;
let vaultSettingsRequest=0,vaultSettingsReady=false,vaultSettingsSaving=false,vaultRefreshing=false;
let automaticSettingsDirty=false,vaultSettingsDirty=false,knowledgeSettingsRequest=0;
let knowledgeSettingsLoading:Promise<void>|null=null;
let knowledgeExtractionRequest=0,knowledgeExtractionBusy=false,knowledgeExtractionSettingsReady=false,knowledgeExtractionSettingsDirty=false,knowledgeExtractionSettingsBusy=false;
let workspaceDocumentRequest=0,workspaceDocumentTask='',workspaceDocumentPath='';
function libraryTargetCurrent(){return !!libraryTarget&&shellCurrent(libraryTarget.epoch)&&selection===libraryTarget.selection&&creatingTask===libraryTarget.create&&(creatingTask||chosen===libraryTarget.task)}
function installLibrary(){
 element('theme-toggle').insertAdjacentHTML('afterend','<button type="button" id="library-open" title="全局知识库 · 历史与结论" aria-label="全局知识库">知识库</button>');
 element('command-open').insertAdjacentHTML('afterend','<button type="button" class="library-compose-button" id="library-task" title="引用历史 / 知识" aria-label="引用历史或知识">引用</button>');
 element('create-status').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-create" title="引用历史 / 知识">引用历史 / 知识</button>');
 element('root').insertAdjacentHTML('beforeend',`<dialog id="library-dialog" class="library-dialog"><div class="library-header"><div><h2>全局知识库</h2><p>打开文档即可阅读；任务资料、通用知识与主题索引放在同一个目录。</p></div><button type="button" id="library-close" aria-label="关闭知识库">关闭</button></div><form id="library-search-form" class="library-search"><input id="library-query" aria-label="搜索知识和历史" placeholder="关键词、报错码；多个词用空格分隔" maxlength="160"><select id="library-kind" aria-label="资料类型"><option value="">全部资料</option><option value="knowledge">结论与经验</option><option value="run">任务记录</option><option value="note">Obsidian 笔记</option></select><select id="library-scope" aria-label="任务范围"><option value="">所有任务与 Vault</option></select><label><input type="checkbox" id="library-stale">包含过时结论</label><button type="submit">查询</button></form><p id="library-state" role="status"></p><div id="library-results" class="library-results"></div><details id="vault-settings"><summary>Obsidian Vault 与 Git 同步</summary><p>选择这台 Duo 服务所在电脑的知识文件根目录。Duo 只读写其中的 <code>Duo/</code> 文件夹；其中的 Markdown 可用编辑器或 Obsidian 查看和编辑。启用后每分钟同步一次，也可手动刷新。</p><label>Vault 绝对路径<input id="vault-directory" placeholder="例如 E:\\Notes\\MyVault"></label><label><input type="checkbox" id="vault-enabled">启用 Markdown 同步与索引</label><label><input type="checkbox" id="vault-runs">同时保存全部任务的已结束对话记录（可能包含私人内容）</label><p>同步文件包含问题、回复和结论，不包含数据库、登录凭据、工具日志或附件。常见密钥会脱敏，提交 Git 前仍请检查文件。建议使用自己的私有仓库。</p><div class="actions"><button type="button" id="vault-save">保存设置</button><button type="button" id="vault-refresh">立即同步 / 重建索引</button></div><p id="vault-status" role="status"></p><pre id="vault-problems" class="hidden"></pre><p>跨电脑：用 Obsidian Git 或 Git 客户端提交并推送 Vault，在另一台电脑克隆 / 拉取，再选择该电脑上的 Vault 路径。Duo 不自动执行 Git 推送；发现冲突会保留两端内容。删除本地任务不会删除已导出的 Markdown 档案。</p></details></dialog>`);
 element('vault-settings').insertAdjacentHTML('beforebegin',`<details id="automatic-knowledge-settings"><summary>对话自动积累</summary><p>对话自动保存到任务资料夹。自动整理在同一轮 AI 回复中提取摘要、前提、共识、疑问和有依据的通用知识，不另外启动模型。模型未提供整理结果时，保留原对话。</p><label><input type="checkbox" id="automatic-capture" disabled>自动记录后续对话的问题与最终回复</label><label><input type="checkbox" id="automatic-organize" disabled>随正常对话自动整理摘要、共识与通用知识（增加少量本轮输出）</label><label><input type="checkbox" id="automatic-recall" disabled>新建会话时自动补回当前任务知识</label><p>新会话优先补回本任务的前提、共识与未决问题；没有摘要时使用最近记录。只补回有限片段，其他任务需选择引用；引用文字计入模型输入。</p><button type="button" id="automatic-save" disabled>保存设置</button><p id="automatic-status" role="status"></p></details>`);
 input('vault-runs').closest('label')!.insertAdjacentHTML('afterend','<label><input type="checkbox" id="vault-automatic">同步自动整理的任务资料、通用知识与主题索引</label>');
 installLibrarySettings();
 const headerActions=document.createElement('div');headerActions.className='library-header-actions';headerActions.innerHTML='<button type="button" id="library-manage-task">管理本任务知识</button>';
 button('library-close').before(headerActions);headerActions.append(button('library-close'));
 headerActions.insertAdjacentHTML('afterbegin','<button type="button" id="library-refresh-files">刷新文件</button>');
 button('library-refresh-files').onclick=async()=>{const epoch=shellEpoch,b=button('library-refresh-files');if(b.disabled)return;b.disabled=true;try{const report=await api<VaultReport>('library/vault/refresh','POST',{});if(!shellCurrent(epoch))return;if(report.error)throw new Error(report.error);if(element<HTMLDialogElement>('library-dialog').open){await searchLibrary();void loadLibraryOverview();if(report.conflicts?.length)notify('文件同步存在冲突，已保留两端内容；请在知识库设置查看详情。')}}catch(e){if(shellCurrent(epoch))notify((e as Error).message)}finally{if(shellCurrent(epoch))b.disabled=false}};
 button('library-manage-task').onclick=()=>{if(!libraryTargetCurrent()||creatingTask||!chosen||!detail)return;element<HTMLDialogElement>('library-dialog').close();switchTab('note')};
 element('notebook').querySelector('.note-head .actions')!.insertAdjacentHTML('beforeend','<button type="button" id="knowledge-library-open">查全局知识库</button>');
 element('notebook').querySelector('h2')!.textContent='本任务知识';
 element('notebook').querySelector('.note-head')!.insertAdjacentHTML('afterend','<p class="muted knowledge-panel-help">这里保留本任务的原始摘录和手工笔记。摘要、前提与共识、对话目录可以直接打开文档查看，无需手动分类。</p>');
 button('summarize').textContent='手动整理';button('summarize').title='调用本任务的 AI 整理知识，完成后可检查并保存';
 button('knowledge-library-open').textContent='打开本任务文档';button('knowledge-library-open').onclick=()=>void openLibrary(chosen);
 button('automatic-save').onclick=()=>void saveAutomaticKnowledge();
 for(const id of ['library-open','library-task','library-create'])button(id).onclick=()=>void openLibrary();
 button('library-close').onclick=()=>element<HTMLDialogElement>('library-dialog').close();
 element('library-search-form').onsubmit=e=>{e.preventDefault();void searchLibrary()};
 button('vault-save').onclick=()=>void saveVault();button('vault-refresh').onclick=()=>void refreshVault();
 installLibraryBrowser();
 installLibraryPages();
 installKnowledgeLayers();
 disposeWithShell(()=>{libraryRequest++;libraryOverviewRequest++;libraryPreviewRequest++;libraryCitationRequest++;libraryCitationBusy=false;libraryLoading=false;libraryBrowseStarted=false;libraryTarget=null;libraryHits=[];knowledgeEditTarget=null;knowledgeSaving=false;knowledgeExpanded.clear();automaticSettingsRequest++;automaticSettingsReady=false;automaticSettingsSaving=false;automaticSettingsDirty=false;vaultSettingsRequest++;vaultSettingsReady=false;vaultSettingsSaving=false;vaultRefreshing=false;vaultSettingsDirty=false;knowledgeSettingsRequest++;knowledgeSettingsLoading=null});
}
function installLibraryBrowser(){
 const dialog=element<HTMLDialogElement>('library-dialog');dialog.setAttribute('aria-label','知识库');
 dialog.querySelector('h2')!.textContent='知识库';
 dialog.querySelector('.library-header p')!.textContent='直接阅读 Markdown 文档，再把需要的资料引用到新任务。';
 element('library-search-form').insertAdjacentHTML('beforebegin','<div id="library-overview" class="library-overview" role="status"></div><details class="library-help"><summary>AI 什么时候会用到这些内容？</summary><p>连续对话沿用原生会话；新会话按设置补回本任务摘要。其他任务的资料可以选中后引用，文档带有相对来源链接。打开文档不会调用模型；自动标签用于查找，不需要逐条分类。</p></details>');
 element('library-kind').innerHTML='<option value="knowledge">知识条目</option><option value="run">原始对话</option><option value="note">独立文件笔记</option><option value="">全部资料</option>';
 element('library-kind').insertAdjacentHTML('afterend','<select id="library-source" aria-label="知识来源"><option value="">全部来源</option><option value="auto">对话自动记录</option><option value="manual">手工保存与整理</option><option value="vault">知识文件目录</option></select>');
 element('library-search-form').insertAdjacentHTML('beforebegin','<nav class="library-layers" aria-label="知识文档层级"><button type="button" data-library-layer="tasks" class="selected" aria-pressed="true">任务资料</button><button type="button" data-library-layer="knowledge" aria-pressed="false">通用知识</button><button type="button" data-library-layer="topics" aria-pressed="false">主题索引</button><button type="button" data-library-layer="all" aria-pressed="false">全部文档</button></nav><input type="hidden" id="library-layer" value="tasks">');
 input('library-kind').value='';
 const filters=document.createElement('div');filters.className='library-filters';
 const form=element('library-search-form');form.append(filters);
 for(const id of ['library-kind','library-source','library-scope'])filters.append(element(id));
 filters.append(input('library-stale').closest('label')!);
 for(const id of ['library-kind','library-source'])element(id).classList.add('hidden');
 input('library-stale').closest('label')!.classList.add('hidden');
 filters.insertAdjacentHTML('beforeend','<input id="library-tag" placeholder="按标签查找（可选）" aria-label="按标签查找" maxlength="32">');
 dialog.querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>b.onclick=()=>{input('library-layer').value=b.dataset.libraryLayer!;dialog.querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(x=>{x.classList.toggle('selected',x===b);x.setAttribute('aria-pressed',String(x===b))});void searchLibrary()});
 input('library-tag').onchange=()=>void searchLibrary();
 for(const id of ['library-kind','library-source','library-scope','library-stale'])input(id).onchange=()=>{if(id==='library-kind')input('library-source').value='';if(id==='library-source'&&['manual','auto'].includes(input(id).value))input('library-kind').value='knowledge';void searchLibrary()};
 const workspace=document.createElement('div');workspace.id='library-workspace';workspace.className='library-workspace';
 element('library-results').before(workspace);
 const list=document.createElement('section');list.className='library-list-pane';workspace.append(list);list.append(element('library-results'));
 list.insertAdjacentHTML('beforeend','<button type="button" id="library-more" class="hidden">加载更多</button>');
 workspace.insertAdjacentHTML('beforeend','<section id="library-preview" class="library-preview" aria-label="资料预览"><button type="button" id="library-preview-back" class="subtle">← 返回结果</button><h3 id="library-preview-title">选择一条资料</h3><p id="library-preview-meta" class="muted">预览内容和来源后，再决定是否引用。</p><div id="library-preview-content" class="content"></div><p id="library-preview-status" role="status"></p><button type="button" id="library-read-more" class="hidden">继续阅读全文</button><div class="library-preview-actions"><button type="button" id="library-download" class="hidden">下载 Markdown</button><button type="button" id="library-preview-cite" class="primary hidden">引用到输入框</button><button type="button" id="library-preview-source" class="hidden">打开来源任务</button></div></section>');
 button('library-more').onclick=()=>void searchLibrary(true);
 button('library-read-more').onclick=()=>{libraryPreviewLimit+=20000;renderLibraryDocument()};
 button('library-download').onclick=()=>{const hit=libraryHits.find(d=>d.id===libraryPreviewID);if(!hit||!libraryPreviewText)return;const url=URL.createObjectURL(new Blob([libraryPreviewText],{type:'text/markdown;charset=utf-8'}));const link=document.createElement('a');link.href=url;link.download=(hit.path?.split('/').at(-1)||'document.md');link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)};
 element('library-preview-content').onclick=e=>{const link=(e.target as HTMLElement).closest<HTMLAnchorElement>('a');if(!link)return;const href=link.getAttribute('href')||'';if(!href||/^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i.test(href))return;e.preventDefault();void followLibraryLink(href)};
 button('library-preview-back').onclick=()=>clearLibraryPreview(true);
 button('library-preview-cite').onclick=()=>void citeLibrary(libraryPreviewID);
 button('library-preview-source').onclick=()=>{const hit=libraryHits.find(d=>d.id===libraryPreviewID),taskID=hit?.source_task_id||hit?.task_id;if(!hit||!taskID||!tasks.some(t=>t.id===taskID&&!t.deleted))return;dialog.close();void choose(taskID,hit.kind==='knowledge'||hit.kind==='curated'?'note':'chat')};
 element('library-results').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-library-reference],[data-library-preview]');if(!b)return;if(b.dataset.libraryReference)void citeLibrary(b.dataset.libraryReference);else void previewLibrary(b.dataset.libraryPreview||'')};
 dialog.addEventListener('close',()=>{libraryRequest++;libraryOverviewRequest++;libraryPreviewRequest++;libraryLoading=false;libraryTarget=null});
 element('knowledge-filter').classList.add('hidden');input('knowledge-state').closest('label')?.classList.add('hidden');
 element('knowledge-filter').insertAdjacentHTML('beforebegin','<div class="knowledge-search"><input id="knowledge-query" aria-label="搜索本任务知识" placeholder="搜索本任务知识"><select id="knowledge-source" aria-label="筛选知识来源"><option value="">全部来源</option><option value="auto">对话自动记录</option><option value="manual">手工保存与整理</option></select></div>');
 input('knowledge-query').oninput=()=>renderKnowledgeList();input('knowledge-source').onchange=()=>renderKnowledgeList();
}
function clearLibraryPreview(returnFocus=false){
 const previous=libraryPreviewID;
 libraryPreviewRequest++;libraryPreviewID='';libraryPreviewText='';button('library-read-more')?.classList.add('hidden');button('library-download')?.classList.add('hidden');element('library-workspace')?.classList.remove('preview-open');
 element('library-preview-title').textContent='选择一条资料';element('library-preview-meta').textContent='预览内容和来源后，再决定是否引用。';element('library-preview-content').textContent='';element('library-preview-status').textContent='';
 button('library-preview-cite').classList.add('hidden');button('library-preview-source').classList.add('hidden');
 libraryPreviewDocument=null;button('library-promote')?.classList.add('hidden');button('library-curated-edit')?.classList.add('hidden');
 highlightLibraryPreview();
 if(returnFocus)Array.from(element('library-results').querySelectorAll<HTMLButtonElement>('[data-library-preview]')).find(b=>b.dataset.libraryPreview===previous)?.focus();
}
function highlightLibraryPreview(){element('library-results').querySelectorAll<HTMLButtonElement>('[data-library-preview]').forEach(b=>{const selected=b.dataset.libraryPreview===libraryPreviewID;b.setAttribute('aria-pressed',String(selected));const card=b.closest<HTMLElement>('.library-card');if(card)card.dataset.selected=String(selected)})}
async function loadLibraryOverview(){
 const token=++libraryOverviewRequest,epoch=shellEpoch;
 element('library-overview').textContent='正在读取积累与同步状态…';
 const results=await Promise.allSettled([api<AutomaticKnowledgeConfig>('library/automatic','GET',undefined,shellController.signal),api<VaultSettingsResponse>('library/vault','GET',undefined,shellController.signal)]);
 if(token!==libraryOverviewRequest||!shellCurrent(epoch))return;
 const automatic=results[0],vault=results[1];const parts:string[]=[];
 if(automatic.status==='fulfilled'){parts.push(automatic.value.capture?'自动记录：开启':'自动记录：关闭');parts.push(automatic.value.recall?'新会话：补回本任务知识':'新会话：不自动补回')}else parts.push('自动积累状态读取失败');
 if(vault.status==='fulfilled'){const v=vault.value;parts.push(!v.config.enabled?'文件同步：未启用':v.report.error?'文件同步：失败':v.report.conflicts?.length?'文件同步：有冲突':v.report.updated?'文件同步：已启用':'文件同步：等待首次同步')}else parts.push('文件同步状态读取失败');
 element('library-overview').innerHTML=parts.map(s=>'<span>'+escapeHTML(s)+'</span>').join('');
}
function installLibrarySettings(){
 element('library-dialog').insertAdjacentHTML('beforeend',`<section id="settings-knowledge" class="library-settings-panel hidden" aria-label="积累设置"><h3>积累设置</h3><p>先选一个便于阅读和同步的目录。每个任务有一篇 Markdown 主文档，集中阅读目标、当前进展、结论和后续事项；可直接用 Obsidian 或其他编辑器打开。</p><details class="knowledge-storage-info"><summary>运行数据与迁移（高级）</summary><code id="knowledge-data-directory" class="data-dir-path"></code><p>任务、对话和知识共用此目录中的数据库。下面设置的积累目录用于保存可阅读的 Markdown，不会迁移运行数据；切换完整运行数据请到“数据与存储”。</p></details></section>`);
 const section=element('settings-knowledge');
 for(const id of ['vault-settings','automatic-knowledge-settings']){const panel=element<HTMLDetailsElement>(id);panel.open=true;section.append(panel)}
 section.append(section.querySelector('.knowledge-storage-info')!);
 element('vault-settings').querySelector('summary')!.textContent='1. 积累目录与文档同步';
 element('vault-settings').querySelector('p')!.textContent='选择运行 Duo 的这台电脑上的目录，也可以使用已有的 Obsidian Vault。Duo 只读写其中的 Duo/ 文件夹。开启同步后每分钟更新一次，也可立即同步。';
 input('vault-directory').previousSibling!.textContent='积累根目录（Duo 服务所在电脑）';
 input('vault-directory').placeholder='留空使用当前数据目录下的 knowledge';
 input('vault-directory').closest('label')!.insertAdjacentHTML('afterend','<p>可留空使用默认目录：当前运行数据目录下的 <code>knowledge</code>。实际文件目录：<code id="vault-document-directory">尚未设置</code></p><div class="knowledge-directory-guide"><strong>一任务一篇主文档</strong><code>Duo/tasks/任务名-短ID.md</code><p>例如 <code>Duo/tasks/375调试-a1b2c3d4.md</code>。任务来源与历次对话放在同级任务资料夹中；通用结论和主题索引另存，主文档用相对链接引用。</p></div><p class="muted">选择下方要同步的内容并保存。更换目录会同步当前资料，旧目录保留；独立外部笔记需自行复制。文件名首次导出后保持不变，修改任务名称会更新文档标题。</p>');
 element('automatic-knowledge-settings').querySelector('summary')!.textContent='2. 每轮自动记录与整理';
 element('automatic-knowledge-settings').querySelector('p')!.textContent='开启“自动记录”保存后续完成的对话；再开启“自动整理”，让同一轮 AI 回复提取摘要、共识、疑问和有依据的知识，不另起模型调用。此处保存后，若要写到上面的目录，还需开启“Markdown 同步与索引”和“同步自动整理的任务资料”。模型未提供整理结果时，保留原对话。';
 button('vault-save').textContent='保存知识库路径与同步设置';
 input('vault-directory').onkeydown=e=>{if(e.key==='Enter'&&!e.isComposing){e.preventDefault();void saveVault()}};
 for(const id of ['vault-directory','vault-enabled','vault-runs','vault-automatic'])input(id).oninput=input(id).onchange=()=>{vaultSettingsDirty=true;element('vault-status').textContent='路径与同步设置尚未保存。'};
 for(const id of ['automatic-capture','automatic-organize','automatic-recall'])input(id).onchange=()=>{automaticSettingsDirty=true;element('automatic-status').textContent='自动记录与整理设置尚未保存。'};
 setVaultControlsDisabled(true);
}
function installLibraryPages(){
 const dialog=element<HTMLDialogElement>('library-dialog'),section=element('settings-knowledge'),header=dialog.querySelector('.library-header')!;
 const browse=document.createElement('section');browse.id='library-browse-panel';browse.setAttribute('aria-label','浏览资料');
 for(const node of Array.from(dialog.children))if(node!==header&&node!==section)browse.append(node);
 header.insertAdjacentHTML('afterend','<nav class="library-pages" aria-label="知识库页面"><button type="button" id="library-browse-open" class="selected" aria-pressed="true" aria-controls="library-browse-panel">浏览资料</button><button type="button" id="library-settings-open" aria-pressed="false" aria-controls="settings-knowledge">积累设置</button></nav>');
 dialog.append(browse,section);
 button('library-browse-open').onclick=()=>{showLibraryPage('browse');void loadLibraryOverview();if(!libraryBrowseStarted)void searchLibrary();input('library-query').focus()};
 button('library-settings-open').onclick=()=>void openLibrarySettings();
 dialog.addEventListener('close',()=>{
  // Clean settings are refreshed on the next visit; unsaved drafts stay intact.
  libraryBrowseStarted=false;
  if(!automaticSettingsSaving&&!automaticSettingsDirty){automaticSettingsRequest++;automaticSettingsReady=false}
  if(!vaultSettingsSaving&&!vaultRefreshing&&!vaultSettingsDirty){vaultSettingsRequest++;vaultSettingsReady=false}
  knowledgeSettingsRequest++;knowledgeSettingsLoading=null;
 });
}
function showLibraryPage(page:'browse'|'settings'){
 const setup=page==='settings';element('library-browse-panel').classList.toggle('hidden',setup);element('settings-knowledge').classList.toggle('hidden',!setup);
 for(const id of ['library-browse-open','library-settings-open']){const active=(id==='library-settings-open')===setup;button(id).classList.toggle('selected',active);button(id).setAttribute('aria-pressed',String(active))}
 button('library-refresh-files').classList.toggle('hidden',setup);button('library-manage-task').classList.toggle('hidden',setup);
 if(button('library-graph-open')){if(setup){button('library-graph-open').classList.remove('selected');button('library-graph-open').setAttribute('aria-pressed','false')}else applyKnowledgeGraphMode()}
}
async function openLibrarySettings(){
 const epoch=shellEpoch,dialog=element<HTMLDialogElement>('library-dialog');
 if(!dialog.open){await openLibrary(undefined,'settings');return}
 showLibraryPage('settings');button('library-settings-open').focus();await loadKnowledgeSettings();
 if(!shellCurrent(epoch)||!dialog.open||element('settings-knowledge').classList.contains('hidden'))return;
}
async function loadKnowledgeSettings(){
 if(knowledgeSettingsLoading)return knowledgeSettingsLoading;
 const epoch=shellEpoch,request=++knowledgeSettingsRequest;
 element('knowledge-data-directory').textContent=settings.data_dir||'正在读取当前数据目录…';
 const loading=(async()=>{
  const results=await Promise.allSettled([api<Settings>('settings','GET',undefined,shellController.signal),automaticSettingsReady?Promise.resolve():loadAutomaticKnowledge(),vaultSettingsReady?Promise.resolve():loadVault(),loadKnowledgeExtractionSettings()]);
  if(request!==knowledgeSettingsRequest||!shellCurrent(epoch))return;
  // This read only updates the location hint, never another settings editor's draft.
  const location=results[0];element('knowledge-data-directory').textContent=location.status==='fulfilled'?(location.value.data_dir||'未能读取当前数据目录'):'当前数据目录读取失败；可在“数据与存储”查看。';
 })();knowledgeSettingsLoading=loading;
 try{await loading}finally{if(knowledgeSettingsLoading===loading)knowledgeSettingsLoading=null}
}
async function openLibrary(taskScope?:string,page:'browse'|'settings'='browse'){
 const epoch=shellEpoch;
 libraryTarget={task:chosen,create:creatingTask,selection,epoch:shellEpoch};
 const scope=element<HTMLSelectElement>('library-scope'),previousScope=scope.value;scope.innerHTML='<option value="">所有任务与文件</option>'+tasks.filter(t=>!t.deleted).map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');scope.value=tasks.some(t=>t.id===previousScope&&!t.deleted)?previousScope:'';
 if(taskScope){scope.value=taskScope;input('library-layer').value='tasks';element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const on=b.dataset.libraryLayer==='tasks';b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))})}
 showLibraryPage(page);const dialog=element<HTMLDialogElement>('library-dialog');if(!dialog.open)dialog.showModal();
 button('library-manage-task').disabled=!chosen||creatingTask||!detail;
 if(page==='settings'){button('library-settings-open').focus();await loadKnowledgeSettings();return}
 void loadLibraryOverview();
 await searchLibrary();
 if(!shellCurrent(epoch)||!dialog.open||element('library-browse-panel').classList.contains('hidden'))return;
 input('library-query').focus();
}
function setAutomaticSettingsDisabled(disabled:boolean){for(const id of ['automatic-capture','automatic-recall','automatic-organize','automatic-save'])input(id).disabled=disabled}
async function loadAutomaticKnowledge(){
 if(automaticSettingsSaving||automaticSettingsDirty)return;
 const token=++automaticSettingsRequest,epoch=shellEpoch;automaticSettingsReady=false;setAutomaticSettingsDisabled(true);element('automatic-status').textContent='正在读取设置…';
 try{const config=await api<AutomaticKnowledgeConfig>('library/automatic','GET',undefined,shellController.signal);if(token!==automaticSettingsRequest||!shellCurrent(epoch))return;input('automatic-capture').checked=config.capture;input('automatic-recall').checked=config.recall;input('automatic-organize').checked=config.organize!==false;automaticSettingsReady=true;element('automatic-status').textContent=config.capture?'自动记录已开启，对后续完成的对话生效。':'自动记录已关闭，已有记录继续保留。'}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent=(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch))setAutomaticSettingsDisabled(!automaticSettingsReady)}
}
async function saveAutomaticKnowledge(){
 if(!automaticSettingsReady||automaticSettingsSaving)return;
 const epoch=shellEpoch,token=++automaticSettingsRequest;automaticSettingsSaving=true;setAutomaticSettingsDisabled(true);
 try{await api('library/automatic','PUT',{capture:input('automatic-capture').checked,recall:input('automatic-recall').checked,organize:input('automatic-organize').checked});if(token===automaticSettingsRequest&&shellCurrent(epoch)){automaticSettingsDirty=false;element('automatic-status').textContent='设置已保存。关闭不会删除已有知识，也不会从原生会话中移除之前已带入的内容。'}}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent='保存失败，请重试：'+(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch)){automaticSettingsSaving=false;setAutomaticSettingsDisabled(false)}}
}
async function searchLibrary(append=false){
 if(append&&libraryLoading)return;
 const token=++libraryRequest,epoch=shellEpoch;libraryLoading=true;libraryBrowseStarted=true;
 if(!append){libraryHits=[];libraryNextOffset=0;libraryHasMore=false;clearLibraryPreview();element('library-results').textContent='';button('library-more').classList.add('hidden');if(knowledgeGraphOpen){knowledgeGraphRequest++;knowledgeGraphDocuments=[];element('knowledge-graph').innerHTML='';element('knowledge-graph-accessible').textContent='';element('knowledge-graph-state').textContent='正在按当前条件重新读取图谱…'}}
 const q=new URLSearchParams({q:input('library-query').value,kind:input('library-kind').value,source:input('library-source').value,task:input('library-scope').value,stale:input('library-stale').checked?'1':'0',layer:input('library-layer')?.value||'',tag:input('library-tag')?.value.trim()||'',offset:String(append?libraryNextOffset:0)});
 element('library-state').textContent=append?'正在加载更多…':'正在检索…';button('library-more').disabled=true;
 try{const result=await api<{documents:LibraryDocument[];truncated:boolean;total?:number;next_offset?:number}>('library/search?'+q,'GET',undefined,shellController.signal);if(token!==libraryRequest||!shellCurrent(epoch))return;
  libraryHits=append?[...new Map([...libraryHits,...result.documents].map(d=>[d.id,d])).values()]:result.documents;libraryNextOffset=result.next_offset??libraryHits.length;
  element('library-state').textContent=`显示 ${libraryHits.length} / ${result.total??libraryHits.length} 条资料。${libraryTarget?.task||libraryTarget?.create?'引用会加入输入框，发送前仍可修改。':'可先预览；选择或新建任务后才能引用。'}`;
  libraryHasMore=result.truncated;renderLibraryHits();button('library-more').classList.toggle('hidden',knowledgeGraphOpen||!result.truncated);if(knowledgeGraphOpen)void loadKnowledgeGraph();
 }catch(e){if(token===libraryRequest&&shellCurrent(epoch))element('library-state').textContent='检索失败，请重试：'+(e as Error).message}
 finally{if(token===libraryRequest&&shellCurrent(epoch)){libraryLoading=false;button('library-more').disabled=false}}
}
function libraryDescription(d:LibraryDocument){return ({consensus:'平台共识',system:'系统知识',project:'项目知识',tasks:'任务资料',knowledge:'候选结论',topics:'主题索引',history:'对话记录',notes:'任务笔记',files:'Markdown 文档'} as Record<string,string>)[d.layer||'']||(d.kind==='run'?'对话记录':'任务笔记')}
function renderLibraryHits(){
 element('library-results').innerHTML=libraryHits.length?libraryHits.map(d=>`<article class="library-card"${d.id===libraryPreviewID?' data-selected="true"':''}><h3><button type="button" data-library-preview="${escapeHTML(d.id)}">${escapeHTML(d.title)}</button></h3><p class="library-card-meta">${escapeHTML(libraryDescription(d))} · ${escapeHTML(d.scope_key||d.task_title||'共用知识')}</p><code class="library-document-path">${escapeHTML(d.path||'')}</code><p class="library-tags">${(d.tags||[]).map(t=>'#'+escapeHTML(t)).join(' · ')}</p><footer><span>${new Date(d.updated).toLocaleDateString()}</span><button type="button" data-library-reference="${escapeHTML(d.id)}"${libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create)?' disabled':''}>引用</button></footer></article>`).join(''):'<div class="library-empty"><strong>暂时没有匹配的文档</strong><p>任务过程随对话保存；可复用结论先保留为候选，再显式整理成平台共识、系统知识或项目知识。旧摘录及主题索引可在“全部资料”查看。</p></div>';
}
async function previewLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit)return;
 const token=++libraryPreviewRequest,epoch=shellEpoch;libraryPreviewID=id;
 libraryPreviewDocument=null;button('library-promote').classList.add('hidden');button('library-curated-edit').classList.add('hidden');
 libraryPreviewText='';button('library-download').classList.add('hidden');button('library-read-more').classList.add('hidden');
 element('library-workspace').classList.add('preview-open');element('library-preview-title').textContent=hit.title;
 element('library-preview-meta').textContent=libraryDescription(hit)+' · '+(hit.task_title||'独立笔记')+(hit.path?' · Duo/'+hit.path:'');
 element('library-preview-content').textContent='';element('library-preview-status').textContent='正在读取预览…';button('library-preview-cite').classList.add('hidden');button('library-preview-source').classList.add('hidden');highlightLibraryPreview();
 try{const result=await api<LibraryDocument>('library/document?'+new URLSearchParams({id,hash:hit.hash}),'GET',undefined,shellController.signal);if(token!==libraryPreviewRequest||!shellCurrent(epoch))return;
  libraryPreviewText=result.content||'';libraryPreviewLimit=20000;libraryPreviewDocument=result;renderLibraryDocument();
  button('library-promote').classList.toggle('hidden',result.kind==='curated');button('library-curated-edit').classList.toggle('hidden',result.kind!=='curated');
  button('library-preview-cite').classList.remove('hidden');button('library-preview-cite').disabled=libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create);
  button('library-preview-source').classList.toggle('hidden',!tasks.some(t=>t.id===(result.source_task_id||result.task_id)&&!t.deleted));
 }catch(e){if(token===libraryPreviewRequest&&shellCurrent(epoch))element('library-preview-status').textContent='预览失败，请重新检索后打开：'+(e as Error).message}
}
function knowledgeStateText(status:string){return status==='verified'?'已验证':status==='stale'?'已过时':'待验证'}
async function citeLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit||libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create))return;
 const target=libraryTarget!,query=libraryRequest,token=++libraryCitationRequest;libraryCitationBusy=true;setLibraryCitationDisabled(true);
 try{const result=await api<LibraryReference>('library/reference?'+new URLSearchParams({id,hash:hit.hash}));if(libraryTarget!==target||!libraryTargetCurrent()||query!==libraryRequest){notify('目标或检索已变化，请重新选择引用。');return}
  const box=appendKnowledgeReference(result.reference,target.task,target.create);
  element<HTMLDialogElement>('library-dialog').close();box.focus();notify(result.truncated?'已加入带来源的引用；长资料已截断，可在发送前检查。':'已加入带来源的引用，可在发送前检查。');
 }catch(e){if(shellCurrent(target.epoch))notify((e as Error).message)}
 finally{if(token===libraryCitationRequest&&shellCurrent(target.epoch)){libraryCitationBusy=false;setLibraryCitationDisabled(false)}}
}
function setLibraryCitationDisabled(disabled:boolean){
 element('library-results').querySelectorAll<HTMLButtonElement>('[data-library-reference]').forEach(b=>b.disabled=disabled||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create));
 button('library-preview-cite').disabled=disabled||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create);
}
function appendKnowledgeReference(reference:string,task:string,create=false){
 const box=input(create?'create-input':'message');if(box.value.length+reference.length>60000)throw new Error('引用内容过多，请先精简当前输入。');
 box.value=box.value.trimEnd()+reference;if(!create)drafts.set(task,box.value);box.dispatchEvent(new Event('input',{bubbles:true}));return box;
}
async function citeTaskKnowledge(k:Knowledge){
 if(libraryCitationBusy||k.task_id!==chosen)return;
 const epoch=shellEpoch,selected=selection,token=++libraryCitationRequest;libraryCitationBusy=true;
 try{const result=await api<LibraryReference>('library/reference?'+new URLSearchParams({id:'knowledge:'+k.id,revision:String(k.revision)}));if(!shellCurrent(epoch)||selection!==selected||chosen!==k.task_id)return;
  const box=appendKnowledgeReference(result.reference,k.task_id);switchTab('chat');box.focus();notify(result.truncated?'已引用知识，长内容已截断，请在发送前检查。':'已加入带来源的引用。');
 }catch(e){if(shellCurrent(epoch)&&selection===selected)notify((e as Error).message)}
 finally{if(token===libraryCitationRequest&&shellCurrent(epoch))libraryCitationBusy=false}
}
function showVaultReport(r:VaultReport){
 element('vault-status').textContent=r.error?'同步失败：'+r.error:r.updated?`索引 ${r.indexed} 份文档；写入 ${r.exported}，导入结论修改 ${r.imported}。${r.conflicts?.length?'存在编辑冲突，请查看详情。':''}`:'尚未同步。';
 const details=[...(r.conflicts||[]).map(s=>'冲突：'+s),...(r.warnings||[]).map(s=>'提示：'+s)];element('vault-problems').textContent=details.join('\n');element('vault-problems').classList.toggle('hidden',!details.length);
}
function setVaultControlsDisabled(disabled:boolean){for(const id of ['vault-directory','vault-consensus-directory','vault-system-directory','vault-project-directory','vault-enabled','vault-runs','vault-automatic','vault-save','vault-refresh'])if(element(id))input(id).disabled=disabled}
function showVaultLocation(directory:string,enabled:boolean){element('vault-document-directory').textContent=directory?(directory+(enabled?'':'（同步未启用）')):'尚未设置有效的知识文件根目录'}
async function loadVault(){
 if(vaultSettingsSaving||vaultRefreshing||vaultSettingsDirty)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsReady=false;setVaultControlsDisabled(true);element('vault-status').textContent='正在读取知识库设置…';
 try{const v=await api<VaultSettingsResponse>('library/vault','GET',undefined,shellController.signal);if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;input('vault-directory').value=v.config.directory||'';input('vault-consensus-directory').value=v.config.consensus_directory||'consensus';input('vault-system-directory').value=v.config.system_directory||'system';input('vault-project-directory').value=v.config.project_directory||'projects';input('vault-enabled').checked=v.config.enabled;input('vault-runs').checked=v.config.include_runs;input('vault-automatic').checked=!!v.config.include_automatic;showVaultLocation(v.document_directory,v.config.enabled);showVaultReport(v.report);vaultSettingsReady=true}
 catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch))setVaultControlsDisabled(!vaultSettingsReady)}
}
async function saveVault(){
 if(!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsSaving=true;setVaultControlsDisabled(true);
 try{const saved=await api<VaultConfig&{document_directory:string}>('library/vault','PUT',{directory:input('vault-directory').value.trim(),consensus_directory:input('vault-consensus-directory').value.trim(),system_directory:input('vault-system-directory').value.trim(),project_directory:input('vault-project-directory').value.trim(),enabled:input('vault-enabled').checked,include_runs:input('vault-runs').checked,include_automatic:input('vault-automatic').checked,task_folders:true});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;vaultSettingsDirty=false;showVaultLocation(saved.document_directory,saved.enabled);element('vault-status').textContent='设置已保存，正在同步…';await syncVaultView(epoch,token)}
 catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch)){vaultSettingsSaving=false;setVaultControlsDisabled(false)}}
}
async function syncVaultView(epoch:number,token:number){
 const report=await api<VaultReport>('library/vault/refresh','POST',{});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;showVaultReport(report);
 if(element<HTMLDialogElement>('library-dialog').open)await searchLibrary();if(chosen)await loadKnowledge();
}
async function refreshVault(){
 if(!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultRefreshing=true;setVaultControlsDisabled(true);element('vault-status').textContent='正在同步 Markdown 与索引…';
 try{await syncVaultView(epoch,token)}catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch)){vaultRefreshing=false;setVaultControlsDisabled(false)}}
}

function renderLibraryDocument(){
 const cut=libraryPreviewText.length>libraryPreviewLimit;
 const d=libraryPreviewDocument;
 const metadata=d?.kind==='curated'?'<section class="curated-metadata"><p><strong>'+escapeHTML(libraryDescription(d))+'</strong> · '+escapeHTML(knowledgeStateText(d.status))+(d.scope_key?' · '+escapeHTML(d.scope_key):'')+'</p>'+[['适用条件',d.applicability],['实际依据',d.evidence],['安全配置引用',d.credential_ref],['原始来源',d.source_id]].filter(([,value])=>value).map(([title,value])=>'<p><strong>'+title+'</strong><br>'+escapeHTML(value!)+'</p>').join('')+'</section>':'';
 element('library-preview-content').innerHTML=metadata+markdown(libraryPreviewText.slice(0,libraryPreviewLimit));
 button('library-read-more').classList.toggle('hidden',!cut);button('library-download').classList.toggle('hidden',!libraryPreviewText);
 element('library-preview-status').textContent=cut?'长文档分段显示，可继续阅读或下载全文。':'已读取全文。引用到输入框时最多带入 6000 字符，并保留来源。';
}
async function followLibraryLink(href:string){
 const current=libraryHits.find(d=>d.id===libraryPreviewID);if(!current?.path)return;
 const parts=current.path.split('/').slice(0,-1);for(const part of href.split('#')[0].split('/')){if(!part||part==='.')continue;if(part==='..'){if(!parts.length){notify('链接超出知识目录');return}parts.pop()}else parts.push(part)}
 const relative=parts.join('/');if(!relative.endsWith('.md'))return;
 const request=++libraryPreviewRequest,epoch=shellEpoch;
 try{const doc=await api<LibraryDocument>('library/document?'+new URLSearchParams({id:'document:'+relative}));if(request!==libraryPreviewRequest||!shellCurrent(epoch))return;libraryHits=libraryHits.filter(d=>d.id!==doc.id);libraryHits.push(doc);await previewLibrary(doc.id)}catch(e){if(request===libraryPreviewRequest&&shellCurrent(epoch))element('library-preview-status').textContent='无法打开来源文档：'+(e as Error).message}
}

type CuratedKnowledge={id:string;layer:string;scope_key:string;title:string;content:string;status:string;source_id:string;source_hash:string;source_task_id:string;applicability:string;evidence:string;credential_ref:string;tags:string[];revision:number;created:number;updated:number};
type WorkspaceMemoryReport={enabled:boolean;directory:string;task_directory:string;exported:number;unchanged:number;tasks:{id:string;title:string;path:string;updated:number;excerpt:string}[];documents?:{path:string;title:string;kind:string}[];conflicts:string[];warnings:string[];error?:string};
let libraryPreviewDocument:LibraryDocument|null=null,curatedDraft:Partial<CuratedKnowledge>|null=null,curatedDirty=false,curatedSaving=false,curatedRequest=0,workspaceMemoryRequest=0,workspaceMemorySaving=false;
function installKnowledgeLayers(){
 element('library-open').insertAdjacentHTML('afterend','<button type="button" id="library-consensus-open">平台共识</button><button type="button" id="library-system-open">系统知识</button><button type="button" id="library-project-open">项目知识</button>');
 for(const layer of ['consensus','system','project'])button('library-'+layer+'-open').onclick=()=>void openKnowledgeLayer(layer);
 const nav=element('library-dialog').querySelector('.library-layers')!;
 nav.innerHTML=[['tasks','任务过程'],['consensus','平台共识'],['system','系统知识'],['project','项目知识'],['knowledge','候选结论'],['all','全部资料']].map(([id,title])=>`<button type="button" data-library-layer="${id}" aria-pressed="${id==='tasks'}" class="${id==='tasks'?'selected':''}">${title}</button>`).join('');
 nav.querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>b.onclick=()=>{input('library-layer').value=b.dataset.libraryLayer!;input('library-kind').value='';input('library-source').value='';input('library-scope').value='';nav.querySelectorAll<HTMLButtonElement>('button').forEach(x=>{x.classList.toggle('selected',x===b);x.setAttribute('aria-pressed',String(x===b))});void searchLibrary()});
 element('library-preview').querySelector('.library-preview-actions')!.insertAdjacentHTML('beforeend','<button type="button" id="library-promote" class="hidden">整理为正式知识</button><button type="button" id="library-curated-edit" class="hidden">编辑正式知识</button>');
 element('library-search-form').insertAdjacentHTML('afterend','<div class="actions" id="library-curated-actions"><button type="button" id="library-curated-new">新建正式知识</button><button type="button" id="library-extract">一键提取已有资料</button><button type="button" id="library-ai-extract">AI 整理并提取本任务</button><span class="muted">已有候选可直接提取；AI 整理会发送本任务历史并消耗模型额度。</span></div><p id="library-extraction-state" class="muted" role="status"></p>');
 button('library-extract').onclick=()=>void extractRecordedKnowledge();button('library-ai-extract').onclick=()=>void extractTaskKnowledgeWithAI();
 button('library-curated-new').onclick=()=>openCuratedEditor();button('library-promote').onclick=()=>openCuratedEditor(libraryPreviewDocument||undefined);button('library-curated-edit').onclick=()=>openCuratedEditor(libraryPreviewDocument||undefined);
 element('library-dialog').querySelector('.library-header p')!.textContent='任务过程、平台共识、系统知识和项目知识分层管理，保留原始来源。';
 element('library-dialog').querySelector('.library-help p')!.textContent='连续对话沿用原生会话；开启自动恢复后，新会话补回本任务摘要、同工作区其他任务的交接和已确认的平台共识。只补回有限片段；系统及项目知识先搜索、引用。引用计入模型输入。知识不授予工具或设备权限。';
 element('settings-knowledge').querySelector('p')!.textContent='过程资料随工作区，共识随 Duo，系统和项目知识放在你指定的 Obsidian 目录。已有记录与旧目录保留，不自动删除或搬移。';
 const guide=element('settings-knowledge').querySelector('.knowledge-directory-guide')!;
 guide.innerHTML='<strong>按任务阅读，按层级复用</strong><p>工作区过程：<code>&lt;workspace&gt;/.duo/tasks/任务名-短ID/</code>，包含任务说明、交接摘要和逐轮记录，改名不会丢失历史。</p><p>在上方选择 Obsidian 根目录后，保存即创建三类文件夹：<code>Duo/consensus/</code>、<code>Duo/system/</code>、<code>Duo/projects/</code>。下方可分别自定义文件夹名；更换目录保留原文件。</p><p>平台共识、系统知识和项目知识都保留标签与来源，可在 Duo 中阅读、编辑和查看关系图谱。已确认的平台共识才自动供新会话参考。</p>';
 input('vault-directory').placeholder='选择你的 Obsidian 笔记根目录';
 const locationHint=element('vault-document-directory');locationHint.parentElement!.replaceChildren(document.createTextNode('选择根目录并开启同步后，知识文件将写入三类目录。实际文件目录：'),locationHint);
 input('vault-directory').closest('label')!.insertAdjacentHTML('afterend','<div class="knowledge-layer-directories"><label>平台共识文件夹<input id="vault-consensus-directory" placeholder="consensus" maxlength="200"></label><label>系统知识文件夹<input id="vault-system-directory" placeholder="system" maxlength="200"></label><label>项目知识文件夹<input id="vault-project-directory" placeholder="projects" maxlength="200"></label><p class="muted">文件夹相对于所选根目录内的 Duo/，可用中文名称或多级相对路径；三个目录不可相互包含。设置跟随本机，Markdown 与标签可通过 Git 等工具跨平台同步。</p></div>');
 for(const id of ['vault-consensus-directory','vault-system-directory','vault-project-directory']){input(id).disabled=!vaultSettingsReady;input(id).oninput=input(id).onchange=()=>{vaultSettingsDirty=true;element('vault-status').textContent='路径与同步设置尚未保存。'}}
 element('automatic-knowledge-settings').querySelector('p')!.textContent='自动记录把任务过程写入实际工作区。自动整理让正常回复输出摘要、带标签的分层候选，不另起模型；自动提取开启后把分类候选分别写入平台共识、系统知识和项目知识，均标为待验证。旧任务可一键提取已有候选，缺少候选时可选择 AI 重新整理。';
 input('automatic-recall').closest('label')!.lastChild!.textContent='新会话自动补回本任务摘要、同工作区交接和已确认的平台共识';
 element('automatic-knowledge-settings').querySelectorAll('p')[1].textContent='同一原生会话不重复补入历史。跨引擎新会话按来源去重，只恢复有界摘要。平台共识须明确确认；系统与项目知识先手动引用。';
 element('notebook').querySelector('.knowledge-panel-help')!.textContent='这里是本任务的初级资料与候选结论。过程自动写入工作区；跨任务复用的内容可到知识库整理为平台共识、系统知识或项目知识。';
 element('notebook').querySelector('.knowledge-panel-help')!.insertAdjacentHTML('afterend','<section class="workspace-memory-panel"><strong>工作区过程资料</strong><p id="workspace-memory-state" role="status">打开本任务知识时读取目录。</p><div id="workspace-memory-tasks"></div><button type="button" id="workspace-memory-sync">回填 / 同步本任务已有记录</button></section>');
 button('workspace-memory-sync').onclick=()=>void loadWorkspaceMemory(true);
 element('workspace-memory-tasks').insertAdjacentHTML('afterend','<div id="workspace-memory-documents" class="workspace-memory-documents" aria-label="本任务记录文件"></div><section id="workspace-memory-reader" class="workspace-memory-reader hidden"><h3 id="workspace-memory-title"></h3><p id="workspace-memory-document-state" role="status"></p><div id="workspace-memory-content" class="content"></div></section>');
 element('workspace-memory-documents').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-workspace-document]');if(b)void readWorkspaceMemoryDocument(b.dataset.workspaceDocument||'')};
 element('workspace-memory-content').onclick=e=>{const link=(e.target as HTMLElement).closest<HTMLAnchorElement>('a');if(!link||!workspaceDocumentPath)return;const href=link.getAttribute('href')||'';if(!href||/^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i.test(href))return;e.preventDefault();const parts=workspaceDocumentPath.split('/').slice(0,-1);for(const part of href.split('#')[0].split('/')){if(!part||part==='.')continue;if(part==='..')parts.pop();else parts.push(part)}const target=parts.join('/');if(target.endsWith('.md'))void readWorkspaceMemoryDocument(target)};
 element('automatic-knowledge-settings').insertAdjacentHTML('beforeend','<div class="knowledge-extraction-settings"><label><input id="knowledge-extraction-enabled" type="checkbox" disabled>自动提取三类知识（均保留来源、标签及待验证状态）</label><p>复用本轮正常回复中的分层结果，不额外调用模型。先配置上方根目录并开启同步；没有分类候选时保留原记录，不编造知识。此开关单独保存。</p><button type="button" id="knowledge-extraction-save" disabled>保存自动提取设置</button><p id="knowledge-extraction-settings-state" role="status"></p></div>');
 input('knowledge-extraction-enabled').onchange=()=>{knowledgeExtractionSettingsDirty=true;element('knowledge-extraction-settings-state').textContent='自动提取设置尚未保存。'};
 button('knowledge-extraction-save').onclick=()=>void saveKnowledgeExtractionSettings();
 installKnowledgeGraph();
 element('root').insertAdjacentHTML('beforeend',`<dialog id="curated-dialog" class="curated-dialog" aria-labelledby="curated-heading"><div class="library-header"><h2 id="curated-heading">整理正式知识</h2><button type="button" id="curated-close">关闭</button></div><p>凭据保留在设备或受保护配置，正文只写配置引用。整理和标记状态不会启动模型或独立验证内容。</p><form id="curated-form"><label>知识层级<select id="curated-layer"><option value="consensus">平台共识</option><option value="system">系统知识</option><option value="project">项目知识</option></select></label><label>主题 / 项目名称<input id="curated-scope" maxlength="80" placeholder="平台共识无需填写"></label><label>标题<input id="curated-title" required maxlength="200"></label><label>正文<textarea id="curated-content" required rows="10"></textarea></label><label>适用条件<textarea id="curated-applicability" rows="2"></textarea></label><label>实际依据 / 验证结果<textarea id="curated-evidence" rows="2" placeholder="未验证时明确写出"></textarea></label><label>安全配置引用<input id="curated-credential" placeholder="仅设备或配置名称，不填写密码"></label><label>标签<input id="curated-tags" placeholder="用逗号分隔"></label><label>状态<select id="curated-status"><option value="observed">待验证</option><option value="verified">已验证（用户确认）</option><option value="stale">已过时</option></select></label><label><input type="checkbox" id="curated-confirm">我确认已填写实际依据和适用条件，并明确将此条标为已验证</label><p id="curated-source" class="muted"></p><p id="curated-state" role="status"></p><div class="actions"><button type="submit" id="curated-save" class="primary">保存正式知识</button><button type="button" id="curated-delete" class="danger hidden">移除正式条目</button></div></form></dialog>`);
 button('curated-close').onclick=()=>{if(!curatedSaving)element<HTMLDialogElement>('curated-dialog').close()};
 button('curated-close').insertAdjacentHTML('beforebegin','<button type="button" id="curated-discard">放弃草稿</button>');
 button('curated-discard').onclick=()=>{if(curatedSaving)return;curatedDirty=false;curatedDraft=null;element<HTMLDialogElement>('curated-dialog').close()};
 element('curated-form').oninput=()=>{curatedDirty=true;element('curated-state').textContent='草稿尚未保存。'};
 element('curated-form').onsubmit=e=>{e.preventDefault();void saveCuratedEditor()};
 button('curated-delete').onclick=()=>void deleteCuratedEditor();
 element('curated-dialog').addEventListener('cancel',e=>{if(curatedSaving)e.preventDefault()});
 disposeWithShell(()=>{curatedRequest++;workspaceMemoryRequest++;workspaceDocumentRequest++;knowledgeExtractionRequest++;knowledgeGraphRequest++;curatedDraft=null;curatedDirty=false;curatedSaving=false;workspaceMemorySaving=false;knowledgeExtractionBusy=false;knowledgeExtractionSettingsBusy=false;knowledgeExtractionSettingsReady=false;knowledgeExtractionSettingsDirty=false;knowledgeGraphOpen=false;knowledgeGraphDocuments=[];libraryPreviewDocument=null});
}
function openCuratedEditor(doc?:LibraryDocument){
 const dialog=element<HTMLDialogElement>('curated-dialog');
 if(curatedSaving)return;
 if(curatedDirty){if(!dialog.open)dialog.showModal();element('curated-state').textContent='保留了尚未保存的草稿，请先完成这份知识整理。';return}
 const edit=doc?.kind==='curated';
 curatedDraft=edit?{...doc,id:doc.id.replace(/^curated:/,'')}:{layer:['consensus','system','project'].includes(input('library-layer').value)?input('library-layer').value:'consensus',source_id:doc?.id||'',source_hash:doc?.hash||'',source_task_id:doc?.task_id||'',status:'observed'};
 for(const [id,value] of Object.entries({'curated-layer':curatedDraft.layer||'consensus','curated-scope':doc?.scope_key||'','curated-title':doc?.title||'','curated-content':doc?.content||'','curated-applicability':doc?.applicability||'','curated-evidence':doc?.evidence||'','curated-credential':doc?.credential_ref||'','curated-tags':(doc?.tags||[]).join(', '),'curated-status':edit?doc!.status:'observed'}))input(id).value=value;
 input('curated-confirm').checked=false;button('curated-delete').classList.toggle('hidden',!edit);element('curated-source').textContent=curatedDraft.source_id?'来源：'+curatedDraft.source_id:'手工创建；建议在正文或依据中记录出处。';element('curated-state').textContent='';if(!dialog.open)dialog.showModal();input('curated-title').focus();
}
function setCuratedBusy(busy:boolean){for(const node of element('curated-form').querySelectorAll<HTMLInputElement|HTMLTextAreaElement|HTMLSelectElement|HTMLButtonElement>('input,textarea,select,button'))node.disabled=busy;button('curated-close').disabled=busy;button('curated-discard').disabled=busy}
async function saveCuratedEditor(){
 if(!curatedDraft||curatedSaving)return;
 const draft=curatedDraft,epoch=shellEpoch,request=++curatedRequest;curatedSaving=true;setCuratedBusy(true);
 const payload={...draft,layer:input('curated-layer').value,scope_key:input('curated-scope').value,title:input('curated-title').value,content:input('curated-content').value,applicability:input('curated-applicability').value,evidence:input('curated-evidence').value,credential_ref:input('curated-credential').value,tags:input('curated-tags').value.split(/[,，]/).map(t=>t.trim()).filter(Boolean),status:input('curated-status').value,confirm_verified:input('curated-confirm').checked};
 try{await api<CuratedKnowledge>('library/curated'+(draft.id?'/'+encodeURIComponent(draft.id):''),draft.id?'PUT':'POST',payload);if(!shellCurrent(epoch)||request!==curatedRequest)return;curatedDirty=false;curatedDraft=null;element<HTMLDialogElement>('curated-dialog').close();notify('正式知识已保存；同一来源在同一层级与范围内不会重复提升。');await searchLibrary();void loadLibraryOverview()}
 catch(e){if(shellCurrent(epoch)&&request===curatedRequest)element('curated-state').textContent='保存失败，草稿已保留：'+(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===curatedRequest){curatedSaving=false;setCuratedBusy(false)}}
}
async function deleteCuratedEditor(){
 if(!curatedDraft?.id||curatedSaving)return;
 const draft=curatedDraft,epoch=shellEpoch,request=++curatedRequest;curatedSaving=true;setCuratedBusy(true);
 try{await api('library/curated/'+encodeURIComponent(draft.id!),'DELETE',{revision:draft.revision});if(!shellCurrent(epoch)||request!==curatedRequest)return;curatedDraft=null;curatedDirty=false;element<HTMLDialogElement>('curated-dialog').close();notify('正式条目已移除，任务原始资料继续保留。');await searchLibrary()}
 catch(e){if(shellCurrent(epoch)&&request===curatedRequest)element('curated-state').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===curatedRequest){curatedSaving=false;setCuratedBusy(false)}}
}
async function loadWorkspaceMemory(sync=false){
 if(!chosen||creatingTask||!detail||(sync&&workspaceMemorySaving))return;
 const task=chosen,selected=selection,epoch=shellEpoch,request=++workspaceMemoryRequest;
 if(workspaceDocumentTask!==task){workspaceDocumentRequest++;workspaceDocumentTask=task;workspaceDocumentPath='';element('workspace-memory-documents').textContent='';element('workspace-memory-reader').classList.add('hidden');element('workspace-memory-content').textContent=''}
 if(sync)workspaceMemorySaving=true;button('workspace-memory-sync').disabled=workspaceMemorySaving;element('workspace-memory-state').textContent=sync?'正在回填工作区资料…':'正在读取工作区资料…';element('workspace-memory-tasks').textContent='';
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===workspaceMemoryRequest;
 try{const report=await api<WorkspaceMemoryReport>('tasks/'+encodeURIComponent(task)+'/workspace-memory',sync?'POST':'GET',sync?{}:undefined,shellController.signal);if(!current())return;
  element('workspace-memory-state').textContent=(report.enabled?'目录：'+report.task_directory:'工作区记录已关闭')+(sync?`；写入 ${report.exported}，未变化 ${report.unchanged}`:'')+[report.error,...(report.conflicts||[]),...(report.warnings||[])].filter(Boolean).map(s=>'；'+s).join('');
  element('workspace-memory-tasks').innerHTML=(report.tasks||[]).map(t=>'<p><strong>'+escapeHTML(t.title)+'</strong> · <code>'+escapeHTML(t.path)+'</code></p>').join('');
  element('workspace-memory-documents').innerHTML=(report.documents||[]).map(d=>'<button type="button" data-workspace-document="'+escapeHTML(d.path)+'">'+escapeHTML(d.title||d.kind||d.path)+'</button>').join('');
 }catch(e){if(current())element('workspace-memory-state').textContent='工作区资料读取失败：'+(e as Error).message}
 finally{if(shellCurrent(epoch)){if(sync)workspaceMemorySaving=false;button('workspace-memory-sync').disabled=workspaceMemorySaving;if(sync&&!current()&&toolsTab==='note')void loadWorkspaceMemory()}}
}

async function readWorkspaceMemoryDocument(path:string){
 if(!chosen||creatingTask||!path)return;
 const task=chosen,selected=selection,epoch=shellEpoch,request=++workspaceDocumentRequest;
 element('workspace-memory-reader').classList.remove('hidden');element('workspace-memory-title').textContent=path;element('workspace-memory-content').textContent='';element('workspace-memory-document-state').textContent='正在读取任务记录…';
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===workspaceDocumentRequest;
 try{const result=await api<{path:string;content:string}>('tasks/'+encodeURIComponent(task)+'/workspace-memory/document?'+new URLSearchParams({path}),'GET',undefined,shellController.signal);if(!current())return;workspaceDocumentPath=result.path;element('workspace-memory-title').textContent=result.path;element('workspace-memory-content').innerHTML=markdown(result.content||'');element('workspace-memory-document-state').textContent='工作区文件内容；可在外部编辑器中继续编辑。'}
 catch(e){if(current())element('workspace-memory-document-state').textContent='记录读取失败：'+(e as Error).message}
}

async function openKnowledgeLayer(layer:string){
 if(!['consensus','system','project'].includes(layer))return;
 input('library-layer').value=layer;input('library-scope').value='';input('library-kind').value='';input('library-source').value='';input('library-tag').value='';
 element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const on=b.dataset.libraryLayer===layer;b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))});
 await openLibrary();
}
function setKnowledgeExtractionSettingsBusy(busy:boolean){input('knowledge-extraction-enabled').disabled=busy;button('knowledge-extraction-save').disabled=busy}
async function loadKnowledgeExtractionSettings(){
 if(knowledgeExtractionSettingsBusy||knowledgeExtractionSettingsDirty)return;
 const epoch=shellEpoch,request=++knowledgeExtractionRequest;knowledgeExtractionSettingsReady=false;setKnowledgeExtractionSettingsBusy(true);
 try{const config=await api<KnowledgeExtractionConfig>('library/extraction','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==knowledgeExtractionRequest)return;input('knowledge-extraction-enabled').checked=config.enabled;knowledgeExtractionSettingsReady=true;element('knowledge-extraction-settings-state').textContent=config.needs_directory?'先设置 Obsidian 根目录并开启同步，自动提取才会写入文件。':config.enabled?'自动提取已开启；候选保持待验证。':'自动提取已关闭，已有知识保留。'}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)element('knowledge-extraction-settings-state').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)setKnowledgeExtractionSettingsBusy(!knowledgeExtractionSettingsReady)}
}
async function saveKnowledgeExtractionSettings(){
 if(!knowledgeExtractionSettingsReady||knowledgeExtractionSettingsBusy)return;
 const epoch=shellEpoch,request=++knowledgeExtractionRequest;knowledgeExtractionSettingsBusy=true;setKnowledgeExtractionSettingsBusy(true);
 try{await api('library/extraction','PUT',{enabled:input('knowledge-extraction-enabled').checked});if(!shellCurrent(epoch)||request!==knowledgeExtractionRequest)return;knowledgeExtractionSettingsDirty=false;element('knowledge-extraction-settings-state').textContent='自动提取设置已保存。'}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)element('knowledge-extraction-settings-state').textContent='保存失败：'+(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===knowledgeExtractionRequest){knowledgeExtractionSettingsBusy=false;setKnowledgeExtractionSettingsBusy(false)}}
}
async function ensureExtractionDirectory(){
 const epoch=shellEpoch,config=await api<KnowledgeExtractionConfig>('library/extraction','GET',undefined,shellController.signal);
 if(!shellCurrent(epoch))return false;
 if(!config.needs_directory)return true;
 await openLibrarySettings();if(!shellCurrent(epoch))return false;element('vault-status').textContent='请先选择 Obsidian 根目录，开启 Markdown 同步并保存，再返回提取知识。';input('vault-directory').focus();return false;
}
async function extractRecordedKnowledge(){
 if(knowledgeExtractionBusy)return;
 const epoch=shellEpoch,taskScope=input('library-scope').value,layer=input('library-layer').value;
 knowledgeExtractionBusy=true;button('library-extract').disabled=true;button('library-ai-extract').disabled=true;element('library-extraction-state').textContent='正在检查知识目录…';
 try{if(!await ensureExtractionDirectory())return;if(!shellCurrent(epoch))return;element('library-extraction-state').textContent='正在从已记录资料中提取，不额外调用模型…';const report=await api<KnowledgeExtractionReport>('library/extract','POST',{task_id:taskScope,layers:['consensus','system','project'].includes(layer)?[layer]:['consensus','system','project']});if(!shellCurrent(epoch))return;element('library-extraction-state').textContent=`提取完成：新增 ${report.created}，已存在 ${report.duplicates}，跳过 ${report.skipped}。${report.created?'新条目保留标签、来源及待验证状态。':'没有新增候选；可选择任务后用 AI 重新整理历史。'}`+(report.warnings?.length?' '+report.warnings.join('；'):'');await searchLibrary();void loadLibraryOverview()}
 catch(e){if(shellCurrent(epoch)){element('library-extraction-state').textContent='提取失败：'+(e as Error).message;if((e as Error&{status?:number}).status===409)await openLibrarySettings()}}
 finally{if(shellCurrent(epoch)){knowledgeExtractionBusy=false;button('library-extract').disabled=false;button('library-ai-extract').disabled=false}}
}

type KnowledgeGraphNode={id:string;title:string;kind:'document'|'tag';layer:string;x:number;y:number;tags:string[]};
type KnowledgeGraphData={nodes:KnowledgeGraphNode[];edges:{source:string;target:string}[];total:number;shown:number;tags:number;width:number;height:number};
let knowledgeGraphOpen=false,knowledgeGraphRequest=0,knowledgeGraphDocuments:LibraryDocument[]=[],knowledgeGraphZoom=1,knowledgeGraphBounds={width:1000,height:320};
function buildKnowledgeGraph(documents:LibraryDocument[],compact=false):KnowledgeGraphData{
 const distinct=[...new Map(documents.map(d=>[d.id,d])).values()],visible=distinct.slice(0,80);
 const counts=new Map<string,number>();for(const d of visible)for(const tag of new Set((d.tags||[]).map(t=>t.trim()).filter(Boolean)))counts.set(tag,(counts.get(tag)||0)+1);
 const tags=[...counts].sort((a,b)=>b[1]-a[1]||a[0].localeCompare(b[0])).slice(0,20).map(([tag])=>tag),nodes:KnowledgeGraphNode[]=[],edges:{source:string;target:string}[]=[];
 const width=compact?600:1000,height=compact?620:320,cx=width/2,cy=height/2;
 tags.forEach((tag,index)=>{const angle=index/Math.max(1,tags.length)*Math.PI*2-Math.PI/2;nodes.push({id:'tag:'+tag,title:'#'+tag,kind:'tag',layer:'tag',x:cx+Math.cos(angle)*(compact?140:205),y:cy+Math.sin(angle)*(compact?140:70),tags:[tag]})});
 visible.forEach((d,index)=>{const angle=index/Math.max(1,visible.length)*Math.PI*2-Math.PI/2,radius=compact?(index%2?230:260):(index%2?360:420);nodes.push({id:d.id,title:d.title,kind:'document',layer:d.layer||'',x:cx+Math.cos(angle)*radius,y:cy+Math.sin(angle)*(compact?245:115),tags:d.tags||[]});for(const tag of new Set(d.tags||[]))if(tags.includes(tag))edges.push({source:d.id,target:'tag:'+tag})});
 return {nodes,edges,total:distinct.length,shown:visible.length,tags:tags.length,width,height};
}
function installKnowledgeGraph(){
 button('library-settings-open').insertAdjacentHTML('beforebegin','<button type="button" id="library-graph-open" aria-pressed="false" aria-controls="knowledge-graph-panel">关系图谱</button>');
 element('library-workspace').querySelector('.library-list-pane')!.insertAdjacentHTML('afterbegin','<section id="knowledge-graph-panel" class="knowledge-graph-panel hidden" aria-label="标签关系图谱"><div class="knowledge-graph-toolbar"><button type="button" id="knowledge-graph-refresh">刷新图谱</button><button type="button" id="knowledge-graph-minus" aria-label="缩小关系图谱">−</button><button type="button" id="knowledge-graph-reset">复位</button><button type="button" id="knowledge-graph-plus" aria-label="放大关系图谱">＋</button></div><p id="knowledge-graph-state" role="status"></p><div id="knowledge-graph-viewport" class="knowledge-graph-viewport"><svg id="knowledge-graph" role="group" aria-label="知识文档与标签关系" viewBox="0 0 1000 690"></svg></div><div id="knowledge-graph-accessible" class="knowledge-graph-accessible" aria-label="图谱文档与标签"></div></section>');
 button('library-graph-open').onclick=()=>{showLibraryPage('browse');if(!['consensus','system','project','all'].includes(input('library-layer').value)){input('library-layer').value='all';element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const selected=b.dataset.libraryLayer==='all';b.classList.toggle('selected',selected);b.setAttribute('aria-pressed',String(selected))})}knowledgeGraphOpen=true;knowledgeGraphZoom=1;applyKnowledgeGraphMode();void loadKnowledgeGraph()};
 button('library-browse-open').addEventListener('click',()=>{knowledgeGraphOpen=false;knowledgeGraphRequest++;applyKnowledgeGraphMode()});
 button('library-settings-open').addEventListener('click',()=>{knowledgeGraphRequest++});
 button('knowledge-graph-refresh').onclick=()=>void loadKnowledgeGraph();
 for(const [id,factor] of [['knowledge-graph-minus',.8],['knowledge-graph-plus',1.25]] as const)button(id).onclick=()=>{knowledgeGraphZoom=Math.max(.6,Math.min(2.8,knowledgeGraphZoom*factor));applyKnowledgeGraphZoom()};
 button('knowledge-graph-reset').onclick=()=>{knowledgeGraphZoom=1;applyKnowledgeGraphZoom()};
 const activate=(target:HTMLElement)=>{const node=target.closest<HTMLElement>('[data-graph-document],[data-graph-tag]');if(!node)return;if(node.dataset.graphTag){input('library-tag').value=node.dataset.graphTag;void searchLibrary()}else if(node.dataset.graphDocument){const d=knowledgeGraphDocuments.find(d=>d.id===node.dataset.graphDocument);if(!d)return;if(!libraryHits.some(item=>item.id===d.id))libraryHits.push(d);void previewLibrary(d.id)}};
 element('knowledge-graph').onclick=e=>activate(e.target as HTMLElement);element('knowledge-graph-accessible').onclick=e=>activate(e.target as HTMLElement);
 element('knowledge-graph').onkeydown=e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();activate(e.target as HTMLElement)}};
 element('library-dialog').addEventListener('close',()=>{knowledgeGraphRequest++;knowledgeGraphOpen=false;applyKnowledgeGraphMode()});
 listenWithShell(window,'resize',()=>{if(knowledgeGraphOpen)renderKnowledgeGraph()});
}
function applyKnowledgeGraphMode(){
 element('library-dialog').classList.toggle('graph-open',knowledgeGraphOpen);
 element('knowledge-graph-panel').classList.toggle('hidden',!knowledgeGraphOpen);element('library-results').classList.toggle('hidden',knowledgeGraphOpen);button('library-more').classList.toggle('hidden',knowledgeGraphOpen||!libraryHasMore);
 button('library-graph-open').classList.toggle('selected',knowledgeGraphOpen);button('library-graph-open').setAttribute('aria-pressed',String(knowledgeGraphOpen));
 button('library-browse-open').classList.toggle('selected',!knowledgeGraphOpen);button('library-browse-open').setAttribute('aria-pressed',String(!knowledgeGraphOpen));
}
async function extractTaskKnowledgeWithAI(){
 if(knowledgeExtractionBusy)return;
 const task=input('library-scope').value||(!creatingTask?chosen:'');
 if(!task){element('library-extraction-state').textContent='请先在任务范围选择一个任务，再整理该任务的历史。';input('library-scope').focus();return}
 const epoch=shellEpoch;knowledgeExtractionBusy=true;button('library-extract').disabled=true;button('library-ai-extract').disabled=true;
 try{if(!await ensureExtractionDirectory())return;if(!shellCurrent(epoch))return;
  const title=tasks.find(t=>t.id===task)?.title||task;
  const confirmed=confirm(`AI 整理并提取知识\n\n将使用“${title}”当前引擎和模型，发送最近最多 8 轮成功对话的有界文本进行整理。会消耗模型额度，输出平台共识、系统知识和项目知识的带标签候选，均保持待验证；不会继续调试或执行硬件操作。\n\n开始整理？`);
  if(!confirmed||!shellCurrent(epoch))return;
  const result=await api<{run:Run}>('tasks/'+encodeURIComponent(task)+'/knowledge/extract','POST',{confirm:true});if(!shellCurrent(epoch))return;element('library-extraction-state').textContent='AI 整理任务已排队，可在该任务执行记录查看进度；完成后刷新知识库。';notify('AI 知识整理已排队：'+title);void result;
 }catch(e){if(shellCurrent(epoch))element('library-extraction-state').textContent='AI 整理未启动：'+(e as Error).message}
 finally{if(shellCurrent(epoch)){knowledgeExtractionBusy=false;button('library-extract').disabled=false;button('library-ai-extract').disabled=false}}
}
function applyKnowledgeGraphZoom(){const width=knowledgeGraphBounds.width/knowledgeGraphZoom,height=knowledgeGraphBounds.height/knowledgeGraphZoom;element('knowledge-graph').setAttribute('viewBox',`${knowledgeGraphBounds.width/2-width/2} ${knowledgeGraphBounds.height/2-height/2} ${width} ${height}`)}
function renderKnowledgeGraph(){
 const data=buildKnowledgeGraph(knowledgeGraphDocuments,window.innerWidth>0&&window.innerWidth<=760),byID=new Map(data.nodes.map(n=>[n.id,n]));knowledgeGraphBounds={width:data.width,height:data.height};
 const edges=data.edges.map(edge=>{const a=byID.get(edge.source)!,b=byID.get(edge.target)!;return `<line x1="${a.x}" y1="${a.y}" x2="${b.x}" y2="${b.y}"></line>`}).join('');
 const nodes=data.nodes.map(n=>`<g role="button" tabindex="0" class="knowledge-graph-node ${n.kind}" data-layer="${escapeHTML(n.layer)}" ${n.kind==='tag'?'data-graph-tag="'+escapeHTML(n.tags[0])+'"':'data-graph-document="'+escapeHTML(n.id)+'"'} aria-label="${escapeHTML(n.title)}" transform="translate(${n.x},${n.y})"><title>${escapeHTML(n.title)}</title><circle r="${n.kind==='tag'?9:6}"></circle><text y="${n.kind==='tag'?-18:24}" text-anchor="middle">${escapeHTML(n.title.length>12?n.title.slice(0,12)+'…':n.title)}</text></g>`).join('');
 element('knowledge-graph').innerHTML=`<g class="knowledge-graph-edges">${edges}</g>${nodes}`;
 element('knowledge-graph-state').textContent=data.shown?`显示 ${data.shown} / ${data.total} 份知识、${data.tags} 个标签。连线表示文档包含该标签；点击文档阅读，点击标签缩小范围。${data.total>80?'图谱最多显示最近 80 份，请使用搜索、层级或标签筛选。':''}`:'当前范围没有正式知识；可先一键提取或新建知识，并添加标签。';
 element('knowledge-graph-accessible').innerHTML=data.nodes.map(n=>'<button type="button" '+(n.kind==='tag'?'data-graph-tag="'+escapeHTML(n.tags[0])+'"':'data-graph-document="'+escapeHTML(n.id)+'"')+'>'+escapeHTML(n.title)+'</button>').join('');
 applyKnowledgeGraphZoom();
}
async function loadKnowledgeGraph(){
 if(!knowledgeGraphOpen)return;
 const epoch=shellEpoch,request=++knowledgeGraphRequest,dialog=element<HTMLDialogElement>('library-dialog');
 element('knowledge-graph-state').textContent='正在读取三类知识的标签关系…';element('knowledge-graph').innerHTML='';element('knowledge-graph-accessible').textContent='';
 try{const docs=await api<CuratedKnowledge[]>('library/curated','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==knowledgeGraphRequest||!knowledgeGraphOpen||!dialog.open)return;
  const layer=input('library-layer').value,tag=input('library-tag').value.trim().replace(/^#/,''),query=input('library-query').value.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean),scope=input('library-scope').value;
  knowledgeGraphDocuments=docs.filter(d=>(!['consensus','system','project'].includes(layer)||d.layer===layer)&&(!tag||(d.tags||[]).some(t=>t.toLocaleLowerCase()===tag.toLocaleLowerCase()))&&(!scope||d.source_task_id===scope)&&(input('library-stale').checked||d.status!=='stale')&&query.every(q=>(d.title+' '+d.content+' '+d.scope_key+' '+(d.tags||[]).join(' ')).toLocaleLowerCase().includes(q))).map(d=>({...d,id:'curated:'+d.id,kind:'curated',task_id:d.source_task_id||'',task_title:tasks.find(t=>t.id===d.source_task_id)?.title||'',hash:'',snippet:d.content.slice(0,300),origin:'local'}));
  renderKnowledgeGraph();
 }catch(e){if(shellCurrent(epoch)&&request===knowledgeGraphRequest)element('knowledge-graph-state').textContent='图谱读取失败：'+(e as Error).message}
}
