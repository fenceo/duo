import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
const profile={id:'claude-fixture',name:'Synthetic Claude',engine:'claude',environment_id:'fixture',kind:'claude_home',reference:'/fixture/account'};
let writes=0,finish;
ctx.api=async(path,method,body)=>{
 if(path==='engines')return {engines:[{id:'claude',name:'Claude Code',credential_kinds:['claude_home']}],profiles:[profile],active_profile:{}};
 if(path==='engine-setup')return [];
 if(path==='account-sync'){writes++;assert.equal(method,'POST');assert.equal(body.source_profile_id,profile.id);assert.deepEqual(Array.from(body.environment_ids),['fixture']);return new Promise(resolve=>finish=resolve)}
 if(path==='engine-status')return {items:[{name:'Synthetic SSH',type:'ssh',codex:{label:'已安装'},claude:{label:'未安装'},harness:{label:'已安装'},kimi:{label:'未安装'},mimo:{label:'未安装'}}]};
 throw Error(path);
};
assert(el('accounts-open'));assert(el('engines-open'));assert(el('desktop-open'));
await ctx.openAccountCenter();assert(el('account-center-dialog').open);assert.match(el('account-center-list').textContent,/Synthetic Claude/);
el('account-center-list').querySelector('[data-account-sync]').onclick();
const target=el('codex-sync-targets').querySelector('input');assert(!target.hasAttribute('checked'));
target.setAttribute('checked','');
const pending=ctx.submitCodexSync();await ctx.submitCodexSync();assert.equal(writes,1);assert(el('codex-sync-targets').disabled);
finish({results:[{environment_id:'fixture',state:'done',message:'fixture saved'}]});await pending;
assert.match(el('codex-sync-status').textContent,/fixture saved/);assert(!el('codex-sync-targets').disabled);
await ctx.refreshEngineStatus();assert.match(el('engine-status-result').textContent,/Synthetic SSH/);
el('engine-account-engine').value='claude';ctx.refreshAccountLogin();assert.equal(el('engine-account-login').value,'apiKey');assert(!el('engine-account-api').classList.contains('hidden'));
console.log('PASS: independent accounts and installer entries, Claude sync, explicit target selection, duplicate lock, environment status and relay form. Synthetic only.');
