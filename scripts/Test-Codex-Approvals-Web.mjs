import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import assert from 'node:assert/strict';

// A deliberately small DOM fixture: no browser service, CLI, account or model is started.
class Node {
 constructor(tag='div'){this.tagName=tag.toUpperCase();this.children=[];this.parentElement=null;this.attributes={};this.dataset={};this.className='';this.value='';this.disabled=false;this.checked=false;this._text='';this._html='';this.classList={contains:value=>this.className.split(/\s+/).includes(value),add:(...values)=>{this.className=[...new Set([...this.className.split(/\s+/).filter(Boolean),...values])].join(' ')},remove:(...values)=>{this.className=this.className.split(/\s+/).filter(value=>!values.includes(value)).join(' ')},toggle:(value,on)=>{on??=!this.classList.contains(value);if(on)this.classList.add(value);else this.classList.remove(value);return on}}}
 setAttribute(name,value){this.attributes[name]=String(value);if(name==='class')this.className=String(value);if(name==='id')this.id=String(value);if(name.startsWith('data-'))this.dataset[name.slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())]=String(value)}
 getAttribute(name){if(name==='class')return this.className;if(name==='id')return this.id;if(name.startsWith('data-'))return this.dataset[name.slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())];return this.attributes[name]}
 append(...nodes){for(const node of nodes){node.remove();node.parentElement=this;this.children.push(node)}}
 before(node){const parent=this.parentElement;if(!parent)return;node.remove();const index=parent.children.indexOf(this);parent.children.splice(index,0,node);node.parentElement=parent}
 remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(node=>node!==this);this.parentElement=null}
 replaceChildren(...nodes){for(const child of this.children)child.parentElement=null;this.children=[];this._text='';this.append(...nodes)}
 get childElementCount(){return this.children.length}
 set textContent(value){this.replaceChildren();this._text=String(value)}
 get textContent(){return this._text+this.children.map(node=>node.textContent).join('')}
 set innerHTML(html){this.replaceChildren();this._html=html;const stack=[this];for(const token of html.matchAll(/<(\/?)([\w-]+)([^>]*)>|([^<]+)/g)){if(token[4]){stack.at(-1)._text+=token[4];continue}if(token[1]){stack.pop();continue}const node=new Node(token[2]);for(const attr of token[3].matchAll(/([\w-]+)(?:="([^"]*)")?/g))node.setAttribute(attr[1],attr[2]||'');stack.at(-1).append(node);if(!['INPUT','BR','HR'].includes(node.tagName))stack.push(node)}}
 get innerHTML(){return this._html}
 matches(selector){if(selector.endsWith(':checked'))return this.checked&&this.matches(selector.slice(0,-8));if(selector.startsWith('.'))return this.classList.contains(selector.slice(1));if(selector.startsWith('#'))return this.id===selector.slice(1);if(selector.startsWith('[')){const match=selector.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);return !!match&&(match[2]===undefined?this.getAttribute(match[1])!==undefined:this.getAttribute(match[1])===match[2])}return this.tagName===selector.toUpperCase()}
 querySelectorAll(selector){const matches=(node,parts)=>{if(!node.matches(parts.at(-1)))return false;if(parts.length===1)return true;for(let parent=node.parentElement;parent;parent=parent.parentElement)if(matches(parent,parts.slice(0,-1)))return true;return false};const selectors=selector.split(',').map(s=>s.trim().split(/\s+/));const found=[];const visit=node=>{for(const child of node.children){if(selectors.some(parts=>matches(child,parts)))found.push(child);visit(child)}};visit(this);return found}
 querySelector(selector){return this.querySelectorAll(selector)[0]||null}
 focus(){document.activeElement=this}
}
const root=new Node('main'),workspace=new Node();workspace.id='workspace';root.append(workspace);
globalThis.document={createElement:tag=>new Node(tag),getElementById:id=>root.querySelector('#'+id),activeElement:null};
globalThis.element=id=>document.getElementById(id);
globalThis.input=globalThis.element;
globalThis.button=globalThis.element;
globalThis.escapeHTML=s=>s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
Object.assign(globalThis,{chosen:'task',selection:1,sequence:0,authenticated:true,creatingTask:false,stoppingTask:'',detail:null});
const source=await readFile(new URL('../web/codex-approvals.ts',import.meta.url),'utf8');
const exports='installCodexApprovals,resetCodexApprovals,renderCodexApprovals,codexApprovalCards,codexApprovalKey,codexApprovalDecisions,codexDecisionBody,codexQuestions,codexAnswersBody,codexApprovalFieldsHTML,submitCodexApproval,refreshCodexApprovals,stopCodexApprovalRun';
const ui=await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source,{mode:'transform'})+'\nexport {'+exports+'};').toString('base64'));
globalThis.renderTask=()=>ui.renderCodexApprovals(detail);
globalThis.renderWorkflow=()=>ui.renderCodexApprovals(detail);
let eventsAppended=[];globalThis.appendEvents=events=>eventsAppended.push(...events);
const task={id:'task',workspace:'/home/test/project',engine:'codex',environment:{name:'开发 WSL',type:'wsl',distro:'Ubuntu',user:'test'}};
const make=(method,params={},id='approval')=>({id,task_id:'task',run_id:'run',created:123,method,params});
const command=make('item/commandExecution/requestApproval',{command:'curl https://example.test/<script>',cwd:'/work/<img>',reason:'访问网络 & 下载',networkApprovalContext:{host:'example.test',protocol:'https'},additionalPermissions:{fileSystem:{write:['/tmp/result']}}});
const file=make('item/fileChange/requestApproval',{grantRoot:'/tmp/notes',reason:'保存文档'});
const permissions=make('item/permissions/requestApproval',{permissions:{network:{enabled:true},fileSystem:{read:['/etc/project'],write:['/tmp/project']}}});
const question=make('item/tool/requestUserInput',{questions:[{id:'flavor',header:'方案',question:'选择方案',isOther:true,isSecret:false,options:[{label:'简单',description:'先完成最小流程'}]},{id:'token',header:'口令',question:'输入测试口令',isOther:false,isSecret:true,options:null}]});
const snapshot=(requests=[])=>({task,runs:[{id:'run',status:'running'}],events:[],approvals:requests});

assert.deepEqual(ui.codexApprovalDecisions(command),['accept','decline','cancel']);
assert.deepEqual(ui.codexApprovalDecisions(file),['accept','decline','cancel']);
assert.deepEqual(ui.codexApprovalDecisions(permissions),['accept','decline']);
assert.deepEqual(ui.codexApprovalDecisions(question),[]);
assert.deepEqual(ui.codexApprovalDecisions(make('mcp/elicitation/request')),[]);
assert.deepEqual(ui.codexApprovalDecisions({...command,params:{availableDecisions:['decline','acceptForSession',{acceptWithExecpolicyAmendment:{}}]}}),['decline']);
for(const request of [command,file,permissions]){assert.deepEqual(ui.codexDecisionBody(request,'accept'),{decision:'accept'});assert.throws(()=>ui.codexDecisionBody(request,'acceptForSession'))}
assert.throws(()=>ui.codexDecisionBody(permissions,'cancel'));
const escaped=ui.codexApprovalFieldsHTML(command,task);assert.match(escaped,/Ubuntu/);assert.match(escaped,/example\.test/);assert.match(escaped,/\/tmp\/result/);assert.match(escaped,/&lt;script&gt;/);assert.doesNotMatch(escaped,/<script>|<img>/);
assert.match(ui.codexApprovalFieldsHTML(file,task),/\/tmp\/notes/);assert.match(ui.codexApprovalFieldsHTML(permissions,task),/\/etc\/project/);
const answerBody=ui.codexAnswersBody(question,(_q,index)=>[index?'test secret':'简单']);assert.equal(answerBody.answers.flavor.answers[0],'简单');assert.equal(answerBody.answers.token.answers[0],'test secret');assert.throws(()=>ui.codexAnswersBody(question,()=>[]));
assert.equal(ui.codexAnswersBody(question,(_q,index)=>[index?' secret with spaces ':'简单']).answers.token.answers[0],' secret with spaces ','secret answers preserve intentional whitespace');
assert.throws(()=>ui.codexQuestions({...question,params:{questions:[{id:'a'},{id:'a'}]}}));
const proto=make('item/tool/requestUserInput',{questions:[{id:'__proto__',question:'文本'}]});assert.equal(JSON.parse(JSON.stringify(ui.codexAnswersBody(proto,()=>['value']))).answers.__proto__.answers[0],'value');
const constrained=make('item/tool/requestUserInput',{questions:[{id:'choice',isOther:false,options:[{label:'A'}]}]});assert.throws(()=>ui.codexAnswersBody(constrained,()=>['unexpected']));

ui.installCodexApprovals();assert.equal(element('codex-approvals').parentElement,root);assert.equal(root.children[0],element('codex-approvals'),'approval area stays outside the conversation and mobile/fullscreen tool dock');
detail=snapshot([question]);ui.renderCodexApprovals(detail);
let card=ui.codexApprovalCards.get(ui.codexApprovalKey(question));const answerInput=card.node.querySelector('[data-answer="free"]');answerInput.value='还在写的答案';answerInput.focus();
const secret=card.node.querySelectorAll('[data-answer="free"]')[1];assert.equal(secret.type,'password');assert.equal(secret.autocomplete,'off');
detail=JSON.parse(JSON.stringify(detail));ui.renderCodexApprovals(detail);assert.equal(ui.codexApprovalCards.get(ui.codexApprovalKey(question)),card);assert.equal(document.activeElement,answerInput);assert.equal(answerInput.value,'还在写的答案','poll must not rebuild inputs or discard unsent answers');
assert.equal(element('codex-approvals').classList.contains('hidden'),false);
creatingTask=true;ui.renderCodexApprovals(detail);assert.equal(element('codex-approvals').classList.contains('hidden'),true);creatingTask=false;
detail=snapshot([command]);ui.renderCodexApprovals(detail);card=ui.codexApprovalCards.get(ui.codexApprovalKey(command));
let completePost,posts=0,gets=0;
globalThis.api=async(path,method,body)=>{if(method==='POST'){posts++;assert.equal(path,'tasks/task/approvals/approval');assert.deepEqual(body,{decision:'accept'});await new Promise(resolve=>completePost=resolve);return {ok:true}}gets++;return snapshot([])};
const sending=ui.submitCodexApproval(card,{decision:'accept'});assert.equal(card.busy,true);assert(card.node.querySelectorAll('.codex-approval-actions button').every(button=>button.disabled));
await ui.submitCodexApproval(card,{decision:'accept'});assert.equal(posts,1,'repeated clicks must not send another decision');completePost();await sending;assert.equal(gets,1);assert.equal(ui.codexApprovalCards.size,0,'resolved request disappears only after the detail refresh');assert.equal(element('codex-approvals').classList.contains('hidden'),true);

detail=snapshot([command]);ui.renderCodexApprovals(detail);card=ui.codexApprovalCards.get(ui.codexApprovalKey(command));gets=0;posts=0;
globalThis.api=async(_path,method)=>{if(method==='POST'){posts++;throw Object.assign(new Error('expired'),{status:409})}gets++;return snapshot([])};
await ui.submitCodexApproval(card,{decision:'decline'});assert.equal(posts,1);assert.equal(gets,1,'409 refreshes the authoritative pending list');assert.equal(card.settled,true);assert.equal(ui.codexApprovalCards.size,0);

detail=snapshot([file]);ui.renderCodexApprovals(detail);card=ui.codexApprovalCards.get(ui.codexApprovalKey(file));
globalThis.api=async()=>{throw new Error('网络中断')};await ui.submitCodexApproval(card,{decision:'cancel'});assert.equal(card.busy,false);assert.equal(card.settled,false);assert.match(card.error,/网络中断/);assert(card.node.querySelectorAll('.codex-approval-actions button').every(button=>!button.disabled),'transient failure permits an explicit retry');
globalThis.api=async(_path,method)=>{if(method==='POST')return {ok:true};throw new Error('同步中断')};await ui.submitCodexApproval(card,{decision:'cancel'});assert.equal(card.settled,true);assert.match(card.error,/同步失败/);assert(card.node.querySelectorAll('.codex-approval-actions button').every(button=>button.disabled),'accepted-but-unrefreshed decision cannot be submitted twice');
globalThis.api=async()=>snapshot([]);await ui.refreshCodexApprovals('task');assert.equal(ui.codexApprovalCards.size,0);

detail=snapshot([question]);ui.renderCodexApprovals(detail);card=ui.codexApprovalCards.get(ui.codexApprovalKey(question));posts=0;globalThis.api=async(path,method)=>{if(method==='POST'){posts++;assert.equal(path,'tasks/task/stop');return {ok:true}}return snapshot([])};
await ui.stopCodexApprovalRun(card);assert.equal(posts,1);assert.equal(ui.codexApprovalCards.size,0);stoppingTask='';
detail=snapshot([command]);ui.renderCodexApprovals(detail);card=ui.codexApprovalCards.get(ui.codexApprovalKey(command));detail=snapshot([]);posts=0;globalThis.api=async()=>{posts++;return {}};await ui.submitCodexApproval(card,{decision:'accept'});await ui.stopCodexApprovalRun(card);assert.equal(posts,0,'stale cards cannot submit or stop after the authoritative request disappears');

let resolveOld,resolveNew;detail=snapshot([command]);globalThis.api=()=>new Promise(resolve=>{if(!resolveOld)resolveOld=resolve;else resolveNew=resolve});const older=ui.refreshCodexApprovals('task'),newer=ui.refreshCodexApprovals('task');resolveNew(snapshot([]));await newer;resolveOld(snapshot([command]));await older;assert.equal(detail.approvals.length,0,'older detail response cannot resurrect a resolved request');
ui.resetCodexApprovals();assert.equal(ui.codexApprovalCards.size,0);

const workflowSource=await readFile(new URL('../web/workflow.ts',import.meta.url),'utf8');
assert.match(workflowSource,/Claude Code 当前不支持 Codex 网页交互审批或自动风险评审/);assert.match(workflowSource,/id="mode-engine-hint"/);
const workflow=await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(workflowSource,{mode:'transform'})+'\nexport {modeForPermission,modeLabel,modeSupportsEngine};export function setCatalog(value){workCatalog=value}').toString('base64'));
const modes=[{id:'work',permission:'workspace',approval:'request',allow_network:true,name:'工作'},{id:'codex:auto',permission:'workspace',approval:'auto',allow_network:true,name:'自动'},{id:'plan',permission:'read',approval:'never',name:'规划'},{id:'full',permission:'full',approval:'never',name:'完全访问'}];workflow.setCatalog({modes,commands:[]});
for(const [permission,id] of [['request','work'],['auto','codex:auto'],['read','plan'],['full','full']])assert.equal(workflow.modeForPermission(permission).id,id);
assert.equal(workflow.modeSupportsEngine(modes[1],'claude'),false);assert.equal(workflow.modeSupportsEngine(modes[0],'claude'),true);assert.match(workflow.modeLabel(modes[1]),/Codex 自动风险评审/);
workflow.setCatalog({modes:[modes[2]],commands:[]});assert.equal(workflow.modeForPermission('request'),undefined,'request mode must never silently map to read');
const appSource=await readFile(new URL('../web/app.ts',import.meta.url),'utf8');const buildSource=await readFile(new URL('./build.mjs',import.meta.url),'utf8');
assert.match(appSource,/approvalRevision!==codexApprovalRevision/);assert.match(appSource,/status:response\.status/);assert.match(buildSource,/codex-approvals\.ts/);assert.doesNotMatch(source,/localStorage|sessionStorage/,'question answers, including secrets, are not cached on disk');
console.log('PASS: native approval decisions, scoped permissions, escaped request details, structured answers, mobile-visible placement, stable input focus, duplicate-submit lock, cancellation, 409 refresh, stale response guards, and Codex-only mode mapping.');
