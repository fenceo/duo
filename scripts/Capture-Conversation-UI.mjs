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
const sourceFiles=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','account-import','account-center','engines','handoff','desktop','usage','app','panels'];
const baseRef=process.env.DUO_UI_BASE||'v0.23.2';
for(const file of [...sourceFiles.map(name=>name+'.ts'),'style.css','workbench.css']){
 const result=spawnSync('git',['show',baseRef+':web/'+file],{encoding:'utf8',windowsHide:true});
 if(result.status!==0&&!['handoff.ts','desktop.ts','account-import.ts','account-center.ts','usage.ts'].includes(file))throw Error('Cannot read UI comparison baseline: '+baseRef);
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
cases.push(['handoffrecovery','light',1280,800],['handoffrecovery','dark',390,844],['handoffrecoverybottom','light',320,844]);
cases.push(['handofflegacy','light',1280,800],['handofflegacy','dark',390,844],['handofflegacybottom','light',320,844]);
cases.push(['accountmanager','light',1280,800],['accountmanager','dark',390,844],['accountmanager','light',320,844]);
cases.push(['accountimport','light',1280,800],['accountimport','dark',390,844],['accountimport','light',320,844]);
cases.push(['onboarding','light',1280,800],['onboarding','dark',390,844],['onboarding','light',320,844],['desktop','light',1280,800],['desktop','dark',390,844],['desktop','light',320,844]);
cases.push(['accountquota','light',1280,800],['accountquota','dark',390,844],['accountquota','light',320,844],['accounterror','dark',320,844]);
cases.push(['accountlist','light',1280,800],['accountlist','dark',390,844],['accountfilter','light',320,844]);
cases.push(['accounts','light',1280,800],['accounts','dark',390,844],['accounts','light',320,844]);
cases.push(['accountsetup','light',1280,800],['accountsetup','dark',390,844],['accountsetup','light',320,844]);
cases.push(['claudeapi','light',1280,800],['claudeapi','dark',390,844],['claudeapi','light',320,844],['claudemimoapi','light',1280,800],['claudemimoapi','dark',390,844],['claudemimoapi','light',320,844],['mimoapi','light',1280,800],['mimoapi','dark',390,844],['mimoapi','light',320,844]);
cases.push(['claudescope','light',1280,800],['claudescope','dark',390,844],['claudescope','light',320,844],['clauderoles','light',1280,800],['clauderoles','dark',390,844],['clauderoles','light',320,844]);
cases.push(['mimoconversation','light',1280,800],['mimoconversation','dark',390,844],['mimoconversation','light',320,844]);
cases.push(['mimomodels','light',1280,800],['mimomodels','dark',390,844],['mimomodels','light',320,844]);
cases.push(['modellatency','light',1280,800],['modellatency','dark',390,844],['modellatency','light',320,844]);
cases.push(['questionssplit','light',1280,800],['questionstools','dark',390,844],['questionstools','light',1280,800],['questionsasync','light',390,844],['questionsasync','dark',320,844]);
cases.push(['usage','light',1280,800],['usage','dark',390,844],['usage','light',320,844],['queued','light',1280,800],['queued','dark',390,844]);
cases.push(['desktopmanual','light',1280,800],['desktopmanual','dark',390,844],['desktopmanual','light',320,844]);
cases.push(['harnessapi','light',1280,800],['harnessapi','dark',390,844],['harnessapi','light',320,844]);
cases.push(['harnesscreate','light',1280,800],['harnesscreate','dark',390,844],['harnesscreate','light',320,844],['harnesspermission','light',1280,800],['harnesspermission','dark',390,844],['harnesspermission','light',320,844]);
cases.push(['harnessmode','light',1280,800],['harnessmode','dark',390,844],['harnessmode','light',320,844],['harnessmodelive','dark',390,844]);
cases.push(['harnessoutput','light',1280,800],['harnessoutput','dark',390,844],['harnessoutput','light',320,844],['harnessknowledge','light',1280,800],['harnessknowledge','dark',390,844]);
const measurements=[],failures=[];
for(const view of ['editorwindows','editorwsl','editorssh'])cases.push([view,'light',1280,800],[view,'dark',320,844]);
cases.push(['editorfiles','light',1280,800],['editorfiles','dark',390,844],['editorfiles','light',320,844]);
if(process.env.DUO_UI_VIEWS){const requested=new Set(process.env.DUO_UI_VIEWS.split(','));const selected=cases.filter(([view])=>requested.has(view));if(!selected.length)throw Error('No matching synthetic UI views');cases.splice(0,cases.length,...selected)}
const renderer=await startFixtureBrowser(browser,output);
try{
for(const [view,theme,width,height] of cases){
 const name=`${view}-${theme}-${width}`;
 const webRoot=view==='before'?pathToFileURL(baseline+path.sep):new URL('../web/',import.meta.url);
 const {document,ctx}=await createWebShellFixture(webRoot);
 if(view.startsWith('harness')&&view!=='harnessapi')runInContext("workCatalog.modes.push({id:'harness:workspace',name:'Harness 工作区权限',permission:'workspace',approval:'request',builtin:true,allow_network:true},{id:'full',name:'完全访问',permission:'full',approval:'never',builtin:true,allow_network:true});",ctx);
 runInContext(`
  chosen='layout-preview';
  detail={task:{id:chosen,title:'Duo 优化',workspace:'C:/Projects/duo',environment:{type:'windows',name:'本机 Windows'},engine:'codex',model:'gpt-6-astra',reasoning_effort:'high',session:'synthetic-session',status:'done',archived:false,updated:1},runs:[{id:'r1',kind:'chat',status:'done',input:'优化对话页面，让常用操作更直接，留出更多阅读空间。',result:'已完成对话页面优化。\\n\\n- 标题与工作目录放在同一行。\\n- 本任务知识可以直接打开。\\n- 引用历史和知识并入输入框底部。\\n\\n会话信息与低频操作集中在右上角。',created:1,finished:2}],events:[],session_started:1790310000000};
  tasks=[detail.task,...Array.from({length:7},(_,i)=>({...detail.task,id:'task-'+i,title:['检查串口连接','整理发布说明','修复页面交互','准备下一次验证'][i%4],workspace:i<3?'C:/Projects/duo':'C:/Projects/demo'}))];renderTask();renderList();
  for(const id of ['tabs','task-actions','composer-wrap','conversation-filter'])element(id).classList.remove('hidden');
  element('conversation').innerHTML='';appendEvents([{seq:1,task_id:chosen,run_id:'r1',kind:'user',text:detail.runs[0].input},{seq:2,task_id:chosen,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);
 `,ctx);
 if(view.startsWith('editor')&&view!=='editorfiles'){
  if(view==='editorwsl')runInContext("detail.task.workspace='/home/developer/project with spaces';detail.task.environment={id:'editor-wsl',type:'wsl',name:'WSL 项目',distro:'Ubuntu',user:'developer'};",ctx);
  if(view==='editorssh')runInContext("detail.task.workspace='/srv/projects/project-with-a-long-name';detail.task.environment={id:'editor-ssh',type:'ssh',name:'SSH 项目',host:'example.invalid',user:'developer',port:2222};",ctx);
  const savedAPI=ctx.api;ctx.api=async()=>runInContext('detail',ctx);await ctx.openEditor('layout-preview');ctx.api=savedAPI;
 }
 if(view==='editorfiles'){
  const savedAPI=ctx.api;ctx.api=async()=>({items:[{name:'app.ts',path:'web/app.ts',size:256,directory:false}],content:'// Synthetic file preview\nexport const example = 1;',size:256});
  ctx.matchMedia=()=>({matches:width<=760,addEventListener(){},removeEventListener(){}});
  runInContext("switchTab('files');syncDocks();",ctx);await ctx.loadFiles();await ctx.previewFile('web/app.ts','read');ctx.api=savedAPI;
 }
 if(view==='harnesscreate'){
  runInContext("for(const id of ['tabs','task-actions','conversation','composer-wrap','session-banner'])element(id).classList.add('hidden');setCreatePageVisible(true);setCreateSubmitState('idle');input('create-engine').value='deepseek-harness';modeOptions(input('create-mode'));setCreatePermission('request');element('task-title').textContent='新建任务';",ctx);
  document.querySelector('[data-create-permission="request"]').id='preview-answer-submit';
  const scroll=document.createElement('script');scroll.textContent="window.addEventListener('load',()=>document.getElementById('preview-answer-submit').scrollIntoView({block:'center'}));";document.body.append(scroll);
 }
 if(view==='modellatency'){
  runInContext("detail.task.engine='codex';detail.task.model='gpt-6-astra';if(!settings.config.environments.some(e=>e.id===detail.task.environment.id))settings.config.environments.push(detail.task.environment);taskPickerModels=[{id:'gpt-6-astra',name:'GPT-6 Astra'}];modelCatalogState.task={key:taskCatalogContextKey(detail.task),loading:false,failed:false,summary:'已读取 1 个模型',details:'合成延时结果，不调用真实模型',defaultModel:'gpt-6-astra'};modelProbeStates.task={key:taskCatalogContextKey(detail.task),models:['gpt-6-astra'],rows:{'gpt-6-astra':{status:'available',first_response_ms:65000,duration_ms:68000,timeout_seconds:180,timing_source:'stream_text'}},summary:'首次可见回复含 CLI 连接与重试，非网络首包时间。',busy:false,mode:'latency',timeout_seconds:180};renderTask();renderModelMenu('task');renderModelProbeText('task');element('task-model-menu').classList.remove('hidden');button('task-model-button').setAttribute('aria-expanded','true');",ctx);
  assert.match(document.getElementById('task-model-test-result').textContent,/65\.00/,'latency fixture must display the first-reply measurement');
  const scroll=document.createElement('script');scroll.textContent="window.addEventListener('load',()=>document.getElementById('task-model-latency-controls').scrollIntoView({block:'center'}));";document.body.append(scroll);
 }
 if(view==='mimoconversation'){
  runInContext("detail.task.engine='mimo';detail.task.model='duo-mimo/mimo-v2.5-pro';detail.runs[0].engine='mimo';detail.runs[0].model=detail.task.model;detail.runs[0].result='已检查配置，原生会话与模型切换正常。';detail.runs[0].usage={input:120,output:32,cached:0,cache_write:0,total:152};resetConversation();sequence=0;element('conversation').replaceChildren();conversationFilter={tools:true,process:true};appendEvents([{seq:1,run_id:'r1',kind:'user',text:'检查模型切换与原生记录'},{seq:2,run_id:'r1',kind:'thought',text:'先核对模型路由，'},{seq:3,run_id:'r1',kind:'thought',text:'再读取原生会话结果。'},{seq:4,run_id:'r1',kind:'assistant',text:'准备'},{seq:5,run_id:'r1',kind:'assistant',text:'准备检查配置。'},{seq:6,run_id:'r1',kind:'tool',text:'read\\n{\"file_path\":\"fixture/config.json\"}'},{seq:7,run_id:'r1',kind:'thought',text:'记录显示模型已经切换，'},{seq:8,run_id:'r1',kind:'thought',text:'继续核对最终回复。'},{seq:9,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);renderTask();",ctx);
  assert.equal(document.querySelectorAll('.message.thought').length,2,'MiMo thought deltas must join');
  assert.match(document.querySelector('.message.thought').textContent,/先核对模型路由，再读取原生会话结果/,'MiMo thought prefix lost');
  assert.equal(document.querySelectorAll('.turn-output .assistant').length,1,'MiMo final reply duplicated');
 }
 if(view==='mimomodels'){
  runInContext("detail.task.engine='mimo';detail.task.model='duo-mimo/mimo-v2.5';detail.task.environment.mimo='fixture-mimo';settings.config.environments.push(detail.task.environment);taskPickerModels=['mimo-v2.5','mimo-v2.5-pro','mimo-v2.6-flash','mimo-v2.6-pro'].map(id=>({id:'duo-mimo/'+id,name:id,engine:'mimo'}));modelCatalogState.task={key:taskCatalogContextKey(detail.task),loading:false,failed:false,summary:'已读取 4 个模型',details:'来源：MiMo API 实时模型目录。服务端共 8 项，含 4 个专用语音型号。',apiTarget:'synthetic-api-target',apiEndpoint:'https://token-plan-cn.xiaomimimo.com/v1'};modelProbeStates.task={key:taskCatalogContextKey(detail.task),models:taskPickerModels.map(m=>m.id),rows:Object.fromEntries(taskPickerModels.map((m,i)=>[m.id,{status:i===0?'available':'unverified',message:i===0?'API 文本调用成功；CLI 工具能力未验证':'尚未测试'}])),summary:'合成 API 测试结果，不调用真实模型',busy:false};renderTask();renderModelMenu('task');element('task-model-menu').classList.remove('hidden');button('task-model-button').setAttribute('aria-expanded','true');",ctx);
  assert.equal(document.getElementById('task-test-models').disabled,false,'MiMo API probe still disabled');
 }
 if(view==='harnesspermission'){
  runInContext("detail.task.engine='deepseek-harness';detail.task.mode={id:'harness:workspace',permission:'workspace',approval:'request'};detail.approvals=[{id:'synthetic-permission',task_id:chosen,run_id:'r1',method:'harness/requestPermission',created:1,params:{sessionId:'synthetic',toolCall:{toolCallId:'synthetic-tool',title:'bash',rawInput:{command:'printf synthetic-marker',sandbox_permissions:'danger-full-access',justification:'需要在工作区外执行本次操作。'}},options:[{optionId:'allow-once',kind:'allow_once'},{optionId:'reject-once',kind:'reject_once'}]}}];renderTask();",ctx);
  document.querySelector('.codex-approval-actions button').id='preview-answer-submit';
 }
 if(view==='harnessmode'||view==='harnessmodelive'){
  runInContext("detail.task.engine='deepseek-harness';detail.task.mode={id:'harness:workspace',name:'Harness 工作区权限',permission:'workspace',approval:'request'};if(!workCatalog.modes.some(m=>m.id==='harness:workspace'))workCatalog.modes.push(detail.task.mode);renderTask();input('message-mode').value='full';input('message-mode').onchange();",ctx);
  if(view==='harnessmodelive')runInContext("detail.runs[0].status='running';renderTask();",ctx);
  assert.equal(document.getElementById('message-mode').disabled,false,'Harness next-turn permission selector disabled');
  assert.match(document.getElementById('mode-engine-hint').textContent,/完全访问/);
 }
 if(view==='harnessoutput'){
  runInContext("detail.task.engine=detail.runs[0].engine='deepseek-harness';detail.runs[0].context={used:34560,size:128000};detail.runs[0].images=[{id:'synthetic',mime:'image/png',size:512,source:'assistant',width:480,height:300}];resetConversation();sequence=0;element('conversation').replaceChildren();appendEvents([{seq:1,run_id:'r1',kind:'user',text:detail.runs[0].input},{seq:2,run_id:'r1',kind:'assistant',text:detail.runs[0].result}]);renderTask();",ctx);
  const preview=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="480" height="300"><rect width="480" height="300" fill="#dce6f0"/><rect x="36" y="42" width="408" height="216" rx="18" fill="#fff"/><text x="64" y="112" font-size="24" fill="#26323f">Synthetic output image</text><path d="M64 212 L140 170 L240 194 L350 140" fill="none" stroke="#4269ea" stroke-width="6"/></svg>').toString('base64');
  document.querySelector('.turn-images img').src='data:image/svg+xml;base64,'+preview;
  const scroll=document.createElement('script');scroll.textContent="window.addEventListener('load',()=>{const c=document.getElementById('conversation');c.scrollTop=c.scrollHeight;});";document.body.append(scroll);
 }
 if(view==='harnessknowledge'){
  const baseAPI=ctx.api;ctx.api=async(route,...args)=>route.startsWith('tasks/layout-preview/knowledge')?[]:baseAPI(route,...args);
  runInContext("detail.task.engine='deepseek-harness';renderTask();switchTab('note');syncDocks();",ctx);
  for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
  assert(!document.getElementById('summarize').disabled);
  assert(document.getElementById('notice').textContent.indexOf('Unexpected fixture request')===-1);
 }
 if(view==='desktopmanual'){
  runInContext(`desktopDialogTask=chosen;desktopHumanToken='synthetic';desktopState={active:true,task_id:chosen,target:'desktop',control:false,human_control:true};desktopFrame={id:'synthetic',bounds:{width:1280,height:720}};input('desktop-target').innerHTML='<option>合成测试窗口</option>';renderDesktopSharing();element('desktop-dialog').showModal();`,ctx);
  const img=document.getElementById('desktop-preview-image');img.classList.remove('hidden');img.src='data:image/svg+xml;base64,'+Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="720"><rect width="1280" height="720" fill="#dce6f0"/><rect x="160" y="90" width="960" height="520" rx="20" fill="#fff"/><text x="220" y="200" font-size="38" fill="#26323f">Synthetic desktop preview</text><rect x="220" y="280" width="220" height="64" rx="12" fill="#4269ea"/><text x="250" y="322" font-size="28" fill="#fff">Test button</text></svg>').toString('base64');
 }
 if(view==='usage'){
  const totals={runs:12,reported:11,missing:1,running:1,input:287672024,output:1275024,total:288947048};
  const models=[{...totals,model:'synthetic-model-name',engines:['claude','codex']}];
  ctx.api=async()=>({timezone:'Asia/Shanghai',generated:1790740800000,total:totals,period:totals,days:Array.from({length:7},(_,i)=>({...totals,date:'2026-09-'+(30-i),models}))});
  await ctx.openUsageDashboard();
 }
 if(view==='queued'){
  runInContext(`detail.task.status='running';detail.runs[0].status='running';detail.runs.push({id:'queued-preview',kind:'chat',status:'queued',input:'这条要求可以在执行前撤销，保留原文和附件。',created:3});conversationTurn('queued-preview');renderTask();applyConversationFilter(false);`,ctx);
  document.querySelector('[data-cancel-run]').id='preview-cancel-queue';
 }
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
   if(view==='questionsasync')runInContext("detail.task.status='done';detail.runs[0].status='done';detail.interaction=null;detail.approvals[0]={...detail.approvals[0],id:'preview-async',method:'duo/asyncQuestion'};renderTask()",ctx);
   const option=document.querySelector('.codex-answer-option');option.id='preview-answer-option';
   document.querySelector('.codex-approval-actions button').id='preview-answer-submit';
   if(view.endsWith('full'))document.getElementById('codex-approvals-expand').onclick();
   if(view==='questionssplit'||view==='questionstools'){
    ctx.loadKnowledge=async()=>{};
    ctx.matchMedia=()=>({matches:width<=760,addEventListener(){},removeEventListener(){}});
    runInContext(`toolFullscreen=${view==='questionstools'&&width>760};switchTab('note');syncDocks();positionCodexApprovals()`,ctx);
   }
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
 if(['accounts','accountlist','accountfilter','accountquota','accounterror','accountimport','accountmanager','accountsetup'].includes(view)){
  runInContext("settings.config.environments.push({id:'local',name:'Windows',type:'windows',codex:'fixture',workspaces:['C:/fixture']});populateEngineOnboarding();engineCatalog={engines:[{id:'codex',name:'Codex',credential_kinds:['codex_home']},{id:'claude',name:'Claude Code',credential_kinds:['claude_home']}],profiles:[{id:'fixture-profile',name:'工作账号 / API',engine:'claude',environment_id:'local',kind:'claude_home',reference:'C:/fixture/claude-account'}],active_profile:{}};renderAccountCenter();populateEngineProfileForm();element('account-center-dialog').setAttribute('open','');element('engine-account-fields').querySelector('details').open=true;",ctx);
 }
 if(view==='accountsetup'){
  runInContext("settings.config.environments.push({id:'remote',name:'开发服务器 · SSH',type:'ssh',host:'fixture.invalid',user:'fixture',workspaces:['/fixture']});populateEngineOnboarding();element('account-center-editor-wrap').open=true;element('engine-account-login').value='apiKey';element('engine-account-login').onchange();",ctx);
  const scroll=document.createElement('script');scroll.textContent="window.addEventListener('load',()=>document.getElementById('engine-account-environment').scrollIntoView({block:'center'}));";document.body.append(scroll);
 }
 if(['claudeapi','claudemimoapi','mimoapi','claudescope','clauderoles'].includes(view)){
  const official=view==='claudeapi';ctx.apiPreview={environment_id:'local',target:'fixture-target',engine:view==='mimoapi'?'mimo':'claude',api_protocol:view==='mimoapi'?'openai-chat':'anthropic-messages',base_url:official?'':'https://token-plan-cn.xiaomimimo.com'+(view==='mimoapi'?'/v1':'/anthropic'),model:official?'':'mimo-v2.6-pro',auth_mode:official?'api-key':'auth-token',key_configured:!official,models:[]};
  runInContext("element('environment-api-dialog').setAttribute('open','');renderEnvironmentAPI(apiPreview);element('environment-api-target').textContent='目标环境：Fixture WSL（wsl / Ubuntu-22.04）';",ctx);
  if(['claudescope','clauderoles'].includes(view))runInContext("input('environment-api-scope').value='duo';updateEnvironmentAPIScopeHint();",ctx);
  if(view==='clauderoles')runInContext("element('environment-api-claude-roles').open=true;input('environment-api-role-mode').value='custom';updateEnvironmentAPIRoleFields();for(const [id,value] of Object.entries({opus:'gateway/reasoning-model',sonnet:'gateway/coding-model',haiku:'gateway/fast-model',fable:'gateway/long-context-model',subagent:'gateway/worker-model'}))input('environment-api-role-'+id).value=value;",ctx);
  const focus=view==='claudescope'?'environment-api-scope':view==='clauderoles'?'environment-api-role-haiku':'environment-api-protocol';
  const scroll=document.createElement('script');scroll.textContent="window.addEventListener('load',()=>{const d=document.getElementById('environment-api-dialog');d.removeAttribute('open');d.showModal();document.getElementById("+JSON.stringify(focus)+").scrollIntoView({block:'center'});});";document.body.append(scroll);
 }
 if(['accounts','accountlist','accountfilter','accountquota','accounterror'].includes(view)){
  runInContext("settings.config.environments.push({id:'remote',name:'开发服务器',type:'ssh',host:'fixture.invalid',user:'fixture',workspaces:['/fixture']});engineCatalog.profiles=[{id:'work',name:'工作账号 · Codex',engine:'codex',environment_id:'local',kind:'codex_home',reference:'C:/fixture/account-a',created:1790740800000,updated:1790740800000},{id:'backup',name:'备用账号 · 测试长名称在窄屏和卡片中的完整换行显示',engine:'codex',environment_id:'local',kind:'codex_home',reference:'C:/fixture/account-b',created:1790740700000,updated:1790740700000},{id:'relay',name:'Relay · Claude API',engine:'claude',environment_id:'remote',kind:'claude_home',reference:'/fixture/account-c',created:1790740600000,updated:1790740600000}];engineCatalog.active_profile={'local:codex':'work','remote:claude':'relay'};renderAccountCenter();",ctx);
  if(view==='accountquota'||view==='accounterror')runInContext("engineCatalog.accounts={work:{email:'work@example.invalid',display_name:'工作账号',account_id:'workspace-fixture',plan:'pro',auth_type:'chatgpt',state:'ready',identity_updated:Date.now(),quota_updated:Date.now(),message:'账号与额度已刷新。',limits:[{name:'codex',used_percent:25,window_minutes:300,resets_at:1900000000},{name:'codex',used_percent:77,window_minutes:10080,resets_at:1900600000}]},backup:{state:'api',auth_type:'apikey',provider:'团队中转站',base_url:'https://relay.example.invalid/v1',message:'API 账号不支持订阅额度查询。'}};engineCatalog.organization={work:{note:'日常开发，按任务选择环境',tags:['工作','主要']}};renderAccountCenter();",ctx);
  if(view==='accounterror')runInContext("engineCatalog.accounts.work.state='error';engineCatalog.accounts.work.message='目标环境连接失败，请检查 CLI 版本和登录状态。';renderAccountCenter()",ctx);
  if(view==='accountlist')runInContext("accountCenterView='list';renderAccountCenter()",ctx);
  if(view==='accountfilter')runInContext("input('account-search').value='Relay';renderAccountCenter()",ctx);
 }
 if(view==='accountimport'){runInContext("element('account-center-editor-wrap').open=true;element('engine-account-fields').querySelector('details').open=false;element('account-center-editor').querySelector('.account-import').open=true;element('account-import-source').value='json';element('account-import-source').onchange();",ctx)}
 if(view==='accountmanager'){runInContext("element('account-center-editor-wrap').open=true;element('engine-account-fields').querySelector('details').open=false;element('account-center-editor').querySelector('.account-import').open=true;element('account-import-source').value='manager';element('account-import-source').onchange();accountManagerPreview={engine:'codex',json:'',file:{},items:[{index:0,name:'工作账号 / 订阅',kind:'订阅登录',valid:true},{index:1,name:'备用中转站 / API',kind:'API / 中转站',valid:true},{index:2,name:'需另行配置的账号',kind:'',valid:false,error:'此账号依赖管理器网关，请导出直接连接配置'}]};renderAccountManagerPreview();element('account-import-result').textContent='识别到 3 个条目，其中 2 个可导入。请确认勾选。';",ctx)}
 if(view==='harnessapi'){runInContext("renderHarnessAPI({environment_id:'fixture',target:'fixture-target',api:'openai-completions',base_url:'https://api.deepseek.com',model:'deepseek-chat',key_configured:true,models:[{id:'deepseek-chat'},{id:'deepseek-reasoner'}]});element('harness-api-target').textContent='目标环境：WSL Ubuntu（Ubuntu-22.04 / dev）';element('harness-api-status').textContent='接口认证通过，已读取 2 个模型；未发送推理请求。';element('harness-api-dialog').setAttribute('open','');",ctx)}
 if(view==='onboarding'){
  runInContext("settings.config.environments.unshift({id:'local',name:'Windows',type:'windows',codex:'fixture',workspaces:['C:/fixture']});populateEngineOnboarding();engineCatalog={engines:[{id:'codex',name:'Codex',targets:['windows','wsl','ssh'],auto_install:true,runnable:true},{id:'claude',name:'Claude Code',targets:['windows','wsl','ssh'],auto_install:true,runnable:true}],profiles:[],active_profile:{}};engineStatusSignature=engineEnvironmentSignature();engineEnvironmentStatuses=[{environment_id:'local',codex:{path:'C:/fixture/tools/codex.exe',state:'installed',label:'已安装',version:'0.165.0'},claude:{path:'',state:'missing',label:'未安装'}}];renderEngineCatalog();element('settings-dialog').setAttribute('open','');document.querySelectorAll('.settings-section').forEach(e=>e.classList.add('hidden'));element('settings-environment').classList.remove('hidden');element('engine-account-login').value='apiKey';element('engine-account-login').onchange();",ctx);
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
   if(route.includes('/continuation/preview'))return {source_task_id:snapshot.task.id,source_title:snapshot.task.title,source_engine:'codex',transferred_runs:route.endsWith('mode=brief')?3:27,transferred_knowledge:3,archive_bytes:route.endsWith('mode=brief')?8192:102400,fingerprint:'fixture-preview',context_truncated:true,context:route.endsWith('mode=brief')?'任务共识：保留聊天记录，使用有限接续资料。最近完成：合成回归通过。待确认：原会话压缩失败后的真实接续效果。需要遗漏细节时请检查原聊天和工作区。':'任务目标：继续完成页面优化。已有验证：合成回归通过。未解决：真实模型接续效果尚未验证。完整历史包含 27 轮记录。'};
   if(route.includes('/models?'))return {models:[{id:'gpt-6-astra',name:'gpt-6-astra'}],source:'本地配置',status:'ready',modified:0};
   throw Error('Unexpected switch preview request '+route);
  };
  await ctx.openHandoff();await new Promise(resolve=>setImmediate(resolve));
  if(view.startsWith('handoffrecovery')){const restart=document.getElementById('handoff-restart-session');restart.checked=true;await restart.onchange();await new Promise(resolve=>setImmediate(resolve))}
 }
 const style=document.createElement('style');style.textContent=(await fs.readFile(new URL('style.css',webRoot),'utf8'))+'\n'+(await fs.readFile(new URL('workbench.css',webRoot),'utf8'));document.head.append(style);
 const meta=document.createElement('meta');meta.name='viewport';meta.content='width=device-width,initial-scale=1';document.head.append(meta);
 document.documentElement.dataset.theme=theme;
 if(view.startsWith('questions')&&view.endsWith('full')){const open=document.createElement('script');open.textContent="window.addEventListener('load',()=>{const d=document.getElementById('codex-requests-dialog');d.removeAttribute('open');d.showModal()})";document.body.append(open)}
 ctx.positionCodexApprovals?.();
 const script=document.createElement('script');script.textContent=`window.addEventListener('load',()=>{for(const field of document.querySelectorAll('[data-note-input]')){field.style.height='auto';field.style.height=Math.max(32,Math.min(96,field.scrollHeight))+'px'}if(${JSON.stringify(view)}==='createbottom'){const page=document.getElementById('create-page');page.scrollTop=page.scrollHeight}if(${JSON.stringify(view)}==='settings'||${JSON.stringify(view)}.startsWith('library')){const dialog=document.getElementById(${JSON.stringify(view)}==='settings'?'settings-dialog':'library-dialog');dialog.removeAttribute('open');dialog.showModal()}if(${JSON.stringify(view)}==='menu'){const dialog=document.getElementById('session-info-dialog');dialog.removeAttribute('open');dialog.showModal()}if(${JSON.stringify(view)}.startsWith('handoff')){const dialog=document.getElementById('handoff-dialog');dialog.removeAttribute('open');dialog.showModal();if(${JSON.stringify(view)}.endsWith('bottom'))dialog.scrollTop=dialog.scrollHeight}if(['onboarding','desktop','accounts','accountlist','accountfilter','accountquota','accounterror','accountimport','accountmanager','accountsetup'].includes(${JSON.stringify(view)})){const d=document.getElementById(${JSON.stringify(view)}==='onboarding'?'settings-dialog':${JSON.stringify(view)}.startsWith('account')?'account-center-dialog':'desktop-dialog');d.removeAttribute('open');d.showModal();if(${JSON.stringify(view)}==='accountsetup')document.getElementById('engine-account-environment').scrollIntoView({block:'center'});if(${JSON.stringify(view)}==='accountmanager')document.getElementById('account-manager-submit').scrollIntoView({block:'end'})}if(${JSON.stringify(view)}==='harnessapi'){const d=document.getElementById('harness-api-dialog');d.removeAttribute('open');d.showModal()}if(${JSON.stringify(view)}==='usage'){const dialog=document.getElementById('usage-dialog');dialog.removeAttribute('open');dialog.showModal()}if(${JSON.stringify(view)}==='desktopmanual'){const d=document.getElementById('desktop-dialog');d.removeAttribute('open');d.showModal();d.scrollTop=d.scrollHeight}if(${JSON.stringify(view)}.startsWith('editor')&&${JSON.stringify(view)}!=='editorfiles'){const d=document.getElementById('editor-dialog');d.removeAttribute('open');d.showModal()}const ids=['task-model-menu','task-test-model-latency','task-model-test-timeout','task-model-test-result','environment-api-dialog','environment-api-save','environment-api-close','environment-api-provider','environment-api-protocol','environment-api-discover','environment-api-scope','environment-api-role-mode','environment-api-role-opus','environment-api-role-sonnet','environment-api-role-haiku','environment-api-role-fable','environment-api-role-subagent','editor-dialog','editor-project','editor-alias','editor-confirm','editor-launch','editor-close','files-editor','file-editor','file-editor-line','engine-account-environment','engine-account-engine','engine-account-login','harness-api-dialog','harness-api-fields','harness-api-url','harness-api-model','harness-api-key','harness-api-save','harness-api-close','desktop-manual','desktop-manual-tools','desktop-preview-image','desktop-close','usage-dialog','usage-content','usage-close','usage-refresh','usage-scope','preview-cancel-queue','account-search','account-engine-tabs','account-center-list','account-manager-items','account-manager-submit','account-center-dialog','accounts-open','engines-open','desktop-dialog','desktop-stop','desktop-start','engine-setup-fields','engine-catalog','account-import-fields','account-import-file','settings-dialog','handoff-legacy','handoff-preserve-legacy','handoff-dialog','handoff-engine','handoff-profile','handoff-model','handoff-mode','handoff-submit','handoff-cancel','handoff-preview-status','chat-tab','conversation-process','conversation-tools','conversation-history','conversation-older','live-steer','live-interrupt','stop','codex-approvals','preview-answer-option','preview-answer-submit','task-title','task-workspace','tabs','note-tab','conversation','composer-wrap','composer','message','library-task','attach-open','command-open','message-mode','task-model','task-model-button','send','task-actions','session-info-open','session-info-dialog','session-info-close','session-reset','task-handoff','task-link-copy','bind-open','scratch-tab','session-recover','mode-engine-hint','create-input','library-create','create-submit','library-dialog','library-query','library-workspace','library-results','library-preview-content','library-preview-cite','library-preview-back','knowledge-query','notebook','sidebar','task-list','tasks-active','tasks-archived','trash-open','new-task','search','hardware-panel','device-picker','device-refresh','device-ai','device-console','sticky-board','sticky-body','sticky-list','sticky-new','sticky-save-status','app-version','app-menu-items','logout','settings-open','theme-toggle','library-open'];const firstTask=document.querySelector('.task strong');if(firstTask){firstTask.id='first-task-title';ids.push('first-task-title')}const environment=document.querySelector('.task-environment');if(environment){environment.id='first-task-environment';ids.push('first-task-environment')}const firstNote=document.querySelector('.quick-note');if(firstNote){firstNote.id='first-quick-note';ids.push('first-quick-note')}const data={width:innerWidth,height:innerHeight,bodyWidth:document.body.scrollWidth,handoffFooterBackground:document.querySelector('#handoff-dialog .dialog-footer')?getComputedStyle(document.querySelector('#handoff-dialog .dialog-footer')).backgroundColor:'',items:{}};for(const id of ids){const e=document.getElementById(id);if(e){const r=e.getBoundingClientRect();data.items[id]={x:r.x,y:r.y,width:r.width,height:r.height,scrollWidth:e.scrollWidth,clientWidth:e.clientWidth,visible:!!e.getClientRects().length}}}const pre=document.createElement('pre');pre.id='layout-metrics';pre.hidden=true;pre.textContent=JSON.stringify(data);document.body.append(pre)});`;document.body.append(script);
 // Serialize current checkbox state as well as markup into the static fixture.
 for(const control of document.querySelectorAll('input[type="checkbox"]'))if(typeof control.checked==='boolean')control.toggleAttribute('checked',control.checked);
 for(const control of document.querySelectorAll('fieldset'))if(typeof control.disabled==='boolean')control.toggleAttribute('disabled',control.disabled);
 for(const logo of document.querySelectorAll('img[src="/icon.svg"]'))logo.src=new URL('icon.svg',webRoot).href;
 const html=path.join(output,name+'.html');await fs.writeFile(html,document.toString());
 runInContext('authenticated=false;renewShellScope()',ctx);
 try{
  const {metrics,png}=await renderer.capture(pathToFileURL(html).href,width,height);
  await fs.writeFile(path.join(output,name+'.png'),png);
  measurements.push({name,view,theme,...metrics});
  assert.equal(metrics.width,width,'browser must use the requested CSS viewport width');
  assert.equal(metrics.height,height,'browser must use the requested CSS viewport height');
  const items=metrics.items;
  if(view.startsWith('editor')&&view!=='editorfiles'){
   const dialog=items['editor-dialog'];assert(dialog.visible&&dialog.x>=0&&dialog.x+dialog.width<=width+1&&dialog.y>=0&&dialog.y+dialog.height<=height+1,'editor dialog exceeds viewport');assert(dialog.scrollWidth<=dialog.clientWidth+1,'editor dialog overflows horizontally');
   for(const id of ['editor-confirm','editor-launch','editor-close']){const b=items[id];assert(b.visible&&b.width>=20&&b.x>=0&&b.x+b.width<=width+1&&b.y>=0&&b.y+b.height<=height+1,id+' is inaccessible')}
  }
  if(view==='editorfiles')for(const id of ['files-editor','file-editor','file-editor-line']){const b=items[id];assert(b.visible&&b.width>=44&&b.x>=0&&b.x+b.width<=width+1,id+' is missing or clipped in the file panel')}
  if(['normal','history','filters','filtered'].includes(view)){
   for(const id of ['chat-tab','conversation-process','conversation-tools']){const b=items[id];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width,id+' is missing or clipped')}
   assert(!document.getElementById('conversation-all'),'trajectory preset is still present');
  }
  if(['onboarding','desktop','accounts','accountlist','accountfilter','accountquota','accounterror','accountimport','accountmanager','accountsetup'].includes(view)){
   const dialog=items[view==='desktop'?'desktop-dialog':view.startsWith('account')?'account-center-dialog':'settings-dialog'];assert(dialog.visible&&dialog.x>=0&&dialog.x+dialog.width<=width+1&&dialog.y>=0&&dialog.y+dialog.height<=height+1,'new feature dialog exceeds viewport');assert(dialog.scrollWidth<=dialog.clientWidth+1,'new feature dialog overflows horizontally');
   if(['accounts','accountlist','accountfilter','accountquota','accounterror'].includes(view)){for(const id of ['account-search','account-engine-tabs','account-center-list']){const b=items[id];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width+1,id+' is missing or overflows')}assert(!document.getElementById('detect-local-environments'),'duplicate environment discovery returned')}
   if(view==='accountmanager'){for(const id of ['account-manager-items','account-manager-submit']){const b=items[id];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width+1&&b.scrollWidth<=b.clientWidth+1,id+' overflows')}const submit=items['account-manager-submit'];assert(submit.y>=0&&submit.y+submit.height<=height+1,'batch import action inaccessible after scrolling')}
   if(view==='accountsetup'){for(const id of ['engine-account-environment','engine-account-engine','engine-account-login']){const b=items[id];assert(b.visible&&b.width>=120&&b.x>=0&&b.x+b.width<=width+1,'account setup field is clipped: '+id);if(width<=760)assert(b.height>=44,'account setup touch target is too small: '+id)}const target=items['engine-account-environment'];assert(target.y>=0&&target.y+target.height<=height+1,'account environment cannot be selected')}
   if(view==='desktop')assert(items['desktop-stop'].visible&&items['desktop-stop'].y+items['desktop-stop'].height<=height,'desktop takeover button is clipped');
  }
  if(view==='harnessapi'){const d=items['harness-api-dialog'];assert(d.visible&&d.x>=0&&d.x+d.width<=width+1&&d.y>=0&&d.y+d.height<=height+1,'Harness API dialog exceeds viewport');assert(d.scrollWidth<=d.clientWidth+1,'Harness API dialog overflows horizontally');for(const id of ['harness-api-fields','harness-api-url','harness-api-model','harness-api-key','harness-api-save','harness-api-close']){const b=items[id];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width+1,id+' clipped horizontally')}for(const id of ['harness-api-save','harness-api-close']){const b=items[id];assert(b.y>=0&&b.y+b.height<=height+1,id+' inaccessible')}}
  if(view==='history')assert(items['conversation-older'].visible,'older history has no visible entry');
  if(view==='composerlarge'){assert(items.message.height>=270,'composer resize height was clamped to its old limit');assert(items.conversation.height>=100,'enlarged draft eliminates readable conversation');assert(items.send.y+items.send.height<=height,'resized draft pushes send action off screen')}
  assert(metrics.bodyWidth<=metrics.width+1,'page overflows horizontally');
   if(['claudeapi','claudemimoapi','mimoapi','claudescope','clauderoles'].includes(view)){
   const dialog=items['environment-api-dialog'];assert(dialog.visible&&dialog.x>=0&&dialog.x+dialog.width<=width+1&&dialog.y>=0&&dialog.y+dialog.height<=height+1,'Standalone API dialog exceeds viewport');
   for(const id of ['environment-api-provider','environment-api-protocol','environment-api-discover']){const control=items[id];assert(control?.visible&&control.width>=44&&control.x>=0&&control.x+control.width<=width+1,id+' is clipped or inaccessible')}
   for(const id of ['environment-api-save','environment-api-close']){const control=items[id];assert(control?.visible&&control.y>=dialog.y&&control.y+control.height<=dialog.y+dialog.height+1,id+' is outside the visible dialog');if(width<=560)assert(control.height>=44,id+' is too small for touch')}
   const focus=view==='claudescope'?'environment-api-scope':view==='clauderoles'?'environment-api-role-haiku':'';
   if(focus){const control=items[focus];assert(control?.visible&&control.x>=dialog.x&&control.x+control.width<=dialog.x+dialog.width+1&&control.y>=dialog.y&&control.y+control.height<=dialog.y+dialog.height+1,'Claude advanced control is outside its dialog')}
   if(view==='clauderoles')for(const role of ['opus','sonnet','haiku','fable','subagent']){const control=items['environment-api-role-'+role];assert(control?.visible&&control.scrollWidth<=control.clientWidth+1&&control.x>=dialog.x&&control.x+control.width<=dialog.x+dialog.width+1,'Claude role field overflows horizontally')}
  }
  if(view==='modellatency'){
   for(const id of ['task-test-model-latency','task-model-test-timeout']){const b=items[id];assert(b?.visible&&b.width>=44&&b.height>=32&&b.x>=0&&b.x+b.width<=width+1,'latency test control unavailable: '+id)}
   const result=items['task-model-test-result'];assert(result?.visible&&result.height>=24&&result.scrollWidth<=result.clientWidth+1,'latency result is empty or overflows horizontally');
  }
  if(view==='harnesscreate'||view==='harnesspermission'){const b=items['preview-answer-submit'];assert(b.visible&&b.width>=44&&b.height>=32&&b.x>=0&&b.x+b.width<=width+1&&b.y>=0&&b.y+b.height<=height+1,'Harness permission action inaccessible');if(view==='harnesspermission'){const p=items['codex-approvals'];assert(p.visible&&p.scrollWidth<=p.clientWidth+1&&p.y>=0&&p.y+p.height<=height+1,'Harness approval panel exceeds viewport');assert(b.y>=p.y&&b.y+b.height<=p.y+p.height,'Harness approve button clipped by its own panel')}}
  if(view==='live'||view.startsWith('questions')){
   const controls=view==='questionstools'?[]:view==='questionsasync'?['send']:['live-steer','live-interrupt','send','stop'];
   for(const id of controls){const b=items[id];assert(b.visible&&b.x>=0&&b.x+b.width<=width&&b.y+b.height<=height,id+' is clipped');if(width<=760)assert(b.height>=44,id+' is too short for touch')}
   if(view!=='questionstools')assert(items.conversation.height>=60,'interactive controls eliminate all conversation space');
   if(view.startsWith('questions')){const panel=items['codex-approvals'],submit=items['preview-answer-submit'];assert(panel.visible&&panel.height>=150&&panel.height<height*(view.endsWith('full')?1:.5),'question panel has no bounded reading space');assert(panel.x>=0&&panel.x+panel.width<=width&&panel.y>=0&&panel.y+panel.height<=height,'expanded questions exceed the viewport');assert(panel.scrollWidth<=panel.clientWidth+1,'question choices overflow horizontally');assert(submit.y>=panel.y&&submit.y+submit.height<=panel.y+panel.height,'question submit is outside the visible card');if(width<=760)assert(items['preview-answer-option'].height>=44,'question option has a small touch target')}
   if(view.startsWith('questions')&&!view.endsWith('full')&&view!=='questionstools'){const panel=items['codex-approvals'],composer=items['composer-wrap'];assert(composer.y-panel.y-panel.height>=-1&&composer.y-panel.y-panel.height<=10,'question card is not directly above the composer');assert(panel.y>=items.conversation.y+items.conversation.height-1,'question card overlaps the conversation')}
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
  if(view==='usage'){
   const box=items['usage-dialog'];assert(box.visible&&box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1,'usage dialog exceeds viewport');assert(box.scrollWidth<=box.clientWidth+1,'usage table should scroll within its wrapper');
   for(const id of ['usage-close','usage-refresh','usage-scope']){const b=items[id];assert(b.visible&&b.x>=box.x&&b.x+b.width<=box.x+box.width&&b.y+b.height<=height,'usage action inaccessible: '+id)}
  }
  if(view==='queued'){const b=items['preview-cancel-queue'];assert(b.visible&&b.width>0&&b.x>=0&&b.x+b.width<=width+1&&b.y>=0&&b.y+b.height<=height+1,'queue cancellation action inaccessible')}
  if(view==='desktopmanual'){
   const box=items['desktop-dialog'],img=items['desktop-preview-image'],close=items['desktop-close'];assert(box.visible&&box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=height+1,'manual desktop exceeds viewport');assert(box.scrollWidth<=box.clientWidth+1,'manual desktop horizontal overflow');assert(img.visible&&img.width>100&&img.x>=box.x&&img.x+img.width<=box.x+box.width+1,'manual desktop image clipped');assert(close.visible&&close.y+close.height<=height+1,'manual desktop close inaccessible');
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
   for(const id of ['accounts-open','engines-open']){const b=items[id];assert(b.visible&&b.x>=0&&b.x+b.width<=width+1&&b.y+b.height<=height+1,'account or engine entry is clipped: '+id);if(width<=760)assert(b.height>=44,id+' touch target is too small')}
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
   if(view.startsWith('handofflegacy'))assert(!items['handoff-preserve-legacy'],'legacy task must not ask for an account binding');
   assert(metrics.handoffFooterBackground&&!['transparent','rgba(0, 0, 0, 0)'].includes(metrics.handoffFooterBackground),'fixed handoff actions must have an opaque background');
   const box=items['handoff-dialog'];assert(box.visible&&box.x>=0&&box.x+box.width<=width&&box.y>=0&&box.y+box.height<=height,'engine switch dialog exceeds viewport');assert(box.scrollWidth<=box.clientWidth+1,'engine switch overflows horizontally');
   for(const id of ['handoff-engine','handoff-model','handoff-mode']){const b=items[id];assert(b.width>90&&b.x>=box.x&&b.x+b.width<=box.x+box.width,'engine switch field clipped: '+id);if(width<=760)assert(b.height>=44,'engine switch touch field too small: '+id)}
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
