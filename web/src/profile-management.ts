import type {Profile} from './contracts';
import {configurationKey, validateConfiguration} from './profile-actions';
import {currentToken, SessionDenied} from './session';

export type ManagementOperation='duplicate'|'deactivate'|'delete'|'reconcile'|'rebuild';
export interface ManagementInput {operation:ManagementOperation;profile:Profile;name?:string;confirmProfileName?:string;confirmOwnDataDeletion?:boolean;acknowledgeTasks?:boolean}
export interface ManagementReceipt {operation:ManagementOperation;profileId:string;profile:Profile|null;jobId:string|null;jobMode:string|null;jobRevision:number|null;deduplicated:boolean;cleanupScheduled:boolean;tasksRequireReview:boolean}
export interface CleanupObservation {profileId:string;deleted:true;pendingGenerations:number;complete:boolean;scope:'sealed-generations'}
export const managementID=(value:unknown):value is string=>typeof value==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value);
const validRevision=(x:unknown):x is number=>Number.isSafeInteger(x)&&Number(x)>0&&Number(x)<=2147483647;
const object=(x:unknown):Record<string,any>=>x!==null&&typeof x==='object'&&!Array.isArray(x)?x as Record<string,any>:{};
// Every response is bounded before parsing; errors expose only fixed product codes.
export async function readManagementJSON(response:Response):Promise<any>{
 if(!response.body)throw Error('MANAGEMENT_RESPONSE_INVALID');const reader=response.body.getReader(),parts:Uint8Array[]=[];let length=0;
 try{for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>65536){await reader.cancel();throw Error('MANAGEMENT_RESPONSE_INVALID');}parts.push(value);}}finally{reader.releaseLock();}
 const bytes=new Uint8Array(length);let offset=0;for(const part of parts){bytes.set(part,offset);offset+=part.length;}return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));
}
// A management request carries a profile revision only where the existing API supports CAS.
export function encodeManagement(input:ManagementInput):{route:'scan'|'profile-action';body:string}{
 const {profile:p,operation}=input;if(!managementID(p.id)||!validRevision(p.revision))throw Error('INVALID_REQUEST');
 let request:unknown;
 if(operation==='reconcile'||operation==='rebuild'){if(!p.active)throw Error('PROFILE_INACTIVE');request={contractVersion:1,profileId:p.id,mode:operation};}
 else {const common={contractVersion:1,operation,profileId:p.id,expectedRevision:p.revision};
  if(operation==='duplicate'){validateConfiguration({...p,name:input.name??''});request={...common,name:input.name};}
  else if(operation==='deactivate')request=common;
  else if(operation==='delete'){if(input.confirmProfileName!==p.name||input.confirmOwnDataDeletion!==true||input.acknowledgeTasks!==true)throw Error('DELETE_CONFIRMATION_REQUIRED');request={...common,confirmProfileName:p.name,confirmOwnDataDeletion:true,acknowledgeTasks:true};}
  else throw Error('INVALID_REQUEST');
 }
 const body=JSON.stringify(request);if(new TextEncoder().encode(body).length>16384)throw Error('INVALID_REQUEST');return{route:operation==='reconcile'||operation==='rebuild'?'scan':'profile-action',body};
}
// Qualification compares the actual receipt to the captured intent, not the subsequently refreshed catalogue.
export function checkedManagementReceipt(raw:unknown,input:ManagementInput):ManagementReceipt{
 const e=object(raw),d=object(e.data),p=input.profile,op=input.operation;if(e.contractVersion!==1)throw Error('MANAGEMENT_RESPONSE_INVALID');
 if(op==='reconcile'||op==='rebuild'){
  const job=object(d.job);if(!managementID(job.id)||job.profileId!==p.id||job.scanMode!==op||!validRevision(job.profileRevision)||typeof d.deduplicated!=='boolean')throw Error('MANAGEMENT_RESPONSE_INVALID');
  return{operation:op,profileId:p.id,profile:null,jobId:job.id,jobMode:job.scanMode,jobRevision:job.profileRevision,deduplicated:d.deduplicated,cleanupScheduled:false,tasksRequireReview:false};
 }
 if(d.operation!==op||!managementID(d.profileId)||typeof d.cleanupScheduled!=='boolean'||typeof d.tasksRequireReview!=='boolean')throw Error('MANAGEMENT_RESPONSE_INVALID');
 if(op==='delete'){if(d.profileId!==p.id||d.profile!==undefined||!d.cleanupScheduled||!d.tasksRequireReview)throw Error('MANAGEMENT_RESPONSE_INVALID');}
 else {const result=d.profile as Profile;validateConfiguration(result);if(!managementID(result.id)||d.profileId!==result.id||!validRevision(result.revision)||d.cleanupScheduled)throw Error('MANAGEMENT_RESPONSE_INVALID');
  if(op==='duplicate'?(result.id===p.id||result.revision!==1||configurationKey(result)!==configurationKey({...p,name:input.name!})||d.tasksRequireReview):(result.id!==p.id||result.revision!==p.revision||configurationKey(result)!==configurationKey({...p,active:false})||!d.tasksRequireReview))throw Error('MANAGEMENT_RESPONSE_INVALID');
 }
 return{operation:op,profileId:d.profileId,profile:d.profile??null,jobId:null,jobMode:null,jobRevision:null,deduplicated:false,cleanupScheduled:d.cleanupScheduled,tasksRequireReview:d.tasksRequireReview};
}
// One unresolved mutation is retained across card changes; teardown is reserved for session disposal.
export class ProfileManagementClient {
 private pending:{input:ManagementInput;route:string;body:string;key:string}|null=null;private controller:AbortController|null=null;private disposed=false;
 status:'idle'|'saving'|'uncertain'='idle';
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private key:()=>string=()=>crypto.randomUUID(),private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 get unresolved(){return this.pending!==null;}
 get pendingProfileId(){return this.pending?.input.profile.id??null;}
 dispose(){this.disposed=true;this.controller?.abort();this.pending=null;}
 private live(){if(this.disposed)throw Error('MANAGEMENT_DISPOSED');}
 async run(input:ManagementInput):Promise<ManagementReceipt>{this.live();if(this.pending)throw Error('MANAGEMENT_PENDING');const wire=encodeManagement(input);this.pending={input:structuredClone(input),...wire,key:this.key()};return this.send();}
 async retry():Promise<ManagementReceipt>{this.live();if(!this.pending||this.status!=='uncertain')throw Error('MANAGEMENT_RETRY_UNAVAILABLE');return this.send();}
 private async send():Promise<ManagementReceipt>{const pending=this.pending!;this.status='saving';this.controller=new AbortController();
  try{const token=this.token();if(!token)throw new SessionDenied();const signal=AbortSignal.any([this.controller.signal,this.deadline()]);const r=await this.transport(`./api.cgi?action=${pending.route}`,{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':pending.key},body:pending.body});this.live();signal.throwIfAborted();if(r.status===401||r.status===403)throw new SessionDenied();const body=await readManagementJSON(r);this.live();signal.throwIfAborted();
   if(!r.ok){const code=body?.contractVersion===1?body.error?.code:null;const allowed=r.status===400?['INVALID_REQUEST']:r.status===404?['PROFILE_NOT_FOUND']:r.status===409?['PROFILE_REVISION_CONFLICT','PROFILE_JOB_ACTIVE','PROFILE_REBIND_REQUIRED','JOB_CONFLICT','IDEMPOTENCY_CONFLICT','QUEUE_FULL']:[];if(allowed.includes(code)){this.pending=null;this.status='idle';throw Error(code);}throw Error('MANAGEMENT_UNAVAILABLE');}
   const result=checkedManagementReceipt(body,pending.input);this.pending=null;this.status='idle';return result;
  }catch(e){this.live();if(this.pending)this.status='uncertain';throw e;}finally{this.controller=null;}
 }
}
// Cleanup progress is a fresh observation, never inferred from the immutable delete receipt.
export async function readProfileCleanup(profileId:string,signal:AbortSignal,transport:typeof fetch=(input,init)=>fetch(input,init),token=currentToken()):Promise<CleanupObservation>{
 if(!managementID(profileId))throw Error('INVALID_REQUEST');signal.throwIfAborted();if(!token)throw new SessionDenied();const r=await transport(`./api.cgi?action=profile-cleanup&profileId=${encodeURIComponent(profileId)}`,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(10000)]),headers:{Accept:'application/json','X-SYNO-TOKEN':token}});signal.throwIfAborted();if(r.status===401||r.status===403)throw new SessionDenied();const e=await readManagementJSON(r);signal.throwIfAborted();const d=e?.data;
 if(!r.ok||e.contractVersion!==1||d?.profileId!==profileId||d.deleted!==true||d.scope!=='sealed-generations'||!Number.isSafeInteger(d.pendingGenerations)||d.pendingGenerations<0||d.complete!==(d.pendingGenerations===0))throw Error('MANAGEMENT_UNAVAILABLE');return d;
}

export interface ProfileCardSummary {
 profileId:string;currentRevision:number;snapshotId:string|null;capturedRevision:number|null;endedAt:string|null;
 quality:'missing'|'unverified'|'complete'|'corrupt'|'unsupported';freshness:'unknown'|'current'|'stale';ageSeconds:string|null;
 integrityQuality:'unknown'|'healthy'|'corrupt'|'unsupported'|'unverified'|'deleting'|'deleted';integrityCheckedAt:string|null;
 lastAttempt:{jobId:string;state:string;scanMode:string;startedAt:string|null;endedAt:string|null;snapshotState:''|'complete'|'partial'}|null;
}
const date=(x:unknown)=>x===null||(typeof x==='string'&&x.length<=40&&Number.isFinite(Date.parse(x)));
const phases=['queued','preflight','scanning','paused','aggregating','validating','publishing','completed','failed','cancelled','interrupted'];
// Ordered identity validation prevents a summary from being displayed under another card.
export function checkedProfileSummaries(raw:unknown,ids:string[]):ProfileCardSummary[]{
 const e=object(raw),d=object(e.data);if(e.contractVersion!==1||typeof d.observedAt!=='string'||!date(d.observedAt)||!Array.isArray(d.profiles)||d.profiles.length!==ids.length)throw Error('MANAGEMENT_RESPONSE_INVALID');
 return d.profiles.map((s:ProfileCardSummary,i:number)=>{
  if(!s||s.profileId!==ids[i]||!validRevision(s.currentRevision)||(s.snapshotId!==null&&!managementID(s.snapshotId))||(s.capturedRevision!==null&&!validRevision(s.capturedRevision))||!date(s.endedAt)||!['missing','unverified','complete','corrupt','unsupported'].includes(s.quality)||!['unknown','current','stale'].includes(s.freshness)||(s.ageSeconds!==null&&(typeof s.ageSeconds!=='string'||!/^(0|[1-9][0-9]{0,39})$/.test(s.ageSeconds)))||!['unknown','healthy','corrupt','unsupported','unverified','deleting','deleted'].includes(s.integrityQuality)||!date(s.integrityCheckedAt))throw Error('MANAGEMENT_RESPONSE_INVALID');
  // Historical publication can be qualified without a recorded capture-end timestamp.
  if(s.quality==='complete'&&(!s.snapshotId||s.capturedRevision===null))throw Error('MANAGEMENT_RESPONSE_INVALID');
  if(s.lastAttempt!==null){const j=s.lastAttempt;if(!j||!managementID(j.jobId)||!phases.includes(j.state)||!['full','reconcile','rebuild'].includes(j.scanMode)||!date(j.startedAt)||!date(j.endedAt)||!['','complete','partial'].includes(j.snapshotState))throw Error('MANAGEMENT_RESPONSE_INVALID');}
  return s;
 });
}
// This guarded POST is read-only; it neither obtains a view lease nor starts any job.
export async function loadProfileSummaries(ids:string[],signal:AbortSignal,transport:typeof fetch=(input,init)=>fetch(input,init),token=currentToken()):Promise<ProfileCardSummary[]>{
 if(ids.length>32||new Set(ids).size!==ids.length||!ids.every(managementID))throw Error('INVALID_REQUEST');if(!ids.length)return[];if(!token)throw new SessionDenied();const bounded=AbortSignal.any([signal,AbortSignal.timeout(10000)]);bounded.throwIfAborted();
 const r=await transport('./api.cgi?action=profile-summaries',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:bounded,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileIds:ids})});bounded.throwIfAborted();if(r.status===401||r.status===403)throw new SessionDenied();const e=await readManagementJSON(r);bounded.throwIfAborted();if(!r.ok)throw Error('MANAGEMENT_UNAVAILABLE');return checkedProfileSummaries(e,ids);
}
