import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
ctx.crypto=webcrypto;
run(`settings.config.environments=[{id:'win',name:'Fixture',type:'windows',codex:'old.exe',workspaces:['C:/fixture']}];editingEnvironments=JSON.parse(JSON.stringify(settings.config.environments));editingID='win';engineCatalog={engines:[{id:'codex',name:'Codex',targets:['windows'],auto_install:true,runnable:true,credential_kinds:[]}],profiles:[],active_profile:{}};engineStatusSignature=engineEnvironmentSignature();engineEnvironmentStatuses=[{environment_id:'win',codex:{path:'C:/resolved/old.exe',state:'installed',label:'已安装',version:'1.2.3'}}];renderEngineCatalog()`);
assert.match(el('engine-catalog').textContent,/v1\.2\.3/);
assert.equal(document.querySelectorAll('[data-install-engine]').length,0);
const update=document.querySelector('[data-update-engine]');assert(update);
let posts=0,finish,request,finishSettings;
const fresh=JSON.parse(run('JSON.stringify(settings)'));
fresh.config.environments[0].codex='new.exe';fresh.config.environments[0].managed_tools={codex:{path:'new.exe',version:'1.2.4',previous_paths:['old.exe']}};
ctx.loadEnvironmentEditor();
let scans=0;ctx.refreshEngineStatus=async()=>{scans++};
ctx.api=async(path,method,body)=>{
 if(method==='POST'){posts++;request=body;assert.equal(body.action,'update');assert.equal(body.expected_path,'old.exe');assert.equal(body.environment_id,'win');assert.equal(body.engine,'codex');return new Promise(resolve=>finish=resolve)}
 if(path==='engine-setup/'+request.id)return {...request,state:'done',message:'updated',version:'1.2.4'};
 if(path==='settings')return new Promise(resolve=>finishSettings=resolve);
 if(path==='engines')return run('engineCatalog');
 if(path==='engine-setup')return [];
 throw Error(path);
};
update.onclick();await ctx.startEngineOnboarding('update',{engine:'codex',environment_id:'win'});
assert.equal(posts,1);assert(el('engine-setup-fields').disabled);
el('environment-name').value='Keep unsaved draft';
finish({...request,state:'running',message:'installing'});
for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));
assert(finishSettings);assert(run('engineSetupBusy'),'keep update locked while refreshed settings are in flight');
await ctx.saveSettings({preventDefault(){}});assert.equal(posts,1,'saving a stale form cannot overwrite the installed path');assert.match(el('settings-error').textContent,/稍后保存/);
finishSettings(fresh);
for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(run('settings.config.environments[0].codex'),'new.exe');
assert.equal(run('editingEnvironments[0].managed_tools.codex.version'),'1.2.4');
assert.equal(run('editingEnvironments[0].name'),'Keep unsaved draft');
assert.equal(run('settings.config.environments[0].managed_tools.codex.previous_paths[0]'),'old.exe');
assert.equal(scans,1);
run(`renderEngineSetup({id:'cancel-update',action:'update',engine:'codex',environment_id:'win',state:'running',message:'installing'})`);
assert(!el('engine-setup-cancel').classList.contains('hidden'));
ctx.api=async(path,method)=>{assert.equal(path,'engine-setup/cancel-update');assert.equal(method,'DELETE')};await el('engine-setup-cancel').onclick();assert.match(el('engine-setup-status').textContent,/取消/);
run(`renderEngineSetup({id:'cancel-update',action:'update',engine:'codex',environment_id:'win',state:'failed',message:'canceled'});engineStatusSignature=engineEnvironmentSignature()`);
const attempts=[];ctx.api=async(path,method,body)=>{attempts.push(body.id);throw Error('fixture offline')};
await ctx.startEngineOnboarding('update',{engine:'codex',environment_id:'win'});await ctx.startEngineOnboarding('update',{engine:'codex',environment_id:'win'});
assert.equal(attempts.length,2);assert.equal(attempts[0],attempts[1]);assert(!el('engine-setup-fields').disabled);
run(`settings.config.environments[0].codex='manual.exe'`);
await ctx.startEngineOnboarding('update',{engine:'codex',environment_id:'win'});assert.equal(attempts.length,2,'stale detection must block update');

// Updating a remote Harness retains its API route, image model list, native
// account references, task/session, and unrelated unsaved settings.
run(`settings.config.environments=[{id:'remote',name:'SSH fixture',type:'ssh',host:'fixture.invalid',user:'fixture',port:2222,identity:'/fixture/key',codex:'codex',claude:'claude',harness:'/fixture/old-dsh',harness_provider:'duo-api',harness_model:'vision-fixture',models:[{id:'vision-fixture',name:'Vision fixture',engine:'deepseek-harness'}],workspaces:['/fixture']}];editingEnvironments=JSON.parse(JSON.stringify(settings.config.environments));editingID='remote';chosen='keep-task';engineSetupAttempt=null;engineCatalog={engines:[{id:'deepseek-harness',name:'Harness',targets:['ssh'],auto_install:true,runnable:true,credential_kinds:['native','dsh_home']}],profiles:[{id:'native-fixture',environment_id:'remote',engine:'deepseek-harness',kind:'dsh_home',reference:'/fixture/dsh-home'}],active_profile:{'remote:deepseek-harness':'native-fixture'}};engineStatusSignature=engineEnvironmentSignature();engineEnvironmentStatuses=[{environment_id:'remote',harness:{path:'/fixture/old-dsh',state:'installed',label:'已安装',version:'0.2.0-rc.2'}}];loadEnvironmentEditor();renderEngineCatalog()`);
assert(document.querySelector('[data-harness-api="remote"]'));assert(document.querySelector('[data-update-engine="deepseek-harness"]'));assert.match(el('engine-catalog').textContent,/0\.2\.0-rc\.2/);
const accounts=run('JSON.stringify(engineCatalog.profiles)'),route=run('JSON.stringify(settings.config.environments[0].models)');
let remote=JSON.parse(run('JSON.stringify(settings)'));
remote.config.environments[0].harness='/fixture/new-dsh';remote.config.environments[0].managed_tools={'deepseek-harness':{path:'/fixture/new-dsh',version:'0.2.1',previous_paths:['/fixture/old-dsh']}};
posts=0;let remoteRequest;
ctx.api=async(path,method,body)=>{
 if(path==='engine-setup'&&method==='POST'){posts++;remoteRequest=body;assert.equal(body.engine,'deepseek-harness');assert.equal(body.environment_id,'remote');assert.equal(body.expected_path,'/fixture/old-dsh');assert.equal(body.api_key,undefined);return {...body,state:'running',message:'installing'}}
 if(path==='engine-setup/'+remoteRequest?.id)return {...remoteRequest,state:'done',message:'updated'};
 if(path==='settings')return remote;
 if(path==='engines')return run('engineCatalog');
 if(path==='engine-setup')return [];
 throw Error(path);
};
run('harnessAPIBusy=true');await ctx.startEngineOnboarding('update',{engine:'deepseek-harness',environment_id:'remote'});assert.equal(posts,0,'API save/discovery cannot overlap an update');run('harnessAPIBusy=false;accountSyncBusy=true');await ctx.startEngineOnboarding('update',{engine:'deepseek-harness',environment_id:'remote'});assert.equal(posts,0,'native account copy cannot overlap an update');run('accountSyncBusy=false');
el('setting-model').value='keep-unsaved-model';
await ctx.startEngineOnboarding('update',{engine:'deepseek-harness',environment_id:'remote'});
for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(posts,1);assert.equal(run('settings.config.environments[0].harness'),'/fixture/new-dsh');assert.equal(run('editingEnvironments[0].managed_tools["deepseek-harness"].version'),'0.2.1');
assert.equal(run('settings.config.environments[0].harness_provider'),'duo-api');assert.equal(run('editingEnvironments[0].harness_model'),'vision-fixture');assert.equal(run('JSON.stringify(settings.config.environments[0].models)'),route);assert.equal(run('JSON.stringify(engineCatalog.profiles)'),accounts);assert.equal(run('chosen'),'keep-task');assert.equal(run('editingEnvironments[0].model'),'keep-unsaved-model');
// A manually edited executable in the form is not replaced by the async
// update result, while the saved environment still reflects the installation.
run(`engineStatusSignature=engineEnvironmentSignature();engineSetupAttempt=null`);
el('setting-harness').value='/fixture/manual-dsh';remote=JSON.parse(JSON.stringify(remote));remote.config.environments[0].harness='/fixture/newer-dsh';remote.config.environments[0].managed_tools['deepseek-harness']={path:'/fixture/newer-dsh',version:'0.2.2',previous_paths:['/fixture/old-dsh','/fixture/new-dsh']};
ctx.api=async(path,method,body)=>{
 if(path==='engine-setup'&&method==='POST'){remoteRequest=body;assert.equal(body.expected_path,'/fixture/new-dsh');return {...body,state:'running',message:'installing'}}
 if(path==='engine-setup/'+remoteRequest.id)return {...remoteRequest,state:'done',message:'updated'};
 if(path==='settings')return remote;
 if(path==='engines')return run('engineCatalog');if(path==='engine-setup')return [];throw Error(path);
};
await ctx.startEngineOnboarding('update',{engine:'deepseek-harness',environment_id:'remote'});for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(run('settings.config.environments[0].harness'),'/fixture/newer-dsh');assert.equal(run('editingEnvironments[0].harness'),'/fixture/manual-dsh');assert.equal(el('setting-harness').value,'/fixture/manual-dsh');assert.equal(run('editingEnvironments[0].managed_tools["deepseek-harness"].version'),'0.2.2');
console.log('PASS: CLI update/version entry, duplicate/stale/settings-write guards, remote Harness API/model/account draft retention, cancel and idempotent retry. Synthetic only.');
