type DetectedTool={path:string;state:string;label:string};
type DetectedEnvironment={environment:Environment;codex:DetectedTool;claude:DetectedTool;message:string};
let detectedEnvironments:DetectedEnvironment[]=[];
function sameDetectedEnvironment(a:Environment,b:Environment){return a.type===b.type&&(a.type==='windows'||a.distro===b.distro&&a.user===b.user)}
function installEnvironmentDiscovery(){
 const section=element('settings-environment');section.insertAdjacentHTML('afterbegin','<div class="environment-detection"><div><strong>找到这台电脑上的 AI 工具</strong><button type="button" id="detect-local-environments">检测环境</button></div><p>检测 Windows 和 WSL，可能启动已安装的 WSL。SSH 使用下方“发现局域网 SSH”。</p><p id="environment-detection-status" role="status"></p><div id="detected-environments"></div></div>');
 button('detect-local-environments').onclick=async()=>{
  const control=button('detect-local-environments');control.disabled=true;element('environment-detection-status').textContent='正在检测安装位置和本地登录配置，约需 10–25 秒…';
  try{const result=await api<{items:DetectedEnvironment[];message:string}>('environments/discover','POST',{});if(!element('detected-environments'))return;detectedEnvironments=result.items;renderDetectedEnvironments();element('environment-detection-status').textContent=result.message}
  catch(e){if(element('environment-detection-status'))element('environment-detection-status').textContent=(e as Error).message}
  finally{control.disabled=false}
 };
 const advanced=document.createElement('details');advanced.className='environment-advanced';advanced.innerHTML='<summary>高级设置 · AI 程序位置和默认模型</summary>';
 const first=element('setting-codex').previousElementSibling!,last=element('setting-workspaces').previousElementSibling!;first.before(advanced);
 let node:ChildNode|null=first;while(node&&node!==last){const next:ChildNode|null=node.nextSibling;advanced.append(node);node=next}
 // Model selection remains visible. Cache details and re-read are occasional actions.
 const extra=document.createElement('details');extra.className='create-advanced';extra.innerHTML='<summary>模型检测详情</summary>';element('reload-models').before(extra);extra.append(element('models-hint'),element('reload-models'));
 input('search').placeholder='搜索任务和记录';
}
function renderDetectedEnvironments(){
 element('detected-environments').innerHTML=detectedEnvironments.map((item,index)=>{const env=item.environment,existing=editingEnvironments.find(e=>sameDetectedEnvironment(e,env));return `<div class="detected-environment"><div><strong>${escapeHTML(env.name)}</strong><small>${escapeHTML(env.user?env.user+' · '+env.workspaces[0]:env.workspaces[0])}</small><small>Codex：${escapeHTML(item.codex.label)}<br>Claude：${escapeHTML(item.claude.label)}</small>${item.message?'<small>'+escapeHTML(item.message)+'</small>':''}</div><button type="button" data-detected="${index}">${existing?'查看配置':'添加'}</button></div>`}).join('');
 element('detected-environments').querySelectorAll<HTMLButtonElement>('[data-detected]').forEach(b=>b.onclick=()=>{
  const item=detectedEnvironments[Number(b.dataset.detected)];if(!item)return;storeEnvironmentEditor();const existing=editingEnvironments.find(e=>sameDetectedEnvironment(e,item.environment));
  if(existing)editingID=existing.id;else{if(editingEnvironments.length>=30){notify('最多配置 30 个环境');return}const env={...item.environment,id:'env_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,6),workspaces:[...item.environment.workspaces]};editingEnvironments.push(env);editingID=env.id}
  environmentPickers();loadEnvironmentEditor();renderDetectedEnvironments();input('setting-workspaces').scrollIntoView({block:'center'});notify(existing?'已打开已有配置，检测结果没有覆盖它。':'已填入环境。选好工作目录后，点击保存设置。');
 });
}
