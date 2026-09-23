import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';

const css=await readFile(new URL('../web/workbench.css',import.meta.url),'utf8');
// Check the final declaration for each selector, since this stylesheet layers
// responsive workbench rules over the base theme. Browser geometry is verified
// separately against the running app at the normal 248px sidebar width.
const declarations=(selector,source=css)=>{
 const bodies=[...source.replace(/\/\*[\s\S]*?\*\//g,'').matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .filter(match=>match[1].trim().split(',').map(part=>part.trim()).includes(selector))
  .map(match=>match[2]);
 assert(bodies.length,'missing rule: '+selector);
 return Object.fromEntries(bodies.join(';').split(';').filter(Boolean).map(part=>{
  const colon=part.indexOf(':');return [part.slice(0,colon).trim(),part.slice(colon+1).trim()];
 }));
};
assert.equal(declarations('.sticky-board>header').display,'grid');
assert.equal(declarations('.sticky-board>header')['grid-template-columns'],'16px minmax(0,1fr) minmax(0,1fr) auto');
assert.equal(declarations('#sticky-count')['white-space'],'nowrap');
assert.equal(declarations('#sticky-count')['grid-row'],'1');
assert.equal(declarations('#sticky-filter')['grid-row'],'2');
assert.equal(declarations('#sticky-scope')['grid-row'],'2');
assert.equal(declarations('.sticky-board>header select')['min-width'],'0');
assert.equal(declarations('.task-list')['overflow-x'],'hidden');
assert.equal(declarations('.workspace-tasks')['grid-template-columns'],'minmax(0,1fr)');
assert.equal(declarations('.task-row>.task').width,'auto');
assert.equal(declarations('.task-list .task strong')['text-overflow'],'ellipsis');
assert.equal(declarations('.task-list .task strong')['white-space'],'nowrap');
assert.equal(declarations('.task-item-menu>div').position,'fixed','task popovers must remain outside the scrolling list');
assert.equal(declarations('#sidebar-resizer').right,'-4px','the drag handle stays reachable');
assert.equal(declarations('.sidebar').overflow,undefined,'do not clip the sidebar resize handle');
assert.match(css,/@media\(max-width:760px\)\{\.panel-resizer\{display:none\}\}/,'mobile behavior remains intact');

const refinement=css.slice(css.indexOf('/* Readable shared chrome.'));
assert(refinement.startsWith('/* Readable shared chrome.'),'shared refinement section is present');
const mobile=refinement.slice(refinement.lastIndexOf('@media(max-width:760px){'));
const desktop=refinement.slice(0,refinement.lastIndexOf('@media(max-width:760px){'));
assert(!refinement.includes('!important'),'readability rules should not fight layout visibility or drag states');
assert.equal(declarations('.create-form',desktop).width,'min(860px,100%)');
assert.equal(declarations('.create-form',desktop).margin,'0 auto');
assert.equal(declarations('.create-page-head',desktop).display,'grid');
assert.equal(declarations('.create-page-head',desktop)['grid-template-areas'],'"title action" "lead lead"');
assert.equal(declarations('.create-page-head>h2',desktop)['white-space'],'nowrap');
assert.equal(declarations('.create-page-head>p',desktop)['grid-area'],'lead');
assert.equal(declarations('.create-permission',desktop)['grid-column'],'1 / -1');
assert.equal(declarations('.create-context-field>select',desktop).width,'100%');
assert.equal(declarations('#model-picker-label')['text-overflow'],'ellipsis');
assert.equal(declarations('#model-picker-label')['white-space'],'nowrap');
assert.equal(declarations('.model-item small',css.slice(0,css.lastIndexOf('@media(max-width:760px){')))['max-width'],'44%','long source labels cannot squeeze model IDs on desktop');
assert.equal(declarations('.model-item small')['min-width'],'0');
assert.equal(declarations('.model-item small')['flex-shrink'],'1');
assert.equal(declarations('.create-permission>small',mobile).display,'block','important execution boundaries remain visible on phones');
assert.equal(declarations('.create-options',mobile)['grid-template-columns'],'minmax(0,1fr) minmax(0,1fr)');
assert.equal(declarations('.create-icon-button',mobile)['grid-row'],'3','attachment stays clear of the two engine/model controls');
assert.equal(declarations('.app button',mobile)['min-height'],'40px');
assert.equal(declarations('dialog button',mobile)['min-height'],'40px');
assert.equal(declarations('.create-segmented button',mobile)['min-height'],'40px');
for(const selector of ['.model-manager','.engine-card']){
 assert.equal(declarations(selector).background,'var(--surface)',selector+' follows both themes');
 assert.equal(declarations(selector)['box-shadow'],'none');
}
assert.equal(declarations('.configured-model').background,'var(--canvas)');
assert.equal(declarations('.settings-nav')['overflow-x'],'auto','settings categories remain reachable at 390px');
assert.equal(declarations('.model-add-row',mobile)['grid-template-columns'],'minmax(0,1fr)');
assert.equal(declarations('.create-page',mobile).padding,'24px 16px 28px');
console.log('PASS: narrow sidebar safety; readable task form, separate permissions, model truncation, themed settings, and phone touch/overflow rules.');
