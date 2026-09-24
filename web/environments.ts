type DetectedTool={path:string;state:string;label:string};
type DetectedEnvironment={environment:Environment;codex:DetectedTool;claude:DetectedTool;harness?:DetectedTool;kimi?:DetectedTool;mimo?:DetectedTool;message:string};
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
 input('search').placeholder='搜索任务和记录';
 const modelManager=document.createElement('section');modelManager.className='model-manager';modelManager.innerHTML='<div class="model-manager-head"><div><h3>模型目录</h3><p>为当前环境的指定引擎补充模型 ID。新建任务会自动读取目标 CLI；这里添加的名称不代表已验证可用，实际调用仍由引擎和 provider 决定。</p></div><span class="model-manager-badge">本地配置</span></div><div id="configured-models" class="configured-models"></div><label for="custom-model-engine">模型所属引擎</label><select id="custom-model-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option><option value="deepseek-harness">DeepSeek Harness</option><option value="">所有引擎（兼容共享配置）</option></select><div class="model-add-row"><input id="custom-model-id" aria-label="模型 ID" maxlength="120" placeholder="模型 ID，例如 deepseek-flash"><input id="custom-model-name" aria-label="模型显示名称" maxlength="120" placeholder="显示名称（可选）"><button type="button" id="custom-model-add" class="primary">添加模型</button></div><p id="custom-model-result" class="settings-result" role="status"></p>';
 section.append(modelManager);
 button('custom-model-add').onclick=()=>{
  const id=input('custom-model-id').value.trim(),name=input('custom-model-name').value.trim()||id,engine=input('custom-model-engine').value;
  if(!id){element('custom-model-result').textContent='请输入模型 ID';input('custom-model-id').focus();return}
  if(!/^[^\s\r\n]{1,120}$/.test(id)){element('custom-model-result').textContent='模型 ID 不能包含空格或换行';return}
  const env=editingEnvironments.find(e=>e.id===editingID);if(!env)return;env.models=env.models||[];
  if(env.models.some(m=>m.id===id&&(m.engine||'')===engine)){element('custom-model-result').textContent='这个引擎的模型已经添加';return}
  env.models.push({id,name,engine});input('custom-model-id').value='';input('custom-model-name').value='';element('custom-model-result').textContent='已添加，保存设置后生效';renderConfiguredModels();
 };
 renderConfiguredModels();
}
function renderConfiguredModels(){
 const target=element('configured-models');if(!target)return;const env=editingEnvironments?.find(e=>e.id===editingID);const models=env?.models||[];
 target.innerHTML=models.map((m,i)=>`<div class="configured-model"><div><strong>${escapeHTML(m.id)}</strong><small>${escapeHTML(m.name||m.id)} · ${m.engine?escapeHTML(taskEngineName(m.engine)):'所有引擎'}</small></div><button type="button" data-remove-custom-model="${i}" title="删除此模型">删除</button></div>`).join('')||'<p class="muted">还没有自定义模型。发现的 CLI 模型仍会自动显示。</p>';
 target.querySelectorAll<HTMLButtonElement>('[data-remove-custom-model]').forEach(b=>b.onclick=()=>{const e=editingEnvironments.find(x=>x.id===editingID);if(!e?.models)return;e.models.splice(Number(b.dataset.removeCustomModel),1);renderConfiguredModels();element('custom-model-result').textContent='已移除，保存设置后生效'});
}
function renderDetectedEnvironments(){
 const toolLabel=(name:string,tool?:DetectedTool)=>`<span class="detected-tool detected-tool-${escapeHTML(tool?.state||'missing')}" title="${escapeHTML(tool?.path||'')}">${escapeHTML(name)}：${escapeHTML(tool?.label||'未发现安装')}</span>`;
 element('detected-environments').innerHTML=detectedEnvironments.map((item,index)=>{const env=item.environment,existing=editingEnvironments.find(e=>sameDetectedEnvironment(e,env));return `<div class="detected-environment"><div><strong>${escapeHTML(env.name)}</strong><small>${escapeHTML(env.user?env.user+' · '+env.workspaces[0]:env.workspaces[0])}</small><div class="detected-tools">${toolLabel('Codex',item.codex)} ${toolLabel('Claude Code',item.claude)} ${toolLabel('DeepSeek Harness',item.harness)} ${toolLabel('Kimi',item.kimi)} ${toolLabel('MiMo',item.mimo)}</div>${item.message?'<small>'+escapeHTML(item.message)+'</small>':''}</div><button type="button" data-detected="${index}">${existing?'查看配置':'添加'}</button></div>`}).join('');
 element('detected-environments').querySelectorAll<HTMLButtonElement>('[data-detected]').forEach(b=>b.onclick=()=>{
  const item=detectedEnvironments[Number(b.dataset.detected)];if(!item)return;storeEnvironmentEditor();const existing=editingEnvironments.find(e=>sameDetectedEnvironment(e,item.environment));
  if(existing)editingID=existing.id;else{if(editingEnvironments.length>=30){notify('最多配置 30 个环境');return}const env={...item.environment,id:'env_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,6),workspaces:[...item.environment.workspaces]};editingEnvironments.push(env);editingID=env.id}
  environmentPickers();loadEnvironmentEditor();renderDetectedEnvironments();input('setting-workspaces').scrollIntoView({block:'center'});notify(existing?'已打开已有配置，检测结果没有覆盖它。':'已填入环境。选好工作目录后，点击保存设置。');
 });
}
