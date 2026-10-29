import {formatByteSize} from '../../size-metrics';
import type {PreferenceDisplay} from '../../contracts';
import type {MapModel} from '../shared/model';
import {colorForNode,type ColorMode} from '../shared/colors';
import type {TreemapLayout,RectBounds} from './layout';

export interface PaintOptions {display?:Readonly<PreferenceDisplay>;language:'de'|'en';locale?:string;theme:'light'|'dark';colorMode:ColorMode;referenceTime:string|null;selectedNodeId:string|null;focusedNodeId:string|null;disabled?:boolean}
export interface DrawnLabel {readonly nodeId:string;readonly text:string;readonly bounds:RectBounds}
// Percentages retain an explicit view denominator and use integer rounding, including huge totals.
export function viewPercentage(weight:bigint,total:bigint,language:'de'|'en',locale:string=language):string|null {
 if(total<=0n||weight<0n||weight>total)return null;
 const tenths=(weight*1000n+total/2n)/total;
 return `${tenths/10n}${new Intl.NumberFormat(locale).formatToParts(1.1).find(x=>x.type==='decimal')?.value??'.'}${tenths%10n}% ${language==='de'?'des Ausschnitts':'of view'}`;
}
// Bound measurement work independently of raw name length; the full name remains in the text/details view.
function fitText(context:CanvasRenderingContext2D,text:string,width:number):string {
 const chars=Array.from(text.slice(0,512)).slice(0,128),shortened=chars.join('')!==text;
 if(!shortened&&context.measureText(text).width<=width)return text;
 if(context.measureText('…').width>width)return '';
 let lo=0,hi=chars.length;
 while(lo<hi){const mid=Math.ceil((lo+hi)/2);if(context.measureText(chars.slice(0,mid).join('')+'…').width<=width)lo=mid;else hi=mid-1;}
 return chars.slice(0,lo).join('')+'…';
}
const kindLabels={de:{nas:'NAS',volume:'Volume',share:'Freigabe',other:'Weiterer Bereich',directory:'Ordner',group:'Darstellungsgruppe'},en:{nas:'NAS',volume:'Volume',share:'Share',other:'Other area',directory:'Folder',group:'Display group'}};
// Painting changes no layout, counters or selection. Headers occupy reserved bands outside child data rectangles.
export function paintTreemap(context:CanvasRenderingContext2D,model:MapModel,layout:TreemapLayout,options:PaintOptions):readonly DrawnLabel[] {
 if(layout.snapshotId!==model.snapshotId||layout.rootId!==model.rootId||layout.metric!==model.metric)throw Error('INVALID_TREEMAP_IDENTITY');
 const expandedParents=new Set(layout.rectangles.map(r=>model.nodes[r.nodeId].parentId).filter((id):id is string=>id!==null));
 const labels:DrawnLabel[]=[],validReference=options.referenceTime!==null&&Number.isFinite(Date.parse(options.referenceTime));
 context.clearRect(0,0,layout.width,layout.height);context.fillStyle=options.theme==='dark'?'#111827':'#f3f4f6';context.fillRect(0,0,layout.width,layout.height);
 context.font='12px sans-serif';context.textBaseline='top';
 for(const r of layout.rectangles){
  const node=model.nodes[r.nodeId],color=options.colorMode==='mtime'&&!validReference?{fill:'#4b5563',text:'#ffffff',pattern:'unknown'}:colorForNode(model,node.id,options.colorMode,options.referenceTime??'');
  context.fillStyle=color.fill;context.fillRect(r.x,r.y,r.width,r.height);
  // A bounded hatch distinguishes unknown/mixed data without relying on hue alone.
  if(color.pattern!=='none'||node.unknown){context.save();context.beginPath();context.rect(r.x,r.y,r.width,r.height);context.clip();context.strokeStyle='#ffffff44';context.lineWidth=1;for(let i=0;i<8;i++){const x=r.x+r.width*i/7;context.beginPath();context.moveTo(x,r.y);context.lineTo(x+r.height,r.y+r.height);context.stroke();}context.restore();}
 }
 for(const r of layout.rectangles){
  const node=model.nodes[r.nodeId],selected=options.selectedNodeId===node.id,focused=options.focusedNodeId===node.id;
  // Bound strokes to the rectangle so tiny tiles cannot gain a misleading minimum painted size.
  const stroke=Math.min(selected||focused?2:options.display?.hierarchyBorders==='subtle'?0.5:r.depth<3?1.5:0.5,r.width/3,r.height/3);
  context.strokeStyle=focused?'#ffffff':selected?'#ffffff':options.theme==='dark'?'#d1d5db':'#e5e7eb';context.lineWidth=stroke;context.setLineDash(focused?[3,2]:[]);if(stroke>0)context.strokeRect(r.x+stroke/2,r.y+stroke/2,Math.max(0,r.width-stroke),Math.max(0,r.height-stroke));context.setLineDash([]);
  const expanded=expandedParents.has(node.id),labelHeight=expanded?r.headerHeight:r.height;
  if(options.display?.labels===false||labelHeight<18||r.width<48)continue;
  const kind=kindLabels[options.language][node.kind as keyof typeof kindLabels.en],name=`${focused?'› ':selected?'✓ ':''}${kind?kind+' · ':''}${node.name}`;
  const bytes=`${options.display&&node.weight>=(options.display.units==='binary'?1073741824n:1000000000n)?formatByteSize(String(node.weight),options.display.units==='binary'?'GiB':'GB').label:node.weight.toLocaleString(options.locale??options.language)+' B'}${node.unknown?' + ?':''}`;
  const suffix=` · ${bytes}`;
  // Wide parent headers retain the exact size; only the name is shortened to make room.
  const title=expanded&&r.width>=context.measureText(suffix).width+90?fitText(context,name,r.width-10-context.measureText(suffix).width)+suffix:name;
  const lines=[title];
  if(!expanded&&labelHeight>=40)lines.push(bytes);
  const percent=viewPercentage(node.weight,model.nodes[model.rootId].weight,options.language,options.locale);
  if(options.display?.percentages!==false&&!expanded&&labelHeight>=58&&r.width>=100&&percent)lines.push(percent);
  context.save();context.beginPath();context.rect(r.x,r.y,r.width,labelHeight);context.clip();context.fillStyle='#ffffff';
  for(let i=0;i<lines.length;i++){const text=fitText(context,lines[i],r.width-10);if(!text)continue;const bounds=Object.freeze({x:r.x+5,y:r.y+3+i*18,width:context.measureText(text).width,height:14});if(bounds.y+bounds.height>r.y+labelHeight)break;context.fillText(text,bounds.x,bounds.y);labels.push(Object.freeze({nodeId:node.id,text,bounds}));}
  context.restore();
 }
 return Object.freeze(labels);
}
