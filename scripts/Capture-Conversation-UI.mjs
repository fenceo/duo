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
const sourceFiles=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','engines','handoff','desktop','app','panels'];
const baseRef=process.env.DUO_UI_BASE||'v0.23.2';
for(const file of [...sourceFiles.map(name=>name+'.ts'),'style.css','workbench.css']){
 const result=spawnSync('git',['show',baseRef+':web/'+file],{encoding:'utf8',windowsHide:true});
 if(result.status!==0&&file!=='handoff.ts'&&file!=='desktop.ts')throw Error('Cannot read UI comparison baseline: '+baseRef);
 await fs.writeFile(path.join(baseline,file),result.stdout);
}
const browser=['C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(existsSync);
if(!browser)throw Error('A standard installed Chromium browser is required for UI captures.');
const cases=[['before','light',1280,800],['normal','light',1280,800],['normal','dark',1280,800],['normal','light',390,844],['normal','dark',390,844],['menu','light',1280,800],['menu','dark',390,844],['long','light',390,844],['closed','light',390,844],['create','light',1280,800],['create','light',390,844],['createbottom','light',390,844],['settings','light',1280,800],['settings','dark',390,844]];
cases.push(['library','light',1280,800],['library','dark',1280,800],['library','light',390,844],['librarypreview','light',1280,800],['librarypreview','dark',390,844],['knowledge','light',1280,800],['knowledge','dark',390,844],['knowledgeexpanded','light',390,844]);
cases.push(['sidebar','light',1280,800],['sidebar','dark',1280,800],['sidebar','dark',390,844],['sidebarempty','light',390,844],['sidebarnarrow','light',1024,600],['sidebarfull','dark',1280,800],['appmenu','light',1280,800],['appmenu','dark',390,844]);
cases.push(['sidebarwide','light',1280,800],['sidebarmigrated','dark',1280,800],['hardware','light',1280,800],['hardware','dark',390,844]);
cases.push(['home','light',1280,800],['home','dark',390,844],['normal','dark',320,844],['header','light',1024,800],['context','dark',390,844]);
cases.push(['headernarrow','light',1101,800]);
cases.push(['live','light',1280,800],['live','dark',390,844],['live','light',320,844],['questions','light',1280,800],['questions','dark',390,844],['questions','light',320,844],['questionsmany','light',390,844]);
cases.push(['questionsfull','light',1280,800],['questionsfull','dark',390,844],['questionsmanyfull','light',320,844]);
cases.push(['composerlarge','light',1280,800],['composerlarge','dark',390,844]);
cases.push(['history','light',1280,800],['history','dark',390,844],['filters','light',1280,800],['filtered','dark',320,844]);
cases.push(['handoff','light',1280,800],['handoff','dark',390,844],['handoff','light',320,844],['handoffbottom','dark',390,844]);
cases.push(['handofflegacy','light',1280,800],['handofflegacy','dark',390,844],['handofflegacybottom','light',320,844]);
cases.push(['onboarding','light',1280,800],['onboarding','dark',390,844],['onboarding','light',320,844],['desktop','light',1280,800],['desktop','dark',390,844],['desktop','light',320,844]);
const measurements=[],failures=[];
const renderer=await startFixtureBrowser(browser,output);
try{
for(const [view,theme,width,height] of cases){
 const name=`${view}-${theme}-${width}`;
 const webRoot=view==='before'?pathToFileURL(baseline+path.sep):new URL('../web/',import.meta.url);
 const {document,ctx}=await createWebShellFixture(webRoot);
 runInContext(`
  chosen='layout-preview';
  detail={task:{id:chosen,title:'Duo 优化',workspace:'C:/Projects/duo',environment:{type:'windows',name:'本机 Windows'},engine:'codex',model:'gpt-6-astra',reasoning_effort:'high',session:'synthetic-session',status:'done',archived:false,updated:1},runs:[{id:'r1',kind:'chat',status:'done',input:'优化对话页面，让常用操作更直接，留出更多阅读空间。',result:'已完成对话页面优化。\\n\\n- 标题与工作目录放在同一行。\\n- 本任务知识可以直接打开。\\n- 引用历史和知识并入输入框底部。\\n\\n会话信息与低频操作集中在右上角。',created:1,finished:2}],events:[],session_started:1790310000000};
  tasks=[detail.task,...Array.from({length:7},(_,i)=>({...detail.task,id:'task-'+i,title:['检查串口连接','整理发布说明','修复页面交互','准备下一次验证'][i%4],workspace:i<3?'C:/Projects/duo':'C:/Projects/demo'}))];renderTask();renderList();
  for(const id of ['tabs','task-actions','composer-wrap','conversation-filter'])element(id).classList.remove('hidden');
  element('conversation').innerHTML='';appendEvents([{seq:1,task_id:chosen,run_id:'r1',kind:'user',text:detail.runs[0].input},{seq:2,task_id:chosen,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);
 `,ctx);
 if(view==='history')runInContext(`
  resetConversation();sequence=0;element('conversation').replaceChildren();detail.runs=[];
  const historyEvents=[];
  for(let i=7;i<12;i++){const id='history-'+i,result='第 '+(i+1)+' 轮已完成，之前的记录按需查看。';detail.runs.push({id,kind:'chat',status:'done',input:'继续第 '+(i+1)+' 轮',result,created:i+1,finished:i+2});historyEvents.push({seq:i*10+1,run_id:id,kind:'user',text:'继续第 '+(i+1)+' 轮',created:i+1},{seq:i*10+2,run_id:id,kind:'assistant',text:'正在检查本轮修改。',created:i+1},{seq:i*10+3,run_id:id,kind:'assistant',text:result,created:i+1})}
  conversationHistory={before:'history-7',hasOlder:true,expanded:false,loading:false,error:''};appendEvents(historyEvents);renderTask();
 `,ctx);
 if(view==='filters'||view==='filtered')runInContext(`
  resetConversation();sequence=0;element('conversation').replaceChildren();appendEvents([{seq:1,run_id:'r1',kind:'user',text:'检查修改并运行测试',created:1},{seq:2,run_id:'r1',kind:'assistant',text:'正在检查修改范围。',created:1},{seq:3,run_id:'r1',kind:'tool',text:'模拟测试命令\\nPASS: synthetic only',created:1},{seq:4,run_id:'r1',kind:'assistant',text:detail.runs[0].result,created:1}]);setConversationFilter({tools:VIEW==='filters',process:false});
 `.replace('VIEW',JSON.stringify(view)),ctx);
 if(view==='long')runInContext(`detail.task.title='很长的任务标题：检查知识记录与对话排版';detail.task.workspace='C:/Projects/a-very-long-workspace/knowledge-and-conversation-layout';renderTask()`,ctx);
 if(view==='closed')runInContext(`detail.task.engine='deepseek-harness';detail.runtime={state:'closed',can_continue:false};renderTask()`,ctx);
 if(view==='menu')await ctx.openSessionInfo();
 if(view==='home')runInContext("chosen='';detail=null;renderShell();renderList()",ctx);
 if(view==='context')runInContext("taskContext=[{name:'AGENTS.md',label:'项目指令'}];renderSessionBanner()",ctx);
 if(view==='headernarrow')runInContext('appearance.sidebar=420;applyAppearance(appearance)',ctx);
 if(view==='composerlarge'){document.getElementById('message').style.height='280px';document.getElementById('message').textContent='较长的任务要求：\n'+('记录前提、共识、未决事项，并检查来源与适用条件。\n'.repeat(12))}
 if(view==='live'||view.startsWith('questions')){
  runInContext(`detail.task.status='running';detail.runs[0].status='running';detail.interaction={run_id:'r1',can_steer:true,can_interrupt:true,steering:false,interrupting:false};input('message').value='先检查失败的测试，保持修改范围。';renderTask()`,ctx);
  if(view.startsWith('questions')){
   runInContext(`detail.approvals=[{id:'preview-question',task_id:chosen,run_id:'r1',method:'item/tool/requestUserInput',params:{isBlocking:${view.includes('many')?'false':'true'},questions:Array.from({length:${view.includes('many')?3:1}},(_,i)=>({id:'q-'+i,header:'修改范围 '+(i+1),question:'希望先修复当前问题，还是一起整理相关功能？',isOther:true,options:[{label:'先修复（推荐）',description:'先解决当前问题，保持改动范围'},{label:'一起整理',description:'同时调整相关交互和布局'}]}))}}];renderWorkflow()`,ctx);
   const option=document.querySelector('.codex-answer-option');option.id='preview-answer-option';
   document.querySelector('.codex-approval-actions button').id='preview-answer-submit';
   if(view.endsWith('full'))document.getElementById('codex-approvals-expand').onclick();
  }
 }
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
 if(view==='onboarding'){
  runInContext("settings.config.environments.push({id:'local',name:'Windows',type:'windows',codex:'fixture',workspaces:['C:/fixture']});populateEngineOnboarding();engineCatalog={engines:[],profiles:[],active_profile:{}};renderEngineCatalog();element('settings-dialog').setAttribute('open','');document.querySelectorAll('.settings-section').forEach(e=>e.classList.add('hidden'));element('settings-engines').classList.remove('hidden');element('settings-engines').querySelector('.engine-onboarding details').open=true;element('engine-account-login').value='apiKey';element('engine-account-login').onchange();",ctx);
 }
 if(view==='desktop'){
  runInContext("desktopDialogTask=chosen;desktopState={active:true,task_id:chosen,control:true,target:'desktop'};element('desktop-target').innerHTML='<option value=desktop>整个桌面</option>';element('desktop-dialog').setAttribute('open','');renderDesktopSharing();",ctx);
 }
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
    if(route.startsWith('library/search'))return {documents:entries.filter(k=>k.status!=='stale').map(k=>({...k,id:'knowledge:'+k.id,kind:'task',layer:'tasks',path:'tasks/fixture/'+k.id+'.md',tags:['知识库','性能'],origin:'local',task_title:'Duo 优化',hash:'synthetic-version',snippet:k.content})),total:4,truncated:false,next_offset:4};
    if(route.startsWith('library/reference'))return {preview:entries[0].content,reference:'合成引用',truncated:false};
    if(route.startsWith('library/document'))return {content:entries[0].content};
    return baseAPI(route,...args);
   };
   await ctx.openLibrary();
   if(view==='librarypreview')await ctx.previewLibrary('knowledge:'+entries[0].id);
  }
 }
 if(view.startsWith('handoff')){
  runInContext("detail.task.environment=settings.config.environments[0];detail.task.workspace='/fixture/duo';detail.task.binding={revision:'fixture-binding'}",ctx);
  if(view.startsWith('handofflegacy'))runInContext('detail.task.binding=undefined;detail.task.mode=undefined',ctx);
  const snapshot=runInContext('detail',ctx),env=snapshot.task.environment;
  ctx.api=async route=>{
   if(route==='engines')return {engines:[],profiles:[{id:'team',name:'团队 API 配置',engine:'codex',environment_id:env.id,kind:'codex_home',reference:'/fixture/team',updated:1}],active_profile:{}};
   if(route.endsWith('?recent=1'))return snapshot;
   if(route.includes('/continuation/preview'))return {source_task_id:snapshot.task.id,source_title:snapshot.task.title,source_engine:'codex',transferred_runs:27,transferred_knowledge:6,archive_bytes:102400,fingerprint:'fixture-preview',context_truncated:true,context:'任务目标：继续完成页面优化。已有验证：合成回归通过。未解决：真实模型接续效果尚未验证。完整历史包含 27 轮记录。'};
   if(route.includes('/models?'))return {models:[{id:'gpt-6-astra',name:'gpt-6-astra'}],source:'本地配置',status:'ready',modified:0};
   throw Error('Unexpected switch preview request '+route);
  };
  await ctx.openHandoff();await new Promise(resolve=>setImmediate(resolve));
 }
 const style=document.createElement('style');style.textContent=(await fs.readFile(new URL('style.css',webRoot),'utf8'))+'\n'+(await fs.readFile(new URL('workbench.css',webRoot),'utf8'));document.head.append(style);
 const meta=document.createElement('meta');meta.name='viewport';meta.content='width=device-width,initial-scale=1';document.head.append(meta);
 document.documentElement.dataset.theme=theme;
 if(view.startsWith('questions')&&view.endsWith('full')){const open=document.createElement('script');open.textContent="window.addEventListener('load',()=>{const d=document.getElementById('codex-requests-dialog');d.removeAttribute('open');d.showModal()})";document.body.append(open)}
 const script=document.createElement('script');script.textContent=`window.addEventListener('load',()=>{for(const field of document.querySelectorAll('[data-note-input]')){field.style.height='auto';field.style.height=Math.max(32,Math.min(96,field.scrollHeight))+'px'}if(${JSON.stringify(view)}==='createbottom'){const page=document.getElementById('create-page');page.scrollTop=page.scrollHeight}if(${JSON.stringify(view)}==='settings'||${JSON.stringify(view)}.startsWith('library')){const dialog=document.getElementById(${JSON.stringify(view)}==='settings'?'settings-dialog':'library-dialog');dialog.removeAttribute('open');dialog.showModal()}if(${JSON.stringify(view)}==='menu'){const dialog=document.getElementById('session-info-dialog');dialog.removeAttribute('open');dialog.showModal()}if(${JSON.stringify(view)}.startsWith('handoff')){const dialog=document.getElementById('handoff-dialog');dialog.removeAttribute('open');dialog.showModal();if(${JSON.stringify(view)}.endsWith('bottom'))dialog.scrollTop=dialog.scrollHeight}if(${JSON.stringify(view)}==='onboarding'||${JSON.stringify(view)}==='desktop'){const d=document.getElementById(${JSON.stringify(view)}==='onboarding'?'settings-dialog':'desktop-dialog');d.removeAttribute('open');d.showModal()}const ids=['desktop-dialog','desktop-stop','desktop-start','engine-setup-fields','engine-setup-install','settings-dialog','handoff-legacy','handoff-preserve-legacy','handoff-dialog','handoff-engine','handoff-profile','handoff-model','handoff-mode','handoff-submit','handoff-cancel','handoff-preview-status','chat-tab','conversation-process','conversation-tools','conversation-history','conversation-older','live-steer','live-interrupt','stop','codex-approvals','preview-answer-option','preview-answer-submit','task-title','task-workspace','tabs','note-tab','conversation','composer','message','library-task','attach-open','command-open','message-mode','task-model','task-model-button','send','task-actions','session-info-open','session-info-dialog','session-info-close','session-reset','task-handoff','task-link-copy','bind-open','scratch-tab','session-recover','mode-engine-hint','create-input','library-create','create-submit','library-dialog','library-query','library-workspace','library-results','library-preview-content','library-preview-cite','library-preview-back','knowledge-query','notebook','sidebar','task-list','tasks-active','tasks-archived','trash-open','new-task','search','hardware-panel','device-picker','device-refresh','device-ai','device-console','sticky-board','sticky-body','sticky-list','sticky-new','sticky-save-status','app-version','app-menu-items','logout','settings-open','theme-toggle','library-open'];const firstTask=document.querySelector('.task strong');if(firstTask){firstTask.id='first-task-title';ids.push('first-task-title')}const environment=document.querySelector('.task-environment');if(environment){environment.id='first-task-environment';ids.push('first-task-environment')}const firstNote=document.querySelector('.quick-note');if(firstNote){firstNote.id='first-quick-note';ids.push('first-quick-note')}const data={width:innerWidth,height:innerHeight,bodyWidth:document.body.scrollWidth,handoffFooterBackground:document.querySelector('#handoff-dialog .dialog-footer')?getComputedStyle(document.querySelector('#handoff-dialog .dialog-footer')).backgroundColor:'',items:{}};for(const id of ids){const e=document.getElementById(id);if(e){const r=e.getBoundingClientRect();data.items[id]={x:r.x,y:r.y,width:r.width,height:r.height,scrollWidth:e.scrollWidth,clientWidth:e.clientWidth,visible:!!e.getClientRects().length}}}const pre=document.createElement('pre');pre.id='layout-metrics';pre.hidden=true;pre.textContent=JSON.stringify(data);document.body.append(pre)});`;document.body.append(script);
 // Serialize current checkbox state as well as markup into the static fixture.
 for(const control of document.querySelectorAll('input[type="checkbox"]'))if(typeof control.checked==='boolean')control.toggleAttribute('checked',control.checked);
 for(const control of document.querySelectorAll('fieldset'))if(typeof control.disabled==='boolean')control.toggleAttribute('disabled',control.disabled);
 const html=path.join(output,name+'.html');await fs.writeFile(html,document.toString());
 runInContext('authenticated=false;renewShellScope()',ctx);
 try{
  const {metrics,png}=await renderer.capture(pathToFileURL(html).href,width,height);
  await fs.writeFile(path.join(output,name+'.png'),png);
  measurements.push({name,view,theme,...metrics});
  assert.equal(metrics.width,width,'browser must use the requested CSS viewport width');
  assert.equal(metrics.height,height,'browser must use the requested CSS viewport height');
  const items=metrics.items;
  if(['normal','history','filters','filtered'].includes(view)){
   for(const id of ['chat-tab','conversation-process','conversation-tools']){const b=items[id];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width,id+' is missing or clipped')}
   assert(!document.getElementById('conversation-all'),'trajectory preset is still present');
  }
  if(view==='onboarding'||view==='desktop'){
   const dialog=items[view==='desktop'?'desktop-dialog':'settings-dialog'];assert(dialog.visible&&dialog.x>=0&&dialog.x+dialog.width<=width+1&&dialog.y>=0&&dialog.y+dialog.height<=height+1,'new feature dialog exceeds viewport');assert(dialog.scrollWidth<=dialog.clientWidth+1,'new feature dialog overflows horizontally');
   if(view==='desktop')assert(items['desktop-stop'].visible&&items['desktop-stop'].y+items['desktop-stop'].height<=height,'desktop takeover button is clipped');
  }
  if(view==='history')assert(items['conversation-older'].visible,'older history has no visible entry');
  if(view==='composerlarge'){assert(items.message.height>=270,'composer resize height was clamped to its old limit');assert(items.conversation.height>=100,'enlarged draft eliminates readable conversation');assert(items.send.y+items.send.height<=height,'resized draft pushes send action off screen')}
  assert(metrics.bodyWidth<=metrics.width+1,'page overflows horizontally');
  if(view==='live'||view.startsWith('questions')){
   for(const id of ['live-steer','live-interrupt','send','stop']){const b=items[id];assert(b.visible&&b.x>=0&&b.x+b.width<=width&&b.y+b.height<=height,id+' is clipped');if(width<=760)assert(b.height>=44,id+' is too short for touch')}
   assert(items.conversation.height>=60,'interactive controls eliminate all conversation space');
   if(view.startsWith('questions')){const panel=items['codex-approvals'],submit=items['preview-answer-submit'];assert(panel.visible&&panel.height>=150&&panel.height<height*(view.endsWith('full')?1:.5),'question panel has no bounded reading space');assert(panel.x>=0&&panel.x+panel.width<=width&&panel.y>=0&&panel.y+panel.height<=height,'expanded questions exceed the viewport');assert(panel.scrollWidth<=panel.clientWidth+1,'question choices overflow horizontally');assert(submit.y>=panel.y&&submit.y+submit.height<=panel.y+panel.height,'question submit is outside the visible card');if(width<=760)assert(items['preview-answer-option'].height>=44,'question option has a small touch target')}
  }
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
   for(const id of ['library-task','attach-open','command-open','message-mode','task-model-button','send','task-handoff'])assert(items[id].height>=44&&items[id].width>=44,id+' touch target is too small');
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
  if(['normal','long','header','headernarrow','appmenu','home','context','create'].includes(view)){
   const controls=['settings-open','theme-toggle','library-open','logout'];
   if(!['home','create'].includes(view))controls.push('session-reset','task-handoff','bind-open','scratch-tab');
   for(const id of controls){const b=items[id];assert(b.visible&&b.width>0&&b.y+b.height<=height&&b.x>=0&&b.x+b.width<=width,'direct header action is clipped: '+id);if(width<=760)assert(b.height>=44&&b.width>=44,id+' touch target is too small')}
  }
  if(view.startsWith('handoff')){
   if(view.startsWith('handofflegacy')){const b=items['handoff-preserve-legacy'];assert(b.visible&&b.width>=16&&b.width<=32,'legacy confirmation must be a readable checkbox')}
   assert(metrics.handoffFooterBackground&&!['transparent','rgba(0, 0, 0, 0)'].includes(metrics.handoffFooterBackground),'fixed handoff actions must have an opaque background');
   const box=items['handoff-dialog'];assert(box.visible&&box.x>=0&&box.x+box.width<=width&&box.y>=0&&box.y+box.height<=height,'engine switch dialog exceeds viewport');assert(box.scrollWidth<=box.clientWidth+1,'engine switch overflows horizontally');
   for(const id of ['handoff-engine','handoff-profile','handoff-model','handoff-mode']){const b=items[id];assert(b.width>90&&b.x>=box.x&&b.x+b.width<=box.x+box.width,'engine switch field clipped: '+id);if(width<=760)assert(b.height>=44,'engine switch touch field too small: '+id)}
   if(view.endsWith('bottom')||width>760)for(const id of ['handoff-submit','handoff-cancel']){const b=items[id];assert(b.visible&&b.y>=box.y&&b.y+b.height<=box.y+box.height,'engine switch confirmation unavailable: '+id)}
  }
  if(view==='menu'){const b=items['session-info-dialog'];assert(b.visible&&b.y>=0&&b.y+b.height<=height&&b.x>=0&&b.x+b.width<=width,'session information exceeds viewport')}
  if(view==='createbottom')for(const id of ['library-create','create-submit']){
   const box=items[id];assert(box.visible&&box.y>=0&&box.y+box.height<=height&&box.x+box.width<=width,id+' is outside the scrolled create page');
  }
 }catch(error){failures.push(name+': '+error.message)}
}
}finally{await renderer.close()}
const before=measurements.find(x=>x.view==='before'),after=measurements.find(x=>x.name==='normal-light-1280');
if(before&&after){if(after.items.conversation.height<before.items.conversation.height-2)failures.push('desktop conversation lost vertical space');if(after.items['task-list'].y>before.items['task-list'].y+1)failures.push('sidebar navigation became taller');}
const report={source:process.env.GITHUB_SHA||'local',baseRef,measurements,failures};
await fs.writeFile(path.join(output,'geometry.json'),JSON.stringify(report,null,2));
const summary=JSON.stringify(measurements.map(x=>({name:x.name,width:x.width,height:x.height,chatHeight:x.items.conversation?.height})));
console.log('::notice title=Conversation layout measurements::'+summary.replaceAll('%','%25').replaceAll('\n','%0A'));
for(const failure of failures)console.log('::error title=Conversation layout regression::'+failure.replaceAll('%','%25').replaceAll('\r','%0D').replaceAll('\n','%0A'));
if(failures.length)process.exitCode=1;
