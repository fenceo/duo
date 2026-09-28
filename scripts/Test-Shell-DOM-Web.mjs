import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import {parseHTML} from 'linkedom';
import assert from 'node:assert/strict';

// Run every production feature initializer against a real connected/detached
// DOM implementation. HTTP is a synthetic in-memory fixture; no browser, CLI,
// credentials, model turn, storage directory or external network is accessed.
const files=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','engines','app','panels'];
const source=(await Promise.all(files.map(name=>readFile(new URL('../web/'+name+'.ts',import.meta.url),'utf8')))).join('\n');
const {document,window}=parseHTML('<!doctype html><html><body><div id="root"></div><div id="notice"></div></body></html>');
// linkedom implements real node attachment and selectors, but intentionally
// omits some browser-only form/layout/media interfaces used at mount time.
Object.defineProperty(window.HTMLSelectElement.prototype,'value',{configurable:true,get(){return this.querySelector('option[selected]')?.value??this.querySelector('option')?.value??''},set(value){for(const option of this.querySelectorAll('option')){if(option.value===value)option.setAttribute('selected','');else option.removeAttribute('selected')}}});
const storage=()=>{const map=new Map();return {getItem:key=>map.get(key)??null,setItem:(key,value)=>map.set(key,String(value)),removeItem:key=>map.delete(key)}};
const requests=[];
const environment={id:'fixture',name:'Fixture WSL',type:'wsl',user:'fixture',distro:'Fixture',host:'',codex:'fixture',model:'',model_cache:'',workspaces:['/fixture']};
const configuration={environments:[environment],default_environment:'fixture',feishu:{enabled:false,app_id:'',owner:''},access:{lan:'',tailscale:''}};
const ctx=createContext({
 document,window,console,AbortController,URL,URLSearchParams,TextEncoder,TextDecoder,Date,Error,
 localStorage:storage(),sessionStorage:storage(),location:{search:'',reload(){}},history:{replaceState(){}},navigator:{},
 matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),
 MutationObserver:class {observe(){}disconnect(){}},setInterval:()=>0,clearInterval(){},setTimeout:()=>0,clearTimeout(){},requestAnimationFrame:()=>0,
 fetch:async(path,options)=>{
  requests.push({path,method:options?.method||'GET'});
  const responses={'/api/auth':{authenticated:true,csrf:'synthetic-token'},'/api/tasks':[], '/api/settings':{config:configuration,data_dir:'C:/fixture/data',secret_configured:false,feishu_status:'disabled',chat:''}, '/api/workbench':{modes:[{id:'work',name:'Work',permission:'workspace',approval:'request',allow_network:true,prompt:''},{id:'codex:auto',name:'Auto',permission:'workspace',approval:'auto',allow_network:true,prompt:''}],commands:[]},'/api/scratch':[], '/api/library/automatic':{capture:true,recall:true},'/api/library/vault':{config:{enabled:false,directory:'',include_runs:false,include_automatic:false},report:{conflicts:[],warnings:[]},document_directory:''}};
  if(!(path in responses))throw new Error('Unexpected fixture request: '+path);
  return {ok:true,status:200,json:async()=>structuredClone(responses[path])};
 }
});
runInContext(stripTypeScriptTypes(source,{mode:'transform'}),ctx,{filename:'duo-full-shell-fixture.js'});
for(let n=0;n<5;n++)await new Promise(resolve=>setImmediate(resolve));
assert(!document.getElementById('shell-retry'),'full mount failed: '+document.getElementById('root').textContent);
assert.equal(typeof document.getElementById('new-task')?.onclick,'function','mount must reach the final task event bindings');
assert(document.getElementById('sticky-board'),'mount must reach notes initialization after workflow');
assert(document.getElementById('update-current'),'mount must reach the final update settings initializer');
for(const id of ['library-open','library-task','library-create','library-dialog'])assert(document.getElementById(id),'knowledge entry point missing: '+id);
for(const id of ['vault-settings','automatic-knowledge-settings'])assert.equal(document.getElementById(id).closest('.settings-section')?.id,'settings-knowledge','one central home for knowledge settings');
assert.equal(document.querySelectorAll('#vault-directory').length,1,'directory controls must not be duplicated');
assert.equal(document.querySelectorAll('[data-settings="knowledge"]').length,1);
assert(!document.getElementById('library-dialog').contains(document.getElementById('vault-settings')),'search dialog must not contain a second settings editor');
assert.equal(document.getElementById('note-tab').textContent,'本任务知识');
assert.equal(typeof document.getElementById('knowledge-library-open').onclick,'function');
assert.equal(typeof document.getElementById('library-manage-task').onclick,'function');
assert.match(document.getElementById('knowledge-data-directory').parentElement.textContent,/共用此目录中的数据库/);
ctx.showSettingsSection('knowledge');
for(let n=0;n<5;n++)await new Promise(resolve=>setImmediate(resolve));
assert(!document.getElementById('settings-knowledge').classList.contains('hidden'));
assert(document.getElementById('settings-save').classList.contains('hidden'),'knowledge settings must not save unrelated engine configuration');
assert.equal(document.getElementById('knowledge-data-directory').textContent,'C:/fixture/data');
assert.equal(document.getElementById('automatic-capture').checked,true);
assert.equal(document.getElementById('vault-save').disabled,false);
assert.equal(document.getElementById('reload-models').closest('.model-picker')?.id,'model-picker');
assert.equal(document.getElementById('test-models').closest('.model-picker')?.id,'model-picker');
for(const id of ['reload-models','test-models','stop-model-test'])assert.equal(document.getElementById(id).closest('.model-catalog-actions')?.id,'model-catalog-actions',id+' must stay in the model menu, including after detached composer mount');
for(const id of ['task-reload-models','task-test-models','task-stop-model-test'])assert.equal(document.getElementById(id).closest('.model-menu')?.id,'task-model-menu',id+' belongs to the task model menu');
assert.equal(document.getElementById('models-hint').closest('.model-menu')?.id,'model-menu');
assert(document.getElementById('settings-data'),'settings must include the data and workspace transfer section');
for(const id of ['workspace-export','workspace-import-file','workspace-import-strategy','workspace-import'])assert(document.getElementById(id),'workspace transfer control missing: '+id);
assert.match(document.getElementById('settings-data').textContent,/不含工作台密码/);
assert.match(document.getElementById('settings-data').textContent,/当前版本只支持追加/);
const sidebarChildren=[...document.getElementById('sidebar').children];
assert(sidebarChildren.indexOf(document.getElementById('sticky-board'))<sidebarChildren.indexOf(document.getElementById('sidebar-footer')));
assert.equal(document.querySelectorAll('#model-catalog-actions').length,1);
assert.equal(document.querySelectorAll('#stop-model-test').length,1);
// A fresh login mounts the same full shell again without losing detached nodes.
await ctx.boot();
assert.equal(typeof document.getElementById('new-task')?.onclick,'function');
assert.equal(document.querySelectorAll('#model-catalog-actions').length,1);
assert(requests.every(request=>request.method==='GET'&&!request.path.includes('/models/test')));
runInContext('authenticated=false;renewShellScope()',ctx);
console.log('PASS: complete shell mounts twice; centralized knowledge settings, distinct task/global knowledge controls, detached controls, task handler, notes, updates and sidebar order. No model calls.');
