import type {MapModel} from './model';

export type HitTarget=Readonly<{kind:'model';nodeId:string}|{kind:'display-group';groupId:string;parentId:string;memberIds:readonly string[]}>;
export type Geometry=Readonly<{target:HitTarget;depth:number}&({shape:'rect';x:number;y:number;width:number;height:number}|{shape:'arc';cx:number;cy:number;innerRadius:number;outerRadius:number;startAngle:number;endAngle:number})>;
export type Point=Readonly<{x:number;y:number}>;
// Geometry is in logical CSS pixels; pointer coordinates never multiply by devicePixelRatio.
export function pointerPoint(clientX:number,clientY:number,rect:{left:number;top:number;width:number;height:number},cssWidth:number,cssHeight:number):Point|null {
 if(![clientX,clientY,rect.left,rect.top,rect.width,rect.height,cssWidth,cssHeight].every(Number.isFinite)||rect.width<=0||rect.height<=0||cssWidth<=0||cssHeight<=0)return null;
 return Object.freeze({x:(clientX-rect.left)*cssWidth/rect.width,y:(clientY-rect.top)*cssHeight/rect.height});
}
// Validate the renderer boundary once; local groups can never masquerade as query node IDs.
export function qualifyGeometry(model:MapModel,geometry:readonly Geometry[]):readonly Geometry[] {
 if(geometry.length>10000)throw Error('INVALID_MAP_GEOMETRY');
 let groupedMembers=0;
 return Object.freeze(geometry.map(g=>{
  if(!Number.isInteger(g.depth)||g.depth<0||g.depth>256)throw Error('INVALID_MAP_GEOMETRY');
  const target=g.target;
  if(target.kind==='model'){if(!model.nodes[target.nodeId])throw Error('INVALID_MAP_TARGET');}
  else if(target.kind==='display-group'){
   groupedMembers+=target.memberIds.length;
   const parent=model.nodes[target.parentId];if(typeof target.groupId!=='string'||!target.groupId||target.groupId.length>320||model.nodes[target.groupId]||groupedMembers>10000||!parent||target.memberIds.length<2||target.memberIds.length>10000||new Set(target.memberIds).size!==target.memberIds.length||target.memberIds.some(id=>!parent.children.includes(id)||model.nodes[id].kind==='group'))throw Error('INVALID_MAP_TARGET');
  }else throw Error('INVALID_MAP_TARGET');
  if(g.shape==='rect'){if(![g.x,g.y,g.width,g.height].every(Number.isFinite)||g.width<0||g.height<0)throw Error('INVALID_MAP_GEOMETRY');}
  else if(g.shape==='arc'){if(![g.cx,g.cy,g.innerRadius,g.outerRadius,g.startAngle,g.endAngle].every(Number.isFinite)||g.innerRadius<0||g.outerRadius<g.innerRadius||g.startAngle<0||g.endAngle>Math.PI*2||g.endAngle<g.startAngle)throw Error('INVALID_MAP_GEOMETRY');}
  else throw Error('INVALID_MAP_GEOMETRY');
  return Object.freeze({...g,target:Object.freeze(target.kind==='model'?{...target}:{...target,memberIds:Object.freeze([...target.memberIds])})});
 }));
}
// Half-open edges eliminate adjacent-boundary ambiguity; deepest hit wins, then recorded geometry order.
export function hitTest(geometry:readonly Geometry[],point:Point):HitTarget|null {
 if(!Number.isFinite(point.x)||!Number.isFinite(point.y))return null;
 let hit:Geometry|undefined;
 for(const g of geometry){
  let inside=false;
  if(g.shape==='rect')inside=point.x>=g.x&&point.x<g.x+g.width&&point.y>=g.y&&point.y<g.y+g.height;
  else {const x=point.x-g.cx,y=point.y-g.cy,r=Math.hypot(x,y),angle=(Math.atan2(y,x)+2*Math.PI)%(2*Math.PI);inside=r>=g.innerRadius&&r<g.outerRadius&&angle>=g.startAngle&&angle<g.endAngle;}
  if(inside&&(!hit||g.depth>hit.depth))hit=g;
 }
 return hit?.target??null;
}
// Focus traversal never changes selection or invokes navigation; the controlled host decides activation.
export function keyboardTarget(model:MapModel,current:string|null,key:string):string|null {
 const order=model.order;if(!order.length)return null;const index=current===null?-1:order.indexOf(current);
 if(key==='Home')return order[0];if(key==='End')return order[order.length-1];
 if(key==='ArrowDown')return order[Math.min(order.length-1,index+1)];
 if(key==='ArrowUp')return order[Math.max(0,index-1)];
 const node=current?model.nodes[current]:undefined;
 if(key==='ArrowRight')return node?.children[0]??current;
 if(key==='ArrowLeft')return node?.parentId&&model.nodes[node.parentId]?node.parentId:current;
 return current;
}
// A single explicit dispatcher keeps local display groups out of every backend-facing callback.
export function dispatchTarget(target:HitTarget,onNode:(nodeId:string)=>void,onGroup:(group:Extract<HitTarget,{kind:'display-group'}>)=>void):void {
 if(target.kind==='model')onNode(target.nodeId);else onGroup(target);
}
