import {queryEnvelope} from '../analysis-query';
import {currentToken,SessionDenied} from '../session';
import type {TimingReporter} from './diagnostics';
export interface ObservedScope {shareId:string;path:string}
export interface ScopeObservation extends ObservedScope {bytes:string|null;recycleBytes:string|null;endedAt:string|null;state:'unscanned'|'complete'|'partial';snapshotId:string;reasons:{relativePath:string;reasonCode:string}[];reasonCount:number;unreadableRecycle:boolean;unreadableSystem:boolean}
// Only explicit transient transport failures may enter the outer bounded retry policy.
export class ScopeObservationUnavailable extends Error {constructor(readonly retryable:boolean,readonly status=0){super('SCOPE_OBSERVATION_UNAVAILABLE');}}
export function observationRetryDelay(error:unknown,attempt:number):number|null{
 return attempt<3&&(error instanceof TypeError||error instanceof Error&&error.name==='TimeoutError'||error instanceof ScopeObservationUnavailable&&error.retryable)?2000*2**attempt:null;
}
type ObservationEntry={scope:ObservedScope;promise:Promise<ScopeObservation>;resolve:(row:ScopeObservation)=>void;reject:(error:unknown)=>void};
// One navigation epoch shares bounded batches; cancelling one reader cannot cancel another reader's work.
export class ScopeObservationQueue {
 private pending=new Map<string,ObservationEntry>();
 private waiting:ObservationEntry[]=[];
 private active=0;
 private controller=new AbortController();
 constructor(private load:typeof loadScopeObservations=loadScopeObservations,private report?:TimingReporter){}
 async read(scopes:ObservedScope[],signal:AbortSignal):Promise<ScopeObservation[]>{
  signal.throwIfAborted();this.controller.signal.throwIfAborted();
  const key=(s:ObservedScope)=>JSON.stringify([s.shareId,s.path]);
  // Fail explicitly rather than retaining an unbounded backlog from many expanded folders.
  const added=new Set(scopes.filter(s=>!this.pending.has(key(s))).map(key));
  if(this.pending.size+added.size>8192)throw new ScopeObservationUnavailable(false);
  const entries=scopes.map(scope=>{
   const identity=key(scope);let entry=this.pending.get(identity);
   if(!entry){
    let resolve!:ObservationEntry['resolve'],reject!:ObservationEntry['reject'];
    const promise=new Promise<ScopeObservation>((yes,no)=>{resolve=yes;reject=no;});
    entry={scope,promise,resolve,reject};this.pending.set(identity,entry);this.waiting.push(entry);
   }
   return entry.promise;
  });
  // Defer dispatch so callers in the same turn can coalesce into full 32-scope requests.
  void Promise.resolve().then(()=>this.pump());
  return new Promise((resolve,reject)=>{
   const abort=()=>{signal.removeEventListener('abort',abort);reject(signal.reason);};
   signal.addEventListener('abort',abort,{once:true});
   void Promise.all(entries).then(rows=>{signal.removeEventListener('abort',abort);if(!signal.aborted)resolve(rows);},error=>{signal.removeEventListener('abort',abort);reject(error);});
   if(signal.aborted)abort();
  });
 }
 // Abort active transport and reject queued readers when their owning epoch is replaced or unmounted.
 close(){this.controller.abort();for(const entry of this.waiting)entry.reject(this.controller.signal.reason);this.waiting=[];this.pending.clear();}
 // Retry transient failures within the shared slot, using a fresh timeout for each transport attempt.
 private async fetch(scopes:ObservedScope[]):Promise<ScopeObservation[]>{
  for(let attempt=0;;attempt++){
   this.controller.signal.throwIfAborted();
   const started=Date.now();
   try{const rows=await this.load(scopes,AbortSignal.any([this.controller.signal,AbortSignal.timeout(10000)]));this.report?.('SCOPE_BATCH',Date.now()-started,{status:200,count:scopes.length,attempt:attempt+1});return rows;}
   catch(error){
    this.report?.('SCOPE_BATCH',Date.now()-started,{status:error instanceof ScopeObservationUnavailable?error.status:0,count:scopes.length,attempt:attempt+1,failed:1});
    this.controller.signal.throwIfAborted();const delay=observationRetryDelay(error,attempt);if(delay===null)throw error;
    await new Promise<void>((resolve,reject)=>{
     const signal=this.controller.signal,abort=()=>{clearTimeout(timer);signal.removeEventListener('abort',abort);reject(signal.reason);};
     const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},delay);
     signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
    });
   }
  }
 }
 private pump(){
  while(!this.controller.signal.aborted&&this.active<4&&this.waiting.length){
   const entries=this.waiting.splice(0,32);this.active++;
   void this.fetch(entries.map(e=>e.scope)).then(rows=>{entries.forEach((entry,i)=>entry.resolve(rows[i]));},error=>{entries.forEach(entry=>entry.reject(error));}).finally(()=>{
    for(const entry of entries)this.pending.delete(JSON.stringify([entry.scope.shareId,entry.scope.path]));
    this.active--;this.pump();
   });
  }
 }
}
// Read saved scope metadata independently of map selection; responses remain ordered and identity-bound.
export async function loadScopeObservations(scopes:ObservedScope[],signal:AbortSignal):Promise<ScopeObservation[]>{
 const token=currentToken();if(!token)throw new SessionDenied();
 // The shared queue is the single retry owner; nested transport retries would multiply backend work.
 signal.throwIfAborted();
 const r=await fetch('./api.cgi?action=scope-observations',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,scopes})});
 signal.throwIfAborted();if(currentToken()!==token)throw new SessionDenied();
 if(r.status===401||r.status===403)throw new SessionDenied();if(!r.ok){await r.body?.cancel();throw new ScopeObservationUnavailable([408,429,500,502,503,504].includes(r.status),r.status);}
 const out=await queryEnvelope(r);signal.throwIfAborted();if(currentToken()!==token)throw new SessionDenied();if(out.contractVersion!==1||!Array.isArray(out.data)||out.data.length!==scopes.length)throw Error('INVALID_RESPONSE');
 for(let i=0;i<scopes.length;i++){const v=out.data[i];if(v.shareId!==scopes[i].shareId||v.path!==scopes[i].path||!(v.bytes===null||typeof v.bytes==='string'&&/^\d{1,128}$/.test(v.bytes))||!['unscanned','complete','partial'].includes(v.state)||typeof v.unreadableRecycle!=='boolean'||typeof v.unreadableSystem!=='boolean'||!Array.isArray(v.reasons))throw Error('INVALID_RESPONSE');}
 return out.data;
}
