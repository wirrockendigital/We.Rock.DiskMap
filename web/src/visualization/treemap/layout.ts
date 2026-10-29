import {hierarchy,treemap,treemapSquarify} from 'd3-hierarchy';
import type {MapModel} from '../shared/model';
import type {Geometry} from '../shared/interaction';

export interface TreemapOptions {width:number;height:number;maxDepth?:number;maxElements?:number;decoration?:boolean}
export interface RectBounds {readonly x:number;readonly y:number;readonly width:number;readonly height:number}
export type TreemapRectangle=Extract<Geometry,{shape:'rect'}>&Readonly<{nodeId:string;weight:bigint;opaque:boolean;content:RectBounds;headerHeight:number}>;
export type OmissionReason='depth-limit'|'element-budget'|'precision'|'viewport';
export interface TreemapLayout {
 readonly snapshotId:string;readonly rootId:string;readonly metric:MapModel['metric'];
 readonly width:number;readonly height:number;readonly rectangles:readonly TreemapRectangle[];
 readonly zeroIds:readonly string[];readonly omitted:readonly Readonly<{nodeId:string;reason:OmissionReason}>[];
}
interface Child {id:string;weight:bigint;children?:Child[]}
// Exact integer ordering is independent of locale, input order, and floating-point rounding.
function compare(a:Child,b:Child):number{return a.weight>b.weight?-1:a.weight<b.weight?1:a.id<b.id?-1:a.id>b.id?1:0;}
// Only pixel ratios enter D3. BigInt counters and comparisons remain authoritative.
function ratio(value:bigint,maximum:bigint):number {
 const a=value.toString(),b=maximum.toString();
 return Number(a.slice(0,16))/Number(b.slice(0,16))*10**(a.length-Math.min(16,a.length)-b.length+Math.min(16,b.length));
}
// Each parent is tiled once from its direct children; ancestor aggregates are never added as leaf weights.
export function layoutTreemap(model:MapModel,options:TreemapOptions):TreemapLayout {
 const {width,height}=options,maxDepth=options.maxDepth??256,maxElements=options.maxElements??10000;
 if(![width,height].every(v=>Number.isFinite(v)&&v>=0&&v<=16384)||!Number.isInteger(maxDepth)||maxDepth<0||maxDepth>256||!Number.isInteger(maxElements)||maxElements<1||maxElements>10000||model.order.length>10000)throw Error('INVALID_TREEMAP_OPTIONS');
 const root=model.nodes[model.rootId];if(!root)throw Error('INVALID_TREEMAP_MODEL');
 const rectangles:TreemapRectangle[]=[],reasons=new Map<string,OmissionReason>(),shown=new Set<string>();
 const zeroIds=model.order.filter(id=>model.nodes[id].weight===0n);
 // Iterative traversal keeps deep trees bounded without recursive hierarchy construction.
 const queue:{id:string;x:number;y:number;width:number;height:number;depth:number;reason?:OmissionReason}[]=[];
 if(root.weight>0n&&width>0&&height>0)queue.push({id:root.id,x:0,y:0,width,height,depth:0});
 else if(root.weight>0n)reasons.set(root.id,'viewport');
 let reserved=queue.length;
 for(let offset=0;offset<queue.length;offset++){
  const box=queue[offset],node=model.nodes[box.id],children=node.children.map(id=>({id,weight:model.nodes[id].weight})).filter(child=>child.weight>0n).sort(compare);
  shown.add(node.id);let opaque=node.opaque;
  // Decoration is outside the parent's data region; tiny regions receive neither a header nor a large inset.
  const inset=options.decoration&&children.length?Math.min(box.depth===0?5:box.depth===1?4:box.depth===2?3:1,Math.min(box.width,box.height)*0.02):0;
  const headerHeight=options.decoration&&children.length&&box.width>=140&&box.height>=180?20:0;
  const content=Object.freeze({x:box.x+inset,y:box.y+inset+headerHeight,width:Math.max(0,box.width-2*inset),height:Math.max(0,box.height-2*inset-headerHeight)});
  if(children.length){
   if(children.reduce((sum,child)=>sum+child.weight,0n)!==node.weight)throw Error('INVALID_TREEMAP_MODEL');
   let blocked:OmissionReason|undefined;
   if(box.depth>=maxDepth)blocked='depth-limit';
   else if(reserved+children.length>maxElements)blocked='element-budget';
   if(blocked){opaque=true;for(const child of children)reasons.set(child.id,blocked);}
   else {
    // Shallow D3 trees avoid repeated deep traversal and preserve the shared parent topology.
    const maximum=children[0].weight,data:Child={id:node.id,weight:0n,children};
    const tree=hierarchy(data).sum(child=>child.children?0:ratio(child.weight,maximum)).sort((a,b)=>compare(a.data,b.data));
    const laid=treemap<Child>().tile(treemapSquarify).size([content.width,content.height]).padding(0).round(false)(tree);
    for(const child of laid.children??[]){
     const w=child.x1-child.x0,h=child.y1-child.y0;
     if(w<=0||h<=0){reasons.set(child.data.id,'precision');opaque=true;continue;}
     queue.push({id:child.data.id,x:content.x+child.x0,y:content.y+child.y0,width:w,height:h,depth:box.depth+1});reserved++;
    }
   }
  }
  rectangles.push(Object.freeze({shape:'rect',target:Object.freeze({kind:'model',nodeId:node.id}),nodeId:node.id,weight:node.weight,depth:box.depth,x:box.x,y:box.y,width:box.width,height:box.height,opaque,content,headerHeight}));
 }
 // Every undisplayed positive node remains explicit, including descendants of a bounded opaque parent.
 const omitted:Readonly<{nodeId:string;reason:OmissionReason}>[]=[];
 const pending=[{id:root.id,inherited:reasons.get(root.id)??'viewport' as OmissionReason}];
 while(pending.length){const current=pending.pop()!,node=model.nodes[current.id],reason=reasons.get(node.id)??current.inherited;
  if(node.weight>0n&&!shown.has(node.id))omitted.push(Object.freeze({nodeId:node.id,reason}));
  for(let i=node.children.length-1;i>=0;i--)pending.push({id:node.children[i],inherited:reason});
 }
 return Object.freeze({snapshotId:model.snapshotId,rootId:model.rootId,metric:model.metric,width,height,rectangles:Object.freeze(rectangles),zeroIds:Object.freeze(zeroIds),omitted:Object.freeze(omitted)});
}
