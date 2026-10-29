import {currentToken,SessionDenied} from '../session';
import type {StorageNode} from './model';

export interface ScanPreview {sequence:number;capturedAt:string;entries:string;knownLogicalBytes:string;segments:{id:string;name:string;path:string;kind:'file'|'directory'|'group';bytes:string}[];truncated:boolean}
export interface ScanPreviewResponse {jobId:string;profileId:string;preview:ScanPreview|null}
const encoder=new TextEncoder(),identifier=/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/,decimal=/^(0|[1-9][0-9]{0,127})$/;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const fields=(v:Record<string,unknown>,keys:string[])=>Object.keys(v).length===keys.length&&keys.every(k=>Object.hasOwn(v,k));
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&encoder.encode(v).length<=max;
// Preview identities belong only to the transient job surface; none are query or action locators.
export function checkedScanPreview(value:unknown,profileId:string,jobId:string):ScanPreviewResponse {
 const bad=()=>{throw Error('INVALID_SCAN_PREVIEW');};
 if(!object(value)||!fields(value,['jobId','profileId','preview'])||value.jobId!==jobId||value.profileId!==profileId)return bad();
 const p=value.preview;if(p===null)return {profileId,jobId,preview:null};
 if(!object(p)||!fields(p,['sequence','capturedAt','entries','knownLogicalBytes','segments','truncated'])||!Number.isSafeInteger(p.sequence)||Number(p.sequence)<1||!text(p.capturedAt,64)||!Number.isFinite(Date.parse(p.capturedAt))||!text(p.entries,128)||!decimal.test(p.entries)||!text(p.knownLogicalBytes,128)||!decimal.test(p.knownLogicalBytes)||!Array.isArray(p.segments)||p.segments.length>64||typeof p.truncated!=='boolean')return bad();
 let sum=0n;const ids=new Set<string>();
 for(const s of p.segments){if(!object(s)||!fields(s,['id','name','path','kind','bytes'])||!text(s.id,128)||!/^preview-[A-Za-z0-9_-]+$/.test(s.id)||ids.has(s.id)||!text(s.name,256)||!text(s.path,768)||!['file','directory','group'].includes(String(s.kind))||!text(s.bytes,128)||!decimal.test(s.bytes))return bad();ids.add(s.id);sum+=BigInt(s.bytes);}
 if(sum!==BigInt(p.knownLogicalBytes))return bad();
 return {profileId,jobId,preview:p as unknown as ScanPreview};
}
// A dedicated read-only tree never enters the authoritative model or search/navigation controller.
export function previewTree(p:ScanPreview,language:'de'|'en'):StorageNode|null {
 if(BigInt(p.knownLogicalBytes)===0n)return null;
 return {id:'preview-root',name:language==='de'?'Bereits erfasste Daten':'Already observed data',kind:'folder',bytesExact:p.knownLogicalBytes,children:p.segments.map(s=>({id:s.id,name:s.id==='preview-remainder'?(language==='de'?'Weitere erfasste Dateien':'Other observed files'):s.name,kind:s.kind==='file'?'file':'folder',bytesExact:s.bytes}))};
}
// One owned read is cancelable across profile/job/session changes, even with a transport ignoring abort.
export class ScanPreviewClient {
 private active:AbortController|null=null;private disposed=false;
 // Keep native fetch on its Window receiver rather than invoking it as a client method.
 constructor(private transport:typeof fetch=(input,init)=>fetch(input,init),private token:()=>string|null=currentToken){}
 clear(){this.active?.abort();this.active=null;}
 dispose(){this.disposed=true;this.clear();}
 async read(profileId:string,jobId:string,outer:AbortSignal):Promise<ScanPreviewResponse>{
  this.clear();if(this.disposed||!identifier.test(profileId)||!identifier.test(jobId))throw Error('INVALID_SCAN_PREVIEW');
  const own=this.active=new AbortController(),signal=AbortSignal.any([own.signal,outer,AbortSignal.timeout(8000)]);
  const check=()=>{signal.throwIfAborted();if(this.disposed||this.active!==own)throw new DOMException('Superseded','AbortError');};
  let abort:(()=>void)|undefined;
  const work=async()=>{check();const token=this.token();if(!token)throw new SessionDenied('SESSION_REQUIRED');
   const response=await this.transport(`./api.cgi?action=scan-preview&jobId=${encodeURIComponent(jobId)}&profileId=${encodeURIComponent(profileId)}`,{signal,credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'X-SYNO-TOKEN':token,Accept:'application/json'}});check();
   if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');if(!response.ok||!response.body)throw Error('SCAN_PREVIEW_UNAVAILABLE');
   const reader=response.body.getReader(),decoder=new TextDecoder('utf-8',{fatal:true});let bytes=0,body='';
   try{while(true){const part=await reader.read();check();if(part.done)break;bytes+=part.value.byteLength;if(bytes>131072)throw Error('SCAN_PREVIEW_TOO_LARGE');body+=decoder.decode(part.value,{stream:true});}body+=decoder.decode();check();const envelope=JSON.parse(body);if(!object(envelope)||envelope.contractVersion!==1||envelope.error)throw Error('INVALID_SCAN_PREVIEW');return checkedScanPreview(envelope.data,profileId,jobId);}
   finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
  };
  try{return await Promise.race([work(),new Promise<never>((_,reject)=>{abort=()=>reject(new DOMException('Aborted','AbortError'));if(signal.aborted)abort();else signal.addEventListener('abort',abort,{once:true});})]);}
  finally{if(abort)signal.removeEventListener('abort',abort);if(this.active===own){this.active=null;own.abort();}}
 }
}
