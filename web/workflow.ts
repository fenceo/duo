type WorkMode={id:string;name:string;permission:'workspace'|'read';prompt:string;builtin?:boolean};
type QuickCommand={id:string;name:string;content:string};
type WorkCatalog={modes:WorkMode[];commands:QuickCommand[]};
type Attachment={id:string;name:string;mime:string;size:number};
let workCatalog:WorkCatalog={modes:[],commands:[]};
const attachmentDrafts=new Map<string,Attachment[]>(),uploadingTasks=new Set<string>();
let renameTaskID='',trashTaskID='',stoppingTask='',presetType:'modes'|'commands'='modes',presetID='',directoryEnvironment='',directoryPath='',directoryParent='',directoryRequest=0,workspacePicked:((path:string,environment:string)=>void)|null=null;
function modeOptions(select:HTMLSelectElement,snapshot?:WorkMode){
 const previous=select.value,items=[...workCatalog.modes];if(snapshot?.id&&!items.some(m=>m.id===snapshot.id))items.push(snapshot);
 select.innerHTML=items.map(m=>`<option value="${escapeHTML(m.id)}">${escapeHTML(m.name)}</option>`).join('');select.value=items.some(m=>m.id===previous)?previous:snapshot?.id||'work';
}
function installWorkflow(){
 element('new-task').insertAdjacentHTML('afterend','<button id="workspace-open" class="workspace-open">▣ 选择工作区 <span>⌄</span></button>');
 button('workspace-open').onclick=()=>openWorkspacePicker(settings.config.default_environment,'',(path,env)=>void showCreateAt(path,env));
 const form=element('create-form'),heading=element('create-heading'),lead=heading.nextElementSibling!;
 const head=document.createElement('div');head.className='create-page-head';
 const cancel=document.createElement('button');cancel.type='button';cancel.id='create-cancel';cancel.className='subtle';cancel.textContent='返回';head.append(heading,lead,cancel);
 lead.textContent='选择执行位置，直接描述目标；第一条要求会在任务创建后立即开始。';
 const context=document.createElement('div');context.className='create-context';
 const environment=input('create-environment'),environmentLabel=environment.previousElementSibling!;
 environmentLabel.remove();environment.setAttribute('aria-label','执行环境');
 const environmentField=document.createElement('div');environmentField.className='create-field create-context-field';environmentField.innerHTML='<span>环境</span>';environmentField.append(environment);
 const workspace=input('create-workspace'),workspaceLabel=workspace.previousElementSibling!,directoryHint=workspace.nextElementSibling!;
 workspaceLabel.remove();directoryHint.remove();workspace.setAttribute('aria-label','工作目录');
 const workspaceControl=document.createElement('div');workspaceControl.className='create-directory-control';
 const browse=document.createElement('button');browse.type='button';browse.id='create-browse';browse.className='create-browse';browse.title='浏览文件夹';browse.setAttribute('aria-label','浏览工作目录');browse.textContent='浏览';
 workspaceControl.append(workspace,browse);
 const workspaceField=document.createElement('div');workspaceField.className='create-field create-context-field create-workspace-field';workspaceField.innerHTML='<span>目录</span>';workspaceField.append(workspaceControl);
 context.append(environmentField,workspaceField,element('workspace-options'));
 const inputLabel=input('create-input').previousElementSibling!;
 inputLabel.classList.add('sr-only');inputLabel.textContent='任务要求';input('create-input').required=false;
 const createComposer=document.createElement('div');createComposer.className='create-composer';
 const attachmentDrafts=document.createElement('div');attachmentDrafts.id='create-attachment-drafts';attachmentDrafts.className='attachment-drafts';
 const toolbar=document.createElement('div');toolbar.className='create-composer-tools';
 const options=document.createElement('div');options.className='create-options';
 const attach=document.createElement('button');attach.type='button';attach.id='create-attach';attach.className='create-icon-button';attach.title='添加附件';attach.setAttribute('aria-label','添加附件');attach.textContent='＋';
 const files=document.createElement('input');files.id='create-files';files.type='file';files.multiple=true;files.className='hidden';
 const permission=document.createElement('div');permission.className='create-permission';permission.innerHTML='<span>权限</span><div class="create-segmented" id="create-permission-options" role="group" aria-label="任务权限"><button type="button" data-create-permission="request">请求批准</button><button type="button" data-create-permission="auto">帮我批准</button></div><small id="create-permission-hint"></small>';
 const mode=document.createElement('select');mode.id='create-mode';mode.className='hidden';mode.setAttribute('aria-hidden','true');mode.tabIndex=-1;
 const optionField=(name:string,control:HTMLElement,className='')=>{const box=document.createElement('div');box.className='create-field'+(className?' '+className:'');const label=document.createElement('span');label.textContent=name;box.append(label,control);return box};
 const engine=input('create-engine'),engineLabel=engine.previousElementSibling!;engineLabel.remove();
 const effort=input('create-effort'),effortLabel=effort.previousElementSibling!;effortLabel.remove();
 const modelLabel=input('model-picker-button').previousElementSibling!;modelLabel.remove();
 const modelField=optionField('模型',element('model-picker'),'create-model-field');modelField.append(input('create-model'));
 options.append(attach,files,permission,mode,optionField('AI 工具',engine),modelField,optionField('推理强度',effort),input('custom-model'));
 const submitWrap=document.createElement('div');submitWrap.className='create-submit-wrap';
 const createStatus=document.createElement('span');createStatus.id='create-status';createStatus.className='create-status';createStatus.setAttribute('role','status');
 const submit=button('create-submit');submit.classList.add('create-submit');submit.closest('.dialog-footer')!.remove();submitWrap.append(createStatus,submit);
 toolbar.append(options,submitWrap);createComposer.append(inputLabel,input('create-input'),attachmentDrafts,toolbar);
 const meta=document.createElement('div');meta.className='create-meta';
 const finalHint=element('create-workspace').closest('form')!.querySelectorAll(':scope > p');for(const node of finalHint)if(node!==element('effort-hint')&&node!==element('models-hint')&&node!==element('create-error'))node.remove();
 meta.append(element('models-hint'),element('reload-models'),element('effort-hint'),element('create-error'));
 form.replaceChildren(head,context,createComposer,meta);
 button('create-cancel').onclick=cancelCreate;
 button('create-browse').onclick=()=>openWorkspacePicker(input('create-environment').value,input('create-workspace').value,async(p,env)=>{input('create-environment').value=env;await loadCreateEnvironment();input('create-workspace').value=p});
 button('create-attach').onclick=()=>input('create-files').click();input('create-files').onchange=()=>{addCreateFiles(Array.from(input('create-files').files||[]));input('create-files').value=''};
 element('create-permission-options').querySelectorAll<HTMLButtonElement>('[data-create-permission]').forEach(b=>b.onclick=()=>setCreatePermission(b.dataset.createPermission==='request'?'request':'auto'));
 input('create-input').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();element<HTMLFormElement>('create-form').requestSubmit()}};
 const bar=document.createElement('div');bar.className='composer-tools';bar.innerHTML='<button type="button" id="attach-open" title="添加文件或图片，也可以拖放、粘贴图片">＋ 附件</button><button type="button" id="command-open">/ 指令</button><select id="message-mode" aria-label="本轮工作模式"></select><button type="button" id="mode-manage" title="管理工作模式">⚙</button><input id="attachment-input" type="file" multiple hidden>';
 element('composer').querySelector('.composer-bottom')!.before(bar);element('message').after(Object.assign(document.createElement('div'),{id:'attachment-drafts',className:'attachment-drafts'}));
 modeOptions(element<HTMLSelectElement>('message-mode'));modeOptions(element<HTMLSelectElement>('create-mode'));setCreatePermission(createPermission);
 button('attach-open').onclick=()=>input('attachment-input').click();input('attachment-input').onchange=()=>{const files=Array.from(input('attachment-input').files||[]);input('attachment-input').value='';void addAttachments(files)};
 const composer=element('composer');composer.ondragover=e=>{if(e.dataTransfer?.types.includes('Files')){e.preventDefault();composer.classList.add('dragover')}};composer.ondragleave=()=>composer.classList.remove('dragover');composer.ondrop=e=>{composer.classList.remove('dragover');if(e.dataTransfer?.files.length){e.preventDefault();void addAttachments(Array.from(e.dataTransfer.files))}};
 input('message').addEventListener('paste',e=>{const files=Array.from(e.clipboardData?.files||[]);if(files.length){e.preventDefault();void addAttachments(files)}});
 input('message').addEventListener('input',()=>{renderWorkflow();if(input('message').value==='/')openCommands()});
 button('mode-manage').onclick=()=>openPresetEditor('modes');button('command-open').onclick=openCommands;
 button('stop').onclick=async()=>{const id=chosen;stoppingTask=id;renderWorkflow();try{await api('tasks/'+id+'/stop','POST',{});await poll()}catch(e){stoppingTask='';notify((e as Error).message);renderWorkflow()}};
 element('tasks-archived').insertAdjacentHTML('afterend','<button id="trash-open" title="查看可恢复的会话">回收站</button>');button('trash-open').onclick=showTrash;
 element('task-title').ondblclick=()=>void openTaskRename();element('task-title').title='双击重命名';
 element('root').insertAdjacentHTML('beforeend',`<dialog id="rename-dialog"><form id="rename-form"><h2>重命名会话</h2><label for="rename-title">新名称</label><input id="rename-title" required maxlength="180"><p id="rename-error" class="error"></p><div class="dialog-footer"><button type="button" id="rename-cancel">取消</button><button class="primary" id="rename-save">保存名称</button></div></form></dialog><dialog id="trash-confirm-dialog"><h2>删除会话</h2><p id="trash-confirm-title"></p><p>移入回收站，可随时恢复。此会话的飞书绑定会解除，工作目录文件保留。</p><p id="trash-confirm-error" class="error"></p><div class="dialog-footer"><button id="trash-cancel">取消</button><button id="trash-confirm" class="danger">移入回收站</button></div></dialog><dialog id="workspace-dialog" class="workspace-dialog"><h2>选择工作区</h2><select id="workspace-environment" aria-label="工作区执行环境"></select><div class="workspace-choices" id="workspace-recent"></div><div class="directory-address"><button id="directory-up" title="上一级">↑</button><input id="directory-path" aria-label="目录路径"><button id="directory-go">前往</button></div><div id="directory-list" class="directory-list"></div><p id="directory-status" role="status"></p><label class="check-row"><input type="checkbox" id="workspace-remember" checked>添加到常用工作区</label><div class="dialog-footer"><button id="workspace-cancel">取消</button><button class="primary" id="workspace-select">选择此目录</button></div></dialog>
 <dialog id="presets-dialog"><h2 id="presets-title">工作模式</h2><p id="presets-hint"></p><div class="preset-list" id="preset-list"></div><form id="preset-form"><label for="preset-name">名称</label><input id="preset-name" required maxlength="40"><div id="preset-permission-wrap"><label for="preset-permission">执行方式</label><select id="preset-permission"><option value="workspace">工作区内执行</option><option value="read">分析规划</option></select></div><label for="preset-content" id="preset-content-label">要求（可留空）</label><textarea id="preset-content" rows="5" maxlength="16000"></textarea><p id="preset-error" class="error"></p><div class="dialog-footer"><button type="button" id="preset-delete" class="danger">删除</button><button type="button" id="presets-close">关闭</button><button class="primary" id="preset-save">保存</button></div></form></dialog>
 <dialog id="commands-dialog"><h2>快捷指令</h2><div id="commands-list" class="command-list"></div><div class="dialog-footer"><button id="commands-manage">管理指令</button><button id="commands-close">关闭</button></div></dialog>
 <dialog id="trash-dialog"><h2>回收站</h2><p>恢复后放回“已归档”。删除会话不会删除工作目录中的文件。</p><div id="trash-list"></div><div class="dialog-footer"><button id="trash-close">关闭</button></div></dialog>`);
 button('rename-cancel').onclick=()=>element<HTMLDialogElement>('rename-dialog').close();element('rename-form').onsubmit=saveTaskRename;button('trash-cancel').onclick=()=>element<HTMLDialogElement>('trash-confirm-dialog').close();button('trash-confirm').onclick=confirmTrashTask;
 input('workspace-environment').onchange=()=>{directoryEnvironment=input('workspace-environment').value;renderWorkspaceRecent();void browseDirectory(settings.config.environments.find(e=>e.id===directoryEnvironment)?.workspaces[0]||'')};
 button('directory-up').onclick=()=>void browseDirectory(directoryParent);button('directory-go').onclick=()=>void browseDirectory(input('directory-path').value.trim());input('directory-path').onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();void browseDirectory(input('directory-path').value.trim())}};
 button('workspace-cancel').onclick=()=>{directoryRequest++;element<HTMLDialogElement>('workspace-dialog').close()};button('workspace-select').onclick=selectWorkspace;
 button('presets-close').onclick=()=>element<HTMLDialogElement>('presets-dialog').close();element('preset-form').onsubmit=savePreset;button('preset-delete').onclick=deletePreset;
 button('commands-close').onclick=()=>element<HTMLDialogElement>('commands-dialog').close();button('commands-manage').onclick=()=>{element<HTMLDialogElement>('commands-dialog').close();openPresetEditor('commands')};button('trash-close').onclick=()=>element<HTMLDialogElement>('trash-dialog').close();
}
function modeForPermission(permission:'request'|'auto'){
 const wanted=permission==='request'?'read':'workspace',preferred=permission==='request'?'plan':'work';
 return workCatalog.modes.find(m=>m.id===preferred&&m.permission===wanted)||workCatalog.modes.find(m=>m.permission===wanted)||workCatalog.modes[0];
}
function setCreatePermission(permission:'request'|'auto'){
 createPermission=permission;const selected=modeForPermission(permission),mode=input('create-mode');
 if(mode&&selected)mode.value=selected.id;
 element('create-permission-options')?.querySelectorAll<HTMLButtonElement>('[data-create-permission]').forEach(b=>{const on=b.dataset.createPermission===permission;b.classList.toggle('selected',on);b.setAttribute('aria-pressed',String(on))});
 if(element('create-permission-hint'))element('create-permission-hint').textContent=permission==='request'?'先分析并请求确认':'自动处理工作目录内的改动';
}
function renderCreateFiles(){
 const target=element('create-attachment-drafts');if(!target)return;
 target.innerHTML=createFiles.map((file,index)=>`<span class="attachment-chip" title="${escapeHTML(file.name)}">${escapeHTML(file.name)} <button type="button" data-remove-create-file="${index}" aria-label="移除附件 ${escapeHTML(file.name)}">×</button></span>`).join('');
 target.querySelectorAll<HTMLButtonElement>('[data-remove-create-file]').forEach(b=>b.onclick=()=>{createFiles.splice(Number(b.dataset.removeCreateFile),1);renderCreateFiles()});
 if(element('create-status')&&!button('create-submit').dataset.starting)element('create-status').textContent=createFiles.length?'已选择 '+createFiles.length+'/5 个附件':'准备开始';
}
function addCreateFiles(files:File[]){
 if(!files.length)return;
 try{validateAttachmentFiles(files,createFiles.length);createFiles.push(...files);element('create-error').textContent='';renderCreateFiles();notify('已添加 '+files.length+' 个附件，任务创建后会上传。')}
 catch(e){element('create-error').textContent=(e as Error).message}
}
function setCreateSubmitState(state:'idle'|'starting'){
 const submit=button('create-submit');if(!submit)return;
 const starting=state==='starting';submit.dataset.starting=starting?'1':'';
 submit.disabled=starting;submit.textContent=starting?'▶':'↑';
 submit.title=starting?'正在创建并开始任务':'创建并开始任务';
 submit.setAttribute('aria-label',submit.title);
 element('create-form').setAttribute('aria-busy',String(starting));
 if(element('create-status'))element('create-status').textContent=starting?'正在创建并开始…':createFiles.length?'已选择 '+createFiles.length+'/5 个附件':'准备开始';
}
async function showCreateAt(path:string,environment:string){await showCreate();input('create-environment').value=environment;await loadCreateEnvironment();input('create-workspace').value=path}
function openWorkspacePicker(environment:string,path:string,pick:(path:string,environment:string)=>void){
 workspacePicked=pick;directoryEnvironment=environment;input('workspace-environment').innerHTML=settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');input('workspace-environment').value=environment;renderWorkspaceRecent();element<HTMLDialogElement>('workspace-dialog').showModal();void browseDirectory(path||settings.config.environments.find(e=>e.id===environment)?.workspaces[0]||'');
}
function renderWorkspaceRecent(){const env=settings.config.environments.find(e=>e.id===directoryEnvironment);const paths=[...new Set([...(env?.workspaces||[]),...tasks.filter(t=>t.environment.id===directoryEnvironment).map(t=>t.workspace)])];element('workspace-recent').innerHTML=paths.map(p=>`<button data-directory="${escapeHTML(p)}" title="${escapeHTML(p)}">${escapeHTML(p)}</button>`).join('');element('workspace-recent').querySelectorAll<HTMLElement>('[data-directory]').forEach(b=>b.onclick=()=>void browseDirectory(b.dataset.directory!))}
async function browseDirectory(path:string){
 const request=++directoryRequest,env=directoryEnvironment;button('workspace-select').disabled=true;element('directory-status').textContent='正在读取目录…';element('directory-list').innerHTML='';
 try{const r=await api<{path:string;parent:string;items:{name:string;path:string}[];truncated:boolean}>(`environments/${encodeURIComponent(env)}/directories?path=${encodeURIComponent(path)}`);if(request!==directoryRequest)return;directoryPath=r.path;directoryParent=r.parent;input('directory-path').value=r.path;button('directory-up').disabled=!r.path;element('directory-list').innerHTML=r.items.map(item=>`<button data-directory="${escapeHTML(item.path)}"><span>▱</span>${escapeHTML(item.name)}<span>›</span></button>`).join('')||'<p class="muted">没有子文件夹，可以选择当前目录。</p>';element('directory-list').querySelectorAll<HTMLElement>('[data-directory]').forEach(b=>b.onclick=()=>void browseDirectory(b.dataset.directory!));element('directory-status').textContent=r.truncated?'显示前 500 个目录，可输入完整路径。':'';button('workspace-select').disabled=!r.path}catch(e){if(request===directoryRequest)element('directory-status').textContent=(e as Error).message}
}
async function selectWorkspace(){const path=directoryPath,env=directoryEnvironment;button('workspace-select').disabled=true;try{if(input('workspace-remember').checked){await api(`environments/${env}/workspaces`,'POST',{path});settings=await api<Settings>('settings')}element<HTMLDialogElement>('workspace-dialog').close();workspacePicked?.(path,env)}catch(e){element('directory-status').textContent=(e as Error).message}finally{button('workspace-select').disabled=false}}
function renderWorkflow(){
 if(!element('message-mode'))return;
 if(detail){const control=element<HTMLSelectElement>('message-mode');if(control.dataset.task!==chosen){control.dataset.task=chosen;control.value='';modeOptions(control,detail.task.mode)}control.disabled=detail.task.archived;}
 const active=detail?.runs.some(r=>['queued','running'].includes(r.status));if(!active&&stoppingTask===chosen)stoppingTask='';button('stop').disabled=stoppingTask===chosen;button('stop').textContent=stoppingTask===chosen?'…':'■';button('stop').title=stoppingTask===chosen?'正在停止':'停止当前执行';button('stop').setAttribute('aria-label',button('stop').title);
 const files=attachmentDrafts.get(chosen)||[];button('send').textContent=sending?'▶':'↑';button('send').title=sending?'正在开始':active?'追加要求并排队':'开始执行';button('send').setAttribute('aria-label',button('send').title);button('send').disabled=sending||uploadingTasks.has(chosen)||!!detail?.task.archived||(!input('message').value.trim()&&!files.length);
 button('attach-open').disabled=!!detail?.task.archived||uploadingTasks.has(chosen);button('command-open').disabled=!!detail?.task.archived;
 const html=files.map(f=>`<span class="attachment-chip" title="${escapeHTML(f.name)}">${escapeHTML(f.name)} <button type="button" data-remove-attachment="${f.id}" aria-label="移除附件 ${escapeHTML(f.name)}">×</button></span>`).join('')+(uploadingTasks.has(chosen)?'<small>正在上传…</small>':'');const target=element('attachment-drafts');if(target.innerHTML!==html){target.innerHTML=html;target.querySelectorAll<HTMLElement>('[data-remove-attachment]').forEach(b=>b.onclick=()=>{attachmentDrafts.set(chosen,(attachmentDrafts.get(chosen)||[]).filter(f=>f.id!==b.dataset.removeAttachment));renderWorkflow()})}
}
function validateAttachmentFiles(files:File[],existing=0){if(files.length+existing>5)throw new Error('每条消息最多 5 个附件');if(files.some(f=>f.size>8*1024*1024))throw new Error('单个附件最多 8 MiB')}
async function uploadTaskFile(task:string,file:File):Promise<Attachment>{const data=new FormData();data.append('file',file);const response=await fetch(`/api/tasks/${task}/attachments`,{method:'POST',credentials:'same-origin',headers:{'X-CSRF-Token':csrf},body:data});const result=await response.json();if(!response.ok)throw new Error(result.error||'上传失败');return result}
async function addAttachments(files:File[],task=chosen){if(!task||!files.length)return;if(uploadingTasks.has(task)){notify('请等待当前附件上传完成');return}try{validateAttachmentFiles(files,(attachmentDrafts.get(task)||[]).length);uploadingTasks.add(task);renderWorkflow();for(const file of files){const uploaded=await uploadTaskFile(task,file);attachmentDrafts.set(task,[...(attachmentDrafts.get(task)||[]),uploaded]);if(task===chosen)renderWorkflow()}}catch(e){notify((e as Error).message)}finally{uploadingTasks.delete(task);if(task===chosen)renderWorkflow()}}
function selectedMessageMode(){const value=input('message-mode').value;return workCatalog.modes.some(m=>m.id===value)?value:''}
async function openPresetEditor(type:'modes'|'commands'){
 try{workCatalog=await api<WorkCatalog>('workbench');presetType=type;element('presets-title').textContent=type==='modes'?'工作模式':'快捷指令';element('presets-hint').textContent=type==='modes'?'模式决定执行方式，可添加自己的工作要求。分析规划模式不接入任务硬件工具。':'指令会填入输入框，检查后由你开始执行。';element('preset-permission-wrap').classList.toggle('hidden',type!=='modes');element('preset-content-label').textContent=type==='modes'?'要求（可留空）':'指令内容';input('preset-content').required=type==='commands';renderPresetList();editPreset('');element<HTMLDialogElement>('presets-dialog').showModal()}catch(e){notify((e as Error).message)}
}
function renderPresetList(){const items=presetType==='modes'?workCatalog.modes:workCatalog.commands;element('preset-list').innerHTML=items.map(v=>`<button type="button" data-preset="${escapeHTML(v.id)}">${escapeHTML(v.name)}</button>`).join('')+'<button type="button" data-preset="">＋ 新增</button>';element('preset-list').querySelectorAll<HTMLElement>('[data-preset]').forEach(b=>b.onclick=()=>editPreset(b.dataset.preset!))}
function editPreset(id:string){presetID=id;const item=(presetType==='modes'?workCatalog.modes:workCatalog.commands).find(m=>m.id===id),builtin=!!(item as WorkMode)?.builtin;input('preset-name').value=item?.name||'';input('preset-content').value=(item as WorkMode)?.prompt||(item as QuickCommand)?.content||'';input('preset-permission').value=(item as WorkMode)?.permission||'workspace';for(const id of ['preset-name','preset-content','preset-permission'])input(id).disabled=builtin;button('preset-save').disabled=builtin;button('preset-delete').disabled=builtin||!presetID;element('preset-error').textContent=builtin?'内置模式可直接选择；需要调整时新增一个模式。':''}
async function savePreset(e:Event){e.preventDefault();await changePreset(false)}
async function deletePreset(){if(presetID&&confirm('删除此预设？已有执行记录保留原模式。'))await changePreset(true)}
async function changePreset(remove:boolean){button('preset-save').disabled=true;try{const fresh=await api<WorkCatalog>('workbench'),id=presetID||'custom_'+Date.now().toString(36);fresh.modes=fresh.modes.filter(m=>!m.builtin);if(presetType==='modes'){fresh.modes=fresh.modes.filter(m=>m.id!==id);if(!remove)fresh.modes.push({id,name:input('preset-name').value,permission:input('preset-permission').value as WorkMode['permission'],prompt:input('preset-content').value})}else{fresh.commands=fresh.commands.filter(c=>c.id!==id);if(!remove)fresh.commands.push({id,name:input('preset-name').value,content:input('preset-content').value})}workCatalog=await api<WorkCatalog>('workbench','PUT',fresh);modeOptions(element<HTMLSelectElement>('message-mode'),detail?.task.mode);modeOptions(element<HTMLSelectElement>('create-mode'));renderPresetList();editPreset(remove?'':id);notify(remove?'预设已删除':'预设已保存')}catch(e){element('preset-error').textContent=(e as Error).message}finally{button('preset-save').disabled=false}}
function openCommands(){element('commands-list').innerHTML=workCatalog.commands.map(c=>`<button data-command="${escapeHTML(c.id)}"><strong>/${escapeHTML(c.name)}</strong><span>${escapeHTML(c.content.slice(0,100))}</span></button>`).join('')||'<p class="muted">还没有指令，点“管理指令”添加常用要求。</p>';element('commands-list').querySelectorAll<HTMLElement>('[data-command]').forEach(b=>b.onclick=()=>{const command=workCatalog.commands.find(c=>c.id===b.dataset.command);if(!command)return;if(input('message').value==='/'){input('message').value='';drafts.set(chosen,'')}bringToChat(command.content);element<HTMLDialogElement>('commands-dialog').close();renderWorkflow()});element<HTMLDialogElement>('commands-dialog').showModal()}
function trashCurrentTask(id=chosen){
 const task=tasks.find(t=>t.id===id)||(id===chosen?detail?.task:undefined);
 if(!id||!task)return;if(id===chosen&&!mayLeave())return;
 trashTaskID=id;element('trash-confirm-title').textContent=task.title;element('trash-confirm-error').textContent='';element<HTMLDialogElement>('trash-confirm-dialog').showModal();
}
async function confirmTrashTask(){const id=trashTaskID;button('trash-confirm').disabled=true;try{await api('tasks/'+id,'DELETE',{});element<HTMLDialogElement>('trash-confirm-dialog').close();tasks=await api<Task[]>('tasks');attachmentDrafts.delete(id);drafts.delete(id);if(id!==chosen){renderList();notify('已移入回收站');void loadStickyBoard();return}dirty=false;if(tasks.length){const next=tasks.find(t=>t.archived===(taskView==='archived'))||tasks[0];taskView=next.archived?'archived':'active';await choose(next.id)}else{chosen='';detail=null;resetConversation();element('conversation').innerHTML='<div class="empty"><h2>开始一个新任务</h2></div>';for(const name of ['tabs','task-actions','composer-wrap'])element(name).classList.add('hidden');element('task-title').textContent='简作';element('task-workspace').textContent='';history.replaceState(null,'','/');switchTab('chat')}renderList();notify('已移入回收站');void loadStickyBoard()}catch(e){element('trash-confirm-error').textContent=(e as Error).message}finally{button('trash-confirm').disabled=false}}
async function showTrash(){try{const items=await api<Task[]>('trash');element('trash-list').innerHTML=items.map(t=>`<div class="trash-item"><span>${escapeHTML(t.title)}</span><button data-restore="${t.id}">恢复</button></div>`).join('')||'<p class="muted">回收站为空</p>';element('trash-list').querySelectorAll<HTMLElement>('[data-restore]').forEach(b=>b.onclick=async()=>{try{await api(`tasks/${b.dataset.restore}/restore`,'POST',{});tasks=await api<Task[]>('tasks');renderList();await showTrash();notify('已恢复到已归档会话')}catch(e){notify((e as Error).message)}});if(!element<HTMLDialogElement>('trash-dialog').open)element<HTMLDialogElement>('trash-dialog').showModal()}catch(e){notify((e as Error).message)}}

function openTaskRename(id=chosen){
 const task=tasks.find(t=>t.id===id)||(id===chosen?detail?.task:undefined);
 if(!id||!task)return;renameTaskID=id;input('rename-title').value=task.title;element('rename-error').textContent='';element<HTMLDialogElement>('rename-dialog').showModal();input('rename-title').focus();input('rename-title').select();
}
async function saveTaskRename(e:Event){e.preventDefault();const id=renameTaskID;button('rename-save').disabled=true;try{await api('tasks/'+id,'PATCH',{title:input('rename-title').value.trim()});element<HTMLDialogElement>('rename-dialog').close();tasks=await api<Task[]>('tasks');if(id===chosen)await poll();renderList();void loadStickyBoard();notify('会话已重命名')}catch(e){element('rename-error').textContent=(e as Error).message}finally{button('rename-save').disabled=false}}
