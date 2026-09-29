// Render only synthetic DOM fixtures. Never connect to a running Duo instance,
// an existing browser profile, a model provider, or a user's data directory.
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {startFixtureBrowser} from './Headless-UI-Fixture.mjs';
import {runInContext} from 'node:vm';
import fs from 'node:fs/promises';
import {existsSync} from 'node:fs';
import path from 'node:path';
import {pathToFileURL} from 'node:url';
import {spawnSync} from 'node:child_process';
import assert from 'node:assert/strict';

const output=path.resolve('build/ui-review');
await fs.mkdir(output,{recursive:true});
const baseline=path.join(output,'baseline');await fs.mkdir(baseline,{recursive:true});
const sourceFiles=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','engines','app','panels'];
const baseRef=process.env.DUO_UI_BASE||'v0.22.11';
for(const file of [...sourceFiles.map(name=>name+'.ts'),'style.css','workbench.css']){
 const result=spawnSync('git',['show',baseRef+':web/'+file],{encoding:'utf8',windowsHide:true});
 if(result.status!==0)throw Error('Cannot read UI comparison baseline: '+baseRef);
 await fs.writeFile(path.join(baseline,file),result.stdout);
}
const browser=['C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(existsSync);
if(!browser)throw Error('A standard installed Chromium browser is required for UI captures.');
const cases=[['before','light',1280,800],['normal','light',1280,800],['normal','dark',1280,800],['normal','light',390,844],['normal','dark',390,844],['menu','light',1280,800],['menu','dark',390,844],['long','light',390,844],['closed','light',390,844],['create','light',1280,800],['create','light',390,844],['createbottom','light',390,844],['settings','light',1280,800],['settings','dark',390,844]];
cases.push(['library','light',1280,800],['library','dark',1280,800],['library','light',390,844],['librarypreview','light',1280,800],['librarypreview','dark',390,844],['knowledge','light',1280,800],['knowledge','dark',390,844],['knowledgeexpanded','light',390,844]);
cases.push(['sidebar','light',1280,800],['sidebar','dark',1280,800],['sidebar','dark',390,844],['sidebarempty','light',390,844],['sidebarnarrow','light',1024,600],['sidebarfull','dark',1280,800],['appmenu','light',1280,800],['appmenu','dark',390,844]);
cases.push(['sidebarwide','light',1280,800],['sidebarmigrated','dark',1280,800],['hardware','light',1280,800],['hardware','dark',390,844]);
const measurements=[],failures=[];
const renderer=await startFixtureBrowser(browser,output);
try{
for(const [view,theme,width,height] of cases){
 const name=`${view}-${theme}-${width}`;
 const webRoot=view==='before'?pathToFileURL(baseline+path.sep):new URL('../web/',import.meta.url);
 const {document,ctx}=await createWebShellFixture(webRoot);
 runInContext(`
  chosen='layout-preview';
  detail={task:{id:chosen,title:'Duo 优化',workspace:'C:/Projects/duo',environment:{name:'本机 Windows'},engine:'codex',model:'gpt-6-astra',reasoning_effort:'high',session:'synthetic-session',status:'done',archived:false,updated:1},runs:[{id:'r1',kind:'chat',status:'done',input:'优化对话页面，让常用操作更直接，留出更多阅读空间。',result:'已完成对话页面优化。\\n\\n- 标题与工作目录放在同一行。\\n- 本任务知识可以直接打开。\\n- 引用历史和知识并入输入框底部。\\n\\n会话信息与低频操作集中在右上角。',created:1,finished:2}],events:[],session_started:1790310000000};
  tasks=[detail.task,...Array.from({length:7},(_,i)=>({...detail.task,id:'task-'+i,title:['检查串口连接','整理发布说明','修复页面交互','准备下一次验证'][i%4],workspace:i<3?'C:/Projects/duo':'C:/Projects/demo'}))];renderTask();renderList();
  for(const id of ['tabs','task-actions','composer-wrap','conversation-filter'])element(id).classList.remove('hidden');
  element('conversation').innerHTML='';appendEvents([{seq:1,task_id:chosen,run_id:'r1',kind:'user',text:detail.runs[0].input},{seq:2,task_id:chosen,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);
 `,ctx);
 if(view==='long')runInContext(`detail.task.title='很长的任务标题：检查知识记录与对话排版';detail.task.workspace='C:/Projects/a-very-long-workspace/knowledge-and-conversation-layout';renderTask()`,ctx);
 if(view==='closed')runInContext(`detail.task.engine='deepseek-harness';detail.runtime={state:'closed',can_continue:false};renderTask()`,ctx);
 if(view==='menu')document.getElementById('task-session-menu').setAttribute('open','');
 if(view==='appmenu')document.getElementById('app-menu').setAttribute('open','');
 if(view.startsWith('sidebar')){
  if(width<=760)document.getElementById('sidebar').classList.add('open');
  if(view!=='sidebarempty')runInContext(`quickNotes={revision:1,color:'neutral',items:Array.from({length:${view==='sidebarfull'?30:4}},(_,i)=>({id:'note-'+i,content:['检查新版侧栏与便签','记录下次要验证的问题','补充发布说明','整理已完成的事项'][i%4],done:i===3}))};stickyReady=true;stickySaved=stickyFingerprint();renderStickyBoard()`,ctx);
  if(view==='sidebarnarrow')runInContext('appearance.sidebar=180;applyAppearance(appearance)',ctx);
  if(view==='sidebarwide')runInContext('appearance.sidebar=380;applyAppearance(appearance)',ctx);
  if(view==='sidebarmigrated')runInContext("localStorage.setItem('jianzuo-dock-layout-v1',JSON.stringify({sticky:'right',tools:'left'}));installDockLayout()",ctx);
  assert.equal(document.getElementById('sticky-board').parentElement.id,'sidebar');
  assert(!document.querySelector('[data-dock-grip="sticky"]'));
  assert(!document.querySelector('.workspace-heading'));
 }
 if(view==='hardware'){
  // Synthetic read-only log view: no browser terminal, device or model connection.
  ctx.loadDevices=async()=>{};
  runInContext(`deviceID='fixture-uart';devices=[{id:deviceID,name:'开发板串口',kind:'console',protocol:'serial',device:'COM99',baud:115200}];
   input('device-picker').innerHTML='<option value="fixture-uart">开发板串口</option>';input('device-view').value='text';
   hardwareView={id:deviceID,task:chosen,alive:true,connected:true,ready:true,generation:'synthetic',controller:chosen,controllerTitle:'Duo 优化',busy:false,paused:false,written:100,term:{options:{},dispose(){}},resize:{disconnect(){}}};
   deviceEvents=Array.from({length:12},(_,i)=>({seq:i+1,direction:'rx',hex:'',text:'[fixture] board ready',created:1790310000000+i*1000}));
   switchTab('hardware');syncDocks();renderDeviceLog();
  `,ctx);
  // deviceLog decodes raw bytes; set only the synthetic output text for capture.
  document.getElementById('device-console').textContent=Array.from({length:12},(_,i)=>'12:00:'+String(i).padStart(2,'0')+' RX  [fixture] board ready').join('\n');
  assert(!document.querySelector('.hardware-ai-overview'));
 }
 if(view==='create'||view==='createbottom')runInContext(`for(const id of ['tabs','task-actions','conversation','composer-wrap','session-banner'])element(id).classList.add('hidden');setCreatePageVisible(true);setCreateSubmitState('idle');element('task-title').textContent='新建任务';element('task-workspace').textContent='选择环境和工作目录'`,ctx);
 if(view==='settings'){
  document.getElementById('settings-dialog').setAttribute('open','');
  ctx.showSettingsSection('knowledge');
  for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
 }
 if(view.startsWith('library')||view.startsWith('knowledge')){
  const entries=[['升级与发布流程','verified','manual'],['目录同步的边界与冲突处理','observed','auto'],['历史故障排查与验证结果','observed','auto'],['知识引用和适用条件','verified','manual'],['旧版配置说明','stale','manual']].map(([title,status,source],i)=>({id:'preview-'+i,task_id:'layout-preview',title,content:'## 适用问题\n查找历史经验，并在新一轮对话中复用。\n\n## 解决办法\n先预览资料，检查来源和验证状态，再引用到输入框。\n\n## 验证结果\n合成用例通过，实际任务仍需核对适用条件。',status,source,run_id:'synthetic-'+i,revision:1,created:1790310000000,updated:1790310000000}));
  ctx.previewEntries=entries;
  runInContext('knowledgeItems=previewEntries;renderKnowledgeList()',ctx);
  if(view.startsWith('knowledge')){
   // The DOM fixture has no MutationObserver; explicitly settle the same dock
   // visibility that the live shell updates after a tab switch.
   runInContext("switchTab('note');syncDocks()",ctx);
   if(view==='knowledgeexpanded')document.querySelector('[data-knowledge-toggle]').onclick();
  }else{
   const baseAPI=ctx.api;
   ctx.api=async(route,...args)=>{
    if(route.startsWith('library/search'))return {documents:entries.filter(k=>k.status!=='stale').map(k=>({...k,id:'knowledge:'+k.id,kind:'knowledge',origin:'local',task_title:'Duo 优化',hash:'synthetic-version',snippet:k.content})),total:4,truncated:false,next_offset:4};
    if(route.startsWith('library/reference'))return {preview:entries[0].content,reference:'合成引用',truncated:false};
    return baseAPI(route,...args);
   };
   await ctx.openLibrary();
   if(view==='librarypreview')await ctx.previewLibrary('knowledge:'+entries[0].id);
  }
 }
 const style=document.createElement('style');style.textContent=(await fs.readFile(new URL('style.css',webRoot),'utf8'))+'\n'+(await fs.readFile(new URL('workbench.css',webRoot),'utf8'));document.head.append(style);
 const meta=document.createElement('meta');meta.name='viewport';meta.content='width=device-width,initial-scale=1';document.head.append(meta);
 document.documentElement.dataset.theme=theme;
 const script=document.createElement('script');script.textContent=`window.addEventListener('load',()=>{for(const field of document.querySelectorAll('[data-note-input]')){field.style.height='auto';field.style.height=Math.max(32,Math.min(96,field.scrollHeight))+'px'}if(${JSON.stringify(view)}==='createbottom'){const page=document.getElementById('create-page');page.scrollTop=page.scrollHeight}if(${JSON.stringify(view)}==='settings'||${JSON.stringify(view)}.startsWith('library')){const dialog=document.getElementById(${JSON.stringify(view)}==='settings'?'settings-dialog':'library-dialog');dialog.removeAttribute('open');dialog.showModal()}const ids=['task-title','task-workspace','tabs','note-tab','conversation','composer','message','library-task','attach-open','command-open','message-mode','task-model','task-model-button','send','task-session-menu','task-session-toggle','session-recover','mode-engine-hint','create-input','library-create','create-submit','library-dialog','library-query','library-workspace','library-results','library-preview-content','library-preview-cite','library-preview-back','knowledge-query','notebook','sidebar','task-list','tasks-active','tasks-archived','trash-open','new-task','search','hardware-panel','device-picker','device-refresh','device-ai','device-console','sticky-board','sticky-body','sticky-list','sticky-new','sticky-save-status','app-version','app-menu','app-menu-toggle','settings-open','theme-toggle','library-open'];const firstTask=document.querySelector('.task strong');if(firstTask){firstTask.id='first-task-title';ids.push('first-task-title')}const environment=document.querySelector('.task-environment');if(environment){environment.id='first-task-environment';ids.push('first-task-environment')}const firstNote=document.querySelector('.quick-note');if(firstNote){firstNote.id='first-quick-note';ids.push('first-quick-note')}const data={width:innerWidth,height:innerHeight,bodyWidth:document.body.scrollWidth,items:{}};for(const id of ids){const e=document.getElementById(id);if(e){const r=e.getBoundingClientRect();data.items[id]={x:r.x,y:r.y,width:r.width,height:r.height,scrollWidth:e.scrollWidth,clientWidth:e.clientWidth,visible:!!e.getClientRects().length}}}const pre=document.createElement('pre');pre.id='layout-metrics';pre.hidden=true;pre.textContent=JSON.stringify(data);document.body.append(pre)});`;document.body.append(script);
 // Serialize current checkbox state as well as markup into the static fixture.
 for(const control of document.querySelectorAll('input[type="checkbox"]'))if(typeof control.checked==='boolean')control.toggleAttribute('checked',control.checked);
 const html=path.join(output,name+'.html');await fs.writeFile(html,document.toString());
 runInContext('authenticated=false;renewShellScope()',ctx);
 try{
  const {metrics,png}=await renderer.capture(pathToFileURL(html).href,width,height);
  await fs.writeFile(path.join(output,name+'.png'),png);
  measurements.push({name,view,theme,...metrics});
  assert.equal(metrics.width,width,'browser must use the requested CSS viewport width');
  assert.equal(metrics.height,height,'browser must use the requested CSS viewport height');
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
  if(width<=760&&['normal','long','closed'].includes(view)){
   for(const id of ['library-task','attach-open','command-open','message-mode','task-model-button','send','task-session-toggle'])assert(items[id].height>=44&&items[id].width>=44,id+' touch target is too small');
  }
  if(view==='closed'){
   assert(items['session-recover'].visible&&items['session-recover'].height>=44,'missing accessible closed-session recovery');
   assert(items['mode-engine-hint'].width>items.message.width*.7,'engine hint is squeezed into a narrow column');
  }
  if(view.startsWith('library')){
   const box=items['library-dialog'];assert(box.visible&&box.x>=0&&box.x+box.width<=width&&box.y>=0&&box.y+box.height<=height,'library dialog exceeds viewport');
   assert(box.scrollWidth<=box.clientWidth+1,'library dialog overflows horizontally');
   const content=items[view==='librarypreview'?'library-preview-content':'library-results'];assert(content.visible&&content.height>120,'library has insufficient readable content space');
   if(view==='librarypreview'){const cite=items['library-preview-cite'];assert(cite.visible&&cite.y+cite.height<=height&&cite.x+cite.width<=width,'preview action is clipped')}
  }
  if(view.startsWith('knowledge')){
   const box=items['notebook'];assert(box.visible&&box.scrollWidth<=box.clientWidth+1,'task knowledge panel overflows');assert(items['knowledge-query'].visible&&items['knowledge-query'].width>=120,'task knowledge search is unusable');
  }
  if(view.startsWith('sidebar')){
   const board=items['sticky-board'],list=items['task-list'];
   for(const id of ['app-version','sticky-board','sticky-list','sticky-new','sticky-save-status']){const b=items[id];assert(b.visible&&b.width>0&&b.height>0,id+' is hidden');assert(b.x>=0&&b.x+b.width<=width+1&&b.y>=0&&b.y+b.height<=height+1,id+' exceeds viewport');assert(b.scrollWidth<=b.clientWidth+1,id+' overflows horizontally')}
   assert(list.height>=90,'task list has too little height');assert(list.y<160,'sidebar header takes too much space');assert(list.y+list.height<=board.y+1,'task list overlaps notes');
   for(const id of ['tasks-active','tasks-archived','trash-open'])assert(Math.abs(items[id].y-items['new-task'].y)<2,'task navigation wraps');
   assert(Math.abs(items['first-task-title'].y-items['first-task-environment'].y)<6,'task environment wraps to another line');
   assert(items['first-task-environment'].width>=28,'task environment loses all readable space');
   assert(items['sticky-list'].height>=32,'notes have no writing room');
   if(view==='sidebarnarrow')assert(items['first-task-title'].width>=60,'narrow sidebar hides task titles behind status labels');
   if(view==='sidebar'&&width>760)assert(items['first-quick-note'].height<=42,'single-line notes have excessive height');
   if(width<=760){assert(items.sidebar.height===height,'phone drawer must fill the viewport');assert(items.conversation.height>height*.5,'opening the task drawer must not push the conversation down')}
  }
  if(view==='hardware'){
   for(const id of ['hardware-panel','device-picker','device-refresh','device-ai','device-console']){const b=items[id];assert(b.visible&&b.width>0&&b.height>0,id+' is hidden');assert(b.x>=0&&b.x+b.width<=width+1,id+' exceeds the viewport')}
   assert(items['device-console'].height>=180,'hardware controls leave too little space for logs');
  }
  if(view==='appmenu')for(const id of ['settings-open','theme-toggle','library-open']){const b=items[id];assert(b.visible&&b.y+b.height<=height&&b.x>=0&&b.x+b.width<=width,'global menu entry is clipped: '+id)}
  if(view==='createbottom')for(const id of ['library-create','create-submit']){
   const box=items[id];assert(box.visible&&box.y>=0&&box.y+box.height<=height&&box.x+box.width<=width,id+' is outside the scrolled create page');
  }
 }catch(error){failures.push(name+': '+error.message)}
}
}finally{await renderer.close()}
const before=measurements.find(x=>x.view==='before'),after=measurements.find(x=>x.name==='normal-light-1280');
if(before&&after){if(after.items.conversation.height<before.items.conversation.height-2)failures.push('desktop conversation lost vertical space');if(after.items['task-list'].y>before.items['task-list'].y-36)failures.push('sidebar navigation did not become materially shorter');}
const report={source:process.env.GITHUB_SHA||'local',baseRef,measurements,failures};
await fs.writeFile(path.join(output,'geometry.json'),JSON.stringify(report,null,2));
const summary=JSON.stringify(measurements.map(x=>({name:x.name,width:x.width,height:x.height,chatHeight:x.items.conversation?.height})));
console.log('::notice title=Conversation layout measurements::'+summary.replaceAll('%','%25').replaceAll('\n','%0A'));
for(const failure of failures)console.log('::error title=Conversation layout regression::'+failure.replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A'));
if(failures.length)process.exitCode=1;
