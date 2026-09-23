import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import assert from 'node:assert/strict';

// All API calls and confirmations are in-memory fixtures. No browser, model,
// credential, or application data is touched by this regression suite.
const source=await readFile(new URL('../web/execution.ts',import.meta.url),'utf8');
const mod=await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source,{mode:'transform'})+'\nexport {resolveModelProbeTarget,testCreateModels,invalidateModelTest,setCreateModels};').toString('base64'));
const nodes=new Map();
function node(id){
 if(!nodes.has(id))nodes.set(id,{value:'',textContent:'',innerHTML:'',disabled:false,classList:{add(){},remove(){},contains(){return true}},setAttribute(){}});
 return nodes.get(id);
}
globalThis.element=node;globalThis.input=node;globalThis.button=node;
globalThis.escapeHTML=value=>String(value);
const env={id:'wsl',name:'WSL test',type:'wsl',model:'codex-default',claude_model:'claude-default',harness_provider:'local-gateway',harness_model:'gateway-default',workspaces:['/tmp/probe']};
globalThis.settings={config:{environments:[env]}};
globalThis.creatingTask=true;
globalThis.createSubmitting=false;
let requests=[],confirmations=[],allow=true,responder=async body=>response(body.models[0]);
globalThis.confirm=message=>{confirmations.push(message);return allow};
globalThis.api=async(path,method,body)=>{requests.push({path,method,body});return responder(body)};
function response(model){return {engine:'deepseek-harness',workspace:'/tmp/probe',results:[{model,status:'available',message:'fixture OK',duration_ms:1}]}}
function configure({engine='deepseek-harness',selected='gateway-default',custom='',workspace='/tmp/probe'}={}){
 globalThis.creatingTask=true;node('model-picker-button').disabled=false;
 node('create-environment').value='wsl';node('create-engine').value=engine;
 node('create-model').value=selected;node('custom-model').value=custom;node('create-workspace').value=workspace;
 mod.invalidateModelTest();requests=[];confirmations=[];allow=true;
 responder=async body=>response(body.models[0]);
}
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}

assert.equal(mod.resolveModelProbeTarget(env,'codex','','','/tmp').model,'codex-default');
assert.equal(mod.resolveModelProbeTarget(env,'claude','','','/tmp').model,'claude-default');
assert.equal(mod.resolveModelProbeTarget(env,'deepseek-harness','','','/tmp').model,'gateway-default');
assert.equal(mod.resolveModelProbeTarget(env,'deepseek-harness','__custom__','  custom-only  ',' /tmp ').model,'custom-only');
assert.throws(()=>mod.resolveModelProbeTarget({...env,model:''},'codex','','','/tmp'),/明确的模型 ID/);
assert.throws(()=>mod.resolveModelProbeTarget(env,'codex','__custom__','','/tmp'),/明确的模型 ID/);
assert.throws(()=>mod.resolveModelProbeTarget(env,'codex','bad\nmodel','','/tmp'),/无效/);

configure();
mod.setCreateModels([{id:'catalog-a',name:'A'},{id:'catalog-b',name:'B'}],'catalog-a');
node('create-model').value='__custom__';node('custom-model').value='not-in-catalog';
await mod.testCreateModels();
assert.equal(requests.length,1);
assert.deepEqual(requests[0],{path:'environments/wsl/models/test',method:'POST',body:{engine:'deepseek-harness',workspace:'/tmp/probe',models:['not-in-catalog']}});
assert.match(confirmations[0],/一条最小测试消息/);assert.match(confirmations[0],/少量模型额度/);
for(const value of ['WSL test','local-gateway','not-in-catalog','/tmp/probe'])assert(confirmations[0].includes(value));
assert.match(node('model-test-result').textContent,/可用 1\/1/);

configure();allow=false;
await mod.testCreateModels();assert.equal(requests.length,0,'declined confirmation must send nothing');
configure();globalThis.createSubmitting=true;
mod.invalidateModelTest();assert.equal(node('test-models').disabled,true);
await mod.testCreateModels();assert.equal(requests.length,0,'task submission must lock model probes');
globalThis.createSubmitting=false;
configure({engine:'codex',selected:'__custom__'});
await mod.testCreateModels();assert.equal(requests.length,0);assert.equal(confirmations.length,0);
assert.match(node('model-test-result').textContent,/明确的模型 ID/,'empty model must never trigger the backend catalog fallback');

configure();
let waiting=deferred();responder=()=>waiting.promise;
let pending=mod.testCreateModels();
await mod.testCreateModels();
assert.equal(requests.length,1,'a second click while pending must not create another paid request');
assert.equal(confirmations.length,1);assert.equal(node('test-models').disabled,true);
node('create-engine').value='codex';mod.invalidateModelTest();
mod.setCreateModels([{id:'codex-other',name:'Codex'}],'codex-other');
assert.equal(node('test-models').disabled,true,'catalog completion must not unlock an in-flight probe');
waiting.resolve(response('gateway-default'));await pending;
assert.equal(node('model-test-result').textContent,'','old engine response must not overwrite the new engine UI');
assert.equal(node('test-models').disabled,false,'busy lock is released after the old request settles');

for(const changed of ['closed','model','custom','environment','provider','workspace','invalidated']){
 configure({selected:changed==='custom'?'__custom__':'gateway-default',custom:'custom-one'});
 waiting=deferred();responder=()=>waiting.promise;pending=mod.testCreateModels();
 if(changed==='closed')globalThis.creatingTask=false;
 if(changed==='model')node('create-model').value='different-model';
 if(changed==='custom')node('custom-model').value='custom-two';
 if(changed==='environment')node('create-environment').value='another-environment';
 if(changed==='provider')env.harness_provider='another-provider';
 if(changed==='workspace')node('create-workspace').value='/tmp/another';
 if(changed==='invalidated')mod.invalidateModelTest();
 node('model-test-result').textContent='new-screen';
 waiting.resolve(response('stale-model'));await pending;
 assert.equal(node('model-test-result').textContent,'new-screen',changed+' must isolate stale success');
 env.harness_provider='local-gateway';
}
configure();waiting=deferred();responder=()=>waiting.promise;pending=mod.testCreateModels();
globalThis.creatingTask=false;node('model-test-result').textContent='closed-screen';
waiting.reject(new Error('old failed request'));await pending;
assert.equal(node('model-test-result').textContent,'closed-screen','closing the page must isolate stale failures too');

configure();mod.setCreateModels([],'');
assert.equal(node('test-models').disabled,false,'an empty catalog must not prevent a later custom-model test');
node('create-model').value='__custom__';node('custom-model').value='custom-without-catalog';
await mod.testCreateModels();assert.deepEqual(requests[0].body.models,['custom-without-catalog']);
console.log('PASS: single selected/default/custom model, explicit cost confirmation, no catalog fallback, duplicate-click lock, and stale response isolation. No real model calls.');
