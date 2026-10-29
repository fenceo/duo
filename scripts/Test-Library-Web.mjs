import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

const source=stripTypeScriptTypes(await readFile(new URL('../web/library.ts',import.meta.url),'utf8'),{mode:'transform'});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
function fixture(){
 const nodes=new Map(),notices=[],calls=[];
 const node=id=>{if(!nodes.has(id))nodes.set(id,{value:'',checked:false,disabled:false,textContent:'',innerHTML:'',classList:{toggle(){},add(){},remove(){},contains(){return false}},querySelectorAll(){return []},setAttribute(){},focus(){},dispatchEvent(){},close(){this.open=false},showModal(){this.open=true}});return nodes.get(id)};
 const ctx=createContext({URLSearchParams,Event,console,element:node,input:node,button:node,shellEpoch:1,selection:4,chosen:'one',creatingTask:false,tasks:[],drafts:new Map(),names:{done:'完成'},shellController:new AbortController(),shellCurrent:e=>e===ctx.shellEpoch,escapeHTML:s=>s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),notify:s=>notices.push(s),loadKnowledge:async()=>{},api:async(path,method,body)=>{calls.push({path,method,body});return path==='library/automatic'?{capture:true,recall:true}:path==='library/vault'?{config:{enabled:false},report:{}}:path.startsWith('library/search')?{documents:[],truncated:false}:{reference:'\n[来源：history]\nprevious conclusion',truncated:false}}});
 runInContext(source,ctx);runInContext("libraryTarget={task:'one',create:false,selection:4,epoch:1};libraryHits=[{id:'knowledge:abc',hash:'version-one'}]",ctx);
 return {ctx,node,notices,calls,run:code=>runInContext(code,ctx)};
}
{
 const {ctx,node,calls}=fixture();node('message').value='current request';await ctx.citeLibrary('knowledge:abc');
 assert.match(calls[0].path,/hash=version-one/);assert.equal(node('message').value,'current request\n[来源：history]\nprevious conclusion');assert.equal(ctx.drafts.get('one'),node('message').value);assert.equal(calls.length,1,'citation never sends a model message');
}
{
 const {ctx,node}=fixture(),gate=deferred();ctx.api=()=>gate.promise;const pending=ctx.citeLibrary('knowledge:abc');ctx.chosen='two';ctx.selection++;node('message').value='new task draft';gate.resolve({reference:'wrong context'});await pending;assert.equal(node('message').value,'new task draft');assert.equal(ctx.drafts.size,0,'task switch cannot redirect a pending citation');
}
{
 const {ctx,node,run}=fixture();ctx.creatingTask=true;ctx.chosen='';run("libraryTarget={task:'',create:true,selection:4,epoch:1}");node('create-input').value='new task';await ctx.citeLibrary('knowledge:abc');assert.match(node('create-input').value,/new task\n\[来源/);assert.equal(ctx.drafts.size,0);
}
{
 const {ctx,node}=fixture(),gate=deferred();ctx.api=()=>gate.promise;const pending=ctx.citeLibrary('knowledge:abc');ctx.shellEpoch++;node('message').value='different login';gate.resolve({reference:'old account'});await pending;assert.equal(node('message').value,'different login');
}
{
 const {ctx,node}=fixture(),old=deferred();let count=0;ctx.api=()=>++count===1?old.promise:Promise.resolve({documents:[{id:'safe',title:'<img onerror=1>',task_title:'test',kind:'knowledge',status:'observed',origin:'vault',path:'a.md',snippet:'<script>bad</script>'}],truncated:false});
 const first=ctx.searchLibrary();await ctx.searchLibrary();old.resolve({documents:[],truncated:false});await first;assert.match(node('library-results').innerHTML,/&lt;img/);assert(!node('library-results').innerHTML.includes('<script>'));assert.match(node('library-results').innerHTML,/data-library-reference="safe"/);
}
{
 const {ctx,node,calls}=fixture();ctx.api=async(path,method,body)=>{calls.push({path,method,body});return {capture:true,recall:false}};
 await ctx.loadAutomaticKnowledge();assert.equal(node('automatic-capture').checked,true);assert.equal(node('automatic-recall').checked,false);assert.equal(node('automatic-save').disabled,false);
 node('automatic-capture').checked=false;await ctx.saveAutomaticKnowledge();assert.equal(calls.length,2);assert.equal(calls[1].method,'PUT');assert.equal(calls[1].body.capture,false);assert.equal(calls[1].body.recall,false);
}
{
 const {ctx,node,calls}=fixture();ctx.api=async()=>{throw new Error('cannot load settings')};await ctx.loadAutomaticKnowledge();await ctx.saveAutomaticKnowledge();
 assert.equal(node('automatic-save').disabled,true);assert.equal(calls.length,0,'failed load cannot overwrite settings with unchecked defaults');assert.match(node('automatic-status').textContent,/cannot load/);
}
{
 const {ctx,node}=fixture(),old=deferred();let count=0;ctx.api=()=>++count===1?old.promise:Promise.resolve({capture:false,recall:false});
 const first=ctx.loadAutomaticKnowledge();await ctx.loadAutomaticKnowledge();old.resolve({capture:true,recall:true});await first;assert.equal(node('automatic-capture').checked,false,'late load must not enable capture');
}
{
 const {ctx,node}=fixture();ctx.api=async()=>({capture:true,recall:true});await ctx.loadAutomaticKnowledge();const gate=deferred();let writes=0;ctx.api=()=>{writes++;return gate.promise};
 const saving=ctx.saveAutomaticKnowledge();await ctx.saveAutomaticKnowledge();await ctx.loadAutomaticKnowledge();assert.equal(writes,1,'saving blocks duplicate writes and reloads');assert.equal(node('automatic-capture').disabled,true);gate.resolve({});await saving;assert.equal(node('automatic-save').disabled,false);
}
{
 const {ctx,node}=fixture(),gate=deferred();ctx.api=()=>gate.promise;const loading=ctx.loadAutomaticKnowledge();ctx.shellEpoch++;node('automatic-status').textContent='new login';gate.resolve({capture:true,recall:true});await loading;assert.equal(node('automatic-status').textContent,'new login');
}
{
 const {ctx,node}=fixture();ctx.api=async()=>({config:{enabled:true,directory:'fixture',include_runs:false,include_automatic:false},report:{conflicts:[],warnings:[]}});await ctx.loadVault();assert.equal(node('vault-automatic').checked,false);
 let body;ctx.api=async(path,method,data)=>{if(method==='PUT')body=data;return path.startsWith('library/search')?{documents:[],truncated:false}:{conflicts:[],warnings:[]}};node('vault-automatic').checked=true;await ctx.saveVault();assert.equal(body.include_automatic,true);assert.equal(body.include_runs,false,'automatic export and full run export remain independent');
}
console.log('PASS: citation provenance and isolation, escaped previews, automatic settings defaults/opt-out, failed/stale requests, write lock, and separate Vault export control.');
{
 const {ctx,node}=fixture(),old=deferred();let count=0;const response=directory=>({config:{directory,enabled:true,include_runs:false,include_automatic:false},document_directory:directory+'/Duo',report:{conflicts:[],warnings:[]}});
 ctx.api=()=>++count===1?old.promise:Promise.resolve(response('fresh'));
 const pending=ctx.loadVault();await ctx.loadVault();old.resolve(response('stale'));await pending;
 assert.equal(node('vault-directory').value,'fresh');assert.equal(node('vault-document-directory').textContent,'fresh/Duo');
}
{
 const {ctx,node}=fixture();let calls=0;ctx.api=async()=>{calls++;throw new Error('cannot load directory')};await ctx.loadVault();await ctx.saveVault();await ctx.refreshVault();
 assert.equal(calls,1,'failed reads must never save unchecked defaults or trigger synchronization');assert.equal(node('vault-save').disabled,true);assert.match(node('vault-status').textContent,/cannot load/);
}
{
 const {ctx,node}=fixture();ctx.api=async()=>({config:{directory:'first',enabled:true},document_directory:'first/Duo',report:{conflicts:[],warnings:[]}});await ctx.loadVault();
 const gate=deferred(),calls=[];ctx.api=(path,method)=>{calls.push({path,method});return method==='PUT'?gate.promise:Promise.resolve({conflicts:[],warnings:[]})};
 const saving=ctx.saveVault();await ctx.saveVault();await ctx.loadVault();await ctx.refreshVault();assert.equal(calls.length,1,'save locks prevent duplicate writes, reloads, and concurrent sync');assert.equal(node('vault-directory').disabled,true);
 gate.resolve({enabled:true,document_directory:'second/Duo'});await saving;assert.equal(calls.length,2);assert.equal(calls[1].path,'library/vault/refresh');assert.equal(node('vault-document-directory').textContent,'second/Duo');assert.equal(node('vault-save').disabled,false);
}
{
 const {ctx,node}=fixture();ctx.api=async()=>({config:{directory:'first',enabled:true},report:{conflicts:[],warnings:[]}});await ctx.loadVault();
 const gate=deferred();let calls=0;ctx.api=()=>{calls++;return gate.promise};const saving=ctx.saveVault();ctx.shellEpoch++;node('vault-status').textContent='new login';
 gate.resolve({enabled:true,document_directory:'old/Duo'});await saving;assert.equal(calls,1,'stale save cannot trigger a sync after login changes');assert.equal(node('vault-status').textContent,'new login');
}
{
 const {ctx,node,calls}=fixture();ctx.detail={task:{id:'one'}};await ctx.openLibrary();assert.equal(calls.length,3);assert(calls.some(c=>c.path.startsWith('library/search')));assert(calls.every(c=>!c.method||c.method==='GET'),'overview only reads status; it never saves settings');assert.equal(node('library-manage-task').disabled,false);
 ctx.creatingTask=true;await ctx.openLibrary();assert.equal(node('library-manage-task').disabled,true);
}
console.log('PASS: directory settings reject stale reads/writes, duplicate saves and failed loads; search remains separate from settings.');
{
 const {document,ctx,requests}=await createWebShellFixture(),node=id=>document.getElementById(id),run=code=>runInContext(code,ctx),api=ctx.api;
 const calls=[];ctx.api=async(path,method,body,...rest)=>{calls.push({path,method,body});if(path.startsWith('library/search'))return {documents:[],truncated:false};if(path==='library/vault/refresh')return {updated:1,conflicts:[],warnings:[]};return api(path,method,body,...rest)};
 assert.equal(document.querySelectorAll('[data-settings="knowledge"]').length,0,'knowledge setup is no longer a second top-level settings category');
 assert.equal(node('settings-knowledge').parentElement,node('library-dialog'),'one original settings editor lives in the knowledge library');
 assert.equal(node('library-settings-open').parentElement.className,'library-pages','setup is one in-library page rather than another header shortcut');
 assert.equal(document.querySelectorAll('#vault-directory').length,1);
 await Promise.all([ctx.openLibrarySettings(),ctx.openLibrarySettings()]);
 assert(node('library-dialog').open);assert(!node('settings-dialog').open,'knowledge setup never opens the unrelated global editor');
 assert(!node('settings-knowledge').classList.contains('hidden'));assert(node('library-browse-panel').classList.contains('hidden'));
 assert.equal(node('knowledge-data-directory').textContent,'C:/fixture/data','advanced data location is loaded without opening settings');
 assert(calls.every(call=>call.method==='GET'),'opening settings only reads configuration');
 for(const path of ['settings','library/vault','library/automatic'])assert.equal(calls.filter(call=>call.path===path).length,1,'concurrent knowledge opens share one read: '+path);
 node('vault-directory').value='/fixture/unsaved-notes';node('vault-directory').oninput();node('automatic-capture').checked=false;node('automatic-capture').onchange();
 node('library-browse-open').onclick();for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
 assert(calls.some(call=>call.path.startsWith('library/search')),'switching from a settings deep link starts the document browser');
 assert(!node('library-browse-panel').classList.contains('hidden'));assert(node('settings-knowledge').classList.contains('hidden'));
 await ctx.openLibrarySettings();
 assert.equal(node('vault-directory').value,'/fixture/unsaved-notes');assert.equal(node('automatic-capture').checked,false,'switching pages never overwrites unsaved choices');
 node('library-dialog').close();await ctx.openLibrarySettings();
 assert.equal(node('vault-directory').value,'/fixture/unsaved-notes');assert.equal(node('automatic-capture').checked,false,'closing and reopening preserves unsaved settings');
 const reads=calls.length;await ctx.loadVault();await ctx.loadAutomaticKnowledge();assert.equal(calls.length,reads,'background reloads cannot overwrite dirty controls');
 const normalAPI=ctx.api;ctx.api=async(path,method,...rest)=>{if(method==='PUT')throw new Error('fixture failed save');return normalAPI(path,method,...rest)};
 await ctx.saveVault();await ctx.saveAutomaticKnowledge();assert(run('vaultSettingsDirty&&automaticSettingsDirty'),'failed saves retain drafts and dirty flags');
 ctx.api=async(path,method,body,...rest)=>{if(method==='PUT')return path==='library/vault'?{...body,document_directory:body.directory+'/Duo'}:{};return normalAPI(path,method,body,...rest)};
 await ctx.saveVault();await ctx.saveAutomaticKnowledge();assert.equal(run('vaultSettingsDirty||automaticSettingsDirty'),false,'successful individual saves clear their own dirty flags');
 // Reading runtime location must not replace another editor's settings snapshot.
 const snapshot=run('settings');ctx.api=async(path,...rest)=>path==='settings'?{data_dir:'D:/fixture/changed-data'}:normalAPI(path,...rest);await ctx.loadKnowledgeSettings();
 assert.equal(run('settings'),snapshot);assert.equal(node('knowledge-data-directory').textContent,'D:/fixture/changed-data');
 const gate=deferred();ctx.api=()=>gate.promise;const pending=ctx.loadKnowledgeSettings();run('renewShellScope()');node('knowledge-data-directory').textContent='next login';gate.resolve({data_dir:'old account data'});await pending;
 assert.equal(node('knowledge-data-directory').textContent,'next login','late directory responses cannot cross login epochs');
 assert.equal(run('vaultSettingsDirty||automaticSettingsDirty'),false,'shell disposal clears the former login drafts');
 assert(requests.every(request=>request.method==='GET'&&!request.path.includes('/models/test')));
}
console.log('PASS: knowledge browsing and accumulation share one dialog and one editor; tab switches, reopening, failed saves, runtime-location refresh and login changes preserve or isolate drafts. Synthetic only.');
