import type {PreferenceAppearance,PreferenceDisplay} from '../../contracts';
import type {ProfileConfiguration} from '../../profile-actions';
import {currentToken,SessionDenied} from '../../session';
export const configurationBundleLimit=4*1024*1024;
export const configurationEnvelopeLimit=configurationBundleLimit+4096;
export interface ConfigurationBundle {
 formatVersion:1;exportedAt:string;
 profiles:{sourceProfileId:string;configuration:ProfileConfiguration}[];
 global:{exclusionTemplate:{rules:{kind:'path'|'pattern';value:string}[]};retentionCompleteCount:number;fileTypeMapping:{version:1;revision:number;assignments:{extension:string;category:string}[]}};
 personal:{appearance:PreferenceAppearance;display:PreferenceDisplay;preferredProfileRef:string|null};
}
const size=(s:string)=>new TextEncoder().encode(s).length;
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&size(v)<=max&&!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(v);
const fields=(v:any,keys:string[])=>v!==null&&typeof v==='object'&&!Array.isArray(v)&&Object.keys(v).length===keys.length&&keys.every(k=>Object.hasOwn(v,k));
const invalid=()=>{throw Error('CONFIGURATION_INVALID');};
// Rule syntax is server-owned; transport bounds preserve valid literal values verbatim.
function validRules(rules:any):boolean{return Array.isArray(rules)&&rules.length<=256&&rules.every(r=>fields(r,['kind','value'])&&['path','pattern'].includes(r.kind)&&text(r.value,4096)&&!!r.value)&&rules.reduce((sum,r)=>sum+size(r.value),0)<=16384;}
// Exact nested allowlists prevent accidental export of server-only fields, even on a successful response.
export function checkedConfigurationBundle(value:unknown):ConfigurationBundle {
 const b=value as ConfigurationBundle;
 if(!fields(b,['formatVersion','exportedAt','profiles','global','personal'])||b.formatVersion!==1||!text(b.exportedAt,64)||!/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(b.exportedAt)||!Number.isFinite(Date.parse(b.exportedAt))||!Array.isArray(b.profiles)||b.profiles.length>200)invalid();
 const ids=new Set<string>();
 for(const p of b.profiles){
  if(!fields(p,['sourceProfileId','configuration'])||!id(p.sourceProfileId)||ids.has(p.sourceProfileId))invalid();ids.add(p.sourceProfileId);
  const c=p.configuration;
  if(!fields(c,['name','scope','active','exclusions','resourceMode','limits','freshnessSeconds'])||!fields(c.scope,c.scope?.kind==='folder'?['kind','targetIds','relativePath']:['kind','targetIds'])||!fields(c.limits,['workers','ioBytesPerSecond','cpuPercent']))invalid();
  // Export accepts persisted resource intent, including custom modes unavailable to the current editor.
  if(!text(c.name,1024)||!c.name.trim()||/\p{Cc}/u.test(c.name)||typeof c.active!=='boolean'||!['gentle','balanced','fast','custom'].includes(c.resourceMode)||!Number.isSafeInteger(c.freshnessSeconds)||c.freshnessSeconds<0)invalid();
  if(!['nas','volumes','shares','mixed','folder'].includes(c.scope.kind)||!Array.isArray(c.scope.targetIds)||c.scope.targetIds.length>256||new Set(c.scope.targetIds).size!==c.scope.targetIds.length||!c.scope.targetIds.every(id)||(c.scope.kind==='nas'?c.scope.targetIds.length!==0:c.scope.targetIds.length===0))invalid();
  // Portable folder intent retains its explicit share-relative path without granting current authority.
  if(c.scope.kind==='folder'&&(c.scope.targetIds.length!==1||!text(c.scope.relativePath,4096)||!c.scope.relativePath||/[\\\p{Cc}]/u.test(c.scope.relativePath)||c.scope.relativePath.split('/').length>256||c.scope.relativePath.split('/').some(part=>!part||part==='.'||part==='..')))invalid();
  const l=c.limits;if(!Number.isInteger(l.workers)||l.workers<1||l.workers>64||!(l.cpuPercent===null||(Number.isInteger(l.cpuPercent)&&l.cpuPercent>=1&&l.cpuPercent<=100))||!(l.ioBytesPerSecond===null||(typeof l.ioBytesPerSecond==='string'&&/^(0|[1-9][0-9]*)$/.test(l.ioBytesPerSecond))))invalid();
  if(!validRules(c.exclusions))invalid();
 }
 const g=b.global;
 if(!fields(g,['exclusionTemplate','retentionCompleteCount','fileTypeMapping'])||!fields(g.exclusionTemplate,['rules'])||![1,2,3,7,30].includes(g.retentionCompleteCount))invalid();
 const rules=g.exclusionTemplate.rules;
 if(!validRules(rules))invalid();
 const m=g.fileTypeMapping;
 if(!fields(m,['version','revision','assignments'])||m.version!==1||!Number.isSafeInteger(m.revision)||m.revision<1||!Array.isArray(m.assignments)||m.assignments.length>256||size(JSON.stringify(m))>32768)invalid();
 const extensions=new Set<string>();
 for(const a of m.assignments){if(!fields(a,['extension','category'])||!text(a.extension,64)||!a.extension||/[A-Z.\/\\\p{White_Space}\p{Cc}]/u.test(a.extension)||!['image','video','audio','document','archive','diskImage','database','otherUnknown'].includes(a.category)||extensions.has(a.extension))invalid();extensions.add(a.extension);}
 const p=b.personal;
 if(!fields(p,['appearance','display','preferredProfileRef'])||!(p.preferredProfileRef===null||id(p.preferredProfileRef)))invalid();
 const a=p.appearance,d=p.display;
 if(!fields(a,['language','theme','map','locale','timeZone'])||!['auto','de','en'].includes(a.language)||!['auto','light','dark'].includes(a.theme)||!['treemap','radial'].includes(a.map)||!(a.locale===null||['de-DE','en-US','en-GB'].includes(a.locale))||!(a.timeZone===null||(text(a.timeZone,128)&&/^[A-Za-z][A-Za-z0-9_+./-]*$/.test(a.timeZone)&&!a.timeZone.includes('..'))))invalid();
 if(!fields(d,['units','colorMode','detail','labels','percentages','hierarchyBorders','reducedMotion'])||!['decimal','binary'].includes(d.units)||!['folder','filetype','mtime'].includes(d.colorMode)||!(d.detail==='auto'||d.detail==='max'||(Number.isInteger(d.detail)&&Number(d.detail)>=1&&Number(d.detail)<=8))||typeof d.labels!=='boolean'||typeof d.percentages!=='boolean'||!['normal','subtle'].includes(d.hierarchyBorders)||!['auto','reduce'].includes(d.reducedMotion))invalid();
 if(size(JSON.stringify(b))>configurationBundleLimit)throw Error('CONFIGURATION_TOO_LARGE');
 return structuredClone(b);
}
// Reads are explicit, serialized and bound to the current authenticated component lifetime.
export class ConfigurationExportClient {
 private controller:AbortController|null=null;private disposed=false;
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 dispose(){this.disposed=true;this.controller?.abort();}
 async read():Promise<{bundle:ConfigurationBundle;json:string}>{
  if(this.disposed)throw Error('CONFIGURATION_DISPOSED');if(this.controller)throw Error('CONFIGURATION_PENDING');
  const controller=new AbortController();this.controller=controller;const signal=AbortSignal.any([controller.signal,this.deadline()]),initialToken=this.token();
  const live=()=>{if(this.disposed||this.token()!==initialToken)throw Error('CONFIGURATION_DISPOSED');signal.throwIfAborted();};
  let rejectAbort=()=>{};const aborted=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(Error('CONFIGURATION_ABORTED'));signal.addEventListener('abort',rejectAbort,{once:true});if(signal.aborted)rejectAbort();});
  const wait=async<T,>(promise:Promise<T>)=>{try{const result=await Promise.race([promise,aborted]);live();return result;}catch(e){live();throw e;}};
  try{
   live();if(!initialToken)throw new SessionDenied();
   const response=await wait(this.transport('./api.cgi?action=configuration-export',{method:'GET',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{Accept:'application/json','X-SYNO-TOKEN':initialToken}}));
   if(response.status===401||response.status===403)throw new SessionDenied();if(response.status===413)throw Error('CONFIGURATION_TOO_LARGE');if(!response.ok)throw Error('CONFIGURATION_UNAVAILABLE');
   if(!response.body)invalid();const reader=response.body!.getReader(),chunks:Uint8Array[]=[];let length=0;
   try{for(;;){const part=await wait(reader.read());if(part.done)break;length+=part.value.length;if(length>configurationEnvelopeLimit)throw Error('CONFIGURATION_TOO_LARGE');chunks.push(part.value);}}finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
   const bytes=new Uint8Array(length);let offset=0;for(const part of chunks){bytes.set(part,offset);offset+=part.length;}
   let envelope:any;try{envelope=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}catch{invalid();}
   if(envelope.contractVersion!==1||!fields(envelope.data,['bundle']))invalid();const bundle=checkedConfigurationBundle(envelope.data.bundle);live();
   // Download only the allowlisted bundle, never the surrounding authenticated envelope.
   const json=JSON.stringify(bundle);if(size(json)>configurationBundleLimit)throw Error('CONFIGURATION_TOO_LARGE');return{bundle,json};
  }finally{signal.removeEventListener('abort',rejectAbort);if(this.controller===controller)this.controller=null;}
 }
}
