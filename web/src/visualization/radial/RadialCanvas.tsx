import {useDisplay} from '../../features/settings/display';
import {useRegionalFormat} from '../../regional-format';
import {useEffect,useRef,useState} from 'react';
import type {MapModel} from '../shared/model';
import type {ColorMode} from '../shared/colors';
import {hitTest,pointerPoint,keyboardTarget,type Geometry,type HitTarget} from '../shared/interaction';
import {configureCanvas} from '../shared/canvas-surface';
import {LatestRenderCoordinator} from '../shared/render-coordinator';
import {layoutRadial} from './layout';
import {radialPresentation,type RadialPresentation} from './presentation';
import {paintRadial} from './paint';
export interface RadialCanvasProps {
 model:MapModel;width:number;height:number;devicePixelRatio:number;colorMode:ColorMode;referenceTime:string|null;
 selectedNodeId:string|null;focusedNodeId:string|null;disabled:boolean;language:'de'|'en';theme?:'light'|'dark';
 maxDepth?:number;maxSegments?:number;canGoUp:boolean;onUp:()=>void;onSelect?:(id:string)=>void;onFocus?:(id:string)=>void;onOpen?:(id:string)=>void;onGroup?:(target:HitTarget)=>void;onGeometry?:(geometry:readonly Geometry[])=>void;
}
type Input={model:MapModel;width:number;height:number;maxDepth?:number;maxSegments?:number};
type Result=Input&{presentation:RadialPresentation};
// One canvas owns backing memory; labels and the parent action remain native accessible HTML.
export function RadialCanvas(p:RadialCanvasProps) {
 const display=useDisplay();
 const regional=useRegionalFormat(p.language);
 const captionHeight=Math.min(104,p.height/3),chartHeight=Math.max(1,p.height-captionHeight);
 const canvas=useRef<HTMLCanvasElement>(null),latest=useRef(p);latest.current=p;
 const hits=useRef<readonly Geometry[]>([]);
 const [hover,setHover]=useState<HitTarget|null>(null);
 const publish=(geometry:readonly Geometry[])=>{hits.current=geometry;latest.current.onGeometry?.(geometry);};
 const worker=useRef<LatestRenderCoordinator<Input,Result>|null>(null);
 const [result,setResult]=useState<Result|null>(null),[failed,setFailed]=useState(false);
 useEffect(()=>{
  const coordinator=new LatestRenderCoordinator<Input,Result>(async(input,signal)=>{
   if(signal.aborted)throw Error('RENDER_CANCELLED');
   return {...input,presentation:radialPresentation(input.model,layoutRadial(input.model,input))};
  },value=>{const now=latest.current;if(value.model!==now.model||value.width!==now.width||value.height!==Math.max(1,now.height-Math.min(104,now.height/3))||value.maxDepth!==now.maxDepth||value.maxSegments!==now.maxSegments)return;setResult(value);setFailed(false);},()=>{setFailed(true);publish([]);});
  worker.current=coordinator;return()=>{coordinator.dispose();worker.current=null;publish([]);};
 },[]);
 useEffect(()=>{setHover(null);publish([]);worker.current?.request({model:p.model,width:p.width,height:chartHeight,maxDepth:p.maxDepth,maxSegments:p.maxSegments});},[p.model,p.width,chartHeight,p.maxDepth,p.maxSegments]);
 const current=result?.model===p.model&&result.width===p.width&&result.height===chartHeight&&result.maxDepth===p.maxDepth&&result.maxSegments===p.maxSegments?result:null;
 useEffect(()=>{
  if(!canvas.current)return;
  try{configureCanvas(canvas.current,p.width,chartHeight,p.devicePixelRatio);const ctx=canvas.current.getContext('2d');if(!ctx)throw Error('CANVAS_UNAVAILABLE');ctx.clearRect(0,0,p.width,chartHeight);if(current){publish([]);paintRadial(ctx,p.model,current.presentation,{...p,display,theme:p.theme??'light'});setFailed(false);publish(current.presentation.geometry);}}
  catch{setFailed(true);publish([]);}
 },[display,current,p.model,p.width,chartHeight,p.devicePixelRatio,p.colorMode,p.referenceTime,p.selectedNodeId,p.focusedNodeId,p.theme,p.language]);
 // Every activation uses current props and paint-qualified CSS geometry, never a cached node object.
 const activate=(target:HitTarget|null,open=false)=>{const now=latest.current;if(now.disabled||!target)return;if(target.kind==='display-group'){now.onGroup?.(target);return;}const node=now.model.nodes[target.nodeId];if(!node)return;if(node.kind==='group'){now.onGroup?.(target);return;}if(open){if(['nas','volume','share','other','directory'].includes(node.kind))now.onOpen?.(node.id);}else now.onSelect?.(node.id);};
 const at=(clientX:number,clientY:number)=>{if(!canvas.current||!current||failed)return null;const point=pointerPoint(clientX,clientY,canvas.current.getBoundingClientRect(),p.width,chartHeight);return point?hitTest(hits.current,point):null;};
 const root=p.model.nodes[p.model.rootId],de=p.language==='de',size=regional.size(String(root.weight),root.weight<(display.units==='binary'?1073741824n:1000000000n)?'B':'GB'),center=current?.presentation.centerRadius??0;
 const timeMissing=p.colorMode==='mtime'&&(p.referenceTime===null||!Number.isFinite(Date.parse(p.referenceTime)));
 // An unavailable canvas must not remain a dead stop in the keyboard sequence.
 // The supplied dimensions are drawable content bounds, so the global section border must not shrink them.
 return <section aria-label={de?'Radialkarte':'Radial map'} aria-busy={!current&&!failed} style={{width:p.width,height:p.height,margin:0,padding:0,border:0,boxSizing:'border-box',overflow:'hidden'}}>
  <div style={{position:'relative',width:p.width,height:chartHeight,maxWidth:'100%'}}>
   <canvas ref={canvas} role="img" aria-label={de?'Radialkarte; Pfeiltasten fokussieren, Eingabe wählt aus.':'Radial map; arrow keys focus, Enter selects.'} tabIndex={p.disabled||failed?-1:0} aria-disabled={p.disabled||failed}
    onPointerMove={event=>{if(!latest.current.disabled)setHover(at(event.clientX,event.clientY));}} onPointerLeave={()=>setHover(null)}
    onClick={event=>activate(at(event.clientX,event.clientY))} onDoubleClick={event=>activate(at(event.clientX,event.clientY),true)}
    onKeyDown={event=>{if(latest.current.disabled||!current||failed)return;const focus=latest.current.focusedNodeId??latest.current.model.rootId;if(event.key==='Enter'||event.key===' '){event.preventDefault();activate({kind:'model',nodeId:focus});}else if(['ArrowDown','ArrowUp','ArrowLeft','ArrowRight','Home','End'].includes(event.key)){event.preventDefault();const next=keyboardTarget(latest.current.model,focus,event.key);if(next)latest.current.onFocus?.(next);}}}/>
   {hover&&current&&!failed&&<div role="tooltip" style={{position:'absolute',top:0,left:0,maxWidth:'100%',pointerEvents:'none',background:p.theme==='dark'?'#111827':'#fff',color:p.theme==='dark'?'#fff':'#111827',overflowWrap:'anywhere'}}>{hover.kind==='model'?`${p.model.nodes[hover.nodeId]?.name??''} · ${p.model.nodes[hover.nodeId]?.kind??''} · ${(p.model.nodes[hover.nodeId]?regional.integer(p.model.nodes[hover.nodeId].weight):'')??''} B · ${p.model.nodes[p.model.nodes[hover.nodeId]?.parentId??'']?.name??''}`:`${de?'Gruppierte direkte Elemente':'Grouped direct entries'} · ${regional.integer(current.presentation.groups.find(g=>g.id===hover.groupId)?.weight??0n)??''} B · ${p.model.nodes[hover.parentId]?.name??''}`}</div>}
   {current&&<div style={{position:'absolute',left:p.width/2,top:chartHeight/2,transform:'translate(-50%,-50%)',width:Math.max(0,center*1.6),textAlign:'center',color:p.theme==='dark'?'#f8fafc':'#111827'}}>
    <strong title={root.name} style={{display:'block',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{root.name}</strong>
    <span title={size} style={{display:'block',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap',fontSize:12}}>{size}</span>
    {center>=90&&<button type="button" disabled={p.disabled||!p.canGoUp} onClick={()=>{if(!latest.current.disabled&&latest.current.canGoUp)latest.current.onUp();}}>{de?'Eine Ebene hoch':'Up one level'}</button>}
   </div>}
  </div>
  <div style={{height:captionHeight,overflow:'auto',fontSize:12,padding:'0 8px',boxSizing:'border-box'}} tabIndex={0} aria-label={de?'Hinweise zur Radialkarte':'Radial map information'}>
  {current&&center<90&&<button type="button" disabled={p.disabled||!p.canGoUp} onClick={()=>{if(!latest.current.disabled&&latest.current.canGoUp)latest.current.onUp();}}>{de?'Eine Ebene hoch':'Up one level'}</button>}
  <p style={{overflowWrap:'anywhere'}}> {root.name} · {size}{root.unknown?` · ${de?'Unbekannte Größen vorhanden':'Unknown sizes present'}`:''}</p>
  <p>{de?'Ringwinkel vergleichen Größen innerhalb derselben Ebene. Freie äußere Bereiche sind kein freier Speicher. Kleine und ungemessene Objekte bleiben in der Objektliste erreichbar.':'Ring angles compare sizes within the same level. Blank outer areas are not free capacity. Small and unmeasured objects remain available in the object list.'}</p>
  {current&&current.presentation.groups.length>0&&<p>{de?'Kleine Geschwistersegmente zusammengefasst':'Small sibling segments grouped'}: {current.presentation.groups.map(g=>`${regional.integer(g.directChildren)} / ${regional.integer(g.weight)} B`).join('; ')}</p>}
  {timeMissing&&<p role="status">{de?'Änderungsalter unbekannt: Referenzzeit fehlt.':'Modification age unknown: reference time unavailable.'}</p>}
  {failed&&<p role="status">{de?'Karte nicht verfügbar. Die Objektliste bleibt nutzbar.':'Map unavailable. The object list remains available.'}</p>}
  </div>
 </section>;
}
