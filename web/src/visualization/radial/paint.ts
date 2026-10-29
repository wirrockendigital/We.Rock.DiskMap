import type {PreferenceDisplay} from '../../contracts';
import type {MapModel} from '../shared/model';
import {colorForNode,type ColorMode} from '../shared/colors';
import type {Geometry} from '../shared/interaction';
import type {RadialPresentation} from './presentation';
export interface RadialPaintOptions {display?:Readonly<PreferenceDisplay>;colorMode:ColorMode;referenceTime:string|null;selectedNodeId:string|null;focusedNodeId:string|null;theme:'light'|'dark';language:'de'|'en'}
// Both fill and outline reuse the same qualified CSS arc; state never changes geometry.
function path(ctx:CanvasRenderingContext2D,g:Extract<Geometry,{shape:'arc'}>):void {
 ctx.beginPath();ctx.arc(g.cx,g.cy,g.outerRadius,g.startAngle,g.endAngle);
 if(g.innerRadius)ctx.arc(g.cx,g.cy,g.innerRadius,g.endAngle,g.startAngle,true);else ctx.lineTo(g.cx,g.cy);ctx.closePath();
}
export function paintRadial(ctx:CanvasRenderingContext2D,model:MapModel,p:RadialPresentation,o:RadialPaintOptions):void {
 const validTime=o.referenceTime!==null&&Number.isFinite(Date.parse(o.referenceTime));
 for(const g of p.geometry){
  if(g.shape!=='arc')continue;
  const node=g.target.kind==='model'?model.nodes[g.target.nodeId]:null;
  const color=node&&!(o.colorMode==='mtime'&&!validTime)?colorForNode(model,node.id,o.colorMode,o.referenceTime??''):null;
  ctx.save();path(ctx,g);ctx.clip();ctx.fillStyle=g.depth===0?(o.theme==='dark'?'#111827':'#f8fafc'):color?.fill??'#4b5563';ctx.fill();
  // The palette stays dark in both themes. Keep a contrasting boundary and account
  // for the sector clip removing half of the centered stroke (1/1.5 CSS px remain).
  ctx.strokeStyle='#ffffff';ctx.lineWidth=o.display?.hierarchyBorders==='subtle'?2:3;ctx.setLineDash([]);ctx.stroke();
  const members=g.target.kind==='display-group'?g.target.memberIds:[];
  const selected=!!node&&(node.id===o.selectedNodeId||node.realNodeId===o.selectedNodeId)||members.some(id=>id===o.selectedNodeId||model.nodes[id].realNodeId===o.selectedNodeId),focused=!!node&&(node.id===o.focusedNodeId||node.realNodeId===o.focusedNodeId)||members.some(id=>id===o.focusedNodeId||model.nodes[id].realNodeId===o.focusedNodeId);
  if(selected||focused){ctx.strokeStyle='#ffffff';ctx.lineWidth=focused?4:3;ctx.setLineDash(focused?[3,2]:[]);ctx.stroke();ctx.strokeStyle='#111827';ctx.lineWidth=1;ctx.stroke();}
  // Pattern outlines preserve mixed/unknown information without relying on hue alone.
  if(color?.pattern!=='none'&&color?.pattern){ctx.strokeStyle='#ffffff';ctx.lineWidth=1;ctx.setLineDash([2,3]);ctx.stroke();}
  if(o.display?.labels===false||!g.depth){ctx.restore();continue;}
  const groupId=g.target.kind==='display-group'?g.target.groupId:null;
  const group=groupId?p.groups.find(x=>x.id===groupId):null;
  const label=node?.name??`${group?.directChildren??0n} ${o.language==='de'?'Objekte':'objects'}`;
  const r=(g.innerRadius+g.outerRadius)/2,angle=(g.startAngle+g.endAngle)/2;
  ctx.font='12px sans-serif';
  // Labels never enlarge sectors. Full names remain in the synchronized text/details view.
  const halfWidth=(ctx.measureText(label).width+10)/2,halfHeight=8;
  const fits=r-halfHeight>=g.innerRadius&&Math.hypot(r+halfHeight,halfWidth)<=g.outerRadius&&Math.atan2(halfWidth,r-halfHeight)<=(g.endAngle-g.startAngle)/2;
  if(!fits){ctx.restore();continue;}
  ctx.save();ctx.translate(g.cx+Math.cos(angle)*r,g.cy+Math.sin(angle)*r);ctx.rotate(angle+Math.PI/2+(angle>0&&angle<Math.PI?Math.PI:0));ctx.textAlign='center';ctx.textBaseline='middle';ctx.fillStyle='#ffffff';ctx.fillText(label,0,0);ctx.restore();ctx.restore();
 }
 ctx.setLineDash([]);
}
