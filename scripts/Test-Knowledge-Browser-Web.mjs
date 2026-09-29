import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const {document,ctx}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=source=>runInContext(source,ctx);
const formatted=document.createElement('div');formatted.innerHTML=ctx.markdown('## 标题\n普通正文\n- 条目\n后续文字\n\n```text\n## 原样代码\n```');
assert.equal(formatted.querySelector('h2').textContent,'标题');assert.equal(formatted.querySelector('p').textContent,'普通正文');assert.equal(formatted.querySelector('li').textContent,'条目');assert.match(formatted.querySelector('pre').textContent,/## 原样代码/);
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}};
const hits=[{id:'knowledge:k1',source:'auto',kind:'knowledge',task_id:'task',task_title:'合成任务',title:'<img src=x onerror=1> 自动记录',status:'observed',revision:1,updated:1,origin:'local',hash:'one',snippet:'自动保存不等于已验证'},
 {id:'knowledge:k2',source:'manual',kind:'knowledge',task_id:'task',task_title:'合成任务',title:'已验证操作',status:'verified',revision:1,updated:2,origin:'local',hash:'two',snippet:'已完成合成测试'}];
const calls=[];
ctx.api=async(path,method='GET',body)=>{
 calls.push({path,method,body});
 if(path==='library/automatic')return {capture:true,recall:false};
 if(path==='library/vault')return {config:{enabled:false},report:{}};
 if(path.startsWith('library/search'))return {documents:new URLSearchParams(path.split('?')[1]).get('offset')==='0'?[hits[0]]:[hits[1]],total:2,truncated:path.endsWith('offset=0'),next_offset:path.endsWith('offset=0')?1:2};
 if(path.startsWith('library/reference'))return {preview:'## 历史资料\n<script>not executable</script>',reference:'\n\n【引用资料】\n来源：knowledge:k1；待验证\n历史资料\n',truncated:false};
 if(path.startsWith('library/document'))return {...hits[0],content:'## 历史资料\n<script>not executable</script>'};
 throw Error('Unexpected fixture request '+path);
};
run("chosen='task';tasks=[{id:chosen,title:'合成任务'}];detail={task:tasks[0],runs:[],events:[]}");
node('vault-directory').value='unsaved path';
await ctx.openLibrary();
assert.equal(node('library-layer').value,'tasks','the library starts with task documents');
assert(calls.find(c=>c.path.startsWith('library/search')).path.includes('layer=tasks'));
assert.equal(node('vault-directory').value,'unsaved path','overview cannot overwrite settings drafts');
assert.match(node('library-overview').textContent,/不自动补回/);
assert.equal(document.querySelector('#library-results img'),null,'listing escapes titles');
await ctx.searchLibrary(true);
assert.equal(document.querySelectorAll('.library-card').length,2);
assert(node('library-more').classList.contains('hidden'));
const previewTrigger=document.querySelector('[data-library-preview]');
await ctx.previewLibrary(hits[0].id);
assert.equal(document.querySelector('[data-library-preview]'),previewTrigger,'opening a preview keeps the triggering node and keyboard focus stable');
assert.match(node('library-preview-content').textContent,/历史资料/);
assert.equal(node('library-preview-content').querySelector('script'),null,'preview cannot execute stored HTML');
assert(!node('library-preview-cite').disabled);

// A late preview or failed search cannot leave old content actionable.
const oldPreview=deferred(),baseAPI=ctx.api;ctx.api=()=>oldPreview.promise;
const pendingPreview=ctx.previewLibrary(hits[0].id);ctx.clearLibraryPreview();oldPreview.resolve({preview:'late',reference:'late'});await pendingPreview;
assert.equal(node('library-preview-content').textContent,'');
ctx.api=async()=>{throw Error('offline')};await ctx.searchLibrary();
assert.equal(document.querySelectorAll('[data-library-reference]').length,0);assert.match(node('library-state').textContent,/offline/);
ctx.api=baseAPI;await ctx.searchLibrary();
const quote=deferred();let reads=0;ctx.api=()=>{reads++;return quote.promise};node('message').value='现有草稿';
const quoting=ctx.citeLibrary(hits[0].id);await ctx.citeLibrary(hits[0].id);assert.equal(reads,1,'duplicate citation clicks make one request');
quote.resolve({reference:'\n来源与状态',truncated:false});await quoting;
assert.equal(node('message').value,'现有草稿\n来源与状态');

const entry={id:'k1',task_id:'task',title:'串口排查',content:'正文内容\n'.repeat(80),status:'observed',source:'auto',run_id:'r1',revision:1,created:1,updated:1};
ctx.sample=entry;run('knowledgeItems=[sample,{...sample,id:"k2",source:"manual",title:"其他结论",status:"verified"}];renderKnowledgeList()');
assert.equal(document.querySelectorAll('.knowledge-card').length,2);
assert.equal(document.querySelector('.knowledge-body').innerHTML,'','closed cards do not render large bodies');
const expand=document.querySelector('[data-knowledge-toggle]');expand.onclick();assert.equal(expand.getAttribute('aria-expanded'),'true');assert.match(document.querySelector('.knowledge-body').textContent,/正文内容/);
node('knowledge-query').value='串口';node('knowledge-query').oninput();assert.equal(document.querySelectorAll('.knowledge-card').length,1);assert.equal(document.querySelector('[data-knowledge-toggle]').getAttribute('aria-expanded'),'true','refresh preserves expansion');
node('knowledge-source').value='manual';node('knowledge-source').onchange();assert.equal(document.querySelectorAll('.knowledge-card').length,0);
node('knowledge-source').value='';node('knowledge-query').value='';ctx.renderKnowledgeList();

// Editing pins the original revision, even when background polling refreshes it.
ctx.editKnowledge('k1');node('knowledge-body').value='未保存的修改';run('knowledgeItems[0]={...knowledgeItems[0],revision:2,content:"另一窗口修改"};renderKnowledgeList()');
let saved;ctx.api=async(path,method,body)=>{saved={path,method,body};throw Object.assign(Error('conflict'),{status:409})};
await ctx.saveKnowledge({preventDefault(){}});
assert.equal(saved.body.revision,1);assert.equal(node('knowledge-body').value,'未保存的修改');assert.match(node('knowledge-error').textContent,/草稿仍保留/);
run('chosen="other";selection++');saved=null;await ctx.saveKnowledge({preventDefault(){}});assert.equal(saved,null,'task switching cannot retarget the editor');

// Task-panel citations also preserve a revision and ignore a late task switch.
run('chosen="task"');const taskQuote=deferred();ctx.api=(path)=>{assert(path.includes('revision=1'));return taskQuote.promise};node('message').value='另一份草稿';
const taskPending=ctx.citeTaskKnowledge(entry);run('selection++;chosen="other"');taskQuote.resolve({reference:'wrong task'});await taskPending;assert.equal(node('message').value,'另一份草稿');
run('authenticated=false;renewShellScope()');
console.log('PASS: browse filters/pages, escaped previews, honest status, preserved settings/drafts, citation locks and task isolation, collapsed/searchable knowledge, pinned edit revision and conflicts. Synthetic only.');
