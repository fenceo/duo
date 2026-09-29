type ConversationFilter={tools:boolean;process:boolean};
// Defined in app.ts. Guarded so this file also loads standalone in tests.
declare function knowledgeForRun(runId:string):unknown;
type ConversationCategory='message'|'tools'|'process'|'error';
const conversationFilterKey='jianzuo-conversation-filter-v2';
let conversationFilter:ConversationFilter={tools:false,process:true};
type ConversationItem={event:EventRecord;node?:HTMLElement;searchMatch?:boolean;loading?:boolean};
const conversationItems=new Map<number,ConversationItem>();
const conversationPageSize=100;
type ConversationTurn={root:HTMLElement;input:HTMLElement;process:HTMLDetailsElement;summary:HTMLElement;body:HTMLElement;output:HTMLElement;files:HTMLElement;footer:HTMLElement;items:ConversationItem[];dirty:boolean;state:string;result:string;limit:number;hidden:number;more:HTMLButtonElement};
const conversationTurns=new Map<string,ConversationTurn>();
type ConversationWindow={before:string;has_older:boolean;sequence:number;has_more:boolean;records:Record<string,{before:number;has_older:boolean}>};
let conversationHistory={before:'',hasOlder:false,expanded:false,loading:false,error:''};
const conversationRecordPages=new Map<string,{before:number;has_older:boolean;loading?:boolean;error?:string}>();

function readConversationFilter(raw:string|null):ConversationFilter{
 try{const value=JSON.parse(raw||'null');if(typeof value?.tools==='boolean'&&typeof value?.process==='boolean')return {tools:value.tools,process:value.process}}catch{}
 return {tools:false,process:true};
}
function finalConversationEvents(events:EventRecord[],runs:Run[]):Set<number>{
 const results=new Map(runs.filter(r=>r.status==='done'&&r.result).map(r=>[r.id,r.result]));
 const final=new Set<number>();
 // Only a completed run can identify a final reply. The last matching event
 // wins if an engine repeated the same text during execution.
 for(let i=events.length-1;i>=0;i--){const event=events[i];if(event.kind==='assistant'&&results.get(event.run_id)===event.text){final.add(event.seq);results.delete(event.run_id)}}
 return final;
}
function conversationCategory(event:EventRecord,final:Set<number>):ConversationCategory{
 if(event.kind==='error'||event.kind==='status'&&/^(failed|interrupted)(\s|$)/.test(event.text.trim()))return 'error';
 if(event.kind==='user'||event.kind==='assistant'&&(!event.run_id||final.has(event.seq)))return 'message';
 if(event.kind==='tool'||event.kind==='log')return 'tools';
 return 'process';
}
function conversationEventVisible(category:ConversationCategory,filter:ConversationFilter):boolean{
 return category==='message'||category==='error'||(category==='tools'?filter.tools:filter.process);
}
function resetConversation(){conversationItems.clear();conversationTurns.clear();conversationRecordPages.clear();conversationHistory={before:'',hasOlder:false,expanded:false,loading:false,error:''}}
function mergeConversationRuns(incoming:Run[],preserveExisting=false){
 const runs=new Map((detail?.runs||[]).map(run=>[run.id,run]));for(const run of incoming)if(!preserveExisting||!runs.has(run.id))runs.set(run.id,run);
 return [...runs.values()].sort((a,b)=>a.created-b.created||a.id.localeCompare(b.id));
}
function receiveConversationDetail(next:Detail,mode:'initial'|'poll'|'older'|'records'='initial'){
 const runs=mode==='initial'?next.runs:mergeConversationRuns(next.runs,mode==='older'||mode==='records');
 if(mode==='initial'||mode==='poll')detail={...next,runs};else if(detail)detail.runs=runs;
 const page=next.conversation;
 if(page){
  if(mode==='initial'||mode==='older'||mode==='poll'&&!conversationHistory.expanded){conversationHistory.before=page.before;conversationHistory.hasOlder=page.has_older}
  for(const [id,records] of Object.entries(page.records)){const previous=conversationRecordPages.get(id);if(!previous||records.before<previous.before)conversationRecordPages.set(id,records)}
  if(mode==='initial'||mode==='poll')sequence=Math.max(sequence,page.sequence);
 }
 if(mode==='older')conversationHistory.expanded=true;
 for(const run of next.runs)conversationTurn(run.id);
 for(const event of next.events)addConversationEvent(event);
 if(mode==='initial'||mode==='poll')for(const event of next.events)sequence=Math.max(sequence,event.seq);
 applyConversationFilter(mode==='initial'||mode==='poll');
}
function conversationHistoryBar(){
 const container=element('conversation');let bar=element('conversation-history');
 if(!bar){bar=document.createElement('div');bar.id='conversation-history';bar.className='conversation-history';bar.innerHTML='<button type="button" id="conversation-older"></button><button type="button" id="conversation-recent">收起早期对话</button><small role="status"></small>';container.prepend(bar);button('conversation-older').onclick=()=>void loadOlderConversation();button('conversation-recent').onclick=()=>{conversationHistory.expanded=false;for(const turn of conversationTurns.values())turn.dirty=true;applyConversationFilter(false);container.scrollTop=container.scrollHeight}}
 const extra=conversationTurns.size>5,more=conversationHistory.hasOlder||extra&&!conversationHistory.expanded;
 const older=button('conversation-older'),recent=button('conversation-recent');
 older.classList.toggle('hidden',!more);older.disabled=conversationHistory.loading;older.textContent=conversationHistory.loading?'正在读取…':'展开更早对话';older.title='按需读取，每次 5 轮；原始记录完整保留';
 recent.classList.toggle('hidden',!conversationHistory.expanded);
 bar.querySelector('small')!.textContent=conversationHistory.error||(!conversationHistory.expanded&&more?'默认显示最近 5 轮':'');bar.classList.toggle('hidden',!more&&!conversationHistory.expanded&&!conversationHistory.error);
 return bar;
}
async function loadOlderConversation(){
 if(!detail||conversationHistory.loading)return;
 const state=conversationHistory,task=chosen,selected=selection,epoch=shellEpoch,container=element('conversation');
 const anchor=container.querySelector<HTMLElement>('.conversation-turn'),top=anchor?.getBoundingClientRect().top;
 state.loading=true;state.error='';conversationHistoryBar();
 try{
  // Reopen already loaded rounds without another request when possible.
  if(!state.expanded&&conversationTurns.size>5){state.expanded=true;for(const turn of conversationTurns.values())turn.dirty=true;applyConversationFilter(false)}
  else if(state.hasOlder&&state.before){const next=await api<Detail>('tasks/'+encodeURIComponent(task)+'?recent=1&before='+encodeURIComponent(state.before),'GET',undefined,shellController.signal);if(task!==chosen||selected!==selection||!shellCurrent(epoch)||conversationHistory!==state)return;receiveConversationDetail(next,'older')}
  if(anchor&&top!==undefined&&anchor.isConnected)container.scrollTop+=anchor.getBoundingClientRect().top-top;
 }catch(e){if(conversationHistory===state)state.error='读取失败，可重试：'+(e as Error).message}
 finally{if(conversationHistory===state){state.loading=false;conversationHistoryBar()}}
}
async function loadOlderConversationRecords(id:string,turn:ConversationTurn){
 const page=conversationRecordPages.get(id);if(!page?.has_older||page.loading)return;
 const task=chosen,selected=selection,epoch=shellEpoch,container=element('conversation'),anchor=turn.body.querySelector<HTMLElement>('[data-event]'),top=anchor?.getBoundingClientRect().top;
 page.loading=true;page.error='';turn.dirty=true;applyConversationFilter(false);
 try{const next=await api<Detail>('tasks/'+encodeURIComponent(task)+'?recent=1&run='+encodeURIComponent(id)+'&before_event='+page.before,'GET',undefined,shellController.signal);if(task!==chosen||selected!==selection||!shellCurrent(epoch)||conversationTurns.get(id)!==turn)return;turn.limit+=conversationPageSize;receiveConversationDetail(next,'records');if(anchor&&top!==undefined&&anchor.isConnected)container.scrollTop+=anchor.getBoundingClientRect().top-top}
 catch(e){if(conversationTurns.get(id)===turn)page.error='读取失败，点击重试：'+(e as Error).message}
 finally{if(conversationTurns.get(id)===turn){page.loading=false;turn.dirty=true;applyConversationFilter(false)}}
}
function conversationTurn(id:string):ConversationTurn{
 const existing=conversationTurns.get(id);if(existing)return existing;
 const root=document.createElement('section');root.className='conversation-turn';root.dataset.run=id;
 root.innerHTML='<div class="turn-input"></div><div class="turn-attachments"></div><details class="turn-process"><summary></summary><div class="turn-records"></div></details><div class="turn-output"></div><footer class="turn-footer"></footer>';
 const more=document.createElement('button');more.type='button';more.className='conversation-more';
 const turn:ConversationTurn={root,input:root.children[0] as HTMLElement,process:root.querySelector<HTMLDetailsElement>('details')!,summary:root.querySelector('summary')!,body:root.querySelector<HTMLElement>('.turn-records')!,output:root.querySelector<HTMLElement>('.turn-output')!,files:root.querySelector<HTMLElement>('.turn-attachments')!,footer:root.querySelector<HTMLElement>('.turn-footer')!,items:[],dirty:true,state:'',result:'',limit:conversationPageSize,hidden:0,more};
 turn.process.open=conversationFilter.tools||conversationFilter.process;
 turn.process.addEventListener('toggle',()=>{if(conversationTurns.get(id)===turn)applyConversationFilter()});
 more.onclick=()=>{
  if(conversationTurns.get(id)!==turn)return;
  if(more.dataset.remote==='true'){void loadOlderConversationRecords(id,turn);return}
  // Keep the first visible record in place when prepending an older page.
  const anchor=turn.body.querySelector<HTMLElement>('[data-event]'),container=element('conversation'),top=anchor?.getBoundingClientRect().top;
  turn.limit+=conversationPageSize;turn.dirty=true;applyConversationFilter(false);
  if(anchor&&top!==undefined)container.scrollTop+=anchor.getBoundingClientRect().top-top;
 };
 conversationTurns.set(id,turn);return turn;
}
function addConversationEvent(event:EventRecord){
 if(conversationItems.has(event.seq))return;
 const item:ConversationItem={event};conversationItems.set(event.seq,item);
 const turn=conversationTurn(event.run_id||'');turn.items.push(item);turn.items.sort((a,b)=>a.event.seq-b.event.seq);turn.dirty=true;
}
function conversationEventNode(item:ConversationItem):HTMLElement{
 if(item.node)return item.node;
 const ev=item.event,node=document.createElement('div');node.dataset.event=String(ev.seq);
 if(ev.kind==='user'||ev.kind==='assistant'){node.className='message '+ev.kind;node.innerHTML='<div class="label">'+(ev.kind==='user'?'你':taskEngineName(detail?.runs.find(r=>r.id===ev.run_id)?.engine||detail?.task.engine))+'</div><div class="content">'+(ev.kind==='user'?escapeHTML(ev.text):markdown(ev.text))+'</div>'}
 else if(ev.kind==='tool'||ev.kind==='log'){
  node.className='log';const disclosure=document.createElement('details'),summary=document.createElement('summary');
  const newline=ev.text.indexOf('\n');summary.textContent=ev.text.slice(0,newline<0?200:Math.min(newline,200));disclosure.append(summary);node.append(disclosure);
  // A collapsed tool needs only its summary, not a multi-megabyte <pre>.
  const show=async()=>{
   if(!disclosure.open){disclosure.querySelector('pre')?.remove();return}
   if(disclosure.querySelector('pre')||item.loading)return;
   disclosure.querySelector('button')?.remove();
   const pre=document.createElement('pre');pre.textContent=ev.truncated?'正在读取完整记录…':ev.text;disclosure.append(pre);
   if(!ev.truncated)return;
   const task=chosen,selected=selection,epoch=shellEpoch;item.loading=true;
   try{const full=await api<EventRecord>('tasks/'+encodeURIComponent(task)+'?event='+ev.seq,'GET',undefined,shellController.signal);if(task!==chosen||selected!==selection||!shellCurrent(epoch)||conversationItems.get(ev.seq)!==item)return;ev.text=full.text;ev.truncated=false;if(disclosure.open){pre.textContent=ev.text;if(!pre.isConnected)disclosure.append(pre)}}
   catch(e){if(conversationItems.get(ev.seq)===item&&disclosure.open){pre.textContent='读取失败：'+(e as Error).message;const retry=document.createElement('button');retry.type='button';retry.textContent='重试读取';retry.onclick=()=>{pre.remove();void show()};disclosure.append(retry)}}
   finally{item.loading=false}
  };
  disclosure.addEventListener('toggle',()=>void show());
 }
 else {node.className='progress'+(ev.kind==='error'?' error':'');node.textContent=ev.kind==='status'?'本轮执行 · '+(names[ev.text.trim()]||ev.text):ev.text}
 item.node=node;return node;
}
function reconcileConversationNodes(parent:HTMLElement,nodes:HTMLElement[]){
 const wanted=new Set(nodes);
 for(const child of Array.from(parent.children))if(!wanted.has(child as HTMLElement))child.remove();
 let next=parent.firstElementChild;
 for(const node of nodes){if(node===next)next=next.nextElementSibling;else parent.insertBefore(node,next)}
}
function revealConversationEvent(seq:number):HTMLElement|null{
 const item=conversationItems.get(seq);if(!item)return null;
 conversationHistory.expanded=true;
 item.searchMatch=true;const turn=conversationTurn(item.event.run_id||'');turn.process.open=true;turn.dirty=true;applyConversationFilter(false);
 const node=conversationEventNode(item),tool=node.querySelector('details');if(tool)tool.open=true;
 return node;
}
function installConversationFilter(){
 try{conversationFilter=readConversationFilter(localStorage.getItem(conversationFilterKey)||localStorage.getItem('jianzuo-conversation-filter-v1'))}catch{}
 element('conversation').insertAdjacentHTML('beforebegin',`<div id="conversation-filter" class="conversation-filter hidden" role="group" aria-label="对话显示选项，仅改变显示"><label title="显示中间过程；只影响页面显示，记录完整保留"><input type="checkbox" id="conversation-process"><span class="filter-full">中间过程</span><span class="filter-short">过程</span></label><label title="显示命令与工具记录；不改变 AI 执行或上下文"><input type="checkbox" id="conversation-tools"><span class="filter-full">命令与工具</span><span class="filter-short">工具</span></label><small id="conversation-hidden"></small></div>`);
 input('conversation-tools').onchange=()=>setConversationFilter({...conversationFilter,tools:input('conversation-tools').checked});
 input('conversation-process').onchange=()=>setConversationFilter({...conversationFilter,process:input('conversation-process').checked});
 updateConversationFilterControls();
}
function setConversationFilter(filter:ConversationFilter){
 conversationFilter=filter;
 for(const turn of conversationTurns.values())turn.process.open=filter.tools||filter.process;
 try{localStorage.setItem(conversationFilterKey,JSON.stringify(filter))}catch{}
 updateConversationFilterControls();applyConversationFilter();
}
function updateConversationFilterControls(){
 input('conversation-tools').checked=conversationFilter.tools;input('conversation-process').checked=conversationFilter.process;
}
function applyConversationFilter(followBottom=true){
 const container=element('conversation');if(!container)return;
 const runs=new Map((detail?.runs||[]).map(run=>[run.id,run])),pending=new Set((detail?.approvals||[]).map(request=>request.run_id));
 const ordered=[...conversationTurns.entries()].sort(([a,x],[b,y])=>{const ra=runs.get(a),rb=runs.get(b);return (ra?.created||x.items[0]?.event.created||0)-(rb?.created||y.items[0]?.event.created||0)||a.localeCompare(b)});
 const recent=new Set(ordered.slice(-5).map(([id])=>id));
 const visible=ordered.filter(([id,turn])=>conversationHistory.expanded||recent.has(id)||['running','queued'].includes(runs.get(id)?.status||'')||turn.items.some(item=>item.searchMatch));
 const changes:{turn:ConversationTurn;run:Run|undefined;state:string;waiting:boolean}[]=[];
 for(const [id,turn] of conversationTurns){
  const run=runs.get(id),knowledge=typeof knowledgeForRun==='function'?knowledgeForRun(id) as {id:string;revision:number;source:string}|null:null,waiting=pending.has(id);
  // Compare only small metadata. Event bodies are immutable and never serialized
  // on idle polls; result equality still detects completion without a new event.
  const state=JSON.stringify([run?.status,run?.created,run?.started,run?.finished,run?.usage,run?.attachments,knowledge?.id,knowledge?.revision,knowledge?.source,waiting,conversationFilter.tools,conversationFilter.process,turn.process.open,visible.some(([,item])=>item===turn)]);
  if(turn.dirty||turn.state!==state||turn.result!==(run?.result||''))changes.push({turn,run,state,waiting});
 }
 // A quiet long task must do no history DOM work or synchronous layout reads.
 if(!changes.length)return;
 const nearBottom=followBottom&&container.scrollHeight-container.scrollTop-container.clientHeight<100,top=container.scrollTop;
 for(const {turn,run,state,waiting} of changes){
  if(!visible.some(([,item])=>item===turn)){turn.root.remove();turn.dirty=false;turn.state=state;turn.result=run?.result||'';continue}
  const final=finalConversationEvents(turn.items.map(item=>item.event),run?[run]:[]),inputs:HTMLElement[]=[],outputs:HTMLElement[]=[],records:ConversationItem[]=[];
  let count=0;turn.hidden=0;
  for(const item of turn.items){
   const category=conversationCategory(item.event,final),record=category==='tools'||category==='process';if(record)count++;
   const shown=item.searchMatch||conversationEventVisible(category,conversationFilter);
   if(!shown){turn.hidden++;continue}
   if(record)records.push(item);
   else {const node=conversationEventNode(item);node.dataset.category=category;node.classList.toggle('error',category==='error');if(item.searchMatch)node.dataset.searchMatch='true';(item.event.kind==='user'?inputs:outputs).push(node)}
  }
  const body:HTMLElement[]=[];
  const page=conversationRecordPages.get(run?.id||''),remote=!!page?.has_older;
  if(turn.process.open){
   const start=Math.max(0,records.length-turn.limit);
   if(start||remote){const text=start?'加载更早的 '+Math.min(start,conversationPageSize)+' 条记录（还有 '+start+' 条）':page?.loading?'正在读取…':page?.error||'加载本轮更早记录';if(turn.more.textContent!==text)turn.more.textContent=text;turn.more.dataset.remote=String(!start&&remote);turn.more.disabled=!!page?.loading;body.push(turn.more)}
   records.forEach((item,index)=>{if(index<start&&!item.searchMatch)return;const node=conversationEventNode(item);node.dataset.category=conversationCategory(item.event,final);if(item.searchMatch)node.dataset.searchMatch='true';body.push(node)});
  }
  reconcileConversationNodes(turn.input,inputs);reconcileConversationNodes(turn.output,outputs);reconcileConversationNodes(turn.body,body);
  if(!inputs.length&&run?.input){const node=document.createElement('div');node.className='message user';node.innerHTML='<div class="content">'+escapeHTML(run.input)+'</div>';turn.input.append(node)}
  if(run?.status==='done'&&run.result&&!final.size){const node=document.createElement('div');node.className='message assistant';node.innerHTML='<div class="label">'+taskEngineName(run.engine||detail?.task.engine)+'</div><div class="content">'+markdown(run.result)+'</div>';turn.output.append(node)}
  if(turn.root.parentElement!==container)container.append(turn.root);
  turn.process.classList.toggle('hidden',!records.length&&!(remote&&(conversationFilter.tools||conversationFilter.process)));
  const label=waiting?'等待你处理':run?.status==='running'?'正在执行':run?.status==='queued'?'排队中':'执行记录';
  const text=label+' · '+records.length+' 条'+(remote?' · 较早记录按需加载':'');
  if(turn.summary.textContent!==text)turn.summary.textContent=text;
  const footer=run?runFooter(run):'';if(turn.footer.innerHTML!==footer)turn.footer.innerHTML=footer;
  const files=(run?.attachments||[]).map(f=>`<a href="/api/tasks/${encodeURIComponent(chosen)}/attachments/${encodeURIComponent(f.id)}" class="attachment-chip" download="${escapeHTML(f.name)}">↧ ${escapeHTML(f.name)}</a>`).join('');if(turn.files.innerHTML!==files)turn.files.innerHTML=files;
  turn.dirty=false;turn.state=state;turn.result=run?.result||'';
 }
 const history=conversationHistoryBar(),extra=Array.from(container.children).filter(node=>node!==history&&!node.classList.contains('conversation-turn')) as HTMLElement[];
 reconcileConversationNodes(container,[...extra,history,...visible.map(([,turn])=>turn.root)]);
 const hidden=Array.from(conversationTurns.values()).reduce((sum,turn)=>sum+turn.hidden,0),counter=element('conversation-hidden'),text=hidden?'已筛除 '+hidden+' 条':'';if(counter&&counter.textContent!==text)counter.textContent=text;
 if(nearBottom)container.scrollTop=container.scrollHeight;else container.scrollTop=top;
}
function formatTokens(n:number):string{if(n>=1000000)return (n/1000000).toFixed(1).replace(/\.0$/,'')+'M';if(n>=1000)return (n/1000).toFixed(1).replace(/\.0$/,'')+'K';return String(n)}
function formatDuration(ms:number):string{const seconds=Math.max(0,Math.round(ms/1000));return seconds>=60?Math.floor(seconds/60)+'分'+seconds%60+'秒':seconds+'秒'}
function runFooter(run:Run):string{
 if(!run.finished||['queued','running'].includes(run.status))return '';
 const usage=run.usage,usageTip=usage?`输入 ${usage.input} · 输出 ${usage.output} · 缓存读取 ${usage.cached||0} · 缓存写入 ${usage.cache_write||0}`:'此轮引擎未返回用量，历史记录不作估算';
 const durationTip=run.started?'从本轮实际开始执行计算':'旧记录未保存开始时间，包含排队时间';
 const runId=typeof run.id==='string'?run.id:'';
 const settled=runId&&typeof knowledgeForRun==='function'?knowledgeForRun(runId):null;
 const automatic=(settled as {source?:string}|null)?.source==='auto';
 const knowledge=runId&&run.status==='done'&&run.result?`<button type="button" class="run-knowledge" data-knowledge-run="${escapeHTML(runId)}" ${settled?'disabled':''} title="${settled?'这轮结果已经沉淀到任务知识':'把这轮结果存成一条任务知识'}">${automatic?'已自动记录':settled?'已沉淀':'沉淀为知识'}</button>`:'';
 return `<span title="${escapeHTML(usageTip)}">用量 ${usage?formatTokens(usage.total)+' tok':'未提供'}</span><span title="${durationTip}">用时 ${formatDuration(run.finished-(run.started||run.created))}</span><time title="${escapeHTML(new Date(run.finished).toLocaleString())}">时间 ${new Date(run.finished).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit',hour12:false})}</time>${knowledge}`;
}
