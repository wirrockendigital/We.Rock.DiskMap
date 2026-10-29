import {currentToken,sessionRequest,SessionDenied} from './session';
export interface SnapshotUsage {bytes:string;from:number;to:number;calculatedAt:number}
type RecordValue=Record<string,unknown>;
const record=(v:unknown):v is RecordValue=>!!v&&typeof v==='object'&&!Array.isArray(v);
export type SnapshotShareState='readable'|'missing'|'denied'|'unknown';
export interface SnapshotShareStatus {state:SnapshotShareState;supported:boolean|null;scheduled:boolean|null;count:number|null}
// Unknown and denied observations must never manufacture a disabled snapshot configuration.
export const unknownSnapshotStatus=():SnapshotShareStatus=>({state:'unknown',supported:null,scheduled:null,count:null});
export class SnapshotAccessDenied extends Error {}
// Keep both read paths under the same bounded, session-bound DSM transport.
export async function snapshotClient(share:string,signal:AbortSignal,request:typeof fetch){
 if(!share||share.length>256||/[\x00-\x1f/]/.test(share))throw Error('INVALID_SHARE');
 const captured=currentToken();if(!captured)throw new SessionDenied('SESSION_REQUIRED');const token:string=captured;
 const bounded=AbortSignal.any([signal,AbortSignal.timeout(10000)]);
 const check=()=>{bounded.throwIfAborted();if(currentToken()!==token)throw new SessionDenied('SESSION_CHANGED');};
 await sessionRequest('session',bounded,token,request);check();
 // Bound response memory independently of the upstream report's snapshot count.
 async function call(url:string,body?:URLSearchParams):Promise<RecordValue>{
  const response=await request(url,{method:body?'POST':'GET',body,credentials:'same-origin',cache:'no-store',redirect:'error',signal:bounded,headers:{'X-SYNO-TOKEN':token,Accept:'application/json'}});check();
  if(response.status===403)throw new SnapshotAccessDenied();
  if(!response.ok||!response.body)throw Error('SNAPSHOT_REPORT_UNAVAILABLE');
  const reader=response.body.getReader(),chunks:Uint8Array[]=[];let size=0;
  try{while(true){const next=await reader.read();check();if(next.done)break;size+=next.value.length;if(size>1048576)throw Error('SNAPSHOT_REPORT_TOO_LARGE');chunks.push(next.value);}}finally{void reader.cancel().catch(()=>{});reader.releaseLock();}
  const bytes=new Uint8Array(size);let offset=0;for(const c of chunks){bytes.set(c,offset);offset+=c.length;}
  const value:unknown=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));check();
  if(record(value)&&value.success===false&&record(value.error)&&value.error.code===105)throw new SnapshotAccessDenied();
  if(!record(value)||value.success!==true||!record(value.data))throw Error('SNAPSHOT_REPORT_UNAVAILABLE');return value.data;
 }
 return call;
}
interface SnapshotAPIObservation {token:string;expires:number;info:Promise<RecordValue>}
const snapshotAPIs=new WeakMap<typeof fetch,SnapshotAPIObservation>();
// Reuse API metadata only, never a session verdict or a share's permission result.
function snapshotAPI(call:(url:string)=>Promise<RecordValue>,request:typeof fetch):Promise<RecordValue>{
 const token=currentToken();if(!token)return Promise.reject(new SessionDenied('SESSION_REQUIRED'));
 const known=snapshotAPIs.get(request);if(known?.token===token&&known.expires>Date.now())return known.info;
 const entry:SnapshotAPIObservation={token,expires:Date.now()+60000,info:call('/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.Core.Share')};
 snapshotAPIs.set(request,entry);void entry.info.catch(()=>{if(snapshotAPIs.get(request)===entry)snapshotAPIs.delete(request);});return entry.info;
}
// Core.Share snapshot_info is the same configuration used by Snapshot Replication.
export async function readSnapshotShareStatus(share:string,signal:AbortSignal,request:typeof fetch=fetch):Promise<SnapshotShareStatus>{
 try{
  const call=await snapshotClient(share,signal,request);
  const info=await snapshotAPI(call,request);
  const api=info['SYNO.Core.Share'];
  if(!record(api)||api.path!=='entry.cgi'||typeof api.minVersion!=='number'||typeof api.maxVersion!=='number'||api.minVersion>1||api.maxVersion<1)return unknownSnapshotStatus();
  const data=await call('/webapi/entry.cgi',new URLSearchParams({api:'SYNO.Core.Share',version:'1',method:'get',name:JSON.stringify(share),additional:JSON.stringify(['support_snapshot','snapshot_schedule','snapshot_count'])}));
  if(data.name!==share||typeof data.support_snapshot!=='boolean')return unknownSnapshotStatus();
  if(!data.support_snapshot)return {state:'missing',supported:false,scheduled:null,count:null};
  const snap=data.snapshot_info;
  if(!record(snap)||!Number.isSafeInteger(snap.snapshot_count)||(snap.snapshot_count as number)<0)return unknownSnapshotStatus();
  if(snap.schedule!==undefined&&snap.schedule!==null&&!record(snap.schedule))return unknownSnapshotStatus();
  const scheduled=record(snap.schedule),count=snap.snapshot_count as number;
  return {state:scheduled||count>0?'readable':'missing',supported:true,scheduled,count};
 }catch(error){
  signal.throwIfAborted();
  if(error instanceof SessionDenied)throw error;
  return {...unknownSnapshotStatus(),state:error instanceof SnapshotAccessDenied?'denied':'unknown'};
 }
}
// Read the DSM-owned estimate only; never traverse snapshot copies or start a calculation implicitly.
export async function readSnapshotUsage(share:string,signal:AbortSignal,request:typeof fetch=fetch):Promise<SnapshotUsage|null>{
 const call=await snapshotClient(share,signal,request);
 const info=await call('/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.Snap.Usage.Share');
 const api=info['SYNO.Snap.Usage.Share'];
 if(!record(api)||api.path!=='entry.cgi'||typeof api.minVersion!=='number'||typeof api.maxVersion!=='number'||api.minVersion>1||api.maxVersion<1)throw Error('SNAPSHOT_REPORT_UNAVAILABLE');
 // DSM advertises JSON-formatted parameter values for this API, including share names.
 const params=(method:string)=>new URLSearchParams({api:'SYNO.Snap.Usage.Share',version:'1',method,share_name:JSON.stringify(share)});
 const status=await call('/webapi/entry.cgi',params('status'));
 if(!Array.isArray(status.share_list))throw Error('INVALID_SNAPSHOT_REPORT');
 if(!status.share_list.some(v=>record(v)&&v.share_name===share&&v.status==='done'))return null;
 const report=await call('/webapi/entry.cgi',params('get_report'));
 for(const field of ['estimated_in_bytes','range_start','range_end','end_task'])if(!Number.isSafeInteger(report[field])||(report[field] as number)<0)throw Error('INVALID_SNAPSHOT_REPORT');
 // Reject timestamps outside the JavaScript date range before the UI formats them.
 for(const field of ['range_start','range_end','end_task'])if((report[field] as number)>8640000000000)throw Error('INVALID_SNAPSHOT_REPORT');
 if(report.status!=='done'||(report.range_end as number)<(report.range_start as number))throw Error('INVALID_SNAPSHOT_REPORT');
 return {bytes:String(report.estimated_in_bytes),from:report.range_start as number,to:report.range_end as number,calculatedAt:report.end_task as number};
}
