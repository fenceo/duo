import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
ctx.crypto=webcrypto;
run("settings.config.environments=[{id:'win',name:'Windows fixture',type:'windows',workspaces:['C:/fixture']}];populateAccountImport()");
const chooseFile=file=>Object.defineProperty(el('account-manager-file'),'files',{configurable:true,value:[file]});
const file={size:100,text:async()=>'[{"OPENAI_API_KEY":"synthetic-key"}]'};
el('account-import-source').value='manager';el('account-import-source').onchange();chooseFile(file);
assert(el('account-import-name').classList.contains('hidden'));assert(!el('account-manager').classList.contains('hidden'));
let posts=0,finish;
ctx.api=async(path,method,body)=>{assert.equal(path,'account-import/preview');assert.equal(method,'POST');assert.match(body.json,/synthetic-key/);posts++;return new Promise(r=>finish=r)};
const pending=ctx.previewAccountManager();await new Promise(r=>setImmediate(r));await ctx.previewAccountManager();assert.equal(posts,1);assert(el('account-import-fields').disabled);
finish([{index:0,name:'<img src=x onerror=alert(1)>',kind:'订阅登录',valid:true},{index:1,name:'Relay',kind:'API / 中转站',valid:true},{index:2,name:'Unsupported',kind:'',valid:false,error:'暂不支持'}]);await pending;
assert.equal(el('account-manager-items').querySelector('img'),null);assert(!el('account-manager-items').textContent.includes('synthetic-key'));
assert.equal(run('selectedAccountManagerIndexes().length'),2);assert.equal(el('account-manager-items').querySelectorAll('input:disabled').length,1);
const first=el('account-manager-items').querySelector('input');first.removeAttribute('checked');first.onchange();assert.match(el('account-manager-submit').textContent,/（1）/);
const attempts=[];ctx.api=async(path,method,body)=>{assert.equal(path,'account-import/batch');attempts.push(body.id);assert.deepEqual(Array.from(body.indexes),[1]);throw Error('fixture offline')};
await ctx.submitAccountManager();await ctx.submitAccountManager();assert.equal(attempts.length,2);assert.equal(attempts[0],attempts[1]);assert(!el('account-import-fields').disabled);
// Changing the file without firing a change event must also invalidate preview.
chooseFile({...file});await ctx.submitAccountManager();assert.equal(attempts.length,2);assert.match(el('account-import-result').textContent,/重新解析/);assert.equal(run('accountManagerPreview'),null);
chooseFile(file);ctx.api=async()=>[{index:0,name:'Fixture',kind:'API',valid:true}];await ctx.previewAccountManager();
ctx.api=async path=>{if(path==='account-import/batch')return[{id:'fixture'}];throw Error(path)};
ctx.loadEngineSettings=async()=>{};await ctx.submitAccountManager();assert.match(el('account-import-result').textContent,/已导入 1/);assert.equal(run('accountManagerPreview'),null);assert.equal(run('accountImportAttempt'),null);
// Oversize rejection happens before File.text or any request.
chooseFile({size:1024*1024+1,text:async()=>{throw Error('must not read')}});ctx.api=async()=>{throw Error('must not request')};await ctx.previewAccountManager();assert.match(el('account-import-result').textContent,/1 MB/);
// A late response for the previous engine cannot restore a stale preview.
chooseFile(file);ctx.api=()=>new Promise(r=>finish=r);const stale=ctx.previewAccountManager();await new Promise(r=>setImmediate(r));el('account-import-engine').value='claude';el('account-import-engine').onchange();finish([{index:0,name:'Old engine',valid:true,kind:'API'}]);await stale;
assert.equal(run('accountManagerPreview'),null);assert(el('account-manager-submit').disabled);assert.equal(el('account-manager-items').textContent,'');
// Logout/shell reset cannot retain a credential-bearing preview.
el('account-import-engine').value='codex';el('account-import-engine').onchange();ctx.api=async()=>[{index:0,name:'Fixture',kind:'API',valid:true}];await ctx.previewAccountManager();run('renewShellScope()');assert.equal(run('accountManagerPreview'),null);assert.equal(run('accountImportAttempt'),null);
console.log('PASS: manager JSON preview, selection, unsupported entries, HTML escaping, duplicate/retry isolation, stale file/engine and logout guards. Synthetic only.');
