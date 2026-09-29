// Local browser benchmark with synthetic data only. Start, open the printed
// loopback URLs, and click Run. Stop this process after inspection.
import {conversationStressDetail} from './Conversation-Performance-Fixture.mjs';
import {readFile} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
import {createServer} from 'node:http';

const before=spawnSync('git',['show','v0.22.9:web/app.optimized.js'],{encoding:'utf8',windowsHide:true});
if(before.status!==0)throw Error('Baseline v0.22.9 is required');
const after=await readFile(new URL('../web/app.optimized.js',import.meta.url),'utf8');
const styles=await Promise.all(['style.css','workbench.css'].map(name=>readFile(new URL('../web/'+name,import.meta.url),'utf8')));
const fixture=JSON.stringify(conversationStressDetail());
const environment={id:'fixture',name:'本机测试',type:'windows',workspaces:['C:/fixture']};
const responses=JSON.stringify({'/api/auth':{authenticated:true,csrf:'synthetic'},'/api/tasks':[],'/api/settings':{config:{environments:[environment],default_environment:'fixture',feishu:{enabled:false},access:{}},data_dir:'C:/fixture',secret_configured:false},'/api/workbench':{modes:[{id:'work',name:'Work',permission:'workspace',approval:'request',allow_network:true,prompt:''}],commands:[]},'/api/scratch':[],'/api/library/automatic':{capture:true,recall:true},'/api/library/vault':{config:{enabled:false},report:{conflicts:[],warnings:[]}}});
function page(version){return `<!doctype html><meta charset="utf-8"><title>Duo ${version} 输入性能测试</title><style>${styles.join('\n')}#benchmark{position:fixed;z-index:99999;top:0;right:0;padding:10px;background:white;color:black;border:1px solid gray;max-width:650px}#measurements{white-space:pre-wrap;font:12px monospace;max-height:45vh;overflow:auto}</style><div id="root"></div><div id="notice"></div><aside id="benchmark"><button id="run-benchmark">运行 4800 条记录输入测试 · ${version}</button><pre id="measurements">合成任务 · 不连接 Duo 服务或模型</pre></aside><script>
const fixture=${fixture},responses=${responses};
// Keep this isolated preview URL reloadable when the app switches task views.
history.replaceState=()=>{};
window.fetch=async path=>{let data=responses[path];if(String(path).startsWith('/api/tasks/long-chat-fixture?after='))data={...fixture,events:[]};if(path==='/api/tasks/long-chat-fixture/knowledge')data=[];if(data===undefined)throw Error('Unexpected synthetic request: '+path);return {ok:true,status:200,json:async()=>structuredClone(data)}};
</script><script src="/${version}.js"></script><script>
document.getElementById('run-benchmark').onclick=async()=>{
 const report=document.getElementById('measurements');document.getElementById('run-benchmark').disabled=true;authenticated=false;
 try{
 chosen=fixture.task.id;detail=structuredClone(fixture);tasks=[detail.task];sequence=0;resetConversation();element('conversation').replaceChildren();for(const id of ['tabs','task-actions','conversation-filter'])element(id).classList.remove('hidden');
 const begin=performance.now();appendEvents(detail.events);renderTask();switchTab('chat');document.body.getBoundingClientRect();const mountMs=performance.now()-begin;
 const nodes=element('conversation').querySelectorAll('*').length;await new Promise(requestAnimationFrame);
 const idle=[];for(let i=0;i<20;i++){const time=performance.now();appendEvents([]);renderTask();document.body.getBoundingClientRect();idle.push(performance.now()-time)}
 const inputTimes=[],nextFrame=[];const field=element('message');field.focus();
 for(let i=0;i<40;i++){await new Promise(requestAnimationFrame);const time=performance.now();field.value+='测';field.dispatchEvent(new InputEvent('input',{bubbles:true,data:'测',inputType:'insertText'}));field.getBoundingClientRect();inputTimes.push(performance.now()-time);await new Promise(requestAnimationFrame);nextFrame.push(performance.now()-time)}
 const stat=values=>{const sorted=values.slice().sort((a,b)=>a-b);return {mean:+(values.reduce((a,b)=>a+b,0)/values.length).toFixed(2),p95:+sorted[Math.floor(sorted.length*.95)].toFixed(2),max:+sorted.at(-1).toFixed(2)}};
 report.textContent=JSON.stringify({version:'${version}',events:fixture.events.length,chars:fixture.events.reduce((n,e)=>n+e.text.length,0),nodes,mountMs:+mountMs.toFixed(2),idlePollMs:stat(idle),inputHandlerAndLayoutMs:stat(inputTimes),inputToNextFrameMs:stat(nextFrame),draftPreserved:drafts.get(chosen)===field.value},null,2);
 }catch(error){report.textContent=String(error.stack||error)}finally{document.getElementById('run-benchmark').disabled=false}
};</script>`}
const server=createServer((request,response)=>{
 const pathname=new URL(request.url,'http://127.0.0.1').pathname;
 if(pathname==='/before.js'||pathname==='/after.js'){response.setHeader('Content-Type','text/javascript; charset=utf-8');response.end(pathname==='/before.js'?before.stdout:after)}
 else if(pathname==='/before'||pathname==='/after'){response.setHeader('Content-Type','text/html; charset=utf-8');response.end(page(pathname.slice(1)))}
 else {response.statusCode=404;response.end('Not found')}
});
server.listen(0,'127.0.0.1',()=>{const base='http://127.0.0.1:'+server.address().port;console.log(base+'/before\n'+base+'/after')});
