import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
ctx.crypto=webcrypto;
run(`settings.config.environments=[{id:'win',name:'Windows fixture',type:'windows',workspaces:['C:/fixture']},{id:'remote',name:'SSH fixture',type:'ssh',host:'fixture.invalid',workspaces:['/fixture']}];editingEnvironments=JSON.parse(JSON.stringify(settings.config.environments));editingID='win';engineCatalog={engines:[{id:'codex',name:'Codex',targets:['windows','ssh'],auto_install:true,runnable:true,credential_kinds:['codex_home']},{id:'kimi',name:'Kimi',targets:['windows','ssh'],auto_install:false,runnable:false,credential_kinds:[]}],profiles:[],active_profile:{}};populateEngineOnboarding();renderEngineCatalog()`);
assert.equal(document.querySelector('[data-settings="engines"]'),null);
assert(el('settings-environment').contains(el('engine-catalog')));
assert(el('engine-environment-editor').contains(el('setting-workspaces')));
assert.equal(el('detect-local-environments'),null,'duplicate discovery action removed');assert.equal(el('detected-environments'),null,'duplicate result list removed');
assert.equal(document.querySelectorAll('[data-install-engine]').length,0,'unknown status cannot offer install');
ctx.api=async(path,method)=>{assert.equal(path,'environments/scan');assert.equal(method,'POST');return {items:[{environment_id:'win',codex:{state:'installed',path:'C:/fixture/codex.exe',label:'已安装'},kimi:{state:'missing',path:'',label:'未安装'}},{environment_id:'remote',codex:{state:'missing',path:'',label:'未安装'},kimi:{state:'unknown',path:'',label:'未完成检测'}}]}};
await ctx.refreshEngineStatus();
assert.match(el('engine-catalog').textContent,/已安装/);assert.match(el('engine-catalog').textContent,/未安装/);assert.match(el('engine-catalog').textContent,/SSH fixture/);
run(`engineDiscoveredEnvironments=[{environment:{id:'detected',name:'New WSL',type:'wsl',distro:'Debian',user:'fixture',codex:'/fixture/codex',workspaces:['/fixture']},codex:{state:'configured',path:'/fixture/codex',label:'已登录'},claude:{state:'missing',label:'未安装'},message:''}];renderEngineCatalog()`);
const saved=run('JSON.stringify(settings.config.environments)');ctx.storeEnvironmentEditor=()=>{};ctx.loadEnvironmentEditor=()=>{};el('engine-environment-editor').scrollIntoView=()=>{};
document.querySelector('[data-add-engine-environment]').onclick();assert.equal(run('editingEnvironments.length'),3);assert.equal(run('JSON.stringify(settings.config.environments)'),saved,'discovery must not save configuration');
document.querySelector('[data-add-engine-environment]').onclick();assert.equal(run('editingEnvironments.length'),3,'new host added to draft only once');assert.match(el('engine-catalog').textContent,/待保存/);
const install=document.querySelector('[data-install-engine]');assert.equal(document.querySelectorAll('[data-install-engine]').length,1);
let target;const start=ctx.startEngineOnboarding;ctx.startEngineOnboarding=(action,body)=>{assert.equal(action,'install');target=body};install.onclick();assert.equal(target.engine,'codex');assert.equal(target.environment_id,'remote');ctx.startEngineOnboarding=start;
let resolve;ctx.api=()=>new Promise(r=>resolve=r);const pending=ctx.refreshEngineStatus();run("settings.config.environments[1].host='changed.invalid'");resolve({items:[{environment_id:'remote',codex:{state:'missing'}}]});await pending;
assert.equal(document.querySelectorAll('[data-install-engine]').length,0,'late result from changed environment discarded');assert.match(el('engine-status-result').textContent,/更改/);

el('account-import-name').value='Native copy';el('account-import-environment').value='remote';
let posts=0,finish;
ctx.api=async(path,method,body)=>{
 if(path==='account-import'){posts++;assert.equal(method,'POST');assert.equal(body.source,'native');assert.equal(body.source_environment_id,'remote');assert.equal(body.environment_id,'win');return new Promise(r=>finish=r)}
 if(path==='engines')return run('engineCatalog');if(path==='engine-setup')return [];throw Error(path);
};
const importing=ctx.submitAccountImport();await ctx.submitAccountImport();assert.equal(posts,1);assert(el('account-import-fields').disabled);finish({id:'imported-fixture'});await importing;
assert.match(el('account-import-result').textContent,/已导入/);assert.equal(el('account-import-name').value,'');assert.equal(run('accountImportAttempt'),null);
el('account-import-source').value='json';el('account-import-source').onchange();assert(el('account-import-native').classList.contains('hidden'));assert(!el('account-import-json').classList.contains('hidden'));
el('account-import-name').value='JSON copy';Object.defineProperty(el('account-import-file'),'files',{configurable:true,value:[{size:42,text:async()=>'{"OPENAI_API_KEY":"synthetic-secret"}'}]});
const attempts=[];ctx.api=async(path,method,body)=>{if(path==='account-import'){attempts.push(body.id);assert.match(body.json,/synthetic-secret/);throw Error('fixture offline')}throw Error(path)};
await ctx.submitAccountImport();await ctx.submitAccountImport();assert.equal(attempts[0],attempts[1],'lost response retries use same import id');assert(!el('account-import-result').textContent.includes('synthetic-secret'));
Object.defineProperty(el('account-import-file'),'files',{configurable:true,value:[{size:256*1024+1,text:async()=>{throw Error('must not read oversized file')}}]});await ctx.submitAccountImport();assert.equal(attempts.length,2);assert.match(el('account-import-result').textContent,/256 KB/);
el('account-import-engine').value='claude';el('account-import-engine').onchange();assert(el('account-import-codex-config').classList.contains('hidden'));
console.log('PASS: unified environment engine status, exact install target, unknown/stale detection isolation, account import sources, duplicate prevention and bounded JSON. Synthetic only.');
