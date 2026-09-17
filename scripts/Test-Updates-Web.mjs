import {readFile} from 'node:fs/promises';
import {stripTypeScriptTypes} from 'node:module';
import assert from 'node:assert/strict';
const source=await readFile(new URL('../web/updates.ts',import.meta.url),'utf8');
const mod=await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source,{mode:'transform'})+'\nexport {safeUpdateLink};').toString('base64'));
for(const url of ['https://github.com/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases/tag/v1.0.0','https://github.com/owner/jianzuo/releases/download/v1.0.0/package.zip'])assert.equal(mod.safeUpdateLink(url,'owner/jianzuo'),url);
for(const url of [undefined,'javascript:alert(1)','https://github.com.evil.test/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases-evil','https://github.com/other/project/releases','https://user:pass@github.com/owner/jianzuo/releases','https://github.com/owner/jianzuo/releases?token=x'])assert.equal(mod.safeUpdateLink(url,'owner/jianzuo'),'');
console.log('PASS: update links limited to selected GitHub repository and HTTPS.');
assert.equal(mod.safeUpdateLink('https://github.com/Owner/Jianzuo/releases','owner/jianzuo'),'https://github.com/Owner/Jianzuo/releases');
