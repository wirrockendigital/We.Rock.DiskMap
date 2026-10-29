import {validNodeProvenance,validSourceAvailability,type QuerySourceAvailability} from '../../query-provenance';
import {counters,parseSummary,addSummaries,equalSummary,immutableJSON,type HierarchyNodeInput,type HierarchyInput,type HierarchyIdentity,type MapModel,type MapNode,type NodeKind,type Summary} from './model';

const kinds:readonly string[]=['nas','volume','share','other','directory','file','symlink','fifo','socket','blockDevice','characterDevice','group'];
const virtualKinds=['nas','volume','share','other','group'];
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=160&&!/[\u0000-\u001f]/.test(v);
const bad=():never=>{throw Error('INVALID_MAP_HIERARCHY');};
// Directory summaries include the physical directory itself; virtual containers never add an entry.
function childTotal(summary:Summary,node:MapNode):Summary {
 if(node.kind!=='directory'||summary.directoryCount===0n)return summary;
 return Object.freeze({...summary,directoryCount:summary.directoryCount-1n});
}
// One node validator is shared by full hierarchy rendering and bounded direct-child pages.
export function adaptMapNode(n:HierarchyNodeInput,mode:'highlight'|'hits-only'):MapNode {
 if(!n||!['highlight','hits-only'].includes(mode))bad();
  if(!id(n.id)||!(n.parentId===null||id(n.parentId))||typeof n.name!=='string'||!kinds.includes(n.kind)||typeof n.virtual!=='boolean'||n.virtual!==virtualKinds.includes(n.kind)||typeof n.hasMore!=='boolean'||!validNodeProvenance(n)||!['direct','subtree','ancestor','none','group'].includes(n.matchKind??''))bad();
  if(n.virtual?n.realNodeId!==null:n.realNodeId!==n.id)bad();
  if(['share','other'].includes(n.kind)&&n.classification!==null)bad();
  // Missing timestamps remain unknown; virtual containers never inherit a fabricated timestamp.
  if(n.colorBranchKey!==undefined&&n.colorBranchKey!==null&&(typeof n.colorBranchKey!=='string'||!/^cb1-[a-f0-9]{64}$/.test(n.colorBranchKey)))bad();
  if(n.modifiedAt!==undefined&&n.modifiedAt!==null&&(typeof n.modifiedAt!=='string'||!Number.isFinite(Date.parse(n.modifiedAt))))bad();
  const summary=parseSummary(n.summary),original=parseSummary(n.originalSummary!),match=parseSummary(n.matchSummary!);
  if(!equalSummary(summary,mode==='highlight'?original:match)||counters.some(k=>match[k]>original[k]))bad();
  let continuation:MapNode['continuation']=null;
  if(n.kind==='group'){
   if(n.parentId===null||n.listRootId!==n.parentId||typeof n.groupedChildren!=='string'||!/^[1-9][0-9]{0,127}$/.test(n.groupedChildren)||!['element-budget','depth-limit','byte-budget','time-budget'].includes(n.groupReason??'')||typeof(n.afterCursor??'')!=='string'||(n.afterCursor??'').length>160||!n.hasMore)bad();
   continuation=Object.freeze({rootId:n.listRootId!,cursor:n.afterCursor??'',reason:n.groupReason!,directChildren:BigInt(n.groupedChildren!)});
  }else if(n.groupReason!==undefined||n.groupedChildren!==undefined||n.listRootId!==undefined||n.afterCursor!==undefined)bad();
  return Object.freeze({colorBranchKey:n.colorBranchKey??null,id:n.id,parentId:n.parentId,realNodeId:n.realNodeId,name:n.name,kind:n.kind as NodeKind,virtual:n.virtual,children:Object.freeze([]),summary,original,match,matchKind:n.matchKind!,weight:summary.knownBytes,unknown:summary.unknownFileCount>0n,hasMore:n.hasMore,opaque:n.hasMore,classification:immutableJSON(n.classification??null),modifiedAt:n.modifiedAt??null,...(n.provenance?{provenance:Object.freeze({...n.provenance})}:{}),...(n.volumeId!==undefined?{volumeId:n.volumeId}:{}),...(n.shareId!==undefined?{shareId:n.shareId}:{}),continuation,canReveal:!n.virtual&&['file','directory'].includes(n.kind)});
}
// The adapter consumes one qualified response and constructs only its bounded display topology.
export function adaptHierarchy(input:HierarchyInput,expected:HierarchyIdentity):MapModel {
 if(!Number.isInteger(expected.maxNodes)||expected.maxNodes<1||expected.maxNodes>10000||!input||input.snapshotId!==expected.snapshotId||input.rootId!==expected.rootId||input.metric!==expected.metric||input.mode!==expected.mode||!['logical','allocated'].includes(input.metric)||!['highlight','hits-only'].includes(input.mode)||!['complete','partial'].includes(input.state)||!Array.isArray(input.nodes)||input.nodes.length<1||input.nodes.length>expected.maxNodes||typeof input.truncated!=='boolean'||input.selectionOrder!=='recorded-rowid'||!['empty','no-match','matches'].includes(input.matchState??''))bad();
 if(new TextEncoder().encode(JSON.stringify(input)).length>4*1024*1024)bad();
 if(!Array.isArray(input.coverage)||!validSourceAvailability(input.sourceAvailability))bad();
 const nodes:Record<string,MapNode>=Object.create(null);const order:string[]=[];const children:Record<string,string[]>=Object.create(null);
 for(const n of input.nodes){
  if(nodes[n.id])bad();
  const node=adaptMapNode(n,expected.mode);children[n.id]=[];order.push(n.id);
  nodes[n.id]={...node,children:children[n.id]};
 }
 const root=nodes[input.rootId];if(!root||(root.parentId!==null&&nodes[root.parentId]))bad();
 for(const node of Object.values(nodes)){
  if(node.id===root.id)continue;
  if(node.parentId===null||!nodes[node.parentId]||nodes[node.parentId].kind==='group')bad();
  children[node.parentId!].push(node.id);
 }
 // Iterative reachability rejects cycles/disconnected components without deep recursion.
 const queue=[root.id];for(let i=0;i<queue.length;i++){queue.push(...children[queue[i]]);if(queue.length>order.length)bad();}
 if(queue.length!==order.length)bad();
 for(const node of Object.values(nodes)){
  const childNodes=children[node.id].map(key=>nodes[key]);
  // hasMore describes omitted children, not the presence of already emitted children.
  if(childNodes.length){
   for(const field of ['summary','original','match'] as const){
    const sum=addSummaries(childNodes.map(n=>n[field]));const total=childTotal(node[field],node);
    // Hit-only pruning can hide unmatched original branches; never manufacture their geometry.
    if(input.mode==='hits-only'&&field==='original'){if(counters.some(k=>sum[k]>total[k]))bad();}
    else if(!equalSummary(sum,total))bad();
   }
  }
  nodes[node.id]=Object.freeze({...node,children:Object.freeze(children[node.id]),opaque:node.hasMore&&childNodes.length===0});
 }
 const original=parseSummary(input.originalSummary),match=parseSummary(input.matchSummary),denominator=parseSummary(input.denominator!);
 if(!equalSummary(original,root.original)||!equalSummary(match,root.match)||!equalSummary(denominator,root.summary))bad();
 return Object.freeze({version:1,snapshotId:input.snapshotId,rootId:root.id,metric:expected.metric,mode:expected.mode,state:input.state as 'complete'|'partial',truncated:input.truncated,selectionOrder:'recorded-rowid',nodes:Object.freeze(nodes),order:Object.freeze(order),original,match,denominator,matchState:input.matchState as MapModel['matchState'],coverage:immutableJSON(input.coverage,4*1024*1024) as readonly unknown[],sourceAvailability:immutableJSON(input.sourceAvailability,16384) as QuerySourceAvailability,filter:immutableJSON(input.filter??null)});
}
