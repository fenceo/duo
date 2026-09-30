import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
ctx.crypto=webcrypto;
run(`settings.config.environments.push({id:'local',name:'Windows',type:'windows',codex:'fixture.exe',workspaces:['C:/fixture']});populateEngineOnboarding()`);
assert.equal(el('engine-setup-environment').value,'fixture');
assert.equal(el('engine-account-environment').value,'local');
assert(el('account-center-dialog').contains(el('engine-account-name')));
assert(!el('settings-engines').contains(el('engine-account-name')));
el('engine-account-name').value='Work';el('engine-account-login').value='apiKey';el('engine-account-login').onchange();assert(!el('engine-account-api').classList.contains('hidden'));
el('engine-account-key').value='synthetic-secret';el('engine-account-url').value='https://provider.example/v1';
let posts=0,finish,id;ctx.api=async(path,method,body)=>{
 if(method==='POST'){posts++;id=body.id;assert.equal(body.api_key,'synthetic-secret');assert.equal(body.engine,'codex');return new Promise(resolve=>finish=resolve)}
 if(path==='engine-setup/'+id)return {id,action:'account',engine:'codex',environment_id:'local',state:'done',message:'saved'};
 if(path==='engines')return {engines:[],profiles:[],active_profile:{}};
 if(path==='engine-setup')return [];
 throw Error(path);
};
const pending=ctx.startEngineOnboarding('account');await ctx.startEngineOnboarding('account');assert.equal(posts,1);assert(el('engine-setup-fields').disabled);
finish({id,action:'account',engine:'codex',environment_id:'local',state:'waiting',message:'login'});await pending;
for(let i=0;i<8;i++)await new Promise(resolve=>setImmediate(resolve));
assert.equal(el('engine-account-key').value,'');assert.equal(run('engineSetupAttempt'),null);
run(`renderEngineSetup({id:'login',action:'account',engine:'codex',environment_id:'local',state:'waiting',message:'等待登录',url:'https://auth.openai.com/codex/device',code:'TEST-1234'})`);
assert.equal(el('engine-account-status').querySelector('a').getAttribute('rel'),'noopener noreferrer');assert.match(el('engine-account-status').textContent,/TEST-1234/);
let canceled=false;ctx.api=async(path,method)=>{assert.equal(path,'engine-setup/login');assert.equal(method,'DELETE');canceled=true};await el('engine-account-cancel').onclick();assert(canceled);assert.match(el('engine-account-status').textContent,/正在取消/);
ctx.api=async()=>{throw Error('fixture offline')};await ctx.pollEngineSetup('login',run('shellEpoch'));assert.match(el('engine-account-status').textContent,/fixture offline/);assert(!el('engine-account-fields').disabled);
console.log('PASS: local tool setup, API inputs, duplicate lock, secret field clearing, native device login link/code and explicit cancellation. No real accounts or downloads.');
