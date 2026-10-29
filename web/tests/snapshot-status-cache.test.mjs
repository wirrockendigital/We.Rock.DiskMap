import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Production cache tests retain authorization checks and isolate row cancellation from shared work.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/snapshot-status-cache.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
function fixture(){let token='a',now=0,reads=0,checks=0,active=0,peak=0,denied=false,mode='readable',hold=null;const pending=[];
 class SessionDenied extends Error{}
 const request=()=>{};
 const read=vm.runInNewContext(source+'\nreadCachedSnapshotShareStatus',{currentToken:()=>token,sessionRequest:async()=>{checks++;if(denied)throw new SessionDenied();},SessionDenied,AbortSignal,Date:{now:()=>now},fetch:request,readSnapshotShareStatus:async()=>{reads++;active++;peak=Math.max(peak,active);if(hold)await new Promise(resolve=>pending.push(resolve));active--;return {state:mode,supported:true,count:1,scheduled:false};}});
 return {read:(share,signal=new AbortController().signal)=>read(share,signal,request),pending,hold:()=>{hold=true;},release:()=>pending.splice(0).forEach(f=>f()),expire:()=>now+=30001,switch:()=>token='b',deny:()=>denied=true,mode:value=>mode=value,get reads(){return reads},get checks(){return checks},get peak(){return peak}};
}
test('same share reuses status but checks authorization, expiry and session identity',async()=>{
 const f=fixture();await f.read('Docs');await f.read('Docs');await f.read('Docs');assert.equal(f.reads,1);assert.equal(f.checks,2);
 f.expire();await f.read('Docs');assert.equal(f.reads,2);f.switch();await f.read('Docs');assert.equal(f.reads,3);f.deny();await assert.rejects(f.read('Docs'));
});
test('concurrent consumers coalesce without a cancelled row cancelling another row',async()=>{
 const f=fixture();f.hold();const abort=new AbortController();const first=f.read('Docs',abort.signal);const second=f.read('Docs');abort.abort();await assert.rejects(first);f.release();assert.equal((await second).state,'readable');assert.equal(f.reads,1);
});
test('unknown status is retried and active DSM status work is limited to four shares',async()=>{
 const f=fixture();f.mode('unknown');await f.read('Docs');await f.read('Docs');assert.equal(f.reads,2);
 f.hold();const reads=Array.from({length:9},(_,i)=>f.read('Share'+i));await new Promise(setImmediate);assert.equal(f.peak,4);assert.equal(f.pending.length,4);
 for(let i=0;i<3;i++){f.release();await new Promise(setImmediate);}await Promise.all(reads);assert.equal(f.peak,4);
});
