import {readFile,readdir} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const app=await readFile(new URL('../web/app.ts',import.meta.url),'utf8');
const tools=await readFile(new URL('../web/tools.ts',import.meta.url),'utf8');
const engines=await readFile(new URL('../web/engines.ts',import.meta.url),'utf8');
function extract(text,start,end){
 const from=text.indexOf(start),to=text.indexOf(end,from+start.length);
 assert(from>=0&&to>from,'Missing lifecycle test boundary: '+start);
 return text.slice(from,to);
}
const source=[
 extract(app,'// A mounted login/workbench shell','function notify('),
 extract(app,'async function api<','function markdown('),
 extract(app,'async function boot(','function renderShell('),
 extract(app,'async function poll(){','async function showCreate('),
 extract(app,'async function openSettings(','function environmentPickers('),
 extract(app,'async function saveSettings(','async function openBinding('),
 extract(engines,'async function loadEngineSettings(','function populateEngineProfileForm('),
 extract(tools,'function showSettingsSection(','function loadAccessSettings('),
].join('\n');
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
const configuration={environments:[{id:'saved'}],default_environment:'saved',feishu:{enabled:false,app_id:'',owner:'owner'}};
function fixture(){
 const nodes=new Map(),notices=[],counts={login:0,render:0,status:0,engine:0,profiles:0};
 const node=id=>{
  if(!nodes.has(id))nodes.set(id,{id,value:'',textContent:'',disabled:false,checked:false,open:true,shown:0,showModal(){this.shown++},classList:{toggle(){}}});
  return nodes.get(id);
 };
 const ctx=createContext({
  AbortController,URLSearchParams,Date,console,selection:0,modelRequest:0,taskModelRequest:0,modelTestRequest:0,
  polling:false,settingsPolling:false,createSubmitting:false,sending:false,sessionResetTask:'',csrf:'synthetic-token',
  authenticated:true,chosen:'',refreshList:0,sequence:0,codexApprovalRevision:0,
  settings:{config:configuration,secret_configured:false,feishu_status:'before',chat:''},tasks:[],workCatalog:{},editingEnvironments:[],editingID:'',
  engineCatalog:null,engineSettingsRequest:0,document:{hidden:false,querySelector(){return null}},location:{search:''},
  element:node,button:node,input:node,notify:message=>notices.push(message),
  renderList(){counts.render++},renderTask(){counts.render++},renderShell(){counts.render++;ctx.renewShellScope()},
  showLogin(){counts.login++;ctx.renewShellScope()},choose:async()=>{},
  loadAccessSettings(){},environmentPickers(){},loadEnvironmentEditor(){},storeEnvironmentEditor(){},
  updateFeishuStatus(){counts.status++},renderEngineCatalog(){counts.engine++},populateEngineProfileForm(){counts.profiles++},
  storeKnowledge(){},appendEvents(){},
  fetch:async()=>{throw new Error('No unmocked HTTP requests permitted')},
 });
 runInContext(stripTypeScriptTypes(source,{mode:'transform'}),ctx,{filename:'shell-lifecycle-under-test.js'});
 return {ctx,node,notices,counts,value:expression=>runInContext(expression,ctx)};
}
{
 const f=fixture(),target=new EventTarget();let oldClicks=0,newClicks=0,cleaned=0;
 f.ctx.listenWithShell(target,'click',()=>oldClicks++);
 f.ctx.disposeWithShell(()=>cleaned++);
 const oldSignal=f.value('shellController.signal');
 f.ctx.renewShellScope();
 assert.equal(oldSignal.aborted,true);assert.equal(cleaned,1);
 f.ctx.listenWithShell(target,'click',()=>newClicks++);
 target.dispatchEvent(new Event('click'));
 assert.equal(oldClicks,0,'old login/workbench listener must be removed');
 assert.equal(newClicks,1,'a repeated login must have one active handler');
 f.ctx.renewShellScope();target.dispatchEvent(new Event('click'));
 assert.equal(newClicks,1);assert.equal(cleaned,1,'dispose callbacks must run once');
}
{
 const f=fixture(),gate=deferred();
 f.ctx.fetch=()=>gate.promise;
 const pending=f.ctx.api('tasks');
 f.ctx.renewShellScope();f.ctx.authenticated=true;
 gate.resolve({ok:false,status:401,json:async()=>({error:'expired old login'})});
 await assert.rejects(pending,error=>error.status===401);
 assert.equal(f.counts.login,0,'late 401 must not log out the new shell');
 assert.equal(f.ctx.authenticated,true);
}
for(const status of [401,403,409]){
 const f=fixture();f.ctx.fetch=async()=>({ok:false,status,json:async()=>({error:'synthetic rejection'})});
 await assert.rejects(f.ctx.api('updates/install','POST',{}),error=>error.status===status);
 assert.equal(f.counts.login,status===401?1:0,'preserve current auth handling and HTTP status');
}
{
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.boot();f.ctx.renewShellScope();
 gate.resolve({authenticated:true,csrf:'old-response'});
 await pending;
 assert.equal(f.ctx.csrf,'synthetic-token');assert.equal(f.counts.render,0,'old boot cannot rebuild a new shell');
}
{
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.openSettings();
 f.ctx.renewShellScope();const fresh={config:{...configuration,default_environment:'new'}};f.ctx.settings=fresh;
 gate.resolve({config:configuration});
 await pending;
 assert.equal(f.ctx.settings,fresh);assert.equal(f.node('settings-dialog').shown,0);
 assert.equal(f.notices.length,0,'discarded settings response must not notify the new login');
}
{
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.saveSettings({preventDefault(){}});
 f.ctx.renewShellScope();const fresh={config:{...configuration,default_environment:'new'}};f.ctx.settings=fresh;
 f.node('settings-save').disabled=true;f.node('settings-saved').textContent='new operation';
 gate.resolve({config:configuration});
 await pending;
 assert.equal(f.ctx.settings,fresh);assert.equal(f.node('settings-save').disabled,true);
 assert.equal(f.node('settings-saved').textContent,'new operation','old finally must not unlock/overwrite the new editor');
}
{
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.poll();
 f.ctx.renewShellScope();const fresh=[{id:'new-session'}];f.ctx.tasks=fresh;f.ctx.polling=true;
 gate.resolve([{id:'old-session'}]);
 await pending;
 assert.equal(f.ctx.tasks,fresh);assert.equal(f.ctx.polling,true,'old poll finally must not release the new poll');
 assert.equal(f.counts.render,0);
}
{
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.pollSettingsStatus();
 const saved={...configuration,environments:[{id:'just-saved'}]};f.ctx.settings={...f.ctx.settings,config:saved};
 gate.resolve({config:configuration,secret_configured:true,feishu_status:'connected',chat:'paired'});
 await pending;
 assert.equal(f.ctx.settings.config.environments,saved.environments,'status refresh must not undo saved configuration');
 assert.equal(f.ctx.settings.feishu_status,'connected');assert.equal(f.ctx.settingsPolling,false);
}
{
 const f=fixture(),first=deferred(),second=deferred();let calls=0;
 f.ctx.api=()=>++calls===1?first.promise:second.promise;
 const old=f.ctx.loadEngineSettings(),latest=f.ctx.loadEngineSettings();
 const fresh={engines:[{id:'fresh'}]};second.resolve(fresh);await latest;
 first.resolve({engines:[{id:'stale'}]});await old;
 assert.equal(f.ctx.engineCatalog,fresh);assert.equal(f.counts.engine,1);
 assert.equal(f.counts.profiles,1,'one current catalog response may populate the form');
}
{
 const f=fixture();let engineLoads=0,updateLoads=0;
 const tabs=['environment','engines','updates'].map(page=>({dataset:{settings:page},classList:{toggle(){}}}));
 const sections=tabs.map(tab=>({id:'settings-'+tab.dataset.settings,classList:{toggle(){}}}));
 f.node('settings-form').querySelector=()=>({querySelectorAll:()=>tabs});
 f.node('settings-form').querySelectorAll=()=>sections;
 f.ctx.loadEngineSettings=()=>engineLoads++;f.ctx.loadUpdateInformation=()=>updateLoads++;
 f.ctx.showSettingsSection('engines');f.ctx.showSettingsSection('updates');f.ctx.showSettingsSection('environment');
 assert.equal(engineLoads,1);assert.equal(updateLoads,1);assert.equal(f.node('settings-save').classList.toggle instanceof Function,true);
 assert.doesNotMatch(engines,/nav\.querySelectorAll<HTMLButtonElement>/,'engine settings must not add a second navigation handler');
}
const webFiles=(await readdir(new URL('../web/',import.meta.url))).filter(name=>/\.(ts|html)$/.test(name));
const web=await Promise.all(webFiles.map(name=>readFile(new URL('../web/'+name,import.meta.url),'utf8')));
assert(web.every(text=>!text.includes('简作')),'visible web source must use Duo branding');
assert(web.join('\n').includes("'jianzuo-appearance-v1'"),'preserve saved appearance compatibility');
assert(web.join('\n').includes("health.app==='jianzuo'"),'preserve update health protocol compatibility');
assert.match(await readFile(new URL('../web/index.html',import.meta.url),'utf8'),/<title>Duo/);
console.log('PASS: shell listener/disposer cleanup, late auth/settings/poll isolation, preserved HTTP errors, status/editor separation, single settings routing and Duo compatibility branding. No network or model requests.');
