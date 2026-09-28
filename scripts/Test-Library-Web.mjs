import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import assert from 'node:assert/strict';

const source=stripTypeScriptTypes(await readFile(new URL('../web/library.ts',import.meta.url),'utf8'),{mode:'transform'});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}};
function fixture(){
 const nodes=new Map(),notices=[],calls=[];
 const node=id=>{if(!nodes.has(id))nodes.set(id,{value:'',checked:false,disabled:false,textContent:'',innerHTML:'',classList:{toggle(){}},focus(){},dispatchEvent(){},close(){},showModal(){}});return nodes.get(id)};
 const ctx=createContext({URLSearchParams,Event,console,element:node,input:node,button:node,shellEpoch:1,selection:4,chosen:'one',creatingTask:false,tasks:[],drafts:new Map(),names:{done:'完成'},shellController:new AbortController(),shellCurrent:e=>e===ctx.shellEpoch,escapeHTML:s=>s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),notify:s=>notices.push(s),loadKnowledge:async()=>{},api:async(path,method,body)=>{calls.push({path,method,body});return path.startsWith('library/search')?{documents:[],truncated:false}:{reference:'\n[来源：history]\nprevious conclusion',truncated:false}}});
 runInContext(source,ctx);runInContext("libraryTarget={task:'one',create:false,selection:4,epoch:1};libraryHits=[{id:'knowledge:abc',hash:'version-one'}]",ctx);
 return {ctx,node,notices,calls,run:code=>runInContext(code,ctx)};
}
{
 const {ctx,node,calls}=fixture();node('message').value='current request';await ctx.citeLibrary('knowledge:abc');
 assert.match(calls[0].path,/hash=version-one/);assert.equal(node('message').value,'current request\n[来源：history]\nprevious conclusion');assert.equal(ctx.drafts.get('one'),node('message').value);assert.equal(calls.length,1,'citation never sends a model message');
}
{
 const {ctx,node}=fixture(),gate=deferred();ctx.api=()=>gate.promise;const pending=ctx.citeLibrary('knowledge:abc');ctx.chosen='two';ctx.selection++;node('message').value='new task draft';gate.resolve({reference:'wrong context'});await pending;assert.equal(node('message').value,'new task draft');assert.equal(ctx.drafts.size,0,'task switch cannot redirect a pending citation');
}
{
 const {ctx,node,run}=fixture();ctx.creatingTask=true;ctx.chosen='';run("libraryTarget={task:'',create:true,selection:4,epoch:1}");node('create-input').value='new task';await ctx.citeLibrary('knowledge:abc');assert.match(node('create-input').value,/new task\n\[来源/);assert.equal(ctx.drafts.size,0);
}
{
 const {ctx,node}=fixture(),gate=deferred();ctx.api=()=>gate.promise;const pending=ctx.citeLibrary('knowledge:abc');ctx.shellEpoch++;node('message').value='different login';gate.resolve({reference:'old account'});await pending;assert.equal(node('message').value,'different login');
}
{
 const {ctx,node}=fixture(),old=deferred();let count=0;ctx.api=()=>++count===1?old.promise:Promise.resolve({documents:[{id:'safe',title:'<img onerror=1>',task_title:'test',kind:'knowledge',status:'observed',origin:'vault',path:'a.md',snippet:'<script>bad</script>'}],truncated:false});
 const first=ctx.searchLibrary();await ctx.searchLibrary();old.resolve({documents:[],truncated:false});await first;assert.match(node('library-results').innerHTML,/&lt;img/);assert(!node('library-results').innerHTML.includes('<script>'));assert.match(node('library-results').innerHTML,/data-library-reference="safe"/);
}
console.log('PASS: citations retain provenance, never send a model turn, remain isolated across task/create/login changes, and escape imported Markdown previews.');
