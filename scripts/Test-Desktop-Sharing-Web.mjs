import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document}=await createWebShellFixture(),run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
run(`chosen='desktop-task';detail={task:{id:chosen,title:'Fixture',engine:'codex'},runs:[],events:[]};tasks=[detail.task]`);
let captures=0,grants=0;ctx.api=async(path,method,body)=>{
 if(path==='desktop/targets')return [{id:'desktop',title:'整个桌面 <unsafe>'}];
 if(path==='desktop')return {active:false,control:false};
 if(path==='tasks/desktop-task/desktop'){grants++;assert.equal(method,'PUT');assert.equal(body.control,false);return {active:true,control:false,task_id:'desktop-task',target:'desktop'}}
 if(path.endsWith('/frame')){captures++;return {image:'synthetic'}}
 throw Error(path);
};
await ctx.openDesktopSharing();assert.equal(captures,0);assert(!el('desktop-allow-control').checked);assert.equal(el('desktop-target').textContent,'整个桌面 <unsafe>');
await ctx.startDesktopSharing();assert.equal(grants,1);assert.match(el('desktop-open').textContent,/接管/);assert.equal(captures,0,'grant does not automatically capture the real desktop');
await ctx.previewDesktop();assert.equal(captures,1);assert.match(el('desktop-preview-image').src,/data:image\/png;base64,/);
await ctx.stopDesktopSharing();assert(!el('desktop-preview-image').hasAttribute('src'));assert.equal(el('desktop-open').textContent,'桌面共享');
run(`desktopState={active:true,control:true,task_id:'other'};renderDesktopSharing()`);assert(el('desktop-start').disabled,'another task share cannot be silently stolen');assert(!el('desktop-stop').disabled,'takeover remains available');
ctx.api=async path=>{if(path==='desktop')return {active:true,control:true,task_id:'desktop-task'};throw Error('window list unavailable')};
await ctx.openDesktopSharing();assert(!el('desktop-stop').disabled,'revoke remains available when window listing fails');assert.match(el('desktop-status').textContent,/window list unavailable/);
console.log('PASS: explicit desktop selection, default view-only sharing, no automatic screenshots, manual preview, immediate revoke and task ownership guards. Synthetic only.');
