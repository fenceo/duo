// Synthetic tasks only: never launch an editor, connect SSH/WSL, or read user files.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import {createContext,runInContext} from 'node:vm';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';

const source=await readFile(new URL('../web/editor.ts',import.meta.url),'utf8');
const pure=createContext({console,encodeURIComponent,Error,Number,detail:null});
runInContext(stripTypeScriptTypes(source,{mode:'transform'}),pure);
const task=(kind,workspace,extra={})=>({id:'editor-task',title:'Editor test',archived:false,status:'done',updated:1,workspace,environment:{id:'test',name:'Test',type:kind,distro:'Ubuntu Test',user:'dev',host:'example.invalid',port:2222,...extra}});
const win=task('windows','C:\\Projects\\测试 # ? % 项目');
const linux=task('wsl','/home/dev/a space/#project');
const ssh=task('ssh','/srv/project');
const ref={path:"src/quote' & # %.ts",line:12,column:3};
{
 const p=pure.buildEditorTarget(win,ref),u=new URL(p.uri);
 assert.equal(u.protocol,'vscode:');assert.equal(u.host,'file');assert.equal(u.search,'');assert.equal(u.hash,'');
 assert.equal(decodeURIComponent(u.pathname),"/C:/Projects/测试 # ? % 项目/src/quote' & # %.ts:12:3");
 assert(!p.command.includes("quote'"),'shell metacharacters must be percent encoded');
 assert.match(p.command,/^code --goto --file-uri 'file:\/\/\/C:/);
 assert.match(p.confirmation,/当前设备/);
 const folder=pure.buildEditorTarget(win);assert.match(folder.uri,/\/$/);assert.match(folder.command,/--folder-uri/);
}
{
 const p=pure.buildEditorTarget(linux,ref),u=new URL(p.uri);
 assert.equal(u.host,'vscode-remote');
 assert.equal(decodeURIComponent(u.pathname),"/wsl+Ubuntu Test/home/dev/a space/#project/src/quote' & # %.ts:12:3");
 assert.match(p.notice,/默认用户/);assert.match(p.notice,/dev/);
}
{
 const p=pure.buildEditorTarget(ssh,undefined,'my-host');
 assert.equal(decodeURIComponent(new URL(p.uri).pathname),'/ssh-remote+my-host/srv/project/');
 assert.equal(p.sshConfig,'Host my-host\n    HostName example.invalid\n    User dev\n    Port 2222\n');
 assert(!p.sshConfig.includes('IdentityFile'),'do not copy server-side key paths to the client');
 for(const alias of ['',"bad\nHost evil",'-option','a b','user@host','a/../../b','a%23b'])assert.throws(()=>pure.buildEditorTarget(ssh,undefined,alias));
 for(const field of [{host:'host\nProxyCommand evil'},{user:'user%h'},{port:70000}])assert.throws(()=>pure.buildEditorTarget(task('ssh','/srv',field),undefined,'test'));
}
for(const path of ['../outside.go','x/../../outside.go','/outside.go','C:/outside.go','x\\file.go','.git/config','a/.GIT/config','a//file.go','a/./file.go','trailing./file.go','bad\nfile.go'])assert.throws(()=>pure.buildEditorTarget(win,{path,line:1,column:1}));
for(const line of [0,-1,1.2,Infinity,10000001])assert.throws(()=>pure.buildEditorTarget(win,{...ref,line}));
for(const t of [task('windows','relative/path'),task('windows','//server/share'),task('wsl','relative'),task('wsl','/a/../b'),task('wsl','/a',{distro:'bad/name'}),task('other','/a')])assert.throws(()=>pure.buildEditorTarget(t));
{
 const project=task('windows','C:/Projects/demo');
 assert.equal(pure.editorReference('src/app.ts:41:7',project).line,41);
 assert.equal(pure.editorReference('C:\\Projects\\demo\\src\\app.ts#L12',project).path,'src/app.ts');
 assert.equal(pure.editorReference('C:/Projects/demolition/file.ts',project),null,'sibling directory is not inside workspace');
 assert.equal(pure.editorReference('/etc/config.json',linux),null);
 assert.equal(pure.editorReference('https://example.invalid/a.ts',project),null);
 assert.equal(pure.editorReference('../secret.json:5',project),null);
 assert.equal(pure.editorReference('gpt-6.1',project),null);
}
const {ctx,document,window}=await createWebShellFixture();
const value=expression=>runInContext(expression,ctx);
ctx.testEditorTask=win;ctx.editorCalls=[];
ctx.api=async(path)=>{ctx.editorCalls.push(path);return path.includes('/files?')?{content:'fixture'}:{task:ctx.testEditorTask,runs:[],events:[]}};
await ctx.openEditor(win.id);
assert(document.getElementById('editor-dialog').open);
const launch=document.getElementById('editor-launch'),check=document.getElementById('editor-confirm');
assert.equal(launch.getAttribute('href'),null,'do not open a project on the wrong computer before the user checks the target');
check.checked=true;check.onchange();assert.match(launch.getAttribute('href'),/^vscode:\/\/file\//);
assert.equal(value('chosen'),'', 'opening a task menu item must not switch the current conversation');
// Changing SSH alias invalidates an earlier target confirmation.
ctx.testEditorTask=ssh;await ctx.openEditor(ssh.id);check.checked=true;check.onchange();
document.getElementById('editor-alias').value='other';document.getElementById('editor-alias').oninput();
assert.equal(check.checked,false);assert.equal(launch.getAttribute('href'),null);
// Server file reader must approve file references (including symlink boundaries).
ctx.api=async path=>{if(path.includes('/files?'))throw Error('符号链接不可打开');return {task:win}};
await ctx.openEditor(win.id,ref);assert.equal(launch.getAttribute('href'),null);assert.match(document.getElementById('editor-error').textContent,/符号链接/);
// Closing the dialog before a response arrives must prevent a late launch.
let resolve;ctx.api=()=>new Promise(r=>resolve=r);const pending=ctx.openEditor(win.id);
document.getElementById('editor-dialog').close();resolve({task:win});await pending;
assert.equal(value('editorTarget'),null);assert.equal(launch.getAttribute('href'),null);
// Another request takes precedence over an earlier menu click.
let firstResolve;ctx.api=()=>new Promise(r=>firstResolve=r);const first=ctx.openEditor(win.id);
ctx.api=async()=>({task:linux});await ctx.openEditor(linux.id);firstResolve({task:win});await first;
assert.match(value('editorTarget.uri'),/vscode-remote/);
// Authentication scope changes discard late results.
let loginResolve;ctx.api=()=>new Promise(r=>loginResolve=r);const stale=ctx.openEditor(win.id);ctx.renewShellScope();loginResolve({task:win});await stale;assert.equal(value('editorTarget'),null);
// Real full-shell menu, file panel and markdown references.
ctx.testEditorTask=win;value('detail={task:testEditorTask,runs:[],events:[]};tasks=[testEditorTask];renderList();');
assert(document.querySelector('[data-task-action="editor"]'));
assert(document.getElementById('files-editor'));assert(document.getElementById('file-editor'));
const markup=ctx.markdown('查看 `src/app.ts:25`。\n\n```ts\nsecret.ts\n```');
assert.match(markup,/data-editor-path="src\/app.ts"/);assert.match(markup,/data-editor-line="25"/);
assert(markup.includes('<pre><code>secret.ts'),'code blocks are never converted to links');
assert(!ctx.markdown('`../outside.go`').includes('data-editor-path'));
assert.match(ctx.markdown('[app.ts](src/app.ts:42)'),/data-editor-line="42"/);
assert(!ctx.markdown('[outside](../secret.json)').includes('data-editor-path'));
const malicious=ctx.markdown('`src/a" onmouseover="evil.ts`');
const container=document.createElement('div');container.innerHTML=malicious;
assert.equal(container.querySelector('[onmouseover]'),null);
console.log('PASS: VS Code local/WSL/SSH URIs; encoded special paths; scoped references and file validation; client target confirmation; no credential/config writes; stale request and login isolation; task menu and file panel.');
