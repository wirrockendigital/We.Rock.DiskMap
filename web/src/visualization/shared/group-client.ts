import type {PreferenceView} from '../../contracts';
import type {ViewPin} from '../../snapshot-reader';
import {effectiveAnalysisFilter,filterKey,qualifyMetric,queryEnvelope} from '../../analysis-query';
import {currentToken,SessionDenied} from '../../session';
import {adaptMapNode} from './adapter';
import {addSummaries,counters,equalSummary,parseSummary,type Continuation,type HierarchyNodeInput,type MapNode,type WireSummary} from './model';

export interface GroupPage {
 readonly snapshotId:string;readonly rootId:string;readonly metric:'logical'|'allocated';readonly mode:'highlight'|'hits-only';
 readonly items:readonly MapNode[];readonly nextCursor:string|null;readonly workLimited:boolean;
}
interface ChildPage {
 snapshotId:string;rootId:string;metric:string;mode:string;filter:PreferenceView['filter'];selectionOrder:string;
 nodes:HierarchyNodeInput[];hasMore:boolean;nextCursor:string;originalSummary:WireSummary;matchSummary:WireSummary;denominator:WireSummary;matchState:string;
}
const identifier=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=160&&!/[\u0000-\u001f]/.test(v);
const opaqueCursor=(v:unknown):v is string=>typeof v==='string'&&v.length<=160&&/^[A-Za-z0-9_-]*$/.test(v);
// Continuation is a direct-child cursor, never a search cursor or a virtual group's ID.
export async function readGroupPage(view:PreferenceView,pin:ViewPin,continuation:Continuation,cursor:string,signal:AbortSignal,request:typeof fetch=fetch):Promise<GroupPage|null> {
 if(pin.profileId!==view.profileId||pin.snapshotId!==view.snapshotId)throw Error('SNAPSHOT_MISMATCH');
 if(!identifier(continuation.rootId)||!opaqueCursor(cursor)||continuation.directChildren<=0n)throw Error('INVALID_GROUP_CONTINUATION');
 signal=AbortSignal.any([signal,AbortSignal.timeout(8000)]);signal.throwIfAborted();
 const token=currentToken();if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const groupView={...view,rootId:continuation.rootId};
 await qualifyMetric(groupView,pin,signal,request,token);
 const filter=effectiveAnalysisFilter(view),query={snapshotId:view.snapshotId,rootId:continuation.rootId,metric:view.metric,mode:view.filterMode,filter,depth:1,elementBudget:100};
 const response=await request('./api.cgi?action=children',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({contractVersion:1,profileId:view.profileId,viewId:pin.viewId,cursor,query})});
 signal.throwIfAborted();if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 const envelope=await queryEnvelope(response);signal.throwIfAborted();
 if(!response.ok||envelope.contractVersion!==1)throw Error(['VIEW_EXPIRED','FILTER_UNAVAILABLE','QUERY_BUDGET_EXCEEDED'].includes(envelope.error?.code)?envelope.error.code:'INVALID_RESPONSE');
 const data=envelope.data;
 // Pending projections carry the same exact selectors; retries remain an explicit panel action.
 if(data?.state==='pending'){
  const echo=data.query;
  if(echo?.snapshotId!==query.snapshotId||echo.rootId!==query.rootId||echo.metric!==query.metric||echo.mode!==query.mode||echo.depth!==1||echo.elementBudget!==100||filterKey(echo.filter)!==filterKey(filter))throw Error('INVALID_RESPONSE');
  return null;
 }
 return checkedGroupPage(data,groupView,filter,cursor);
}
// Page totals describe the physical parent; page members need not exhaust that parent or its remainder.
export function checkedGroupPage(data:ChildPage,view:PreferenceView,filter:PreferenceView['filter'],cursor:string):GroupPage {
 if(!data||data.snapshotId!==view.snapshotId||data.rootId!==view.rootId||data.metric!==view.metric||data.mode!==view.filterMode||filterKey(data.filter)!==filterKey(filter)||data.selectionOrder!=='recorded-rowid'||!Array.isArray(data.nodes)||data.nodes.length>100||typeof data.hasMore!=='boolean'||!opaqueCursor(data.nextCursor)||data.hasMore!==!!data.nextCursor||!['empty','no-match','matches'].includes(data.matchState))throw Error('INVALID_RESPONSE');
 const original=parseSummary(data.originalSummary),match=parseSummary(data.matchSummary),denominator=parseSummary(data.denominator);
 if(counters.some(key=>match[key]>original[key])||!equalSummary(denominator,view.filterMode==='highlight'?original:match))throw Error('INVALID_RESPONSE');
 const seen=new Set<string>(),items=data.nodes.map(raw=>{
  const node=adaptMapNode(raw,view.filterMode);
  if(node.parentId!==view.rootId||node.id===view.rootId||seen.has(node.id)||node.kind==='group'||node.kind==='nas')throw Error('INVALID_RESPONSE');
  seen.add(node.id);return node;
 });
 for(const [field,total] of [['summary',denominator],['original',original],['match',match]] as const){
  const sum=addSummaries(items.map(node=>node[field]));if(counters.some(key=>sum[key]>total[key]))throw Error('INVALID_RESPONSE');
 }
 if(items.length>0&&data.hasMore&&data.nextCursor===cursor)throw Error('INVALID_RESPONSE');
 return Object.freeze({snapshotId:view.snapshotId,rootId:view.rootId,metric:view.metric,mode:view.filterMode,items:Object.freeze(items),nextCursor:data.hasMore?data.nextCursor:null,workLimited:data.hasMore&&items.length===0});
}
