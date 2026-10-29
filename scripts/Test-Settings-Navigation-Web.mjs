import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';

const {document,ctx,requests}=await createWebShellFixture(),node=id=>document.getElementById(id),run=code=>runInContext(code,ctx);
const nav=node('workspace-settings');
assert(nav,'settings shortcuts are visible without selecting a task');
assert.equal(nav.previousElementSibling,document.querySelector('.header'));
assert.equal(nav.closest('details'),null,'settings categories do not require opening a menu');
const pages=[...document.querySelectorAll('.settings-nav [data-settings]')].map(button=>button.dataset.settings);
assert(pages.length>=5);
for(const page of pages){
 const shortcuts=nav.querySelectorAll('[data-settings-open="'+page+'"]');
 assert.equal(shortcuts.length,1,'one direct shortcut per settings category');
 assert.equal(shortcuts[0].getAttribute('aria-controls'),'settings-dialog');
 assert.equal(shortcuts[0].getAttribute('aria-haspopup'),'dialog');
}
let knowledgeLoads=0,updateLoads=0,usageOpens=0;
ctx.loadKnowledgeSettings=async()=>{knowledgeLoads++};ctx.loadUpdateInformation=async()=>{updateLoads++};ctx.openUsageDashboard=async()=>{usageOpens++};
for(const page of pages){
 node('settings-dialog').close();
 await nav.querySelector('[data-settings-open="'+page+'"]').onclick();
 assert(node('settings-dialog').open,'shortcut opens its existing dialog');
 assert(!node('settings-'+page).classList.contains('hidden'),page+' opens directly');
 assert(document.querySelector('[data-settings="'+page+'"]').classList.contains('selected'));
}
assert(knowledgeLoads>0);assert(updateLoads>0);
await node('usage-shortcut').onclick();assert.equal(usageOpens,1,'usage opens directly');

// Changing the visible section must preserve edits and reuse the same editor.
await ctx.openSettingsPage('feishu');node('feishu-id').value='unsaved fixture app';node('setting-workspaces').value='/unsaved-fixture';
const reads=requests.filter(request=>request.path==='/api/settings').length;
await ctx.openSettingsPage('access');await ctx.openSettingsPage('feishu');
assert.equal(node('feishu-id').value,'unsaved fixture app');assert.equal(node('setting-workspaces').value,'/unsaved-fixture');
assert.equal(requests.filter(request=>request.path==='/api/settings').length,reads,'section switch must not reload unsaved settings');
assert.equal(document.querySelectorAll('#vault-directory').length,1,'shortcuts do not duplicate settings fields');

// Multiple quick clicks share one settings read; the last click chooses the page.
node('settings-dialog').close();let finish,loads=0;const api=ctx.api,loaded=run('JSON.parse(JSON.stringify(settings))');
ctx.api=(path,...rest)=>path==='settings'?(loads++,new Promise(resolve=>{finish=resolve})):api(path,...rest);
const first=ctx.openSettingsPage('access'),second=ctx.openSettingsPage('knowledge');
assert.equal(loads,1);finish(loaded);await Promise.all([first,second]);
assert(!node('settings-knowledge').classList.contains('hidden'),'last requested category wins');
assert(node('settings-access').classList.contains('hidden'));
await ctx.openSettingsPage('missing-category');assert(!node('settings-knowledge').classList.contains('hidden'),'unknown categories leave the current page intact');
ctx.api=api;node('settings-dialog').close();

// Knowledge setup starts with the directory and keeps original switches/IDs.
const section=node('settings-knowledge'),panels=[...section.children];
assert(panels.indexOf(node('vault-settings'))<panels.indexOf(node('automatic-knowledge-settings')));
assert(panels.indexOf(section.querySelector('.knowledge-storage-info'))>panels.indexOf(node('automatic-knowledge-settings')));
assert.match(section.textContent,/一任务一篇主文档/);assert.match(section.textContent,/Duo\/tasks\/任务名-短ID\.md/);
assert.match(section.textContent,/可留空使用默认目录/);assert.match(node('automatic-knowledge-settings').textContent,/还需开启/);
for(const id of ['vault-enabled','vault-automatic','automatic-capture','automatic-organize'])assert(node(id));

const css=await readFile(new URL('../web/workbench.css',import.meta.url),'utf8');
assert.match(css,/\.workspace-settings\{[^}]*flex-wrap:wrap/,'desktop categories can wrap without clipping');
assert.match(css,/\.workspace-settings\{[^}]*flex-wrap:nowrap;overflow-x:auto/,'phone categories remain reachable by horizontal scroll');
assert.match(css,/\.workspace-settings>button\{min-height:40px/,'phone controls retain touch targets');

ctx.location.search='?settings=access';await ctx.boot();
assert(node('settings-dialog').open,'access deep link still opens settings');
assert(!node('settings-access').classList.contains('hidden'));
assert.equal(document.querySelectorAll('#workspace-settings').length,1,'fresh login does not duplicate shortcut bars');
assert(requests.every(request=>request.method==='GET'&&!request.path.includes('/models/test')));
run('authenticated=false;renewShellScope()');
console.log('PASS: all settings categories open directly from the top bar; one editor preserves drafts, concurrent opens select the latest category, knowledge setup is directory-first, mobile scrolling and access deep links remain available. Synthetic only.');
