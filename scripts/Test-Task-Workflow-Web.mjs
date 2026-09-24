import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const app=await readFile(new URL('../web/app.ts',import.meta.url),'utf8');
const workflow=await readFile(new URL('../web/workflow.ts',import.meta.url),'utf8');
assert.match(workflow,/create-input'\)\.addEventListener\('paste'/,'new-task composer accepts pasted clipboard images');
assert.match(workflow,/function clipboardFiles\(event:ClipboardEvent\)/,'clipboard image normalization is shared by create and task composers');
assert.match(workflow,/item\.type\.startsWith\('image\/'\)/,'clipboard image items are converted to uploadable files');
function extract(source,start,end){const from=source.indexOf(start),to=source.indexOf(end,from+start.length);assert(from>=0&&to>from);return source.slice(from,to)}
const source=[
 extract(app,'function harnessSessionClosed(', '// Describe'),
 extract(app,'async function resetSession(', 'async function loadTaskContext('),
 extract(app,'async function createTask(', 'async function loadKnowledge('),
 extract(workflow,'function setCreateSubmitState(', 'async function showCreateAt('),
 extract(workflow,'async function addAttachments(', 'function selectedMessageMode('),
].join('\n');
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
function fixture(){
 const nodes=new Map(),calls=[],notices=[];
 const node=id=>{if(!nodes.has(id))nodes.set(id,{value:'',disabled:false,dataset:{},textContent:'',focus(){},setAttribute(){},querySelectorAll(){return []}});return nodes.get(id)};
 const values={'create-input':'keep my task','create-engine':'codex','create-effort':'','create-environment':'test-env','create-workspace':'/test','create-model':'test-model'};
 Object.entries(values).forEach(([id,value])=>node(id).value=value);
 const ctx=createContext({
  input:node,button:node,element:node,createFiles:[],createSubmitting:false,creatingTask:true,createPermission:'request',createReturnTask:'',modelRequest:0,shellEpoch:0,shellCurrent:epoch=>epoch===ctx.shellEpoch,
  sessionResetTask:'',chosen:'',selection:0,detail:null,sending:false,dirty:false,tasks:[],drafts:new Map(),attachmentDrafts:new Map(),pendingUploadFiles:new Map(),uploadingTasks:new Set(),
  validateEngineAttachments(){},validateAttachmentFiles(){},modeForPermission:()=>({id:'work'}),modeSupportsEngine:()=>true,createTaskTitle:text=>text,
  renderCreateFiles(){},setCreatePageVisible(){},renderTask(){},renderWorkflow(){},invalidateModelTest(){},notify:text=>notices.push(text),confirm:()=>true,poll:async()=>{},selectedMessageMode:()=> 'work',
  choose:async id=>{ctx.chosen=id;ctx.detail={task:{id,engine:'codex'},runs:[],events:[]}},
  api:async(path,method,data)=>{calls.push({path,method,data});return path==='tasks'?{task:{id:'created',engine:'codex'}}:{}},
  uploadTaskFile:async(_,file)=>({id:file.name,name:file.name}),
 });
 runInContext(stripTypeScriptTypes(source,{mode:'transform'}),ctx,{filename:'task-workflow-under-test.js'});
 return {ctx,node,calls,notices};
}
const event={preventDefault(){}};
{
 const {ctx,calls,node}=fixture(),gate=deferred();ctx.createFiles=[{name:'retained.txt'}];
 ctx.api=async(path,method,data)=>{calls.push({path,method,data});return gate.promise};
 const pending=ctx.createTask(event);ctx.shellEpoch++;ctx.chosen='new-login-task';node('create-input').value='new login draft';
 gate.resolve({task:{id:'created',engine:'codex'}});await pending;
 assert.equal(calls.length,1,'a stale creation response must not start uploads or a model run');
 assert.equal(ctx.chosen,'new-login-task');assert.equal(node('create-input').value,'new login draft');
 assert.equal(ctx.drafts.get('created'),'keep my task');assert.equal(ctx.pendingUploadFiles.get('created')[0].name,'retained.txt');
}
{
 const {ctx,calls,node}=fixture(),gate=deferred();
 ctx.api=async(path,method,data)=>{calls.push({path,method,data});return path==='tasks'?gate.promise:{}};
 const first=ctx.createTask(event);await ctx.createTask(event);
 assert.equal(calls.length,1,'double Enter must create exactly one task');
 ctx.setCreateSubmitState('idle');assert.equal(node('create-submit').disabled,true,'late model response cannot unlock submission');
 gate.resolve({task:{id:'created',engine:'codex'}});await first;
 assert.equal(calls.filter(c=>c.path==='tasks/created/messages').length,1);
 assert.equal(ctx.createSubmitting,false);assert.equal(ctx.drafts.size,0);
}
{
 const {ctx,node,calls}=fixture();node('create-input').value=' ';await ctx.createTask(event);
 assert.equal(calls.length,0,'empty submit must not create an empty task');assert.match(node('create-error').textContent,/任务要求/);
}
{
 const {ctx,calls}=fixture();ctx.createFiles=[{name:'first'},{name:'second'},{name:'third'}];
 ctx.uploadTaskFile=async(_,file)=>{if(file.name==='second')throw new Error('offline');return {id:file.name,name:file.name}};
 await ctx.createTask(event);
 assert.equal(ctx.chosen,'created');assert.equal(ctx.drafts.get('created'),'keep my task');
 assert.deepEqual(Array.from(ctx.attachmentDrafts.get('created'),f=>f.id),['first']);
 assert.deepEqual(Array.from(ctx.pendingUploadFiles.get('created'),f=>f.name),['second','third']);
 assert.equal(calls.filter(c=>c.path.endsWith('/messages')).length,0);
 const uploaded=[];ctx.uploadTaskFile=async(_,file)=>{uploaded.push(file.name);return {id:file.name,name:file.name}};
 await ctx.retryPendingUploads('created');
 assert.deepEqual(uploaded,['second','third'],'retry must not reupload the successful file');
 assert.equal(ctx.pendingUploadFiles.has('created'),false);
}
{
 const {ctx}=fixture(),gate=deferred();ctx.chosen='a';ctx.detail={task:{id:'a',session:'old',engine:'deepseek-harness'},runs:[],events:[]};
 ctx.api=()=>gate.promise;ctx.drafts.set('a','unsent text');
 const reset=ctx.resetSession();ctx.chosen='b';ctx.selection++;const other={task:{id:'b'},runs:[],events:[]};ctx.detail=other;
 gate.resolve({id:'a',session:'',engine:'deepseek-harness'});await reset;
 assert.equal(ctx.detail,other,'late reset must not replace another task');assert.equal(ctx.drafts.get('a'),'unsent text');
}
{
 const {ctx}=fixture();ctx.chosen='a';const events=[{kind:'user',text:'old history'}];ctx.detail={task:{id:'a',session:'old',engine:'deepseek-harness'},runs:[],events};
 ctx.drafts.set('a','unsent text');ctx.api=async()=>({id:'a',session:'',engine:'deepseek-harness'});await ctx.resetSession();
 assert.equal(ctx.detail.runtime.state,'new');assert.equal(ctx.detail.events,events);assert.equal(ctx.drafts.get('a'),'unsent text');
}
{
 const {ctx,calls,node}=fixture();ctx.chosen='a';ctx.detail={task:{id:'a',engine:'deepseek-harness'},runtime:{state:'closed',can_continue:false}};
 node('message').value='keep this';await ctx.send('keep this',true);assert.equal(calls.length,0);assert.equal(node('message').value,'keep this');
}
for(const newer of [false,true]){
 const {ctx,node}=fixture(),gate=deferred();ctx.chosen='a';ctx.detail={task:{id:'a',engine:'codex'}};
 node('message').value='sent from A';ctx.drafts.set('a','sent from A');ctx.api=()=>gate.promise;
 const sending=ctx.send('sent from A',true);ctx.chosen='b';ctx.detail={task:{id:'b',engine:'codex'}};node('message').value='B draft';
 if(newer)ctx.drafts.set('a','newer A draft');gate.resolve({});await sending;
 assert.equal(ctx.drafts.get('a'),newer?'newer A draft':undefined,'successful send must clean only its unchanged original draft even after navigation');
 assert.equal(node('message').value,'B draft');
}
console.log('PASS: duplicate creation lock, stale-load lock, empty submit, retained partial-upload drafts, retry only missing uploads, reset navigation isolation, history preservation and closed-session send guard. No model requests.');
