type KnowledgeRevision={key:string;content:string;evidence:string;applicability:string;tags:string[];source_id:string;source_task_id:string;created:number;supersedes?:string[];base_changed?:boolean};
type LibraryDocument={scope_key?:string;source_id?:string;source_hash?:string;source_task_id?:string;applicability?:string;evidence?:string;credential_ref?:string;tags?:string[];pending_revisions?:KnowledgeRevision[];layer?:string;generated?:boolean;content?:string;id:string;kind:string;source?:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type LibraryReference={document?:LibraryDocument;reference:string;preview?:string;truncated:boolean};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean;include_automatic:boolean;task_folders?:boolean;consensus_directory?:string;system_directory?:string;project_directory?:string;root_mode?:'direct'|'legacy';include_legacy?:boolean;human_layout?:boolean};
type KnowledgeExtractionConfig={enabled:boolean;needs_directory?:boolean;scheduled?:boolean;interval_hours?:number;last_organized?:number;last_error?:string};
type KnowledgeExtractionReport={created:number;revisions?:number;duplicates:number;skipped:number;items?:CuratedKnowledge[];warnings?:string[]};
type AutomaticKnowledgeConfig={capture:boolean;recall:boolean;organize?:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
type VaultSettingsResponse={config:VaultConfig;report:VaultReport;document_directory:string};
type KnowledgeMigrationPreview={source_directory:string;target_directory:string;fingerprint:string;files:{path:string;size:number;action:'copy'|'identical'|'conflict'}[];file_count:number;total_bytes:number;copy_count:number;identical_count:number;conflicts:string[];warnings:string[];source_dirty:boolean;ready:boolean;completed?:boolean;copied?:number;config?:VaultConfig};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
let libraryLoading=false,libraryBrowseStarted=false,libraryNextOffset=0,libraryHasMore=false,libraryPreviewRequest=0,libraryOverviewRequest=0,libraryCitationRequest=0,libraryCitationBusy=false;
let libraryPreviewID='',libraryPreviewText='',libraryPreviewLimit=20000;
let automaticSettingsRequest=0,automaticSettingsReady=false,automaticSettingsSaving=false;
let vaultSettingsRequest=0,vaultSettingsReady=false,vaultSettingsSaving=false,vaultRefreshing=false;
let automaticSettingsDirty=false,vaultSettingsDirty=false,knowledgeSettingsRequest=0;
let knowledgeSettingsLoading:Promise<void>|null=null;
let knowledgeExtractionRequest=0,knowledgeExtractionBusy=false,knowledgeExtractionSettingsReady=false,knowledgeExtractionSettingsDirty=false,knowledgeExtractionSettingsBusy=false;
let knowledgeExtractionActionRequest=0;
let knowledgeMigrationRequest=0,knowledgeMigrationBusy=false,knowledgeMigrationPreview:KnowledgeMigrationPreview|null=null;
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
 const headerActions=document.createElement('div');headerActions.className='library-header-actions';headerActions.innerHTML='<button type="button" id="library-manage-task">打开任务记录</button>';
 button('library-close').before(headerActions);headerActions.append(button('library-close'));
 headerActions.insertAdjacentHTML('afterbegin','<button type="button" id="library-refresh-files">刷新文件</button>');
 button('library-refresh-files').onclick=async()=>{const epoch=shellEpoch,b=button('library-refresh-files');if(b.disabled||knowledgeMigrationBusy)return;b.disabled=true;try{const report=await api<VaultReport>('library/vault/refresh','POST',{});if(!shellCurrent(epoch))return;showVaultReport(report);if(element<HTMLDialogElement>('library-dialog').open){await searchLibrary();void loadLibraryOverview()}}catch(e){if(shellCurrent(epoch)){showKnowledgeSyncFailure((e as Error).message);notify((e as Error).message)}}finally{if(shellCurrent(epoch))b.disabled=false}};
 button('library-manage-task').onclick=()=>{if(!libraryTargetCurrent()||creatingTask||!chosen||!detail)return;element<HTMLDialogElement>('library-dialog').close();switchTab('note')};
 element('notebook').querySelector('.note-head .actions')!.insertAdjacentHTML('beforeend','<button type="button" id="knowledge-library-open">查全局知识库</button>');
 element('notebook').querySelector('h2')!.textContent='本任务知识';
 element('notebook').querySelector('.note-head')!.insertAdjacentHTML('afterend','<p class="muted knowledge-panel-help">这里保留本任务的原始摘录和手工笔记。摘要、前提与共识、对话目录可以直接打开文档查看，无需手动分类。</p>');
 button('summarize').textContent='手动整理';button('summarize').title='调用本任务的 AI 整理知识，完成后可检查并保存';
 button('knowledge-library-open').textContent='查找其他任务资料';button('knowledge-library-open').onclick=()=>void openLibrary(chosen);
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
 element('library-preview-content').insertAdjacentHTML('beforebegin','<nav id="library-outline" class="document-outline hidden" aria-label="知识文档章节"></nav>');
 element('library-preview-content').onclick=e=>{const revision=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-knowledge-revision]');if(revision){void resolveKnowledgeRevision(revision.dataset.knowledgeRevision||'',revision.dataset.accept==='true');return}const link=(e.target as HTMLElement).closest<HTMLAnchorElement>('a');if(!link)return;const href=link.getAttribute('href')||'';if(!href||/^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(href))return;e.preventDefault();void followLibraryLink(href)};
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
 element('library-outline').textContent='';element('library-outline').classList.add('hidden');
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
 element('library-dialog').insertAdjacentHTML('beforeend',`<section id="settings-knowledge" class="library-settings-panel hidden" aria-label="知识库设置"><h3>知识库设置</h3><p>任务记录保存在工作区，长期可复用的知识保存在你的笔记目录。两层资料各有一份可阅读、可编辑的 Markdown，不把每次提问都变成知识笔记。</p><details class="knowledge-storage-info"><summary>运行数据与迁移（高级）</summary><code id="knowledge-data-directory" class="data-dir-path"></code><p>任务、对话和知识共用此目录中的数据库。下面设置的笔记目录用于保存可阅读的 Markdown，不会迁移运行数据；切换完整运行数据请到“数据与存储”。</p></details></section>`);
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
 header.insertAdjacentHTML('afterend','<nav class="library-pages" aria-label="知识库页面"><button type="button" id="library-browse-open" class="selected" aria-pressed="true" aria-controls="library-browse-panel">阅读知识</button><button type="button" id="library-settings-open" aria-pressed="false" aria-controls="settings-knowledge">目录与整理</button></nav>');
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
 renderKnowledgeOrganizationScope();
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
 if(taskScope){scope.value=taskScope;input('library-layer').value='tasks';element<HTMLDetailsElement>('library-history').open=true;element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const on=b.dataset.libraryLayer==='tasks';b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))})}
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
 renderKnowledgeLayerHelp();
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
function libraryReadingGuide(d:LibraryDocument){return d.kind==='index'&&(d.generated||d.id.startsWith('curated-index:'))}
function libraryDescription(d:LibraryDocument){const layer=libraryReadingGuide(d)?d.id.match(/^curated-index:(consensus|system|project)(?::|$)/)?.[1]||d.layer:d.layer;const category=({consensus:'平台共识',system:'系统知识',project:'项目知识',tasks:'任务资料',knowledge:'候选结论',topics:'主题索引',history:'对话记录',notes:'任务笔记',files:'Markdown 文档'} as Record<string,string>)[layer||'']||(d.kind==='run'?'对话记录':'任务笔记');return category+(libraryReadingGuide(d)?' · 阅读手册':'')}
function libraryDocumentState(d:LibraryDocument){return libraryReadingGuide(d)?'以原笔记状态为准':knowledgeStateText(d.status)}
function librarySnippet(text:string){return text.replace(/^---\s*\n[\s\S]*?\n---\s*(?:\n|$)/,'').replace(/^\s{0,3}#{1,6}\s+/gm,'').replace(/\[([^\]]+)\]\([^)]+\)/g,'$1').replace(/[`*_]/g,'').replace(/\s+/g,' ').trim().slice(0,240)}
function renderLibraryHits(){
 element('library-results').innerHTML=libraryHits.length?libraryHits.map(d=>`<article class="library-card"${d.id===libraryPreviewID?' data-selected="true"':''}><h3><button type="button" data-library-preview="${escapeHTML(d.id)}"${d.path?' title="'+escapeHTML(d.path)+'"':''}>${escapeHTML(d.title)}</button></h3><p class="library-card-meta">${escapeHTML(libraryDescription(d))} · ${escapeHTML(d.scope_key||d.task_title||'共用知识')} · ${escapeHTML(libraryDocumentState(d))}</p>${d.snippet?'<p class="library-snippet">'+escapeHTML(librarySnippet(d.snippet))+'</p>':''}<p class="library-tags">${(d.tags||[]).map(t=>'#'+escapeHTML(t)).join(' · ')}</p>${d.pending_revisions?.length?'<button type="button" class="knowledge-revision-link" data-library-preview="'+escapeHTML(d.id)+'">查看待合并修订（'+d.pending_revisions.length+'）</button>':''}<footer><span>${new Date(d.updated).toLocaleDateString()}</span><button type="button" data-library-reference="${escapeHTML(d.id)}"${libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create)?' disabled':''}>引用</button></footer></article>`).join(''):'<div class="library-empty"><strong>暂时没有匹配的文档</strong><p>当前只查找所选知识分类。可切换平台共识、系统知识、项目知识；旧任务过程可在“旧资料（兼容）”中查看，当前任务主文档从“打开任务记录”进入。</p></div>';
}
async function previewLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit)return;
 const token=++libraryPreviewRequest,epoch=shellEpoch;libraryPreviewID=id;
 libraryPreviewDocument=null;button('library-promote').classList.add('hidden');button('library-curated-edit').classList.add('hidden');
 libraryPreviewText='';button('library-download').classList.add('hidden');button('library-read-more').classList.add('hidden');
 element('library-workspace').classList.add('preview-open');element('library-preview-title').textContent=hit.title;
 element('library-preview-meta').textContent=libraryDescription(hit)+' · '+(hit.scope_key||hit.task_title||'共用知识')+' · '+libraryDocumentState(hit);
 element('library-outline').textContent='';element('library-outline').classList.add('hidden');
 element('library-preview-content').textContent='';element('library-preview-status').textContent='正在读取预览…';button('library-preview-cite').classList.add('hidden');button('library-preview-source').classList.add('hidden');highlightLibraryPreview();
 try{const result=await api<LibraryDocument>('library/document?'+new URLSearchParams({id,hash:hit.hash}),'GET',undefined,shellController.signal);if(token!==libraryPreviewRequest||!shellCurrent(epoch))return;
  libraryPreviewText=result.content||'';libraryPreviewLimit=20000;libraryPreviewDocument=result;renderLibraryDocument();
  button('library-promote').classList.toggle('hidden',result.kind==='curated'||libraryReadingGuide(result));button('library-curated-edit').classList.toggle('hidden',result.kind!=='curated');
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
 const state=element('library-sync-state');if(state){const problems=[r.error,...(r.conflicts||[]),...(r.warnings||[])].filter(Boolean);state.textContent=problems.length?'笔记文件同步需要处理：'+problems.join('；')+'。请到“目录与整理”查看详情；知识整理成功不代表文件已同步。':r.updated?`笔记文件已同步：写入 ${r.exported}，导入 ${r.imported}，索引 ${r.indexed}。`:'';state.classList.toggle('hidden',!state.textContent);state.classList.toggle('knowledge-organize-error',!!problems.length)}
}
function showKnowledgeSyncFailure(message:string){const state=element('library-sync-state');if(state){state.textContent='笔记文件同步失败：'+message+'。已整理的候选保留；请修复后刷新文件。';state.classList.remove('hidden');state.classList.add('knowledge-organize-error')}}
function markKnowledgeSyncPending(){const state=element('library-sync-state');if(state){state.textContent='知识变更已保存；请点击“刷新文件”核对 Markdown 同步结果。候选入库和文件同步是两个步骤。';state.classList.remove('hidden','knowledge-organize-error')}}
function updateKnowledgeOrganizationButtons(){
 for(const id of ['library-extract','library-ai-extract'])if(button(id))button(id).disabled=knowledgeExtractionBusy||knowledgeRevisionBusy;
 if(button('library-topic-consolidate'))button('library-topic-consolidate').disabled=knowledgeExtractionBusy||knowledgeRevisionBusy;
 element('library-preview-content')?.querySelectorAll<HTMLButtonElement>('[data-knowledge-revision]').forEach(b=>b.disabled=knowledgeExtractionBusy||knowledgeRevisionBusy||b.dataset.accept==='true'&&!!libraryPreviewDocument?.pending_revisions?.find(r=>r.key===b.dataset.knowledgeRevision)?.base_changed);
}
function setVaultControlsDisabled(disabled:boolean){for(const id of ['vault-directory','vault-root-mode','vault-consensus-directory','vault-system-directory','vault-project-directory','vault-enabled','vault-legacy','vault-runs','vault-automatic','vault-save','vault-refresh'])if(element(id))input(id).disabled=disabled}
function showVaultLocation(directory:string,enabled:boolean){element('vault-document-directory').textContent=directory?(directory+(enabled?'':'（同步未启用）')):'尚未设置有效的知识文件根目录'}
async function loadVault(){
 if(vaultSettingsSaving||vaultRefreshing||vaultSettingsDirty||knowledgeMigrationBusy)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsReady=false;setVaultControlsDisabled(true);element('vault-status').textContent='正在读取知识库设置…';
 try{const v=await api<VaultSettingsResponse>('library/vault','GET',undefined,shellController.signal);if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;const human=v.config.human_layout||!v.config.directory;input('vault-directory').value=v.config.directory||'';input('vault-root-mode').value=v.config.root_mode||(v.config.directory?'legacy':'direct');input('vault-consensus-directory').value=v.config.consensus_directory||(human?'平台共识':'consensus');input('vault-system-directory').value=v.config.system_directory||(human?'系统知识':'system');input('vault-project-directory').value=v.config.project_directory||(human?'项目知识':'projects');input('vault-enabled').checked=v.config.enabled;input('vault-legacy').checked=v.config.include_legacy??!!v.config.directory;input('vault-runs').checked=v.config.include_runs;input('vault-automatic').checked=!!v.config.include_automatic;showVaultLocation(v.document_directory,v.config.enabled);showVaultReport(v.report);vaultSettingsReady=true}
 catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch))setVaultControlsDisabled(!vaultSettingsReady)}
}
async function saveVault(){
 if(!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing||knowledgeMigrationBusy)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsSaving=true;setVaultControlsDisabled(true);
 try{const saved=await api<VaultConfig&{document_directory:string}>('library/vault','PUT',{directory:input('vault-directory').value.trim(),root_mode:input('vault-root-mode').value==='legacy'?'legacy':'direct',consensus_directory:input('vault-consensus-directory').value.trim(),system_directory:input('vault-system-directory').value.trim(),project_directory:input('vault-project-directory').value.trim(),enabled:input('vault-enabled').checked,include_legacy:input('vault-legacy').checked,include_runs:input('vault-runs').checked,include_automatic:input('vault-automatic').checked,task_folders:true,human_layout:true});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;vaultSettingsDirty=false;showVaultLocation(saved.document_directory,saved.enabled);element('vault-status').textContent='设置已保存，正在同步…';await syncVaultView(epoch,token)}
 catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch)){vaultSettingsSaving=false;setVaultControlsDisabled(false)}}
}
async function syncVaultView(epoch:number,token:number){
 const report=await api<VaultReport>('library/vault/refresh','POST',{});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;showVaultReport(report);
 if(element<HTMLDialogElement>('library-dialog').open)await searchLibrary();if(chosen)await loadKnowledge();
}
async function refreshVault(){
 if(!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing||knowledgeMigrationBusy)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultRefreshing=true;setVaultControlsDisabled(true);element('vault-status').textContent='正在同步 Markdown 与索引…';
 try{await syncVaultView(epoch,token)}catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch)){vaultRefreshing=false;setVaultControlsDisabled(false)}}
}

function renderLibraryDocument(){
 const cut=libraryPreviewText.length>libraryPreviewLimit;
 const d=libraryPreviewDocument;
 const location=d?.path||libraryHits.find(hit=>hit.id===libraryPreviewID)?.path;
 const fields=d?.kind==='curated'?[['适用条件',d.applicability],['实际依据',d.evidence],['安全配置引用',d.credential_ref],['原始来源',d.source_id],['文件位置',location]]:[['文件位置',location]];
 const metadata=fields.some(([,value])=>value)?'<details class="curated-metadata"><summary>'+(d?.kind==='curated'?'适用条件、依据与来源':'文件位置与来源')+'</summary>'+fields.filter(([,value])=>value).map(([title,value])=>'<p><strong>'+title+'</strong><br>'+escapeHTML(value!)+'</p>').join('')+'</details>':'';
 const revisions=d?.kind==='curated'&&d.pending_revisions?.length?'<details class="knowledge-pending-revisions" open><summary>待处理修订（'+d.pending_revisions.length+'）</summary><p>先对照上方原文与下方候选。采用会替换正文并退回待验证；忽略保留原文。多条线索可先让 AI 整理成完整合并稿，再检查采用。</p><button type="button" id="library-topic-consolidate"'+(knowledgeRevisionBusy||knowledgeExtractionBusy?' disabled':'')+'>用 AI 整理此主题</button>'+d.pending_revisions.map(r=>'<article><h4>'+(r.supersedes?.length?'完整合并稿 · 覆盖 '+r.supersedes.length+' 条修订':'修订候选')+' · '+escapeHTML(new Date(r.created).toLocaleString())+'</h4>'+(r.base_changed?'<p class="knowledge-organize-error">原正文已在此候选生成后变化，请重新整理；不能直接采用旧基线的候选。</p>':'')+'<div class="content">'+markdown(r.content)+'</div><p><strong>依据</strong> '+escapeHTML(r.evidence||'未提供，需核查')+'</p><p><strong>适用条件</strong> '+escapeHTML(r.applicability||'未说明')+'</p><p class="library-tags">'+(r.tags||[]).map(t=>'#'+escapeHTML(t)).join(' · ')+'</p><p class="muted">来源：'+escapeHTML(r.source_id||r.source_task_id||'未提供')+'</p><div class="actions"><button type="button" data-knowledge-revision="'+escapeHTML(r.key)+'" data-accept="true"'+(knowledgeRevisionBusy||r.base_changed?' disabled':'')+'>采用修订（退回待验证）</button><button type="button" data-knowledge-revision="'+escapeHTML(r.key)+'" data-accept="false"'+(knowledgeRevisionBusy?' disabled':'')+'>忽略修订</button></div></article>').join('')+'<p id="library-revision-state" role="status"></p></details>':'';
 const guide=d&&libraryReadingGuide(d)?'<p class="library-guide-notice muted">这是一份自动汇集的只读阅读手册；要修改具体内容，请打开文中的原笔记。每条结论的验证状态、依据与适用条件以原笔记为准。</p>':'';
 const visible=libraryPreviewText.slice(0,libraryPreviewLimit);
 // Close only the displayed portion of a cut code fence. Otherwise the shared
 // renderer would parse code lines as prose and add fake outline headings.
 const previewBody=cut&&/```[^\n]*\n/.test(visible.replace(/```[^\n]*\n[\s\S]*?```/g,''))?visible+'\n```':visible;
 element('library-preview-content').innerHTML=guide+metadata+'<div id="library-article">'+markdown(previewBody)+'</div>'+revisions;
 if(button('library-topic-consolidate'))button('library-topic-consolidate').onclick=()=>void consolidateKnowledgeTopic();
 updateKnowledgeOrganizationButtons();
 renderDocumentOutline('library-article','library-outline','library-section',cut);
 button('library-read-more').classList.toggle('hidden',!cut);button('library-download').classList.toggle('hidden',!libraryPreviewText);
 element('library-preview-status').textContent=cut?'长文档分段显示，可继续阅读或下载全文。':'已读取全文。引用到输入框时最多带入 6000 字符，并保留来源。';
}
function renderDocumentOutline(contentID:string,outlineID:string,prefix:string,partial=false){
 const content=element(contentID),outline=element(outlineID);if(!content||!outline)return;
 const allHeadings=Array.from(content.querySelectorAll<HTMLElement>('h1,h2,h3,h4,h5,h6')).filter(h=>h.textContent?.trim());
 const slugs=new Map<string,number>();allHeadings.forEach((h,index)=>{h.id=prefix+'-'+index;h.tabIndex=-1;const base=markdownHeadingSlug(h.dataset.markdownHeading??h.textContent??''),count=slugs.get(base)||0;h.dataset.documentSlug=base+(count?'-'+count:'');slugs.set(base,count+1)});
 // Deep source headings still consume slug counters and accept links, while
 // the compact navigation lists the main sections of a reading collection.
 const headings=allHeadings.filter(h=>Number(h.tagName.slice(1))<=3);
 outline.classList.toggle('hidden',headings.length<2);if(headings.length<2){outline.textContent='';return}
 const visible=headings.length>200?[...headings.slice(0,100),...headings.slice(-100)]:headings;
 outline.innerHTML='<label for="'+prefix+'-select">文档目录</label><select id="'+prefix+'-select" aria-label="跳转到文档章节"><option value="">'+(partial?'已载入部分 · ':'')+headings.length+' 个章节</option><option value="start">↑ 回到文首</option>'+visible.map(h=>'<option value="'+h.id+'">'+'　'.repeat(Math.max(0,Number(h.tagName.slice(1))-1))+escapeHTML(h.textContent!.trim().slice(0,110))+'</option>').join('')+'<option value="end">↓ '+(partial?'当前已载入末尾':'跳到文末')+'</option></select>';
 outline.querySelector('select')!.onchange=e=>{const select=e.target as HTMLSelectElement,value=select.value;const target=value==='start'?content.firstElementChild:value==='end'?content.lastElementChild:headings.find(h=>h.id===value);if(target){target.scrollIntoView?.({block:'start'});if(target instanceof window.HTMLElement)(target as HTMLElement).focus?.({preventScroll:true})}select.value=''};
}
function markdownHeadingSlug(text:string){return text.toLowerCase().trim().replace(/[^\p{L}\p{N}\s_-]/gu,'').replace(/\s+/g,'-')}
function jumpMarkdownFragment(contentID:string,fragment:string){
 let target:string;try{target=decodeURIComponent(fragment.replace(/^#/,''))}catch{notify('章节链接编码无效');return}
 if(!target||/%[0-9a-f]{2}/i.test(target)){notify('章节链接无效');return}
 const heading=Array.from(element(contentID)?.querySelectorAll<HTMLElement>('h1,h2,h3,h4,h5,h6')||[]).find(h=>h.id===target||h.dataset.documentSlug===target||h.dataset.documentSlug===markdownHeadingSlug(target));
 if(!heading){notify('当前已载入正文中没有这个章节；长文档可先继续阅读全文。');return}heading.scrollIntoView?.({block:'start'});heading.focus?.({preventScroll:true});
}
function relativeMarkdownLink(currentPath:string,href:string){
 let relative:string;try{relative=decodeURIComponent(href.split('#')[0].split('?')[0])}catch{throw Error('文档链接编码无效')}
 // Decode exactly once. A second encoded traversal must never be forwarded to
 // a filesystem resolver, even when another layer might decode it later.
 if(!relative||/%[0-9a-f]{2}/i.test(relative)||/^(?:[a-z][a-z0-9+.-]*:|[\\/])/i.test(relative)||/[\x00-\x1f:]/.test(relative))throw Error('文档链接不是有效的相对路径');
 relative=relative.replace(/\\/g,'/');const parts=currentPath.replace(/\\/g,'/').split('/').slice(0,-1);
 for(const part of relative.split('/')){if(!part||part==='.')continue;if(part==='..'){if(!parts.length)throw Error('链接超出知识目录');parts.pop()}else parts.push(part)}
 const path=parts.join('/');if(!path.toLowerCase().endsWith('.md'))throw Error('只支持打开 Markdown 文档');return path;
}
async function resolveKnowledgeRevision(key:string,accept:boolean){
 const doc=libraryPreviewDocument,revision=doc?.pending_revisions?.find(r=>r.key===key);if(knowledgeRevisionBusy||knowledgeExtractionBusy||!doc||doc.kind!=='curated'||!revision||accept&&revision.base_changed)return;
 if(curatedDirty||curatedSaving||element<HTMLDialogElement>('curated-dialog').open){notify('请先保存、放弃或关闭正在编辑的知识草稿，再处理修订。');return}
 const epoch=shellEpoch,request=++knowledgeRevisionRequest,preview=libraryPreviewRequest,id=doc.id;let applied=false;
 const current=()=>shellCurrent(epoch)&&request===knowledgeRevisionRequest&&preview===libraryPreviewRequest&&libraryPreviewID===id;
 knowledgeRevisionBusy=true;renderLibraryDocument();element('library-revision-state').textContent=accept?'正在采用修订…':'正在忽略修订…';
 try{await api<CuratedKnowledge>('library/curated/'+encodeURIComponent(id.replace(/^curated:/,''))+'/revisions/'+encodeURIComponent(key),'POST',{revision:doc.revision,accept});applied=true;if(shellCurrent(epoch))markKnowledgeSyncPending();if(!current()){if(shellCurrent(epoch))notify(accept?'原文档修订已采用，并退回待验证。':'原文档修订已忽略。');return}const updated=await api<LibraryDocument>('library/document?'+new URLSearchParams({id}),'GET',undefined,shellController.signal);if(!current())return;libraryPreviewDocument=updated;libraryPreviewText=updated.content||'';libraryHits=libraryHits.map(hit=>hit.id===id?updated:hit);knowledgeRevisionBusy=false;renderLibraryDocument();element('library-preview-status').textContent=accept?'修订已采用，知识已退回待验证；请核查后确认。':'修订已忽略，原正文保持不变。'}
 catch(e){if(current()){const state=element('library-revision-state');if(state)state.textContent=(applied?'修订已处理，但重新读取失败；请刷新文件后查看：':'处理失败，原预览已保留：')+(e as Error).message}}
 finally{if(shellCurrent(epoch)&&request===knowledgeRevisionRequest){knowledgeRevisionBusy=false;updateKnowledgeOrganizationButtons()}}
}
async function followLibraryLink(href:string){
 if(href.startsWith('#')){jumpMarkdownFragment('library-article',href);return}
 const current=libraryHits.find(d=>d.id===libraryPreviewID);if(!current?.path)return;
 let relative:string;try{relative=relativeMarkdownLink(current.path,href)}catch(e){notify((e as Error).message);return}
 const request=++libraryPreviewRequest,epoch=shellEpoch;
 try{const doc=await api<LibraryDocument>('library/document?'+new URLSearchParams({id:'document:'+relative}));if(request!==libraryPreviewRequest||!shellCurrent(epoch))return;libraryHits=libraryHits.filter(d=>d.id!==doc.id);libraryHits.push(doc);await previewLibrary(doc.id);if(shellCurrent(epoch)&&libraryPreviewID===doc.id&&href.includes('#'))jumpMarkdownFragment('library-article',href.slice(href.indexOf('#')))}catch(e){if(request===libraryPreviewRequest&&shellCurrent(epoch))element('library-preview-status').textContent='无法打开来源文档：'+(e as Error).message}
}

type CuratedKnowledge={id:string;layer:string;scope_key:string;title:string;content:string;status:string;source_id:string;source_hash:string;source_task_id:string;applicability:string;evidence:string;credential_ref:string;tags:string[];pending_revisions?:KnowledgeRevision[];revision:number;created:number;updated:number};
type WorkspaceMemoryReport={enabled:boolean;directory:string;task_directory:string;main_document?:string;sync_pending?:boolean;exported:number;unchanged:number;tasks:{id:string;title:string;path:string;updated:number;excerpt:string}[];documents?:{path:string;title:string;kind:string}[];conflicts:string[];warnings:string[];error?:string;error_code?:string};
type WorkspaceDocument={path:string;content:string;revision?:string;editable?:boolean};
type WorkspaceDocumentDraft=WorkspaceDocument&{task:string;dirty:boolean;editing:boolean;saving:boolean;status:string};
type KnowledgeProject={id:string;name:string;aliases:string[]};
type TaskKnowledgeProjectSelection={project:KnowledgeProject|null;projects:KnowledgeProject[]};
type TaskKnowledgeProjectDraft=TaskKnowledgeProjectSelection&{selected:string;name:string;aliases:string;dirty:boolean;ready:boolean;loading:boolean;saving:boolean;status:string;request:number};
const taskKnowledgeProjectDrafts=new Map<string,TaskKnowledgeProjectDraft>();
let taskKnowledgeProjectRequest=0;
const workspaceDocumentDrafts=new Map<string,WorkspaceDocumentDraft>(),workspaceInitialSync=new Set<string>();
function workspaceDraftKey(task:string,path:string){return task+'\n'+path}
function workspaceDraftsPending(){return Array.from(workspaceDocumentDrafts.values()).some(d=>d.dirty||d.saving)||Array.from(taskKnowledgeProjectDrafts.values()).some(d=>d.dirty||d.saving)}
let libraryPreviewDocument:LibraryDocument|null=null,curatedDraft:Partial<CuratedKnowledge>|null=null,curatedDirty=false,curatedSaving=false,curatedRequest=0,workspaceMemoryRequest=0,workspaceMemorySaving=false,knowledgeRevisionBusy=false,knowledgeRevisionRequest=0;
function installKnowledgeLayers(){
 const nav=element('library-dialog').querySelector('.library-layers')!;
 const layerButton=([id,title]:string[])=>`<button type="button" data-library-layer="${id}" aria-pressed="${id==='consensus'}" class="${id==='consensus'?'selected':''}">${title}</button>`;
 nav.innerHTML=[['consensus','平台共识'],['system','系统知识'],['project','项目知识']].map(layerButton).join('')+'<details id="library-history" class="library-history"><summary>旧资料（兼容）</summary><div>'+[['tasks','任务资料'],['knowledge','候选结论'],['all','全部资料']].map(layerButton).join('')+'</div></details>';
 input('library-layer').value='consensus';
 nav.insertAdjacentHTML('afterend','<p id="library-layer-help" class="library-layer-help"></p>');
 nav.querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>b.onclick=()=>{input('library-layer').value=b.dataset.libraryLayer!;input('library-kind').value='';input('library-source').value='';input('library-scope').value='';nav.querySelectorAll<HTMLButtonElement>('button').forEach(x=>{x.classList.toggle('selected',x===b);x.setAttribute('aria-pressed',String(x===b))});void searchLibrary()});
 renderKnowledgeLayerHelp();
 element('library-preview').querySelector('.library-preview-actions')!.insertAdjacentHTML('beforeend','<button type="button" id="library-promote" class="hidden">整理为正式知识</button><button type="button" id="library-curated-edit" class="hidden">编辑正式知识</button>');
 element('library-search-form').insertAdjacentHTML('afterend','<div class="actions" id="library-curated-actions"><button type="button" id="library-curated-new">新建知识</button><button type="button" id="library-extract">一键整理已有候选</button><button type="button" id="library-ai-extract">用 AI 整理本任务</button><span class="muted">一键整理复用已保存的分层候选，不调用模型。AI 整理需确认并消耗额度。</span></div><p id="library-extraction-state" class="muted" role="status"></p>');
 element('library-curated-actions').insertAdjacentHTML('afterend','<p id="library-organization-scope" class="muted knowledge-organization-scope"></p><p id="library-sync-state" class="knowledge-sync-state hidden" role="status"></p>');
 button('library-extract').onclick=()=>void extractRecordedKnowledge();button('library-ai-extract').onclick=()=>void extractTaskKnowledgeWithAI();
 button('library-curated-new').onclick=()=>openCuratedEditor();button('library-promote').onclick=()=>openCuratedEditor(libraryPreviewDocument||undefined);button('library-curated-edit').onclick=()=>openCuratedEditor(libraryPreviewDocument||undefined);
 element('library-dialog').querySelector('.library-header p')!.textContent='在这里查找和编辑平台共识、系统知识、项目知识。每个任务的连续记录从左侧任务菜单打开。';
 element('library-dialog').querySelector('.library-help p')!.textContent='连续对话沿用原生会话；开启自动恢复后，新会话补回本任务摘要、同工作区其他任务的交接和已确认的平台共识。只补回有限片段；系统及项目知识先搜索、引用。引用计入模型输入。知识不授予工具或设备权限。';
 element('settings-knowledge').querySelector('p')!.textContent='第一层是工作区里的任务记录，维护目标、计划、进展、决策与证据，供切换 AI 和同工作区任务接续。第二层是笔记目录里的平台共识、系统知识、项目知识，提取可复用内容供你阅读学习和 AI 查找。';
 element('settings-knowledge').querySelector('p')!.insertAdjacentHTML('afterend','<details id="knowledge-start-guide" class="knowledge-start-guide"><summary>第一次使用与验收顺序</summary><ol><li>在第 1 组选择笔记根目录，开启同步并保存；三类知识会写入此处，任务原件继续保存在工作区。</li><li>在第 2 组开启自动记录、随回复整理和自动读取，再单独保存；新进展进入同一个任务 Markdown，已有记录可在任务菜单中回填。</li><li>在第 3 组保存每轮自动归档或定时归档；二者只处理已保存候选，不另起模型。</li><li>打开“阅读知识”，选分类和任务范围。一键整理处理已有候选；“用 AI 整理本任务”确认后分批重读历史。</li><li>打开知识核对正文、依据和适用条件。同主题修订可采用、忽略或先用 AI 生成合并稿；只有实际核查后才标记已验证。</li><li>点击“刷新文件”，确认同步无错误，再去 Obsidian 核对。外部编辑后再刷新，Duo 应显示新内容并退回待验证。</li></ol><p>切换引擎使用输入框旁选择器并确认接续范围；自动补入有界任务摘录，不保证任意模型无损接替。跨电脑笔记同步由 Obsidian 或 Git 完成，工作区与完整运行数据需分别同步。</p></details>');
 element('vault-settings').querySelector('summary')!.textContent='1. 笔记根目录与知识分类';
 element('vault-settings').querySelector('p')!.textContent='选择运行 Duo 的这台电脑上的 Obsidian 笔记目录。新设置默认直接使用所选目录；已有配置保留兼容路径。开启同步后每分钟检查 Markdown，也可立即同步。';
 input('vault-directory').previousSibling!.textContent='笔记根目录（Duo 服务所在电脑）';
 const guide=element('settings-knowledge').querySelector('.knowledge-directory-guide')!;
 guide.innerHTML='<strong>两层资料，一条积累路径</strong><div class="knowledge-flow"><section><h4>任务记录 · 工作区</h4><code>&lt;workspace&gt;/.duo/tasks/任务名-短ID/任务记录.md</code><p>持续维护当前状态与时间进展；原始对话保留来源。每个任务从左侧任务菜单打开，同工作区可以互相参考。</p></section><span aria-hidden="true">→</span><section><h4>可复用知识 · 笔记目录</h4><p>平台共识 / 系统知识 / 项目知识</p><p>按主题成文，保留依据、适用条件、标签与任务来源。在知识库阅读、编辑或查看关系图谱。</p></section></div><p>只把用户明确约定的跨任务习惯放进平台共识；通用原理归系统知识，具体项目结论归项目知识。候选保持待验证，确认状态不等同于自动完成实际测试。</p>';
 input('vault-directory').placeholder='选择你的 Obsidian 笔记根目录';
 const locationHint=element('vault-document-directory');locationHint.parentElement!.replaceChildren(document.createTextNode('选择根目录并开启同步后，知识文件将写入三类目录。实际知识根目录：'),locationHint);
 input('vault-directory').closest('label')!.insertAdjacentHTML('afterend','<label>根目录方式<select id="vault-root-mode"><option value="direct">所选目录就是知识根（新配置推荐）</option><option value="legacy">兼容旧配置：所选目录下的 Duo/</option></select></label><div class="knowledge-layer-directories"><label>平台共识相对目录<input id="vault-consensus-directory" placeholder="平台共识" maxlength="200"></label><label>系统知识相对目录<input id="vault-system-directory" placeholder="系统知识" maxlength="200"></label><label>项目知识相对目录<input id="vault-project-directory" placeholder="项目知识" maxlength="200"></label><p class="muted">三个目录相对于实际知识根，可用中文名称或多级相对路径，不可相互包含。更换根目录方式或路径不会搬走、删除原件。保存后采用按主题命名的可读布局；有外部修改的文件保留原路径，避免覆盖。</p></div>');
 for(const id of ['vault-root-mode','vault-consensus-directory','vault-system-directory','vault-project-directory']){input(id).disabled=!vaultSettingsReady;input(id).oninput=input(id).onchange=()=>{vaultSettingsDirty=true;element('vault-status').textContent='路径与同步设置尚未保存。'}}
 input('vault-enabled').closest('label')!.lastChild!.textContent='启用三类知识的 Markdown 同步与索引';
 const legacy=document.createElement('details');legacy.id='vault-legacy-settings';legacy.className='knowledge-legacy-settings';legacy.innerHTML='<summary>旧资料镜像（兼容）</summary><label><input type="checkbox" id="vault-legacy">继续往笔记库导出旧资料镜像</label><p>默认只导出三类正式知识。关闭旧资料镜像后，不再向笔记库追加聊天摘录、旧知识片段和主题索引；已导出文件不删除，原始来源仍在 Duo 与工作区保留。</p>';
 input('vault-enabled').closest('label')!.after(legacy);
 for(const id of ['vault-runs','vault-automatic'])legacy.append(input(id).closest('label')!);
 const legacyPrivacy=Array.from(element('vault-settings').querySelectorAll('p')).find(p=>p.textContent?.startsWith('同步文件包含问题、回复和结论'));if(legacyPrivacy)legacy.append(legacyPrivacy);
 input('vault-automatic').closest('label')!.lastChild!.textContent='镜像旧版自动资料、片段与主题索引';
 input('vault-legacy').disabled=!vaultSettingsReady;input('vault-legacy').onchange=()=>{vaultSettingsDirty=true;element('vault-status').textContent='旧资料镜像设置尚未保存。'};
 const storageNote=guide.nextElementSibling;if(storageNote)storageNote.textContent='更换目录会同步三类知识，旧目录保留；独立笔记需自行复制。知识正文可直接在 Obsidian 编辑，同步冲突会保留两端内容。';
 element('automatic-knowledge-settings').querySelector('summary')!.textContent='2. 任务记录与每轮候选';
 element('automatic-knowledge-settings').querySelector('p')!.textContent='自动记录维护工作区任务文档。自动整理让正常回复给出任务状态变化和带标签的分层候选，不另起模型；没有新进展时不编造记录，没有可复用内容时不创建知识。';
 input('automatic-capture').closest('label')!.lastChild!.textContent='自动记录完成的对话并更新工作区任务资料';
 input('automatic-organize').closest('label')!.lastChild!.textContent='随正常回复整理任务状态与可复用知识候选（增加本轮输出）';
 input('automatic-recall').closest('label')!.lastChild!.textContent='自动读取任务文档变化；新会话补回摘要、同工作区交接和已确认的平台共识';
 element('automatic-knowledge-settings').querySelectorAll('p')[1].textContent='同一原生会话只补入任务 Markdown 的变更范围，未变内容不重复发送；失败不确认读取进度。跨引擎新会话恢复有界摘要。平台共识须明确确认；系统与项目知识按问题与项目范围检索。';
 element('notebook').querySelector('.knowledge-panel-help')!.textContent='这里是本任务的初级资料与候选结论。过程自动写入工作区；跨任务复用的内容可到知识库整理为平台共识、系统知识或项目知识。';
 const notebook=element('notebook'),head=notebook.querySelector('.note-head')!;
 head.querySelector('h2')!.textContent='任务记录';button('note-tab').textContent='任务记录';
 const history=document.createElement('details');history.id='task-record-history';history.className='task-record-history';history.innerHTML='<summary>逐轮来源与历史资料</summary>';
 notebook.append(history);
 for(const node of Array.from(notebook.children))if(node!==head&&node!==history)history.append(node);
 history.querySelector('summary')!.after(element('note-status'),head.querySelector('.actions')!);
 head.querySelector('div')!.insertAdjacentHTML('beforeend','<p>一个任务，一份 Markdown；按时间连续记录，可直接阅读和编辑。</p>');
 head.insertAdjacentHTML('afterend','<section class="workspace-memory-panel"><p id="workspace-memory-state" role="status">正在读取任务记录…</p><button type="button" id="workspace-memory-sync">回填 / 同步本任务已有记录</button><section id="workspace-memory-reader" class="workspace-memory-reader hidden"><h3 id="workspace-memory-title"></h3><div class="workspace-memory-actions"><button type="button" id="workspace-memory-read">阅读</button><button type="button" id="workspace-memory-edit">编辑 Markdown</button><button type="button" id="workspace-memory-save" class="primary">保存</button><button type="button" id="workspace-memory-discard">放弃修改 / 读取最新</button><button type="button" id="workspace-memory-reload">重新读取</button></div><p id="workspace-memory-document-state" role="status"></p><textarea id="workspace-memory-editor" aria-label="任务记录 Markdown 正文" class="hidden" spellcheck="false"></textarea><div id="workspace-memory-content" class="content"></div></section></section>');
 element('workspace-memory-state').insertAdjacentHTML('beforebegin','<details id="task-knowledge-project" class="workspace-document-location"><summary>知识项目范围</summary><p>为本任务指定稳定的项目名称，切换引擎、重命名或更换目录后仍使用同一项目的知识。系统知识和已确认的平台共识仍可跨项目复用；明确提到其他项目时也可检索。</p><label>知识项目<select id="task-knowledge-project-select" disabled><option value="">自动推断（未指定项目）</option></select></label><div id="task-knowledge-project-new" class="hidden"><label>新项目名称<input id="task-knowledge-project-name" maxlength="80" placeholder="例如 Duo、375 平台" disabled></label><label>项目别名<input id="task-knowledge-project-aliases" placeholder="用逗号分隔，最多 8 个" disabled></label></div><p id="task-knowledge-project-state" role="status">展开后读取本任务的项目设置。</p><div class="actions"><button type="button" id="task-knowledge-project-save" disabled>保存项目范围</button><button type="button" id="task-knowledge-project-reload" disabled>放弃草稿 / 读取最新</button></div></details>');
 element<HTMLDetailsElement>('task-knowledge-project').ontoggle=()=>{if(element<HTMLDetailsElement>('task-knowledge-project').open)void loadTaskKnowledgeProject()};
 input('task-knowledge-project-select').onchange=()=>{const draft=activeTaskKnowledgeProjectDraft();if(!draft||!draft.ready||draft.loading||draft.saving||!taskKnowledgeProjectWritable())return;draft.selected=input('task-knowledge-project-select').value;draft.dirty=true;draft.status='项目范围尚未保存；切换任务后草稿仍保留。';renderTaskKnowledgeProject(draft)};
 for(const id of ['task-knowledge-project-name','task-knowledge-project-aliases'])input(id).oninput=()=>{const draft=activeTaskKnowledgeProjectDraft();if(!draft||!draft.ready||draft.loading||draft.saving||!taskKnowledgeProjectWritable())return;draft.name=input('task-knowledge-project-name').value;draft.aliases=input('task-knowledge-project-aliases').value;draft.dirty=true;draft.status='项目范围尚未保存；切换任务后草稿仍保留。';renderTaskKnowledgeProject(draft)};
 button('task-knowledge-project-save').onclick=()=>void saveTaskKnowledgeProject();
 button('task-knowledge-project-reload').onclick=()=>{const draft=activeTaskKnowledgeProjectDraft();if(draft&&!draft.saving&&!draft.loading){draft.dirty=false;draft.ready=false;void loadTaskKnowledgeProject()}};
 history.querySelector('summary')!.insertAdjacentHTML('afterend','<div id="workspace-memory-documents" class="workspace-memory-documents" aria-label="本任务历史记录文件"></div>');
 element('workspace-memory-reader').insertAdjacentHTML('beforebegin','<details id="workspace-memory-related" class="workspace-related-tasks hidden"><summary>同工作区任务</summary><div id="workspace-memory-tasks"></div></details>');
 element('workspace-memory-title').insertAdjacentHTML('afterend','<details class="workspace-document-location"><summary>文件位置</summary><code id="workspace-memory-path"></code></details>');
 element('workspace-memory-content').insertAdjacentHTML('beforebegin','<nav id="workspace-memory-outline" class="document-outline hidden" aria-label="任务记录章节"></nav>');
 button('workspace-memory-sync').onclick=()=>void loadWorkspaceMemory(true);
 button('workspace-memory-read').onclick=()=>setWorkspaceDocumentEditing(false);
 button('workspace-memory-edit').onclick=()=>setWorkspaceDocumentEditing(true);
 button('workspace-memory-save').onclick=()=>void saveWorkspaceMemoryDocument();
 button('workspace-memory-discard').onclick=()=>void discardWorkspaceMemoryDocument();
 button('workspace-memory-reload').onclick=()=>void readWorkspaceMemoryDocument(workspaceDocumentPath,true);
 input('workspace-memory-editor').oninput=()=>{const draft=activeWorkspaceDraft();if(!draft||draft.saving)return;draft.content=input('workspace-memory-editor').value;draft.dirty=true;draft.status='修改尚未保存；切换任务后草稿仍保留。';renderWorkspaceDocumentControls(draft)};
 element('workspace-memory-documents').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-workspace-document]');if(b)void readWorkspaceMemoryDocument(b.dataset.workspaceDocument||'')};
 element('workspace-memory-tasks').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-workspace-task]'),id=b?.dataset.workspaceTask;if(!id||!tasks.some(t=>t.id===id&&!t.deleted))return;const draft=activeWorkspaceDraft();if(draft?.dirty||draft?.saving)notify('当前任务的编辑草稿已保留，返回时可继续修改。');void openTaskRecords(id)};
 element('workspace-memory-content').onclick=e=>{const link=(e.target as HTMLElement).closest<HTMLAnchorElement>('a');if(!link||!workspaceDocumentPath)return;const href=link.getAttribute('href')||'';if(!href||/^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(href))return;e.preventDefault();void followWorkspaceDocumentLink(href)};
 element('automatic-knowledge-settings').insertAdjacentHTML('afterend','<details id="knowledge-extraction-settings" class="knowledge-extraction-settings" open><summary>3. 将候选整理进笔记</summary><label><input id="knowledge-extraction-enabled" type="checkbox" disabled>每轮结束后自动归入三类知识</label><label><input id="knowledge-extraction-scheduled" type="checkbox" disabled>定时整理已有候选</label><label class="knowledge-schedule-interval">整理间隔（小时，1～168）<input id="knowledge-extraction-interval" type="number" min="1" max="168" step="1" value="24" disabled></label><p>自动、定时及“一键整理已有候选”都复用已保存的分层结果，不额外调用模型。没有候选时保留任务记录；要重新理解旧对话，请在阅读知识页选择“用 AI 整理本任务”，确认后才调用模型。整理成功与 Markdown 同步成功分别显示；候选默认待验证。</p><p id="knowledge-extraction-last" class="muted"></p><p id="knowledge-extraction-error" class="knowledge-organize-error hidden" role="status"></p><button type="button" id="knowledge-extraction-save" disabled>保存整理方式</button><p id="knowledge-extraction-settings-state" role="status"></p></details>');
 for(const id of ['knowledge-extraction-enabled','knowledge-extraction-scheduled','knowledge-extraction-interval'])input(id).oninput=input(id).onchange=()=>{knowledgeExtractionSettingsDirty=true;element('knowledge-extraction-settings-state').textContent='整理方式尚未保存。'};
 button('knowledge-extraction-save').onclick=()=>void saveKnowledgeExtractionSettings();
 installKnowledgeMigration();
 installKnowledgeGraph();
 element('root').insertAdjacentHTML('beforeend',`<dialog id="curated-dialog" class="curated-dialog" aria-labelledby="curated-heading"><div class="library-header"><h2 id="curated-heading">整理正式知识</h2><button type="button" id="curated-close">关闭</button></div><p>凭据保留在设备或受保护配置，正文只写配置引用。整理和标记状态不会启动模型或独立验证内容。</p><form id="curated-form"><label>知识层级<select id="curated-layer"><option value="consensus">平台共识</option><option value="system">系统知识</option><option value="project">项目知识</option></select></label><label>主题 / 项目名称<input id="curated-scope" maxlength="80" placeholder="平台共识无需填写"></label><label>标题<input id="curated-title" required maxlength="200"></label><label>正文<textarea id="curated-content" required rows="10"></textarea></label><label>适用条件<textarea id="curated-applicability" rows="2"></textarea></label><label>实际依据 / 验证结果<textarea id="curated-evidence" rows="2" placeholder="未验证时明确写出"></textarea></label><label>安全配置引用<input id="curated-credential" placeholder="仅设备或配置名称，不填写密码"></label><label>标签<input id="curated-tags" placeholder="用逗号分隔"></label><label>状态<select id="curated-status"><option value="observed">待验证</option><option value="verified">已验证（用户确认）</option><option value="stale">已过时</option></select></label><label><input type="checkbox" id="curated-confirm">我确认已填写实际依据和适用条件，并明确将此条标为已验证</label><p id="curated-source" class="muted"></p><p id="curated-state" role="status"></p><div class="actions"><button type="submit" id="curated-save" class="primary">保存正式知识</button><button type="button" id="curated-delete" class="danger hidden">移除正式条目</button></div></form></dialog>`);
 button('curated-close').onclick=()=>{if(!curatedSaving)element<HTMLDialogElement>('curated-dialog').close()};
 button('curated-close').insertAdjacentHTML('beforebegin','<button type="button" id="curated-discard">放弃草稿</button>');
 button('curated-discard').onclick=()=>{if(curatedSaving)return;curatedDirty=false;curatedDraft=null;element<HTMLDialogElement>('curated-dialog').close()};
 element('curated-form').oninput=()=>{curatedDirty=true;element('curated-state').textContent='草稿尚未保存。'};
 element('curated-form').onsubmit=e=>{e.preventDefault();void saveCuratedEditor()};
 button('curated-delete').onclick=()=>void deleteCuratedEditor();
 element('curated-dialog').addEventListener('cancel',e=>{if(curatedSaving)e.preventDefault()});
 disposeWithShell(()=>{curatedRequest++;workspaceMemoryRequest++;workspaceDocumentRequest++;knowledgeExtractionRequest++;knowledgeExtractionActionRequest++;knowledgeRevisionRequest++;knowledgeGraphRequest++;curatedDraft=null;curatedDirty=false;curatedSaving=false;knowledgeRevisionBusy=false;workspaceMemorySaving=false;workspaceDocumentDrafts.clear();workspaceInitialSync.clear();workspaceDocumentTask='';workspaceDocumentPath='';knowledgeExtractionBusy=false;knowledgeExtractionSettingsBusy=false;knowledgeExtractionSettingsReady=false;knowledgeExtractionSettingsDirty=false;knowledgeGraphOpen=false;knowledgeGraphDocuments=[];libraryPreviewDocument=null});
 disposeWithShell(()=>{taskKnowledgeProjectRequest++;taskKnowledgeProjectDrafts.clear();knowledgeMigrationRequest++;knowledgeMigrationBusy=false;knowledgeMigrationPreview=null});
}
function renderKnowledgeLayerHelp(){
 const help=element('library-layer-help');if(!help)return;
 help.textContent=({consensus:'平台共识：你明确约定的跨任务习惯与协作流程。待验证候选需确认后才作为共识使用。',system:'系统知识：可跨项目复用的原理、排查方法和学习总结，按主题持续修订。',project:'项目知识：具体项目的资料、约束、决策与验证结论，按项目归档。',tasks:'旧任务资料仅供回溯；当前任务主文档请从左侧任务菜单的“任务记录”打开。',knowledge:'旧候选片段仅供整理；可复用内容应归入三类知识，不按提问重复生成笔记。',all:'兼容查看全部已索引文件。任务记录是过程依据，三类知识是可复用结论。'} as Record<string,string>)[input('library-layer').value]||'';
 renderKnowledgeOrganizationScope();
}
function renderKnowledgeOrganizationScope(){
 const help=element('library-organization-scope');if(!help)return;
 const scope=input('library-scope').value,layer=input('library-layer').value,task=scope||(!creatingTask?chosen:'');
 const layerName=({consensus:'平台共识',system:'系统知识',project:'项目知识'} as Record<string,string>)[layer]||'三类知识';
 help.textContent='一键整理范围：'+(scope?'“'+(tasks.find(t=>t.id===scope)?.title||scope)+'”':'所有任务')+'的'+layerName+'候选。AI 历史整理对象：'+(task?'“'+(tasks.find(t=>t.id===task)?.title||task)+'”（三类知识）':'请先选择一个任务')+'。';
}
function openCuratedEditor(doc?:LibraryDocument){
 if(doc&&libraryReadingGuide(doc)){notify('阅读手册自动汇集原笔记；请打开具体笔记后编辑。');return}
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
 try{await api<CuratedKnowledge>('library/curated'+(draft.id?'/'+encodeURIComponent(draft.id):''),draft.id?'PUT':'POST',payload);if(!shellCurrent(epoch)||request!==curatedRequest)return;curatedDirty=false;curatedDraft=null;element<HTMLDialogElement>('curated-dialog').close();markKnowledgeSyncPending();notify('正式知识已保存；同一来源在同一层级与范围内不会重复提升。');await searchLibrary();void loadLibraryOverview()}
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
 if(element<HTMLDetailsElement>('task-knowledge-project').open)void loadTaskKnowledgeProject();else renderTaskKnowledgeProject(activeTaskKnowledgeProjectDraft());
 const task=chosen,selected=selection,epoch=shellEpoch,request=++workspaceMemoryRequest;
 if(workspaceDocumentTask!==task){workspaceDocumentRequest++;workspaceDocumentTask=task;workspaceDocumentPath='';element('workspace-memory-documents').textContent='';element('workspace-memory-reader').classList.add('hidden');element('workspace-memory-content').textContent='';input('workspace-memory-editor').value=''}
 let writing=sync;
 if(sync)workspaceMemorySaving=true;button('workspace-memory-sync').disabled=workspaceMemorySaving;element('workspace-memory-state').textContent=sync?'正在回填工作区资料…':'正在读取任务记录…';element('workspace-memory-tasks').textContent='';
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===workspaceMemoryRequest;
 try{let report=await api<WorkspaceMemoryReport>('tasks/'+encodeURIComponent(task)+'/workspace-memory',sync?'POST':'GET',sync?{}:undefined,shellController.signal);if(!current())return;
  const hasMain=(r:WorkspaceMemoryReport)=>!!r.main_document&&!!r.documents?.some(d=>d.path===r.main_document);
  if(!sync&&!hasMain(report)&&report.enabled&&!report.error&&!workspaceMemorySaving&&!workspaceInitialSync.has(task)){
   workspaceInitialSync.add(task);writing=true;workspaceMemorySaving=true;button('workspace-memory-sync').disabled=true;element('workspace-memory-state').textContent='正在将已有记录整理为本任务 Markdown…';
   report=await api<WorkspaceMemoryReport>('tasks/'+encodeURIComponent(task)+'/workspace-memory','POST',{},shellController.signal);if(!current())return;
  }
  element('workspace-memory-state').textContent=(report.enabled?'任务记录自动更新已开启':'自动记录已关闭；已有文件仍可阅读和编辑')+(writing?`；写入 ${report.exported}，未变化 ${report.unchanged}`:'')+(report.sync_pending?'；早期记录正在分批回填，空闲时会继续；也可再次点击同步':'')+[report.error,...(report.conflicts||[]),...(report.warnings||[])].filter(Boolean).map(s=>'；'+s).join('');
  const related=(report.tasks||[]).filter(t=>t.id!==task);element('workspace-memory-related').classList.toggle('hidden',!related.length);element('workspace-memory-related').querySelector('summary')!.textContent='同工作区任务（'+related.length+'）';
  element('workspace-memory-tasks').innerHTML=related.map(t=>'<article>'+(tasks.some(known=>known.id===t.id&&!known.deleted)?'<button type="button" data-workspace-task="'+escapeHTML(t.id)+'">'+escapeHTML(t.title)+' →</button>':'<strong>'+escapeHTML(t.title)+'</strong>')+(t.excerpt?'<p>'+escapeHTML(librarySnippet(t.excerpt))+'</p>':'')+'</article>').join('');
  element('workspace-memory-documents').innerHTML=(report.documents||[]).filter(d=>d.path!==report.main_document).map(d=>'<button type="button" data-workspace-document="'+escapeHTML(d.path)+'">'+escapeHTML(d.title||d.kind||d.path)+'</button>').join('');
  if(hasMain(report))await readWorkspaceMemoryDocument(report.main_document!);
  else if(!workspaceDocumentPath)element('workspace-memory-state').textContent+='；尚无主任务记录。'+(report.enabled?'可点击“回填 / 同步本任务已有记录”重试。':'请在知识库 → 目录与整理 → 任务记录与每轮候选中开启自动记录，再手动回填。');
 }catch(e){if(current())element('workspace-memory-state').textContent='工作区资料读取失败：'+(e as Error).message}
 finally{if(shellCurrent(epoch)){if(writing)workspaceMemorySaving=false;button('workspace-memory-sync').disabled=workspaceMemorySaving;if(writing&&!current()&&toolsTab==='note')void loadWorkspaceMemory()}}
}

function activeTaskKnowledgeProjectDraft(){return chosen&&!creatingTask?taskKnowledgeProjectDrafts.get(chosen):undefined}
function taskKnowledgeProjectWritable(){const task=detail?.task;return !!task&&task.id===chosen&&!creatingTask&&!task.archived&&!task.deleted&&!['running','queued'].includes(task.status)&&!(detail?.runs||[]).some(run=>['running','queued'].includes(run.status))}
function renderTaskKnowledgeProject(draft?:TaskKnowledgeProjectDraft){
 const select=input('task-knowledge-project-select'),busy=!!draft&&(draft.loading||draft.saving),writable=taskKnowledgeProjectWritable();
 select.innerHTML='<option value="">自动推断（未指定项目）</option>'+(draft?.projects||[]).map(project=>'<option value="'+escapeHTML(project.id)+'">'+escapeHTML(project.name)+'</option>').join('')+'<option value="__new__">新建知识项目…</option>';select.value=draft?.selected||'';
 select.disabled=!draft?.ready||busy||!writable;input('task-knowledge-project-name').value=draft?.name||'';input('task-knowledge-project-aliases').value=draft?.aliases||'';
 element('task-knowledge-project-new').classList.toggle('hidden',draft?.selected!=='__new__');
 for(const id of ['task-knowledge-project-name','task-knowledge-project-aliases'])input(id).disabled=!draft?.ready||busy||!writable;
 button('task-knowledge-project-save').disabled=!draft?.ready||!draft.dirty||busy||!writable;button('task-knowledge-project-reload').disabled=!draft||busy;
 element('task-knowledge-project-state').textContent=(draft?.status||'展开后读取本任务的项目设置。')+(!writable?' 请在任务空闲且未归档时修改。':'');
 element('task-knowledge-project').querySelector('summary')!.textContent='知识项目范围'+(draft?.project?' · '+draft.project.name:'');
}
async function loadTaskKnowledgeProject(){
 if(!chosen||creatingTask||!detail)return;
 const task=chosen,selected=selection,epoch=shellEpoch;
 let draft=taskKnowledgeProjectDrafts.get(task);
 if(!draft){draft={project:null,projects:[],selected:'',name:'',aliases:'',dirty:false,ready:false,loading:false,saving:false,status:'',request:0};taskKnowledgeProjectDrafts.set(task,draft)}
 renderTaskKnowledgeProject(draft);if(draft.dirty||draft.ready||draft.loading||draft.saving)return;
 const request=++taskKnowledgeProjectRequest;draft.request=request;draft.loading=true;draft.status='正在读取项目范围…';renderTaskKnowledgeProject(draft);
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===draft!.request;
 try{const result=await api<TaskKnowledgeProjectSelection>('tasks/'+encodeURIComponent(task)+'/knowledge/project','GET',undefined,shellController.signal);if(!current())return;draft.project=result.project;draft.projects=result.projects;draft.selected=result.project?.id||'';draft.ready=true;draft.status=result.project?'当前使用项目“'+result.project.name+'”；历史文档的项目名称不会自动改写。':'当前按任务名称、工作区和来源推断；可指定项目，减少无关知识。';renderTaskKnowledgeProject(draft)}
 catch(e){if(current()){draft.status='项目范围读取失败：'+(e as Error).message;renderTaskKnowledgeProject(draft)}}
 finally{if(shellCurrent(epoch)&&request===draft.request){draft.loading=false;if(current())renderTaskKnowledgeProject(draft);else if(chosen===task&&element<HTMLDetailsElement>('task-knowledge-project').open)void loadTaskKnowledgeProject()}}
}
async function saveTaskKnowledgeProject(){
 const draft=activeTaskKnowledgeProjectDraft();if(!draft?.ready||!draft.dirty||draft.loading||draft.saving||!taskKnowledgeProjectWritable())return;
 const task=chosen,selected=selection,epoch=shellEpoch,request=++taskKnowledgeProjectRequest;
 const payload=draft.selected==='__new__'?{name:draft.name.trim(),aliases:draft.aliases.split(/[,，\n]/).map(value=>value.trim()).filter(Boolean)}:{project_id:draft.selected};
 if(draft.selected==='__new__'&&!draft.name.trim()){draft.status='请填写项目名称。';renderTaskKnowledgeProject(draft);return}
 draft.request=request;draft.saving=true;draft.status='正在保存项目范围…';renderTaskKnowledgeProject(draft);
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===draft.request;
 try{const result=await api<TaskKnowledgeProjectSelection>('tasks/'+encodeURIComponent(task)+'/knowledge/project','PUT',payload);if(!shellCurrent(epoch)||request!==draft.request)return;draft.project=result.project;draft.projects=result.projects;draft.selected=result.project?.id||'';draft.name='';draft.aliases='';draft.dirty=false;draft.status='项目范围已保存；仅用于后续知识检索与新记录，历史文档保持原样。';if(current())renderTaskKnowledgeProject(draft)}
 catch(e){if(shellCurrent(epoch)&&request===draft.request){draft.status='保存失败，草稿已保留：'+(e as Error).message;if(current())renderTaskKnowledgeProject(draft)}}
 finally{if(shellCurrent(epoch)&&request===draft.request){draft.saving=false;if(current())renderTaskKnowledgeProject(draft);else if(chosen===task&&element<HTMLDetailsElement>('task-knowledge-project').open)void loadTaskKnowledgeProject()}}
}

function activeWorkspaceDraft(){return workspaceDocumentTask===chosen?workspaceDocumentDrafts.get(workspaceDraftKey(chosen,workspaceDocumentPath)):undefined}
function renderWorkspaceDocumentControls(draft:WorkspaceDocumentDraft){
 element('workspace-memory-document-state').textContent=draft.status||(draft.editable?'工作区中的任务 Markdown；保存会直接更新此文件。':'历史来源文件只读；可在外部编辑器中查看。');
 button('workspace-memory-edit').classList.toggle('hidden',!draft.editable);button('workspace-memory-save').classList.toggle('hidden',!draft.editable);button('workspace-memory-discard').classList.toggle('hidden',!draft.editable||!draft.dirty);
 button('workspace-memory-save').disabled=!draft.dirty||draft.saving||!draft.revision;button('workspace-memory-edit').disabled=draft.saving;button('workspace-memory-read').disabled=draft.saving;button('workspace-memory-reload').disabled=draft.saving;button('workspace-memory-discard').disabled=draft.saving;
 button('workspace-memory-read').setAttribute('aria-pressed',String(!draft.editing));button('workspace-memory-edit').setAttribute('aria-pressed',String(draft.editing));
 input('workspace-memory-editor').disabled=draft.saving;
}
function renderWorkspaceDocument(draft:WorkspaceDocumentDraft){
 workspaceDocumentTask=draft.task;workspaceDocumentPath=draft.path;element('workspace-memory-reader').classList.remove('hidden');element('workspace-memory-path').textContent=draft.path;
 input('workspace-memory-editor').value=draft.content;input('workspace-memory-editor').classList.toggle('hidden',!draft.editing);element('workspace-memory-content').classList.toggle('hidden',draft.editing);element('workspace-memory-content').innerHTML=markdown(draft.content);
 element('workspace-memory-title').textContent=element('workspace-memory-content').querySelector('h1')?.textContent||tasks.find(t=>t.id===draft.task)?.title||draft.path.split('/').at(-1)||'任务记录';
 renderDocumentOutline('workspace-memory-content','workspace-memory-outline','workspace-section');if(draft.editing)element('workspace-memory-outline').classList.add('hidden');renderWorkspaceDocumentControls(draft);
}
function setWorkspaceDocumentEditing(editing:boolean){const draft=activeWorkspaceDraft();if(!draft||draft.saving||editing&&!draft.editable)return;draft.editing=editing;renderWorkspaceDocument(draft);if(editing)input('workspace-memory-editor').focus()}
async function followWorkspaceDocumentLink(href:string){
 if(href.startsWith('#')){jumpMarkdownFragment('workspace-memory-content',href);return}
 const epoch=shellEpoch,task=chosen,selected=selection;let path:string;try{path=relativeMarkdownLink(workspaceDocumentPath,href)}catch(e){notify((e as Error).message);return}
 await readWorkspaceMemoryDocument(path);if(shellCurrent(epoch)&&task===chosen&&selected===selection&&workspaceDocumentPath===path&&href.includes('#'))jumpMarkdownFragment('workspace-memory-content',href.slice(href.indexOf('#')));
}
async function readWorkspaceMemoryDocument(path:string,reload=false){
 if(!chosen||creatingTask||!path)return;
 const task=chosen,selected=selection,epoch=shellEpoch,request=++workspaceDocumentRequest;
 const key=workspaceDraftKey(task,path),cached=workspaceDocumentDrafts.get(key);
 if(cached&&(cached.dirty||cached.saving)){if(reload)cached.status='草稿尚未保存；请先保存，或选择“放弃修改 / 读取最新”。';renderWorkspaceDocument(cached);return}
 workspaceDocumentTask=task;workspaceDocumentPath=path;element('workspace-memory-reader').classList.remove('hidden');element('workspace-memory-title').textContent='正在读取任务记录…';element('workspace-memory-path').textContent=path;element('workspace-memory-outline').classList.add('hidden');
 if(cached)renderWorkspaceDocument(cached);else{element('workspace-memory-content').textContent='';input('workspace-memory-editor').value='';input('workspace-memory-editor').classList.add('hidden');element('workspace-memory-content').classList.remove('hidden');for(const id of ['workspace-memory-read','workspace-memory-edit','workspace-memory-save','workspace-memory-discard','workspace-memory-reload'])button(id).disabled=true}
 element('workspace-memory-document-state').textContent='正在读取任务记录…';
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&request===workspaceDocumentRequest;
 try{const result=await api<WorkspaceDocument>('tasks/'+encodeURIComponent(task)+'/workspace-memory/document?'+new URLSearchParams({path}),'GET',undefined,shellController.signal);if(!current())return;if(result.path!==path)throw new Error('返回的文档路径不一致，请重新读取。');
  const latest=workspaceDocumentDrafts.get(key);if(latest&&(latest.dirty||latest.saving)){renderWorkspaceDocument(latest);return}
  const draft:WorkspaceDocumentDraft={...result,task,content:result.content||'',editable:result.editable===true&&!!result.revision,dirty:false,editing:cached?.editing||false,saving:false,status:''};workspaceDocumentDrafts.set(key,draft);renderWorkspaceDocument(draft);
 }
 catch(e){if(current())element('workspace-memory-document-state').textContent='记录读取失败：'+(e as Error).message}
}
async function discardWorkspaceMemoryDocument(){const draft=activeWorkspaceDraft();if(!draft||draft.saving)return;workspaceDocumentDrafts.delete(workspaceDraftKey(draft.task,draft.path));await readWorkspaceMemoryDocument(draft.path,true)}
async function saveWorkspaceMemoryDocument(){
 const draft=activeWorkspaceDraft();if(!draft||!draft.editable||!draft.revision||!draft.dirty||draft.saving)return;
 const task=draft.task,path=draft.path,selected=selection,epoch=shellEpoch,key=workspaceDraftKey(task,path),content=draft.content,revision=draft.revision;
 draft.saving=true;draft.status='正在保存任务记录…';renderWorkspaceDocumentControls(draft);
 const current=()=>shellCurrent(epoch)&&chosen===task&&selection===selected&&workspaceDocumentPath===path;
 try{const result=await api<WorkspaceDocument>('tasks/'+encodeURIComponent(task)+'/workspace-memory/document','PUT',{path,content,revision},shellController.signal);if(!shellCurrent(epoch))return;if(result.path!==path||!result.revision)throw new Error('保存结果缺少文件版本，请重新核对文件。');
  if(workspaceDocumentDrafts.get(key)!==draft)return;draft.content=result.content??content;draft.revision=result.revision;draft.dirty=false;draft.status='已保存到任务 Markdown。';if(current())renderWorkspaceDocument(draft);
 }catch(e){if(!shellCurrent(epoch))return;draft.status=(e as {status?:number}).status===409?'文件已被其他窗口或自动记录更新。你的草稿仍保留；请复制需要的内容，再放弃修改并读取最新版本合并。':'保存失败，草稿已保留：'+(e as Error).message;if(current())renderWorkspaceDocumentControls(draft)}
 finally{if(shellCurrent(epoch)){draft.saving=false;if(current())renderWorkspaceDocumentControls(draft)}}
}

async function openKnowledgeLayer(layer:string){
 if(!['consensus','system','project'].includes(layer))return;
 input('library-layer').value=layer;input('library-scope').value='';input('library-kind').value='';input('library-source').value='';input('library-tag').value='';
 element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const on=b.dataset.libraryLayer===layer;b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))});
 await openLibrary();
}
function setKnowledgeExtractionSettingsBusy(busy:boolean){for(const id of ['knowledge-extraction-enabled','knowledge-extraction-scheduled','knowledge-extraction-interval','knowledge-extraction-save'])input(id).disabled=busy}
function showKnowledgeExtractionHistory(config:KnowledgeExtractionConfig){
 element('knowledge-extraction-last').textContent=config.last_organized?'最近定时整理：'+new Date(config.last_organized).toLocaleString():'尚无成功定时整理记录。';
 element('knowledge-extraction-error').textContent=config.last_error?'最近定时整理未完成：'+config.last_error:'';
 element('knowledge-extraction-error').classList.toggle('hidden',!config.last_error);
}
async function loadKnowledgeExtractionSettings(){
 if(knowledgeExtractionSettingsBusy||knowledgeExtractionSettingsDirty)return;
 const epoch=shellEpoch,request=++knowledgeExtractionRequest;knowledgeExtractionSettingsReady=false;setKnowledgeExtractionSettingsBusy(true);
 try{const config=await api<KnowledgeExtractionConfig>('library/extraction','GET',undefined,shellController.signal);if(!shellCurrent(epoch)||request!==knowledgeExtractionRequest)return;input('knowledge-extraction-enabled').checked=config.enabled;input('knowledge-extraction-scheduled').checked=!!config.scheduled;input('knowledge-extraction-interval').value=String(config.interval_hours||24);showKnowledgeExtractionHistory(config);knowledgeExtractionSettingsReady=true;element('knowledge-extraction-settings-state').textContent=config.needs_directory?'先设置笔记根目录并开启同步，整理结果才会写入文件。':config.scheduled?`定时整理已开启，每 ${config.interval_hours||24} 小时检查已有候选；不调用模型。`:config.enabled?'每轮自动整理已开启；候选保持待验证。':'自动与定时整理已关闭，已有知识保留。'}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)element('knowledge-extraction-settings-state').textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)setKnowledgeExtractionSettingsBusy(!knowledgeExtractionSettingsReady)}
}
async function saveKnowledgeExtractionSettings(){
 if(!knowledgeExtractionSettingsReady||knowledgeExtractionSettingsBusy)return;
 const interval=Number(input('knowledge-extraction-interval').value);if(!Number.isInteger(interval)||interval<1||interval>168){element('knowledge-extraction-settings-state').textContent='整理间隔请输入 1～168 之间的整数小时。';input('knowledge-extraction-interval').focus();return}
 const epoch=shellEpoch,request=++knowledgeExtractionRequest;knowledgeExtractionSettingsBusy=true;setKnowledgeExtractionSettingsBusy(true);
 try{const saved=await api<KnowledgeExtractionConfig>('library/extraction','PUT',{enabled:input('knowledge-extraction-enabled').checked,scheduled:input('knowledge-extraction-scheduled').checked,interval_hours:interval});if(!shellCurrent(epoch)||request!==knowledgeExtractionRequest)return;knowledgeExtractionSettingsDirty=false;showKnowledgeExtractionHistory(saved);element('knowledge-extraction-settings-state').textContent='整理方式已保存；自动与定时整理只使用已有候选，不另起模型调用。'}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeExtractionRequest)element('knowledge-extraction-settings-state').textContent='保存失败：'+(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===knowledgeExtractionRequest){knowledgeExtractionSettingsBusy=false;setKnowledgeExtractionSettingsBusy(false)}}
}
async function ensureExtractionDirectory(){
 const epoch=shellEpoch,config=await api<KnowledgeExtractionConfig>('library/extraction','GET',undefined,shellController.signal);
 if(!shellCurrent(epoch))return false;
 if(!config.needs_directory)return true;
 await openLibrarySettings();if(!shellCurrent(epoch))return false;element('vault-status').textContent='请先选择 Obsidian 笔记根目录，开启 Markdown 同步并保存，再返回整理知识。';input('vault-directory').focus();return false;
}
function knowledgeExtractionActionTarget(){return {epoch:shellEpoch,request:++knowledgeExtractionActionRequest,view:libraryRequest,selected:selection,task:chosen,create:creatingTask,scope:input('library-scope').value,layer:input('library-layer').value}}
function knowledgeExtractionActionCurrent(target:ReturnType<typeof knowledgeExtractionActionTarget>){return shellCurrent(target.epoch)&&target.request===knowledgeExtractionActionRequest&&target.view===libraryRequest&&target.selected===selection&&target.task===chosen&&target.create===creatingTask&&target.scope===input('library-scope').value&&target.layer===input('library-layer').value}
async function extractRecordedKnowledge(){
 if(knowledgeExtractionBusy)return;
 const target=knowledgeExtractionActionTarget(),epoch=target.epoch,taskScope=target.scope,layer=target.layer;
 knowledgeExtractionBusy=true;button('library-extract').disabled=true;button('library-ai-extract').disabled=true;element('library-extraction-state').textContent='正在检查知识目录…';
 try{if(!await ensureExtractionDirectory()||!knowledgeExtractionActionCurrent(target))return;element('library-extraction-state').textContent='正在整理已保存的分层候选，不额外调用模型…';const report=await api<KnowledgeExtractionReport>('library/extract','POST',{task_id:taskScope,layers:['consensus','system','project'].includes(layer)?[layer]:['consensus','system','project']});if(!shellCurrent(epoch))return;if(report.created||report.revisions)markKnowledgeSyncPending();if(!knowledgeExtractionActionCurrent(target)){notify('原范围的候选整理已完成；当前查看范围保持不变。');return}element('library-extraction-state').textContent=`整理完成：新增 ${report.created}，修订候选 ${report.revisions||0}，已存在 ${report.duplicates}，跳过 ${report.skipped}。${report.created||report.revisions?'新条目保持待验证，同主题修订请在原文中查看。':'没有新增候选；可选择任务后用 AI 重新整理历史。'}`+(report.warnings?.length?' '+report.warnings.join('；'):'');await searchLibrary();void loadLibraryOverview()}
 catch(e){if(knowledgeExtractionActionCurrent(target)){element('library-extraction-state').textContent='整理失败：'+(e as Error).message;if((e as Error&{status?:number}).status===409)await openLibrarySettings()}}
 finally{if(shellCurrent(epoch)&&target.request===knowledgeExtractionActionRequest){knowledgeExtractionBusy=false;updateKnowledgeOrganizationButtons()}}
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
 const target=knowledgeExtractionActionTarget(),epoch=target.epoch;knowledgeExtractionBusy=true;button('library-extract').disabled=true;button('library-ai-extract').disabled=true;
 try{if(!await ensureExtractionDirectory()||!knowledgeExtractionActionCurrent(target))return;
  const title=tasks.find(t=>t.id===task)?.title||task;
  const preview=await api<{remaining:number;batch_size:number;source_ids:string[]}>('tasks/'+encodeURIComponent(task)+'/knowledge/extract','GET',undefined,shellController.signal);if(!knowledgeExtractionActionCurrent(target))return;
  if(!preview.remaining){element('library-extraction-state').textContent='该任务已结束的对话均已整理；没有新资料，无需重复调用模型。';return}
  const confirmed=confirm(`AI 整理并提取知识\n\n将使用“${title}”当前引擎和模型，按时间整理尚未处理的对话，包含失败及停止记录。当前剩余 ${preview.remaining} 轮，本次最多 ${preview.batch_size} 轮；会附加当前任务 Markdown 与相关已有主题的有界摘录。长文本可能截断，执行时以当时尚未整理的资料为准。\n\n会消耗模型额度，候选均保持待验证，同主题变更进入待合并修订。成功后保存进度，再次点击可继续下一批；失败不推进进度。不会继续调试或执行硬件操作。\n\n开始本批整理？`);
  if(!confirmed||!knowledgeExtractionActionCurrent(target))return;
  const result=await api<{run:Run}>('tasks/'+encodeURIComponent(task)+'/knowledge/extract','POST',{confirm:true});if(!shellCurrent(epoch))return;if(knowledgeExtractionActionCurrent(target))element('library-extraction-state').textContent=`AI 整理已排队（本批最多 ${preview.batch_size} 轮），可在任务执行记录查看进度；成功后再次点击继续下一批。`;notify('AI 知识整理已排队：'+title);void result;
 }catch(e){if(knowledgeExtractionActionCurrent(target))element('library-extraction-state').textContent='AI 整理未启动：'+(e as Error).message}
 finally{if(shellCurrent(epoch)&&target.request===knowledgeExtractionActionRequest){knowledgeExtractionBusy=false;updateKnowledgeOrganizationButtons()}}
}
function installKnowledgeMigration(){
 element('knowledge-extraction-settings').insertAdjacentHTML('afterend','<details id="knowledge-migration-settings" class="knowledge-extraction-settings"><summary>4. 迁移已有知识到新目录</summary><p>完整复制当前知识根中的 Markdown 与来源资料，并保留原有身份、修订和相对链接。预览确认后才切换根目录；不覆盖目标异文、不删除旧目录，不负责语义重写旧内容。三类相对目录保持不变；仅改变设置不会搬迁独立原件。</p><label>新笔记根目录<input id="knowledge-migration-directory" placeholder="选择一个新的 Obsidian 专用文件夹" maxlength="1000"></label><label>新根目录方式<select id="knowledge-migration-root-mode"><option value="direct">所选目录就是知识根</option><option value="legacy">所选目录下的 Duo/</option></select></label><div class="actions"><button type="button" id="knowledge-migration-preview">预览迁移</button><button type="button" id="knowledge-migration-apply" class="primary" disabled>确认迁移并切换目录</button></div><p id="knowledge-migration-state" role="status"></p><pre id="knowledge-migration-files" class="hidden"></pre></details>');
 const invalidate=()=>{knowledgeMigrationRequest++;knowledgeMigrationPreview=null;button('knowledge-migration-apply').disabled=true;element('knowledge-migration-state').textContent='目标目录有变化，请重新预览。';element('knowledge-migration-files').classList.add('hidden')};
 for(const id of ['knowledge-migration-directory','knowledge-migration-root-mode'])input(id).oninput=input(id).onchange=invalidate;
 button('knowledge-migration-preview').onclick=()=>void previewKnowledgeMigration();button('knowledge-migration-apply').onclick=()=>void applyKnowledgeMigration();
}
function setKnowledgeMigrationBusy(busy:boolean){
 knowledgeMigrationBusy=busy;for(const id of ['knowledge-migration-directory','knowledge-migration-root-mode','knowledge-migration-preview'])input(id).disabled=busy;
 button('knowledge-migration-apply').disabled=busy||!knowledgeMigrationPreview?.ready;
 setVaultControlsDisabled(busy||!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing);
 button('library-refresh-files').disabled=busy;
}
function showKnowledgeMigrationPreview(report:KnowledgeMigrationPreview){
 element('knowledge-migration-state').textContent=(report.completed?'迁移完成：复制 '+(report.copied||0)+' 份，已切换到新知识根。旧目录保留。':'预览：'+report.file_count+' 份文件，共 '+report.total_bytes+' 字节；待复制 '+report.copy_count+'，内容相同 '+report.identical_count+'。')+' 来源：'+report.source_directory+'；目标：'+report.target_directory+'。'+(report.source_dirty?'当前知识还有未同步修改，请先“立即同步 / 重建索引”，处理冲突后重新预览。':'')+[...(report.conflicts||[]),...(report.warnings||[])].map(s=>' '+s).join('');
 element('knowledge-migration-state').classList.toggle('knowledge-organize-error',!report.ready&&!report.completed);
 const actions={copy:'复制',identical:'相同，跳过',conflict:'冲突，停止'};element('knowledge-migration-files').textContent=(report.files||[]).slice(0,200).map(f=>actions[f.action]+' · '+f.path+' · '+f.size+' 字节').join('\n')+((report.files||[]).length>200?'\n… 界面只显示前 200 份，确认时仍校验全部清单。':'');element('knowledge-migration-files').classList.toggle('hidden',!report.files?.length);
}
async function previewKnowledgeMigration(){
 if(knowledgeMigrationBusy||vaultSettingsSaving||vaultRefreshing)return;
 if(vaultSettingsDirty){element('knowledge-migration-state').textContent='请先保存或放弃第 1 组的路径草稿，再预览当前已保存知识根的迁移。';return}
 const directory=input('knowledge-migration-directory').value.trim(),rootMode=input('knowledge-migration-root-mode').value;
 if(!directory){element('knowledge-migration-state').textContent='请填写新笔记根目录。';input('knowledge-migration-directory').focus();return}
 const epoch=shellEpoch,request=++knowledgeMigrationRequest;knowledgeMigrationPreview=null;setKnowledgeMigrationBusy(true);element('knowledge-migration-state').textContent='正在比对源文件、目标文件和同步基线…';
 try{const report=await api<KnowledgeMigrationPreview>('library/vault/migration/preview','POST',{directory,root_mode:rootMode});if(!shellCurrent(epoch)||request!==knowledgeMigrationRequest)return;knowledgeMigrationPreview=report;showKnowledgeMigrationPreview(report)}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeMigrationRequest)element('knowledge-migration-state').textContent='迁移预览失败，当前路径未更改：'+(e as Error).message}
 finally{if(shellCurrent(epoch)&&request===knowledgeMigrationRequest)setKnowledgeMigrationBusy(false)}
}
async function applyKnowledgeMigration(){
 const preview=knowledgeMigrationPreview;if(knowledgeMigrationBusy||!preview?.ready||!preview.fingerprint)return;
 if(vaultSettingsDirty||vaultSettingsSaving||vaultRefreshing){knowledgeMigrationPreview=null;button('knowledge-migration-apply').disabled=true;element('knowledge-migration-state').textContent='笔记路径设置有未保存或正在进行的变化，请先处理第 1 组设置，再重新预览。';return}
 const epoch=shellEpoch,request=knowledgeMigrationRequest,directory=input('knowledge-migration-directory').value.trim(),rootMode=input('knowledge-migration-root-mode').value;
 if(!confirm('迁移已有知识并切换笔记目录？\n\n来源：'+preview.source_directory+'\n目标：'+preview.target_directory+'\n复制 '+preview.copy_count+' 份，跳过 '+preview.identical_count+' 份同内容文件，共 '+preview.total_bytes+' 字节。\n\n将保留知识身份、来源及修订，不删除旧目录，不覆盖不同内容的目标文件。源文件、数据库或目标文件变化时会停止，需要重新预览。'))return;
 if(!shellCurrent(epoch)||request!==knowledgeMigrationRequest)return;setKnowledgeMigrationBusy(true);element('knowledge-migration-state').textContent='正在按预览清单复制并切换知识根…';
 try{const report=await api<KnowledgeMigrationPreview>('library/vault/migration','POST',{directory,root_mode:rootMode,fingerprint:preview.fingerprint,confirm:true});if(!shellCurrent(epoch)||request!==knowledgeMigrationRequest)return;knowledgeMigrationPreview=null;showKnowledgeMigrationPreview(report);vaultSettingsReady=false;vaultSettingsDirty=false;setKnowledgeMigrationBusy(false);await loadVault();if(shellCurrent(epoch)&&request===knowledgeMigrationRequest){void loadLibraryOverview();element('knowledge-migration-settings').setAttribute('open','')}}
 catch(e){if(shellCurrent(epoch)&&request===knowledgeMigrationRequest){knowledgeMigrationPreview=null;element('knowledge-migration-state').textContent='迁移完成状态未确认：'+(e as Error).message+'。旧目录保留；请关闭后重新打开目录设置，核对实际知识根再操作。若尚未切换，可重新预览并跳过已复制的同内容文件。';element('knowledge-migration-state').classList.add('knowledge-organize-error');vaultSettingsReady=false}}
 finally{if(shellCurrent(epoch)&&request===knowledgeMigrationRequest)setKnowledgeMigrationBusy(false)}
}
async function consolidateKnowledgeTopic(){
 const doc=libraryPreviewDocument;if(knowledgeExtractionBusy||knowledgeRevisionBusy||!doc||doc.kind!=='curated'||!doc.pending_revisions?.length)return;
 if(curatedDirty||curatedSaving||element<HTMLDialogElement>('curated-dialog').open){notify('请先保存、放弃或关闭正在编辑的知识草稿，再整理主题。');return}
 const task=input('library-scope').value||(!creatingTask?chosen:'');if(!task){element('library-revision-state').textContent='请先在任务范围选择用于整理的任务，其引擎和模型将承担本次调用。';return}
 const target=knowledgeExtractionActionTarget(),epoch=target.epoch,id=doc.id,preview=libraryPreviewRequest,request=target.request,taskTitle=tasks.find(t=>t.id===task)?.title||task;
 const current=()=>knowledgeExtractionActionCurrent(target)&&preview===libraryPreviewRequest&&libraryPreviewID===id;
 knowledgeExtractionBusy=true;updateKnowledgeOrganizationButtons();
 try{if(!await ensureExtractionDirectory()||!current())return;
  if(!confirm('用 AI 整理此主题？\n\n主题：'+doc.title+'\n调用任务：“'+taskTitle+'”当前引擎和模型。\n\n会发送本主题当前正文、依据、适用条件及全部待合并修订，消耗模型额度。只整理本主题，不重读整个任务历史，不执行工具。原正文保持不变，结果保存为待采用合并稿，需你核查。无依据的冲突将保留为未决事项。'))return;
  if(!current())return;element('library-revision-state').textContent='正在排队整理本主题…';
  await api<{run:Run}>('library/curated/'+encodeURIComponent(id.replace(/^curated:/,''))+'/consolidate','POST',{task_id:task,revision:doc.revision,confirm:true});if(!shellCurrent(epoch))return;if(current())element('library-revision-state').textContent='AI 主题整理已排队。可在任务执行记录查看；结束后刷新文件并重新打开本主题，核对完整合并稿后采用。原正文尚未更改。';notify('AI 主题整理已排队：'+doc.title)}
 catch(e){if(current())element('library-revision-state').textContent='主题整理请求未确认成功：'+(e as Error).message+'。请先在任务执行记录核对是否已排队，再重试。'}
 finally{if(shellCurrent(epoch)&&request===knowledgeExtractionActionRequest){knowledgeExtractionBusy=false;updateKnowledgeOrganizationButtons()}}
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
