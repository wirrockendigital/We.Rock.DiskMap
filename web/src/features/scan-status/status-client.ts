import { currentToken, SessionDenied } from '../../session';
import { checkedProgress, type ProgressInfo } from '../../ProgressHistory';
// This is an observation, not a scheduler promise or a second job state machine.
export interface StatusJob {jobId:string;profileId:string;profileRevision:number;scanMode:'full'|'rebuild'|'reconcile';trigger:'ui'|'cli'|'scheduler';state:string;acceptedAt:string;startedAt:string|null;queuePosition:number}
export interface StatusSnapshot {observedAt:string;active:StatusJob|null;queued:StatusJob[]}
export interface ProgressObservation {current:ProgressInfo|null;history:ProgressInfo[];limited:boolean;observedAt:string}
export class StatusReadError extends Error {constructor(readonly status:number){super('STATUS_UNAVAILABLE');}}
const id=/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const activeStates=['preflight','scanning','paused','aggregating','validating','publishing'];
const date=(v:unknown)=>typeof v==='string'&&v.length<=64&&Number.isFinite(Date.parse(v));
// Strict identity and ordinal checks preserve the server's transient all-queued state.
export function checkedStatus(value:unknown):StatusSnapshot {
 const s=value as StatusSnapshot;
 if(!s||!date(s.observedAt)||!Array.isArray(s.queued)||s.queued.length+(s.active?1:0)>17||(s.active!==null&&(typeof s.active!=='object'||Array.isArray(s.active))))throw Error('INVALID_STATUS');
 const jobs=[...(s.active?[s.active]:[]),...s.queued],ids=new Set<string>(),profiles=new Set<string>();
 for(const [i,j] of jobs.entries()){
  const active=!!s.active&&i===0;
  if(!j||typeof j.jobId!=='string'||typeof j.profileId!=='string'||!id.test(j.jobId)||!id.test(j.profileId)||ids.has(j.jobId)||profiles.has(j.profileId)||!Number.isSafeInteger(j.profileRevision)||j.profileRevision<1||j.profileRevision>2147483647||!['full','rebuild','reconcile'].includes(j.scanMode)||!['ui','cli','scheduler'].includes(j.trigger)||!date(j.acceptedAt)||(active?!activeStates.includes(j.state):j.state!=='queued')||j.queuePosition!==(active?0:i+(s.active?0:1))||(active?!date(j.startedAt)||Date.parse(j.startedAt!)<Date.parse(j.acceptedAt):j.startedAt!==null))throw Error('INVALID_STATUS');
  ids.add(j.jobId);profiles.add(j.profileId);
 }
 return s;
}
// Bound bytes before JSON and qualify abort before every auth/data interpretation.
async function read(action:string,signal:AbortSignal):Promise<unknown>{
 const token=currentToken();signal.throwIfAborted();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await fetch(`./api.cgi?action=${action}`,{signal,credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'X-SYNO-TOKEN':token,Accept:'application/json'}});
 signal.throwIfAborted();if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');if(!response.ok)throw new StatusReadError(response.status);if(!response.body)throw Error('INVALID_STATUS');
 const reader=response.body.getReader();let size=0,body='';const decoder=new TextDecoder('utf-8',{fatal:true});
 try{while(true){const part=await reader.read();signal.throwIfAborted();if(part.done)break;size+=part.value.byteLength;if(size>65536)throw Error('STATUS_TOO_LARGE');body+=decoder.decode(part.value,{stream:true});}body+=decoder.decode();signal.throwIfAborted();const parsed=JSON.parse(body);if(parsed.contractVersion!==1)throw Error('INVALID_STATUS');return parsed.data;}
 finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
}
export async function readStatus(signal:AbortSignal):Promise<StatusSnapshot>{return checkedStatus(await read('status-snapshot',signal));}
// History and current progress are separate observations; publish the pair only when both qualify.
export async function readProgressObservation(profileId:string,jobId:string|null,signal:AbortSignal):Promise<ProgressObservation>{
 const h=await read(`history&profileId=${encodeURIComponent(profileId)}`,signal) as {jobs:ProgressInfo[];truncated:boolean};
 if(!h||!Array.isArray(h.jobs)||h.jobs.length>50||typeof h.truncated!=='boolean')throw Error('INVALID_PROGRESS');
 const rows=h.jobs.map(checkedProgress);if(rows.some(p=>p.profileId!==profileId)||new Set(rows.map(p=>p.jobId)).size!==rows.length)throw Error('INVALID_PROGRESS');
 const current=jobId?checkedProgress(await read(`progress&jobId=${encodeURIComponent(jobId)}`,signal) as ProgressInfo):null;
 signal.throwIfAborted();if(current&&(current.profileId!==profileId||current.jobId!==jobId))throw Error('INVALID_PROGRESS');
 return{current,history:rows,limited:h.truncated,observedAt:new Date().toISOString()};
}
