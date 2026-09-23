type SerialPortInfo={name:string;product:string;vid:string;pid:string;busy:boolean};
type HardwareTerminal=TerminalView&{reset():void;clear():void;scrollToBottom():void;parser:{registerCsiHandler(id:{prefix?:string;intermediates?:string;final:string},fn:()=>boolean):unknown;registerOscHandler(id:number,fn:()=>boolean):unknown;registerDcsHandler(id:{intermediates?:string;final:string},fn:()=>boolean):unknown}};
type HardwareView={task:string;id:string;term:HardwareTerminal;resize:ResizeObserver;alive:boolean;connected:boolean;generation:string;ready:boolean;controller:string;controllerTitle:string;relay?:{known:boolean;power:boolean;coil:boolean;updated:number;busy:boolean}|null;requesting:boolean;busy:boolean;pending:Uint8Array[];pendingBytes:number;flushing:boolean;flushTimer?:ReturnType<typeof setTimeout>;written:number;screenBytes:number;paused:boolean};
let hardwareView:HardwareView|null=null,hardwarePortsRequest=0,hardwareEditingTask='',hardwareLoadRequest=0;
let hardwarePollTimer:ReturnType<typeof setInterval>|undefined,hardwareSaving=false;
const hardwareDrafts=new Map<string,string>();
type HardwareGrant={read:boolean;write:boolean;power:boolean};
let hardwareAIGrants:Record<string,HardwareGrant>={},hardwareAIEditing:{task:string;device:DeviceConfig}|null=null;
type HardwareTaskUse={id:string;title:string;archived:boolean;ai:HardwareGrant;active_ai?:HardwareGrant};
type HardwareOverview={config:DeviceConfig;connected:boolean;controller_task:string;controller_title:string;tasks:HardwareTaskUse[]};
let hardwareOverview:HardwareOverview[]=[],hardwareOverviewTask='',hardwareOverviewLoading=false,hardwareAISaving=false,hardwareLibraryAttaching=false;
let hardwareOverviewTimer:ReturnType<typeof setInterval>|undefined;
function hexBytes(hex:string){return Uint8Array.from(hex.match(/.{1,2}/g)||[],pair=>parseInt(pair,16))}
function bytesHex(bytes:Uint8Array){return Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('')}
function hardwarePlainText(events:DeviceEvent[]){
 const decode=new TextDecoder();let text='';
 for(const e of events){if(e.direction==='rx')text+=decode.decode(hexBytes(e.hex),{stream:true});else if(e.direction==='status')text+=decode.decode()+'\n['+e.text+']\n'}
 return (text+decode.decode()).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').replace(/\x1b\][\s\S]*?(?:\x07|\x1b\\)/g,'');
}
function installHardware(){
 element('hardware-panel').innerHTML=`<div class="hardware-connect"><select id="device-picker" aria-label="硬件连接"></select><button id="device-connect" class="primary">连接</button><button id="device-disconnect" class="hidden">断开</button><button id="device-edit">配置</button><button id="device-library">设备库</button><button id="device-new" title="添加串口或网络连接">＋</button></div><div class="hardware-ownership"><span id="device-owner"></span><button id="device-claim" class="hidden">取得控制</button><button id="device-release" class="hidden">释放控制</button></div><section id="relay-panel" class="hidden"><strong id="relay-status">尚未查询</strong><div class="actions"><button data-relay="query">查询</button><button data-relay="on">上电</button><button data-relay="off">断电</button><button data-relay="cycle">断电重启</button></div><small>显示继电器触点反馈，不代表板子已启动。</small></section><div class="hardware-meta"><span id="device-status" role="status">未连接</span><span id="device-description"></span></div><div class="hardware-viewbar"><select id="device-view" aria-label="显示方式"><option value="terminal">交互终端</option><option value="text">收发日志</option><option value="hex">HEX 日志</option></select><button id="device-clear">清屏</button><button id="device-pause">暂停显示</button><button id="device-export">导出</button><button id="device-analyze">选中分析</button><label id="hardware-task-filter" class="check-row"><input type="checkbox" id="hardware-task-log">本任务日志</label></div><div id="hardware-terminal" role="group" aria-label="硬件交互终端"></div><pre id="device-console" class="hidden" tabindex="0" aria-label="硬件收发日志"></pre><div class="hardware-keys"><button data-hardware-key="enter">Enter</button><button data-hardware-key="tab">Tab</button><button data-hardware-key="up" aria-label="串口历史上一条">↑</button><button data-hardware-key="down" aria-label="串口历史下一条">↓</button><button data-hardware-key="ctrl-c">Ctrl+C</button><button data-hardware-key="esc">Esc</button><button id="device-bottom">到底部</button></div><p id="hardware-hint" class="muted">连接后点击终端，直接输入命令。</p><details id="hardware-send-details"><summary>文本 / HEX 发送与终端选项</summary><form id="device-send"><textarea id="device-data" rows="2" aria-label="发送内容" placeholder="输入文本或十六进制字节"></textarea><div class="device-toolbar"><select id="device-encoding" aria-label="发送编码"><option value="text">文本</option><option value="hex">HEX</option></select><select id="device-newline" aria-label="行尾"><option value="cr">CR</option><option value="lf">LF</option><option value="crlf">CRLF</option><option value="none">无</option></select><button type="button" id="device-interrupt">Ctrl+C</button><button id="device-send-button" class="primary">发送</button></div></form><div class="hardware-options"><label>终端回车<select id="hardware-enter"><option value="cr">CR</option><option value="lf">LF</option><option value="crlf">CRLF</option></select></label><label>退格<select id="hardware-backspace"><option value="del">DEL</option><option value="bs">BS</option></select></label><label class="check-row"><input type="checkbox" id="hardware-echo">本地回显</label></div><p class="muted">关闭面板不会断开设备。清屏只清当前显示，后台保留最近 2000 条收发记录。</p></details>`;
 element('root').insertAdjacentHTML('beforeend',`<dialog id="hardware-library-dialog"><h2>设备库</h2><p class="muted">查看每台设备关联的任务、AI 权限和当前控制权。</p><div id="hardware-library-list"></div><div class="dialog-footer"><button id="hardware-library-close">关闭</button></div></dialog>`);
 element('device-form').querySelector('label[for="device-name"]')!.insertAdjacentHTML('beforebegin','<label for="device-kind">设备用途</label><select id="device-kind"><option value="console">串口 / 网络控制台</option><option value="relay">串口继电器 · 板子电源</option></select>');
 element('device-readonly').parentElement!.insertAdjacentHTML('beforebegin','<div id="relay-fields" class="hidden"><label for="relay-contact">实际接线</label><select id="relay-contact"><option value="">请选择接线方式…</option><option value="no">COM + NO（常开）</option><option value="nc">COM + NC（常闭）</option></select><div class="form-grid"><label>继电器地址<input id="relay-channel" type="number" min="1" max="254" value="1"></label><label>断电间隔（秒）<input id="relay-delay" type="number" min="1" max="60" value="3"></label></div><p class="muted">CH340 / A0 协议，9600 · 8N1。COM 口变更后在此修改，设备名称和任务关联会保留。</p></div>');
 input('device-kind').onchange=()=>{if(input('device-kind').value==='relay'){input('device-baud').value='9600';input('hardware-bits').value='8';input('hardware-parity').value='none';input('hardware-stop').value='1';if(!input('device-name').value)input('device-name').value='板子电源'}deviceFields()};
 button('device-library').onclick=()=>void openHardwareLibrary();button('hardware-library-close').onclick=()=>element<HTMLDialogElement>('hardware-library-dialog').close();
 element('device-library').insertAdjacentHTML('afterend','<button id="device-ai">AI 使用</button>');
 element('hardware-panel').insertAdjacentHTML('afterbegin','<section class="hardware-ai-overview"><div class="hardware-overview-head"><strong>本任务 AI 硬件</strong><span id="hardware-ai-count"></span><button id="hardware-overview-refresh" title="刷新设备连接和任务授权">刷新</button></div><div id="hardware-ai-list"></div><small id="hardware-ai-tip">无需提前连接。点设备设置 AI 权限。</small></section>');
 button('hardware-overview-refresh').onclick=()=>void loadDevices();
 element('hardware-ai-list').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('[data-ai-device]');if(b){selectDevice(b.dataset.aiDevice!);void editHardwareAI()}};
 element('hardware-library-list').onclick=e=>{const b=(e.target as HTMLElement).closest<HTMLButtonElement>('button');if(!b)return;if(b.dataset.attach)void attachHardwareFromLibrary(b.dataset.attach);else if(b.dataset.hardwareTask){element<HTMLDialogElement>('hardware-library-dialog').close();deviceID=b.dataset.device!;void choose(b.dataset.hardwareTask,'hardware')}};
 element('root').insertAdjacentHTML('beforeend',`<dialog id="hardware-ai-dialog"><form id="hardware-ai-form"><h2>允许本任务 AI 使用</h2><p id="hardware-ai-name"></p><label class="check-row"><input type="checkbox" id="hardware-ai-read">连接、读取日志和查询状态</label><label class="check-row" id="hardware-ai-write-row"><input type="checkbox" id="hardware-ai-write">发送串口 / 控制台命令</label><label class="check-row" id="hardware-ai-power-row"><input type="checkbox" id="hardware-ai-power">上电、断电和断电重启</label><p>适用于本任务的 Codex 和 Claude。新增权限从下一轮使用；收回权限会立即撤销本轮硬件凭据。操作记录留在任务和硬件日志中。</p><p class="error" id="hardware-ai-error"></p><div class="dialog-footer"><button type="button" id="hardware-ai-cancel">取消</button><button class="primary" id="hardware-ai-save">保存</button></div></form></dialog>`);
 button('device-ai').onclick=()=>void editHardwareAI();button('hardware-ai-cancel').onclick=()=>element<HTMLDialogElement>('hardware-ai-dialog').close();input('hardware-ai-read').onchange=renderHardwareAIForm;element('hardware-ai-form').onsubmit=e=>{e.preventDefault();void saveHardwareAI()};
 button('device-claim').onclick=()=>{const h=hardwareView;if(h?.controller&&h.controller!==chosen&&!confirm('接管“'+(h.controllerTitle||h.controller)+'”正在控制的设备？原任务会转为查看模式。'))return;cancelHardwareInput();void deviceAction('claim',{})};
 button('device-release').onclick=()=>{cancelHardwareInput();void deviceAction('release',{})};
 element('relay-panel').querySelectorAll<HTMLButtonElement>('[data-relay]').forEach(b=>b.onclick=()=>void relayOperation(b.dataset.relay!));
 input('hardware-task-log').onchange=renderDeviceLog;
 element('device-portname').insertAdjacentHTML('beforebegin','<select id="device-serial-choice" aria-label="检测到的串口"></select>');
 input('device-baud').setAttribute('list','hardware-bauds');
 input('device-baud').insertAdjacentHTML('afterend','<datalist id="hardware-bauds">'+[9600,19200,38400,57600,115200,230400,460800,921600,1500000].map(b=>`<option value="${b}"></option>`).join('')+'</datalist><details class="hardware-advanced"><summary>串口参数 · 默认 8N1</summary><div class="form-grid"><label>数据位<select id="hardware-bits"><option>8</option><option>7</option><option>6</option><option>5</option></select></label><label>校验<select id="hardware-parity"><option value="none">无</option><option value="even">偶</option><option value="odd">奇</option><option value="mark">Mark</option><option value="space">Space</option></select></label><label>停止位<select id="hardware-stop"><option>1</option><option>1.5</option><option>2</option></select></label><div><label class="check-row"><input id="hardware-dtr" type="checkbox">DTR</label><label class="check-row"><input id="hardware-rts" type="checkbox">RTS</label></div></div></details>');
 element('device-baud').previousElementSibling!.textContent='波特率';
 element('device-form').querySelector('.dialog-footer')!.insertAdjacentHTML('beforeend','<button type="button" class="primary" id="device-save-connect">保存并连接</button>');
 button('device-new').onclick=()=>editDevice(null);button('device-edit').onclick=()=>editDevice(devices.find(d=>d.id===deviceID)||null);
 input('device-picker').onchange=()=>selectDevice(input('device-picker').value);
 button('device-refresh-ports').onclick=()=>void refreshHardwarePorts();
 input('device-protocol').onchange=()=>{deviceFields();input('device-port').value=input('device-protocol').value==='ssh'?'22':'23';if(input('device-protocol').value==='serial')void refreshHardwarePorts()};
 input('device-serial-choice').onchange=()=>{const name=input('device-serial-choice').value;input('device-portname').classList.toggle('hidden',name!=='__manual__');if(name!=='__manual__'){input('device-portname').value=name;if(!input('device-name').value||input('device-name').value.startsWith('串口 · '))input('device-name').value=name?'串口 · '+name:''}};
 button('device-cancel').onclick=()=>element<HTMLDialogElement>('device-dialog').close();element('device-form').onsubmit=e=>{e.preventDefault();void saveDevice(false)};
 button('device-save-connect').onclick=()=>{if(element<HTMLFormElement>('device-form').reportValidity())void saveDevice(true)};
 button('device-delete').textContent='移出当前任务';button('device-delete').onclick=async()=>{if(!deviceEditing||!confirm('从当前任务移除此设备？设备库及历史记录会保留。'))return;try{await api(`tasks/${hardwareEditingTask}/hardware/${deviceEditing.id}`,'DELETE',{});element<HTMLDialogElement>('device-dialog').close();await loadDevices()}catch(e){element('device-error').textContent=(e as Error).message}};
 button('device-connect').onclick=()=>connectSelectedDevice();
 button('device-password-cancel').onclick=()=>{input('device-password').value='';element<HTMLDialogElement>('device-password-dialog').close()};
 element('device-password-form').onsubmit=e=>{e.preventDefault();const password=input('device-password').value;input('device-password').value='';element<HTMLDialogElement>('device-password-dialog').close();void deviceAction('connect',{password})};
 button('device-disconnect').onclick=()=>{cancelHardwareInput();void deviceAction('disconnect',{})};
 element('device-send').onsubmit=e=>{e.preventDefault();void deviceAction('send',{data:input('device-data').value,encoding:input('device-encoding').value,newline:input('device-newline').value})};
 button('device-interrupt').onclick=()=>queueHardwareInput(new Uint8Array([3]));
 input('device-data').oninput=()=>hardwareDrafts.set(chosen+'/'+deviceID,input('device-data').value);
 input('device-view').onchange=()=>{renderHardwareState();renderDeviceLog();fitHardwareTerminal()};
 button('device-clear').onclick=()=>{deviceEvents=[];hardwareView?.term.reset();renderDeviceLog()};
 button('device-pause').onclick=()=>{const h=hardwareView;if(!h)return;h.paused=!h.paused;if(h.paused)cancelHardwareInput();else renderDeviceLog();renderHardwareState()};
 button('device-bottom').onclick=()=>{hardwareView?.term.scrollToBottom();const e=element('device-console');e.scrollTop=e.scrollHeight};
 button('device-export').onclick=()=>{const text=input('device-view').value==='terminal'?hardwarePlainText(hardwareLogEvents()):deviceLog();const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([text],{type:'text/plain;charset=utf-8'}));a.download='hardware-'+deviceID+'.log';a.click();setTimeout(()=>URL.revokeObjectURL(a.href),1000)};
 button('device-analyze').onclick=()=>{const selected=input('device-view').value==='terminal'?hardwareView?.term.getSelection():window.getSelection()?.toString();const text=(selected||hardwarePlainText(hardwareLogEvents())).slice(-24000);if(!text){notify('先接收日志，或选中要分析的文字');return}bringToChat('请分析以下硬件调试输出：\n\n'+text)};
 element('hardware-panel').querySelectorAll<HTMLButtonElement>('[data-hardware-key]').forEach(b=>b.onclick=()=>{const key=b.dataset.hardwareKey!;const data=key==='enter'?hardwareEnter():({tab:'\t',up:'\x1b[A',down:'\x1b[B','ctrl-c':'\x03',esc:'\x1b'} as Record<string,string>)[key];queueHardwareInput(new TextEncoder().encode(data));hardwareView?.term.focus()});
 clearInterval(hardwarePollTimer);hardwarePollTimer=setInterval(()=>void pollDevice(),100);
 clearInterval(hardwareOverviewTimer);hardwareOverviewTimer=setInterval(()=>{if(authenticated&&chosen&&!document.hidden&&!hardwareOverviewLoading&&!hardwareAISaving&&(toolsTab==='hardware'||element<HTMLDialogElement>('hardware-library-dialog')?.open))void loadDevices(true)},4000);
 disposeWithShell(()=>{clearInterval(hardwarePollTimer);clearInterval(hardwareOverviewTimer)});
}
function hardwareEnter(){return ({cr:'\r',lf:'\n',crlf:'\r\n'} as Record<string,string>)[input('hardware-enter').value]||'\r'}
function hardwareKeyEvent(e:KeyboardEvent,selected:string,enter:string,backspace:string,send:(bytes:Uint8Array)=>void){
 if(e.isComposing||e.keyCode===229)return true;
 const interrupt=e.ctrlKey&&!e.altKey&&!e.metaKey&&e.key.toLowerCase()==='c';
 if(interrupt&&selected)return false;
 const carriage=e.key==='Enter'&&!e.altKey&&!e.ctrlKey,bs=e.key==='Backspace'&&!e.altKey&&!e.ctrlKey&&backspace==='bs';
 if(!interrupt&&!carriage&&!bs)return true;
 e.preventDefault();
 if(e.type==='keydown'){
  send(interrupt?new Uint8Array([3]):carriage?new TextEncoder().encode(enter):new Uint8Array([8]));
  // Keep xterm's hidden input lifecycle consistent with its native Enter/Ctrl+C.
  if((carriage||interrupt)&&e.target instanceof HTMLTextAreaElement)e.target.value='';
 }
 return false;
}
function deviceFields(){const relay=input('device-kind').value==='relay';element('relay-fields').classList.toggle('hidden',!relay);input('relay-contact').required=relay;const p=input('device-protocol').value;element('device-serial-fields').classList.toggle('hidden',p!=='serial');element('device-network-fields').classList.toggle('hidden',p==='serial');element('device-ssh-fields').classList.toggle('hidden',p!=='ssh')}
async function refreshHardwarePorts(){
 const request=++hardwarePortsRequest;button('device-refresh-ports').disabled=true;
 try{const ports=await api<SerialPortInfo[]>('hardware/serial-ports');if(request!==hardwarePortsRequest)return;const current=input('device-portname').value;
  input('device-serial-choice').innerHTML='<option value="">选择串口…</option>'+ports.map(p=>`<option value="${escapeHTML(p.name)}">${escapeHTML(p.name+' · '+(p.product||[p.vid,p.pid].filter(Boolean).join(':')||'串口')+(p.busy?'（Duo内已占用）':''))}</option>`).join('')+'<option value="__manual__">手动填写 / 未检测到的端口…</option>';
  input('device-serial-choice').value=ports.some(p=>p.name===current)?current:current||!ports.length?'__manual__':'';
  input('device-portname').classList.toggle('hidden',input('device-serial-choice').value!=='__manual__');
  element('device-ports').innerHTML=ports.map(p=>`<option value="${escapeHTML(p.name)}">${escapeHTML(p.product)}</option>`).join('');
 }catch(e){if(request===hardwarePortsRequest){input('device-serial-choice').innerHTML='<option value="__manual__">手动填写串口</option>';input('device-portname').classList.remove('hidden');element('device-error').textContent=(e as Error).message}}finally{if(request===hardwarePortsRequest)button('device-refresh-ports').disabled=false}
}
function editDevice(d:DeviceConfig|null){
 hardwareEditingTask=chosen;deviceEditing=d;input('device-kind').value=d?.kind||'console';input('relay-contact').value=d?.relay?.contact||'';input('relay-channel').value=String(d?.relay?.channel||1);input('relay-delay').value=String(d?.relay?.cycle_seconds||3);
 for(const [key,value] of Object.entries({name:d?.name||'',protocol:d?.protocol||'serial',portname:d?.device||'',baud:d?.baud||115200,host:d?.host||'',port:d?.port||22,user:d?.user||'root',fingerprint:d?.fingerprint||''}))input('device-'+key).value=String(value);
 input('device-readonly').checked=d?.read_only||false;input('hardware-bits').value=String(d?.data_bits||8);input('hardware-parity').value=d?.parity||'none';input('hardware-stop').value=d?.stop_bits||'1';input('hardware-dtr').checked=d?(d.dtr??true):false;input('hardware-rts').checked=d?(d.rts??true):false;
 element('device-error').textContent='';button('device-delete').classList.toggle('hidden',!d);deviceFields();element<HTMLDialogElement>('device-dialog').showModal();if(!d||d.protocol==='serial')void refreshHardwarePorts();
}
async function saveDevice(connect:boolean){
 if(hardwareSaving)return;hardwareSaving=true;
 const task=hardwareEditingTask,c={kind:input('device-kind').value,relay:input('device-kind').value==='relay'?{channel:Number(input('relay-channel').value),contact:input('relay-contact').value,cycle_seconds:Number(input('relay-delay').value)}:undefined,name:input('device-name').value||('串口 · '+input('device-portname').value),protocol:input('device-protocol').value,device:input('device-portname').value.trim(),baud:Number(input('device-baud').value),host:input('device-host').value.trim(),port:Number(input('device-port').value),user:input('device-user').value,fingerprint:input('device-fingerprint').value.trim(),read_only:input('device-readonly').checked,data_bits:Number(input('hardware-bits').value),parity:input('hardware-parity').value,stop_bits:input('hardware-stop').value,dtr:input('hardware-dtr').checked,rts:input('hardware-rts').checked};
 const saveButtons=element('device-form').querySelectorAll<HTMLButtonElement>('button');saveButtons.forEach(b=>b.disabled=true);
 try{const d=await api<DeviceConfig>(`tasks/${task}/hardware`+(deviceEditing?'/'+deviceEditing.id:''),deviceEditing?'PUT':'POST',c);element<HTMLDialogElement>('device-dialog').close();if(chosen!==task)return;deviceID=d.id;await loadDevices();if(connect)connectSelectedDevice()}catch(e){element('device-error').textContent=(e as Error).message}finally{hardwareSaving=false;saveButtons.forEach(b=>b.disabled=false)}
}
function connectSelectedDevice(){if(devices.find(d=>d.id===deviceID)?.protocol==='ssh'){input('device-password').value='';element<HTMLDialogElement>('device-password-dialog').showModal()}else void deviceAction('connect',{})}
async function loadDevices(silent=false){
 const task=chosen,request=++hardwareLoadRequest;if(!task)return;hardwareOverviewLoading=true;
 if(hardwareOverviewTask!==task){devices=[];hardwareAIGrants={};closeHardwareView();input('device-picker').disabled=true;renderHardwareState()}
 button('hardware-overview-refresh').disabled=true;
 try{const list=await api<HardwareOverview[]>('hardware/overview');if(chosen!==task||request!==hardwareLoadRequest)return;
  hardwareOverview=list;hardwareOverviewTask=task;devices=list.filter(d=>d.tasks.some(t=>t.id===task)).map(d=>d.config);hardwareAIGrants={};for(const item of list){const use=item.tasks.find(t=>t.id===task);if(use)hardwareAIGrants[item.config.id]=use.ai}
  const picker=input('device-picker'),html=devices.map(d=>`<option value="${d.id}">${escapeHTML(d.name)}</option>`).join('')||'<option value="">添加串口或网络设备</option>';
  if(picker.dataset.snapshot!==html){picker.innerHTML=html;picker.dataset.snapshot=html}picker.disabled=false;
  if(!devices.some(d=>d.id===deviceID))deviceID=devices[0]?.id||'';selectDevice(deviceID);renderHardwareOverview();if(element<HTMLDialogElement>('hardware-library-dialog').open)renderHardwareLibrary();
  element('hardware-ai-tip').textContent='无需提前连接。点设备设置 AI 权限。';
 }catch(e){if(chosen===task&&request===hardwareLoadRequest){element('hardware-ai-tip').textContent='刷新失败，当前显示可能已过期。';if(!silent)notify((e as Error).message)}}finally{if(request===hardwareLoadRequest){hardwareOverviewLoading=false;if(element('hardware-overview-refresh'))button('hardware-overview-refresh').disabled=false}}
}
function closeHardwareView(){const h=hardwareView;if(h){h.alive=false;h.resize.disconnect();clearTimeout(h.flushTimer);h.pending=[];h.term.dispose();hardwareView=null}}
function selectDevice(id:string){
 if(hardwareView?.task===chosen&&hardwareView.id===id){input('device-picker').value=id;renderHardwareState();fitHardwareTerminal();void pollDevice();return}
 closeHardwareView();if(devices.find(d=>d.id===id)?.kind==='relay')input('device-view').value='hex';deviceID=id;deviceTask=chosen;deviceSeq=0;deviceEvents=[];input('device-picker').value=id;input('device-data').value=hardwareDrafts.get(chosen+'/'+id)||'';
 element('hardware-terminal').replaceChildren();
 if(id){
  const term=new Terminal({cols:100,rows:24,fontFamily:'Consolas, "Microsoft YaHei", monospace',fontSize:13,lineHeight:1.2,scrollback:5000,cursorBlink:true,screenReaderMode:true,disableStdin:true,convertEol:true,allowProposedApi:true,theme:{background:'#101318',foreground:'#e0e7f1',cursor:'#d8f383',selectionBackground:'#46533a'}}) as HardwareTerminal;
  const h:HardwareView={task:chosen,id,term,resize:new ResizeObserver(()=>fitHardwareTerminal()),alive:true,connected:false,generation:'',ready:false,controller:'',controllerTitle:'',requesting:false,busy:false,pending:[],pendingBytes:0,flushing:false,written:0,screenBytes:0,paused:false};hardwareView=h;
  // Serial log replay is display-only: terminal queries must never emit bytes
  // back to Linux/U-Boot, including reports requested in historical output.
  for(const prefix of ['', '?','>','='])for(const final of ['n','c','t'])term.parser.registerCsiHandler({prefix,final},()=>true);
  for(const id of [10,11,12,52])term.parser.registerOscHandler(id,()=>true);
  for(const intermediates of ['$','+'])term.parser.registerDcsHandler({intermediates,final:'q'},()=>true);
  term.open(element('hardware-terminal'));h.resize.observe(element('hardware-terminal'));
  term.onData(data=>{if(hardwareView===h)queueHardwareInput(new TextEncoder().encode(data))});
  term.onBinary(data=>{if(hardwareView===h)queueHardwareInput(Uint8Array.from(data,c=>c.charCodeAt(0)))});
  term.attachCustomKeyEventHandler(e=>hardwareKeyEvent(e,term.getSelection(),hardwareEnter(),input('hardware-backspace').value,queueHardwareInput));
 }
 renderDeviceLog();renderHardwareState();void pollDevice();
}
function fitHardwareTerminal(){const h=hardwareView,host=element('hardware-terminal');if(!h||toolsTab!=='hardware'||input('device-view').value!=='terminal'||!host.clientHeight)return;const screen=h.term.element?.querySelector<HTMLElement>('.xterm-screen'),rect=screen?.getBoundingClientRect();if(!rect?.width||!rect.height)return;const viewport=h.term.element?.querySelector<HTMLElement>('.xterm-viewport');h.term.resize(Math.max(2,Math.min(400,Math.floor((viewport?.clientWidth||host.clientWidth-22)/(rect.width/h.term.cols)))),Math.max(2,Math.min(150,Math.floor((host.clientHeight-14)/(rect.height/h.term.rows)))))}
function hardwareCanWrite(h:HardwareView|null){return !!h&&toolsTab==='hardware'&&h.alive&&h.task===chosen&&h.id===deviceID&&h.connected&&h.controller===chosen&&devices.find(d=>d.id===h.id)?.kind!=='relay'&&h.ready&&!h.busy&&!h.paused&&!!h.generation&&!devices.find(d=>d.id===h.id)?.read_only}
function renderHardwareState(){
 if(!element('hardware-panel'))return;const h=hardwareView,d=devices.find(d=>d.id===deviceID),relayDevice=d?.kind==='relay',terminal=input('device-view').value==='terminal'&&!relayDevice;
 element('hardware-task-filter').classList.toggle('hidden',terminal);element<HTMLSelectElement>('device-view').querySelector<HTMLOptionElement>('option[value=terminal]')!.disabled=!!relayDevice;
 button('device-ai').disabled=!d||!!detail?.task.archived;button('device-ai').textContent=hardwareAIGrants[deviceID]?.read?'AI 权限':'AI 使用';renderHardwareOverview();
 element('relay-panel').classList.toggle('hidden',!relayDevice);element('hardware-send-details').classList.toggle('hidden',!!relayDevice);element('hardware-panel').querySelector('.hardware-keys')!.classList.toggle('hidden',!!relayDevice);
 element('hardware-terminal').classList.toggle('hidden',!terminal);element('device-console').classList.toggle('hidden',terminal);element('hardware-panel').classList.toggle('hardware-log-view',!terminal);
 element('device-description').textContent=d?(d.protocol==='serial'?`${d.device} · ${d.baud} · ${d.data_bits||8}${({none:'N',even:'E',odd:'O',mark:'M',space:'S'} as Record<string,string>)[d.parity||'none']}${d.stop_bits||'1'}`:`${d.protocol.toUpperCase()} · ${d.host}:${d.port}`):'';
 element('device-status').textContent=h?.busy?'处理中…':h?.connected?'已连接'+(d?.read_only?' · 只读':''):h&&!h.ready?'读取状态…':'未连接';
 button('device-connect').classList.toggle('hidden',!!h?.connected);button('device-disconnect').classList.toggle('hidden',!h?.connected);button('device-connect').disabled=!d||!!h?.busy||!h?.ready;button('device-disconnect').disabled=!!h?.busy||h?.controller!==chosen||!!h?.relay?.busy;button('device-edit').disabled=!d||!!h?.connected||!!h?.busy;
 for(const id of ['device-send-button','device-interrupt'])button(id).disabled=!hardwareCanWrite(h);
 element('hardware-panel').querySelectorAll<HTMLButtonElement>('[data-hardware-key]').forEach(b=>b.disabled=!hardwareCanWrite(h));
 for(const id of ['device-clear','device-export','device-analyze','device-pause','device-bottom'])button(id).disabled=!h;
 button('device-pause').textContent=h?.paused?'继续显示':'暂停显示';
 if(h)h.term.options.disableStdin=!hardwareCanWrite(h)||toolsTab!=='hardware';
 element('device-owner').textContent=h?.connected?(h.controller===chosen?'本任务控制':h.controller?'由「'+(h.controllerTitle||h.controller)+'」控制 · 当前只查看':'设备已连接 · 控制权空闲'):'';
 button('device-claim').classList.toggle('hidden',!h?.connected||h.controller===chosen);button('device-release').classList.toggle('hidden',!h?.connected||h.controller!==chosen);
 button('device-claim').disabled=!!h?.busy||!!h?.relay?.busy;button('device-release').disabled=!!h?.busy||!!h?.relay?.busy;
 const power=h?.relay;element('relay-status').textContent=!h?.connected?'未连接':power?.busy?'电源操作中…':power?.known?'继电器反馈：供电'+(power.power?'接通':'断开')+' · '+new Date(power.updated).toLocaleTimeString():'供电状态未知 · 点击查询';
 element('relay-panel').querySelectorAll<HTMLButtonElement>('[data-relay]').forEach(b=>b.disabled=!h?.connected||!h.ready||h.controller!==chosen||h.busy||!!power?.busy||!!d?.read_only);
 element('hardware-hint').classList.toggle('hidden',!!relayDevice);
 element('hardware-hint').textContent=h?.paused?'已暂停显示与输入，后台仍在接收。':h?.connected?'点击终端直接输入；Tab 补全，↑↓ 历史，Ctrl+C 中断。':'连接后点击终端，直接输入命令。';
}
function cancelHardwareInput(){const h=hardwareView;if(h){clearTimeout(h.flushTimer);h.pending=[];h.pendingBytes=0}}
function queueHardwareInput(bytes:Uint8Array){const h=hardwareView;if(!hardwareCanWrite(h)||!h||!bytes.length)return;if(h.pendingBytes+bytes.length>32768){notify('输入过长，请分段粘贴');return}h.pending.push(bytes);h.pendingBytes+=bytes.length;if(!h.flushing){clearTimeout(h.flushTimer);h.flushTimer=setTimeout(()=>void flushHardwareInput(h),12)}}
async function flushHardwareInput(h:HardwareView){
 if(h.flushing)return;h.flushing=true;const generation=h.generation;
 try{while(h.pendingBytes){if(hardwareView!==h||!hardwareCanWrite(h)||h.generation!==generation){h.pending=[];h.pendingBytes=0;break}const bytes=new Uint8Array(h.pendingBytes);let offset=0;for(const p of h.pending){bytes.set(p,offset);offset+=p.length}h.pending=[];h.pendingBytes=0;await api(`tasks/${h.task}/hardware/${h.id}/send`,'POST',{encoding:'hex',data:bytesHex(bytes),connection_id:generation})}}
 catch(e){h.pending=[];h.pendingBytes=0;if(hardwareView===h){h.connected=false;h.ready=false;renderHardwareState();notify('发送未完成，未自动重发：'+(e as Error).message)}}finally{h.flushing=false}
}
function deviceLog(){const hexView=input('device-view').value==='hex',decoders=new Map<string,TextDecoder>();return hardwareLogEvents().map(e=>{let text=e.text;if(e.direction!=='status'){const decoder=decoders.get(e.direction)||new TextDecoder();decoders.set(e.direction,decoder);text=hexView?(e.hex.match(/.{2}/g)||[]).join(' ').toUpperCase():decoder.decode(hexBytes(e.hex),{stream:true}).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'')}return `${new Date(e.created).toLocaleTimeString()} ${e.direction.toUpperCase()}  ${text}`}).join('\n')}
function renderDeviceLog(){const h=hardwareView;if(h&&!h.paused){for(const e of deviceEvents){if(e.seq<=h.written)continue;if(e.direction==='rx'||(e.direction==='tx'&&input('hardware-echo').checked)){const bytes=hexBytes(e.hex);if(h.screenBytes+bytes.length>2*1024*1024){h.paused=true;renderHardwareState();notify('输出过快，已暂停显示；后台仍在接收。');break}h.screenBytes+=bytes.length;h.term.write(bytes,()=>{h.screenBytes-=bytes.length})}h.written=e.seq}}if(!h?.paused&&(input('device-view').value!=='terminal'||devices.find(d=>d.id===deviceID)?.kind==='relay')){const el=element('device-console'),bottom=el.scrollHeight-el.scrollTop-el.clientHeight<80;el.textContent=deviceLog();if(bottom)el.scrollTop=el.scrollHeight}}
async function pollDevice(){
 const h=hardwareView;if(!authenticated||toolsTab!=='hardware'||!h||h.task!==chosen||h.requesting||document.hidden)return;h.requesting=true;
 try{const r=await api<{events:DeviceEvent[];connected:boolean;connection_id:string;controller_task:string;controller_title:string;relay:HardwareView['relay']}>(`tasks/${h.task}/hardware/${h.id}/events?after=${deviceSeq}`+(h.ready?'&wait=1':'&tail=1'));if(hardwareView!==h||chosen!==h.task)return;
  if(h.generation&&h.generation!==r.connection_id)cancelHardwareInput();h.generation=r.connection_id;h.connected=r.connected;h.controller=r.controller_task;h.controllerTitle=r.controller_title;h.relay=r.relay;h.ready=true;
  deviceEvents.push(...r.events);deviceEvents=deviceEvents.slice(-2000);if(r.events.length)deviceSeq=r.events.at(-1)!.seq;renderDeviceLog();renderHardwareState();
 }catch(e){if(hardwareView===h){h.connected=false;h.ready=false;cancelHardwareInput();renderHardwareState();element('device-status').textContent=(e as Error).message}}finally{h.requesting=false}
}
async function deviceAction(action:string,data:unknown){const h=hardwareView;if(!h||h.busy)return;if(action==='send'&&!hardwareCanWrite(h))return;h.busy=true;renderHardwareState();try{await api(`tasks/${h.task}/hardware/${h.id}/${action}`,'POST',{...(data as object),connection_id:h.generation});if(hardwareView===h){if(action==='disconnect'){h.connected=false;h.generation=''}if(action==='connect')h.term.focus()}}catch(e){notify((e as Error).message)}finally{h.busy=false;if(hardwareView===h){renderHardwareState();void pollDevice()}}}

function hardwareLogEvents(){return input('hardware-task-log').checked&&(input('device-view').value!=='terminal'||devices.find(d=>d.id===deviceID)?.kind==='relay')?deviceEvents.filter(e=>e.task_id===chosen):deviceEvents}
async function editHardwareAI(){const d=devices.find(d=>d.id===deviceID);if(!d||detail?.task.archived)return;const task=chosen;try{const grants=await api<Record<string,HardwareGrant>>(`tasks/${task}/hardware-ai`);if(task!==chosen||d.id!==deviceID)return;hardwareAIGrants=grants;hardwareAIEditing={task,device:d};const g=grants[d.id];element('hardware-ai-name').textContent='任务：'+(detail?.task.title||task)+'\n设备：'+d.name+' · '+(d.device||d.host);for(const key of ['read','write','power'] as const)input('hardware-ai-'+key).checked=!!g?.[key];element('hardware-ai-write-row').classList.toggle('hidden',d.kind==='relay');element('hardware-ai-power-row').classList.toggle('hidden',d.kind!=='relay');element('hardware-ai-error').textContent='';renderHardwareAIForm();element<HTMLDialogElement>('hardware-ai-dialog').showModal()}catch(e){notify((e as Error).message)}}
function renderHardwareAIForm(){const d=hardwareAIEditing?.device,read=input('hardware-ai-read').checked;for(const key of ['write','power']){const el=input('hardware-ai-'+key);el.disabled=!read||!!d?.read_only;if(el.disabled)el.checked=false}if(d?.kind==='relay')input('hardware-ai-write').checked=false;else input('hardware-ai-power').checked=false}
async function saveHardwareAI(){const editing=hardwareAIEditing;if(!editing||hardwareAISaving)return;hardwareAISaving=true;++hardwareLoadRequest;hardwareOverviewLoading=false;button('hardware-ai-save').disabled=true;
 try{await api<HardwareGrant>(`tasks/${editing.task}/hardware/${editing.device.id}/ai`,'PUT',{read:input('hardware-ai-read').checked,write:input('hardware-ai-write').checked,power:input('hardware-ai-power').checked});element<HTMLDialogElement>('hardware-ai-dialog').close();if(chosen===editing.task)await loadDevices();notify('AI 硬件权限已保存')}
 catch(e){element('hardware-ai-error').textContent=(e as Error).message}finally{hardwareAISaving=false;button('hardware-ai-save').disabled=false}}

function hardwareGrantText(g:HardwareGrant|undefined,d:DeviceConfig,archived=false){
 if(archived)return '已归档 · AI 停用';if(!g?.read)return 'AI 未授权';
 return [d.kind==='relay'?'读取 / 查询':'读取',g.write&&!d.read_only&&d.kind!=='relay'?'发送命令':'',g.power&&!d.read_only&&d.kind==='relay'?'电源控制':''].filter(Boolean).join(' · ');
}
function hardwareConnectionText(item:HardwareOverview,task:string){
 const h=hardwareView?.task===chosen&&hardwareView?.id===item.config.id&&hardwareView.ready?hardwareView:null;
 const connected=h?h.connected:item.connected,owner=h?h.controller:item.controller_task,title=h?h.controllerTitle:item.controller_title;
 if(!connected){const use=item.tasks.find(t=>t.id===task);return '未连接'+(use?.ai.read&&!use.archived?' · AI 可按需连接':'')}
 return '已连接 · '+(owner===task?'本任务控制':owner?'由「'+(title||owner)+'」控制':'控制权空闲');
}
function renderHardwareOverview(){
 const host=element('hardware-ai-list');if(!host)return;
 if(hardwareOverviewTask!==chosen){element('hardware-ai-count').textContent='';host.textContent='正在读取设备和权限…';delete host.dataset.snapshot;return}
 const list=hardwareOverview.filter(d=>d.tasks.some(t=>t.id===chosen)),enabled=list.filter(d=>d.tasks.some(t=>t.id===chosen&&!t.archived&&t.ai.read)).length;
 element('hardware-ai-count').textContent=`已授权 ${enabled} / ${list.length}`;
 const html=list.map(item=>{const d=item.config,use=item.tasks.find(t=>t.id===chosen)!;return `<button class="hardware-ai-device ${d.id===deviceID?'selected':''}" data-ai-device="${d.id}" ${use.archived?'disabled':''}><span><strong>${escapeHTML(d.name)}</strong><small>${escapeHTML((d.device||d.host)+' · '+hardwareConnectionText(item,chosen))}</small>${use.active_ai?`<small class="hardware-active-grant">本轮：${escapeHTML(hardwareGrantText(use.active_ai,d))}</small>`:''}</span><span class="hardware-grant ${use.ai.read&&!use.archived?'enabled':''}">${escapeHTML(hardwareGrantText(use.ai,d,use.archived))}</span></button>`}).join('')||'<p class="muted">当前任务尚未添加设备，可从设备库选择。</p>';
 if(host.dataset.snapshot!==html){host.innerHTML=html;host.dataset.snapshot=html}
}
function renderHardwareLibrary(){
 if(hardwareLibraryAttaching)return;const host=element('hardware-library-list');
 const html=hardwareOverview.map(item=>{const d=item.config,attached=item.tasks.some(t=>t.id===chosen);return `<article class="library-device"><div class="library-device-head"><div><strong>${escapeHTML(d.name)}</strong><small>${escapeHTML(d.kind==='relay'?'板子电源':d.protocol.toUpperCase())} · ${escapeHTML(d.device||d.host)}</small><small>${escapeHTML(hardwareConnectionText(item,chosen))}</small></div><button data-attach="${d.id}" ${attached||detail?.task.archived?'disabled':''}>${attached?'本任务已添加':'添加到本任务'}</button></div><div class="library-task-list">${item.tasks.map(t=>`<div class="library-task"><button data-hardware-task="${t.id}" data-device="${d.id}" title="打开此任务的硬件面板">${escapeHTML(t.title)}${t.id===chosen?'（本任务）':''}</button><span class="hardware-grant ${t.ai.read&&!t.archived?'enabled':''}">${escapeHTML(hardwareGrantText(t.ai,d,t.archived))}</span>${t.active_ai?`<small>本轮：${escapeHTML(hardwareGrantText(t.active_ai,d))}</small>`:''}</div>`).join('')||'<small>尚未关联任务</small>'}</div></article>`}).join('')||'<p>还没有设备。在硬件面板点击 ＋ 添加。</p>';
 if(host.dataset.snapshot!==html){host.innerHTML=html;host.dataset.snapshot=html}
}
async function openHardwareLibrary(){const task=chosen;await loadDevices();if(task!==chosen||hardwareOverviewTask!==task)return;renderHardwareLibrary();element<HTMLDialogElement>('hardware-library-dialog').showModal()}
async function attachHardwareFromLibrary(id:string){
 if(hardwareLibraryAttaching)return;const task=chosen;hardwareLibraryAttaching=true;element('hardware-library-list').querySelectorAll<HTMLButtonElement>('[data-attach]').forEach(b=>b.disabled=true);
 try{await api(`tasks/${task}/hardware/${id}/attach`,'POST',{});element<HTMLDialogElement>('hardware-library-dialog').close();if(chosen===task){deviceID=id;await loadDevices()}}catch(e){notify((e as Error).message)}finally{hardwareLibraryAttaching=false;delete element('hardware-library-list').dataset.snapshot;renderHardwareLibrary()}
}
async function relayOperation(action:string){const h=hardwareView,d=devices.find(d=>d.id===deviceID);if(!h||!d||h.busy||h.controller!==chosen)return;const label=({on:'上电',off:'断电',cycle:'断电 '+d.relay?.cycle_seconds+' 秒后重新上电',query:'查询'} as Record<string,string>)[action];if(action!=='query'&&!confirm('对「'+d.name+'」执行'+label+'？'+(action==='off'||action==='cycle'?'这会中断板子当前运行。':'')))return;h.busy=true;renderHardwareState();try{await api(`tasks/${h.task}/hardware/${h.id}/relay`,'POST',{action,connection_id:h.generation,confirmed:action!=='query'});notify('已收到继电器反馈')}catch(e){notify((e as Error).message)}finally{h.busy=false;if(hardwareView===h){renderHardwareState();void pollDevice()}}}
