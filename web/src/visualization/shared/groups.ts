import {addSummaries,type MapModel,type Summary,type Continuation} from './model';

export interface DisplayGroup {
 readonly id:string;readonly parentId:string;readonly virtual:true;readonly realNodeId:null;readonly canReveal:false;
 readonly memberIds:readonly string[];readonly directChildren:bigint;
 readonly summary:Summary;readonly original:Summary;readonly match:Summary;readonly weight:bigint;
}
// Local grouping selects only direct siblings. It replaces those display entries;
// it never changes the immutable physical parent relation or adds their totals twice.
export function groupDirectChildren(model:MapModel,parentId:string,memberIds:readonly string[]):DisplayGroup {
 const parent=model.nodes[parentId];if(!parent||!Array.isArray(memberIds)||memberIds.length<2||memberIds.length>10000)throw Error('INVALID_MAP_GROUP');
 const selected=new Set(memberIds);if(selected.size!==memberIds.length)throw Error('INVALID_MAP_GROUP');
 const members=parent.children.filter(id=>selected.has(id));
 if(members.length!==memberIds.length||members.some(id=>model.nodes[id].parentId!==parentId||model.nodes[id].kind==='group'))throw Error('INVALID_MAP_GROUP');
 const summary=addSummaries(members.map(id=>model.nodes[id].summary));
 return Object.freeze({id:`display-group:${parentId}`,parentId,virtual:true,realNodeId:null,canReveal:false,memberIds:Object.freeze(members),directChildren:BigInt(members.length),summary,original:addSummaries(members.map(id=>model.nodes[id].original)),match:addSummaries(members.map(id=>model.nodes[id].match)),weight:summary.knownBytes});
}
// The original snapshot/filter/metric identity remains owned by the surrounding model.
export function groupContinuation(model:MapModel,id:string):Continuation {
 const node=model.nodes[id];if(!node||node.kind!=='group'||!node.continuation)throw Error('INVALID_MAP_GROUP');return node.continuation;
}
