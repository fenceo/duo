import {readFile} from 'node:fs/promises';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture();
const node=id=>document.getElementById(id);
runInContext('dockNarrow.matches=true;applyDockLayout()',ctx);
assert.equal(node('sticky-board').parentElement,node('sidebar'),'phone notes stay in the task drawer, preserving conversation space');
assert.equal(node('sticky-expand').getAttribute('aria-expanded'),'true','notes default to expanded inside the drawer');
for(const previous of ['left','right','bottom','sidebar']){
 ctx.localStorage.setItem('jianzuo-dock-layout-v1',JSON.stringify({sticky:previous,tools:'left'}));
 runInContext('installDockLayout()',ctx);
 assert.equal(node('sticky-board').parentElement,node('sidebar'),'old note positions return to the task drawer');
 assert.deepEqual(JSON.parse(ctx.localStorage.getItem('jianzuo-dock-layout-v1')),{sticky:'sidebar',tools:'left'},'migration keeps tool panel preferences');
 runInContext("moveDockPanel('sticky','right')",ctx);
 assert.equal(node('sticky-board').parentElement,node('sidebar'),'notes cannot be moved accidentally');
}
assert(!document.querySelector('[data-dock-grip="sticky"]'));
assert(!node('appearance-sticky-zone'));
assert(document.querySelector('[data-dock-grip="tools"]'));
ctx.innerHeight=800;node('sticky-board').getBoundingClientRect=()=>({height:300});
node('sticky-resizer').onkeydown({key:'ArrowUp',preventDefault(){}});
assert.equal(node('sticky-board').style.getPropertyValue('--sticky-height'),'316px');
assert.equal(ctx.localStorage.getItem('jianzuo-sticky-height'),'316','height remains adjustable and persistent');
node('sticky-resizer').ondblclick();
assert.equal(ctx.localStorage.getItem('jianzuo-sticky-height'),null);
const css=await readFile(new URL('../web/workbench.css',import.meta.url),'utf8');
const mobile=css.slice(css.lastIndexOf('@media(max-width:760px){'));
assert.match(mobile,/\.composer-tools\{display:grid;grid-template-columns:auto auto auto minmax\(0,1fr\) 44px;/);
assert.match(mobile,/\.composer-tools button,\.composer-tools select\{[^}]*min-height:44px/);
assert.match(mobile,/\.composer-tools \.mode-engine-hint\{grid-column:1 \/ -1\}/,'engine restrictions remain visible on their own row');
assert.match(mobile,/\.composer-bottom\{display:grid;grid-template-columns:minmax\(0,1fr\) auto;/,'model and send share a row');
assert.match(mobile,/\.composer-bottom button\{min-height:44px;min-width:44px\}/);
assert.match(mobile,/#chat-column \.composer textarea\{min-height:56px;max-height:120px;overflow-y:auto\}/,'long drafts remain scrollable');
assert(!mobile.includes('.session-banner{display:none'),'do not conceal session boundaries');

runInContext('authenticated=false;renewShellScope()',ctx);
console.log('PASS: fixed notes migrate old positions, preserve tool placement and height resizing; compact accessible phone controls. Browser geometry is checked separately.');
