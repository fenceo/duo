type LibraryDocument={id:string;kind:string;source?:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type LibraryReference={document?:LibraryDocument;reference:string;preview?:string;truncated:boolean};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean;include_automatic:boolean};
type AutomaticKnowledgeConfig={capture:boolean;recall:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
type VaultSettingsResponse={config:VaultConfig;report:VaultReport;document_directory:string};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
let libraryLoading=false,libraryNextOffset=0,libraryPreviewRequest=0,libraryOverviewRequest=0,libraryCitationRequest=0,libraryCitationBusy=false;
let libraryPreviewID='';
let automaticSettingsRequest=0,automaticSettingsReady=false,automaticSettingsSaving=false;
let vaultSettingsRequest=0,vaultSettingsReady=false,vaultSettingsSaving=false,vaultRefreshing=false;
function libraryTargetCurrent(){return !!libraryTarget&&shellCurrent(libraryTarget.epoch)&&selection===libraryTarget.selection&&creatingTask===libraryTarget.create&&(creatingTask||chosen===libraryTarget.task)}
function installLibrary(){
 element('new-task').insertAdjacentHTML('afterend','<button type="button" id="library-open">知识库 · 历史与结论</button>');
 element('command-open').insertAdjacentHTML('afterend','<button type="button" class="library-compose-button" id="library-task" title="引用历史 / 知识" aria-label="引用历史或知识">引用</button>');
 element('create-status').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-create" title="引用历史 / 知识">引用历史 / 知识</button>');
 element('root').insertAdjacentHTML('beforeend',`<dialog id="library-dialog" class="library-dialog"><div class="library-header"><div><h2>全局知识库</h2><p>查找之前的任务记录、结论和 Obsidian 笔记，选好后引用到任务要求中。</p></div><button type="button" id="library-close" aria-label="关闭知识库">关闭</button></div><form id="library-search-form" class="library-search"><input id="library-query" aria-label="搜索知识和历史" placeholder="关键词、报错码；多个词用空格分隔" maxlength="160"><select id="library-kind" aria-label="资料类型"><option value="">全部资料</option><option value="knowledge">结论与经验</option><option value="run">任务记录</option><option value="note">Obsidian 笔记</option></select><select id="library-scope" aria-label="任务范围"><option value="">所有任务与 Vault</option></select><label><input type="checkbox" id="library-stale">包含过时结论</label><button type="submit">查询</button></form><p id="library-state" role="status"></p><div id="library-results" class="library-results"></div><details id="vault-settings"><summary>Obsidian Vault 与 Git 同步</summary><p>选择这台 Duo 服务所在电脑的知识文件根目录。Duo 只读写其中的 <code>Duo/</code> 文件夹；其中的 Markdown 可用编辑器或 Obsidian 查看和编辑。启用后每分钟同步一次，也可手动刷新。</p><label>Vault 绝对路径<input id="vault-directory" placeholder="例如 E:\\Notes\\MyVault"></label><label><input type="checkbox" id="vault-enabled">启用 Markdown 同步与索引</label><label><input type="checkbox" id="vault-runs">同时保存全部任务的已结束对话记录（可能包含私人内容）</label><p>同步文件包含问题、回复和结论，不包含数据库、登录凭据、工具日志或附件。常见密钥会脱敏，提交 Git 前仍请检查文件。建议使用自己的私有仓库。</p><div class="actions"><button type="button" id="vault-save">保存设置</button><button type="button" id="vault-refresh">立即同步 / 重建索引</button></div><p id="vault-status" role="status"></p><pre id="vault-problems" class="hidden"></pre><p>跨电脑：用 Obsidian Git 或 Git 客户端提交并推送 Vault，在另一台电脑克隆 / 拉取，再选择该电脑上的 Vault 路径。Duo 不自动执行 Git 推送；发现冲突会保留两端内容。删除本地任务不会删除已导出的 Markdown 档案。</p></details></dialog>`);
 element('vault-settings').insertAdjacentHTML('beforebegin',`<details id="automatic-knowledge-settings"><summary>对话自动积累</summary><p>默认开启。完成一轮对话后，自动把本轮要求和最终回复记为待验证知识，不需要点保存。相同的问题与回复不会重复记录；不会另行调用模型做摘要。</p><label><input type="checkbox" id="automatic-capture" disabled>自动记录后续对话的问题与最终回复</label><label><input type="checkbox" id="automatic-recall" disabled>新建会话时自动补回当前任务知识</label><p>恢复时优先选已验证知识，再选最近记录，最多 3 条，每条最多 1200 字。排除过时条目，不读取其他任务；正常续聊不重复添加。恢复的文字计入本轮模型输入。</p><button type="button" id="automatic-save" disabled>保存设置</button><p id="automatic-status" role="status"></p></details>`);
 input('vault-runs').closest('label')!.insertAdjacentHTML('afterend','<label><input type="checkbox" id="vault-automatic">同步自动积累的知识（默认关闭；手工保存的知识始终纳入同步）</label>');
 installLibrarySettings();
 const headerActions=document.createElement('div');headerActions.className='library-header-actions';headerActions.innerHTML='<button type="button" id="library-manage-task">管理本任务知识</button><button type="button" id="library-settings-open">知识库设置</button>';
 button('library-close').before(headerActions);headerActions.append(button('library-close'));
 button('library-settings-open').onclick=()=>void openLibrarySettings();
 button('library-manage-task').onclick=()=>{if(!libraryTargetCurrent()||creatingTask||!chosen||!detail)return;element<HTMLDialogElement>('library-dialog').close();switchTab('note')};
 element('notebook').querySelector('.note-head .actions')!.insertAdjacentHTML('beforeend','<button type="button" id="knowledge-library-open">查全局知识库</button>');
 element('notebook').querySelector('h2')!.textContent='本任务知识';
 element('notebook').querySelector('.note-head')!.insertAdjacentHTML('afterend','<p class="muted knowledge-panel-help">这里是全局知识库中属于本任务的条目。自动记录会出现在这里，可按需纠错、验证或删除；日常聊天无需手动整理。</p>');
 button('summarize').textContent='手动整理';button('summarize').title='调用本任务的 AI 整理知识，完成后可检查并保存';
 button('knowledge-library-open').onclick=()=>void openLibrary();
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
 dialog.querySelector('.library-header p')!.textContent='先查找和预览，再把有用的内容引用到对话。';
 element('library-search-form').insertAdjacentHTML('beforebegin','<div id="library-overview" class="library-overview" role="status"></div><details class="library-help"><summary>AI 什么时候会用到这些内容？</summary><p>连续对话沿用原生会话。开启自动补回后，新建会话会带入本任务最多 3 条知识，优先已验证、再选近期记录，每条最多 1200 字。其他任务和文件笔记需手动引用；检索、预览不会调用模型。</p></details>');
 element('library-kind').innerHTML='<option value="knowledge">知识条目</option><option value="run">原始对话</option><option value="note">独立文件笔记</option><option value="">全部资料</option>';
 element('library-kind').insertAdjacentHTML('afterend','<select id="library-source" aria-label="知识来源"><option value="">全部来源</option><option value="auto">对话自动记录</option><option value="manual">手工保存与整理</option><option value="vault">知识文件目录</option></select>');
 const filters=document.createElement('div');filters.className='library-filters';
 const form=element('library-search-form');form.append(filters);
 for(const id of ['library-kind','library-source','library-scope'])filters.append(element(id));
 filters.append(input('library-stale').closest('label')!);
 for(const id of ['library-kind','library-source','library-scope','library-stale'])input(id).onchange=()=>{if(id==='library-kind')input('library-source').value='';if(id==='library-source'&&['manual','auto'].includes(input(id).value))input('library-kind').value='knowledge';void searchLibrary()};
 const workspace=document.createElement('div');workspace.id='library-workspace';workspace.className='library-workspace';
 element('library-results').before(workspace);
 const list=document.createElement('section');list.className='library-list-pane';workspace.append(list);list.append(element('library-results'));
 list.insertAdjacentHTML('beforeend','<button type="button" id="library-more" class="hidden">加载更多</button>');
 workspace.insertAdjacentHTML('beforeend','<section id="library-preview" class="library-preview" aria-label="资料预览"><button type="button" id="library-preview-back" class="subtle">← 返回结果</button><h3 id="library-preview-title">选择一条资料</h3><p id="library-preview-meta" class="muted">预览内容和来源后，再决定是否引用。</p><div id="library-preview-content" class="content"></div><p id="library-preview-status" role="status"></p><div class="library-preview-actions"><button type="button" id="library-preview-cite" class="primary hidden">引用到输入框</button><button type="button" id="library-preview-source" class="hidden">打开来源任务</button></div></section>');
 button('library-more').onclick=()=>void searchLibrary(true);
 button('library-preview-back').onclick=()=>clearLibraryPreview(true);
 button('library-preview-cite').onclick=()=>void citeLibrary(libraryPreviewID);
 button('library-preview-source').onclick=()=>{const hit=libraryHits.find(d=>d.id===libraryPreviewID);if(!hit||!tasks.some(t=>t.id===hit.task_id&&!t.deleted))return;dialog.close();void choose(hit.task_id,hit.kind==='knowledge'?'note':'chat')};
 element('library-results').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-library-reference],[data-library-preview]');if(!b)return;if(b.dataset.libraryReference)void citeLibrary(b.dataset.libraryReference);else void previewLibrary(b.dataset.libraryPreview||'')};
 dialog.addEventListener('close',()=>{libraryRequest++;libraryOverviewRequest++;libraryPreviewRequest++;libraryLoading=false;libraryTarget=null});
 element('knowledge-filter').insertAdjacentHTML('beforebegin','<div class="knowledge-search"><input id="knowledge-query" aria-label="搜索本任务知识" placeholder="搜索本任务知识"><select id="knowledge-source" aria-label="筛选知识来源"><option value="">全部来源</option><option value="auto">对话自动记录</option><option value="manual">手工保存与整理</option></select></div>');
 input('knowledge-query').oninput=()=>renderKnowledgeList();input('knowledge-source').onchange=()=>renderKnowledgeList();
}
function clearLibraryPreview(returnFocus=false){
 const previous=libraryPreviewID;
 libraryPreviewRequest++;libraryPreviewID='';element('library-workspace')?.classList.remove('preview-open');
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
 element('settings-error').insertAdjacentHTML('beforebegin',`<section id="settings-knowledge" class="settings-section hidden"><h3>知识库</h3><p>统一管理所有任务的知识积累和文件目录。日常查找与引用在左侧“知识库”，纠错、验证和删除在“本任务知识”。</p><div class="knowledge-storage-info"><h4>本机运行数据</h4><code id="knowledge-data-directory" class="data-dir-path"></code><p>任务、对话和知识共用此目录中的数据库。知识文件目录用于 Markdown 同步；切换完整运行数据请到“数据与存储”。</p></div></section>`);
 const section=element('settings-knowledge');
 for(const id of ['automatic-knowledge-settings','vault-settings']){const panel=element<HTMLDetailsElement>(id);panel.open=true;section.append(panel)}
 element('vault-settings').querySelector('summary')!.textContent='知识文件目录与同步';
 input('vault-directory').previousSibling!.textContent='知识文件根目录（服务所在电脑）';
 input('vault-directory').placeholder='例如 E:\\Notes\\DuoKnowledge';
 input('vault-directory').closest('label')!.insertAdjacentHTML('afterend','<p>实际文件目录：<code id="vault-document-directory">尚未设置</code></p><p class="muted">支持普通文件夹，也可选 Obsidian Vault。所有任务共用这一个根目录；Duo 只管理其中的 Duo/ 子目录。更换目录会重新同步当前内容，旧目录保留，不自动搬移文件。</p>');
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
async function openLibrary(){
 libraryTarget={task:chosen,create:creatingTask,selection,epoch:shellEpoch};
 const scope=element<HTMLSelectElement>('library-scope'),previousScope=scope.value;scope.innerHTML='<option value="">所有任务与文件</option>'+tasks.filter(t=>!t.deleted).map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');scope.value=tasks.some(t=>t.id===previousScope&&!t.deleted)?previousScope:'';
 element<HTMLDialogElement>('library-dialog').showModal();
 button('library-manage-task').disabled=!chosen||creatingTask||!detail;
 void loadLibraryOverview();
 await searchLibrary();
 input('library-query').focus();
}
function setAutomaticSettingsDisabled(disabled:boolean){for(const id of ['automatic-capture','automatic-recall','automatic-save'])input(id).disabled=disabled}
async function loadAutomaticKnowledge(){
 if(automaticSettingsSaving)return;
 const token=++automaticSettingsRequest,epoch=shellEpoch;automaticSettingsReady=false;setAutomaticSettingsDisabled(true);element('automatic-status').textContent='正在读取设置…';
 try{const config=await api<AutomaticKnowledgeConfig>('library/automatic','GET',undefined,shellController.signal);if(token!==automaticSettingsRequest||!shellCurrent(epoch))return;input('automatic-capture').checked=config.capture;input('automatic-recall').checked=config.recall;automaticSettingsReady=true;element('automatic-status').textContent=config.capture?'自动记录已开启，对后续完成的对话生效。':'自动记录已关闭，已有记录继续保留。'}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent=(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch))setAutomaticSettingsDisabled(!automaticSettingsReady)}
}
async function saveAutomaticKnowledge(){
 if(!automaticSettingsReady||automaticSettingsSaving)return;
 const epoch=shellEpoch,token=++automaticSettingsRequest;automaticSettingsSaving=true;setAutomaticSettingsDisabled(true);
 try{await api('library/automatic','PUT',{capture:input('automatic-capture').checked,recall:input('automatic-recall').checked});if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent='设置已保存。关闭不会删除已有知识，也不会从原生会话中移除之前已带入的内容。'}
 catch(e){if(token===automaticSettingsRequest&&shellCurrent(epoch))element('automatic-status').textContent='保存失败，请重试：'+(e as Error).message}
 finally{if(token===automaticSettingsRequest&&shellCurrent(epoch)){automaticSettingsSaving=false;setAutomaticSettingsDisabled(false)}}
}
async function searchLibrary(append=false){
 if(append&&libraryLoading)return;
 const token=++libraryRequest,epoch=shellEpoch;libraryLoading=true;
 if(!append){libraryHits=[];libraryNextOffset=0;clearLibraryPreview();element('library-results').textContent='';button('library-more').classList.add('hidden')}
 const q=new URLSearchParams({q:input('library-query').value,kind:input('library-kind').value,source:input('library-source').value,task:input('library-scope').value,stale:input('library-stale').checked?'1':'0',offset:String(append?libraryNextOffset:0)});
 element('library-state').textContent=append?'正在加载更多…':'正在检索…';button('library-more').disabled=true;
 try{const result=await api<{documents:LibraryDocument[];truncated:boolean;total?:number;next_offset?:number}>('library/search?'+q,'GET',undefined,shellController.signal);if(token!==libraryRequest||!shellCurrent(epoch))return;
  libraryHits=append?[...new Map([...libraryHits,...result.documents].map(d=>[d.id,d])).values()]:result.documents;libraryNextOffset=result.next_offset??libraryHits.length;
  element('library-state').textContent=`显示 ${libraryHits.length} / ${result.total??libraryHits.length} 条资料。${libraryTarget?.task||libraryTarget?.create?'引用会加入输入框，发送前仍可修改。':'可先预览；选择或新建任务后才能引用。'}`;
  renderLibraryHits();button('library-more').classList.toggle('hidden',!result.truncated);
 }catch(e){if(token===libraryRequest&&shellCurrent(epoch))element('library-state').textContent='检索失败，请重试：'+(e as Error).message}
 finally{if(token===libraryRequest&&shellCurrent(epoch)){libraryLoading=false;button('library-more').disabled=false}}
}
function libraryDescription(d:LibraryDocument){return (d.origin==='vault'?'文件笔记':d.kind==='run'?'原始对话':d.source==='auto'?'对话自动记录':'手工保存与整理')+' · '+(d.kind==='run'?(({done:'已完成',failed:'执行失败',interrupted:'已中断'} as Record<string,string>)[d.status]||d.status):knowledgeStateText(d.status))}
function renderLibraryHits(){
 element('library-results').innerHTML=libraryHits.length?libraryHits.map(d=>`<article class="library-card"${d.id===libraryPreviewID?' data-selected="true"':''}><h3><button type="button" data-library-preview="${escapeHTML(d.id)}">${escapeHTML(d.title)}</button></h3><p class="library-card-meta">${escapeHTML(libraryDescription(d))}</p><p class="library-snippet">${escapeHTML(d.snippet)}</p><footer><span>${escapeHTML(d.task_title||'独立笔记')}</span><button type="button" data-library-reference="${escapeHTML(d.id)}"${libraryCitationBusy||!libraryTargetCurrent()||(!libraryTarget?.task&&!libraryTarget?.create)?' disabled':''}>引用</button></footer></article>`).join(''):'<div class="library-empty"><strong>没有匹配的资料</strong><p>试试简短关键词，或调整资料类型、来源和任务范围。旧对话可切换到“原始对话”查找。</p></div>';
}
async function previewLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit)return;
 const token=++libraryPreviewRequest,epoch=shellEpoch;libraryPreviewID=id;
 element('library-workspace').classList.add('preview-open');element('library-preview-title').textContent=hit.title;
 element('library-preview-meta').textContent=libraryDescription(hit)+' · '+(hit.task_title||'独立笔记')+(hit.path?' · Duo/'+hit.path:'');
 element('library-preview-content').textContent='';element('library-preview-status').textContent='正在读取预览…';button('library-preview-cite').classList.add('hidden');button('library-preview-source').classList.add('hidden');highlightLibraryPreview();
 try{const result=await api<LibraryReference>('library/reference?'+new URLSearchParams({id,hash:hit.hash}),'GET',undefined,shellController.signal);if(token!==libraryPreviewRequest||!shellCurrent(epoch))return;
  element('library-preview-content').innerHTML=markdown(result.preview||'');element('library-preview-status').textContent=result.truncated?'预览与引用均截取前 6000 字符；完整内容请查看来源。':'引用将保留来源和验证状态，不会直接发送。';
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
  const box=appendKnowledgeReference(result.reference,k.task_id);switchTab('chat');box.focus();notify(result.truncated?'已引用知识，长内容已截断，请在发送前检查。':'已加入带来源和验证状态的引用。');
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
 try{const saved=await api<VaultConfig&{document_directory:string}>('library/vault','PUT',{directory:input('vault-directory').value.trim(),enabled:input('vault-enabled').checked,include_runs:input('vault-runs').checked,include_automatic:input('vault-automatic').checked});if(token!==vaultSettingsRequest||!shellCurrent(epoch))return;showVaultLocation(saved.document_directory,saved.enabled);element('vault-status').textContent='设置已保存，正在同步…';await syncVaultView(epoch,token)}
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
