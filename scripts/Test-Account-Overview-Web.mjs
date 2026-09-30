import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document,window}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
window.HTMLElement.prototype.scrollIntoView=function(){};
const profiles=[
 {id:'a',name:'Work Codex',engine:'codex',environment_id:'win',kind:'codex_home',reference:'C:/fixture/a',created:10,updated:30},
 {id:'b',name:'Relay Claude',engine:'claude',environment_id:'remote',kind:'claude_home',reference:'/fixture/b',created:20,updated:40},
 {id:'c',name:'Backup Codex',engine:'codex',environment_id:'win',kind:'codex_home',reference:'C:/fixture/c',created:30,updated:50},
];
const catalog={engines:[{id:'codex',name:'Codex',runnable:true,credential_kinds:['codex_home'],targets:['windows','ssh']},{id:'claude',name:'Claude Code',runnable:true,credential_kinds:['claude_home'],targets:['windows','ssh']}],profiles,active_profile:{'win:codex':'a'}};
ctx.fixtureCatalog=catalog;
run("settings.config.environments=[{id:'win',name:'Windows',type:'windows',workspaces:['C:/fixture']},{id:'remote',name:'Remote SSH',type:'ssh',workspaces:['/fixture']}];engineCatalog=fixtureCatalog;renderAccountCenter()");
assert.equal(document.querySelectorAll('[data-account-card]').length,3);assert.equal(document.querySelectorAll('.account-default-badge').length,1);
assert(el('account-center-list').querySelector('[data-account-activate="a"]').disabled);
document.querySelector('[data-account-engine="codex"]').onclick();assert.equal(document.querySelectorAll('[data-account-card]').length,2);
el('account-search').value='backup';el('account-search').oninput();assert.equal(document.querySelectorAll('[data-account-card]').length,1);assert.match(el('account-center-list').textContent,/Backup Codex/);
el('account-privacy').onclick();assert(!el('account-center-list').textContent.includes('Backup Codex'));assert.equal(el('account-privacy').getAttribute('aria-pressed'),'true');el('account-privacy').onclick();
el('account-search').value='';el('account-search').oninput();document.querySelector('[data-account-engine=""]').onclick();
el('account-environment-filter').value='remote';el('account-environment-filter').onchange();assert.equal(document.querySelectorAll('[data-account-card]').length,1);
el('account-default-filter').value='default';el('account-default-filter').onchange();assert.match(el('account-center-list').textContent,/没有匹配/);
el('account-environment-filter').value='';el('account-default-filter').value='';el('account-default-filter').onchange();el('account-view-list').onclick();assert(el('account-center-list').classList.contains('account-list-view'));assert.equal(ctx.localStorage.getItem('duo.accountView'),'list');
let writes=0,finish;
ctx.api=async(path,method,body)=>{
 if(path==='engine-profiles/c/activate'){writes++;return new Promise(r=>finish=()=>{catalog.active_profile['win:codex']='c';r({ok:true})})}
 if(path==='engines')return catalog;if(path==='engine-setup')return [];throw Error(path);
};
const switching=ctx.activateEngineProfile('c');await ctx.activateEngineProfile('b');assert.equal(writes,1);assert(document.querySelector('[data-account-activate="b"]').disabled);finish();await switching;
assert(document.querySelector('[data-account-activate="c"]').disabled);assert(!document.querySelector('[data-account-activate="a"]').disabled);
// Renaming only sends display metadata; stale edits remain visible for retry.
document.querySelector('[data-account-rename="b"]').onclick();el('account-edit-name').value='Renamed relay';
ctx.api=async(path,method,body)=>{assert.equal(path,'engine-profiles/b');assert.equal(method,'PATCH');assert.deepEqual(Object.keys(body).sort(),['expected_updated','name']);assert.equal(body.expected_updated,40);throw Error('账号配置已更改')};
await ctx.renameAccount();assert(el('account-edit-dialog').open);assert.match(el('account-edit-status').textContent,/已更改/);assert(!el('account-edit-save').disabled);el('account-edit-cancel').onclick();
let target;
ctx.openHandoff=async(...args)=>target=args;run("chosen='task-fixture';renderAccountCenter()");document.querySelector('[data-account-task="b"]').onclick();assert.deepEqual(target,['task-fixture','b']);assert.equal(writes,1,'task shortcut must not activate or sync an account');
el('account-center-import').onclick();assert(el('account-center-editor-wrap').open);assert(document.querySelector('.account-import').open);
// An older refresh cannot replace a newer catalog.
let old,newer;ctx.api=()=>new Promise(r=>{if(!old)old=r;else newer=r});const before=ctx.refreshAccountCenter(),after=ctx.refreshAccountCenter();newer({...catalog,profiles:[profiles[1]]});await after;old(catalog);await before;assert.equal(run('engineCatalog.profiles.length'),1);
console.log('PASS: account cards, engine/environment/default filters, search, privacy, list view, activation lock, stale rename and refresh guards, task handoff shortcut and import entry. Synthetic only.');
