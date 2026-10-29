import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import vm from 'node:vm';
// Run the production queue and hook with controlled transport completion and no live DSM requests.
const compile=file=>stripTypeScriptTypes(readFileSync(new URL(file,import.meta.url),'utf8'),{mode:'transform'}).replace(/^import .*;$/gm,'').replace(/export /g,'');
const production=compile('../src/design/scope-observations.ts');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const create=(extra={})=>vm.runInNewContext(production+'\n({ScopeObservationQueue,observationRetryDelay})',{Error,TypeError,AbortController,AbortSignal,setTimeout,clearTimeout,...extra});
const scope=path=>({shareId:'share',path});
test('shared queue limits transport to four, coalesces duplicates and preserves reader order',async()=>{
 const {ScopeObservationQueue}=create();let active=0,peak=0,calls=0;const releases=[];
 const queue=new ScopeObservationQueue(async scopes=>{calls++;active++;peak=Math.max(peak,active);await new Promise(resolve=>releases.push(()=>{active--;resolve();}));return scopes;});
 const signal=new AbortController().signal;
 const reads=Array.from({length:32},(_,page)=>queue.read(Array.from({length:128},(_,i)=>scope(`${page}/${i}`)),signal));
 const duplicate=queue.read([scope('0/0'),scope('0/0')],signal);
 await tick();assert.equal(peak,4);assert.equal(calls,4);
 while(releases.length){releases.shift()();await tick();}
 const rows=await Promise.all(reads);assert.equal(rows.flat().length,4096);assert.equal(calls,128);assert.equal(peak,4);
 assert.deepEqual(Array.from(await duplicate,x=>x.path),['0/0','0/0']);assert.equal(rows[31][127].path,'31/127');queue.close();
});
test('caller cancellation leaves shared work alive; epoch close cancels active and queued work',async()=>{
 const {ScopeObservationQueue}=create();let finish,transport;
 const queue=new ScopeObservationQueue(async(scopes,signal)=>{transport=signal;await new Promise(resolve=>{finish=resolve;});return scopes;});
 const a=new AbortController(),b=new AbortController();
 const first=queue.read([scope('.')],a.signal),second=queue.read([scope('.')],b.signal);
 const cancelled=assert.rejects(first,{name:'AbortError'});await tick();a.abort();await cancelled;
 assert.equal(transport.aborted,false);finish();assert.equal((await second)[0].path,'.');queue.close();
 const stopped=new ScopeObservationQueue(async(scopes,signal)=>{await new Promise((resolve,reject)=>signal.addEventListener('abort',()=>reject(signal.reason),{once:true}));return scopes;});
 const pending=stopped.read(Array.from({length:160},(_,i)=>scope(String(i))),b.signal);
 const rejected=assert.rejects(pending,{name:'AbortError'});await tick();stopped.close();await rejected;
});
test('transport timeout retries with fresh signals and bounded delays; explicit abort and invalid data do not retry',async()=>{
 const timers=[],signals=[];let attempts=0;
 const {ScopeObservationQueue,observationRetryDelay}=create({setTimeout:(fn,delay)=>{timers.push({fn,delay});return fn;},clearTimeout:()=>{}});
 const queue=new ScopeObservationQueue(async(scopes,signal)=>{signals.push(signal);if(attempts++<3)throw new DOMException('timeout','TimeoutError');return scopes;});
 const result=queue.read([scope('.')],new AbortController().signal);await tick();
 for(const delay of [2000,4000,8000]){assert.equal(timers.length,1);const timer=timers.shift();assert.equal(timer.delay,delay);timer.fn();await tick();}
 assert.equal((await result)[0].path,'.');assert.equal(attempts,4);assert.equal(new Set(signals).size,4);queue.close();
 assert.equal(observationRetryDelay(new DOMException('cancelled','AbortError'),0),null);
 assert.equal(observationRetryDelay(new DOMException('timeout','TimeoutError'),3),null);
 let failures=0;const invalid=new ScopeObservationQueue(async()=>{failures++;throw Error('INVALID_RESPONSE');});
 await assert.rejects(invalid.read([scope('.')],new AbortController().signal),/INVALID_RESPONSE/);assert.equal(failures,1);invalid.close();
});
test('progressive pages remain immediately readable and observe the parent only on the first page',async()=>{
 let pages=0,active=0,peak=0;const requested=[];
 const {ScopeObservationQueue}=create({loadScopeObservations:async scopes=>{requested.push(...scopes);active++;peak=Math.max(peak,active);return new Promise(()=>{});}});
 // Inject the production loader through the queue constructor while retaining hook ownership semantics.
 const Queue=class extends ScopeObservationQueue{constructor(){super(async(scopes,signal)=>{requested.push(...scopes);active++;peak=Math.max(peak,active);await new Promise((resolve,reject)=>signal.addEventListener('abort',()=>reject(signal.reason),{once:true}));active--;return scopes;});}};
 const effects=[];
 const hook=vm.runInNewContext(compile('../src/design/useSourceNavigation.ts')+'\nuseSourceNavigation',{
  ScopeObservationQueue:Queue,useRef:v=>({current:v}),useState:v=>[v,()=>{}],useMemo:f=>f(),useEffect:f=>effects.push(f),useCallback:f=>f,AbortController,AbortSignal,Date,Map,Set,SessionDenied:class extends Error{},visibleCoverage:v=>v,scopeStamp:()=>({state:'unknown'}),recycleStateForScope:()=> 'unknown',
  loadScopeFolders:async()=>({entries:Array.from({length:128},(_,i)=>({name:`f${pages}-${i}`,relativePath:`f${pages}-${i}`})),nextCursor:String(++pages)})
 })({sources:{volumes:[{id:'vol',name:'volume1',state:'reachable'}],shares:[{id:'share',name:'photo',volumeId:'vol',readable:true}]},summaries:[],path:[],workspace:{},analysis:{},profiles:[]},()=>{});
 const controller=new AbortController();
 for(let page=0;page<32;page++){const result=await hook.read('source:shares:share:.',page?String(page):'',controller.signal);assert.equal(result.items.length,128);}
 assert.equal(pages,32);assert.equal(peak,4);assert.equal(requested.filter(s=>s.path==='.').length,1);
 // Cancel readers and close the queue through the normal effect cleanup after the synthetic render.
 controller.abort();const cleanup=effects[0]();cleanup();await tick();
});
// A failed shared batch must release its slot, and closing during backoff must stop further retries.
test('retry exhaustion releases slots and epoch cancellation stops a scheduled retry',async()=>{
 const timers=[];let calls=0;
 const {ScopeObservationQueue}=create({setTimeout:(fn,delay)=>{timers.push({fn,delay});return fn;},clearTimeout:fn=>{const i=timers.findIndex(t=>t.fn===fn);if(i>=0)timers.splice(i,1);}});
 const queue=new ScopeObservationQueue(async()=>{calls++;throw new DOMException('timeout','TimeoutError');});
 const result=queue.read([scope('.')],new AbortController().signal),rejected=assert.rejects(result,{name:'TimeoutError'});
 await tick();for(let i=0;i<3;i++){timers.shift().fn();await tick();}await rejected;assert.equal(calls,4);assert.equal(timers.length,0);queue.close();
 const cancelled=new ScopeObservationQueue(async()=>{calls++;throw new TypeError('offline');});
 const pending=cancelled.read([scope('.')],new AbortController().signal),aborted=assert.rejects(pending,{name:'AbortError'});
 await tick();const before=calls;assert.equal(timers.length,1);cancelled.close();await aborted;await tick();assert.equal(calls,before);assert.equal(timers.length,0);
});
