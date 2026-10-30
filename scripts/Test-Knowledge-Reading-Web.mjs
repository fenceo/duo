import assert from 'node:assert/strict';
import {runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

// Exercise production readers and navigation. All content, tasks and HTTP are
// synthetic; no real files, model, credentials or hardware are accessed.
const {ctx,document}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx),tick=()=>new Promise(resolve=>setImmediate(resolve));
const calls=[],notices=[];ctx.notify=text=>notices.push(text);
const primary=id=>`.duo/tasks/${id}-1234/任务记录.md`;
const body='# 375 平台验证\n\n## 目标\n验证启动与 NFS。\n\n## 当前计划\n保留实际进展。\n\n## 进展\n### 板端检查\n已检查启动状态。\n\n### 板端检查\n后续记录。\n\n```text\n## 代码中的假章节\n```';
const home={id:'vault:项目知识/375/README.md',path:'项目知识/375/README.md',kind:'index',layer:'project',scope_key:'375',title:'375 项目知识',status:'observed',revision:1,hash:'fixture-home',updated:1,task_id:'one',snippet:'## 启动与 NFS\n保留适用条件 <img src=x onerror=fixture>',content:body};
const linked={...home,id:'vault:项目知识/375/子目录/验证.md',path:'项目知识/375/子目录/验证.md',title:'验证详情',content:'# 验证详情\n\n## 实际结果\n合成验证。'};
const guide={...home,id:'curated-index:project:375',generated:true,title:'375 阅读手册',content:body+'\n\n```text\n## 分页代码里的假章节\n'+'fixture-code '.repeat(2000)+'\n```\n\n## 后续完整章节\n完整文档末尾。'};
const staleGuide={...guide,id:'vault:项目知识/375/阅读手册-previous-02.md',path:'项目知识/375/阅读手册-previous-02.md',title:'旧分页阅读手册'};
const curated={...linked,id:'curated:fixture-note',kind:'curated',content:'# 验证详情\n\n可编辑的原笔记。'};
const files=new Map(['one','two'].map(id=>[id,{path:primary(id),content:id==='one'?body:'# 同工作区任务\n\n## 目标\n参考上一任务。',revision:'v1',editable:true}]));
ctx.api=async(path,method='GET',payload)=>{
 calls.push({path,method,payload});
 if(path==='library/automatic')return {capture:true,recall:true,organize:true};
 if(path==='library/vault')return {config:{enabled:false},report:{conflicts:[],warnings:[]}};
 if(path.startsWith('library/search'))return {documents:[home,guide,staleGuide,curated],total:4,truncated:false};
 if(path.startsWith('library/document')){
  const id=new URLSearchParams(path.split('?')[1]).get('id');
  if(id==='document:'+linked.path||id===linked.id)return {...linked};
  if(id===home.id)return {...home};
  if(id===guide.id)return {...guide};
  if(id===staleGuide.id)return {...staleGuide};
  if(id===curated.id)return {...curated};
  throw Error('Unexpected document '+id);
 }
 const task=path.match(/^tasks\/([^/]+)\//)?.[1];
 if(path.endsWith('/workspace-memory'))return {enabled:true,task_directory:`.duo/tasks/${task}-1234`,main_document:primary(task),tasks:[{id:'one',title:'375 平台验证',path:primary('one')},{id:'two',title:'同工作区任务',path:primary('two'),excerpt:'## 目标\n参考上一任务。'},{id:'not-loaded',title:'另一个来源',path:primary('not-loaded')}],documents:[{path:primary(task),kind:'main'}],conflicts:[],warnings:[]};
 if(path.includes('/workspace-memory/document?')){const selected=new URLSearchParams(path.split('?')[1]).get('path');return selected===primary(task)?{...files.get(task)}:{path:selected,content:'# 历史文档\n\n## 结果\n只读内容',editable:false}}
 if(path.endsWith('/knowledge'))return [];
 throw Error('Unexpected reading fixture request '+path);
};
run("tasks=[{id:'one',title:'375 平台验证',workspace:'C:/fixture'},{id:'two',title:'同工作区任务',workspace:'C:/fixture'}];chosen='one';selection=1;creatingTask=false;detail={task:tasks[0],runs:[],events:[]}");

await ctx.openLibrary();
assert.match(node('library-results').textContent,/启动与 NFS.*保留适用条件/);
assert.equal(node('library-results').querySelector('img'),null,'snippets render as escaped text');
assert.equal(node('library-results').querySelector('.library-document-path'),null,'raw paths no longer replace readable summaries');
assert.match(node('library-results').textContent,/375.*待验证/);
assert.match(node('library-results').textContent,/项目知识 · 阅读手册.*以原笔记状态为准/);
await ctx.previewLibrary(home.id);
assert(!node('library-preview-meta').textContent.includes('README.md'),'technical locations live in collapsible details');
assert(!node('library-preview-content').querySelector('details').open);
assert.match(node('library-preview-content').querySelector('details').textContent,/项目知识\/375\/README.md/);
assert(!node('library-outline').classList.contains('hidden'));
assert.equal(node('library-outline').querySelectorAll('option').length,9,'six real headings plus prompt/start/end');
assert(!node('library-outline').textContent.includes('假章节'),'fenced code never enters the table of contents');
const duplicate=node('library-article').querySelector('[data-document-slug="板端检查-1"]');let jumped=false;duplicate.scrollIntoView=()=>{jumped=true};
const beforeFragment=calls.length;await ctx.followLibraryLink('#'+encodeURIComponent('板端检查-1'));assert(jumped);assert.equal(calls.length,beforeFragment,'same-document fragments do not fetch another file');

for(const invalid of ['../../../outside.md','%2e%2e/%2e%2e/%2e%2e/outside.md','%252e%252e/outside.md','%25252e%25252e/outside.md','%2foutside.md','%5coutside.md','%68%74%74%70%3A%2F%2Fexample.test/a.md','broken%.md']){
 const before=calls.length;await ctx.followLibraryLink(invalid);assert.equal(calls.length,before,'invalid or escaping links never reach the API: '+invalid);
}
assert(notices.some(text=>text.includes('超出'))&&notices.some(text=>text.includes('编码')));
await ctx.followLibraryLink(encodeURIComponent('子目录')+'/'+encodeURIComponent('验证')+'.md');
assert(calls.some(call=>call.path.startsWith('library/document')&&new URLSearchParams(call.path.split('?')[1]).get('id')==='document:'+linked.path),'encoded Chinese paths reach the API once decoded');
assert.match(node('library-preview-title').textContent,/验证详情/);

await ctx.previewLibrary(guide.id);
assert.match(node('library-preview-meta').textContent,/项目知识 · 阅读手册/);
assert.match(node('library-preview-content').textContent,/只读阅读手册.*请打开文中的原笔记/);
assert(!node('library-outline').textContent.includes('假章节'),'a code fence cut at the preview boundary remains code');
assert.match(node('library-outline').textContent,/已载入部分/);
node('library-read-more').onclick();assert.match(node('library-outline').textContent,/后续完整章节/);
assert(!node('library-outline').textContent.includes('假章节'),'loading more does not promote code into headings');
assert(node('library-promote').classList.contains('hidden'),'generated manuals cannot be promoted again');
assert(node('library-curated-edit').classList.contains('hidden'),'generated manuals are read only');
ctx.openCuratedEditor(guide);assert(!node('curated-dialog').open,'manual read-only protection also guards direct editor calls');
await ctx.previewLibrary(staleGuide.id);
assert.match(node('library-preview-content').textContent,/只读阅读手册/,'a generated vault page retains reading-only treatment after its current canonical page disappears');
assert(node('library-promote').classList.contains('hidden'),'old generated vault pages cannot become duplicate knowledge');
assert(node('library-curated-edit').classList.contains('hidden'));
ctx.openCuratedEditor(staleGuide);assert(!node('curated-dialog').open,'old generated vault pages also reject direct editor calls');
await ctx.previewLibrary(curated.id);
assert(!node('library-curated-edit').classList.contains('hidden'),'the original curated note remains editable');

// Deep excerpt headings render normally and consume the same slug counters as
// the offline organizer. Keep only main sections in the compact select.
node('library-article').innerHTML=ctx.markdown('# 阅读标题\n## 主章节\n### 子章节\n#### 按时间记录\n##### 按时间记录\n###### 按时间记录\n## 按时间记录\n\n```text\n#### 代码里的假标题\n```');
ctx.renderDocumentOutline('library-article','library-outline','fixture-heading');
for(let level=1;level<=6;level++)assert(node('library-article').querySelector('h'+level),'heading level '+level+' renders as a heading');
assert.equal(node('library-article').querySelectorAll('h1,h2,h3,h4,h5,h6').length,7,'code lines remain excluded from real headings');
assert.equal(node('library-article').querySelector('h4').textContent,'按时间记录','deep headings do not show raw Markdown markers');
assert.equal(node('library-outline').querySelectorAll('option').length,7,'only four main headings plus prompt/start/end enter navigation');
const deepHeading=node('library-article').querySelector('h6');assert.equal(deepHeading.dataset.documentSlug,'按时间记录-2');
const laterMain=node('library-article').querySelector('h2[data-document-slug="按时间记录-3"]');assert(laterMain,'deep headings participate in later duplicate anchor counts');
let deepJump=false;deepHeading.scrollIntoView=()=>{deepJump=true};await ctx.followLibraryLink('#'+encodeURIComponent('按时间记录-2'));assert(deepJump,'fragments can still target deep headings omitted from the compact select');
let mainJump=false;laterMain.scrollIntoView=()=>{mainJump=true};await ctx.followLibraryLink('#'+encodeURIComponent('按时间记录-3'));assert(mainJump,'offline generated duplicate anchors resolve to the later main heading');
node('library-article').innerHTML=ctx.markdown('#### [引用](子文档.md)\n## [引用](子文档.md)\n###### <img src=x onerror="fixture">');ctx.renderDocumentOutline('library-article','library-outline','fixture-source-heading');
assert.equal(node('library-article').querySelector('h2').dataset.documentSlug,'引用子文档md-1','slug generation uses the source heading, matching the offline organizer even when inline links render as labels');
assert.equal(node('library-article').querySelector('img'),null,'source heading metadata remains escaped');
assert.equal(node('library-article').querySelector('h6').getAttribute('onerror'),null,'source heading metadata cannot inject attributes');

await ctx.loadWorkspaceMemory();
assert.equal(node('workspace-memory-title').textContent,'375 平台验证');assert.equal(node('workspace-memory-path').textContent,primary('one'));
assert(!node('workspace-memory-outline').classList.contains('hidden'));
assert(!node('workspace-memory-related').classList.contains('hidden'));
assert.equal(node('workspace-memory-tasks').querySelectorAll('[data-workspace-task]').length,1,'only a known task can be opened from another workspace record');
assert(!node('workspace-memory-tasks').textContent.includes('.duo/'),'related-task navigation uses names and summaries');
const workspaceBefore=calls.length;await ctx.followWorkspaceDocumentLink('%252e%252e/outside.md');assert.equal(calls.length,workspaceBefore);
let workspaceJump=false;const goal=node('workspace-memory-content').querySelector('[data-document-slug="目标"]');goal.scrollIntoView=()=>{workspaceJump=true};await ctx.followWorkspaceDocumentLink('#'+encodeURIComponent('目标'));assert(workspaceJump);
node('workspace-memory-edit').onclick();node('workspace-memory-editor').value=body+'\n\n## 未保存的补充\n保留此草稿。';node('workspace-memory-editor').oninput();assert(node('workspace-memory-outline').classList.contains('hidden'),'editing never displays a stale reading outline');
ctx.choose=async id=>{ctx.nextTask=id;run("chosen=nextTask;selection++;detail={task:tasks.find(task=>task.id===chosen),runs:[],events:[]}")};
const relatedButton=node('workspace-memory-tasks').querySelector('[data-workspace-task]');node('workspace-memory-tasks').onclick({target:relatedButton});await tick();await tick();
assert.equal(run('chosen'),'two');assert(notices.some(text=>text.includes('草稿已保留')));
await ctx.openTaskRecords('one');await ctx.loadWorkspaceMemory();assert.match(node('workspace-memory-editor').value,/未保存的补充/,'related-task navigation preserves the original dirty draft');
node('workspace-memory-read').onclick();assert.match(node('workspace-memory-outline').textContent,/未保存的补充/,'reading mode derives the outline from the current draft');
console.log('PASS: readable escaped summaries and status, generated read-only current/old manuals and editable source notes, all six heading levels and duplicate anchors with compact navigation, collapsed locations, real Markdown titles, encoded Chinese links, traversal/multiple-encoding rejection, related-task navigation and dirty draft preservation. Synthetic only.');
