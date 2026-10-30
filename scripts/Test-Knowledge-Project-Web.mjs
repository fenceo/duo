import assert from 'node:assert/strict';
import {runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

// Only production DOM and synthetic in-memory responses. No model or files.
const {ctx,document,requests}=await createWebShellFixture();
const node=id=>document.getElementById(id),run=code=>runInContext(code,ctx);
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve}};
const duo={id:'duo-project',name:'Duo <fixture>',aliases:['jianzuo']},other={id:'other-project',name:'Other',aliases:[]};
const states=new Map([['one',{project:duo,projects:[duo,other]}],['two',{project:null,projects:[duo,other]}],['three',{project:other,projects:[duo,other]}]]),calls=[];
let saveGate=null,loadGate=null,failSave=false;
ctx.api=async(path,method='GET',payload)=>{
 calls.push({path,method,payload});
 const id=path.match(/^tasks\/([^/]+)\/knowledge\/project$/)?.[1];
 assert(id,'Unexpected project fixture request '+path);
 if(method==='PUT'){
  if(failSave)throw Error('synthetic save failure');
  if(saveGate)return saveGate.promise;
  const result={project:payload.project_id?duo:null,projects:[duo,other]};states.set(id,result);return result;
 }
 if(id==='three'&&loadGate)return loadGate.promise;
 return states.get(id);
};
run("tasks=['one','two','three'].map(id=>({id,title:id,status:'idle',archived:false}));chosen='one';selection++;creatingTask=false;detail={task:tasks[0],runs:[],events:[]}");
const choose=id=>{ctx.fixtureProjectTask=id;run("chosen=fixtureProjectTask;selection++;detail={task:tasks.find(task=>task.id===chosen),runs:[],events:[]}")};
assert(!requests.some(request=>request.path.endsWith('/knowledge/project')),'project settings are read only when opened');
const panel=node('task-knowledge-project');panel.open=true;panel.ontoggle();await tick();
assert.equal(calls.length,1);assert.equal(node('task-knowledge-project-select').value,duo.id);
assert.equal(node('task-knowledge-project-select').querySelector('fixture'),null,'project names are escaped');
assert.match(panel.querySelector('summary').textContent,/Duo <fixture>/);

node('task-knowledge-project-select').value='__new__';node('task-knowledge-project-select').onchange();
node('task-knowledge-project-name').value='New Project';node('task-knowledge-project-name').oninput();
node('task-knowledge-project-aliases').value='alias-one，alias-two';node('task-knowledge-project-aliases').oninput();
const beforeDraftSwitch=calls.length;choose('two');await ctx.loadTaskKnowledgeProject();assert.equal(node('task-knowledge-project-name').value,'');
choose('one');await ctx.loadTaskKnowledgeProject();assert.equal(calls.length,beforeDraftSwitch+1,'switching back preserves dirty draft instead of refetching');
assert.equal(node('task-knowledge-project-name').value,'New Project');assert.equal(node('task-knowledge-project-aliases').value,'alias-one，alias-two');
assert(!node('task-knowledge-project-new').classList.contains('hidden'));

saveGate=deferred();const saving=ctx.saveTaskKnowledgeProject();await ctx.saveTaskKnowledgeProject();
const mutations=calls.filter(call=>call.method==='PUT');assert.equal(mutations.length,1,'repeated save clicks submit once');
assert.equal(mutations[0].payload.name,'New Project');assert.deepEqual(Array.from(mutations[0].payload.aliases),['alias-one','alias-two']);
assert(node('task-knowledge-project-save').disabled&&node('task-knowledge-project-name').disabled,'saving locks the form');
choose('two');await ctx.loadTaskKnowledgeProject();saveGate.resolve({project:{id:'new-project',name:'New Project',aliases:['alias-one','alias-two']},projects:[duo,other,{id:'new-project',name:'New Project',aliases:[]}]});await saving;
assert.equal(node('task-knowledge-project-select').value,'','save for a previous task never changes the visible task');
assert(!node('task-knowledge-project-state').textContent.includes('已保存'));
choose('one');await ctx.loadTaskKnowledgeProject();assert.equal(node('task-knowledge-project-select').value,'new-project','saved original task remains available after switching back');saveGate=null;

node('task-knowledge-project-select').value='__new__';node('task-knowledge-project-select').onchange();node('task-knowledge-project-name').value='Retry Draft';node('task-knowledge-project-name').oninput();
failSave=true;await ctx.saveTaskKnowledgeProject();assert.equal(node('task-knowledge-project-name').value,'Retry Draft');assert.match(node('task-knowledge-project-state').textContent,/草稿已保留.*synthetic save failure/);assert(!node('task-knowledge-project-save').disabled);failSave=false;
run("detail.task.status='running'");ctx.renderTaskKnowledgeProject(run('activeTaskKnowledgeProjectDraft()'));const beforeBusy=calls.length;await ctx.saveTaskKnowledgeProject();assert.equal(calls.length,beforeBusy);assert(node('task-knowledge-project-save').disabled);
run("detail.task.status='idle';detail.task.archived=true");ctx.renderTaskKnowledgeProject(run('activeTaskKnowledgeProjectDraft()'));assert(node('task-knowledge-project-select').disabled);run('detail.task.archived=false');

loadGate=deferred();choose('three');const loading=ctx.loadTaskKnowledgeProject();choose('two');await ctx.loadTaskKnowledgeProject();loadGate.resolve(states.get('three'));await loading;assert.equal(node('task-knowledge-project-select').value,'','stale read never paints another task');loadGate=null;
choose('three');await ctx.loadTaskKnowledgeProject();assert.equal(node('task-knowledge-project-select').value,other.id,'stale read is retried when returning to its task');

const previousState=node('task-knowledge-project-state');run('taskKnowledgeProjectDrafts.get(chosen).ready=false');loadGate=deferred();const oldLogin=ctx.loadTaskKnowledgeProject();run('renewShellScope()');previousState.textContent='next login';loadGate.resolve(states.get('three'));await oldLogin;assert.equal(previousState.textContent,'next login','previous shell results never mutate the next login');assert.equal(run('taskKnowledgeProjectDrafts.size'),0,'shell disposal releases cached project drafts');
console.log('PASS: lazily loaded task project scope, escaped names, per-task drafts, explicit save payload, duplicate-save lock, stale read/save isolation, failure retention, idle/archive controls and shell cleanup. Synthetic only.');
