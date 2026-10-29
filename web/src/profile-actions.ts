import type {Profile} from './contracts';
import {currentToken,SessionDenied} from './session';

export type ProfileConfiguration=Omit<Profile,'id'|'revision'>;
export type ProfileSave={contractVersion:1;operation:'create'|'update';profileId?:string;expectedRevision?:number;configuration:ProfileConfiguration};
export type ProfileActionStatus='idle'|'saving'|'uncertain'|'conflict';
const id=(x:unknown):x is string=>typeof x==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(x);
const bytes=(x:string)=>new TextEncoder().encode(x).length;
const unicode=(x:string)=>!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(x);
export class ProfileActionError extends Error{}
// The browser checks transport/shape bounds; the server remains authoritative for rule syntax and root binding.
export function validateConfiguration(c:ProfileConfiguration):void {
 if(!c||typeof c.name!=='string'||!unicode(c.name)||!c.name.trim()||bytes(c.name)>1024||/[\u0000-\u001f\u007f-\u009f]/u.test(c.name))throw new ProfileActionError('PROFILE_NAME_INVALID');
 if(!c.scope||!['nas','volumes','shares','mixed','folder'].includes(c.scope.kind)||!Array.isArray(c.scope.targetIds)||c.scope.targetIds.length>256||new Set(c.scope.targetIds).size!==c.scope.targetIds.length||!c.scope.targetIds.every(id)||(c.scope.kind==='nas'?c.scope.targetIds.length!==0:c.scope.targetIds.length===0))throw new ProfileActionError('PROFILE_SCOPE_INVALID');
 // Folder roots retain their relative path and remain bound to exactly one share identity.
 if(c.scope.kind==='folder'&&(c.scope.targetIds.length!==1||typeof c.scope.relativePath!=='string'||!unicode(c.scope.relativePath)||bytes(c.scope.relativePath)>4096||/[\\\p{Cc}]/u.test(c.scope.relativePath)||c.scope.relativePath.split('/').length>256||c.scope.relativePath.split('/').some(p=>!p||p==='.'||p==='..')))throw new ProfileActionError('PROFILE_SCOPE_INVALID');
 if(typeof c.active!=='boolean'||!['gentle','balanced','fast'].includes(c.resourceMode)||!c.limits||c.limits.workers!==1||c.limits.ioBytesPerSecond!==null||c.limits.cpuPercent!==null)throw new ProfileActionError('PROFILE_RESOURCE_UNSUPPORTED');
 if(!Number.isSafeInteger(c.freshnessSeconds)||c.freshnessSeconds<0)throw new ProfileActionError('PROFILE_FRESHNESS_INVALID');
 if(!Array.isArray(c.exclusions)||c.exclusions.length>256)throw new ProfileActionError('PROFILE_RULES_INVALID');
 let total=0;for(const r of c.exclusions){if(!r||!['path','pattern'].includes(r.kind)||typeof r.value!=='string'||!unicode(r.value)||!r.value||bytes(r.value)>4096)throw new ProfileActionError('PROFILE_RULES_INVALID');total+=bytes(r.value);}
 if(total>16384)throw new ProfileActionError('PROFILE_RULES_INVALID');
}
// Only declared configuration fields enter the wire payload; IDs and bindings are server-owned.
export function profileConfiguration(p:ProfileConfiguration):ProfileConfiguration{return structuredClone({name:p.name,scope:{kind:p.scope.kind,targetIds:p.scope.targetIds,...(p.scope.kind==='folder'?{relativePath:p.scope.relativePath}:{})},active:p.active,exclusions:p.exclusions.map(r=>({kind:r.kind,value:r.value})),resourceMode:p.resourceMode,limits:{workers:p.limits.workers,ioBytesPerSecond:p.limits.ioBytesPerSecond,cpuPercent:p.limits.cpuPercent},freshnessSeconds:p.freshnessSeconds});}
// New profiles use the bounded fast preset; existing captured policies remain unchanged.
export function newProfileConfiguration():ProfileConfiguration{return {name:'',scope:{kind:'shares',targetIds:[]},active:true,exclusions:[],resourceMode:'fast',limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null},freshnessSeconds:86400};}
// Canonical comparison ignores order-only serialization, not configuration differences.
export function configurationKey(p:ProfileConfiguration):string{const c=profileConfiguration(p);c.scope.targetIds.sort();c.exclusions.sort((a,b)=>a.kind.localeCompare(b.kind)||a.value.localeCompare(b.value));return JSON.stringify(c);}
export function encodeProfileSave(profile:Profile|null,configuration:ProfileConfiguration):string {
 validateConfiguration(configuration);if(profile&&(!id(profile.id)||!Number.isSafeInteger(profile.revision)||profile.revision<1||profile.revision>2147483647))throw new ProfileActionError('PROFILE_IDENTITY_INVALID');
 const body=JSON.stringify({contractVersion:1,operation:profile?'update':'create',...(profile?{profileId:profile.id,expectedRevision:profile.revision}:{}),configuration:profileConfiguration(configuration)} satisfies ProfileSave);
 if(bytes(body)>16384)throw new ProfileActionError('PROFILE_REQUEST_TOO_LARGE');return body;
}
// The bounded streaming reader never buffers arbitrary server errors or private diagnostics.
async function readJSON(r:Response):Promise<any>{if(!r.body)throw new ProfileActionError('PROFILE_RESPONSE_INVALID');const reader=r.body.getReader(),chunks:Uint8Array[]=[];let n=0;try{for(;;){const x=await reader.read();if(x.done)break;n+=x.value.length;if(n>65536){await reader.cancel();throw new ProfileActionError('PROFILE_RESPONSE_INVALID');}chunks.push(x.value);}}finally{reader.releaseLock();}const all=new Uint8Array(n);let i=0;for(const c of chunks){all.set(c,i);i+=c.length;}try{return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(all));}catch{throw new ProfileActionError('PROFILE_RESPONSE_INVALID');}}
// One unresolved intent survives retries; a new editor draft cannot replace its immutable bytes or key.
export class ProfileActionClient {
 private pending:{body:string;key:string}|null=null;private disposed=false;private controller:AbortController|null=null;
 status:ProfileActionStatus='idle';
 // Invoke native fetch as a global function, never with this client as its receiver.
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private key:()=>string=()=>crypto.randomUUID(),private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 get unresolved(){return this.pending!==null;}
 async save(profile:Profile|null,configuration:ProfileConfiguration):Promise<Profile>{this.live();if(this.pending||this.controller)throw new ProfileActionError('PROFILE_PENDING');if(this.status==='conflict')throw new ProfileActionError('PROFILE_RELOAD_REQUIRED');this.pending=Object.freeze({body:encodeProfileSave(profile,configuration),key:this.key()});return this.send();}
 // Recovery imports only a server-confirmed create intent and preserves its exact canonical wire bytes.
 async submitPreparedCreate(body:string,key:string):Promise<Profile>{
  this.live();if(this.pending||this.controller)throw new ProfileActionError('PROFILE_PENDING');
  if(!id(key)||bytes(body)>16384)throw new ProfileActionError('PROFILE_REQUEST_TOO_LARGE');
  let request:ProfileSave;try{request=JSON.parse(body);}catch{throw new ProfileActionError('PROFILE_IDENTITY_INVALID');}
  if(!request||request.contractVersion!==1||request.operation!=='create'||Object.keys(request).some(k=>!['contractVersion','operation','configuration'].includes(k)))throw new ProfileActionError('PROFILE_IDENTITY_INVALID');
  validateConfiguration(request.configuration);
  // Go canonical JSON may escape characters differently; do not reserialize persisted request bytes.
  this.pending=Object.freeze({body,key});return this.send();
 }
 async retry():Promise<Profile>{this.live();if(!this.pending||this.status!=='uncertain')throw new ProfileActionError('PROFILE_RETRY_UNAVAILABLE');return this.send();}
 // Disposal is used only for session teardown, never to pretend that a remote mutation was cancelled.
 dispose(){this.disposed=true;this.controller?.abort();this.controller=null;this.pending=null;}
 private live(){if(this.disposed)throw new ProfileActionError('PROFILE_DISPOSED');}
 private async send():Promise<Profile>{
  const intent=this.pending!;this.status='saving';this.controller=new AbortController();
  try{
   const token=this.token();if(!token)throw new SessionDenied('SESSION_REQUIRED');
   const response=await this.transport('./api.cgi?action=profile-action',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([this.controller.signal,this.deadline()]),headers:{'Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':intent.key,Accept:'application/json'},body:intent.body});this.live();
   if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
   const envelope=await readJSON(response);this.live();
   if(!response.ok){const code=envelope?.contractVersion===1?envelope.error?.code:undefined;
    const known=['INVALID_REQUEST','PROFILE_NOT_FOUND','PROFILE_REVISION_CONFLICT','PROFILE_JOB_ACTIVE','JOB_CONFLICT','PROFILE_REBIND_REQUIRED','IDEMPOTENCY_CONFLICT'];
    if([400,404,409].includes(response.status)&&known.includes(code)){this.pending=null;this.status=response.status===409?'conflict':'idle';throw new ProfileActionError(code);}
    throw new ProfileActionError('PROFILE_UNAVAILABLE');
   }
   const submitted=JSON.parse(intent.body) as ProfileSave,result=envelope?.data,p=result?.profile as Profile;
   if(envelope?.contractVersion!==1||result?.operation!==submitted.operation||!p||!id(p.id)||result.profileId!==p.id||!Number.isSafeInteger(p.revision)||p.revision<1||p.revision>2147483647||result.cleanupScheduled!==false||typeof result.tasksRequireReview!=='boolean')throw new ProfileActionError('PROFILE_RESPONSE_INVALID');
   validateConfiguration(p);if(configurationKey(p)!==configurationKey(submitted.configuration)||(submitted.operation==='create'?p.revision!==1:p.id!==submitted.profileId||![submitted.expectedRevision,submitted.expectedRevision!+1].includes(p.revision)))throw new ProfileActionError('PROFILE_RESPONSE_INVALID');
   this.pending=null;this.status='idle';return structuredClone(p);
  }catch(error){this.live();if(this.pending)this.status='uncertain';throw error;}finally{this.controller=null;}
 }
}
