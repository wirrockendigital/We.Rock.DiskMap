import { validNodeProvenance, validSourceAvailability, type QuerySourceAvailability, type QueryProvenance } from './query-provenance';
import type { Node, ContentAggregate } from './contracts';
import type { NodePaths } from './NodeDetails';
import { exactBytes } from './size-metrics';
import { currentToken, SessionDenied } from './session';

// A confirmed central view supplies every selector; this client never invents a second selection.
export interface DetailSelection { profileId:string; viewId:string; snapshotId:string; rootId:string; nodeId:string; metric:'logical'|'allocated'; epoch:number }
export type DetailOperation = 'details'|'direct-folders'|'recursive-files';
export interface DetailSummary { knownBytes:string; unknownFileCount:string; fileCount:string; directoryCount:string; specialCount:string }
export interface DetailMetric { selectable:boolean; knownBytes:string|null; unknownFileCount:string|null; method:string|null; reason:string|null; qualification:string|null }
export interface DetailPage { snapshotId:string; rootId:string; metric:string; scope:string; selectionOrder:string; hasMore:boolean; workLimited:boolean; nextCursor:string; items:{snapshotId:string;nodeId:string;name:string;path:string;kind:string;size:string|null;summary:DetailSummary}[] }
export interface HistoricalDetail {
 profileId:string;viewId:string;paths:NodePaths|null;pathProvenance:string;fileStationReason:string;
 detail:{sourceAvailability?:QuerySourceAvailability;provenance?:QueryProvenance;snapshotId:string;rootId:string;nodeId:string;operation:DetailOperation;metric:string;kind:string;name:string;virtual:boolean;node:Node|null;groupId:string|null;
 classification:{category:string|null;basis:string;mappingRevision:number;folderComposition:string;folderCategories:string[];contextHints:string[]}|null;
 aggregate:{direct:ContentAggregate|null;subtree:ContentAggregate;directSemantics:string}|null;
 numerator:DetailSummary;denominator:DetailSummary;basis:string;logical:DetailMetric;allocated:DetailMetric;capture:{startedAt:string;endedAt:string};profileRevision:number;snapshotState:'complete'|'partial';coverage:{targetId:string;state:string;errors:number}[];coverageTruncated:boolean;boundaryCount:string;ranking:DetailPage|null;rankingSemantics:string};
}
const categories=['image','video','audio','document','archive','diskImage','database','otherUnknown'];
const kinds=['file','directory','symlink','fifo','socket','blockDevice','characterDevice'];
const invalid=():never=>{throw new Error('DETAIL_RESPONSE_INVALID');};
const text=(v:unknown,max=32768):v is string=>typeof v==='string'&&v.length<=max;
const nullableText=(v:unknown,max=32768)=>v===null||text(v,max);
const decimal=(v:unknown)=>{try{return exactBytes(v)!==null;}catch{return false;}};
const time=(v:unknown)=>text(v,64)&&Number.isFinite(Date.parse(v));
const summary=(s:DetailSummary)=>s&&[s.knownBytes,s.unknownFileCount,s.fileCount,s.directoryCount,s.specialCount].every(decimal);
const metric=(m:DetailMetric)=>m&&typeof m.selectable==='boolean'&&nullableText(m.method,128)&&nullableText(m.reason,128)&&nullableText(m.qualification,256)&&(m.selectable?decimal(m.knownBytes)&&decimal(m.unknownFileCount)&&m.method!==null&&m.reason===null:m.knownBytes===null&&m.unknownFileCount===null&&m.method===null&&m.qualification===null&&m.reason!==null);
const content=(c:ContentAggregate):boolean=>!!c&&[c.fileCount,c.directoryCount,c.specialCount].every(decimal)&&[c.logical,c.allocated].every(m=>m&&decimal(m.knownBytes)&&decimal(m.unknownFileCount))&&!!c.types&&typeof c.types==='object'&&!Array.isArray(c.types)&&Object.entries(c.types).every(([k,v])=>categories.includes(k)&&v&&decimal(v.fileCount)&&[v.logical,v.allocated].every(m=>m&&decimal(m.knownBytes)&&decimal(m.unknownFileCount)));
// Every success is bound to the exact immutable view and closed ranking vocabulary.
export function checkedHistoricalDetail(value:unknown,s:DetailSelection,operation:DetailOperation):HistoricalDetail {
 const v=value as HistoricalDetail,d=v?.detail;
 if(!v||v.profileId!==s.profileId||v.viewId!==s.viewId||!d||d.snapshotId!==s.snapshotId||d.rootId!==s.rootId||d.nodeId!==s.nodeId||d.metric!==s.metric||d.operation!==operation||!text(d.name)||typeof d.virtual!=='boolean'||!summary(d.numerator)||!summary(d.denominator)||d.basis!=='unfiltered-recorded-path-entries'||!metric(d.logical)||!metric(d.allocated)||!d[s.metric].selectable||!d.capture||!time(d.capture.startedAt)||!time(d.capture.endedAt)||!Number.isSafeInteger(d.profileRevision)||d.profileRevision<1||!['complete','partial'].includes(d.snapshotState)||!decimal(d.boundaryCount)||typeof d.coverageTruncated!=='boolean'||!Array.isArray(d.coverage)||d.coverage.length>32||!nullableText(d.groupId,128))return invalid();
 if(d.logical.method!=='st_size'||!d.logical.selectable||d.logical.qualification!==null||(d.allocated.selectable&&(d.allocated.method!=='st_blocks*512'||d.allocated.qualification!=='g06-dsm90080-kvmx64-st-blocks512-v1')))return invalid();
 for(const c of d.coverage)if(!c||!text(c.targetId,128)||!['complete','partial'].includes(c.state)||!Number.isSafeInteger(c.errors)||c.errors<0)return invalid();
 if(['share','other'].includes(d.kind)&&(!d.virtual||d.classification!==null))return invalid();
 if(d.classification){const c=d.classification;if(!nullableText(c.category,64)||(c.category!==null&&!categories.includes(c.category))||!['extension','unknown','not-applicable'].includes(c.basis)||!Number.isSafeInteger(c.mappingRevision)||c.mappingRevision<1||!['not-directory','no-regular-files','single-category','mixed'].includes(c.folderComposition)||!Array.isArray(c.folderCategories)||c.folderCategories.length>8||!c.folderCategories.every(x=>categories.includes(x))||!Array.isArray(c.contextHints)||c.contextHints.length>8||!c.contextHints.every(x=>text(x,128)))return invalid();}
 if(d.sourceAvailability!==undefined&&!validSourceAvailability(d.sourceAvailability))return invalid();
 if(!validNodeProvenance(d))return invalid();
 if(d.virtual){if(!['nas','volume','share','other'].includes(d.kind)||d.node!==null||v.paths!==null||v.pathProvenance!=='not-applicable'||v.fileStationReason!=='VIRTUAL_NODE')return invalid();}
 else {const n=d.node,p=v.paths;if(!n||n.id!==s.nodeId||n.snapshotId!==s.snapshotId||n.kind!==d.kind||!kinds.includes(n.kind)||!text(n.originalName)||!text(n.relativePath)||!text(n.volumeId,128)||!nullableText(n.shareId,128)||!nullableText(n.ownerId,128)||!(n.modifiedAt===null||time(n.modifiedAt))||!(n.linkCount===null||(Number.isSafeInteger(n.linkCount)&&n.linkCount>=0))||![n.logicalBytes,n.allocatedBytes].every(x=>x===null||decimal(x))||!p||p.nodeId!==s.nodeId||p.snapshotId!==s.snapshotId||p.fileStationPath!==null||v.pathProvenance!=='snapshot'||v.fileStationReason!=='HISTORICAL_MAPPING_NOT_RECORDED')return invalid();for(const x of [p.name,p.nasPath])if(!x||!text(x.text)||!['utf8','escaped-bytes'].includes(x.encoding))return invalid();}
 if(d.kind==='directory'||d.virtual){if(!d.aggregate||!content(d.aggregate.subtree)||(d.virtual?d.aggregate.direct!==null||d.aggregate.directSemantics!=='virtual-children-not-file-content':!d.aggregate.direct||!content(d.aggregate.direct)||d.aggregate.directSemantics!=='immediate-real-children'))return invalid();}else if(d.aggregate!==null)return invalid();
 if(operation==='details'){if(d.ranking!==null||d.rankingSemantics!=='not-requested')return invalid();}
 else {const p=d.ranking,folder=operation==='direct-folders';if(!p||p.snapshotId!==s.snapshotId||p.rootId!==s.nodeId||p.metric!==s.metric||p.scope!==(folder?'direct':'recursive')||p.selectionOrder!=='size-desc-recorded-rowid'||d.rankingSemantics!==(folder?'direct-real-folders-disjoint-subtrees':'recursive-file-path-entries-not-exclusive-physical-bytes')||typeof p.hasMore!=='boolean'||typeof p.workLimited!=='boolean'||!text(p.nextCursor,512)||p.hasMore!==(p.nextCursor!=='')||p.workLimited&&!p.hasMore||!Array.isArray(p.items)||p.items.length>20)return invalid();const ids=new Set<string>();for(const item of p.items){if(!item||item.snapshotId!==s.snapshotId||!text(item.nodeId,160)||ids.has(item.nodeId)||item.kind!==(folder?'directory':'file')||!text(item.name)||!text(item.path)||!(item.size===null||decimal(item.size))||!summary(item.summary))return invalid();ids.add(item.nodeId);}}
 return v;
}
// Streaming bounds apply before JSON parsing, including error responses.
async function readBounded(response:Response):Promise<unknown>{if(!response.body)return invalid();const reader=response.body.getReader();let size=0;const chunks:Uint8Array[]=[];try{for(;;){const part=await reader.read();if(part.done)break;size+=part.value.byteLength;if(size>256*1024){await reader.cancel();return invalid();}chunks.push(part.value);}const bytes=new Uint8Array(size);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.length;}return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}finally{reader.releaseLock();}}
// Token and credentials remain transient and same-origin; this read never schedules a scan.
export async function requestNodeDetail(s:DetailSelection,operation:DetailOperation,cursor:string,signal:AbortSignal,transport:typeof fetch=(a,b)=>fetch(a,b),token:string|null=currentToken()):Promise<HistoricalDetail>{
 const active=()=>{if(signal.aborted)throw new Error('DETAIL_ABORTED');};active();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await transport('./api.cgi?action=node-details',{method:'POST',credentials:'same-origin',redirect:'error',cache:'no-store',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':s.viewId},body:JSON.stringify({contractVersion:1,profileId:s.profileId,viewId:s.viewId,snapshotId:s.snapshotId,rootId:s.rootId,nodeId:s.nodeId,metric:s.metric,operation,...(operation==='details'?{}:{limit:20,...(cursor?{cursor}:{})})})});
 active();if([401,403].includes(response.status))throw new SessionDenied('SESSION_REQUIRED');const envelope=await readBounded(response) as {contractVersion:number;data:unknown;error?:{code?:string}};active();if(envelope.contractVersion!==1)return invalid();if(!response.ok){const code=envelope.error?.code;throw new Error(code&&['DETAIL_UNAVAILABLE','VIEW_EXPIRED','SNAPSHOT_MISMATCH','METRIC_UNAVAILABLE','INDEX_REFRESH_REQUIRED','QUERY_BUDGET_EXCEEDED','SERVICE_UNAVAILABLE'].includes(code)?code:'DETAIL_UNAVAILABLE');}return checkedHistoricalDetail(envelope.data,s,operation);
}
// Request identity includes the central epoch, so both stale success and failure become inert.
export function detailSelectionKey(s:DetailSelection):string{return JSON.stringify([s.profileId,s.viewId,s.snapshotId,s.rootId,s.nodeId,s.metric,s.epoch]);}
export async function settleDetail<T>(promise:Promise<T>,signal:AbortSignal,key:string,current:()=>string,success:(value:T)=>void,failure:(error:unknown)=>void):Promise<void>{try{const value=await promise;if(!signal.aborted&&current()===key)success(value);}catch(error){if(!signal.aborted&&current()===key)failure(error);}}
