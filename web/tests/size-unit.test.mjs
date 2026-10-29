import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,mkdirSync,mkdtempSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
import vm from 'node:vm';
// Exercise the production formatter without coupling the checks to React rendering.
const web=fileURLToPath(new URL('../',import.meta.url)),tmp=(mkdirSync(join(web,'.test-build'),{recursive:true}),mkdtempSync(join(web,'.test-build/size-unit-')));
let source;
try{
 execFileSync(join(web,'node_modules/.bin/tsc'),['--ignoreConfig','--target','ES2022','--module','esnext','--moduleResolution','bundler','--jsx','react-jsx','--esModuleInterop','--skipLibCheck','--rootDir',join(web,'src'),'--outDir',tmp,join(web,'src/design/size-unit.tsx')],{stdio:'pipe'});
 source=readFileSync(join(tmp,'design/size-unit.js'),'utf8').replace(/import \{([^}]+)\} from ["']([^"']+)["'];/g,(_,names,path)=>`const {${names.replace(/ as /g,':')}}=require('${path}');`).replace(/export /g,'')+'\nObject.assign(exports,{nextSizeUnit,formatUnitBytes});';
}finally{rmSync(tmp,{recursive:true,force:true});}
const exports={};vm.runInNewContext(source,{exports,Intl,require:name=>name==='react'?{createContext:()=>({})}:{}});
const {nextSizeUnit:next,formatUnitBytes:format}=exports;
test('size units cycle in the requested order',()=>{assert.deepEqual(['TB','GB','MB','mixed','hidden'].map(next),['GB','MB','mixed','hidden','TB']);});
test('fixed decimal units preserve small, unknown, empty and huge sizes',()=>{
 assert.equal(format('82','GB','de'),'0,0 GB');assert.equal(format('0','GB','de'),'0,0 GB');
 assert.equal(format(undefined,'GB','de'),'—');assert.equal(format(undefined,'hidden','de'),'');
 assert.equal(format('1500000000','GB','de'),'1,5 GB');assert.equal(format('1500000000','MB','en'),'1,500.0 MB');
 assert.equal(format('9007199254740993','GB','en'),'9,007,199.3 GB');assert.equal(format('1000000000000','TB','de'),'1,0 TB');
});

test('mixed chooses decimal TB GB MB KB per value',()=>{
 for(const [bytes,expected] of [['1000000000000','1,0 TB'],['1500000000','1,5 GB'],['81900000','81,9 MB'],['12345','12,3 KB'],['82','0,1 KB'],['0','0,0 KB']])assert.equal(format(bytes,'mixed','de'),expected);
 assert.equal(format(undefined,'mixed','en'),'—');assert.equal(format('9007199254740993','mixed','en'),'9,007.2 TB');
});
