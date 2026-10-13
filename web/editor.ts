type EditorReference={path:string;line:number;column:number};
type EditorTarget={uri:string;command:string;project:string;environment:string;kind:string;notice:string;confirmation:string;sshConfig:string};
let editorRequest=0,editorTarget:EditorTarget|null=null;

// Links are opened by the browser's computer, never by the Duo service.
function editorEncoded(value:string){return encodeURIComponent(value).replace(/[!'()*]/g,c=>'%'+c.charCodeAt(0).toString(16).toUpperCase())}
function editorPath(value:string){return value.split('/').map(editorEncoded).join('/')}
function editorCleanRelative(value:string):string{
 if(!value||value.length>4096||/[\\:\x00-\x1f\x7f]/.test(value)||value.startsWith('/'))throw Error('请选择任务工作目录内的文件。');
 const parts=value.split('/');if(parts.some(p=>!p||p==='.'||p==='..'||p.toLowerCase()==='.git'||/[. ]$/.test(p)))throw Error('不能打开工作目录外或 Git 内部文件。');
 return parts.join('/');
}
function editorRoot(task:Task):string{
 const kind=task.environment?.type,root=(task.workspace||'').replace(/\\/g,'/');
 if(!root||root.length>4096||/[\x00-\x1f\x7f]/.test(root)||!['windows','wsl','ssh'].includes(kind))throw Error('任务没有可用的工作目录或执行环境。');
 if(kind==='windows'? !/^[a-z]:\//i.test(root):!root.startsWith('/')||root.startsWith('//'))throw Error('当前目录暂不支持 VS Code 联动；请使用盘符绝对路径或 Linux 绝对路径。');
 if(root.split('/').some(p=>p==='.'||p==='..'))throw Error('工作目录包含无效路径。');
 return root.replace(/\/+$/,'')||'/';
}
function editorReference(value:string,task:Task):EditorReference|null{
 try{
  let file=value.trim(),line=1,column=1;
  const position=file.match(/(?::([1-9][0-9]*)(?::([1-9][0-9]*))?|#L([1-9][0-9]*))$/);
  if(position){line=Number(position[1]||position[3]);column=Number(position[2]||1);file=file.slice(0,position.index)}
  if(!Number.isSafeInteger(line)||!Number.isSafeInteger(column)||line>10000000||column>1000000)return null;
  const root=editorRoot(task),normalized=file.replace(/\\/g,'/'),prefix=root==='/'?'/':root+'/';
  if(normalized.startsWith('/')||/^[a-z]:\//i.test(normalized)){
   const matches=task.environment.type==='windows'?normalized.toLowerCase().startsWith(prefix.toLowerCase()):normalized.startsWith(prefix);
   if(!matches)return null;file=normalized.slice(prefix.length);
  }else file=normalized.replace(/^\.\//,'');
  if(!/\.(?:[cm]?[jt]sx?|go|py|rs|c|cc|cpp|h|hpp|cs|java|kt|rb|php|sh|ps1|html?|css|scss|json|toml|ya?ml|md|txt|sql|vue|svelte|xml)$/i.test(file))return null;
  return {path:editorCleanRelative(file),line,column};
 }catch{return null}
}
function editorReferenceHTML(escaped:string,label?:string):string|null{
 if(!detail?.task)return null;
 const entities:Record<string,string>={amp:'&',lt:'<',gt:'>',quot:'"','#39':"'"};
 const text=escaped.replace(/&(amp|lt|gt|quot|#39);/g,(_,name:string)=>entities[name]||'');
 const reference=editorReference(text,detail.task);if(!reference)return null;
 return `<button type="button" class="editor-file-reference" data-editor-path="${escapeHTML(reference.path)}" data-editor-line="${reference.line}" data-editor-column="${reference.column}" title="在 VS Code 中打开">${label||escaped}</button>`;
}
function buildEditorTarget(task:Task,reference?:EditorReference,sshAlias=''):EditorTarget{
 const root=editorRoot(task),env=task.environment,kind=env.type;
 if(reference){editorCleanRelative(reference.path);if(!Number.isSafeInteger(reference.line)||!Number.isSafeInteger(reference.column)||reference.line<1||reference.column<1||reference.line>10000000||reference.column>1000000)throw Error('文件行号无效。')}
 const target=reference?(root==='/'?'':root)+'/'+reference.path:root;
 const suffix=reference?':'+reference.line+':'+reference.column:'/',encoded=editorPath(target);
 let uri='',cliURI='',sshConfig='',notice='',confirmation='';
 if(kind==='windows'){
  const local=encoded.replace(/^([a-z])%3A\//i,'$1:/');
  uri='vscode://file/'+local+suffix;cliURI='file:///'+local+suffix;
  notice='打开当前访问设备上的 VS Code。Windows 路径属于 Duo 服务电脑；在另一台电脑访问时，请改用匹配的 SSH 环境。';
  confirmation='当前设备就是该 Windows 项目所在电脑';
 }else{
  let authority='';
  if(kind==='wsl'){
   if(!env.distro||/[\/\\\x00-\x1f\x7f]/.test(env.distro))throw Error('任务缺少有效的 WSL 发行版。');
   authority='wsl+'+env.distro;
   notice='需要本机 VS Code 的 WSL 扩展。它连接当前设备上的该发行版，并使用发行版默认用户'+(env.user?'；任务用户为 '+env.user:'')+'。若用户不同，请先核对 WSL 默认用户；Duo 不会更改它。';
   confirmation='该发行版在当前设备上，默认用户与任务用户一致';
  }else{
   if(!/^[a-z0-9][a-z0-9_.-]{0,79}$/i.test(sshAlias))throw Error('请填写本机 SSH 配置中的 Host 别名（字母、数字、点、横线或下划线）。');
   if(!env.host||!env.user||!Number.isInteger(env.port||22)||(env.port||22)<1||(env.port||22)>65535||[env.host,env.user].some(v=>/[\s"'\\#%\x00-\x1f\x7f]/.test(v)||v.startsWith('-')))throw Error('任务 SSH 主机、用户或端口无效。');
   authority='ssh-remote+'+sshAlias;
   sshConfig=`Host ${sshAlias}\n    HostName ${env.host}\n    User ${env.user}\n    Port ${env.port||22}\n`;
   notice='需要本机 VS Code 的 Remote - SSH 扩展。将下面的配置添加到本机 SSH 配置，或填写已有的匹配别名；私钥、代理和跳板配置请在本机自行配置。Duo 不复制凭据、不修改 SSH 配置。';
   confirmation='本机该 Host 别名的主机、用户、端口与任务一致';
  }
  uri='vscode://vscode-remote/'+editorEncoded(authority)+encoded+suffix;
  cliURI='vscode-remote://'+editorEncoded(authority)+encoded+suffix;
 }
 return {uri,command:`code ${reference?'--goto --file-uri':'--folder-uri'} '${cliURI}'`,project:target+(reference?':'+reference.line+':'+reference.column:''),environment:[env.name,kind,kind==='wsl'?env.distro:env.host,env.user].filter(Boolean).join(' · '),kind,notice,confirmation,sshConfig};
}
function installEditor(){
 element('root').insertAdjacentHTML('beforeend',`<dialog id="editor-dialog" class="editor-dialog" aria-labelledby="editor-title"><h2 id="editor-title">在 VS Code 中打开</h2><p>窗口显示在当前设备，文件仍保存在任务执行环境中。</p><p id="editor-environment"></p><code id="editor-project"></code><p id="editor-notice"></p><div id="editor-ssh" class="hidden"><label for="editor-alias">本机 SSH Host 别名</label><input id="editor-alias" maxlength="80" autocomplete="off"><pre id="editor-ssh-config"></pre><button type="button" id="editor-copy-ssh">复制 SSH 配置</button></div><label class="check-row"><input type="checkbox" id="editor-confirm"><span id="editor-confirm-label"></span></label><p class="error" id="editor-error" role="status"></p><details><summary>浏览器没有打开 VS Code？</summary><p>需安装 VS Code，并允许浏览器打开外部应用。也可复制命令到当前设备的终端运行。</p><pre id="editor-command"></pre><button type="button" id="editor-copy-command">复制打开命令</button></details><div class="dialog-footer"><button type="button" id="editor-close">关闭</button><a id="editor-launch" class="editor-launch" aria-disabled="true">打开 VS Code</a></div></dialog>`);
 button('editor-close').onclick=()=>element<HTMLDialogElement>('editor-dialog').close();
 element('editor-dialog').addEventListener('close',()=>{editorRequest++;editorTarget=null});
 input('editor-confirm').onchange=()=>renderEditorLaunch();
 button('editor-copy-command').onclick=()=>void copyEditorText(editorTarget?.command||'');
 button('editor-copy-ssh').onclick=()=>void copyEditorText(editorTarget?.sshConfig||'');
 element('editor-launch').onclick=e=>{
  if(!editorTarget||!input('editor-confirm').checked){e.preventDefault();return}
  // Browsers cannot confirm whether an external application actually opened.
  element('editor-error').textContent='已请求打开 VS Code；若没有响应，请允许浏览器打开外部应用，或使用下方命令。';
 };
 listenWithShell(element('conversation'),'click',e=>{
  const node=(e.target as HTMLElement).closest<HTMLElement>('[data-editor-path]');if(!node||!chosen)return;
  void openEditor(chosen,{path:node.dataset.editorPath||'',line:Number(node.dataset.editorLine||1),column:Number(node.dataset.editorColumn||1)});
 });
 disposeWithShell(()=>{editorRequest++;editorTarget=null});
}
function renderEditorLaunch(){
 const link=element<HTMLAnchorElement>('editor-launch');link.removeAttribute('href');
 const ready=!!editorTarget&&input('editor-confirm').checked;link.setAttribute('aria-disabled',String(!ready));if(ready)link.href=editorTarget!.uri;
}
async function copyEditorText(text:string){
 if(!text)return;const epoch=shellEpoch;
 try{await navigator.clipboard.writeText(text);if(shellCurrent(epoch))notify('已复制。')}catch{if(shellCurrent(epoch)){element('editor-error').textContent='浏览器未允许复制，请选择下方命令或配置手动复制。'}}
}
async function openEditor(id=chosen,reference?:EditorReference){
 if(!id)return;const request=++editorRequest,epoch=shellEpoch,dialog=element<HTMLDialogElement>('editor-dialog');
 editorTarget=null;input('editor-confirm').checked=false;input('editor-alias').oninput=null;
 for(const name of ['editor-project','editor-environment','editor-notice','editor-confirm-label','editor-command','editor-ssh-config'])element(name).textContent='';
 element('editor-ssh').classList.add('hidden');element('editor-error').textContent='正在读取任务目录…';renderEditorLaunch();if(!dialog.open)dialog.showModal();
 try{
  const result=await api<Detail>('tasks/'+encodeURIComponent(id)+'?recent=1','GET',undefined,shellController.signal);
  if(request!==editorRequest||!shellCurrent(epoch)||!dialog.open)return;
  // Validate references against the saved task, not a stale task-list snapshot.
  if(reference)await api<FileResult>('tasks/'+encodeURIComponent(id)+'/files?action=read&path='+encodeURIComponent(editorCleanRelative(reference.path)),'GET',undefined,shellController.signal);
  if(request!==editorRequest||!shellCurrent(epoch)||!dialog.open)return;
  input('editor-alias').value='duo-'+(result.task.environment.id||'remote').replace(/[^a-z0-9_-]/gi,'').slice(0,40);
  const update=()=>{
   if(request!==editorRequest||!shellCurrent(epoch))return;
   editorTarget=null;input('editor-confirm').checked=false;
   try{editorTarget=buildEditorTarget(result.task,reference,input('editor-alias').value.trim());element('editor-error').textContent='';
    element('editor-project').textContent=editorTarget.project;element('editor-environment').textContent=editorTarget.environment;element('editor-notice').textContent=editorTarget.notice;element('editor-confirm-label').textContent=editorTarget.confirmation;element('editor-command').textContent=editorTarget.command;element('editor-ssh-config').textContent=editorTarget.sshConfig;
   }catch(e){element('editor-command').textContent='';element('editor-ssh-config').textContent='';element('editor-error').textContent=(e as Error).message}
   renderEditorLaunch();
  };
  element('editor-ssh').classList.toggle('hidden',result.task.environment.type!=='ssh');input('editor-alias').oninput=update;update();
 }catch(e){if(request===editorRequest&&shellCurrent(epoch)){editorTarget=null;element('editor-error').textContent=(e as Error).message;renderEditorLaunch()}}
}
