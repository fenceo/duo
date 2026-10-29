import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';
const {ctx,document,window}=await createWebShellFixture(),run=s=>runInContext(s,ctx),el=id=>document.getElementById(id);
run(`chosen='human-task';detail={task:{id:chosen,title:'Synthetic',engine:'codex'},runs:[],events:[]};tasks=[detail.task]`);
let active=false,human=false,actions=[],frames=0,waitAction,waitFrame,waitControl,delayAction=false,delayFrame=false,delayControl=false,releases=0;
const state=()=>({active,task_id:active?'human-task':undefined,target:'desktop',control:false,human_control:human});
const frame=()=>({id:'frame-'+(++frames),image:'synthetic',bounds:{x:-1920,y:-100,width:1920,height:1080}});
ctx.api=async(path,method,body)=>{
 if(path==='desktop/targets')return [{id:'desktop',title:'Synthetic desktop'}];
 if(path==='desktop'){if(method==='DELETE'){active=false;human=false}return state()}
 if(path==='tasks/human-task/desktop'){active=true;return state()}
 if(path.endsWith('/control')){human=body.enabled;if(!human)releases++;if(human&&delayControl)return new Promise(resolve=>waitControl=resolve);return human?{token:'synthetic-token',state:state()}:state()}
 if(path.endsWith('/control/action')){actions.push(body);if(delayAction)return new Promise(resolve=>waitAction=resolve);return {ok:true}}
 if(path.endsWith('/frame')){if(delayFrame)return new Promise(resolve=>waitFrame=resolve);return frame()}
 throw Error(path);
};
await ctx.openDesktopSharing();await ctx.startDesktopSharing();await ctx.previewDesktop();
const img=el('desktop-preview-image');img.getBoundingClientRect=()=>({left:100,top:200,width:962,height:542});
for(const [k,v] of Object.entries({clientLeft:1,clientTop:1,clientWidth:960,clientHeight:540}))Object.defineProperty(img,k,{configurable:true,value:v});
const mouse={clientX:581,clientY:471,button:0};
await ctx.desktopPointerAction('click',mouse);assert.equal(actions.length,0,'view-only image must never send input');
await ctx.toggleDesktopHuman();assert(human);assert(!el('desktop-manual-tools').classList.contains('hidden'));
run("desktopState.instance='server-fixture';desktopState.revision=4");ctx.renderDesktopSharing({...state(),instance:'server-fixture',revision:3,human_control:false});assert.equal(run('desktopHumanToken'),'synthetic-token','old poll cannot revoke a freshly granted controller');
img.onpointerdown({clientX:100,clientY:200});img.onpointermove({clientX:200,clientY:250,buttons:1});img.onclick({...mouse,detail:1});assert.equal(actions.length,0,'unsupported drag must not turn into an unintended click');
await ctx.desktopPointerAction('click',mouse);assert.equal(actions.length,1);assert.deepEqual(JSON.parse(JSON.stringify(actions[0].action)),{action:'click',x:960,y:540,frame:'frame-2'});
await ctx.desktopPointerAction('right_click',{clientX:100,clientY:200});assert.equal(actions.length,1,'border/outside click ignored');
await ctx.desktopPointerAction('scroll',mouse,-3);assert.equal(actions.at(-1).action.delta,-3);assert.equal(actions.at(-1).token,'synthetic-token');
delayAction=true;const first=ctx.desktopHumanAction({action:'type',text:'中文'});await ctx.desktopHumanAction({action:'key',key:'enter'});assert.equal(actions.length,3,'actions must not queue behind pending input');waitAction({ok:true});await first;delayAction=false;
// A displayed frame stays actionable while an automatic refresh downloads.
delayFrame=true;const refresh=ctx.previewDesktop();await new Promise(resolve=>setImmediate(resolve));
const previous=run('desktopFrame.id');delayFrame=false;await ctx.desktopPointerAction('click',mouse);assert.equal(actions.at(-1).action.frame,previous);const newest=run('desktopFrame.id');waitFrame({id:'stale',image:'old',bounds:{width:1,height:1}});await refresh;assert.equal(run('desktopFrame.id'),newest);
const goodApi=ctx.api;ctx.api=async path=>{if(path.endsWith('/action'))throw Error('uncertain input');return goodApi(path)};
await ctx.desktopHumanAction({action:'key',key:'enter'});assert.equal(run('desktopFrame'),null);assert.match(el('desktop-status').textContent,/不会自动重试/);const count=actions.length;await ctx.desktopPointerAction('click',mouse);assert.equal(actions.length,count);
ctx.api=goodApi;await ctx.previewDesktop();await ctx.endDesktopHuman();assert(!human);assert.equal(run('desktopHumanToken'),'');assert(el('desktop-manual-tools').classList.contains('hidden'));assert.equal(run('desktopState.control'),false,'manual control must not change AI permission');
await ctx.toggleDesktopHuman();el('desktop-dialog').close();await new Promise(resolve=>setImmediate(resolve));assert(!human);assert(!img.hasAttribute('src'),'closing panel clears sensitive image and releases control');
await ctx.openDesktopSharing();await ctx.toggleDesktopHuman();
const dialog=el('desktop-dialog'),nativeClose=dialog.close.bind(dialog),queuedCloses=[];
dialog.close=()=>{const wasOpen=dialog.open;dialog.open=false;if(wasOpen)queuedCloses.push(()=>dialog.dispatchEvent(new window.Event('close')))};
delayFrame=true;const oldCapture=ctx.previewDesktop();await new Promise(resolve=>setImmediate(resolve));const beforeRelease=releases;
run(`chosen='another-task';detail={task:{id:chosen,title:'Another',engine:'codex'},runs:[],events:[]};syncDesktopTaskContext()`);
assert(!el('desktop-dialog').open);assert.equal(run('desktopHumanToken'),'');assert.equal(run('desktopFrame'),null);assert(!img.hasAttribute('src'));assert.equal(releases,beforeRelease+1,'switching tasks releases the old manual controller');
assert.equal(queuedCloses.length,1,'native close event has not fired yet');
await ctx.openDesktopSharing();const newRevision=run('desktopRevision');queuedCloses.shift()();
assert(dialog.open,'late close event cannot dismiss a reopened task dialog');assert.equal(run('desktopDialogTask'),'another-task');assert.equal(run('desktopRevision'),newRevision,'late close event cannot invalidate the new task request');
waitFrame({id:'late-frame',image:'sensitive-old-task',bounds:{width:1,height:1}});await oldCapture;delayFrame=false;
assert(!img.hasAttribute('src'),'late old-task capture cannot restore the previous preview');assert.equal(run('desktopFrame'),null);
assert(active,'switching tasks does not revoke the original AI sharing');assert.equal(actions.length,count,'task navigation never sends desktop input');
dialog.close=nativeClose;dialog.close();
// A manual lease granted after task navigation must be released immediately.
run(`chosen='human-task';detail={task:{id:chosen,title:'Synthetic',engine:'codex'},runs:[],events:[]}`);
await ctx.openDesktopSharing();delayControl=true;const beforeFrames=frames,pendingControl=ctx.toggleDesktopHuman();
run(`chosen='another-task';detail={task:{id:chosen,title:'Another',engine:'codex'},runs:[],events:[]};syncDesktopTaskContext()`);
waitControl({token:'synthetic-token',state:state()});await pendingControl;await new Promise(resolve=>setImmediate(resolve));
assert(!human);assert.equal(run('desktopHumanToken'),'');assert.equal(frames,beforeFrames,'late lease must not start capturing for the old task');assert(!el('desktop-dialog').open);
console.log('PASS: browser coordinates, manual lease, Unicode, no queued/replayed input, capture/action races, task-switch controller release, stale frames and late-lease cleanup. Synthetic only.');
