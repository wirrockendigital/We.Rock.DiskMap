import {useSizeFormatter} from './size-unit';
import {createMapWorker} from './worker-runtime';
import {useEffect, useMemo, useRef, useState} from 'react';
import type {Geometry,Hit} from './chart-layout';
import { type Language, type StorageNode} from './model';

interface ChartProps {
  loadGeometry?:import('./chart-layout').GeometryLoader;
  savedZoom?:{root:string;scale:number;x:number;y:number}|null;onZoom?:(zoom:{root:string;scale:number;x:number;y:number})=>void;
  interactive?:boolean;
  root: StorageNode; mode: 'radial' | 'treemap'; language: Language;
  selectedId?: string | null;
  hovered: string | null; onHover: (id: string | null) => void;
  onOpen: (node: StorageNode) => void;
  onContextMenu?: (node: StorageNode, x: number, y: number) => void;
}


// Geometry and base paint do not depend on hover; interaction redraws a lightweight overlay only.
export function StorageChart({root,mode,language,hovered,selectedId,onHover,onOpen,onContextMenu,interactive=true,loadGeometry,savedZoom,onZoom}:ChartProps){
 const formatNodeSize=useSizeFormatter();
  const host=useRef<HTMLDivElement>(null),canvas=useRef<HTMLCanvasElement>(null),overlay=useRef<HTMLCanvasElement>(null);
  const [zoom,setZoom]=useState(savedZoom?.root===root.id?savedZoom:{scale:1,x:0,y:0});
  // Keep the same zoom object when its values are unchanged; identity changes restart layout.
  useEffect(()=>{const next=savedZoom?.root===root.id?savedZoom:{scale:1,x:0,y:0};setZoom(old=>old.scale===next.scale&&old.x===next.x&&old.y===next.y?old:next);},[root.id,mode]);
  useEffect(()=>{if(loadGeometry)onZoom?.({root:root.id,...zoom});},[zoom]);
  // An unmeasured placeholder must not render or evict a useful finished raster.
  const [box,setBox]=useState({width:0,height:0});
  // Keep only the parent lookup on the UI thread; paths are reconstructed for the hovered item only.
  const nodes=useMemo(()=>{const map=new Map<string,StorageNode>(),pending=[root];while(pending.length){const n=pending.pop()!;map.set(n.id,n);for(const c of n.children??[])pending.push(c);}return map;},[root]);
  const locationFor=(id:string)=>{const names:string[]=[];let n=nodes.get(id);for(let depth=0;n&&n.id!==root.id&&depth<512;depth++){if(n.name!=='.')names.push(n.name);n=nodes.get(n.parentId??'');}return names.reverse().join(' / ');};
  useEffect(()=>{
    if(!host.current)return;
    const observer=new ResizeObserver(([entry])=>{const next={width:Math.round(entry.contentRect.width),height:Math.round(entry.contentRect.height)};setBox(old=>old.width===next.width&&old.height===next.height?old:next);});
    observer.observe(host.current);return()=>observer.disconnect();
  },[]);
  const [pointerHit,setPointerHit]=useState<Hit|null>(null),pointerEpoch=useRef(0),pointerTimer=useRef<ReturnType<typeof setTimeout>|undefined>(undefined);
  const [geometry,setGeometry]=useState<Geometry>({hits:[],groups:[],cx:0,cy:0,radius:0,inner:0,complete:false});
  useEffect(()=>{
   // Wait for the actual viewport before admitting worker work.
   if(box.width<1||box.height<1)return;
   if(loadGeometry){const request=new AbortController();pointerEpoch.current++;setPointerHit(null);
    // Mark the new view pending before its first worker frame can arrive.
    setGeometry(old=>{old.bitmap?.close();return {hits:[],groups:[],cx:0,cy:0,radius:0,inner:0,complete:false};});
    // Close replaced bitmaps immediately; React state contains only the currently displayed frame.
    const publish=(value:Geometry)=>{if(request.signal.aborted){value.bitmap?.close();return;}setGeometry(old=>{if(old.bitmap!==value.bitmap)old.bitmap?.close();return value;});};
    void loadGeometry(root.id,mode,box,zoom,publish,request.signal).then(publish).catch(()=>{if(!request.signal.aborted)publish({hits:[],groups:[],cx:0,cy:0,radius:0,inner:0});});return()=>request.abort();}
   const worker=createMapWorker('layout');
   setGeometry({hits:[],groups:[],cx:0,cy:0,radius:0,inner:0});
   worker.onmessage=event=>{if(!event.data.error)setGeometry(event.data);worker.terminate();};
   worker.postMessage({root,mode,box});return()=>worker.terminate();
  },[root,mode,box,loadGeometry,zoom]);

  const displayedBitmap=useRef<ImageBitmap|undefined>(undefined);
  useEffect(()=>{displayedBitmap.current=geometry.bitmap;},[geometry.bitmap]);
  useEffect(()=>()=>{clearTimeout(pointerTimer.current);displayedBitmap.current?.close();},[]);
  const [labelHits,setLabelHits]=useState<Hit[]>([]);
  useEffect(()=>{let active=true;setLabelHits([]);if(geometry.complete&&geometry.resolveLabels)void geometry.resolveLabels().then(hits=>{if(active)setLabelHits(hits);}).catch(()=>{/* A newer view may supersede labels. */});return()=>{active=false;};},[geometry]);
  const hitById=useMemo(()=>new Map([...geometry.hits,...labelHits,...(pointerHit?[pointerHit]:[])].map(h=>[h.node.id,h])),[geometry,pointerHit,labelHits]);
  const groupById=useMemo(()=>new Map(geometry.groups.map(g=>[g.node.id,g])),[geometry]);
  // Arc boundaries and hit testing use exactly the same gap, including small sectors.
  function trace(ctx:CanvasRenderingContext2D,hit:Hit){
    ctx.beginPath();
    if(mode==='treemap'||hit.rasterBounds){ctx.rect(hit.x0,hit.y0,hit.x1-hit.x0,hit.y1-hit.y0);return;}
    const gap=Math.min(.002,(hit.x1-hit.x0)*.08),a=hit.x0-Math.PI/2+gap,b=hit.x1-Math.PI/2-gap;
    ctx.arc(geometry.cx,geometry.cy,hit.y1,a,b);ctx.arc(geometry.cx,geometry.cy,hit.y0,b,a,true);ctx.closePath();
  }
  // Gradients are limited to resolvable tiles. Dense one-pixel files retain the fast flat path.
  useEffect(()=>{
    const element=canvas.current;if(!element||box.width<1||box.height<1)return;
    const ctx=element.getContext('2d');if(!ctx)return;
    // Match the worker pixel budget for both visible canvases, including high-DPI 4K displays.
    const dpr=Math.min(devicePixelRatio||1,2,Math.sqrt(8388608/Math.max(1,box.width*box.height)));element.width=box.width*dpr;element.height=box.height*dpr;ctx.scale(dpr,dpr);
    if(geometry.bitmap)ctx.drawImage(geometry.bitmap,0,0,box.width,box.height);
    // Async geometry carries its own raster dimensions across resize and mode changes.
    if(geometry.raster&&geometry.rasterWidth&&geometry.rasterHeight){const image=new ImageData(new Uint8ClampedArray(geometry.raster),geometry.rasterWidth,geometry.rasterHeight);const buffer=document.createElement('canvas');buffer.width=geometry.rasterWidth;buffer.height=geometry.rasterHeight;buffer.getContext('2d')?.putImageData(image,0,0);ctx.drawImage(buffer,0,0);}
    for(const hit of (geometry.bitmap?labelHits:geometry.hits)){
      const {surface}=hit;ctx.fillStyle=surface.fill;
      if(mode==='radial'&&!geometry.bitmap){
        trace(ctx,hit);ctx.fill();
        if(hit.node.aggregated){ctx.save();trace(ctx,hit);ctx.clip();ctx.strokeStyle='rgba(255,255,255,.20)';ctx.lineWidth=1;for(let x=0;x<box.width+box.height;x+=12){ctx.beginPath();ctx.moveTo(x,0);ctx.lineTo(x-box.height,box.height);ctx.stroke();}ctx.restore();}
      }else if(mode==='treemap'){
        const width=hit.x1-hit.x0,height=hit.y1-hit.y0;if(width<=0||height<=0)continue;
        if(!geometry.bitmap){
        if(width>=14&&height>=14){
          // A shallow bevel stays inside the allocated tile: no overlap or false data area.
          ctx.fillStyle=surface.shade;ctx.fillRect(hit.x0,hit.y0,width,height);
          const lift=Math.min(3,height*.08),gradient=ctx.createLinearGradient(hit.x0,hit.y0,hit.x1,hit.y1);
          gradient.addColorStop(0,surface.light);gradient.addColorStop(.25,surface.fill);gradient.addColorStop(1,surface.shade);
          ctx.fillStyle=gradient;ctx.fillRect(hit.x0+.5,hit.y0+.5,width-1,height-lift-.5);
          ctx.strokeStyle='rgba(255,255,255,.30)';ctx.lineWidth=1;ctx.beginPath();ctx.moveTo(hit.x0+.5,hit.y0+height-lift);ctx.lineTo(hit.x0+.5,hit.y0+.5);ctx.lineTo(hit.x1-.5,hit.y0+.5);ctx.stroke();
          ctx.fillStyle='rgba(5,9,25,.22)';ctx.fillRect(hit.x0,hit.y1-1,width,1);
        }else ctx.fillRect(hit.x0,hit.y0,width,height);
        // Hatching distinguishes condensed folder contents from individual file tiles.
        if(hit.node.aggregated){ctx.save();ctx.beginPath();ctx.rect(hit.x0,hit.y0,width,height);ctx.clip();ctx.strokeStyle='rgba(255,255,255,.20)';for(let x=hit.x0-height;x<hit.x1;x+=12){ctx.beginPath();ctx.moveTo(x,hit.y1);ctx.lineTo(x+height,hit.y0);ctx.stroke();}ctx.restore();}
        }
        if(width>110&&height>52&&hit.node.name){
          ctx.save();ctx.beginPath();ctx.rect(hit.x0+7,hit.y0+6,width-14,height-12);ctx.clip();
          // Text gets an opaque, color-matched backing to preserve contrast over the lighting.
          const name=hit.residual?(language==='de'?'Weitere Inhalte':'Other contents'):hit.node.aggregated?hit.node.name+(hit.node.name.endsWith('Details')?'':' · Details'):hit.node.name;
          const size=formatNodeSize({...hit.node,bytesExact:hit.ownBytes??hit.node.bytesExact},language);
          ctx.font='600 12px -apple-system, sans-serif';const labelWidth=Math.min(width-14,Math.max(ctx.measureText(name).width,ctx.measureText(size).width)+10);
          ctx.fillStyle=surface.fill;ctx.fillRect(hit.x0+6,hit.y0+6,labelWidth,size?36:22);
          ctx.fillStyle=surface.text;ctx.fillText(name,hit.x0+10,hit.y0+21);ctx.font='11px -apple-system, sans-serif';ctx.fillText(size,hit.x0+10,hit.y0+37);ctx.restore();
        }
      }
    }
  },[geometry,box,mode,language,formatNodeSize,labelHits]);
  useEffect(()=>{
    const element=overlay.current;if(!element||box.width<1||box.height<1)return;
    const ctx=element.getContext('2d');if(!ctx)return;
    // Match the worker pixel budget for both visible canvases, including high-DPI 4K displays.
    const dpr=Math.min(devicePixelRatio||1,2,Math.sqrt(8388608/Math.max(1,box.width*box.height)));element.width=box.width*dpr;element.height=box.height*dpr;ctx.scale(dpr,dpr);
    // Highlight the closest visible enclosing folder only during interaction.
    const focus=hovered??selectedId;
    const focusedHit=focus?hitById.get(focus):undefined;
    const group=focusedHit?[...focusedHit.ancestors].map(id=>groupById.get(id)).filter(g=>g!==undefined).sort((a,b)=>b.depth-a.depth)[0]:undefined;
    if(group){
      ctx.strokeStyle='rgba(230,239,255,.85)';ctx.lineWidth=2;
      ctx.strokeRect(group.x0+1,group.y0+1,group.x1-group.x0-2,group.y1-group.y0-2);
    }
    for(const hit of new Set([hovered?hitById.get(hovered):undefined,selectedId?hitById.get(selectedId):undefined])){
      if(!hit)continue;const selected=hit.node.id===selectedId,active=!!hovered&&hit.ancestors.has(hovered);
      if(!active&&!selected)continue;
      ctx.save();trace(ctx,hit);ctx.clip();
      if(active){ctx.fillStyle='rgba(255,255,255,.12)';ctx.fill();}
      ctx.lineWidth=selected?4:2;ctx.strokeStyle=selected?'#ffffff':'rgba(255,255,255,.8)';ctx.stroke();ctx.restore();
    }
  },[geometry,box,mode,hovered,selectedId]);
  // Resolve only visible geometry; gaps never trigger a neighboring file action.
  const hitAt=async(clientX:number,clientY:number)=>{
    const rect=canvas.current!.getBoundingClientRect(),x=clientX-rect.left,y=clientY-rect.top;
    if(geometry.complete===false)return null;
    if(geometry.probe)return geometry.probe(x,y);
    if(mode==='treemap'){const candidates=geometry.cells?.get(Math.floor(x/64)+':'+Math.floor(y/64))??[];for(let i=candidates.length-1;i>=0;i--){const h=geometry.hits[candidates[i]];if(x>=h.x0&&x<h.x1&&y>=h.y0&&y<h.y1)return h;}return;}
    const dx=x-geometry.cx,dy=y-geometry.cy,r=Math.hypot(dx,dy),angle=(Math.atan2(dy,dx)+Math.PI*2.5)%(Math.PI*2);
    return geometry.hits.find(h=>{const gap=Math.min(.002,(h.x1-h.x0)*.08);return r>=h.y0&&r<h.y1&&angle>=h.x0+gap&&angle<h.x1-gap;});
  };
  const focused=hovered??selectedId;
  const location=focused?(locationFor(focused)||hitById.get(focused)?.node.path||''):'';
  const label=hitById.get(hovered??'')?.node??nodes.get(hovered??'')??root,parts=formatNodeSize(label.id===root.id?{...label,bytesExact:root.normalBytes??label.bytesExact}:label,language).split(/ (?=[^ ]+$)/);
  return <div ref={host} className="disk-chart-stage" aria-busy={geometry.complete===false}>
    <canvas onWheel={event=>{if(!loadGeometry||mode!=='treemap')return;const rect=event.currentTarget.getBoundingClientRect(),x=event.clientX-rect.left,y=event.clientY-rect.top;setZoom(old=>{const scale=Math.max(1,Math.min(64,old.scale*(event.deltaY<0?1.3:1/1.3))),ratio=scale/old.scale;return {scale,x:Math.max(box.width*(1-scale),Math.min(0,x-(x-old.x)*ratio)),y:Math.max(box.height*(1-scale),Math.min(0,y-(y-old.y)*ratio))};});}} ref={canvas} className="disk-chart-canvas" role="img" aria-label={!interactive?(language==='de'?'Nur lesbare Vorschau bereits erfasster Daten':'Read-only preview of already observed data'):language==='de'?'Speicherkarte. Ordner lassen sich auch in der Liste öffnen.':'Storage map. Folders can also be opened in the list.'}
      onPointerMove={event=>{if(!interactive)return;const epoch=++pointerEpoch.current,{clientX,clientY}=event;clearTimeout(pointerTimer.current);pointerTimer.current=setTimeout(()=>{void hitAt(clientX,clientY).then(hit=>{if(epoch!==pointerEpoch.current)return;setPointerHit(hit??null);onHover(hit?.node.detailTarget??hit?.node.id??null);}).catch(()=>{/* Navigation may revoke a pending hover lookup. */});},40);}} onPointerLeave={()=>{clearTimeout(pointerTimer.current);pointerEpoch.current++;setPointerHit(null);if(interactive)onHover(null);}}
      onContextMenu={async event=>{if(!interactive)return;event.preventDefault();const {clientX,clientY}=event;const hit=await hitAt(clientX,clientY).catch(()=>null);if(hit&&!hit.node.detailTarget&&onContextMenu){event.preventDefault();onContextMenu(hit.node,clientX,clientY);}}}
      onClick={async event=>{if(!interactive)return;const hit=await hitAt(event.clientX,event.clientY).catch(()=>null);if(hit){const chain=locationFor(hit.node.id);onOpen({...hit.node,id:hit.node.detailTarget??hit.node.id,path:chain||hit.node.path});}}}/>
    {geometry.complete===false&&<div className="disk-map-location" role="status">{language==='de'?'Details werden geladen …':'Loading details …'}</div>}
    {location&&<div className="disk-map-location">{nodes.get(focused!)?.kind==='file'?(language==='de'?'Datei · ':'File · '):(language==='de'?'Ordner · ':'Folder · ')}{location}{nodes.get(focused!)?.aggregated?(language==='de'?' · zusammengefasste Inhalte':' · grouped contents'):''}</div>}
    <canvas ref={overlay} className="disk-chart-overlay" aria-hidden="true"/>
    {mode==='radial'&&<div className="disk-chart-center"><strong>{parts[0]}</strong><span>{parts[1]}</span><small>{label.name}</small></div>}
  </div>;
}
