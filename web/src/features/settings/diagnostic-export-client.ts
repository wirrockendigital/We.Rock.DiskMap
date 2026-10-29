import {currentToken,SessionDenied} from '../../session';
export interface DiagnosticPreview {token:string;digest:string;documentSha256:string;expiresAt:string;includePrivatePaths:boolean;document:Record<string,unknown>;includedPrivatePaths:number;omittedPrivatePaths:number}
const invalid=()=>{throw Error('DIAGNOSTIC_RESPONSE_INVALID');};
// The server and CGI validate the closed document; this client additionally binds receipt and privacy option.
export function checkedDiagnosticPreview(value:unknown,privatePaths:boolean):DiagnosticPreview{
 const p=value as DiagnosticPreview;
 if(!p||typeof p!=='object'||Array.isArray(p)||Object.keys(p).sort().join(',')!==['token','digest','documentSha256','expiresAt','includePrivatePaths','document','includedPrivatePaths','omittedPrivatePaths'].sort().join(','))invalid();
 if(!/^[a-f0-9]{64}$/.test(p.documentSha256)||!/^[a-f0-9]{64}$/.test(p.token)||!/^[a-f0-9]{64}$/.test(p.digest)||typeof p.expiresAt!=='string'||!Number.isFinite(Date.parse(p.expiresAt))||p.includePrivatePaths!==privatePaths||!p.document||typeof p.document!=='object'||Array.isArray(p.document))invalid();
 if(!Number.isSafeInteger(p.includedPrivatePaths)||!Number.isSafeInteger(p.omittedPrivatePaths)||p.includedPrivatePaths<0||p.omittedPrivatePaths<0||p.includedPrivatePaths+p.omittedPrivatePaths>128||(!privatePaths&&p.includedPrivatePaths!==0))invalid();
 return p;
}
// Session disposal and an absolute request deadline dominate late successes and authentication errors.
export class DiagnosticExportClient {
 private controller:AbortController|null=null;private disposed=false;
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken,private deadline:()=>AbortSignal=()=>AbortSignal.timeout(10000)){}
 dispose(){this.disposed=true;this.controller?.abort();}
 async preview(includePrivatePaths:boolean):Promise<DiagnosticPreview>{const raw=await this.request('preview',{contractVersion:1,includePrivatePaths},266240);let v:any;try{v=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));}catch{invalid();}if(v.contractVersion!==1)invalid();return checkedDiagnosticPreview(v.data,includePrivatePaths);}
 async download(p:DiagnosticPreview,consent:boolean):Promise<Uint8Array>{if(p.includePrivatePaths&&!consent)throw Error('DIAGNOSTIC_CONSENT_REQUIRED');return this.request('download',{contractVersion:1,includePrivatePaths:p.includePrivatePaths,token:p.token,digest:p.digest,documentSha256:p.documentSha256,consentPrivatePaths:p.includePrivatePaths&&consent},524288);}
 private async request(action:'preview'|'download',body:unknown,limit:number):Promise<Uint8Array>{
  if(this.disposed||this.controller)throw Error('DIAGNOSTIC_UNAVAILABLE');
  const controller=new AbortController();this.controller=controller;const signal=AbortSignal.any([controller.signal,this.deadline()]),token=this.token();
  const live=()=>{if(this.disposed||this.token()!==token)throw Error('DIAGNOSTIC_DISPOSED');signal.throwIfAborted();};
  let rejectAbort=()=>{};const aborted=new Promise<never>((_,reject)=>{rejectAbort=()=>reject(Error('DIAGNOSTIC_ABORTED'));signal.addEventListener('abort',rejectAbort,{once:true});if(signal.aborted)rejectAbort();});
  const wait=async<T,>(p:Promise<T>)=>{try{const v=await Promise.race([p,aborted]);live();return v;}catch(e){live();throw e;}};
  try{
   live();if(!token)throw new SessionDenied();
   const r=await wait(this.transport('./api.cgi?action=diagnostic-export-'+action,{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(body)}));
   if(r.status===401||r.status===403)throw new SessionDenied();if(!r.ok)throw Error(r.status===409?'DIAGNOSTIC_PREVIEW_EXPIRED':'DIAGNOSTIC_UNAVAILABLE');
   if(action==='download'&&r.headers.get('Content-Type')?.split(';')[0]!=='application/zip')invalid();if(!r.body)invalid();
   const reader=r.body!.getReader(),parts:Uint8Array[]=[];let size=0;
   try{for(;;){const v=await wait(reader.read());if(v.done)break;size+=v.value.length;if(size>limit)invalid();parts.push(v.value);}}finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
   const raw=new Uint8Array(size);let offset=0;for(const part of parts){raw.set(part,offset);offset+=part.length;}live();return raw;
  }finally{signal.removeEventListener('abort',rejectAbort);if(this.controller===controller)this.controller=null;}
 }
}
