import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Verify recovery limits, authorization and cancellation for read-only source discovery.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/scope-api.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
function fixture(responses){let calls=0,token='session';class SessionDenied extends Error{};const fn=vm.runInNewContext(source+'\nfetchScope',{currentToken:()=>token,SessionDenied,fetch:async()=>new Response('{}',{status:responses[Math.min(calls++,responses.length-1)]}),setTimeout,clearTimeout});return{fn,calls:()=>calls,changeIdentity:()=>{token='other';}};}
test('discovery recovers from bounded transient failures',async()=>{const f=fixture([503,502,200]);assert.equal((await f.fn('read',{signal:new AbortController().signal},'session')).status,200);assert.equal(f.calls(),3);});
test('discovery returns persistent failure and never retries access denial',async()=>{for(const status of [503,403]){const f=fixture([status]);assert.equal((await f.fn('read',{signal:new AbortController().signal},'session')).status,status);assert.equal(f.calls(),status===503?3:1);}});
test('identity changes and cancellation stop delayed recovery',async()=>{for(const cancel of [false,true]){const f=fixture([503]),controller=new AbortController();const result=f.fn('read',{signal:controller.signal},'session');await new Promise(resolve=>setTimeout(resolve,10));if(cancel)controller.abort();else f.changeIdentity();await assert.rejects(result);assert.equal(f.calls(),1);}});
