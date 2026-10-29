import { validNodeProvenance, validSourceAvailability, type QuerySourceAvailability, type QueryProvenance } from './query-provenance';
import type { PreferenceView } from './contracts';
import type { ViewPin } from './snapshot-reader';
import { responseJSON } from './preference-client';
import { checkedSearchListPage,type SearchListPage } from './features/searchlist/types';
import { currentToken, SessionDenied } from './session';

export interface AnalysisSummary { knownBytes:string;unknownFileCount:string;fileCount:string;directoryCount:string;specialCount:string }
// Preserve server-qualified group continuation and historical source identifiers for both maps.
// Unknown modification metadata and virtual containers carry no inferred timestamp.
export interface AnalysisNode { colorBranchKey?:string|null;modifiedAt?:string|null;volumeId?:string;shareId?:string|null;groupReason?:string;groupedChildren?:string;listRootId?:string;afterCursor?:string; provenance?:QueryProvenance;classification?:unknown; id:string;parentId:string|null;realNodeId:string|null;name:string;kind:string;virtual:boolean;summary:AnalysisSummary;hasMore:boolean;originalSummary?:AnalysisSummary;matchSummary?:AnalysisSummary;matchKind?:string }
export interface AnalysisHierarchy { selectionOrder?:string; sourceAvailability?:QuerySourceAvailability; mode?:string;depth?:number;elementBudget?:number;filter?:PreferenceView['filter'];snapshotId:string;rootId:string;metric:string;nodes:AnalysisNode[];truncated:boolean;state:string;originalSummary:AnalysisSummary;matchSummary:AnalysisSummary;denominator?:AnalysisSummary;matchState?:string;coverage?:unknown[] }
const decimal=(v:unknown)=>typeof v==='string'&&/^(0|[1-9][0-9]{0,127})$/.test(v);
const summary=(v:AnalysisSummary)=>v&&[v.knownBytes,v.unknownFileCount,v.fileCount,v.directoryCount,v.specialCount].every(decimal);
// Normalize only server-documented defaults and ordering; preserve nanosecond time boundaries.
export function filterKey(filter:PreferenceView['filter']):string {
 if(!filter||!Array.isArray(filter.categories))throw new Error('INVALID_RESPONSE');
 const time=(value:string|undefined)=>{if(value===undefined)return null;const fraction=value.match(/\.(\d+)/)?.[1]??'';return[Date.parse(value.replace(/\.\d+/,'')),fraction.padEnd(9,'0')];};
 return JSON.stringify({categories:[...filter.categories].sort(),objectKinds:[...(filter.objectKinds??[])].sort(),text:filter.text??'',field:filter.field??'both',minSize:filter.minSize??null,maxSize:filter.maxSize??null,modifiedFrom:time(filter.modifiedFrom),modifiedTo:time(filter.modifiedTo)});
}
export interface AnalysisRenderBudget {depth:number;elementBudget:number}
// Only the exact qualified view may supply a rendered hierarchy; response bytes are capped before JSON parsing.
export async function readAnalysis(view:PreferenceView,pin:ViewPin,signal:AbortSignal,request:typeof fetch=fetch,budget:AnalysisRenderBudget={depth:1,elementBudget:200}):Promise<AnalysisHierarchy|null>{
 if(!Number.isInteger(budget.depth)||budget.depth<1||budget.depth>8||!Number.isInteger(budget.elementBudget)||budget.elementBudget<1||budget.elementBudget>(view.map==='radial'?2000:10000))throw new Error('INVALID_RENDER_BUDGET');
 if(pin.profileId!==view.profileId||pin.snapshotId!==view.snapshotId)throw new Error('SNAPSHOT_MISMATCH');
 signal=AbortSignal.any([signal,AbortSignal.timeout(8000)]);
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 await qualifyMetric(view,pin,signal,request);
 const filter=effectiveAnalysisFilter(view);
 const response=await request('./api.cgi?action=hierarchy',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(8000)]),headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileId:view.profileId,viewId:pin.viewId,query:{snapshotId:view.snapshotId,rootId:view.rootId,metric:view.metric,mode:view.filterMode,filter,depth:budget.depth,elementBudget:budget.elementBudget}})});
 signal.throwIfAborted();
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const body=await queryEnvelope(response);signal.throwIfAborted();
 if(!response.ok||body.contractVersion!==1)throw new Error(['VIEW_EXPIRED','QUERY_BUDGET_EXCEEDED','FILTER_UNAVAILABLE'].includes(body.error?.code)?body.error.code:'INVALID_RESPONSE');
 const data=body.data as AnalysisHierarchy;
 if(data?.state==='pending'){const pending=body.data.query;if(pending?.snapshotId!==view.snapshotId||pending.rootId!==view.rootId||pending.metric!==view.metric||pending.mode!==view.filterMode||pending.depth!==budget.depth||pending.elementBudget!==budget.elementBudget||filterKey(pending.filter)!==filterKey(filter))throw new Error('INVALID_RESPONSE');return null;}
 if(!data||data.snapshotId!==view.snapshotId||data.rootId!==view.rootId||data.metric!==view.metric||data.mode!==view.filterMode||data.depth!==budget.depth||data.elementBudget!==budget.elementBudget||filterKey(data.filter!)!==filterKey(filter)||!Array.isArray(data.nodes)||data.nodes.length>budget.elementBudget||!summary(data.originalSummary)||!summary(data.matchSummary)||!summary(data.denominator!)||typeof data.truncated!=='boolean')throw new Error('INVALID_RESPONSE');
 if(data.sourceAvailability!==undefined&&!validSourceAvailability(data.sourceAvailability))throw new Error('INVALID_RESPONSE');
 const ids=new Set<string>();
 for(const node of data.nodes){if(typeof node.id!=='string'||!node.id||ids.has(node.id)||typeof node.name!=='string'||typeof node.kind!=='string'||typeof node.virtual!=='boolean'||!(node.realNodeId===null||typeof node.realNodeId==='string')||!summary(node.summary)||!validNodeProvenance(node)||(['share','other'].includes(node.kind)&&(!node.virtual||node.realNodeId!==null||node.classification!==null)))throw new Error('INVALID_RESPONSE');ids.add(node.id);}
 return data;
}

// A single text predicate drives map totals; direct scope only narrows the separately labelled list.
export function effectiveAnalysisFilter(view:PreferenceView):PreferenceView['filter'] {
 const saved=view.filter.text??'';
 if(saved&&view.search.text&&(saved!==view.search.text||(view.filter.field??'both')!==view.search.field))throw Error('FILTER_TEXT_CONFLICT');
 return view.search.text?{...view.filter,text:view.search.text,field:view.search.field}:{...view.filter};
}
// Keep the paginated list transport independent from hierarchy totals and cursor ownership.
export async function readSearchList(view:PreferenceView,pin:ViewPin,cursor:string,signal:AbortSignal,request:typeof fetch=fetch,listOptions:{matchMode?:string;foldersFirst?:boolean;categorySort?:boolean}={}):Promise<SearchListPage> {
 if(pin.profileId!==view.profileId||pin.snapshotId!==view.snapshotId)throw Error('SNAPSHOT_MISMATCH');
 effectiveAnalysisFilter(view);
 signal=AbortSignal.any([signal,AbortSignal.timeout(8000)]);
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 await qualifyMetric(view,pin,signal,request);
 const response=await request('./api.cgi?action=search',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(8000)]),headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileId:view.profileId,viewId:pin.viewId,cursor,query:{snapshotId:view.snapshotId,rootId:view.rootId,metric:view.metric,filter:view.filter,...view.search,sort:view.sort,direction:view.direction,limit:50,...listOptions}})});
 signal.throwIfAborted();
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const envelope=await queryEnvelope(response) as {contractVersion?:number;data?:unknown;error?:{code?:string}};
 if(!response.ok||envelope.contractVersion!==1)throw Error(['VIEW_EXPIRED','QUERY_BUDGET_EXCEEDED','FILTER_UNAVAILABLE'].includes(envelope.error?.code??'')?envelope.error!.code!:'INVALID_RESPONSE');
 signal.throwIfAborted();
 return checkedSearchListPage(envelope.data,{view,pin});
}

// Availability is historical method evidence, never a second source of view state or rounded sizes.
export interface MetricAvailability { allocated:boolean;reason:'METHOD_UNQUALIFIED'|null }
export async function readMetricAvailability(view:PreferenceView,pin:ViewPin,signal:AbortSignal,request:typeof fetch=fetch):Promise<MetricAvailability>{
 if(pin.profileId!==view.profileId||pin.snapshotId!==view.snapshotId)throw Error('SNAPSHOT_MISMATCH');
 signal=AbortSignal.any([signal,AbortSignal.timeout(8000)]);signal.throwIfAborted();
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await request('./api.cgi?action=size-metrics',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileId:view.profileId,viewId:pin.viewId,snapshotId:view.snapshotId,rootId:view.rootId})});
 signal.throwIfAborted();
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const envelope=await responseJSON(response) as {contractVersion?:number;data?:{snapshotId?:string;rootId?:string;defaultMetric?:string;logical?:Record<string,unknown>;allocated?:Record<string,unknown>};error?:{code?:string}};
 signal.throwIfAborted();
 if(!response.ok)throw Error(envelope.error?.code==='VIEW_EXPIRED'?'VIEW_EXPIRED':'METRIC_UNAVAILABLE');
 const d=envelope.data,l=d?.logical,a=d?.allocated;
 if(envelope.contractVersion!==1||d?.snapshotId!==view.snapshotId||d.rootId!==view.rootId||d.defaultMetric!=='logical'||!l||!a||l.selectable!==true||l.method!=='st_size'||l.reason!==null||l.qualification!==null||!decimal(l.knownBytes)||!decimal(l.unknownFileCount))throw Error('METRIC_UNAVAILABLE');
 if(a.selectable===true&&a.method==='st_blocks*512'&&a.qualification==='g06-dsm90080-kvmx64-st-blocks512-v1'&&a.reason===null&&decimal(a.knownBytes)&&decimal(a.unknownFileCount))return{allocated:true,reason:null};
 if(a.selectable===false&&a.method===null&&a.qualification===null&&a.knownBytes===null&&a.unknownFileCount===null&&a.reason==='METHOD_UNQUALIFIED')return{allocated:false,reason:'METHOD_UNQUALIFIED'};
 throw Error('METRIC_UNAVAILABLE');
}

// Both independent readers must qualify historical allocation before presenting its values.
export async function qualifyMetric(view:PreferenceView,pin:ViewPin,signal:AbortSignal,request:typeof fetch,_token?:string):Promise<void>{
 // Re-read the bound provider for every actual allocated query; selector availability is not an authority cache.
 if(view.metric==='allocated'&&!(await readMetricAvailability(view,pin,signal,request)).allocated)throw new Error('METRIC_UNAVAILABLE');
}

// Search paths and hierarchy payloads share the query budget, not the smaller preference budget.
export async function queryEnvelope(response:Response){
 // DSM may return an HTML gateway page; never expose it as a raw parser error.
 if(!response.ok&&!response.headers.get('content-type')?.includes('application/json'))throw new Error('SERVICE_UNAVAILABLE');
 if(!response.body)throw new Error('INVALID_RESPONSE');
 const reader=response.body.getReader();const chunks:Uint8Array[]=[];let length=0;
 try{for(;;){const part=await reader.read();if(part.done)break;length+=part.value.length;if(length>4*1024*1024+4096){await reader.cancel();throw new Error('INVALID_RESPONSE');}chunks.push(part.value);}}finally{reader.releaseLock();}
 const raw=new Uint8Array(length);let offset=0;for(const chunk of chunks){raw.set(chunk,offset);offset+=chunk.length;}
 return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));
}
