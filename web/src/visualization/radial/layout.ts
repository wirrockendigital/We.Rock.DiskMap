import type {MapModel} from '../shared/model';
import {qualifyGeometry, type Geometry} from '../shared/interaction';

export interface RadialOptions {width:number;height:number;maxDepth?:number;maxSegments?:number}
export type ListOnlyReason='zero'|'unknown'|'precision'|'depth'|'budget';
export interface RadialLayout {
 readonly version:1;readonly rootId:string;readonly width:number;readonly height:number;
 readonly geometry:readonly Geometry[];
 readonly listOnly:readonly Readonly<{nodeId:string;reason:ListOnlyReason}>[];
 readonly centerRadius:number;readonly ringWidth:number;readonly ringCount:number;
}
const TAU=2*Math.PI;
// Convert a bounded ratio, never an absolute byte count, into drawing coordinates.
function fraction(numerator:bigint,denominator:bigint):number {
 if(numerator===denominator)return 1;
 return Number(numerator)/Number(denominator);
}
// Layout is pure: selection, color, hover and the mutable canvas never enter its inputs.
export function layoutRadial(model:MapModel,options:RadialOptions):RadialLayout {
 const {width,height}=options,maxDepth=options.maxDepth??8,maxSegments=options.maxSegments??2000;
 if(![width,height].every(v=>Number.isFinite(v)&&v>0&&v<=65536)||!Number.isInteger(maxDepth)||maxDepth<1||maxDepth>8||!Number.isInteger(maxSegments)||maxSegments<1||maxSegments>2000||model.order.length>10000||!model.nodes[model.rootId])throw Error('INVALID_RADIAL_LAYOUT');
 // Walk the bounded model iteratively; uneven branches retain their actual depth.
 const depths=new Map<string,number>([[model.rootId,0]]),order=[model.rootId];let deepest=0;
 for(let i=0;i<order.length;i++){
  const parent=model.nodes[order[i]],depth=depths.get(parent.id)!;
  for(const id of parent.children){if(depths.has(id)||!model.nodes[id]||model.nodes[id].parentId!==parent.id)throw Error('INVALID_RADIAL_LAYOUT');depths.set(id,depth+1);order.push(id);deepest=Math.max(deepest,depth+1);}
 }
 if(order.length!==model.order.length)throw Error('INVALID_RADIAL_LAYOUT');
 const ringCount=Math.min(maxDepth,deepest),ringWidth=Math.min(width,height)/2/(ringCount+1),cx=width/2,cy=height/2;
 const geometry:Geometry[]=[{target:{kind:'model',nodeId:model.rootId},depth:0,shape:'arc',cx,cy,innerRadius:0,outerRadius:ringWidth,startAngle:0,endAngle:TAU}];
 const listOnly:{nodeId:string;reason:ListOnlyReason}[]=[],hidden=new Map<string,ListOnlyReason>();
 const angles=new Map<string,{start:number;end:number}>([[model.rootId,{start:0,end:TAU}]]);
 // Breadth-first traversal spends the segment budget on nearer hierarchy levels first.
 for(const parentId of order){
  const parent=model.nodes[parentId],span=angles.get(parentId),depth=depths.get(parentId)!+1;
  const children=[...parent.children].sort((a,b)=>{const x=model.nodes[a].weight,y=model.nodes[b].weight;return x===y?(a<b?-1:a>b?1:0):x>y?-1:1;});
  let cumulative=0n;
  for(const id of children){
   const node=model.nodes[id];cumulative+=node.weight;
   if(node.weight<0n||cumulative>parent.weight)throw Error('INVALID_RADIAL_LAYOUT');
   let reason=hidden.get(parentId);
   if(!reason&&depth>maxDepth)reason='depth';
   if(!reason&&node.weight===0n)reason=node.unknown?'unknown':'zero';
   let start=0,end=0;
   if(!reason&&span&&parent.weight>0n){
    // Clamp floating-point endpoints to their exact parent interval, including its final boundary.
    start=Math.min(span.end,span.start+(span.end-span.start)*fraction(cumulative-node.weight,parent.weight));
    end=cumulative===parent.weight?span.end:Math.min(span.end,span.start+(span.end-span.start)*fraction(cumulative,parent.weight));
    if(!(end>start))reason='precision';
   }
   else if(!reason)reason='precision';
   if(!reason&&geometry.length>=maxSegments)reason='budget';
   if(reason){hidden.set(id,reason);listOnly.push({nodeId:id,reason});continue;}
   angles.set(id,{start,end});geometry.push({target:{kind:'model',nodeId:id},depth,shape:'arc',cx,cy,innerRadius:depth*ringWidth,outerRadius:(depth+1)*ringWidth,startAngle:start,endAngle:end});
  }
 }
 // The root disk is structural context even when no measurable byte weight exists.
 return Object.freeze({version:1,rootId:model.rootId,width,height,geometry:qualifyGeometry(model,geometry),listOnly:Object.freeze(listOnly.map(x=>Object.freeze(x))),centerRadius:ringWidth,ringWidth,ringCount});
}
