import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

// In-memory DOM + HTTP fixtures: discovering a list must never send a model turn.
const source=await readFile(new URL('../web/execution.ts',import.meta.url),'utf8');
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
const response=(id,extra={})=>({models:id?[{id,name:id,origin:'native'}]:[],source:'fixture app-server/model/list',modified:0,status:id?'ready':'empty',...extra});
function fixture(){
 const nodes=new Map(),calls=[];
 const node=id=>{
  if(!nodes.has(id)){
   const classes=new Set(id.endsWith('menu')?['hidden']:[]);
   nodes.set(id,{value:'',textContent:'',innerHTML:'',attributes:{},disabled:false,focus(){},setAttribute(k,v){this.attributes[k]=v},classList:{contains:v=>classes.has(v),add:v=>classes.add(v),remove:v=>classes.delete(v),toggle(v,on){on?classes.add(v):classes.delete(v)}},querySelectorAll(){return []}});
  }
  return nodes.get(id);
 };
 const environment={id:'wsl',name:'WSL fixture',type:'wsl',user:'test',model:'',workspaces:['/work']};
 const ctx=createContext({console,Date,URLSearchParams,AbortController,modelRequest:0,shellEpoch:1,creatingTask:true,createSubmitting:false,detail:null,settings:{config:{environments:[environment,{...environment,id:'ssh',name:'SSH fixture',type:'ssh'}]}},element:node,input:node,button:node,escapeHTML:value=>String(value).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'),notify(){},api:async(path,method,body)=>{calls.push({path,method,body});return response('native-one')}});
 runInContext(stripTypeScriptTypes(source,{mode:'transform'}),ctx);
 node('create-environment').value='wsl';node('create-engine').value='codex';node('create-workspace').value='/work/space here';
 return {ctx,node,calls,environment,value:code=>runInContext(code,ctx)};
}
const flush=()=>new Promise(resolve=>setImmediate(resolve));
{
 const f=fixture();
 f.node('create-engine').value='deepseek-harness';
 f.environment.harness_model='deepseek-flash';f.environment.harness_provider='deepseek-official';
 f.ctx.api=async()=>response('deepseek-v4.1-flash',{models:[{id:'deepseek-v4.1-flash',name:'Flash',reasoning_levels:[]}],source:'DSH settings.yaml',default_model:''});
 await f.ctx.loadCreateModels(true);
 assert.doesNotMatch(f.node('model-list').innerHTML,/deepseek-flash/,'refresh must not reinsert the obsolete default');
 assert.equal(f.node('create-model').value,'','do not silently select an arbitrary provider model');
 f.node('create-model').value='deepseek-v4.1-flash';f.node('create-effort').value='max';
 f.ctx.updateReasoning();
 assert.equal(f.node('create-effort').value,'','changing to an unknown-capability model clears unsupported max');
 assert.equal(f.node('create-effort').disabled,true);
 assert.doesNotMatch(f.node('create-effort').innerHTML,/value="max"/);
 f.ctx.api=async()=>response('deepseek-v4.1-flash',{default_model:'deepseek-v4.1-flash'});
 await f.ctx.loadCreateModels(false,true);
 assert.equal(f.node('create-model').value,'deepseek-v4.1-flash','refresh preserves the explicit model');
}
{
 const f=fixture();await f.ctx.toggleModelMenu('create');await flush();
 assert.equal(f.calls.length,1);assert.match(f.calls[0].path,/engine=codex/);assert.match(f.calls[0].path,/workspace=%2Fwork%2Fspace\+here/);
 assert.match(f.node('model-list').innerHTML,/native-one/);assert.equal(f.node('model-menu').classList.contains('hidden'),false);
 assert.match(f.node('models-context').textContent,/WSL fixture · Codex/);assert.match(f.node('models-hint').textContent,/不代表调用已验证/);
 await f.ctx.toggleModelMenu('create');await f.ctx.toggleModelMenu('create');await flush();assert.equal(f.calls.length,2,'every open rereads the current configured CLI catalog');
 f.node('create-model').value='__custom__';f.node('custom-model').value='my-manual-id';
 await f.ctx.loadCreateModels(false,true);assert.match(f.calls.at(-1).path,/refresh=1/);assert.equal(f.node('custom-model').value,'my-manual-id');assert.equal(f.node('create-model').value,'__custom__','refresh preserves an explicit choice');
 assert(f.calls.every(call=>!call.path.includes('/test')&&call.method==='GET'),'discovery never spends model quota');
}
{
 const f=fixture(),first=deferred(),second=deferred();let index=0,oldSignal;
 f.ctx.api=(path,method,body,signal)=>{if(++index===1){oldSignal=signal;return first.promise}return second.promise};
 const old=f.ctx.loadCreateModels(true);
 assert.equal(f.node('model-picker-button').disabled,false,'slow metadata must not lock the default/custom choice');
 assert.match(f.node('model-list').innerHTML,/正在读取/);assert.equal(f.node('reload-models').disabled,true);
 f.node('create-environment').value='ssh';f.node('create-engine').value='claude';f.node('create-workspace').value='/new';
 const fresh=f.ctx.loadCreateModels(true);assert.equal(oldSignal.aborted,true,'switching target cancels the old native metadata process');second.resolve(response('claude-only'));await fresh;first.resolve(response('stale-codex'));await old;
 assert.match(f.node('model-list').innerHTML,/claude-only/);assert.doesNotMatch(f.node('model-list').innerHTML,/stale-codex/);assert.match(f.node('models-context').textContent,/SSH fixture · Claude Code/);
}
for(const kind of ['workspace','config','shell','closed']){
 const f=fixture(),gate=deferred();f.ctx.api=()=>gate.promise;
 const pending=f.ctx.loadCreateModels(true);
 if(kind==='workspace')f.node('create-workspace').value='/changed';
 if(kind==='config')f.environment.user='other-user';
 if(kind==='shell')f.ctx.shellEpoch++;
 if(kind==='closed')f.ctx.creatingTask=false;
 f.node('models-status').textContent='new-screen';gate.resolve(response('stale-result'));await pending;
 assert.equal(f.node('models-status').textContent,'new-screen',kind+' isolates stale results');assert.doesNotMatch(f.node('model-list').innerHTML,/stale-result/);
}
{
 const f=fixture(),first=deferred(),second=deferred();let calls=0;f.ctx.api=()=>++calls===1?first.promise:second.promise;
 const pending=f.ctx.loadCreateModels(true);f.ctx.invalidateModelCatalogs();
 second.resolve(response('new-profile'));await flush();first.resolve(response('old-profile'));await pending;
 assert.equal(calls,2);assert.match(f.node('model-list').innerHTML,/new-profile/);assert.doesNotMatch(f.node('model-list').innerHTML,/old-profile/);
}
{
 const f=fixture();f.ctx.api=async()=>response('',{default_model:'native-default'});await f.ctx.loadCreateModels(true);
 assert.equal(f.node('create-model').value,'','native default keeps CLI-managed default semantics');assert.equal(f.node('model-picker-label').textContent,'默认 · native-default');
 assert.match(f.node('model-list').innerHTML,/当前默认：native-default/);
 assert.equal(f.value('createResolvedDefault'),'native-default');
 f.ctx.api=async()=>{throw new Error('fixture CLI unavailable')};await f.ctx.loadCreateModels(false,true);
 assert.match(f.node('models-status').textContent,/读取失败/);assert.match(f.node('models-hint').textContent,/fixture CLI unavailable/);assert.equal(f.node('reload-models').disabled,false);
 f.node('model-search').value='my-custom';f.ctx.renderModelMenu('create');assert.match(f.node('model-list').innerHTML,/使用「my-custom」/);
}
{
 const f=fixture();f.ctx.api=async()=>({models:[{id:'nameless-native'}],source:'fixture',modified:0});await f.ctx.loadCreateModels(true);
 f.node('model-search').value='native';assert.doesNotThrow(()=>f.ctx.renderModelMenu('create'));assert.match(f.node('model-list').innerHTML,/nameless-native/);
}
{
 const f=fixture(),gate=deferred();f.ctx.creatingTask=false;f.ctx.detail={task:{id:'task-one',engine:'codex',model:'explicit-task-model',workspace:'/task',environment:f.environment}};f.ctx.api=()=>gate.promise;
 await f.ctx.toggleModelMenu('task');assert.match(f.node('task-model-list').innerHTML,/explicit-task-model/);
 f.ctx.detail={task:{...f.ctx.detail.task,id:'task-two'}};f.node('task-models-status').textContent='new-task';gate.resolve(response('stale-task'));await flush();
 assert.equal(f.node('task-models-status').textContent,'new-task');assert.doesNotMatch(f.node('task-model-list').innerHTML,/stale-task/);
}
const workflow=await readFile(new URL('../web/workflow.ts',import.meta.url),'utf8');
assert.match(workflow,/modelFooter\.querySelector\('#model-catalog-actions'\)!\.append\(element\('reload-models'\),element\('test-models'\),element\('stop-model-test'\)\)/);
assert.match(workflow,/meta\.append\(element\('effort-hint'\),element\('create-error'\)\)/);
const environments=await readFile(new URL('../web/environments.ts',import.meta.url),'utf8');
assert.match(environments,/id="custom-model-engine"/);assert.match(environments,/env\.models\.push\(\{id,name,engine\}\)/);
console.log('PASS: picker auto-read/current context, explicit refresh, pending/empty/error/source UI, native default, custom selection, no paid discovery, stale environment/engine/workspace/profile/shell/task isolation.');
