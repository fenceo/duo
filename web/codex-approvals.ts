type CodexPendingRequest={id:string;task_id:string;run_id:string;method:string;params:unknown;created:number};
type CodexApprovalDecision='accept'|'decline'|'cancel';
type CodexQuestion={id:string;header:string;question:string;isOther:boolean;isSecret:boolean;options:{label:string;description:string}[]};
type CodexApprovalCard={request:CodexPendingRequest;node:HTMLElement;busy:boolean;settled:boolean;error:string};
const codexApprovalCards=new Map<string,CodexApprovalCard>();
let codexApprovalRevision=0;
function codexRecord(value:unknown):Record<string,unknown>{return value!==null&&typeof value==='object'&&!Array.isArray(value)?value as Record<string,unknown>:{}}
function codexText(value:unknown):string{return typeof value==='string'?value:''}
function codexPretty(value:unknown):string{return value===undefined||value===null?'':typeof value==='string'?value:JSON.stringify(value,null,2)}
function codexApprovalKey(request:CodexPendingRequest){return JSON.stringify([request.task_id,request.run_id,request.id])}
function codexIsQuestion(request:CodexPendingRequest){return request.method==='item/tool/requestUserInput'||request.method==='duo/asyncQuestion'}
function codexApprovalKind(request:CodexPendingRequest){return ({'item/commandExecution/requestApproval':'命令执行','item/fileChange/requestApproval':'文件修改','item/permissions/requestApproval':'临时权限','item/tool/requestUserInput':'请你选择或补充','duo/asyncQuestion':'请你选择或补充'} as Record<string,string>)[request.method]||'暂不支持的请求'}
function codexApprovalDecisions(request:CodexPendingRequest):CodexApprovalDecision[]{
 if(request.method==='item/permissions/requestApproval')return ['accept','decline'];
 if(!['item/commandExecution/requestApproval','item/fileChange/requestApproval'].includes(request.method))return [];
 const offered=codexRecord(request.params).availableDecisions;
 return (['accept','decline','cancel'] as const).filter(value=>!Array.isArray(offered)||offered.includes(value));
}
function codexDecisionBody(request:CodexPendingRequest,decision:CodexApprovalDecision){
 if(!codexApprovalDecisions(request).includes(decision))throw new Error('此请求不支持该操作');
 return {decision};
}
function codexQuestions(request:CodexPendingRequest):CodexQuestion[]{
 const values=codexRecord(request.params).questions;if(!Array.isArray(values))return [];
 const ids=new Set<string>();
 return values.map(value=>{const q=codexRecord(value),id=codexText(q.id);if(!id||ids.has(id))throw new Error('原生问题缺少唯一标识，无法安全提交');ids.add(id);return {id,header:codexText(q.header),question:codexText(q.question),isOther:q.isOther===true,isSecret:q.isSecret===true,options:Array.isArray(q.options)?q.options.map(option=>{const o=codexRecord(option);return {label:codexText(o.label),description:codexText(o.description)}}).filter(o=>o.label):[]}});
}
function codexAnswersBody(request:CodexPendingRequest,read:(question:CodexQuestion,index:number)=>string[]){
 if(!codexIsQuestion(request))throw new Error('此请求不是问题表单');
 const questions=codexQuestions(request);if(!questions.length)throw new Error('请求未提供可回答的问题，请停止本轮后重试');
 const answers:Record<string,{answers:string[]}>=Object.create(null);
 questions.forEach((question,index)=>{const values=read(question,index).map(s=>question.isSecret?s:s.trim()).filter(s=>s.trim());if(!values.length)throw new Error('请回答：'+(question.header||question.question||question.id));if(values.some(v=>v.length>16000))throw new Error('答案过长，请缩短后重试');if(question.options.length&&!question.isOther&&values.some(v=>!question.options.some(o=>o.label===v)))throw new Error('请选择请求提供的答案');answers[question.id]={answers:values}});
 return {answers};
}
function codexApprovalFields(request:CodexPendingRequest,task:Task):{label:string;value:string}[]{
 const params=codexRecord(request.params),env=task.environment;
 const fields=[{label:'执行环境',value:[env?.name,env?.type?.toUpperCase(),env?.distro,env?.host,env?.user].filter(Boolean).join(' · ')||'未提供'},{label:'工作目录',value:codexText(params.cwd)||task.workspace||'未提供'},{label:'原因',value:codexText(params.reason)||'原生请求未提供原因'}];
 const add=(label:string,value:unknown)=>{const text=codexPretty(value);if(text)fields.push({label,value:text})};
 if(request.method==='item/commandExecution/requestApproval')add('命令',params.command??'原生请求未提供命令');
 const network=codexRecord(params.networkApprovalContext);if(network.host)add('网络目标',[codexText(network.protocol),codexText(network.host)].filter(Boolean).join(' · '));
 if(params.grantRoot)add('申请授权路径',params.grantRoot);
 if(params.changes)add('申请修改路径 / 内容',params.changes);
 if(params.commandActions)add('命令解析',params.commandActions);
 if(params.additionalPermissions)add('附加权限',params.additionalPermissions);
 if(params.permissions)add('本轮申请的权限',params.permissions);
 if(request.method==='item/fileChange/requestApproval'&&!params.changes)add('文件范围','此原生请求未提供逐文件差异；请结合执行记录及申请授权路径核对。');
 return fields;
}
function codexApprovalFieldsHTML(request:CodexPendingRequest,task:Task){return codexApprovalFields(request,task).map(field=>`<div class="codex-approval-field"><dt>${escapeHTML(field.label)}</dt><dd><pre>${escapeHTML(field.value)}</pre></dd></div>`).join('')}
function installCodexApprovals(){
 const workspace=element('workspace');
 const panel=document.createElement('section');panel.id='codex-approvals';panel.className='codex-approvals hidden';panel.setAttribute('aria-label','Codex 待处理请求');
 panel.innerHTML='<div class="codex-approvals-heading"><strong id="codex-approvals-title" role="status"></strong><span id="codex-approvals-hint"></span><button type="button" id="codex-approvals-expand" aria-haspopup="dialog">放大</button></div><div id="codex-approval-list"></div>';
 element('composer-wrap').before(panel);
 const dialog=document.createElement('dialog');dialog.id='codex-requests-dialog';dialog.className='codex-requests-dialog';dialog.setAttribute('aria-labelledby','codex-approvals-title');workspace.before(dialog);
 const expand=panel.querySelector<HTMLButtonElement>('#codex-approvals-expand')!;
 dialog.addEventListener('close',()=>{if(dialog.open)return;positionCodexApprovals();expand.textContent='放大'});
 expand.onclick=()=>{if(dialog.open){dialog.close();return}dialog.append(panel);expand.textContent='收起';dialog.showModal()};
 const observer=new MutationObserver(positionCodexApprovals);for(const node of [workspace,element('composer-wrap')])observer.observe(node,{attributes:true,attributeFilter:['class']});disposeWithShell(()=>observer.disconnect());
 listenWithShell(window,'resize',positionCodexApprovals);
 positionCodexApprovals();
}
function positionCodexApprovals(){
 const panel=element('codex-approvals'),workspace=element('workspace'),composer=element('composer-wrap');
 if(!panel||!workspace||!composer||element<HTMLDialogElement>('codex-requests-dialog')?.open)return;
 const hiddenChat=workspace.classList.contains('tool-full')||(workspace.classList.contains('tool-open')&&matchMedia('(max-width:760px)').matches);
 const besideComposer=!hiddenChat&&!composer.classList.contains('hidden');
 if(besideComposer?panel.nextElementSibling===composer:workspace.nextElementSibling===panel)return;
 const active=document.activeElement as HTMLElement|null,focus=active&&panel.contains(active)?active:null;
 // Keep the same form nodes, including unsubmitted answers, when changing layout.
 if(besideComposer)composer.before(panel);else workspace.after(panel);
 focus?.focus({preventScroll:true});
}
function resetCodexApprovals(){codexApprovalRevision++;codexApprovalCards.clear();const dialog=element<HTMLDialogElement>('codex-requests-dialog');if(dialog?.open)dialog.close();element('codex-approval-list')?.replaceChildren();element('codex-approvals')?.classList.add('hidden')}
function createCodexApprovalCard(request:CodexPendingRequest,task:Task):CodexApprovalCard{
 const node=document.createElement('article');node.className='codex-approval-card';node.dataset.request=request.id;
 const questionRequest=codexIsQuestion(request),asyncQuestion=request.method==='duo/asyncQuestion',background=questionRequest&&codexRecord(request.params).isBlocking===false;
 node.classList.toggle('codex-question-card',questionRequest);
 const scope=asyncQuestion?'AI 可以继续工作；答案将作为下一条消息排队发送。':questionRequest?(background?'AI 可以继续工作；提交后会收到你的补充。':'AI 正在等你回答。选择选项或填写答案后，点击提交。'):request.method==='item/permissions/requestApproval'?'仅授予本次请求列出的权限，有效范围为当前轮次。':'批准仅针对当前请求；不会改写会话的审批模式。';
 node.innerHTML=`<h3>${escapeHTML(codexApprovalKind(request))}</h3>${questionRequest?'':`<dl class="codex-approval-fields">${codexApprovalFieldsHTML(request,task)}</dl>`}<p class="codex-approval-scope">${scope}</p><div class="codex-approval-questions"></div><div class="codex-approval-actions"></div><p class="codex-approval-status" role="status"></p>`;
 const card:CodexApprovalCard={request,node,busy:false,settled:false,error:''},actions=node.querySelector<HTMLElement>('.codex-approval-actions')!;
 if(questionRequest){
  try{
   const questions=codexQuestions(request),target=node.querySelector<HTMLElement>('.codex-approval-questions')!;
   questions.forEach((question,index)=>{
    const field=document.createElement('fieldset');field.dataset.question=String(index);const legend=document.createElement('legend');legend.textContent=question.header||question.id;field.append(legend);
    const prompt=document.createElement('p');prompt.textContent=question.question;field.append(prompt);
    question.options.forEach(option=>{const label=document.createElement('label');label.className='codex-answer-option';const control=document.createElement('input');control.type='radio';control.name=codexApprovalKey(request)+'-'+index;control.value=option.label;control.dataset.answer='option';const text=document.createElement('span');text.textContent=option.label+(option.description?' — '+option.description:'');label.append(control,text);field.append(label)});
    if(question.isOther||!question.options.length){const label=document.createElement('label');label.textContent=question.options.length?'其他答案（填写后优先提交）':'你的答案';const control=document.createElement('input');control.type=question.isSecret?'password':'text';control.autocomplete='off';control.maxLength=16000;control.dataset.answer='free';control.setAttribute('aria-label',question.header||question.question||question.id);label.append(control);field.append(label)}
    target.append(field);
   });
   if(questions.length)appendCodexAction(actions,'提交答案','primary',()=>{try{const body=codexAnswersBody(request,(_question,index)=>{const field=target.querySelector<HTMLElement>(`[data-question="${index}"]`)!,free=field.querySelector<HTMLInputElement>('[data-answer="free"]')?.value,option=field.querySelector<HTMLInputElement>('[data-answer="option"]:checked')?.value;return free?.trim()?[free]:option?[option]:[]});void submitCodexApproval(card,body)}catch(error){card.error=(error as Error).message;updateCodexApprovalCard(card)}});
   else card.error='原生请求未提供可回答的问题，请停止本轮后重试。';
  }catch(error){card.error=(error as Error).message}
  if(asyncQuestion)appendCodexAction(actions,'暂不回答','',()=>void submitCodexApproval(card,{dismiss:true}));
  else appendCodexAction(actions,'停止本轮','',()=>void stopCodexApprovalRun(card));
 }else{
  for(const decision of codexApprovalDecisions(request))appendCodexAction(actions,({accept:'批准本次',decline:'拒绝本次',cancel:'取消本轮'} as const)[decision],decision==='accept'?'primary':'',()=>void submitCodexApproval(card,codexDecisionBody(request,decision)));
  if(!actions.childElementCount){card.error='暂不支持此请求，不能在此页面授权；请停止本轮。';appendCodexAction(actions,'停止本轮','',()=>void stopCodexApprovalRun(card))}
 }
 const refresh=document.createElement('button');refresh.type='button';refresh.className='codex-approval-refresh subtle';refresh.textContent='刷新请求';refresh.onclick=()=>void refreshCodexApprovals(request.task_id).catch(error=>{card.error=(error as Error).message;updateCodexApprovalCard(card)});node.append(refresh);
 return card;
}
function appendCodexAction(target:HTMLElement,label:string,className:string,action:()=>void){const control=document.createElement('button');control.type='button';control.className=className;control.textContent=label;control.onclick=action;target.append(control)}
function updateCodexApprovalCard(card:CodexApprovalCard){
 const disabled=card.busy||card.settled||stoppingTask===card.request.task_id;
 card.node.setAttribute('aria-busy',String(card.busy));card.node.querySelectorAll<HTMLInputElement|HTMLButtonElement>('.codex-approval-actions button,.codex-approval-questions input').forEach(control=>control.disabled=disabled);
 const status=card.node.querySelector<HTMLElement>('.codex-approval-status')!,message=card.error||(card.busy?'正在提交…':card.settled?'已提交或请求已过期，正在同步最新状态…':stoppingTask===card.request.task_id?'正在停止本轮…':'等待你处理');
 if(status.textContent!==message)status.textContent=message;status.classList.toggle('error',!!card.error);
}
function renderCodexApprovals(current:Detail|null){
 const panel=element('codex-approvals'),list=element('codex-approval-list');if(!panel||!list)return;
 const pending=current&&!creatingTask&&current.task.id===chosen?(current.approvals||[]).filter(request=>request.task_id===current.task.id):[],keys=new Set(pending.map(codexApprovalKey));
 const dialog=element<HTMLDialogElement>('codex-requests-dialog');if(!pending.length&&dialog?.open)dialog.close();
 for(const [key,card] of codexApprovalCards)if(!keys.has(key)){card.node.remove();codexApprovalCards.delete(key)}
 for(const request of pending){const key=codexApprovalKey(request);let card=codexApprovalCards.get(key);if(!card){card=createCodexApprovalCard(request,current!.task);codexApprovalCards.set(key,card);list.append(card.node)}updateCodexApprovalCard(card)}
 panel.classList.toggle('hidden',!pending.length);const questions=pending.filter(codexIsQuestion),onlyQuestions=questions.length===pending.length;
 const heading=element('codex-approvals-title'),title=(onlyQuestions?'Codex 有问题需要你回答':'Codex 等待处理')+' · '+pending.length;if(heading.textContent!==title)heading.textContent=title;
 const hint=element('codex-approvals-hint'),hintText=onlyQuestions?'答案会直接交回当前 Codex 会话':'批准只针对当前请求，不创建永久授权规则';if(hint.textContent!==hintText)hint.textContent=hintText;
 positionCodexApprovals();
}
async function refreshCodexApprovals(taskID:string){
 if(taskID!==chosen||!authenticated)return;const token=selection,revision=++codexApprovalRevision;
 const fresh=await api<Detail>('tasks/'+encodeURIComponent(taskID)+'?after='+sequence);
 if(taskID!==chosen||token!==selection||revision!==codexApprovalRevision)return;
 detail=fresh;appendEvents(fresh.events);renderTask();
}
async function submitCodexApproval(card:CodexApprovalCard,body:unknown){
 if(card.busy||card.settled||stoppingTask===card.request.task_id||!detail?.approvals?.some(request=>codexApprovalKey(request)===codexApprovalKey(card.request)))return;
 card.busy=true;card.error='';codexApprovalRevision++;updateCodexApprovalCard(card);
 try{await api('tasks/'+encodeURIComponent(card.request.task_id)+(card.request.method==='duo/asyncQuestion'?'/questions/':'/approvals/')+encodeURIComponent(card.request.id),'POST',body);card.settled=true}
 catch(error){if((error as Error&{status?:number}).status===409){card.settled=true;card.error='请求已过期或已在其他窗口处理，正在刷新。'}else card.error=(error as Error).message}
 finally{card.busy=false;updateCodexApprovalCard(card)}
 if(card.settled)try{await refreshCodexApprovals(card.request.task_id)}catch(error){card.error='同步失败，请刷新请求：'+(error as Error).message;updateCodexApprovalCard(card)}
}
async function stopCodexApprovalRun(card:CodexApprovalCard){
 if(card.busy||card.settled||stoppingTask===card.request.task_id||!detail?.approvals?.some(request=>codexApprovalKey(request)===codexApprovalKey(card.request)))return;card.busy=true;stoppingTask=card.request.task_id;renderWorkflow();updateCodexApprovalCard(card);
 try{await api('tasks/'+encodeURIComponent(card.request.task_id)+'/stop','POST',{});card.settled=true;await refreshCodexApprovals(card.request.task_id)}catch(error){card.error=(error as Error).message;stoppingTask=''}finally{card.busy=false;updateCodexApprovalCard(card);if(detail?.task.id===card.request.task_id)renderWorkflow()}
}
