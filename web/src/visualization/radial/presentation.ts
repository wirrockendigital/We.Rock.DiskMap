import type {MapModel} from '../shared/model';
import {groupDirectChildren,type DisplayGroup} from '../shared/groups';
import {qualifyGeometry,type Geometry} from '../shared/interaction';
import type {RadialLayout} from './layout';

export interface RadialPresentation {readonly geometry:readonly Geometry[];readonly groups:readonly DisplayGroup[];readonly centerRadius:number}
// Small contiguous siblings share one exact display group; descendants are never reparented.
export function radialPresentation(model:MapModel,layout:RadialLayout):RadialPresentation {
 const radius=Math.min(layout.width,layout.height)/2;
 const centerRadius=Math.min(radius*.45,Math.max(layout.centerRadius,80));
 const ringWidth=layout.ringCount?(radius-centerRadius)/layout.ringCount:0;
 const arcs=layout.geometry.map(g=>{if(g.shape!=='arc')throw Error('INVALID_RADIAL_GEOMETRY');return {...g,innerRadius:g.depth?centerRadius+(g.depth-1)*ringWidth:0,outerRadius:g.depth?centerRadius+g.depth*ringWidth:radius};});
 // The center is separate navigation context; reserve the remaining radii for actual rings.
 if(layout.ringCount)arcs[0]={...arcs[0],outerRadius:centerRadius};
 const byParent=new Map<string,typeof arcs>();
 for(const g of arcs){const parent=model.nodes[(g.target as {nodeId:string}).nodeId].parentId;if(parent!==null){const list=byParent.get(parent)??[];list.push(g);byParent.set(parent,list);}}
 const groups:DisplayGroup[]=[],replaced=new Map<string,Geometry>(),hidden=new Set<string>();
 for(const parentId of model.order){
  const parent=model.nodes[parentId];if(parent.children.length<2)continue;
  const candidates=(byParent.get(parentId)??[]).sort((a,b)=>a.startAngle-b.startAngle);
  let run:typeof candidates=[],best:typeof candidates=[];
  const consider=()=>{if(run.length>best.length)best=run;run=[];};
  for(const g of candidates){const id=(g.target as {nodeId:string}).nodeId;if(!hidden.has(parentId)&&model.nodes[id].kind!=='group'&&(g.endAngle-g.startAngle)*(g.innerRadius+g.outerRadius)/2<6)run.push(g);else consider();}consider();run=best;
  const finish=()=>{
   if(run.length>1){const ids=run.map(g=>(g.target as {nodeId:string}).nodeId),group=groupDirectChildren(model,parentId,ids),first=run[0],last=run[run.length-1];groups.push(group);
    replaced.set(ids[0],{...first,endAngle:last.endAngle,target:{kind:'display-group',groupId:group.id,parentId,memberIds:group.memberIds}});
    const pending=[...ids];while(pending.length){const id=pending.pop()!;hidden.add(id);pending.push(...model.nodes[id].children);}
   }run=[];
  };
  finish();
 }
 const geometry:Geometry[]=[];
 for(const g of arcs){const id=(g.target as {nodeId:string}).nodeId;const replacement=replaced.get(id);if(replacement&&!hidden.has(model.nodes[id].parentId??''))geometry.push(replacement);else if(!hidden.has(id))geometry.push(g);}
 return Object.freeze({geometry:qualifyGeometry(model,geometry),groups:Object.freeze(groups.filter(group=>geometry.some(g=>g.target.kind==='display-group'&&g.target.groupId===group.id))),centerRadius:layout.ringCount?centerRadius:radius});
}
