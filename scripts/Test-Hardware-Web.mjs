import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import assert from 'node:assert/strict';
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
