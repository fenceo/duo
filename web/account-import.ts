let accountImportBusy=false,accountImportAttempt:{signature:string;id:string}|null=null;
function installAccountImport(){
 accountImportBusy=false;accountImportAttempt=null;
 element('account-center-editor').insertAdjacentHTML('afterbegin',`<details class="account-import"><summary>导入账号</summary><p>复制到 Duo 的独立账号目录，导入后可切换或同步到其他环境。导入不会验证登录是否过期。</p><fieldset id="account-import-fields"><label for="account-import-name">账号名称</label><input id="account-import-name" maxlength="60" autocomplete="off"><label for="account-import-engine">引擎</label><select id="account-import-engine"><option value="codex">Codex</option><option value="claude">Claude Code</option></select><label for="account-import-destination">保存到本机 Windows 环境</label><select id="account-import-destination"></select><label for="account-import-source">导入方式</label><select id="account-import-source"><option value="native">读取环境已有账号</option><option value="json">选择 JSON 文件</option></select><div id="account-import-native"><label for="account-import-environment">来源环境</label><select id="account-import-environment"></select><p class="muted">读取所选 Windows、WSL 或 SSH 用户的默认账号文件。“本机”是运行 Duo 的电脑；源文件保持不变。仅保存在系统钥匙串或进程环境变量中的凭据无法导入。</p></div><div id="account-import-json" class="hidden"><label for="account-import-file">账号 JSON</label><input id="account-import-file" type="file" accept=".json,application/json"><p class="muted">Codex：auth.json。Claude：.credentials.json，或含 API / 中转站配置的 settings.json。暂不支持第三方管理器的批量导出。</p><div id="account-import-codex-config"><label for="account-import-config">Codex config.toml（中转站账号请同时选择）</label><input id="account-import-config" type="file" accept=".toml"><p class="muted">auth.json 不包含 API 地址；独立导入时使用官方服务。只保留账号、服务地址和模型配置。</p></div></div><button type="button" id="account-import-submit" class="primary">导入为新账号</button></fieldset><p id="account-import-result" role="status" aria-live="polite"></p></details>`);
 const refresh=()=>{element('account-import-native').classList.toggle('hidden',input('account-import-source').value!=='native');element('account-import-json').classList.toggle('hidden',input('account-import-source').value!=='json');element('account-import-codex-config').classList.toggle('hidden',input('account-import-engine').value!=='codex')};
 element('account-import-source').onchange=refresh;element('account-import-engine').onchange=refresh;refresh();
 button('account-import-submit').onclick=()=>void submitAccountImport();
}
function populateAccountImport(){
 for(const [id,envs] of [['account-import-destination',settings.config.environments.filter(e=>e.type==='windows')],['account-import-environment',settings.config.environments]] as const){
  const select=element<HTMLSelectElement>(id),previous=select.value;select.innerHTML=envs.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)} · ${escapeHTML(e.type)}</option>`).join('');if(envs.some(e=>e.id===previous))select.value=previous;
  else if(id==='account-import-environment'){const windows=envs.find(e=>e.type==='windows');if(windows)select.value=windows.id}
 }
 element<HTMLFieldSetElement>('account-import-fields').disabled=accountImportBusy||engineSetupBusy||!input('account-import-destination').value;
}
async function submitAccountImport(){
 if(accountImportBusy||engineSetupBusy)return;
 const epoch=shellEpoch,result=element('account-import-result');
 const body={name:input('account-import-name').value.trim(),engine:input('account-import-engine').value,environment_id:input('account-import-destination').value,source:input('account-import-source').value,source_environment_id:input('account-import-environment').value,json:'',config_toml:''};
 if(!body.name){result.textContent='请填写账号名称。';return}
 accountImportBusy=true;element<HTMLFieldSetElement>('account-import-fields').disabled=true;result.textContent='正在读取并导入账号…';
 try{
  if(body.source==='json'){
   const file=input('account-import-file').files?.[0],config=body.engine==='codex'?input('account-import-config').files?.[0]:undefined;
   if(!file)throw Error('请选择账号 JSON 文件。');
   if(file.size>256*1024||(config?.size||0)>256*1024)throw Error('每个账号文件最多 256 KB。');
   body.json=await file.text();if(config)body.config_toml=await config.text();body.source_environment_id='';
  }
  if(!shellCurrent(epoch))return;
  const signature=JSON.stringify(body);if(accountImportAttempt?.signature!==signature)accountImportAttempt={signature,id:Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('')};
  await api<EngineProfile>('account-import','POST',{...body,id:accountImportAttempt!.id});if(!shellCurrent(epoch))return;
  accountImportAttempt=null;input('account-import-file').value='';input('account-import-config').value='';input('account-import-name').value='';
  result.textContent='已导入独立账号。可设为新任务默认或同步到环境；尚未验证登录有效性。';invalidateModelCatalogs();await loadEngineSettings();
 }catch(e){if(shellCurrent(epoch))result.textContent=(e as Error).message}
 finally{if(shellCurrent(epoch)){accountImportBusy=false;populateAccountImport()}}
}
