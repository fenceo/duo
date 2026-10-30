import assert from 'node:assert/strict';
import {runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

const {ctx,document}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx),tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
let vault={enabled:true,directory:'Fixture/old',root_mode:'direct',human_layout:true,consensus_directory:'平台共识',system_directory:'系统知识',project_directory:'项目知识'};
let needsDirectory=false;
let preview={source_directory:'Fixture/old',target_directory:'Fixture/new',fingerprint:'fixture-fingerprint',file_count:2,total_bytes:30,copy_count:1,identical_count:1,files:[{path:'项目知识/Duo/知识流程.md',size:20,action:'copy'},{path:'平台共识/协作.md',size:10,action:'identical'}],conflicts:[],warnings:[],source_dirty:false,ready:true};
let doc={id:'curated:one',kind:'curated',title:'知识流程',layer:'project',scope_key:'Duo',source_task_id:'one',task_id:'one',task_title:'Fixture task',content:'# 当前正文\n\n有据可查。',status:'observed',revision:7,updated:1,hash:'fixture',path:'项目知识/Duo/知识流程.md',pending_revisions:[{key:'changed',content:'# 旧基线候选',evidence:'旧基线报告',applicability:'合成测试',tags:['知识'],source_id:'source:changed',created:1,base_changed:true},{key:'merged',content:'# 完整合并稿',evidence:'合成来源',applicability:'合成测试',tags:['知识'],source_id:'source:merged',created:2,supersedes:['changed']}]};
const calls=[],notices=[],confirms=[];
ctx.notify=message=>notices.push(message);ctx.confirm=message=>(confirms.push(message),true);
ctx.api=async(path,method='GET',body)=>{
 calls.push({path,method,body});
 if(path==='settings')return {data_dir:'Fixture/data'};
 if(path==='library/automatic')return {capture:true,recall:true,organize:true};
 if(path==='library/extraction')return {enabled:true,needs_directory:needsDirectory};
 if(path==='library/vault')return {config:{...vault},report:{conflicts:[],warnings:[]},document_directory:vault.directory};
 if(path==='library/vault/migration/preview')return structuredClone(preview);
 if(path==='library/vault/migration'){vault={...vault,directory:body.directory,root_mode:body.root_mode};return {...preview,completed:true,copied:1,config:{...vault}}}
 if(path==='library/curated/one/consolidate')return {run:{id:'consolidation'}};
 if(path==='library/vault/refresh')return {updated:1,indexed:2,exported:1,imported:0,conflicts:[],warnings:[]};
 if(path.startsWith('library/search'))return {documents:[structuredClone(doc)],total:1,truncated:false};
 if(path.startsWith('library/document'))return structuredClone(doc);
 if(path==='tasks/one/knowledge')return [];
 throw Error('Unexpected fixture request '+path);
};
run("tasks=[{id:'one',title:'Fixture task',engine:'codex'},{id:'two',title:'Other task',engine:'claude'}];chosen='one';selection=1;creatingTask=false;detail={task:tasks[0],runs:[],events:[]}");
await ctx.openLibrarySettings();
for(const id of ['vault-settings','automatic-knowledge-settings','knowledge-extraction-settings','knowledge-migration-settings'])assert.equal(node(id).parentElement,node('settings-knowledge'),'numbered settings groups are peers rather than nested saves');
assert.match(node('knowledge-start-guide').textContent,/第一次使用与验收.*第 1 组.*第 2 组.*第 3 组.*Obsidian/);
assert.equal(document.querySelectorAll('#library-open').length,1,'the workflow adds no global knowledge entry');

await ctx.previewKnowledgeMigration();assert.match(node('knowledge-migration-state').textContent,/填写新笔记根目录/);
node('knowledge-migration-directory').value='Fixture/new';
run('vaultSettingsDirty=true');await ctx.previewKnowledgeMigration();assert.match(node('knowledge-migration-state').textContent,/先保存或放弃/);run('vaultSettingsDirty=false');
const before=calls.length;await ctx.previewKnowledgeMigration();assert.equal(calls.length,before+1);assert(!node('knowledge-migration-apply').disabled);assert.match(node('knowledge-migration-files').textContent,/复制.*知识流程.md.*相同，跳过/s);
node('knowledge-migration-directory').oninput();assert(node('knowledge-migration-apply').disabled,'target edits invalidate the reviewed fingerprint');
preview={...preview,source_dirty:true,ready:false};await ctx.previewKnowledgeMigration();assert(node('knowledge-migration-apply').disabled);assert.match(node('knowledge-migration-state').textContent,/先.*立即同步/);const blocked=calls.length;await ctx.applyKnowledgeMigration();assert.equal(calls.length,blocked,'dirty source never starts a migration');
preview={...preview,source_dirty:false,ready:false,conflicts:['目标有不同内容']};await ctx.previewKnowledgeMigration();assert(node('knowledge-migration-apply').disabled);assert.match(node('knowledge-migration-state').textContent,/目标有不同内容/);
preview={...preview,ready:true,conflicts:[]};await ctx.previewKnowledgeMigration();
run('vaultSettingsDirty=true');const changedRoot=calls.length;await ctx.applyKnowledgeMigration();assert.equal(calls.length,changedRoot,'a new unsaved root draft invalidates an earlier migration preview');assert(node('knowledge-migration-apply').disabled);run('vaultSettingsDirty=false');await ctx.previewKnowledgeMigration();
ctx.confirm=()=>false;await ctx.applyKnowledgeMigration();assert(!calls.some(c=>c.path==='library/vault/migration'),'cancelled confirmation never mutates');
ctx.confirm=message=>(confirms.push(message),true);
const baseAPI=ctx.api,gate=deferred();let migrationPosts=0;
ctx.api=(path,method,...rest)=>path==='library/vault/migration'?(migrationPosts++,gate.promise):baseAPI(path,method,...rest);
const migrating=ctx.applyKnowledgeMigration();await tick();await ctx.applyKnowledgeMigration();await ctx.saveVault();await node('logout').onclick();assert.equal(migrationPosts,1,'migration locks duplicate application and root-setting writes');assert(!calls.some(c=>c.path==='logout'),'logout waits for the migration mutation response');assert(notices.some(s=>s.includes('请等待操作返回')));assert(node('vault-save').disabled);assert.match(confirms.at(-1),/来源.*Fixture\/old.*目标.*Fixture\/new.*保留知识身份/s);
vault={...vault,directory:'Fixture/new'};gate.resolve({...preview,completed:true,copied:1,config:{...vault}});await migrating;assert.match(node('knowledge-migration-state').textContent,/迁移完成/);assert.equal(node('vault-directory').value,'Fixture/new');assert(node('knowledge-migration-apply').disabled,'successful migration consumes the preview');ctx.api=baseAPI;
node('knowledge-migration-directory').value='Fixture/retry';node('knowledge-migration-directory').oninput();await ctx.previewKnowledgeMigration();ctx.api=(path,...rest)=>path==='library/vault/migration'?Promise.reject(Error('synthetic connection lost')):baseAPI(path,...rest);await ctx.applyKnowledgeMigration();assert.match(node('knowledge-migration-state').textContent,/未确认.*核对实际知识根/);assert(!node('knowledge-migration-state').textContent.includes('当前知识根未切换'),'a lost response cannot prove the server did not switch roots');assert(node('knowledge-migration-apply').disabled);ctx.api=baseAPI;await ctx.loadVault();

await ctx.openLibrary('one');run("input('library-layer').value='project'");await ctx.searchLibrary();assert.match(node('library-organization-scope').textContent,/Fixture task.*项目知识.*AI.*Fixture task.*三类知识/);
assert.match(node('library-results').textContent,/查看待合并修订（2）/);await ctx.previewLibrary('curated:one');assert(node('library-preview-content').querySelector('.knowledge-pending-revisions').open,'pending proposals are directly readable');
assert(node('library-preview-content').querySelector('[data-knowledge-revision="changed"][data-accept="true"]').disabled);assert.match(node('library-preview-content').textContent,/原正文已.*变化.*完整合并稿.*覆盖 1/s);
const staleBefore=calls.length;await ctx.resolveKnowledgeRevision('changed',true);assert.equal(calls.length,staleBefore,'changed baselines cannot be adopted even via direct handler calls');
needsDirectory=true;const beforeDirectoryPrompt=confirms.length;await ctx.consolidateKnowledgeTopic();assert.equal(confirms.length,beforeDirectoryPrompt,'a missing notebook directory is configured before asking to spend model quota');assert(!node('settings-knowledge').classList.contains('hidden'));assert.match(node('vault-status').textContent,/先选择 Obsidian/);needsDirectory=false;await ctx.openLibrary('one');await ctx.previewLibrary('curated:one');
ctx.confirm=()=>false;await ctx.consolidateKnowledgeTopic();assert(!calls.some(c=>c.path.endsWith('/consolidate')),'topic AI requests require explicit product confirmation');
ctx.confirm=message=>(confirms.push(message),true);const topicGate=deferred();let topicPosts=0;ctx.api=(path,...rest)=>path.endsWith('/consolidate')?(topicPosts++,topicGate.promise):baseAPI(path,...rest);
const consolidating=ctx.consolidateKnowledgeTopic();await tick();await ctx.consolidateKnowledgeTopic();assert.equal(topicPosts,1);assert(node('library-topic-consolidate').disabled);assert.match(confirms.at(-1),/全部待合并修订.*消耗模型额度.*原正文保持不变/s);
run("input('library-scope').value='two'");node('library-revision-state').textContent='other scope';topicGate.resolve({run:{id:'queued'}});await consolidating;assert.equal(node('library-revision-state').textContent,'other scope','late topic results do not replace another scope status');assert(!node('library-topic-consolidate').disabled,'the active preview unlocks after an operation on another scope finishes');ctx.api=baseAPI;
run("input('library-scope').value='one'");await ctx.consolidateKnowledgeTopic();const topicCall=calls.find(c=>c.path.endsWith('/consolidate'));assert.deepEqual({...topicCall.body},{task_id:'one',revision:7,confirm:true});assert.match(node('library-revision-state').textContent,/已排队.*原正文尚未更改/);

ctx.showVaultReport({updated:1,indexed:2,exported:0,imported:0,conflicts:['合成冲突'],warnings:[]});assert(!node('library-sync-state').classList.contains('hidden'));assert.match(node('library-sync-state').textContent,/合成冲突.*整理成功不代表文件已同步/);
ctx.showVaultReport({updated:1,indexed:2,exported:1,imported:0,conflicts:[],warnings:[]});assert.match(node('library-sync-state').textContent,/笔记文件已同步/);assert(!node('library-sync-state').classList.contains('knowledge-organize-error'));

// Structured state has an independent workspace consumer. Never render its
// JSON as conversation output, but preserve the public event cursor.
run("sequence=0;resetConversation();element('conversation').replaceChildren();detail={task:tasks[0],runs:[],events:[]};appendEvents([{seq:40,run_id:'run',kind:'task_state',text:'{\"state\":{\"goal\":\"internal\"}}'}])");
assert.equal(run('sequence'),40);assert.equal(run('conversationItems.size'),0);assert(!node('conversation').textContent.includes('internal'));
run("appendEvents([{seq:41,run_id:'run',kind:'progress',text:'公开进度'}])");assert.equal(run('sequence'),41);assert.equal(run('conversationItems.size'),1);
run('renewShellScope()');assert.equal(run('knowledgeMigrationPreview'),null);
console.log('PASS: knowledge setup workflow, bounded scope guidance, migration preview/confirmation/dirty/conflict/fingerprint/duplicate locks, topic consolidation and stale-baseline/scope guards, visible sync state and hidden structured-state events. Synthetic only.');
