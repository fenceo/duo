type SearchHit={task_id:string;title:string;kind:string;reference:string;snippet:string;archived:boolean};
type FileEntry={name:string;path:string;directory:boolean;size:number;status?:string;blocked?:boolean};
type FileResult={path:string;items:FileEntry[];content:string;size:number;binary:boolean;truncated:boolean;message:string};
let taskView='active',searchHits:SearchHit[]|null=null,searchRequest=0,searchTimer:ReturnType<typeof setTimeout>,searchPending=false,searchLimited=false,searchError='';
let fileTask='',fileDirectory='',filePath='',fileMode='list',fileView='read',fileContent='',fileEntries:FileEntry[]=[],fileRequest=0,filePreviewRequest=0;

function installProductivity(){
 input('search').placeholder='搜索任务、对话、便签、知识';input('search').maxLength=160;input('search').setAttribute('aria-label','统一搜索');
 input('search').insertAdjacentHTML('afterend','<nav class="task-views" aria-label="任务视图"><button id="tasks-active" class="selected">任务</button><button id="tasks-archived">已归档</button></nav><div id="search-summary" class="muted hidden"></div><button id="search-clear" class="subtle hidden">清空搜索</button>');
 input('search').oninput=()=>{searchRequest++;clearTimeout(searchTimer);searchHits=null;searchError='';searchPending=!!input('search').value.trim();renderList();searchTimer=setTimeout(()=>void searchEverywhere(),250)};
 button('search-clear').onclick=()=>{input('search').value='';searchRequest++;searchHits=null;searchPending=false;searchError='';renderList()};
 for(const view of ['active','archived'])button('tasks-'+view).onclick=()=>{taskView=view;button('search-clear').click();renderList()};
 element('task-model').insertAdjacentHTML('beforebegin','<button id="files-tab">文件与改动</button>');button('files-tab').onclick=()=>switchTab('files');
 element('tool-body').insertAdjacentHTML('beforeend',`<section id="files-panel" class="tool-panel hidden"><p class="muted">读取当前任务执行环境中的工作目录。Git 状态包含已有修改，不能归因于本轮执行。</p><div class="file-toolbar"><button id="files-root">根目录</button><button id="files-up">上一级</button><button id="files-refresh">刷新</button><button id="files-changes">查看 Git 改动</button></div><p id="file-location" class="file-location"></p><input id="file-filter" aria-label="筛选当前文件列表" placeholder="筛选当前列表中的文件"><p id="file-list-status" class="muted" role="status"></p><div id="file-list" class="file-list"></div><div id="file-preview" class="hidden"><h3 id="file-name"></h3><div class="file-toolbar"><button id="file-read">内容</button><button id="file-diff">Git 差异</button><a id="file-download">下载文件</a><button id="file-reference">路径带入对话</button></div><p id="file-status" class="muted" role="status"></p><pre id="file-code" tabindex="0" aria-label="文件内容或差异"></pre></div></section>`);
 button('files-root').onclick=()=>{fileMode='list';fileDirectory='';void loadFiles()};button('files-up').onclick=()=>{fileMode='list';fileDirectory=fileDirectory.split('/').slice(0,-1).join('/');void loadFiles()};
 button('files-refresh').onclick=()=>void loadFiles();button('files-changes').onclick=()=>{fileMode=fileMode==='changes'?'list':'changes';void loadFiles()};input('file-filter').oninput=renderFiles;
 button('file-read').onclick=()=>void previewFile(filePath,'read');button('file-diff').onclick=()=>void previewFile(filePath,'diff');
 button('file-reference').onclick=()=>{if(fileTask!==chosen||!filePath)return;bringToChat('请查看当前工作目录中的文件：'+filePath)};
}
async function searchEverywhere(){
 const q=input('search').value.trim(),token=++searchRequest;if(!q){searchHits=null;searchPending=false;renderList();return}
 searchPending=true;renderList();try{const r=await api<{hits:SearchHit[];truncated:boolean}>('search?q='+encodeURIComponent(q));if(token!==searchRequest)return;searchHits=r.hits;searchLimited=r.truncated;searchError=''}catch(e){if(token!==searchRequest)return;searchHits=[];searchError=(e as Error).message}finally{if(token===searchRequest){searchPending=false;renderList()}}
}
async function openSearchHit(hit:SearchHit){
 try{
 await choose(hit.task_id);if(chosen!==hit.task_id||!detail)return;
 if(hit.kind==='knowledge'){switchTab('note');element('knowledge-list').querySelector<HTMLElement>('[data-knowledge="'+hit.reference+'"]')?.scrollIntoView({block:'center'});element('knowledge-list').querySelector<HTMLElement>('[data-knowledge-edit="'+hit.reference+'"]')?.focus()}
 else if(hit.kind==='scratch'){switchTab('scratch');await loadScratch();const target=element('scratch-list').querySelector<HTMLElement>(`[data-edit="${hit.reference}"]`);target?.scrollIntoView({block:'center'});target?.focus()}
 else if(hit.kind==='message'){
  const seq=Number(hit.reference);if(seq>0){const d=await api<Detail>('tasks/'+hit.task_id+'?after='+Math.max(0,seq-5));if(chosen!==hit.task_id)return;sequence=0;resetConversation();detail=d;element('conversation').innerHTML='<div class="search-context">正在显示搜索位置附近的对话。<button id="search-full-chat">从开头查看</button></div>';appendEvents(d.events);button('search-full-chat').onclick=()=>void choose(hit.task_id);const target=element('conversation').querySelector<HTMLElement>(`[data-event="${seq}"]`);if(target){target.dataset.searchMatch='true';applyConversationFilter();notify('已临时显示搜索命中的消息，筛选偏好保持不变。')}target?.scrollIntoView({block:'center'})}
 }
 }catch(e){notify((e as Error).message)}
}
async function changeTaskPreference(field:'pinned'|'archived',id=chosen){
 const target=tasks.find(t=>t.id===id)||(id===chosen?detail?.task:undefined);if(!id||!target)return;const value=!target[field];
 try{const t=await api<Task>('tasks/'+id+'/preferences','PATCH',{[field]:value});const i=tasks.findIndex(x=>x.id===id);if(i>=0)tasks[i]=t;if(id===chosen&&detail){detail.task=t;if(field==='archived')taskView=t.archived?'archived':'active';renderTask()}renderList();notify(field==='pinned'?(value?'任务已置顶':'已取消置顶'):(value?'任务已归档，记录保留，可随时恢复':'任务已恢复'));if(input('search').value.trim())void searchEverywhere()}catch(e){notify((e as Error).message)}
}
function filesURL(action:string,p:string){return `tasks/${fileTask}/files?action=${action}&path=${encodeURIComponent(p)}`}
async function loadFiles(){
 if(!chosen)return;if(fileTask!==chosen){fileTask=chosen;filePreviewRequest++;fileDirectory='';filePath='';fileMode='list';input('file-filter').value='';element('file-preview').classList.add('hidden')}
 const task=fileTask,token=++fileRequest;fileEntries=[];renderFiles();element('file-list-status').textContent='正在读取 '+(detail?.task.environment?.name||'执行环境')+'…';element('file-location').textContent=fileMode==='changes'?'工作目录当前 Git 改动':(fileDirectory||'工作目录 /');button('files-up').disabled=!fileDirectory;button('files-changes').textContent=fileMode==='changes'?'返回文件列表':'查看 Git 改动';
 try{const result=await api<FileResult>(filesURL(fileMode,fileMode==='changes'?'':fileDirectory));if(chosen!==task||token!==fileRequest)return;fileEntries=result.items||[];renderFiles();element('file-list-status').textContent=(result.message||`${fileEntries.length} 项`)+(result.truncated?' · 仅显示前 500 项，请进入子目录查看':'')}catch(e){if(token===fileRequest)element('file-list-status').textContent=(e as Error).message}
}
function renderFiles(){
 if(!element('file-list'))return;const q=input('file-filter').value.trim().toLowerCase();element('file-list').innerHTML=fileEntries.filter(f=>f.path.toLowerCase().includes(q)).map(f=>`<button class="file-row" data-file="${escapeHTML(f.path)}" ${f.blocked?'disabled':''}><span class="file-icon">${f.directory?'▸':'·'}</span><span>${escapeHTML(fileMode==='changes'?f.path:f.name)}${f.blocked?'（链接或特殊文件）':''}</span><small>${escapeHTML(f.status||(!f.directory?formatFileSize(f.size):''))}</small></button>`).join('')||'<p class="muted">列表为空。</p>';
 element('file-list').querySelectorAll<HTMLButtonElement>('[data-file]').forEach(b=>b.onclick=()=>{const f=fileEntries.find(f=>f.path===b.dataset.file)!;if(f.directory){fileDirectory=f.path;fileMode='list';input('file-filter').value='';void loadFiles()}else{void previewFile(f.path,fileMode==='changes'&&f.status!=='??'?'diff':'read')}})
}
function formatFileSize(size:number){return size>=1048576?(size/1048576).toFixed(1)+' MiB':size>=1024?(size/1024).toFixed(1)+' KiB':size+' B'}
async function previewFile(p:string,view:string){
 if(!p||fileTask!==chosen)return;filePath=p;fileView=view;fileContent='';const token=++filePreviewRequest,task=chosen;element('file-preview').classList.remove('hidden');element('file-name').textContent=p;element('file-status').textContent='正在读取…';element('file-code').textContent='';button('file-read').classList.toggle('selected',view==='read');button('file-diff').classList.toggle('selected',view==='diff');
 const download=element<HTMLAnchorElement>('file-download');download.href='/api/'+filesURL('download',p);download.download=p.split('/').at(-1)||'download';
 try{const r=await api<FileResult>(filesURL(view,p));if(chosen!==task||token!==filePreviewRequest)return;fileContent=r.content||'';element('file-status').textContent=(r.message||(view==='read'?formatFileSize(r.size)+' · UTF-8 文本':'工作目录当前差异'))+(r.truncated?' · 内容已截断':'');
  if(view==='diff')element('file-code').innerHTML=fileContent.split('\n').map(line=>`<span class="${line.startsWith('+')?'diff-add':line.startsWith('-')?'diff-remove':line.startsWith('@@')?'diff-context':''}">${escapeHTML(line)}\n</span>`).join('');else element('file-code').textContent=fileContent;
 }catch(e){if(token===filePreviewRequest)element('file-status').textContent=(e as Error).message}
}
