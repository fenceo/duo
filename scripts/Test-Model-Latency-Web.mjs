import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
import assert from 'node:assert/strict';

// Exercise the real mounted shell and detached workflow rearrangement, with
// synthetic streams only. No CLI, model, credentials, or external HTTP calls.
const {ctx,document,intervals}=await createWebShellFixture();
const run=code=>runInContext(code,ctx),el=id=>document.getElementById(id);
for(const prefix of ['','task-']){
 const menu=el(prefix?'task-model-menu':'model-menu'),controls=el(prefix+'model-latency-controls');
 assert(menu.contains(controls),'latency controls belong to the model picker after workflow rearrangement');
 assert(controls.contains(el(prefix+'test-model-latency')));
 assert.equal(el(prefix+'model-test-timeout').value,'180');
 assert.deepEqual(Array.from(el(prefix+'model-test-timeout').options,option=>option.value),['60','120','180','300','600']);
}
run(`creatingTask=true;createSubmitting=false;input('create-environment').innerHTML='<option value="fixture">Fixture</option>';input('create-environment').value='fixture';input('create-engine').value='codex';input('create-workspace').value='/fixture';input('create-model').value='fixture-model';createModels=[{id:'fixture-model',name:'Fixture model'}];modelCatalogState.create.key=currentCreateCatalogContext().key;modelCatalogState.create.loading=false;modelCatalogState.create.failed=false;renderModelMenu('create')`);
ctx.confirm=()=>true;ctx.api=async()=>({profiles:[],active_profile:{}});
let request;
ctx.fetch=async(path,options)=>{
 request=options;
 const stream=new ReadableStream({start(controller){options.signal.addEventListener('abort',()=>controller.error(new DOMException('aborted','AbortError')),{once:true})}});
 return new Response(stream,{headers:{'Content-Type':'application/x-ndjson'}});
};
el('test-model-latency').onclick();
for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
assert(request);assert.equal(JSON.parse(request.body).timeout_seconds,180);
const timer=run('modelProbeStates.create.timer');assert(intervals.has(timer));
assert(el('test-model-latency').disabled);assert(el('model-test-timeout').disabled);assert(!el('stop-model-test').classList.contains('hidden'));assert(!el('stop-model-test').disabled);
run('renewShellScope()');
for(let i=0;i<5;i++)await new Promise(resolve=>setImmediate(resolve));
assert(request.signal.aborted);assert(!intervals.has(timer));assert.equal(el('model-test-result').textContent,'');
console.log('PASS: mounted create/task latency controls, timeout defaults/options, busy locking, visible stop, shell abort and timer cleanup. Synthetic only.');
