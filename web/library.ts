type LibraryDocument={id:string;kind:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean;include_automatic:boolean};
type AutomaticKnowledgeConfig={capture:boolean;recall:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
type VaultSettingsResponse={config:VaultConfig;report:VaultReport;document_directory:string};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
let automaticSettingsRequest=0,automaticSettingsReady=false,automaticSettingsSaving=false;
let vaultSettingsRequest=0,vaultSettingsReady=false,vaultSettingsSaving=false,vaultRefreshing=false;
function libraryTargetCurrent(){return !!libraryTarget&&shellCurrent(libraryTarget.epoch)&&selection===libraryTarget.selection&&creatingTask===libraryTarget.create&&(creatingTask||chosen===libraryTarget.task)}
function installLibrary(){
 element('new-task').insertAdjacentHTML('afterend','<button type="button" id="library-open">知识库 · 历史与结论</button>');
 element('message').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-task">引用历史 / 知识</button>');
 element('create-input').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-create">引用历史 / 知识</button>');
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
 element('library-results').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-library-reference]');if(b)void citeLibrary(b.dataset.libraryReference||'')};
 disposeWithShell(()=>{libraryRequest++;libraryTarget=null;libraryHits=[];automaticSettingsRequest++;automaticSettingsReady=false;automaticSettingsSaving=false;vaultSettingsRequest++;vaultSettingsReady=false;vaultSettingsSaving=false;vaultRefreshing=false});
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
 const scope=element<HTMLSelectElement>('library-scope');scope.innerHTML='<option value="">所有任务与 Vault</option>'+tasks.filter(t=>!t.deleted).map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');
 element<HTMLDialogElement>('library-dialog').showModal();
 button('library-manage-task').disabled=!chosen||creatingTask||!detail;
 await searchLibrary();
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
async function searchLibrary(){
 const token=++libraryRequest,epoch=shellEpoch;
 const q=new URLSearchParams({q:input('library-query').value,kind:input('library-kind').value,task:input('library-scope').value,stale:input('library-stale').checked?'1':'0'});
 element('library-state').textContent='正在检索…';
 try{const result=await api<{documents:LibraryDocument[];truncated:boolean}>('library/search?'+q,'GET',undefined,shellController.signal);if(token!==libraryRequest||!shellCurrent(epoch))return;libraryHits=result.documents;
  element('library-state').textContent=`找到 ${libraryHits.length} 条资料${result.truncated?'（还有更多，请缩小关键词或范围）':''}。${libraryTarget?.task||libraryTarget?.create?'引用后可在发送前编辑。':'请先新建或选择任务，再引用。'}`;
  element('library-results').innerHTML=libraryHits.length?libraryHits.map(d=>`<article class="library-card"><h3>${escapeHTML(d.title)}</h3><p class="muted">${escapeHTML(d.task_title||'独立笔记')} · ${d.origin==='vault'?'Obsidian':'本机'} · ${escapeHTML(d.kind==='knowledge'?knowledgeStateText(d.status):d.kind==='run'?'任务记录 · '+(names[d.status]||d.status):'笔记 · 待验证')}</p><p class="library-snippet">${escapeHTML(d.snippet)}</p>${d.path?`<small>${escapeHTML('Duo/'+d.path)}</small>`:''}<button type="button" data-library-reference="${escapeHTML(d.id)}"${libraryTargetCurrent()&&(libraryTarget?.task||libraryTarget?.create)?'':' disabled'}>引用到${libraryTarget?.create?'新任务':'当前任务'}</button></article>`).join(''):'<p class="muted">没有匹配资料。试试简短关键词，或先从 Git 拉取 Vault 并刷新索引。</p>';
 }catch(e){if(token===libraryRequest&&shellCurrent(epoch))element('library-state').textContent=(e as Error).message}
}
function knowledgeStateText(status:string){return status==='verified'?'已验证':status==='stale'?'已过时':'待验证'}
async function citeLibrary(id:string){
 const hit=libraryHits.find(d=>d.id===id);if(!hit||!libraryTargetCurrent())return;
 const target=libraryTarget!;
 try{const result=await api<{reference:string;truncated:boolean}>('library/reference?'+new URLSearchParams({id,hash:hit.hash}));if(libraryTarget!==target||!libraryTargetCurrent()){notify('任务已切换，请重新打开知识库选择引用。');return}
  const box=input(target.create?'create-input':'message');if(box.value.length+result.reference.length>60000)throw new Error('引用内容过多，请先精简当前输入。');
  box.value=box.value.trimEnd()+result.reference;if(!target.create)drafts.set(target.task,box.value);box.dispatchEvent(new Event('input',{bubbles:true}));
  element<HTMLDialogElement>('library-dialog').close();box.focus();notify(result.truncated?'已加入带来源的引用；长资料已截断，可在发送前检查。':'已加入带来源的引用，可在发送前检查。');
 }catch(e){if(shellCurrent(target.epoch))notify((e as Error).message)}
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
