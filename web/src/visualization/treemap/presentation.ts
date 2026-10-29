import type {MapModel,MapNode} from '../shared/model';
import {groupDirectChildren,type DisplayGroup} from '../shared/groups';
import {qualifyGeometry} from '../shared/interaction';
import {layoutTreemap,type TreemapLayout,type TreemapOptions,type TreemapRectangle} from './layout';

export interface TreemapPresentation {readonly model:MapModel;readonly layout:TreemapLayout;readonly groups:readonly DisplayGroup[]}
// A private display projection replaces only direct siblings; it never becomes a backend query model.
export function treemapPresentation(source:MapModel,options:TreemapOptions):TreemapPresentation {
 const initial=layoutTreemap(source,options),byId=new Map(initial.rectangles.map(r=>[r.nodeId,r])),precision=new Set(initial.omitted.filter(x=>x.reason==='precision').map(x=>x.nodeId));
 const hidden=new Set<string>(),groups:DisplayGroup[]=[],byParent=new Map<string,DisplayGroup>();
 for(const r of initial.rectangles){
  if(hidden.has(r.nodeId))continue;
  const parent=source.nodes[r.nodeId],members=parent.children.filter(id=>{
   const node=source.nodes[id],box=byId.get(id);
   return node.kind!=='group'&&node.weight>0n&&(precision.has(id)||!!box&&(box.width*box.height<64||Math.min(box.width,box.height)<3));
  });
  if(members.length<2)continue;
  const group=groupDirectChildren(source,parent.id,members);if(source.nodes[group.id])throw Error('INVALID_MAP_GROUP');
  groups.push(group);byParent.set(parent.id,group);
  // Collapsed descendants remain reachable through their actual member ancestor in the shared group panel.
  const pending=[...group.memberIds];while(pending.length){const id=pending.pop()!;hidden.add(id);pending.push(...source.nodes[id].children);}
 }
 if(!groups.length)return Object.freeze({model:source,layout:initial,groups:Object.freeze([])});
 const nodes:Record<string,MapNode>=Object.create(null);
 for(const id of source.order){
  if(hidden.has(id))continue;
  const node=source.nodes[id],group=byParent.get(id);
  nodes[id]=group?Object.freeze({...node,children:Object.freeze([...node.children.filter(child=>!hidden.has(child)),group.id])}):node;
  if(group)nodes[group.id]=Object.freeze({...node,id:group.id,parentId:id,realNodeId:null,name:group.directChildren.toString(),kind:'group',virtual:true,children:Object.freeze([]),summary:group.summary,original:group.original,match:group.match,matchKind:'group',weight:group.weight,unknown:group.summary.unknownFileCount>0n,hasMore:true,opaque:true,classification:null,modifiedAt:null,colorBranchKey:null,provenance:undefined,continuation:null,canReveal:false});
 }
 const order=[source.rootId];for(let i=0;i<order.length;i++)order.push(...nodes[order[i]].children);
 const model=Object.freeze({...source,nodes:Object.freeze(nodes),order:Object.freeze(order)}),layout=layoutTreemap(model,options),byGroup=new Map(groups.map(g=>[g.id,g]));
 const rectangles=layout.rectangles.map(r=>{const group=byGroup.get(r.nodeId);return group?Object.freeze({...r,target:Object.freeze({kind:'display-group' as const,groupId:group.id,parentId:group.parentId,memberIds:group.memberIds})}):r;});
 // This gate checks every local member against the immutable source, before hit targets leave the renderer.
 qualifyGeometry(source,rectangles);
 return Object.freeze({model,layout:Object.freeze({...layout,rectangles:Object.freeze(rectangles) as readonly TreemapRectangle[]}),groups:Object.freeze(groups)});
}
