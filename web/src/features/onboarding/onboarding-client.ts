import {currentToken,SessionDenied} from '../../session';
import {responseJSON} from '../../preference-client';
import {validateConfiguration,configurationKey} from '../../profile-actions';
import type {OnboardingChange,OnboardingDraft,OnboardingRequest,OnboardingState,OnboardingResponse,OnboardingCreateRequest} from './onboarding-types';
export * from './onboarding-types';
export class OnboardingError extends Error{}
const bad=():never=>{throw new OnboardingError('ONBOARDING_RESPONSE_INVALID');};
const bytes=(s:string)=>new TextEncoder().encode(s).length;
const id=(x:unknown):x is string=>typeof x==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(x);
const integer=(x:unknown,max=2147483647):x is number=>Number.isInteger(x)&&Number(x)>=0&&Number(x)<=max;
// Closed keys prevent opaque server or caller fields from silently entering the durable draft.
function object(x:any,keys:string[]):void{if(!x||typeof x!=='object'||Array.isArray(x)||Object.keys(x).some(k=>!keys.includes(k)))bad();}
function text(x:unknown,max:number):boolean{return typeof x==='string'&&bytes(x)<=max&&!x.includes('\0')&&!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(x);}
function draft(d:OnboardingDraft|null):void{
 if(d===null)return;object(d,['step','language','configuration','profileId','updateChoice']);
 if(!integer(d.step,6)||d.step<1||!['auto','de','en'].includes(d.language)||!(d.profileId===null||id(d.profileId))||!['undecided','manual','dsm-help'].includes(d.updateChoice))bad();
 const c=d.configuration;if(c!==null){object(c,['name','scope','active','exclusions','resourceMode','limits','freshnessSeconds']);
 if(c.name!==undefined&&!text(c.name,1024))bad();if(c.active!==undefined&&typeof c.active!=='boolean')bad();
 if(c.scope!==undefined){object(c.scope,['kind','targetIds']);if(!['','nas','volumes','shares','mixed'].includes(c.scope.kind)||!Array.isArray(c.scope.targetIds)||c.scope.targetIds.length>256||new Set(c.scope.targetIds).size!==c.scope.targetIds.length||!c.scope.targetIds.every(id))bad();}
 if(c.exclusions!==undefined){if(!Array.isArray(c.exclusions)||c.exclusions.length>128)bad();for(const r of c.exclusions){object(r,['kind','value']);if(!['path','pattern'].includes(r.kind)||!text(r.value,4096))bad();}}
 if(c.resourceMode!==undefined&&!['','gentle','balanced','fast','custom'].includes(c.resourceMode))bad();
 if(c.limits!==undefined){object(c.limits,['workers','cpuPercent','ioBytesPerSecond']);if(!integer(c.limits.workers,64)||!(c.limits.cpuPercent===null||integer(c.limits.cpuPercent,100))||!(c.limits.ioBytesPerSecond===null||typeof c.limits.ioBytesPerSecond==='string'&&/^(0|[1-9][0-9]{0,127})$/.test(c.limits.ioBytesPerSecond)))bad();}
 if(c.freshnessSeconds!==undefined&&!integer(c.freshnessSeconds))bad();}
 if(bytes(JSON.stringify(d))>24576)bad();
}
// Preserve the server's canonical bytes; parsing is solely validation, never replacement serialization.
function create(payload:string):OnboardingCreateRequest{
 if(typeof payload!=='string'||bytes(payload)>16384)bad();let v:any;try{v=JSON.parse(payload);}catch{bad();}
 object(v,['contractVersion','operation','configuration']);if(v.contractVersion!==1||v.operation!=='create')bad();
 object(v.configuration,['name','scope','active','exclusions','resourceMode','limits','freshnessSeconds']);
 try{validateConfiguration(v.configuration);}catch{bad();}return v;
}
function state(v:any,read:boolean):OnboardingState|OnboardingResponse{
 object(v,['version','revision','draft','pendingProfileCreate',...(read?['profileState']:[])]);
 if(v.version!==1||!integer(v.revision))bad();draft(v.draft);
 const p=v.pendingProfileCreate;
 if(v.revision===0&&(v.draft!==null||p!==null))bad();
 if(p!==null){object(p,['key','canonicalPayload',...(read?['status','receipt','profileState','rejectionCode']:[])]);if(!id(p.key)||!v.draft||v.draft.profileId!==null)bad();const request=create(p.canonicalPayload);
 if(read){if(p.status!=='rejected'&&p.rejectionCode!==undefined)bad();if(p.status==='rejected'){if(p.receipt!==null||p.profileState!=='unknown'||!['INVALID_REQUEST','JOB_CONFLICT','PROFILE_REBIND_REQUIRED'].includes(p.rejectionCode))bad();}
 else if(p.status==='pending'){if(p.receipt!==null||p.profileState!=='unknown')bad();}
 else{if(!['committed','profile-unavailable'].includes(p.status)||(p.status==='committed'?p.profileState!=='active':!['missing','inactive','changed'].includes(p.profileState)))bad();const r=p.receipt;object(r,['operation','profileId','profile','cleanupScheduled','tasksRequireReview']);if(r.operation!=='create'||!id(r.profileId)||r.profile?.id!==r.profileId||r.profile?.revision!==1||r.cleanupScheduled!==false||r.tasksRequireReview!==false)bad();try{validateConfiguration(r.profile);if(configurationKey(r.profile)!==configurationKey(request.configuration))bad();}catch{bad();}}}}
 if(read&&(!['none','active','inactive','missing'].includes(v.profileState)||Boolean(v.draft?.profileId)===(v.profileState==='none')))bad();return structuredClone(v);
}
function equal(a:unknown,b:unknown):boolean{if(a===b)return true;if(!a||!b||typeof a!=='object'||typeof b!=='object')return false;const aa=a as Record<string,unknown>,bb=b as Record<string,unknown>;return Object.keys(aa).length===Object.keys(bb).length&&Object.keys(aa).every(k=>Object.hasOwn(bb,k)&&equal(aa[k],bb[k]));}
// Mutation receipts are bound to the submitted CAS revision and tagged operation.
export function checkedOnboarding(value:any,request?:OnboardingRequest):OnboardingState|OnboardingResponse{
 object(value,['contractVersion','requestId','data']);if(value.contractVersion!==1)bad();const v=state(value.data,!request);
 if(request){if(v.revision!==request.expectedRevision+1)bad();const c=request.change;
 if(c.operation==='replace'&&(v.pendingProfileCreate!==null||!equal(v.draft,c.draft)))bad();
 if(c.operation==='reset'&&(v.draft!==null||v.pendingProfileCreate!==null))bad();
 if(c.operation==='prepareProfileCreate'&&(!v.pendingProfileCreate||v.pendingProfileCreate.key!==c.create.key||!equal(create(v.pendingProfileCreate.canonicalPayload),c.create.request)))bad();
 if(c.operation==='acknowledgeProfileRejection'&&(v.pendingProfileCreate!==null||!v.draft||v.draft.profileId!==null))bad();
 if(c.operation==='acknowledgeProfileCreate'&&(v.pendingProfileCreate!==null||!v.draft?.profileId))bad();}return v;
}
// One outstanding CAS intent is retained verbatim until a receipt or definite rejection resolves it.
export class OnboardingClient {
 status:'idle'|'saving'|'uncertain'|'conflict'='idle';
 private pending:{body:string;key:string;rejectedDraft?:OnboardingDraft}|null=null;private observed:OnboardingResponse|null=null;private controller:AbortController|null=null;private disposed=false;private epoch=0;
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private key:()=>string=()=>crypto.randomUUID(),private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 get unresolved(){return this.pending!==null;}
 private live(epoch=this.epoch){if(this.disposed||epoch!==this.epoch)throw new OnboardingError('ONBOARDING_DISPOSED');}
 dispose(){this.disposed=true;++this.epoch;this.controller?.abort();this.controller=null;this.pending=null;}
 async read():Promise<OnboardingResponse>{this.live();if(this.pending||this.controller)throw new OnboardingError('ONBOARDING_PENDING');return this.perform() as Promise<OnboardingResponse>;}
 async change(expectedRevision:number,change:OnboardingChange):Promise<OnboardingState>{this.live();if(this.pending||this.controller)throw new OnboardingError('ONBOARDING_PENDING');if(this.status==='conflict')throw new OnboardingError('ONBOARDING_RELOAD_REQUIRED');
 if(!integer(expectedRevision)||expectedRevision===2147483647)throw new OnboardingError('INVALID_REQUEST');
 object(change,change.operation==='replace'?['operation','draft']:change.operation==='prepareProfileCreate'?['operation','create']:['operation']);
 if(change.operation==='replace'){if(!change.draft)bad();draft(change.draft);}else if(change.operation==='prepareProfileCreate'){object(change.create,['key','request']);if(!id(change.create.key))bad();create(JSON.stringify(change.create.request));}else if(!['reset','acknowledgeProfileCreate','acknowledgeProfileRejection'].includes(change.operation))bad();
 const body=JSON.stringify({contractVersion:1,expectedRevision,change});if(bytes(body)>32768)throw new OnboardingError('INVALID_REQUEST');const key=this.key();if(!id(key))throw new OnboardingError('INVALID_REQUEST');const rejectedDraft=change.operation==='acknowledgeProfileRejection'?this.observed?.pendingProfileCreate?.status==='rejected'&&this.observed.revision===expectedRevision?structuredClone(this.observed.draft!):bad():undefined;this.pending=Object.freeze({body,key,...(rejectedDraft?{rejectedDraft}:{})});return this.perform() as Promise<OnboardingState>;}
 async retry():Promise<OnboardingState>{this.live();if(!this.pending||this.controller||this.status!=='uncertain')throw new OnboardingError('ONBOARDING_RETRY_UNAVAILABLE');return this.perform() as Promise<OnboardingState>;}
 private async perform():Promise<OnboardingState|OnboardingResponse>{const pending=this.pending,epoch=++this.epoch,controller=this.controller=new AbortController();if(pending)this.status='saving';
 try{const token=this.token();if(!token)throw new SessionDenied('SESSION_REQUIRED');const signal=AbortSignal.any([controller.signal,this.deadline()]);
 const response=await this.transport('./api.cgi?action=onboarding-draft',{method:pending?'POST':'GET',signal,credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'X-SYNO-TOKEN':token,Accept:'application/json',...(pending?{'Content-Type':'application/json','Idempotency-Key':pending.key}:{})},...(pending?{body:pending.body}:{})});this.live(epoch);signal.throwIfAborted();if([401,403].includes(response.status))throw new SessionDenied('SESSION_REQUIRED');
 const raw:any=await responseJSON(response);this.live(epoch);signal.throwIfAborted();
 if(!response.ok){const code=raw?.contractVersion===1?raw.error?.code:null;if(pending&&((response.status===409&&['ONBOARDING_CONFLICT','ONBOARDING_PENDING','IDEMPOTENCY_CONFLICT'].includes(code))||(response.status===400&&code==='INVALID_REQUEST'))){this.pending=null;this.status=response.status===409?'conflict':'idle';throw new OnboardingError(code);}throw new OnboardingError('ONBOARDING_UNAVAILABLE');}
 const result=checkedOnboarding(raw,pending?JSON.parse(pending.body):undefined);if(pending?.rejectedDraft&&!equal(result.draft,pending.rejectedDraft))bad();if(!pending)this.observed=result as OnboardingResponse;else this.observed=null;this.pending=null;this.status='idle';return result;
 }catch(error){this.live(epoch);if(this.pending)this.status='uncertain';throw error;}finally{if(epoch===this.epoch)this.controller=null;}}
}
