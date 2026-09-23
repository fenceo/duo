import {readFile} from 'node:fs/promises';
import assert from 'node:assert/strict';

const css=await readFile(new URL('../web/workbench.css',import.meta.url),'utf8');
// Check the final declaration for each selector, since this stylesheet layers
// responsive workbench rules over the base theme. Browser geometry is verified
// separately against the running app at the normal 248px sidebar width.
const declarations=selector=>{
 const bodies=[...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
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
console.log('PASS: narrow sticky header/count/filter layout, task text truncation, bounded sidebar scrolling, fixed menus and unchanged resize/mobile controls.');
