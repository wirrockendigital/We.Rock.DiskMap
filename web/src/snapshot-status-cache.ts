import {currentToken,sessionRequest,SessionDenied} from './session';
import {readSnapshotShareStatus,type SnapshotShareStatus} from './snapshot-usage';

interface StatusEntry {value:SnapshotShareStatus;expires:number}
interface SessionStatus {token:string;entries:Map<string,StatusEntry>;pending:Map<string,Promise<SnapshotShareStatus>>;active:number;queue:(()=>void)[]}
const stores=new WeakMap<typeof fetch,SessionStatus>();
// Cancellation belongs to each row; shared reads retain their own short transport deadline.
async function awaitStatus<T>(value:Promise<T>,signal:AbortSignal):Promise<T>{
 signal.throwIfAborted();return new Promise((resolve,reject)=>{const abort=()=>reject(signal.reason);signal.addEventListener('abort',abort,{once:true});value.then(v=>{signal.removeEventListener('abort',abort);if(!signal.aborted)resolve(v);},e=>{signal.removeEventListener('abort',abort);reject(e);});});
}
// Cache successful immutable observations briefly; every reuse rechecks current DSM authorization.
export async function readCachedSnapshotShareStatus(share:string,signal:AbortSignal,request:typeof fetch=fetch):Promise<SnapshotShareStatus>{
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 let store=stores.get(request);if(!store||store.token!==token){store={token,entries:new Map(),pending:new Map(),active:0,queue:[]};stores.set(request,store);}
 const cached=store.entries.get(share);
 if(cached&&cached.expires>Date.now()){
  await sessionRequest('session',signal,token,request);signal.throwIfAborted();if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');
  return cached.value;
 }
 let pending=store.pending.get(share);
 if(!pending){
  const owner=store;
  if(owner.pending.size>=128)throw Error('SNAPSHOT_STATUS_BUSY');
  pending=(async()=>{
   // Four shared reads run at once; queued metadata remains bounded independently of visible rows.
   if(owner.active>=4)await new Promise<void>(resolve=>owner.queue.push(resolve));else owner.active++;
   try{if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');return await readSnapshotShareStatus(share,AbortSignal.timeout(10000),request);}
   finally{const next=owner.queue.shift();if(next)next();else owner.active--;}
  })().then(value=>{
   if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');
   if(value.state==='readable'||value.state==='missing'){
    if(owner.entries.size>=128)owner.entries.delete(owner.entries.keys().next().value!);
    owner.entries.set(share,{value,expires:Date.now()+30000});
   }
   return value;
  }).finally(()=>owner.pending.delete(share));
  owner.pending.set(share,pending);
 }
 const value=await awaitStatus(pending,signal);if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');return value;
}
