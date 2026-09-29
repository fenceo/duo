type ConversationFilter={tools:boolean;process:boolean};
// Defined in app.ts. Guarded so this file also loads standalone in tests.
declare function knowledgeForRun(runId:string):unknown;
type ConversationCategory='message'|'tools'|'process'|'error';
const conversationFilterKey='jianzuo-conversation-filter-v1';
let conversationFilter:ConversationFilter={tools:false,process:false};
type ConversationItem={event:EventRecord;node?:HTMLElement;searchMatch?:boolean};
const conversationItems=new Map<number,ConversationItem>();
const conversationPageSize=100;
type ConversationTurn={root:HTMLElement;input:HTMLElement;process:HTMLDetailsElement;summary:HTMLElement;body:HTMLElement;output:HTMLElement;files:HTMLElement;footer:HTMLElement;items:ConversationItem[];dirty:boolean;state:string;result:string;limit:number;hidden:number;more:HTMLButtonElement};
const conversationTurns=new Map<string,ConversationTurn>();

function readConversationFilter(raw:string|null):ConversationFilter{
 try{const value=JSON.parse(raw||'null');if(typeof value?.tools==='boolean'&&typeof value?.process==='boolean')return {tools:value.tools,process:value.process}}catch{}
 return {tools:false,process:false};
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
function resetConversation(){conversationItems.clear();conversationTurns.clear()}
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
 const turn=conversationTurn(event.run_id||'');turn.items.push(item);turn.dirty=true;
}
function conversationEventNode(item:ConversationItem):HTMLElement{
 if(item.node)return item.node;
 const ev=item.event,node=document.createElement('div');node.dataset.event=String(ev.seq);
 if(ev.kind==='user'||ev.kind==='assistant'){node.className='message '+ev.kind;node.innerHTML='<div class="label">'+(ev.kind==='user'?'你':taskEngineName(detail?.task.engine))+'</div><div class="content">'+(ev.kind==='user'?escapeHTML(ev.text):markdown(ev.text))+'</div>'}
 else if(ev.kind==='tool'||ev.kind==='log'){
  node.className='log';const disclosure=document.createElement('details'),summary=document.createElement('summary');
  const newline=ev.text.indexOf('\n');summary.textContent=ev.text.slice(0,newline<0?200:Math.min(newline,200));disclosure.append(summary);node.append(disclosure);
  // A collapsed tool needs only its summary, not a multi-megabyte <pre>.
  disclosure.addEventListener('toggle',()=>{
   if(disclosure.open&&!disclosure.querySelector('pre')){const pre=document.createElement('pre');pre.textContent=ev.text;disclosure.append(pre)}
   else if(!disclosure.open)disclosure.querySelector('pre')?.remove();
  });
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
 item.searchMatch=true;const turn=conversationTurn(item.event.run_id||'');turn.process.open=true;turn.dirty=true;applyConversationFilter(false);
 const node=conversationEventNode(item),tool=node.querySelector('details');if(tool)tool.open=true;
 return node;
}
function installConversationFilter(){
 try{conversationFilter=readConversationFilter(localStorage.getItem(conversationFilterKey))}catch{}
 element('conversation').insertAdjacentHTML('beforebegin',`<div id="conversation-filter" class="conversation-filter hidden" role="group" aria-label="对话显示"><div class="filter-presets"><button id="conversation-results" title="折叠每轮执行过程，保留回复和错误">对话</button><button id="conversation-all" title="展开各轮执行记录，较早记录可继续加载">轨迹</button></div><details class="display-menu"><summary>筛选</summary><div><label><input type="checkbox" id="conversation-tools">命令与工具</label><label><input type="checkbox" id="conversation-process">中间过程</label><small>仅改变显示，记录完整保留</small></div></details><small id="conversation-hidden"></small></div>`);
 button('conversation-results').onclick=()=>setConversationFilter({tools:false,process:false});
 button('conversation-all').onclick=()=>setConversationFilter({tools:true,process:true});
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
 for(const [id,selected] of [['conversation-results',!conversationFilter.tools&&!conversationFilter.process],['conversation-all',conversationFilter.tools&&conversationFilter.process]] as const){button(id).classList.toggle('selected',selected);button(id).setAttribute('aria-pressed',String(selected))}
}
function applyConversationFilter(followBottom=true){
 const container=element('conversation');if(!container)return;
 const runs=new Map((detail?.runs||[]).map(run=>[run.id,run])),pending=new Set((detail?.approvals||[]).map(request=>request.run_id));
 const changes:{turn:ConversationTurn;run:Run|undefined;state:string;waiting:boolean}[]=[];
 for(const [id,turn] of conversationTurns){
  const run=runs.get(id),knowledge=typeof knowledgeForRun==='function'?knowledgeForRun(id) as {id:string;revision:number;source:string}|null:null,waiting=pending.has(id);
  // Compare only small metadata. Event bodies are immutable and never serialized
  // on idle polls; result equality still detects completion without a new event.
  const state=JSON.stringify([run?.status,run?.created,run?.started,run?.finished,run?.usage,run?.attachments,knowledge?.id,knowledge?.revision,knowledge?.source,waiting,conversationFilter.tools,conversationFilter.process,turn.process.open]);
  if(turn.dirty||turn.state!==state||turn.result!==(run?.result||''))changes.push({turn,run,state,waiting});
 }
 // A quiet long task must do no history DOM work or synchronous layout reads.
 if(!changes.length)return;
 const nearBottom=followBottom&&container.scrollHeight-container.scrollTop-container.clientHeight<100,top=container.scrollTop;
 const compact=!conversationFilter.tools&&!conversationFilter.process;
 for(const {turn,run,state,waiting} of changes){
  const final=finalConversationEvents(turn.items.map(item=>item.event),run?[run]:[]),inputs:HTMLElement[]=[],outputs:HTMLElement[]=[],records:ConversationItem[]=[];
  let count=0;turn.hidden=0;
  for(const item of turn.items){
   const category=conversationCategory(item.event,final),record=category==='tools'||category==='process';if(record)count++;
   const visible=item.searchMatch||conversationEventVisible(category,conversationFilter)||(record&&compact);
   if(!visible){turn.hidden++;continue}
   if(record)records.push(item);
   else {const node=conversationEventNode(item);node.dataset.category=category;node.classList.toggle('error',category==='error');if(item.searchMatch)node.dataset.searchMatch='true';(item.event.kind==='user'?inputs:outputs).push(node)}
  }
  const body:HTMLElement[]=[];
  if(turn.process.open){
   const start=Math.max(0,records.length-turn.limit);
   if(start){const text='加载更早的 '+Math.min(start,conversationPageSize)+' 条记录（还有 '+start+' 条）';if(turn.more.textContent!==text)turn.more.textContent=text;body.push(turn.more)}
   records.forEach((item,index)=>{if(index<start&&!item.searchMatch)return;const node=conversationEventNode(item);node.dataset.category=conversationCategory(item.event,final);if(item.searchMatch)node.dataset.searchMatch='true';body.push(node)});
  }
  reconcileConversationNodes(turn.input,inputs);reconcileConversationNodes(turn.output,outputs);reconcileConversationNodes(turn.body,body);
  if(turn.root.parentElement!==container)container.append(turn.root);
  turn.process.classList.toggle('hidden',count===0);
  const label=waiting?'等待你处理':run?.status==='running'?'正在执行':run?.status==='queued'?'排队中':'执行记录';
  const text=label+' · '+count+' 条';
  if(turn.summary.textContent!==text)turn.summary.textContent=text;
  const footer=run?runFooter(run):'';if(turn.footer.innerHTML!==footer)turn.footer.innerHTML=footer;
  const files=(run?.attachments||[]).map(f=>`<a href="/api/tasks/${encodeURIComponent(chosen)}/attachments/${encodeURIComponent(f.id)}" class="attachment-chip" download="${escapeHTML(f.name)}">↧ ${escapeHTML(f.name)}</a>`).join('');if(turn.files.innerHTML!==files)turn.files.innerHTML=files;
  turn.dirty=false;turn.state=state;turn.result=run?.result||'';
 }
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
