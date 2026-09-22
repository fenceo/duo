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
 const noCSRF=await fetch(base+'/api/tasks/test/approvals/test',{method:'POST',headers:{Cookie:cookie,'Content-Type':'application/json','Origin':base},body:'{"decision":"accept"}'});
 assert.equal(noCSRF.status,403);
 const expired=await fetch(base+'/api/tasks/test/approvals/test',{method:'POST',headers:{Cookie:cookie,'X-CSRF-Token':csrf,'Content-Type':'application/json','Origin':base},body:'{"decision":"accept"}'});
 assert.equal(expired.status,409);
 const source=await fetch(base+'/app.js').then(r=>r.text());
 assert.ok(source.includes('renderCodexApprovals'),'built assets include approvals');
 console.log('PASS: isolated executable starts; login/CSRF, native mode catalog, stale approval rejection and embedded approval UI verified. No model turn sent.');
}finally{
 child.stdin.end();
 const timer=setTimeout(()=>child.kill(),5000);
 await exited;clearTimeout(timer);
}
