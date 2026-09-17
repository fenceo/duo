type ConversationFilter={tools:boolean;process:boolean};
// Defined in app.ts. Guarded so this file also loads standalone in tests.
declare function knowledgeForRun(runId:string):unknown;
type ConversationCategory='message'|'tools'|'process'|'error';
const conversationFilterKey='jianzuo-conversation-filter-v1';
let conversationFilter:ConversationFilter={tools:false,process:false};
let conversationItems:{event:EventRecord;node:HTMLElement}[]=[];
type ConversationTurn={root:HTMLElement;input:HTMLElement;process:HTMLDetailsElement;summary:HTMLElement;body:HTMLElement;output:HTMLElement;files:HTMLElement;footer:HTMLElement};
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
function resetConversation(){conversationItems=[];conversationTurns.clear()}
function conversationTurn(id:string):ConversationTurn{
 const existing=conversationTurns.get(id);if(existing)return existing;
 const root=document.createElement('section');root.className='conversation-turn';root.dataset.run=id;
 root.innerHTML='<div class="turn-input"></div><div class="turn-attachments"></div><details class="turn-process"><summary></summary><div class="turn-records"></div></details><div class="turn-output"></div><footer class="turn-footer"></footer>';
 const turn={root,input:root.children[0] as HTMLElement,process:root.querySelector<HTMLDetailsElement>('details')!,summary:root.querySelector('summary')!,body:root.querySelector<HTMLElement>('.turn-records')!,output:root.querySelector<HTMLElement>('.turn-output')!,files:root.querySelector<HTMLElement>('.turn-attachments')!,footer:root.querySelector<HTMLElement>('.turn-footer')!};
 turn.process.open=conversationFilter.tools||conversationFilter.process;conversationTurns.set(id,turn);element('conversation').append(root);return turn;
}
function installConversationFilter(){
 try{conversationFilter=readConversationFilter(localStorage.getItem(conversationFilterKey))}catch{}
 element('conversation').insertAdjacentHTML('beforebegin',`<div id="conversation-filter" class="conversation-filter hidden" role="group" aria-label="对话显示"><div class="filter-presets"><button id="conversation-results" title="折叠每轮执行过程，保留回复和错误">对话</button><button id="conversation-all" title="展开全部执行记录">轨迹</button></div><details class="display-menu"><summary>筛选</summary><div><label><input type="checkbox" id="conversation-tools">命令与工具</label><label><input type="checkbox" id="conversation-process">中间过程</label><small>仅改变显示，记录完整保留</small></div></details><small id="conversation-hidden"></small></div>`);
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
function applyConversationFilter(){
 const container=element('conversation');if(!container)return;
 const nearBottom=container.scrollHeight-container.scrollTop-container.clientHeight<100,top=container.scrollTop;
 const final=finalConversationEvents(conversationItems.map(item=>item.event),detail?.runs||[]),counts=new Map<string,number>();let hidden=0;
 const compact=!conversationFilter.tools&&!conversationFilter.process;
 for(const {event,node} of conversationItems){
  const category=conversationCategory(event,final),record=category==='tools'||category==='process';
  const turn=event.run_id?conversationTurn(event.run_id):null;
  // In conversation mode the whole turn is collapsed, but opening its summary
  // reveals its complete record without changing the global display preference.
  const visible=node.dataset.searchMatch==='true'||conversationEventVisible(category,conversationFilter)||(!!turn&&compact);
  node.dataset.category=category;node.classList.toggle('hidden',!visible);node.classList.toggle('error',category==='error');if(!visible)hidden++;
  if(turn){const parent=record?turn.body:event.kind==='user'?turn.input:turn.output;if(node.parentElement!==parent)parent.append(node);if(record)counts.set(event.run_id,(counts.get(event.run_id)||0)+1);if(node.dataset.searchMatch==='true')turn.process.open=true}
 }
 for(const [id,turn] of conversationTurns){
  const count=counts.get(id)||0,run=detail?.runs.find(r=>r.id===id);turn.process.classList.toggle('hidden',count===0);
  const label=run?.status==='running'?'正在执行':run?.status==='queued'?'排队中':'执行记录';
  const text=label+' · '+count+' 条';
  if(turn.summary.textContent!==text)turn.summary.textContent=text;
  const footer=run?runFooter(run):'';if(turn.footer.innerHTML!==footer)turn.footer.innerHTML=footer;
  const files=(run?.attachments||[]).map(f=>`<a href="/api/tasks/${encodeURIComponent(chosen)}/attachments/${encodeURIComponent(f.id)}" class="attachment-chip" download="${escapeHTML(f.name)}">↧ ${escapeHTML(f.name)}</a>`).join('');if(turn.files.innerHTML!==files)turn.files.innerHTML=files;
 }
 const counter=element('conversation-hidden');if(counter)counter.textContent=hidden?'已筛除 '+hidden+' 条':'';
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
 const knowledge=runId?`<button type="button" class="run-knowledge" data-knowledge-run="${escapeHTML(runId)}" ${settled?'disabled':''} title="${settled?'这轮结果已经沉淀到任务知识':'把这轮结果存成一条任务知识'}">${settled?'已沉淀':'沉淀为知识'}</button>`:'';
 return `<span title="${escapeHTML(usageTip)}">用量 ${usage?formatTokens(usage.total)+' tok':'未提供'}</span><span title="${durationTip}">用时 ${formatDuration(run.finished-(run.started||run.created))}</span><time title="${escapeHTML(new Date(run.finished).toLocaleString())}">时间 ${new Date(run.finished).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit',hour12:false})}</time>${knowledge}`;
}
