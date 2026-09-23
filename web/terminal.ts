// xterm.js is vendored under its MIT license; this is Duo's session UI.
type TerminalView={cols:number;rows:number;element?:HTMLElement;options:{disableStdin:boolean};open(el:HTMLElement):void;write(data:string|Uint8Array,done?:()=>void):void;resize(cols:number,rows:number):void;focus():void;dispose():void;getSelection():string;onData(fn:(data:string)=>void):void;onBinary(fn:(data:string)=>void):void;attachCustomKeyEventHandler(fn:(event:KeyboardEvent)=>boolean):void};
declare const Terminal:{new(options:Record<string,unknown>):TerminalView};
type TaskTerminal={id:string;task:string;environmentID:string;environment:string;workspace:string;term:TerminalView;host:HTMLElement;socket:WebSocket|null;state:string;live:boolean;starting:boolean;resize:ResizeObserver;timer:ReturnType<typeof setTimeout>|null;pendingBytes:number;sentCols:number;sentRows:number};
const taskTerminals=new Map<string,TaskTerminal>();
const selectedTerminals=new Map<string,string>();
let terminalDialogTask='',terminalSequence=0;
function currentTaskTerminal(){return taskTerminals.get(selectedTerminals.get(chosen)||'')}
function terminalLabel(t:TaskTerminal){return t.environment+' · '+t.workspace}
function chooseTerminalEnvironment(){const id=input('terminal-environment').value,e=settings.config.environments.find(e=>e.id===id);input('terminal-workspace').value=id?(e?.workspaces[0]||''):(detail?.task.workspace||'');element('terminal-workspaces').innerHTML=(id?e?.workspaces||[]:[detail?.task.workspace||'']).map(p=>`<option value="${escapeHTML(p)}"></option>`).join('')}
function newTerminalDialog(){if(!detail||detail.task.archived)return;terminalDialogTask=chosen;input('terminal-environment').innerHTML='<option value="">当前任务环境</option>'+settings.config.environments.map(e=>`<option value="${escapeHTML(e.id)}">${escapeHTML(e.name)}</option>`).join('');chooseTerminalEnvironment();element<HTMLDialogElement>('terminal-dialog').showModal()}

function installTerminal(){
 element('task-model').insertAdjacentHTML('beforebegin','<button id="terminal-tab">终端</button>');
 element('tool-body').insertAdjacentHTML('beforeend',`<section id="terminal-panel" class="hidden"><div class="terminal-toolbar"><select id="terminal-picker" aria-label="任务终端"></select><button id="terminal-new" title="新建终端，可另选环境">＋</button><span id="terminal-state" role="status">未打开</span><button id="terminal-open" class="primary">打开终端</button><button id="terminal-end" class="hidden">结束</button><button id="terminal-use" title="将选中的终端文本放入任务输入框">选中内容带入对话</button></div><p id="terminal-context" class="muted"></p><div id="terminal-stage"><div id="terminal-empty">在当前任务的工作目录打开终端。</div></div><div class="terminal-keys"><button data-terminal-key="ctrl-c">Ctrl+C</button><button data-terminal-key="tab">Tab</button><button data-terminal-key="esc">Esc</button><button data-terminal-key="up" aria-label="历史上一条">↑</button><button data-terminal-key="down" aria-label="历史下一条">↓</button><button data-terminal-key="enter">Enter</button><small>刷新或关闭页面会结束终端</small></div></section>`);
 element('root').insertAdjacentHTML('beforeend',`<dialog id="terminal-dialog"><form id="terminal-form"><h2>新建终端</h2><label for="terminal-environment">执行环境</label><select id="terminal-environment"></select><label for="terminal-workspace">工作目录</label><input id="terminal-workspace" list="terminal-workspaces" required><datalist id="terminal-workspaces"></datalist><p class="muted">只设置这个终端，任务的 AI 环境保持不变。</p><div class="dialog-footer"><button type="button" id="terminal-cancel">取消</button><button class="primary">打开终端</button></div></form></dialog>`);
 button('terminal-new').onclick=newTerminalDialog;input('terminal-picker').onchange=()=>{selectedTerminals.set(chosen,input('terminal-picker').value);renderTerminal()};
 input('terminal-environment').onchange=chooseTerminalEnvironment;button('terminal-cancel').onclick=()=>element<HTMLDialogElement>('terminal-dialog').close();
 element('terminal-form').onsubmit=e=>{e.preventDefault();if(terminalDialogTask!==chosen){notify('任务已切换，请重新打开终端设置');return}element<HTMLDialogElement>('terminal-dialog').close();void openTaskTerminal(true,input('terminal-environment').value,input('terminal-workspace').value)};
 button('terminal-tab').onclick=()=>switchTab('terminal');
 button('terminal-open').onclick=()=>void openTaskTerminal();
 button('terminal-end').onclick=()=>{const t=currentTaskTerminal();if(t){t.live=false;t.starting=false;t.state='已结束';t.term.options.disableStdin=true;t.socket?.close();renderTerminal()}};
 button('terminal-use').onclick=()=>{const text=currentTaskTerminal()?.term.getSelection();if(!text){notify('先在终端里选中要分析的文字');return}if(text.length>24000){notify('选中内容太长，请缩小到 24000 字以内');return}bringToChat('请分析以下终端输出：\n\n'+text)};
 element('terminal-panel').querySelectorAll<HTMLButtonElement>('[data-terminal-key]').forEach(b=>b.onclick=()=>{const t=currentTaskTerminal();if(!t)return;terminalInput(t,({'ctrl-c':'\x03',tab:'\t',esc:'\x1b',up:'\x1b[A',down:'\x1b[B',enter:'\r'} as Record<string,string>)[b.dataset.terminalKey!]);t.term.focus()});
}
function renderTerminal(){
 if(!element('terminal-panel'))return;
 const t=currentTaskTerminal();
 for(const entry of taskTerminals.values())entry.host.classList.toggle('hidden',entry!==t);
 const list=[...taskTerminals.values()].filter(v=>v.task===chosen);
 input('terminal-picker').innerHTML=list.map((v,i)=>`<option value="${v.id}">${i+1} · ${escapeHTML(v.environment)}${v.live?'':' · '+escapeHTML(v.state)}</option>`).join('')||'<option>暂无终端</option>';input('terminal-picker').value=t?.id||'';
 button('terminal-new').disabled=!detail||!!detail.task.archived;
 element('terminal-empty').classList.toggle('hidden',!!t);
 element('terminal-state').textContent=t?.state||'未打开';
 element('terminal-context').textContent=t?terminalLabel(t):detail?(detail.task.environment?.name||'')+' · '+detail.task.workspace:'';
 button('terminal-open').classList.toggle('hidden',!!t&&(t.live||t.starting));button('terminal-open').textContent=t?'重新打开':'打开终端';button('terminal-open').disabled=!detail||detail.task.archived;
 button('terminal-end').classList.toggle('hidden',!t||(!t.live&&!t.starting));button('terminal-use').disabled=!t;
 element('terminal-panel').querySelectorAll<HTMLButtonElement>('[data-terminal-key]').forEach(b=>b.disabled=!t?.live);
 const count=[...taskTerminals.values()].filter(x=>x.live||x.starting).length;button('terminal-tab').textContent=count?'终端 · '+count:'终端';
 if(t&&toolsTab==='terminal')requestAnimationFrame(()=>fitTaskTerminal(t));
}
function fitTaskTerminal(t:TaskTerminal){
 if(t.host.classList.contains('hidden')||!t.host.clientWidth||!t.host.clientHeight)return;
 const screen=t.term.element?.querySelector<HTMLElement>('.xterm-screen'),rect=screen?.getBoundingClientRect();
 if(!rect?.width||!rect.height)return;
 const viewport=t.term.element?.querySelector<HTMLElement>('.xterm-viewport');
 const cols=Math.max(2,Math.min(500,Math.floor((viewport?.clientWidth||t.host.clientWidth-30)/(rect.width/t.term.cols)))),rows=Math.max(2,Math.min(200,Math.floor((t.host.clientHeight-12)/(rect.height/t.term.rows))));
 if(cols!==t.term.cols||rows!==t.term.rows)t.term.resize(cols,rows);
 if(t.live&&t.socket?.readyState===WebSocket.OPEN&&(t.sentCols!==cols||t.sentRows!==rows)){t.socket.send(JSON.stringify({type:'resize',cols,rows}));t.sentCols=cols;t.sentRows=rows}
}
function terminalInput(t:TaskTerminal,data:string|Uint8Array){
 if(!t.live||t.socket?.readyState!==WebSocket.OPEN)return;
 const bytes=typeof data==='string'?new TextEncoder().encode(data):data;
 if(bytes.length>65536||t.socket.bufferedAmount>131072){notify('终端输入过长或网络繁忙，请分段发送');return}
 t.socket.send(new Uint8Array(bytes));
}
function disposeTaskTerminal(t:TaskTerminal){
 t.live=false;t.starting=false;t.resize.disconnect();if(t.timer)clearTimeout(t.timer);t.socket?.close();t.term.dispose();t.host.remove();taskTerminals.delete(t.id);
}
function closeTaskTerminals(){for(const t of taskTerminals.values())disposeTaskTerminal(t)}
async function openTaskTerminal(fresh=false,environmentID='',workspace=''){
 const task=chosen,id='terminal-'+(++terminalSequence);if(!detail||detail.task.id!==task||detail.task.archived)return;
 const previous=currentTaskTerminal();if(!fresh){if(previous?.live||previous?.starting)return;if(previous){environmentID=previous.environmentID;workspace=previous.workspace;disposeTaskTerminal(previous)}}
 const environment=environmentID?settings.config.environments.find(e=>e.id===environmentID):detail.task.environment;
 if(!environment){notify('执行环境不存在');return}workspace=workspace||(environmentID?environment.workspaces[0]:detail.task.workspace);
 // Bound retained scrollback even when visiting many finished terminals.
 if(taskTerminals.size>=8){const old=[...taskTerminals.values()].find(t=>!t.live&&!t.starting);if(old)disposeTaskTerminal(old)}
 const host=document.createElement('div');host.className='task-terminal';host.setAttribute('aria-label','任务交互终端');element('terminal-stage').append(host);
 const term=new Terminal({cols:100,rows:30,fontFamily:'Consolas, "Microsoft YaHei", monospace',fontSize:13,lineHeight:1.2,scrollback:3000,cursorBlink:true,screenReaderMode:true,disableStdin:true,theme:{background:'#101318',foreground:'#e0e7f1',cursor:'#d8f383',selectionBackground:'#46533a'}});
 const t:TaskTerminal={id,task,environmentID,environment:environment.name,workspace,term,host,socket:null,state:'正在连接…',live:false,starting:true,resize:new ResizeObserver(()=>{if(t.timer)clearTimeout(t.timer);t.timer=setTimeout(()=>fitTaskTerminal(t),100)}),timer:null,pendingBytes:0,sentCols:0,sentRows:0};
 taskTerminals.set(id,t);selectedTerminals.set(task,id);term.open(host);t.resize.observe(host);
 term.onData(data=>terminalInput(t,data));term.onBinary(data=>terminalInput(t,Uint8Array.from(data,c=>c.charCodeAt(0))));
 term.attachCustomKeyEventHandler(e=>!(e.type==='keydown'&&e.ctrlKey&&e.key.toLowerCase()==='c'&&term.getSelection()));
 renderTerminal();
 try{
  const ticket=await api<{ticket:string}>(`tasks/${task}/terminal`,'POST',{environment_id:environmentID,workspace});
  if(!t.starting||taskTerminals.get(id)!==t)return;
  const socket=new WebSocket((location.protocol==='https:'?'wss://':'ws://')+location.host+'/api/terminal/'+encodeURIComponent(ticket.ticket));t.socket=socket;socket.binaryType='arraybuffer';
  socket.onmessage=e=>{
   if(taskTerminals.get(id)!==t)return;
   if(e.data instanceof ArrayBuffer){const data=new Uint8Array(e.data);t.pendingBytes+=data.length;if(t.pendingBytes>2*1024*1024){t.state='输出过快，连接已结束';t.live=false;t.starting=false;socket.close();renderTerminal();return}term.write(data,()=>t.pendingBytes-=data.length);return}
   try{const msg=JSON.parse(e.data);if(msg.type==='ready'&&t.starting){t.starting=false;t.live=true;t.state='已连接';term.options.disableStdin=false;fitTaskTerminal(t);if(chosen===task&&currentTaskTerminal()===t&&toolsTab==='terminal')term.focus()}else if(msg.type==='error'||msg.type==='exit'){t.starting=false;t.live=false;t.state=msg.type==='error'?msg.message:`已退出 · ${msg.code}`;term.options.disableStdin=true}renderTerminal()}catch{t.state='终端响应异常';socket.close()}
  };
  socket.onclose=()=>{if(taskTerminals.get(id)!==t)return;if(t.live||t.starting)t.state='连接已断开，可重新打开';t.live=false;t.starting=false;term.options.disableStdin=true;renderTerminal()};
  socket.onerror=()=>{if(taskTerminals.get(id)===t){t.state='连接失败，请重新打开';renderTerminal()}};
 }catch(e){if(taskTerminals.get(id)===t){t.starting=false;t.state=(e as Error).message;renderTerminal()}}
}
window.addEventListener('beforeunload',e=>{if([...taskTerminals.values()].some(t=>t.live||t.starting)){e.preventDefault();e.returnValue=''}});
window.addEventListener('pagehide',closeTaskTerminals);
