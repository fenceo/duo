type LibraryDocument={id:string;kind:string;task_id:string;task_title:string;title:string;status:string;revision:number;updated:number;origin:string;path?:string;hash:string;snippet:string};
type VaultConfig={enabled:boolean;directory:string;include_runs:boolean};
type VaultReport={updated:number;exported:number;imported:number;indexed:number;conflicts:string[];warnings:string[];error?:string};
let libraryRequest=0,libraryTarget:{task:string;create:boolean;selection:number;epoch:number}|null=null;
let libraryHits:LibraryDocument[]=[];
function libraryTargetCurrent(){return !!libraryTarget&&shellCurrent(libraryTarget.epoch)&&selection===libraryTarget.selection&&creatingTask===libraryTarget.create&&(creatingTask||chosen===libraryTarget.task)}
function installLibrary(){
 element('new-task').insertAdjacentHTML('afterend','<button type="button" id="library-open">知识库 · 历史与结论</button>');
 element('message').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-task">引用历史 / 知识</button>');
 element('create-input').insertAdjacentHTML('beforebegin','<button type="button" class="library-compose-button" id="library-create">引用历史 / 知识</button>');
 element('root').insertAdjacentHTML('beforeend',`<dialog id="library-dialog" class="library-dialog"><div class="library-header"><div><h2>知识库</h2><p>查找之前的任务记录、结论和 Obsidian 笔记，选好后引用到任务要求中。</p></div><button type="button" id="library-close" aria-label="关闭知识库">关闭</button></div><form id="library-search-form" class="library-search"><input id="library-query" aria-label="搜索知识和历史" placeholder="关键词、报错码；多个词用空格分隔" maxlength="160"><select id="library-kind" aria-label="资料类型"><option value="">全部资料</option><option value="knowledge">结论与经验</option><option value="run">任务记录</option><option value="note">Obsidian 笔记</option></select><select id="library-scope" aria-label="任务范围"><option value="">所有任务与 Vault</option></select><label><input type="checkbox" id="library-stale">包含过时结论</label><button type="submit">查询</button></form><p id="library-state" role="status"></p><div id="library-results" class="library-results"></div><details id="vault-settings"><summary>Obsidian Vault 与 Git 同步</summary><p>选择这台 Duo 服务所在电脑的 Vault。Duo 只读写其中的 <code>Duo/</code> 文件夹；其中的 Markdown 可在 Obsidian 编辑。启用后每分钟同步一次，也可手动刷新。</p><label>Vault 绝对路径<input id="vault-directory" placeholder="例如 E:\\Notes\\MyVault"></label><label><input type="checkbox" id="vault-enabled">启用 Markdown 同步与索引</label><label><input type="checkbox" id="vault-runs">将所有任务的已结束记录也写入 Vault（可能包含私人内容）</label><p>同步文件包含问题、回复和结论，不包含数据库、登录凭据、工具日志或附件。常见密钥会脱敏，提交 Git 前仍请检查文件。建议使用自己的私有仓库。</p><div class="actions"><button type="button" id="vault-save">保存设置</button><button type="button" id="vault-refresh">立即同步 / 重建索引</button></div><p id="vault-status" role="status"></p><pre id="vault-problems" class="hidden"></pre><p>跨电脑：用 Obsidian Git 或 Git 客户端提交并推送 Vault，在另一台电脑克隆 / 拉取，再选择该电脑上的 Vault 路径。Duo 不自动执行 Git 推送；发现冲突会保留两端内容。删除本地任务不会删除已导出的 Markdown 档案。</p></details></dialog>`);
 for(const id of ['library-open','library-task','library-create'])button(id).onclick=()=>void openLibrary();
 button('library-close').onclick=()=>element<HTMLDialogElement>('library-dialog').close();
 element('library-search-form').onsubmit=e=>{e.preventDefault();void searchLibrary()};
 button('vault-save').onclick=()=>void saveVault();button('vault-refresh').onclick=()=>void refreshVault();
 element('library-results').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-library-reference]');if(b)void citeLibrary(b.dataset.libraryReference||'')};
 disposeWithShell(()=>{libraryRequest++;libraryTarget=null;libraryHits=[]});
}
async function openLibrary(){
 libraryTarget={task:chosen,create:creatingTask,selection,epoch:shellEpoch};
 const scope=element<HTMLSelectElement>('library-scope');scope.innerHTML='<option value="">所有任务与 Vault</option>'+tasks.filter(t=>!t.deleted).map(t=>`<option value="${escapeHTML(t.id)}">${escapeHTML(t.title)}</option>`).join('');
 element<HTMLDialogElement>('library-dialog').showModal();
 await Promise.all([searchLibrary(),loadVault()]);
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
async function loadVault(){const epoch=shellEpoch;try{const v=await api<{config:VaultConfig;report:VaultReport}>('library/vault','GET',undefined,shellController.signal);if(!shellCurrent(epoch))return;input('vault-directory').value=v.config.directory;input('vault-enabled').checked=v.config.enabled;input('vault-runs').checked=v.config.include_runs;showVaultReport(v.report)}catch(e){if(shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}}
async function saveVault(){const epoch=shellEpoch;button('vault-save').disabled=true;try{await api('library/vault','PUT',{directory:input('vault-directory').value.trim(),enabled:input('vault-enabled').checked,include_runs:input('vault-runs').checked});if(shellCurrent(epoch)){element('vault-status').textContent='设置已保存。';await refreshVault()}}catch(e){if(shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}finally{if(shellCurrent(epoch))button('vault-save').disabled=false}}
async function refreshVault(){const epoch=shellEpoch;button('vault-refresh').disabled=true;element('vault-status').textContent='正在同步 Markdown 与索引…';try{const report=await api<VaultReport>('library/vault/refresh','POST',{});if(!shellCurrent(epoch))return;showVaultReport(report);await searchLibrary();if(chosen)await loadKnowledge()}catch(e){if(shellCurrent(epoch))element('vault-status').textContent=(e as Error).message}finally{if(shellCurrent(epoch))button('vault-refresh').disabled=false}}
