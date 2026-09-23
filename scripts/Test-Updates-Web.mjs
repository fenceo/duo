import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const source=await readFile(new URL('../web/updates.ts',import.meta.url),'utf8');
const script=stripTypeScriptTypes(source,{mode:'transform'});
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}};
const repo='owner/jianzuo',base='https://github.com/'+repo+'/releases/download/v0.19.0/';
const installer=base+'Jianzuo-Setup-User-x64.exe',portable=base+'Jianzuo-portable-windows-x64.zip';
const info={current:'0.18.1',latest:'v0.19.0',repository:repo,state:'available',message:'有新版本',installation_mode:'installed',package_kind:'installer',download_url:installer,checksum_url:installer+'.sha256',installer_download_url:installer,portable_download_url:portable,install_supported:true};
function fixture(){
 const nodes=new Map(),calls=[],notices=[],confirmations=[];let mounted=true,reloads=0;
 const node=id=>{
  if(!nodes.has(id)){
   const classes=new Set(),attributes=new Map();
   nodes.set(id,{id,value:'',textContent:'',innerHTML:'',disabled:false,title:'',dataset:{},classList:{add(...v){v.forEach(x=>classes.add(x))},remove(...v){v.forEach(x=>classes.delete(x))},contains:v=>classes.has(v),toggle(v,on){on??=!classes.has(v);on?classes.add(v):classes.delete(v);return on}},setAttribute:(k,v)=>attributes.set(k,v),removeAttribute:k=>attributes.delete(k),getAttribute:k=>attributes.get(k)});
  }
  return nodes.get(id);
 };
 const steps=['checking','preparing','reconnecting','complete'].map(phase=>{const n=node('step-'+phase);n.dataset.updateStep=phase;return n});
 const ctx=createContext({
  document:{getElementById:id=>mounted?node(id):null,querySelectorAll:()=>mounted?steps:[]},
  element:node,input:node,button:node,URL,AbortController,setTimeout,clearTimeout,Date,console,
  escapeHTML:v=>v.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),
  api:async(path,method,data)=>{calls.push({path,method,data});return path==='updates/install'?{state:'scheduled',version:'v0.19.0',message:'prepared'}:info},
  notify:message=>notices.push(message),confirm:message=>{confirmations.push(message);return true},
  location:{reload(){reloads++}},fetch:async()=>{throw new Error('No unmocked HTTP requests permitted')},
 });
 runInContext(script,ctx,{filename:'updates-under-test.js'});
 return {ctx,node,calls,notices,confirmations,get reloads(){return reloads},unmount(){mounted=false},value:expression=>runInContext(expression,ctx)};
}
function syntheticClock(ctx){
 let now=0,next=0;const timers=new Map();
 ctx.Date=class extends Date {static now(){return now}};
 ctx.setTimeout=(callback,ms)=>{const id=++next;timers.set(id,{at:now+ms,callback});return id};ctx.clearTimeout=id=>timers.delete(id);
 return {async advance(ms){const end=now+ms;await new Promise(setImmediate);for(;;){const nextTimer=[...timers].filter(([,timer])=>timer.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!nextTimer)break;now=nextTimer[1].at;timers.delete(nextTimer[0]);nextTimer[1].callback();await new Promise(setImmediate)}now=end;await new Promise(setImmediate)},get pending(){return timers.size}};
}
{
 const {ctx}=fixture();
 for(const url of ['https://github.com/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases/tag/v1.0.0',installer])assert.equal(ctx.safeUpdateLink(url,repo),url);
 for(const url of [undefined,'javascript:alert(1)','https://github.com.evil.test/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases-evil','https://github.com/other/project/releases','https://user:pass@github.com/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases?token=x','https://github.com/owner/jianzuo/releases#bad'])assert.equal(ctx.safeUpdateLink(url,repo),'');
 assert.equal(ctx.safeUpdateLink('https://github.com/Owner/Jianzuo/releases',repo),'https://github.com/Owner/Jianzuo/releases');
 const installed=ctx.updateLinks(info);
 assert.equal(installed[0][1],installer);assert.match(installed[0][0],/安装包 EXE/);
 assert(installed.some(([label,url])=>label==='便携版 ZIP'&&url===portable),'portable remains a visible alternative');
 const portableLinks=ctx.updateLinks({...info,installation_mode:'portable',package_kind:'portable',download_url:portable});
 assert.equal(portableLinks[0][1],portable);assert.match(portableLinks[0][0],/便携版 ZIP/);
 const unmanaged=ctx.updateLinks({...info,installation_mode:'unmanaged',install_supported:false});
 assert.equal(unmanaged[0][1],installer);
}
{
 const {ctx,node}=fixture();ctx.renderUpdateInformation(info);
 assert.equal(node('update-current').textContent,'0.18.1');assert.equal(node('update-mode').textContent,'Windows 安装版');assert.equal(node('update-install').disabled,false);
 ctx.renderUpdateInformation({...info,installation_mode:'unmanaged',install_supported:false,install_message:'请手动下载'});
 assert.equal(node('update-install').disabled,true);assert.equal(node('update-support').textContent,'请手动下载');
 ctx.renderUpdateInformation({...info,notes:'<script>bad</script>',download_url:'javascript:bad'});
 assert.equal(node('update-install').disabled,true);assert.equal(node('update-notes-content').textContent,'<script>bad</script>');
}
{
 const f=fixture(),{ctx,node,calls}=f,gate=deferred(),health=deferred();
 ctx.renderUpdateInformation(info);
 ctx.api=async(path,method,data)=>{calls.push({path,method,data});return gate.promise};
 ctx.waitForUpdatedService=()=>health.promise;
 const first=ctx.installNewVersion();await ctx.installNewVersion();await ctx.checkNewVersion();await ctx.saveUpdateSource();
 assert.equal(calls.length,1,'preparing must lock duplicate install/check/source actions');
 assert.match(node('update-result').textContent,/下载、校验并准备/);assert.equal(node('update-check').disabled,true);
 gate.resolve({state:'scheduled',version:'v0.19.0',message:'prepared'});await new Promise(setImmediate);
 assert.equal(f.reloads,0,'202 scheduled is not proof of a completed update');assert.match(node('update-result').textContent,/确认新版前不会显示成功/);
 health.resolve({matched:true,lastVersion:'0.19.0'});await first;
 assert.equal(f.reloads,1);assert.equal(f.value('updatePhase'),'complete');assert.equal(node('update-install').disabled,true,'keep locked during navigation');
 assert.match(f.confirmations[0],/不会强停/);assert.doesNotMatch(f.confirmations[0],/会停止正在运行/);
}
for(const status of [401,403,409,500]){
 const f=fixture(),{ctx,node,calls}=f;ctx.renderUpdateInformation(info);
 ctx.api=async path=>{calls.push({path});throw Object.assign(new Error(status===500?'下载失败':'fixture error'),{status})};
 await ctx.installNewVersion();
 assert.equal(calls.length,1);assert.equal(node('update-check').disabled,false);assert.equal(node('update-install').disabled,false);
 assert.equal(f.value('updateExpectedVersion'),'');assert.equal(f.reloads,0);
 if(status===409)assert.match(node('update-result').textContent,/任务.*终端.*硬件/);
 if(status===401||status===403)assert.match(node('update-result').textContent,/登录/);
}
{
 const f=fixture();f.ctx.renderUpdateInformation(info);
 f.ctx.api=async()=>{f.unmount();throw Object.assign(new Error('expired'),{status:401})};
 await f.ctx.installNewVersion();assert.equal(f.value('updateLoading'),false,'login screen replacement must not throw during cleanup');
}
{
 const f=fixture(),{ctx,node,calls}=f;ctx.renderUpdateInformation(info);
 ctx.waitForUpdatedService=async()=>({matched:false,lastVersion:'0.18.1'});
 await ctx.installNewVersion();
 assert.equal(f.reloads,0);assert.equal(f.value('updatePhase'),'timeout');assert.match(node('update-result').textContent,/尚未确认更新成功/);
 assert.equal(node('update-reconnect').classList.contains('hidden'),false);assert.equal(node('update-reconnect').disabled,false);
 assert.equal(node('update-check').disabled,false);assert.equal(node('update-install').disabled,true,'uncertain update cannot be submitted twice');
 await ctx.installNewVersion();assert.equal(calls.filter(c=>c.path==='updates/install').length,1);
 ctx.api=async()=>{throw Object.assign(new Error('check unavailable'),{status:500})};await ctx.checkNewVersion();
 assert.equal(node('update-reconnect').classList.contains('hidden'),false,'failed recheck must preserve reconnect action');
 ctx.waitForUpdatedService=async()=>({matched:true,lastVersion:'0.19.0'});
 await ctx.reconnectAfterUpdate();assert.equal(f.reloads,1);
}
{
 const f=fixture();f.ctx.renderUpdateInformation(info);
 f.ctx.api=async()=>{throw new TypeError('connection reset after accepting install')};
 let expected='';f.ctx.waitForUpdatedService=async version=>{expected=version;return {matched:true,lastVersion:'0.19.0'}};
 await f.ctx.installNewVersion();assert.equal(expected,'0.19.0');assert.equal(f.reloads,1,'lost response may still reconnect, but only after matching version');
}
{
 const f=fixture();f.ctx.renderUpdateInformation(info);f.ctx.confirm=()=>false;await f.ctx.installNewVersion();assert.equal(f.calls.length,0);
 f.ctx.confirm=()=>true;f.ctx.renderUpdateInformation({...info,latest:undefined});await f.ctx.installNewVersion();assert.equal(f.calls.length,0);
 f.ctx.renderUpdateInformation({...info,download_url:'https://evil.test/payload'});await f.ctx.installNewVersion();assert.equal(f.calls.length,0);
}
{
 const {ctx}=fixture(),versions=['0.18.1','0.18.1','0.19.0-portable'];let requests=0;
 ctx.fetch=async(path,options)=>{assert.equal(path,'/healthz');assert.equal(options.cache,'no-store');requests++;return {ok:true,status:200,json:async()=>({app:'jianzuo',version:versions.shift()})}};
 const outcome=await ctx.waitForUpdatedService('v0.19.0',100,1);
 assert.equal(outcome.matched,true);assert.equal(requests,3,'old healthy service is not update success');
}
for(const health of [{app:'other',version:'0.19.0'},{app:'jianzuo',version:'0.18.1'},{app:'jianzuo',version:'0.19.0-dev'}]){
 const {ctx}=fixture();let requests=0;ctx.fetch=async()=>{requests++;return {ok:true,status:200,json:async()=>health}};
 const outcome=await ctx.waitForUpdatedService('0.19.0',15,1);assert.equal(outcome.matched,false);assert(requests<30,'polling is bounded');
}
{
 const {ctx}=fixture();ctx.fetch=async(_,options)=>new Promise((resolve,reject)=>options.signal.addEventListener('abort',()=>reject(new Error('timeout'))));
 const start=Date.now(),outcome=await ctx.waitForUpdatedService('0.19.0',15,1);
 assert.equal(outcome.matched,false);assert(Date.now()-start<500,'a hanging health request must obey the overall timeout');
}
for(const status of [401,403]){
 const {ctx}=fixture();let requests=0;ctx.fetch=async()=>{requests++;return {ok:false,status}};
 const result=await ctx.waitForUpdatedService('0.19.0',100,1);assert.equal(result.status,status);assert.equal(result.matched,false);assert.equal(requests,1);
}
{
 const f=fixture(),clock=syntheticClock(f.ctx);let healthReads=0;
 f.ctx.fetch=async()=>{healthReads++;if(healthReads===1)return {ok:true,status:200,json:async()=>({app:'jianzuo',version:'0.18.1'})};throw new Error('synthetic service offline')};f.ctx.renderUpdateInformation(info);
 const pending=f.ctx.installNewVersion();await clock.advance(0);
 assert.match(f.node('update-result').textContent,/已等待 0 秒，本次最多等待 180 秒/);
 assert.match(f.node('update-result').textContent,/不是安装实时进度/);
 await clock.advance(60000);assert.match(f.node('update-result').textContent,/已等待 60 秒/);
 assert.equal(f.node('update-install').disabled,true);assert.equal(f.reloads,0);
 await clock.advance(118000);assert.match(f.node('update-result').textContent,/已等待 178 秒/);
 await clock.advance(2000);await pending;
 assert.equal(f.value('updatePhase'),'timeout');assert.equal(clock.pending,0,'all health request and polling timers settle');assert(healthReads<=91);
 assert.match(f.node('update-result').textContent,/服务尚未恢复连接/);assert.match(f.node('update-result').textContent,/从桌面或开始菜单启动 Duo/);
 assert.doesNotMatch(f.node('update-result').textContent,/服务仍返回版本/,'an old pre-shutdown health response is not evidence the service is still online');
 assert.match(f.node('update-result').textContent,/update\.log 和 update-installer\.log/);
 assert.equal(f.node('update-install').disabled,true,'timeout keeps duplicate-install protection');assert.equal(f.node('update-reconnect').disabled,false);
 assert.equal(f.reloads,0,'elapsed time is never proof of installation');
}
assert.doesNotMatch(source,/Math\.random|setInterval|[0-9]+%|会停止正在运行的任务/,'do not fabricate progress or force-stop work');
console.log('PASS: mode-aware EXE/ZIP links, real stages, duplicate locks, 401/403/409/download failure recovery, lost-response reconnect, strict health/version proof, bounded polling/abort, retry after timeout, and no model or network calls.');
