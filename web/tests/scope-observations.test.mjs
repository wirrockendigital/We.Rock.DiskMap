import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Exercise bounded CGI recovery without weakening identity or payload validation.
const source=stripTypeScriptTypes(readFileSync(new URL('../src/design/scope-observations.ts',import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
const row={shareId:'share-1',path:'.',bytes:'4',recycleBytes:'0',endedAt:null,state:'complete',snapshotId:'snapshot',reasons:[],reasonCount:0,unreadableRecycle:false,unreadableSystem:false};
// Fast backoff scheduling exercises the real queue plus real transport without a fourteen-second sleep.
function fixture(responses){let calls=0;class SessionDenied extends Error{};const compiled=vm.runInNewContext(source+'\n({loadScopeObservations,ScopeObservationQueue})',{Error,TypeError,AbortController,AbortSignal,currentToken:()=> 'session',SessionDenied,fetch:async()=>{const next=responses[Math.min(calls++,responses.length-1)];return new Response(JSON.stringify(next.data??{}),{status:next.status});},queryEnvelope:r=>r.json(),crypto:{randomUUID:()=> 'read-key'},setTimeout:fn=>setImmediate(fn),clearTimeout:clearImmediate});return{fn:compiled.loadScopeObservations,Queue:compiled.ScopeObservationQueue,calls:()=>calls,SessionDenied};}
test('one retry owner recovers transient CGI failures and records each HTTP attempt',async()=>{const f=fixture([{status:502},{status:503},{status:200,data:{contractVersion:1,data:[row]}}]);const events=[],queue=new f.Queue(f.fn,(...event)=>events.push(event));const value=await queue.read([{shareId:'share-1',path:'.'}],new AbortController().signal);assert.equal(value[0].bytes,'4');assert.equal(f.calls(),3);assert.deepEqual(events.map(e=>e[2].status),[502,503,200]);queue.close();});
test('permanent service failure makes four HTTP requests rather than twelve',async()=>{const f=fixture([{status:503}]),queue=new f.Queue(f.fn);await assert.rejects(queue.read([{shareId:'share-1',path:'.'}],new AbortController().signal),/SCOPE_OBSERVATION_UNAVAILABLE/);assert.equal(f.calls(),4);queue.close();});
test('denial and mismatched identities never retry or become an empty success',async()=>{for(const response of [{status:403},{status:200,data:{contractVersion:1,data:[{...row,shareId:'foreign'}]}}]){const f=fixture([response]);await assert.rejects(f.fn([{shareId:'share-1',path:'.'}],new AbortController().signal));assert.equal(f.calls(),1);}});
