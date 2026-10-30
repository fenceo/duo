import assert from 'node:assert/strict';
import {runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

const {ctx,document}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx),tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
const calls=[],notices=[];
ctx.notify=message=>notices.push(message);
let extraction={enabled:true,scheduled:true,interval_hours:24,last_organized:1700000000000,last_error:'synthetic retry needed',needs_directory:false};
let vault={enabled:true,directory:'Fixture',include_runs:false,include_automatic:true,include_legacy:true};
let documentRecord={id:'curated:source',kind:'curated',title:'NFS 调试',content:'# 原有正文',layer:'project',scope_key:'375',task_id:'one',source_task_id:'one',task_title:'Synthetic task',status:'verified',revision:7,hash:'hash7',updated:1,path:'项目/375/NFS调试.md',pending_revisions:[{key:'revision/one',content:'# 新结论\n\n<script>synthetic</script>',evidence:'synthetic command succeeded',applicability:'EVT1 only',tags:['NFS'],source_id:'knowledge:source',source_task_id:'one',created:1700000000000}]};
ctx.api=async(path,method='GET',body)=>{
 calls.push({path,method,body});
 if(path==='settings')return {data_dir:'C:/fixture/data'};
 if(path==='library/automatic')return {capture:true,recall:true,organize:true};
 if(path==='library/extraction'){if(method==='PUT')extraction={...extraction,...body};return {...extraction}}
 if(path==='library/vault'){if(method==='PUT')vault={...vault,...body};return method==='PUT'?{...vault,document_directory:vault.directory+(vault.root_mode==='direct'?'':'/Duo')}:{config:{...vault},report:{conflicts:[],warnings:[]},document_directory:vault.directory+(vault.root_mode==='direct'?'':'/Duo')}}
 if(path==='library/vault/refresh')return {updated:1,exported:0,indexed:1,imported:0,conflicts:[],warnings:[]};
 if(path==='library/extract')return {created:1,revisions:2,duplicates:3,skipped:4};
 if(path.startsWith('library/search'))return {documents:[{...documentRecord}],total:1,truncated:false};
 if(path.startsWith('library/document'))return {...documentRecord};
 if(path==='tasks/one/knowledge')return [];
 if(path.startsWith('library/curated/source/revisions/')){documentRecord={...documentRecord,revision:8,status:body.accept?'observed':documentRecord.status,content:body.accept?'# 已采用修订':documentRecord.content,pending_revisions:[]};return {...documentRecord}}
 throw Error('Unexpected fixture request '+path);
};
run("tasks=[{id:'one',title:'Synthetic task'}];chosen='one';selection=1;creatingTask=false;detail={task:tasks[0],runs:[],events:[]}");
assert.match(node('settings-knowledge').textContent,/第一层.*工作区.*第二层.*平台共识.*系统知识.*项目知识/);
assert.match(node('knowledge-extraction-last').parentElement.textContent,/不额外调用模型/);
assert.equal(node('vault-runs').closest('details').id,'vault-legacy-settings','raw conversation export is an optional compatibility setting');
await ctx.openLibrarySettings();
assert.equal(node('vault-root-mode').value,'legacy','existing roots keep the old location until explicitly changed');
assert.equal(node('vault-legacy').checked,true);
assert.equal(node('vault-consensus-directory').value,'consensus','legacy folder defaults are displayed accurately before an explicit layout change');assert.equal(node('vault-system-directory').value,'system');assert.equal(node('vault-project-directory').value,'projects');
assert.equal(node('knowledge-extraction-scheduled').checked,true);
assert.equal(node('knowledge-extraction-interval').value,'24');
assert.match(node('knowledge-extraction-last').textContent,/最近定时整理/);
assert.match(node('knowledge-extraction-error').textContent,/最近定时整理未完成.*synthetic retry/);
assert(!node('knowledge-extraction-error').classList.contains('hidden'));

node('vault-root-mode').value='direct';node('vault-root-mode').onchange();node('vault-legacy').checked=false;node('vault-legacy').onchange();
node('knowledge-extraction-interval').value='12';node('knowledge-extraction-interval').oninput();node('knowledge-extraction-scheduled').checked=false;node('knowledge-extraction-scheduled').onchange();
node('library-browse-open').onclick();await tick();node('library-dialog').close();await ctx.openLibrarySettings();
assert.equal(node('vault-root-mode').value,'direct');assert.equal(node('vault-legacy').checked,false);
assert.equal(node('knowledge-extraction-interval').value,'12');assert.equal(node('knowledge-extraction-scheduled').checked,false,'unsaved schedule survives navigation and reopening');
assert.equal(calls.filter(call=>call.method==='PUT').length,0,'opening or switching pages never saves drafts');
await ctx.saveVault();const vaultWrite=calls.find(call=>call.path==='library/vault'&&call.method==='PUT');
assert.equal(vaultWrite.body.root_mode,'direct');assert.equal(vaultWrite.body.include_legacy,false);assert.equal(vaultWrite.body.human_layout,true);assert.equal(node('vault-document-directory').textContent,'Fixture');
node('knowledge-extraction-interval').value='0';const writes=calls.filter(call=>call.method==='PUT').length;await ctx.saveKnowledgeExtractionSettings();
assert.equal(calls.filter(call=>call.method==='PUT').length,writes,'invalid interval cannot write settings');assert.match(node('knowledge-extraction-settings-state').textContent,/1～168/);
node('knowledge-extraction-interval').value='12';await ctx.saveKnowledgeExtractionSettings();
const schedule=calls.find(call=>call.path==='library/extraction'&&call.method==='PUT');assert.deepEqual({...schedule.body},{enabled:true,scheduled:false,interval_hours:12});
assert.equal(run('knowledgeExtractionSettingsDirty'),false);assert.match(node('knowledge-extraction-settings-state').textContent,/不另起模型调用/);

const ordinaryAPI=ctx.api;
let saveCount=0;const scheduleGate=deferred();ctx.api=(path,method,...rest)=>path==='library/extraction'&&method==='PUT'?(saveCount++,scheduleGate.promise):ordinaryAPI(path,method,...rest);
node('knowledge-extraction-scheduled').checked=true;node('knowledge-extraction-scheduled').onchange();
const saving=ctx.saveKnowledgeExtractionSettings();await ctx.saveKnowledgeExtractionSettings();await ctx.loadKnowledgeExtractionSettings();
assert.equal(saveCount,1);assert(node('knowledge-extraction-interval').disabled);scheduleGate.resolve({enabled:true,scheduled:true,interval_hours:12});await saving;assert(!node('knowledge-extraction-interval').disabled);assert.equal(node('knowledge-extraction-last').textContent,'尚无成功定时整理记录。');
ctx.api=async(path,method,...rest)=>{if(path==='library/extraction'&&method==='PUT')throw Error('synthetic save failed');return ordinaryAPI(path,method,...rest)};
node('knowledge-extraction-interval').value='6';node('knowledge-extraction-interval').oninput();await ctx.saveKnowledgeExtractionSettings();
assert(run('knowledgeExtractionSettingsDirty'),'failed save preserves schedule draft');await ctx.loadKnowledgeExtractionSettings();assert.equal(node('knowledge-extraction-interval').value,'6');
ctx.api=ordinaryAPI;await ctx.saveKnowledgeExtractionSettings();

ctx.showLibraryPage('browse');run("input('library-layer').value='project';input('library-scope').value='one'");calls.length=0;
await ctx.extractRecordedKnowledge();assert.match(node('library-extraction-state').textContent,/新增 1.*修订候选 2.*已存在 3.*跳过 4/);
assert.equal(calls.filter(call=>call.path==='library/extract').length,1);assert(!calls.some(call=>call.path.endsWith('/knowledge/extract')),'saved-candidate organization never starts a model');
const extractionGate=deferred();ctx.api=(path,...rest)=>path==='library/extract'?extractionGate.promise:ordinaryAPI(path,...rest);
const extracting=ctx.extractRecordedKnowledge();await tick();run("input('library-layer').value='system'");await ctx.searchLibrary();node('library-extraction-state').textContent='new view';
extractionGate.resolve({created:4,revisions:0,duplicates:0,skipped:0});await extracting;assert.equal(node('library-extraction-state').textContent,'new view','old extraction cannot replace another scope status');assert(notices.some(message=>message.includes('原范围')));
ctx.api=ordinaryAPI;await ctx.previewLibrary('curated:source');
assert.equal(node('library-preview-content').querySelector('script'),null,'revision Markdown is sanitized');assert.match(node('library-preview-content').textContent,/待处理修订.*依据.*EVT1/s);
assert(!node('library-preview-meta').textContent.includes('Duo/'),'preview uses the indexed path without inventing another root');
run('curatedDirty=true');calls.length=0;await ctx.resolveKnowledgeRevision('revision/one',true);assert.equal(calls.length,0,'a pending knowledge draft cannot be invalidated by revision adoption');run('curatedDirty=false');
await ctx.resolveKnowledgeRevision('revision/one',true);
const adoption=calls.find(call=>call.path.includes('/revisions/'));assert.equal(adoption.path,'library/curated/source/revisions/revision%2Fone');assert.deepEqual({...adoption.body},{revision:7,accept:true});
assert.match(node('library-preview-content').textContent,/已采用修订/);assert.match(node('library-preview-status').textContent,/退回待验证/);assert.equal(node('library-preview-content').querySelector('[data-knowledge-revision]'),null);

documentRecord={...documentRecord,revision:9,pending_revisions:[{key:'late',content:'late proposal',evidence:'not tested',applicability:'synthetic',tags:[],source_id:'late',source_task_id:'one',created:1}]};
await ctx.previewLibrary('curated:source');const revisionGate=deferred();let revisionPosts=0;ctx.api=(path,...rest)=>path.includes('/revisions/')?(revisionPosts++,revisionGate.promise):ordinaryAPI(path,...rest);calls.length=0;
const adopting=ctx.resolveKnowledgeRevision('late',false);await ctx.resolveKnowledgeRevision('late',false);ctx.clearLibraryPreview();node('library-preview-status').textContent='another document';revisionGate.resolve({...documentRecord,pending_revisions:[]});await adopting;
assert.equal(revisionPosts,1,'duplicate revision clicks send only one mutation');assert.equal(node('library-preview-status').textContent,'another document','late revision results cannot reopen the previous document');assert.equal(calls.filter(call=>call.path.startsWith('library/document')).length,0,'stale adoption does not fetch private previous content');ctx.api=ordinaryAPI;

run('knowledgeExtractionSettingsDirty=false');let failedReads=0;ctx.api=async()=>{failedReads++;throw Error('synthetic configuration unavailable')};await ctx.loadKnowledgeExtractionSettings();await ctx.saveKnowledgeExtractionSettings();assert.equal(failedReads,1,'failed schedule reads cannot write unchecked defaults');assert(node('knowledge-extraction-save').disabled);ctx.api=ordinaryAPI;await ctx.loadKnowledgeExtractionSettings();

const stale=deferred();ctx.api=()=>stale.promise;run('knowledgeExtractionSettingsDirty=false');const loading=ctx.loadKnowledgeExtractionSettings();run('renewShellScope()');node('knowledge-extraction-last').textContent='next login';stale.resolve({enabled:true,scheduled:true,interval_hours:1,last_organized:1,last_error:'former account'});await loading;
assert.equal(node('knowledge-extraction-last').textContent,'next login','late schedule metadata never crosses login epochs');
console.log('PASS: two-layer knowledge setup, explicit root compatibility, opt-in legacy mirrors, schedule validation/drafts/locks/failures, candidate-only organization, scope isolation, revision CAS/adoption/dirty protection and login isolation. Synthetic only.');
