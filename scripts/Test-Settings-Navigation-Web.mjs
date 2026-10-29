import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';

const {document,ctx,requests}=await createWebShellFixture(),node=id=>document.getElementById(id),run=code=>runInContext(code,ctx);
const nav=node('workspace-settings');
assert(nav,'settings categories are visible without selecting a task');
assert.equal(nav.parentElement,node('app-menu-items'),'categories replace settings in its existing toolbar');
assert.equal(node('settings-open'),null,'generic settings entry is replaced');
assert.equal(node('usage-shortcut'),null,'usage is moved rather than duplicated');
assert.equal(node('settings-form').querySelector('.settings-nav'),null,'the dialog has no duplicate category navigation');
assert.equal(node('settings-form').querySelector('[data-settings]'),null,'category buttons have moved out of the dialog');
assert.equal(document.querySelector('.header').nextElementSibling,node('tabs'),'there is no standalone settings row');
assert.equal(nav.closest('details'),null,'settings categories do not require opening a menu');
const pages=[...nav.querySelectorAll('[data-settings]')].map(button=>button.dataset.settings);
assert(pages.length>=4);
assert(!pages.includes('knowledge'),'knowledge settings live inside the knowledge library');
for(const page of pages){
 const shortcuts=document.querySelectorAll('[data-settings="'+page+'"]');
 assert.equal(shortcuts.length,1,'one original button per settings category');
 assert.equal(shortcuts[0].parentElement,nav);
 assert.equal(shortcuts[0].getAttribute('aria-controls'),'settings-dialog');
 assert.equal(shortcuts[0].getAttribute('aria-haspopup'),'dialog');
}
let updateLoads=0,usageOpens=0;
ctx.loadUpdateInformation=async()=>{updateLoads++};ctx.openUsageDashboard=async()=>{usageOpens++};
for(const page of pages){
 node('settings-dialog').close();
 await nav.querySelector('[data-settings="'+page+'"]').onclick();
 assert(node('settings-dialog').open,'shortcut opens its existing dialog');
 assert(!node('settings-'+page).classList.contains('hidden'),page+' opens directly');
 assert(document.querySelector('[data-settings="'+page+'"]').classList.contains('selected'));
 assert.equal(node('settings-form').querySelector('h2').textContent,nav.querySelector('[data-settings="'+page+'"]').textContent,'dialog title identifies the current category');
}
assert(updateLoads>0);
await node('usage-open').onclick();assert.equal(usageOpens,1,'original usage button opens directly');
assert.equal(node('bind-open').textContent,'任务绑定');
assert.equal([...document.querySelectorAll('.header button')].filter(button=>button.textContent==='飞书连接').length,1,'global Feishu configuration and task binding have distinct labels');

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
const first=ctx.openSettingsPage('access'),second=ctx.openSettingsPage('updates');
assert.equal(loads,1);finish(loaded);await Promise.all([first,second]);
assert(!node('settings-updates').classList.contains('hidden'),'last requested category wins');
assert(node('settings-access').classList.contains('hidden'));
await ctx.openSettingsPage('missing-category');assert(!node('settings-updates').classList.contains('hidden'),'unknown categories leave the current page intact');
ctx.api=api;node('settings-dialog').close();

// A late settings load cannot reopen its dialog over the unified library.
loads=0;ctx.api=(path,...rest)=>path==='settings'&&++loads===1?new Promise(resolve=>{finish=resolve}):api(path,...rest);
const pending=ctx.openSettingsPage('access'),library=ctx.openSettingsPage('knowledge');
finish(loaded);await Promise.all([pending,library]);ctx.api=api;
assert(node('library-dialog').open,'knowledge route opens the unified library');
assert(!node('settings-dialog').open,'late settings load cannot cover the library');
assert(!node('settings-knowledge').classList.contains('hidden'));
assert(node('library-browse-panel').classList.contains('hidden'));
assert.equal(document.querySelectorAll('[data-settings="knowledge"]').length,0);
assert.equal(node('settings-knowledge').closest('dialog'),node('library-dialog'));
node('library-dialog').close();

// Knowledge setup starts with the directory and keeps original switches/IDs.
const section=node('settings-knowledge'),panels=[...section.children];
assert(panels.indexOf(node('vault-settings'))<panels.indexOf(node('automatic-knowledge-settings')));
assert(panels.indexOf(section.querySelector('.knowledge-storage-info'))>panels.indexOf(node('automatic-knowledge-settings')));
assert.match(section.textContent,/一任务一篇主文档/);assert.match(section.textContent,/Duo\/tasks\/任务名-短ID\.md/);
assert.match(section.textContent,/可留空使用默认目录/);assert.match(node('automatic-knowledge-settings').textContent,/还需开启/);
for(const id of ['vault-enabled','vault-automatic','automatic-capture','automatic-organize'])assert(node(id));

const css=await readFile(new URL('../web/workbench.css',import.meta.url),'utf8');
assert.match(css,/\.workspace-settings\{display:contents\}/,'moved categories share the original toolbar layout');
assert.match(css,/\.header-global-actions\{[^}]*flex-wrap:wrap/,'desktop toolbar can wrap naturally');
assert.match(css,/\.header-global-actions\{[^}]*display:grid;grid-template-columns:repeat\(5,minmax\(0,1fr\)\)/,'phone toolbar keeps all categories visible');
assert.match(css,/\.header \.workspace-settings>button\{min-height:44px/,'phone categories retain touch targets');

ctx.location.search='?settings=access';await ctx.boot();
assert(node('settings-dialog').open,'access deep link still opens settings');
assert(!node('settings-access').classList.contains('hidden'));
assert.equal(document.querySelectorAll('#workspace-settings').length,1,'fresh login does not duplicate shortcut bars');
ctx.location.search='?settings=knowledge';await ctx.boot();
assert(node('library-dialog').open,'legacy knowledge deep link opens the unified library');
assert(!node('settings-dialog').open);
assert(!node('settings-knowledge').classList.contains('hidden'));
assert(requests.every(request=>request.method==='GET'&&!request.path.includes('/models/test')));
run('authenticated=false;renewShellScope()');
console.log('PASS: original category buttons replace settings in the existing toolbar without duplicated controls or a second navigation row; one editor preserves drafts, concurrent opens, task binding and access deep links. Synthetic only.');
