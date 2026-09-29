import {spawn} from 'node:child_process';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {randomBytes} from 'node:crypto';
import {createServer} from 'node:net';
import assert from 'node:assert/strict';

// Isolated HTTP acceptance check. No real account, task, model or hardware use.
const binary=resolve(process.argv[2]||'build/jianzuo-codex-native.exe');
const data=await mkdtemp(join(tmpdir(),'jianzuo-native-build-'));
const password=randomBytes(24).toString('hex');
const run=(args,input)=>new Promise((done,reject)=>{
 const child=spawn(binary,args,{windowsHide:true});
 child.stdout.resume();child.stderr.resume();child.on('error',reject);
 child.on('exit',code=>code===0?done():reject(new Error('Fixture setup failed: '+code)));
 child.stdin.end(input);
});
await run(['--data',data,'--init-password-stdin'],password);
const port=await new Promise((done,reject)=>{const socket=createServer();socket.on('error',reject);socket.listen(0,'127.0.0.1',()=>{const port=socket.address().port;socket.close(()=>done(port))})});
const base='http://127.0.0.1:'+port;
const child=spawn(binary,['--data',data,'--listen','127.0.0.1:'+port,'--managed'],{windowsHide:true});
child.stdout.resume();child.stderr.resume();
const exited=new Promise(done=>child.on('exit',done));
try{
 let ready=false;
 for(let attempt=0;attempt<100;attempt++){
  try{const health=await fetch(base+'/healthz');if(health.ok){ready=true;break}}catch{}
  await new Promise(done=>setTimeout(done,100));
 }
 assert.ok(ready,'Isolated server became ready');
 assert.equal((await fetch(base+'/api/workbench')).status,401);
 const login=await fetch(base+'/api/login',{method:'POST',headers:{'Content-Type':'application/json','Origin':base},body:JSON.stringify({password})});
 assert.equal(login.status,200);
 const {csrf}=await login.json(),cookie=login.headers.get('set-cookie').split(';')[0];
 const catalog=await fetch(base+'/api/workbench',{headers:{Cookie:cookie}}).then(r=>r.json());
 assert.equal(catalog.modes.find(m=>m.id==='work').approval,'request');
 assert.equal(catalog.modes.find(m=>m.id==='codex:auto').approval,'auto');
 assert.equal(catalog.modes.find(m=>m.id==='plan').permission,'read');
 assert.equal(catalog.modes.find(m=>m.id==='harness:read').allow_network,true);
 const headers={Cookie:cookie,'X-CSRF-Token':csrf,'Content-Type':'application/json','Origin':base};
 const automatic=await fetch(base+'/api/library/automatic',{headers}).then(r=>r.json());
 assert.deepEqual(automatic,{capture:true,recall:true,organize:true},'automatic recording, organization and recall work without setup');
 assert.equal((await fetch(base+'/api/library/automatic',{method:'PUT',headers,body:JSON.stringify({capture:false,recall:false})})).status,200);
 assert.deepEqual(await fetch(base+'/api/library/automatic',{headers}).then(r=>r.json()),{capture:false,recall:false,organize:true},'older clients preserve the organization preference');
 assert.equal((await fetch(base+'/api/library/automatic',{method:'PUT',headers,body:JSON.stringify({capture:true,recall:true,organize:false})})).status,200);
 assert.deepEqual(await fetch(base+'/api/library/automatic',{headers}).then(r=>r.json()),{capture:true,recall:true,organize:false},'organization can be disabled independently');
 assert.equal((await fetch(base+'/api/library/automatic',{method:'PUT',headers,body:JSON.stringify({capture:false,recall:false})})).status,200);
 assert.deepEqual(await fetch(base+'/api/library/automatic',{headers}).then(r=>r.json()),{capture:false,recall:false,organize:false},'older clients also preserve an explicit organization opt-out');
 const vault=await fetch(base+'/api/library/vault',{headers}).then(r=>r.json());
 assert.equal(vault.document_directory,join(data,'knowledge','Duo'),'new installations use their own data directory for portable documents');
 assert.deepEqual(vault.config,{enabled:true,directory:join(data,'knowledge'),include_runs:true,include_automatic:true,task_folders:true},'new installations record task documents without manual configuration');
 const engines=await fetch(base+'/api/engines',{headers}).then(r=>r.json());
 assert.equal(engines.engines.find(e=>e.id==='deepseek-harness').transport,'acp');
 const createHarness=mode_id=>fetch(base+'/api/tasks',{method:'POST',headers,body:JSON.stringify({title:'Harness isolated workflow',workspace:'/tmp',engine:'deepseek-harness',model:'deepseek-flash',mode_id})});
 assert.equal((await createHarness('plan')).status,400,'Harness must not promise network isolation');
 const created=await createHarness('harness:read');assert.equal(created.status,201);
 const {task}=await created.json();assert.equal(task.engine,'deepseek-harness');
 const recent=await fetch(base+'/api/tasks/'+task.id+'?recent=1',{headers}).then(r=>r.json());
 assert.deepEqual(recent.runs,[]);assert.deepEqual(recent.events,[]);
 assert.deepEqual(recent.conversation,{before:'',has_older:false,sequence:0,has_more:false,records:{}},'empty recent conversation has a valid snapshot cursor');
 assert.equal((await fetch(base+'/api/tasks/'+task.id+'?recent=1&after=invalid',{headers})).status,400,'invalid history cursors are rejected');
 assert.deepEqual(await fetch(base+'/api/tasks/'+task.id+'/knowledge?summary=1',{headers}).then(r=>r.json()),[],'knowledge metadata can load independently');
 assert.equal((await fetch(base+'/api/tasks/'+task.id,{method:'PATCH',headers,body:JSON.stringify({model:'other-model'})})).status,200,'idle Harness task can change model');
 const noCSRF=await fetch(base+'/api/tasks/test/approvals/test',{method:'POST',headers:{Cookie:cookie,'Content-Type':'application/json','Origin':base},body:'{"decision":"accept"}'});
 assert.equal(noCSRF.status,403);
 const expired=await fetch(base+'/api/tasks/test/approvals/test',{method:'POST',headers:{Cookie:cookie,'X-CSRF-Token':csrf,'Content-Type':'application/json','Origin':base},body:'{"decision":"accept"}'});
 assert.equal(expired.status,409);
 const html=await fetch(base+'/').then(r=>r.text());
 const asset=html.match(/<script type="module" src="([^"]+)"/)[1];
 const source=await fetch(base+asset).then(r=>r.text());
 assert.ok(source.includes('renderCodexApprovals'),'built assets include approvals');
 assert.ok(source.includes('setting-harness-provider'),'served entrypoint includes Harness configuration');
 assert.ok(source.includes('automatic-capture')&&source.includes('vault-automatic'),'built assets include automatic accumulation and separate Vault export settings');
 assert.ok(source.includes('settings-knowledge')&&source.includes('vault-document-directory')&&source.includes('library-manage-task'),'built assets include central knowledge settings and task management navigation');
 assert.ok(source.includes('automatic-organize')&&source.includes('library-layer')&&source.includes('composer-resizer'),'built assets include document layers, automatic organization and composer resizing');
 assert.ok(source.includes('conversation-older')&&!source.includes('id="conversation-all"'),'built conversation includes lazy history without the trajectory preset');
 console.log('PASS: isolated executable starts; login/CSRF, native modes, stale approvals, automatic knowledge defaults and opt-outs, portable directory metadata and embedded document/resizing UI verified. No model turn sent.');
}finally{
 child.stdin.end();
 const timer=setTimeout(()=>child.kill(),5000);
 await exited;clearTimeout(timer);
}
