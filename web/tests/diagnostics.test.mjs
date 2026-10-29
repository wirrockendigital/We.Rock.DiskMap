import {strict as assert} from 'node:assert';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {rolldown} from 'rolldown';
// Verify diagnostic privacy, bounded retention, opt-in and installation override semantics.
const directory=await mkdtemp(join(tmpdir(),'diskmap-debug-test-'));
try{
 const bundle=await rolldown({cwd:new URL('../',import.meta.url).pathname,input:'src/design/diagnostics.ts',platform:'node'});
 await bundle.write({file:join(directory,'policy.mjs'),format:'esm'});await bundle.close();
 const {DiagnosticLog,safeErrorCode}=await import(pathToFileURL(join(directory,'policy.mjs')));
 const log=new DiagnosticLog(()=>{});const saved=new Map();
 globalThis.localStorage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v)};
 assert.equal(log.enabled,false);log.add('SOURCE_OPEN');assert.equal(log.entries.length,0);
 globalThis.fetch=async()=>new Response(JSON.stringify({enabled:true,installation:'vdsm-one'}));
 await log.configure(new AbortController().signal);assert.equal(log.enabled,true);
 assert.equal(log.limitMB,10);
 for(let i=0;i<300;i++)log.add('SOURCE_OPEN');assert.equal(log.count,301);
 log.setLimit(1);for(let i=0;i<20000;i++)log.add('SOURCE_OPEN');assert(log.bytes<=1000000);assert(log.count>200);assert.equal(log.entries.length,500);
 assert.equal(Buffer.byteLength(log.exportText()+'\n'),log.bytes);
 log.setLimit(101);assert.equal(log.limitMB,1);log.setLimit(0);assert.equal(log.limitMB,1);
 log.add('SCAN_PARTIAL','INCOMPLETE_COVERAGE');log.setFilter('warning');assert(log.entries.every(x=>x.includes('[WARNING]')));assert.equal(log.entries.length,1);
 log.add('REQUEST_FAILED','SNAPSHOT_MISMATCH');log.setFilter('critical');assert.equal(log.entries.length,1);log.setFilter('all');
 log.clear();log.observe('job','scanning',{entries:'10',knownLogicalBytes:'1000',files:'password'},100000);assert.equal(log.count,2);
 log.observe('job','scanning',{entries:'10',knownLogicalBytes:'1000',files:'password'},105000);assert.equal(log.count,2);
 log.observe('job','scanning',{entries:'10',knownLogicalBytes:'1000',files:'password'},160000);assert(log.exportText().includes('SCAN_COUNTERS_UNCHANGED'));assert(!log.exportText().includes('password'));
 log.clear();log.observe('job','aggregating',{entries:'10'},100000);log.observe('job','aggregating',{entries:'10'},200000);assert.equal(log.count,3);assert(!log.exportText().includes('SCAN_COUNTERS_UNCHANGED'));

 log.add('password=secret');log.add('REQUEST_FAILED','secret');assert(!log.entries.join('').includes('secret'));
 // Query timings accept only fixed stages and bounded numbers, even when callers supply hostile objects.
 log.clear();log.timing('SCOPE_BATCH',1250.6,{status:502,count:32,attempt:2,failed:1,url:'private-secret',pending:NaN});
 assert(log.exportText().includes('stage=SCOPE_BATCH ms=1251 status=502 count=32 attempt=2 failed=1'));
 assert(!log.exportText().includes('private-secret'));assert(!log.exportText().includes('NaN'));
 log.timing('private-secret',1);log.timing('MAP_LAYOUT',-1);assert.equal(log.count,1);
 // Fixed scanner operation codes survive logging; arbitrary backend diagnostics do not.
 log.clear();log.observe('failed-job','failed',{errorCodes:['SCAN_NODE_WRITE_FAILED_SQLITE_2067','private source path','SCAN_UNKNOWN_FAILED']},210000);
 assert(log.exportText().includes('SCAN_NODE_WRITE_FAILED_SQLITE_2067'));assert(!log.exportText().includes('private source path'));assert(!log.exportText().includes('SCAN_UNKNOWN_FAILED'));
 assert.equal(safeErrorCode(new Error('my-password')),'SERVICE_UNAVAILABLE');
 assert.equal(safeErrorCode({code:'SNAPSHOT_MISMATCH'}),'SNAPSHOT_MISMATCH');
 log.set(false);assert.equal(log.entries.length,0);await log.configure(new AbortController().signal);assert.equal(log.enabled,false);
 globalThis.fetch=async()=>new Response(JSON.stringify({enabled:true,installation:'vdsm-two'}));
 await log.configure(new AbortController().signal);assert.equal(log.enabled,true);
 log.clear();assert.equal(log.entries.length,0);
 console.log('PASS: byte rotation, 1–100 MB limits, levels, safe counters, phase/heartbeat/stall semantics, default off and installation override');
}finally{await rm(directory,{recursive:true,force:true});}
