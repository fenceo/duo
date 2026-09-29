import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import {parseHTML} from 'linkedom';

export async function createWebShellFixture(webRoot=new URL('../web/',import.meta.url)){
// Run every production feature initializer against a real connected/detached
// DOM implementation. HTTP is a synthetic in-memory fixture; no browser, CLI,
// credentials, model turn, storage directory or external network is accessed.
const files=['library','updates','workflow','sticky','conversation','codex-approvals','layout','environments','hardware','execution','discovery','terminal','productivity','tools','engines','app','panels'];
const source=(await Promise.all(files.map(name=>readFile(new URL(name+'.ts',webRoot),'utf8')))).join('\n');
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
return {document,window,ctx,requests,environment};
}
