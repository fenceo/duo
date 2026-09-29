import {spawn} from 'node:child_process';
import {readFile,mkdtemp} from 'node:fs/promises';
import path from 'node:path';

// Dedicated browser and profile for local synthetic files. Exact CSS viewport
// emulation avoids the minimum native window width on Windows headless Chrome.
export async function startFixtureBrowser(executable,output){
 const profile=await mkdtemp(path.join(output,'profile-'));
 const child=spawn(executable,['--headless','--disable-gpu','--no-first-run','--no-default-browser-check','--disable-extensions','--disable-background-networking','--remote-debugging-address=127.0.0.1','--remote-debugging-port=0','--user-data-dir='+profile,'about:blank'],{windowsHide:true,stdio:['ignore','ignore','pipe']});
 let error='',stderr='';child.on('error',e=>{error=e.message});child.stderr.setEncoding('utf8');child.stderr.on('data',text=>{stderr=(stderr+text).slice(-1500)});
 const pause=()=>new Promise(resolve=>setTimeout(resolve,100));
 let socket;
 try{
  let port;
  for(let i=0;i<150;i++){
   if(error||child.exitCode!==null)throw Error('Browser could not start: '+(error||stderr));
   try{port=(await readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split(/\r?\n/)[0];break}catch{await pause()}
  }
  if(!/^\d+$/.test(port||''))throw Error('Browser debugging endpoint was not created.');
  const version=await(await fetch('http://127.0.0.1:'+port+'/json/version',{signal:AbortSignal.timeout(5000)})).json();
  const url=new URL(version.webSocketDebuggerUrl);if(url.hostname!=='127.0.0.1'||url.protocol!=='ws:'||url.port!==port)throw Error('Unexpected browser endpoint.');
  socket=new WebSocket(url);await new Promise((resolve,reject)=>{socket.addEventListener('open',resolve,{once:true});socket.addEventListener('error',reject,{once:true})});
  const pending=new Map();let sequence=0;
  socket.addEventListener('message',event=>{const data=JSON.parse(event.data);const request=pending.get(data.id);if(request){pending.delete(data.id);clearTimeout(request.timer);data.error?request.reject(Error(data.error.message)):request.resolve(data.result)}});
  socket.addEventListener('close',()=>{for(const request of pending.values()){clearTimeout(request.timer);request.reject(Error('Fixture browser closed'))}pending.clear()});
  const send=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const id=++sequence;const timer=setTimeout(()=>{pending.delete(id);reject(Error('Browser command timed out: '+method))},15000);pending.set(id,{resolve,reject,timer});socket.send(JSON.stringify({id,method,params,...(sessionId?{sessionId}:{})}))});
  const {targetId}=await send('Target.createTarget',{url:'about:blank'});
  const {sessionId}=await send('Target.attachToTarget',{targetId,flatten:true});
  const page=(method,params)=>send(method,params,sessionId);
  await page('Page.enable');
  return {
   async capture(url,width,height){
    await page('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false});
    await page('Page.navigate',{url});
    let ready=false;
    for(let i=0;i<100;i++){const result=await page('Runtime.evaluate',{expression:`location.href===${JSON.stringify(url)}&&document.readyState==='complete'&&!!document.getElementById('layout-metrics')`,returnByValue:true});if(result.result.value){ready=true;break}await pause()}
    if(!ready)throw Error('Synthetic page did not finish loading.');
    await page('Runtime.evaluate',{expression:'document.fonts.ready.then(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))',awaitPromise:true});
    const result=await page('Runtime.evaluate',{expression:"document.getElementById('layout-metrics').textContent",returnByValue:true});
    const screenshot=await page('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
    return {metrics:JSON.parse(result.result.value),png:Buffer.from(screenshot.data,'base64')};
   },
   async close(){try{await send('Browser.close')}catch{}finally{socket.close();if(child.exitCode===null)child.kill()}},
  };
 }catch(error){socket?.close();if(child.exitCode===null)child.kill();throw error}
}
