import type {Profile} from './contracts';
export type Exclusion=Profile['exclusions'][number];
import {currentToken,SessionDenied} from './session';
export type PreviewRequest={contractVersion:1;profileId:string;syntaxOnly:boolean;exclusions:Exclusion[];templateExclusions:Exclusion[];partialJobId?:string};
export type Preview={availablePartial:{jobId:string;profileRevision:number;endedAt:string}|null;syntaxVersion:1;valid:boolean;syntaxError?:{ruleIndex:number;code:string};effectiveExclusions:Exclusion[];warnings:string[];profileRevision:number;snapshotId:string|null;snapshotProfileRevision:number|null;snapshotState:'none'|'complete'|'partial';examined:number;matched:number;matches:{nodeId:string;targetId:string}[];truncated:boolean;matchesTruncated:boolean};
export type Template={version:1;revision:number;rules:Exclusion[]};
const obj=(x:unknown):x is Record<string,any>=>!!x&&typeof x==='object'&&!Array.isArray(x);
const integer=(x:unknown,max:number)=>Number.isSafeInteger(x)&&(x as number)>=0&&(x as number)<=max;
const rules=(x:unknown):x is Exclusion[]=>Array.isArray(x)&&x.length<=256&&x.every(r=>obj(r)&&Object.keys(r).every(k=>['kind','value'].includes(k))&&['path','pattern'].includes(r.kind)&&typeof r.value==='string'&&new TextEncoder().encode(r.value).length<=4096);
// Shape validation never implements glob semantics; the backend owns the effective rule list.
export function checkedPreview(x:unknown):Preview{
 if(!obj(x)||x.syntaxVersion!==1||typeof x.valid!=='boolean'||!rules(x.effectiveExclusions)||!Array.isArray(x.warnings)||!x.warnings.every((v:unknown)=>typeof v==='string'&&['EXCLUSIONS_WIN','RULES_APPLY_TO_EACH_NORMALIZED_ROOT','EXISTING_INDEX_ONLY','DUPLICATE_RULES_MERGED','SYNTAX_ONLY'].includes(v))||!integer(x.profileRevision,2147483647)||!['none','complete','partial'].includes(x.snapshotState)||!integer(x.examined,1000)||!integer(x.matched,x.examined)||typeof x.truncated!=='boolean'||typeof x.matchesTruncated!=='boolean'||!Array.isArray(x.matches)||x.matches.length>50||!x.matches.every((m:unknown)=>obj(m)&&typeof m.nodeId==='string'&&m.nodeId.length<=128&&typeof m.targetId==='string'&&m.targetId.length<=128))throw Error('PREVIEW_INVALID');
 if(x.availablePartial!==null&&(!obj(x.availablePartial)||typeof x.availablePartial.jobId!=='string'||!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(x.availablePartial.jobId)||!integer(x.availablePartial.profileRevision,2147483647)||typeof x.availablePartial.endedAt!=='string'||!Number.isFinite(Date.parse(x.availablePartial.endedAt))))throw Error('PREVIEW_INVALID');
 if(x.snapshotState==='none'?(x.snapshotId!==null||x.snapshotProfileRevision!==null||x.examined!==0):(typeof x.snapshotId!=='string'||!integer(x.snapshotProfileRevision,2147483647)))throw Error('PREVIEW_INVALID');
 if(!x.valid&&(!obj(x.syntaxError)||!Number.isInteger(x.syntaxError.ruleIndex)||typeof x.syntaxError.code!=='string'||!/^EXCLUSION_[A-Z_]+$/.test(x.syntaxError.code)))throw Error('PREVIEW_INVALID');
 return x as Preview;
}
// Reads are bounded during streaming and abort checks precede late authorization/error interpretation.
async function read(r:Response,signal:AbortSignal):Promise<any>{
 signal.throwIfAborted();if(r.status===401||r.status===403)throw new SessionDenied();if(!r.ok)throw Error(r.status===503?'PREVIEW_TIMEOUT_OR_UNAVAILABLE':'PREVIEW_UNAVAILABLE');
 if(!r.body)throw Error('PREVIEW_INVALID');const rd=r.body.getReader(),parts:Uint8Array[]=[];let size=0;
 try{for(;;){const x=await rd.read();signal.throwIfAborted();if(x.done)break;size+=x.value.length;if(size>65536){await rd.cancel();throw Error('PREVIEW_LIMIT')};parts.push(x.value)}}finally{rd.releaseLock()}
 const all=new Uint8Array(size);let pos=0;for(const p of parts){all.set(p,pos);pos+=p.length}const e=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(all));if(e.contractVersion!==1)throw Error('PREVIEW_INVALID');return e.data;
}
// A request owns a deadline as well as the caller's selection/session cancellation.
async function request(action:string,body:unknown,signal:AbortSignal):Promise<any>{
 const combined=AbortSignal.any([signal,AbortSignal.timeout(8000)]);try{combined.throwIfAborted();const token=currentToken();if(!token)throw new SessionDenied();const raw=body===null?undefined:JSON.stringify(body);if(raw&&new TextEncoder().encode(raw).length>65536)throw Error('PREVIEW_LIMIT');const response=await fetch(`./api.cgi?action=${action}`,{method:raw?'POST':'GET',credentials:'same-origin',cache:'no-store',redirect:'error',signal:combined,headers:{'X-SYNO-TOKEN':token,Accept:'application/json',...(raw?{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()}:{})},body:raw});return await read(response,combined)}catch(e){signal.throwIfAborted();throw e}
}
export async function previewExclusions(input:PreviewRequest,signal:AbortSignal):Promise<Preview>{return checkedPreview(await request('exclusion-preview',input,signal))}
export async function loadExclusionTemplate(signal:AbortSignal):Promise<Template>{const x=await request('exclusion-template',null,signal);if(!obj(x)||x.version!==1||!integer(x.revision,2147483647)||!rules(x.rules))throw Error('TEMPLATE_INVALID');return x as Template}
