import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import assert from 'node:assert/strict';
import {createWebShellFixture} from './Web-Shell-Fixture.mjs';
import {runInContext} from 'node:vm';
const source=await readFile(new URL('../web/hardware.ts',import.meta.url),'utf8');
const mod=await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source,{mode:'transform'})+'\nexport {hexBytes,bytesHex,hardwarePlainText,hardwareKeyEvent};').toString('base64'));
assert.equal(mod.bytesHex(mod.hexBytes('001bff09')), '001bff09');
assert.equal(mod.hardwarePlainText([
 {direction:'rx',hex:'e4',text:'�'},
 {direction:'tx',hex:'03',text:''},
 {direction:'rx',hex:'b8ad1b5b33326d4f4b1b5b306d',text:'�OK'},
]), '中OK');
assert.equal(mod.hardwarePlainText([{direction:'rx',hex:Buffer.from('root@board:~# ').toString('hex')}]), 'root@board:~# ');
assert.equal(mod.hardwarePlainText([{direction:'rx',hex:Buffer.from('\x1b]52;c;hidden\x07hello').toString('hex')}]), 'hello');
globalThis.HTMLTextAreaElement=class {value='composition text'};
let wire=[],prevented=0;
const event=(key,type='keydown',extra={})=>({key,type,target:new HTMLTextAreaElement(),preventDefault(){prevented++},...extra});
const send=bytes=>wire.push(...bytes);
for(const type of ['keydown','keypress','keyup'])mod.hardwareKeyEvent(event('Enter',type),'','\r','del',send);
assert.deepEqual(wire,[13]);assert.equal(prevented,3);
wire=[];
mod.hardwareKeyEvent(event('Enter','keydown',{isComposing:true}),'','\r','del',send);
mod.hardwareKeyEvent(event('Enter','keydown',{keyCode:229}),'','\r','del',send);
assert.deepEqual(wire,[],'IME confirmation must not submit a shell command');
mod.hardwareKeyEvent(event('c','keydown',{ctrlKey:true}),'selected output','\r','del',send);
assert.deepEqual(wire,[],'Copying selected output must not interrupt the device');
const interrupt=event('c','keydown',{ctrlKey:true});
mod.hardwareKeyEvent(interrupt,'','\r','del',send);
assert.deepEqual(wire,[3]);assert.equal(interrupt.target.value,'');
wire=[];
mod.hardwareKeyEvent(event('Backspace'),'','\r','bs',send);
mod.hardwareKeyEvent(event('Enter'),'','\r\n','del',send);
assert.deepEqual(wire,[8,13,10]);
console.log('PASS: split UTF-8, raw bytes, ANSI/OSC, continuous prompts; single Enter, IME, copy vs Ctrl+C, BS/CRLF.');

const {ctx,document,intervals}=await createWebShellFixture();
const state=code=>runInContext(code,ctx),node=id=>document.getElementById(id);
assert(!document.querySelector('.hardware-ai-overview'));
assert(node('device-ai')&&node('hardware-ai-dialog'),'explicit AI permission controls remain available');
assert(node('device-refresh'),'device list still has an explicit refresh action');
const calls=[],config={id:'serial',name:'Fixture UART',protocol:'serial',device:'COM99',baud:115200,kind:'console',read_only:false};
ctx.fixtureDevice=config;
ctx.selectDevice=id=>state('deviceID='+JSON.stringify(id));
ctx.api=async route=>{calls.push(route);if(route==='hardware/overview')return [{config,connected:false,controller_task:'',controller_title:'',tasks:[{id:'a',title:'Fixture task',archived:false,ai:{read:true,write:false,power:false}}]}];if(route==='tasks/a/hardware-ai')return {serial:{read:true,write:false,power:false}};throw Error('Unexpected route: '+route)};
state("chosen='a';toolsTab='hardware';detail={task:{id:'a',title:'Fixture task',archived:false}};");
await ctx.loadDevices();assert.deepEqual(calls,['hardware/overview']);
await ctx.editHardwareAI();assert(node('hardware-ai-dialog').open);assert.equal(node('hardware-ai-read').checked,true);assert.equal(node('hardware-ai-write').checked,false);
let finishRead;ctx.api=async(route,method)=>{if(method==='PUT')throw Error('Synthetic save failure');return new Promise(resolve=>finishRead=resolve)};
const staleRead=ctx.loadDevices();assert(node('device-refresh').disabled);
await ctx.saveHardwareAI();assert(!node('device-refresh').disabled,'failed permission save must not leave refresh locked after invalidating a pending read');
finishRead([]);await staleRead;assert.equal(state('devices.length'),1,'invalidated reads cannot overwrite the current device list');
// No timer reloads the overview. Only a selected visible device long-polls logs.
ctx.poll=async()=>{};ctx.pollSettingsStatus=async()=>{};ctx.pollSetup=async()=>{};ctx.loadStickyBoard=async()=>{};
calls.length=0;for(let i=0;i<3;i++)for(const {callback} of intervals.values())callback();
await Promise.resolve();assert.equal(calls.length,0,'opening hardware with no terminal does not poll the device overview');
let logWrites=0,stateWrites=0,reply={events:[],connected:true,connection_id:'generation',controller_task:'a',controller_title:'Fixture task',relay:null};
ctx.renderDeviceLog=()=>logWrites++;ctx.renderHardwareState=()=>stateWrites++;
ctx.api=async route=>{calls.push(route);return reply};
state("hardwareView={task:'a',id:'serial',alive:true,ready:true,connected:true,generation:'generation',controller:'a',controllerTitle:'Fixture task',relay:null,requesting:false,busy:false,pending:[],term:{dispose(){}},resize:{disconnect(){}}};deviceSeq=0;deviceEvents=[]");
await ctx.pollDevice();await ctx.pollDevice();
assert.equal(logWrites,0);assert.equal(stateWrites,0,'unchanged long-poll responses do not rewrite hardware UI');
assert(calls.every(path=>path==='tasks/a/hardware/serial/events?after=0&wait=1'));
reply={...reply,events:[{seq:1,direction:'rx',hex:'6869',text:'hi'}]};await ctx.pollDevice();assert.equal(logWrites,1);assert.equal(state('deviceSeq'),1);
reply={...reply,events:[],controller_task:'other'};await ctx.pollDevice();assert.equal(stateWrites,1,'ownership changes remain visible');
assert.equal(ctx.hardwareCanWrite(state('hardwareView')),false,'other tasks retain write protection');
calls.length=0;state("toolsTab='chat'");await ctx.pollDevice();assert.equal(calls.length,0,'closed hardware panel stops log requests');
state('authenticated=false;renewShellScope()');
console.log('PASS: no periodic hardware overview, explicit refresh/grants retained, unchanged logs avoid DOM writes, controller changes and hidden-panel guards preserved. Synthetic only.');
