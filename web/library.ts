type LibraryDocument={tags?:string[];layer?:string;generated?:boolean;content?:string;id:string;kind:string;source?:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type LibraryReference={document?:LibraryDocument;reference:string;preview?:string;truncated:boolean};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean;include_automatic:boolean;task_folders?:boolean};
type AutomaticKnowledgeConfig={capture:boolean;recall:boolean;organize?:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
type VaultSettingsResponse={config:VaultConfig;report:VaultReport;document_directory:string};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
let libraryLoading=false,libraryNextOffset=0,libraryPreviewRequest=0,libraryOverviewRequest=0,libraryCitationRequest=0,libraryCitationBusy=false;
let libraryPreviewID='',libraryPreviewText='',libraryPreviewLimit=20000;
let automaticSettingsRequest=0,automaticSettingsReady=false,automaticSettingsSaving=false;
let vaultSettingsRequest=0,vaultSettingsReady=false,vaultSettingsSaving=false,vaultRefreshing=false;
function libraryTargetCurrent(){return !!libraryTarget&&shellCurrent(libraryTarget.epoch)&&selection===libraryTarget.selection&&creatingTask===libraryTarget.create&&(creatingTask||chosen===libraryTarget.task)}
function installLibrary(){
 element('theme-toggle').insertAdjacentHTML('afterend','<button type="button" id="library-open" title="全局知识库 · 历史与结论" aria-label="全局知识库">知识库</button>');
 element('command-open').insertAdjacentHTML('afterend','<button type="button" class="library-compose-button" id="library-task" title="引用历史 / 知识" aria-label="引用历史或知识">引用</button>');
 element('create-status').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-create" title="引用历史 / 知识">引用历史 / 知识</button>');
 element('root').insertAdjacentHTML('beforeend',`<dialog id="library-dialog" class="library-dialog"><div class="library-header"><div><h2>全局知识库</h2><p>打开文档即可阅读；任务资料、通用知识与主题索引放在同一个目录。</p></div><button type="button" id="library-close" aria-label="关闭知识库">关闭</button></div><form id="library-search-form" class="library-search"><input id="library-query" aria-label="搜索知识和历史" placeholder="关键词、报错码；多个词用空格分隔" maxlength="160"><select id="library-kind" aria-label="资料类型"><option value="">全部资料</option><option value="knowledge">结论与经验</option><option value="run">任务记录</option><option value="note">Obsidian 笔记</option></select><select id="library-scope" aria-label="任务范围"><option value="">所有任务与 Vault</option></select><label><input type="checkbox" id="library-stale">包含过时结论</label><button type="submit">查询</button></form><p id="library-state" role="status"></p><div id="library-results" class="library-results"></div><details id="vault-settings"><summary>Obsidian Vault 与 Git 同步</summary><p>选择这台 Duo 服务所在电脑的知识文件根目录。Duo 只读写其中的 <code>Duo/</code> 文件夹；其中的 Markdown 可用编辑器或 Obsidian 查看和编辑。启用后每分钟同步一次，也可手动刷新。</p><label>Vault 绝对路径<input id="vault-directory" placeholder="例如 E:\\Notes\\MyVault"></label><label><input type="checkbox" id="vault-enabled">启用 Markdown 同步与索引</label><label><input type="checkbox" id="vault-runs">同时保存全部任务的已结束对话记录（可能包含私人内容）</label><p>同步文件包含问题、回复和结论，不包含数据库、登录凭据、工具日志或附件。常见密钥会脱敏，提交 Git 前仍请检查文件。建议使用自己的私有仓库。</p><div class="actions"><button type="button" id="vault-save">保存设置</button><button type="button" id="vault-refresh">立即同步 / 重建索引</button></div><p id="vault-status" role="status"></p><pre id="vault-problems" class="hidden"></pre><p>跨电脑：用 Obsidian Git 或 Git 客户端提交并推送 Vault，在另一台电脑克隆 / 拉取，再选择该电脑上的 Vault 路径。Duo 不自动执行 Git 推送；发现冲突会保留两端内容。删除本地任务不会删除已导出的 Markdown 档案。</p></details></dialog>`);
 element('vault-settings').insertAdjacentHTML('beforebegin',`<details id="automatic-knowledge-settings"><summary>对话自动积累</summary><p>对话自动保存到任务资料夹。自动整理在同一轮 AI 回复中提取摘要、前提、共识、疑问和有依据的通用知识，不另外启动模型。模型未提供整理结果时，保留原对话。</p><label><input type="checkbox" id="automatic-capture" disabled>自动记录后续对话的问题与最终回复</label><label><input type="checkbox" id="automatic-organize" disabled>随正常对话自动整理摘要、共识与通用知识（增加少量本轮输出）</label><label><input type="checkbox" id="automatic-recall" disabled>新建会话时自动补回当前任务知识</label><p>新会话优先补回本任务的前提、共识与未决问题；没有摘要时使用最近记录。只补回有限片段，其他任务需选择引用；引用文字计入模型输入。</p><button type="button" id="automatic-save" disabled>保存设置</button><p id="automatic-status" role="status"></p></details>`);
 input('vault-runs').closest('label')!.insertAdjacentHTML('afterend','<label><input type="checkbox" id="vault-automatic">同步自动整理的任务资料、通用知识与主题索引</label>');
 installLibrarySettings();
 const headerActions=document.createElement('div');headerActions.className='library-header-actions';headerActions.innerHTML='<button type="button" id="library-manage-task">管理本任务知识</button><button type="button" id="library-settings-open">知识库设置</button>';
 button('library-close').before(headerActions);headerActions.append(button('library-close'));
 button('library-settings-open').onclick=()=>void openLibrarySettings();
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
 disposeWithShell(()=>{libraryRequest++;libraryOverviewRequest++;libraryPreviewRequest++;libraryCitationRequest++;libraryCitationBusy=false;libraryLoading=false;libraryTarget=null;libraryHits=[];knowledgeEditTarget=null;knowledgeSaving=false;knowledgeExpanded.clear();automaticSettingsRequest++;automaticSettingsReady=false;automaticSettingsSaving=false;vaultSettingsRequest++;vaultSettingsReady=false;vaultSettingsSaving=false;vaultRefreshing=false});
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
 button('library-preview-source').onclick=()=>{const hit=libraryHits.find(d=>d.id===libraryPreviewID);if(!hit||!tasks.some(t=>t.id===hit.task_id&&!t.deleted))return;dialog.close();void choose(hit.task_id,hit.kind==='knowledge'?'note':'chat')};
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
 const nav=element('settings-form').querySelector('.settings-nav')!;
 nav.querySelector('[data-settings="data"]')!.insertAdjacentHTML('afterend','<button type="button" data-settings="knowledge">知识库</button>');
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-knowledge" class="settings-section hidden"><h3>知识库</h3><p>所有文档共用一个目录。默认在当前运行数据目录下自动建立 knowledge/Duo/，也可填写自选目录；文档不依赖此电脑的绝对路径。</p><div class="knowledge-storage-info"><h4>本机运行数据</h4><code id="knowledge-data-directory" class="data-dir-path"></code><p>任务、对话和知识共用此目录中的数据库。知识文件目录用于 Markdown 同步；切换完整运行数据请到“数据与存储”。</p></div></section>`);
 const section=element('settings-knowledge');
 for(const id of ['automatic-knowledge-settings','vault-settings']){const panel=element<HTMLDetailsElement>(id);panel.open=true;section.append(panel)}
 element('vault-settings').querySelector('summary')!.textContent='知识文件目录与同步';
 input('vault-directory').previousSibling!.textContent='知识文件根目录（服务所在电脑）';
 input('vault-directory').placeholder='留空使用当前数据目录下的 knowledge';
 input('vault-directory').closest('label')!.insertAdjacentHTML('afterend','<p>实际文件目录：<code id="vault-document-directory">尚未设置</code></p><p class="muted">Duo/ 下按 tasks/、knowledge/、topics/ 保存。路径仅配置在本机；文档内部使用相对链接。更换目录会同步当前资料，旧目录保留；独立外部笔记需自行复制。</p>');
 button('vault-save').textContent='保存知识库路径与同步设置';
 input('vault-directory').onkeydown=e=>{if(e.key==='Enter'&&!e.isComposing){e.preventDefault();void saveVault()}};
 setVaultControlsDisabled(true);
}
async function openLibrarySettings(){
 const epoch=shellEpoch;element<HTMLDialogElement>('library-dialog').close();
 await openSettings();if(!shellCurrent(epoch)||!element<HTMLDialogElement>('settings-dialog').open)return;
 if(element('settings-knowledge').classList.contains('hidden'))showSettingsSection('knowledge');
}
async function loadKnowledgeSettings(){
 element('knowledge-data-directory').textContent=settings.data_dir||'未能读取当前数据目录';
 await Promise.all([loadAutomaticKnowledge(),loadVault()]);
}
async function openLibrary(taskScope?:string){
 libraryTarget={task:chosen,create:creatingTask,selection,epoch:shellEpoch};
 const scope=element<HTMLSelectElement>('library-scope'),previousScope=scope.value;scope.innerHTML='<option value="">所有任务与文件</option>'+tasks.filter(t=>!t.deleted).map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');scope.value=tasks.some(t=>t.id===previousScope&&!t.deleted)?previousScope:'';
 if(taskScope){scope.value=taskScope;input('library-layer').value='tasks';element('library-dialog').querySelectorAll<HTMLButtonElement>('[data-library-layer]').forEach(b=>{const on=b.dataset.libraryLayer==='tasks';b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))})}
 element<HTMLDialogElement>('library-dialog').showModal();
 button('library-manage-task').disabled=!chosen||creatingTask||!detail;
 void loadLibraryOverview();
 await searchLibrary();
 input('library-query').focus();
}
function setAutomaticSettingsDisabled(disabled:boolean){for(const id of ['automatic-capture','automatic-recall','automatic-organize','automatic-save'])input(id).disabled=disabled}
async function loadAutomaticKnowledge(){
 if(automaticSettingsSaving)return;
 const token=++automaticSettingsRequest,epoch=shellEpoch;automaticSettingsReady=false;setAutomaticSettingsDisabled(true);element('automatic-status').textContent='正在读取设置…';
 try{const config=await api<AutomaticKnowledgeConfig>('library/automatic','GET',undefined,shellController.signal);if(token!==automaticSettingsRequest||!shellCurrent(epoch))return;input('automatic-capture').checked=config.capture;input('automatic-recall').checked=config.recall;input('automatic-organize').checked=config.organize!==false;automaticSettingsReady=true;element('automatic-status').textContent=config.capture?'自动记录已开启，对后续完成的对话生效。':'自动记录已关闭，已有记录继续保留。'}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent=(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch))setAutomaticSettingsDisabled(!automaticSettingsReady)}
}
async function saveAutomaticKnowledge(){
 if(!automaticSettingsReady||automaticSettingsSaving)return;
 const epoch=shellEpoch,token=++automaticSettingsRequest;automaticSettingsSaving=true;setAutomaticSettingsDisabled(true);
 try{await api('library/automatic','PUT',{capture:input('automatic-capture').checked,recall:input('automatic-recall').checked,organize:input('automatic-organize').checked});if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent='设置已保存。关闭不会删除已有知识，也不会从原生会话中移除之前已带入的内容。'}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent='保存失败，请重试：'+(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch)){automaticSettingsSaving=false;setAutomaticSettingsDisabled(false)}}
}
async function searchLibrary(append=false){
 if(append&&libraryLoading)return;
 const token=++libraryRequest,epoch=shellEpoch;libraryLoading=true;
 if(!append){libraryHits=[];libraryNextOffset=0;clearLibraryPreview();element('library-results').textContent='';button('library-more').classList.add('hidden')}
 const q=new URLSearchParams({q:input('library-query').value,kind:input('library-kind').value,source:input('library-source').value,task:input('library-scope').value,stale:input('library-stale').checked?'1':'0',layer:input('library-layer')?.value||'',tag:input('library-tag')?.value.trim()||'',offset:String(append?libraryNextOffset:0)});
 element('library-state').textContent=append?'正在加载更多…':'正在检索…';button('library-more').disabled=true;
 try{const result=await api<{documents:LibraryDocument[];truncated:boolean;total?:number;next_offset?:number}>('library/search?'+q,'GET',undefined,shellController.signal);if(token!==libraryRequest||!shellCurrent(epoch))return;
  libraryHits=append?[...new Map([...libraryHits,...result.documents].map(d=>[d.id,d])).values()]:result.documents;libraryNextOffset=result.next_offset??libraryHits.length;
  element('library-state').textContent=`显示 ${libraryHits.length} / ${result.total??libraryHits.length} 条资料。${libraryTarget?.task||libraryTarget?.create?'引用会加入输入框，发送前仍可修改。':'可先预览；选择或新建任务后才能引用。'}`;
  renderLibraryHits();button('library-more').classList.toggle('hidden',!result.truncated);
 }catch(e){if(token===libraryRequest&&shellCurrent(epoch))element('library-state').textContent='检索失败，请重试：'+(e as Error).message}
 finally{if(token===libraryRequest&&shellCurrent(epoch)){libraryLoading=false;button('library-more').disabled=false}}
}
function libraryDescription(d:LibraryDocument){return ({tasks:'任务资料',knowledge:'通用知识',topics:'主题索引',history:'对话记录',notes:'任务笔记',files:'Markdown 文档'} as Record<string,string>)[d.layer||'']||(d.kind==='run'?'对话记录':'任务笔记')}
function renderLibraryHits(){
 element('library-results').innerHTML=libraryHits.length?libraryHits.map(d=>`<article class="library-card"${d.id===libraryPreviewID?' data-selected="true"':''}><h3><button type="button" data-library-preview="${escapeHTML(d.id)}">${escapeHTML(d.title)}</button></h3><p class="library-card-meta">${escapeHTML(libraryDescription(d))} · ${escapeHTML(d.task_title||'共用知识')}</p><code class="library-document-path">${escapeHTML(d.path||'')}</code><p class="library-tags">${(d.tags||[]).map(t=>'#'+escapeHTML(t)).join(' · ')}</p><footer><span>${new Date(d.updated).toLocaleDateString()}</span><button type="button" data-library-reference="${escapeHTML(d.id)}"${libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create)?' disabled':''}>引用</button></footer></article>`).join(''):'<div class="library-empty"><strong>暂时没有匹配的文档</strong><p>任务资料随对话保存；通用知识需要回复中有明确依据，主题索引在出现标签后自动形成。旧摘录可在“全部文档”查看。</p></div>';
}
async function previewLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit)return;
 const token=++libraryPreviewRequest,epoch=shellEpoch;libraryPreviewID=id;
 libraryPreviewText='';button('library-download').classList.add('hidden');button('library-read-more').classList.add('hidden');
 element('library-workspace').classList.add('preview-open');element('library-preview-title').textContent=hit.title;
 element('library-preview-meta').textContent=libraryDescription(hit)+' · '+(hit.task_title||'独立笔记')+(hit.path?' · Duo/'+hit.path:'');
 element('library-preview-content').textContent='';element('library-preview-status').textContent='正在读取预览…';button('library-preview-cite').classList.add('hidden');button('library-preview-source').classList.add('hidden');highlightLibraryPreview();
 try{const result=await api<LibraryDocument>('library/document?'+new URLSearchParams({id,hash:hit.hash}),'GET',undefined,shellController.signal);if(token!==libraryPreviewRequest||!shellCurrent(epoch))return;
  libraryPreviewText=result.content||'';libraryPreviewLimit=20000;renderLibraryDocument();
  button('library-preview-cite').classList.remove('hidden');button('library-preview-cite').disabled=libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create);
  button('library-preview-source').classList.toggle('hidden',!tasks.some(t=>t.id===hit.task_id&&!t.deleted));
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
function setVaultControlsDisabled(disabled:boolean){for(const id of ['vault-directory','vault-enabled','vault-runs','vault-automatic','vault-save','vault-refresh'])input(id).disabled=disabled}
function showVaultLocation(directory:string,enabled:boolean){element('vault-document-directory').textContent=directory?(directory+(enabled?'':'（同步未启用）')):'尚未设置有效的知识文件根目录'}
async function loadVault(){
 if(vaultSettingsSaving||vaultRefreshing)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsReady=false;setVaultControlsDisabled(true);element('vault-status').textContent='正在读取知识库设置…';
 try{const v=await api<VaultSettingsResponse>('library/vault','GET',undefined,shellController.signal);if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;input('vault-directory').value=v.config.directory;input('vault-enabled').checked=v.config.enabled;input('vault-runs').checked=v.config.include_runs;input('vault-automatic').checked=!!v.config.include_automatic;showVaultLocation(v.document_directory,v.config.enabled);showVaultReport(v.report);vaultSettingsReady=true}
 catch(e){if(token===vaultSettingsRequest&&shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}
 finally{if(token===vaultSettingsRequest&&shellCurrent(epoch))setVaultControlsDisabled(!vaultSettingsReady)}
}
async function saveVault(){
 if(!vaultSettingsReady||vaultSettingsSaving||vaultRefreshing)return;
 const epoch=shellEpoch,token=++vaultSettingsRequest;vaultSettingsSaving=true;setVaultControlsDisabled(true);
 try{const saved=await api<VaultConfig&{document_directory:string}>('library/vault','PUT',{directory:input('vault-directory').value.trim(),enabled:input('vault-enabled').checked,include_runs:input('vault-runs').checked,include_automatic:input('vault-automatic').checked,task_folders:true});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;showVaultLocation(saved.document_directory,saved.enabled);element('vault-status').textContent='设置已保存，正在同步…';await syncVaultView(epoch,token)}
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
 element('library-preview-content').innerHTML=markdown(libraryPreviewText.slice(0,libraryPreviewLimit));
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
