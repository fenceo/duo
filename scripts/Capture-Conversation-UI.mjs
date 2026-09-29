// Render only synthetic DOM fixtures. Never connect to a running Duo instance,
// an existing browser profile, a model provider, or a user's data directory.
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import fs from 'node:fs/promises';
import {existsSync,mkdtempSync} from 'node:fs';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {spawnSync} from 'node:child_process';
import assert from 'node:assert/strict';

const output=path.resolve('build/ui-review');
await fs.mkdir(output,{recursive:true});
const baseline=path.join(output,'baseline');await fs.mkdir(baseline,{recursive:true});
const sourceFiles=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','engines','app','panels'];
const baseRef=process.env.DUO_UI_BASE||'v0.22.7';
for(const file of [...sourceFiles.map(name=>name+'.ts'),'style.css','workbench.css']){
 const result=spawnSync('git',['show',baseRef+':web/'+file],{encoding:'utf8',windowsHide:true});
 if(result.status!==0)throw Error('Cannot read UI comparison baseline: '+baseRef);
 await fs.writeFile(path.join(baseline,file),result.stdout);
}
const browser=['C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(existsSync);
if(!browser)throw Error('A standard installed Chromium browser is required for UI captures.');
const cases=[['before','light',1280,800],['normal','light',1280,800],['normal','dark',1280,800],['normal','light',390,844],['normal','dark',390,844],['menu','light',1280,800],['menu','dark',390,844],['long','light',390,844],['closed','light',390,844],['create','light',1280,800],['create','light',390,844],['settings','light',1280,800],['settings','dark',390,844]];
const measurements=[],failures=[];
for(const [view,theme,width,height] of cases){
 const name=`${view}-${theme}-${width}`;
 const webRoot=view==='before'?pathToFileURL(baseline+path.sep):new URL('../web/',import.meta.url);
 const {document,ctx}=await createWebShellFixture(webRoot);
 runInContext(`
  chosen='layout-preview';
  detail={task:{id:chosen,title:'Duo 优化',workspace:'C:/Projects/duo',environment:{name:'本机 Windows'},engine:'codex',model:'gpt-6-astra',reasoning_effort:'high',session:'synthetic-session',status:'done',archived:false,updated:1},runs:[{id:'r1',kind:'chat',status:'done',input:'优化对话页面，让常用操作更直接，留出更多阅读空间。',result:'已完成对话页面优化。\\n\\n- 标题与工作目录放在同一行。\\n- 本任务知识可以直接打开。\\n- 引用历史和知识并入输入框底部。\\n\\n会话信息与低频操作集中在右上角。',created:1,finished:2}],events:[],session_started:1790310000000};
  tasks=[detail.task];renderTask();
  for(const id of ['tabs','task-actions','composer-wrap','conversation-filter'])element(id).classList.remove('hidden');
  element('conversation').innerHTML='';appendEvents([{seq:1,task_id:chosen,run_id:'r1',kind:'user',text:detail.runs[0].input},{seq:2,task_id:chosen,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);
 `,ctx);
 if(view==='long')runInContext(`detail.task.title='很长的任务标题：检查知识记录与对话排版';detail.task.workspace='C:/Projects/a-very-long-workspace/knowledge-and-conversation-layout';renderTask()`,ctx);
 if(view==='closed')runInContext(`detail.task.engine='deepseek-harness';detail.runtime={state:'closed',can_continue:false};renderTask()`,ctx);
 if(view==='menu')document.getElementById('task-session-menu').setAttribute('open','');
 if(view==='create')runInContext(`for(const id of ['tabs','task-actions','conversation','composer-wrap','session-banner'])element(id).classList.add('hidden');setCreatePageVisible(true);element('task-title').textContent='新建任务';element('task-workspace').textContent='选择环境和工作目录'`,ctx);
 if(view==='settings'){
  document.getElementById('settings-dialog').setAttribute('open','');
  ctx.showSettingsSection('knowledge');
  for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
 }
 const style=document.createElement('style');style.textContent=(await fs.readFile(new URL('style.css',webRoot),'utf8'))+'\n'+(await fs.readFile(new URL('workbench.css',webRoot),'utf8'));document.head.append(style);
 const meta=document.createElement('meta');meta.name='viewport';meta.content='width=device-width,initial-scale=1';document.head.append(meta);
 document.documentElement.dataset.theme=theme;
 const script=document.createElement('script');script.textContent=`window.addEventListener('load',()=>{if(${JSON.stringify(view)}==='settings'){const dialog=document.getElementById('settings-dialog');dialog.removeAttribute('open');dialog.showModal()}const ids=['task-title','task-workspace','tabs','note-tab','conversation','composer','message','library-task','attach-open','command-open','message-mode','task-model','send','task-session-menu','session-recover','create-input','library-create'];const data={width:innerWidth,height:innerHeight,bodyWidth:document.body.scrollWidth,items:{}};for(const id of ids){const e=document.getElementById(id);if(e){const r=e.getBoundingClientRect();data.items[id]={x:r.x,y:r.y,width:r.width,height:r.height,visible:!!e.getClientRects().length}}}const pre=document.createElement('pre');pre.id='layout-metrics';pre.hidden=true;pre.textContent=JSON.stringify(data);document.body.append(pre)});`;document.body.append(script);
 const html=path.join(output,name+'.html');await fs.writeFile(html,document.toString());
 runInContext('authenticated=false;renewShellScope()',ctx);
 try{
  const profile=mkdtempSync(path.join(output,'profile-'));
  const result=spawnSync(browser,['--headless','--disable-gpu','--no-first-run','--no-default-browser-check','--disable-extensions','--disable-background-networking','--user-data-dir='+profile,`--window-size=${width},${height}`,'--screenshot='+path.join(output,name+'.png'),'--dump-dom','--virtual-time-budget=1000',pathToFileURL(html).href],{encoding:'utf8',windowsHide:true,timeout:30000,maxBuffer:8*1024*1024});
  if(result.status!==0)throw Error('Browser failed: '+(result.error?.message||result.status)+' '+result.stderr.slice(-500));
  const match=result.stdout.match(/<pre id="layout-metrics" hidden="">(.*?)<\/pre>/s);
  assert(match,'missing browser measurements');
  const metrics=JSON.parse(match[1].replaceAll('&quot;','"').replaceAll('&amp;','&'));
  measurements.push({name,view,theme,...metrics});
  const items=metrics.items;
  assert(metrics.bodyWidth<=metrics.width+1,'page overflows horizontally');
  if(['normal','long'].includes(view)){
   for(const id of ['note-tab','task-title','task-workspace','library-task','message-mode','task-model','send']){
    const box=items[id];assert(box.visible&&box.width>0,id+' is hidden');assert(box.x>=-1&&box.x+box.width<=metrics.width+1,id+' exceeds the viewport');
   }
   assert(Math.abs(items['task-title'].y-items['task-workspace'].y)<10,'header context wraps to another row');
   assert(Math.abs(items['library-task'].y-items['attach-open'].y)<5,'reference button reserves its own row');
   assert(items.composer.y+items.composer.height<=metrics.height+1,'composer extends below viewport');
   assert(items.conversation.height>metrics.height*.5,'conversation receives too little vertical space');
  }
  if(view==='closed')assert(items['session-recover'].visible,'missing closed-session recovery');
 }catch(error){failures.push(name+': '+error.message)}
}
const before=measurements.find(x=>x.view==='before'),after=measurements.find(x=>x.name==='normal-light-1280');
if(before&&after&&after.items.conversation.height<=before.items.conversation.height)failures.push('desktop conversation height did not increase');
const report={source:process.env.GITHUB_SHA||'local',baseRef,measurements,failures};
await fs.writeFile(path.join(output,'geometry.json'),JSON.stringify(report,null,2));
const summary=JSON.stringify(measurements.map(x=>({name:x.name,width:x.width,height:x.height,chatHeight:x.items.conversation?.height})));
console.log('::notice title=Conversation layout measurements::'+summary.replaceAll('%','%25').replaceAll('\n','%0A'));
for(const failure of failures)console.log('::error title=Conversation layout regression::'+failure.replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A'));
if(failures.length)process.exitCode=1;
