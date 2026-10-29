import {useDisplay} from '../../features/settings/display';
import {useRegionalFormat} from '../../regional-format';
import {nodeKindLabel} from '../shared/labels';
import {useEffect,useMemo,useRef,useState} from 'react';
import type {MapModel} from '../shared/model';
import type {ColorMode} from '../shared/colors';
import {hitTest,pointerPoint,keyboardTarget,type Geometry,type HitTarget} from '../shared/interaction';
import {configureCanvas} from '../shared/canvas-surface';
import {treemapPresentation} from './presentation';
import {activateTreemap,type TreemapActions} from './actions';
import {paintTreemap} from './paint';

export interface TreemapCanvasProps extends TreemapActions {
 model:MapModel;width:number;height:number;devicePixelRatio:number;colorMode:ColorMode;referenceTime:string|null;
 selectedNodeId:string|null;focusedNodeId:string|null;disabled:boolean;language:'de'|'en';theme?:'light'|'dark';
 maxDepth?:number;maxElements?:number;onPreview?:(id:string|null)=>void;onGeometry?:(geometry:readonly Geometry[])=>void;
}
// The renderer shares one immutable layout; color, focus and selection changes only repaint it.
export function TreemapCanvas(props:TreemapCanvasProps) {
 const display=useDisplay();
 const regional=useRegionalFormat(props.language);
 const canvas=useRef<HTMLCanvasElement>(null),[failed,setFailed]=useState(false),[localFocus,setLocalFocus]=useState<{model:MapModel;external:string|null;id:string|null}|null>(null),[hover,setHover]=useState<string|null>(null);
 const painted=useRef<object|null>(null);
 // A new authoritative focus or model invalidates the temporary pointer/keyboard focus.
 const focus=localFocus?.model===props.model&&localFocus.external===props.focusedNodeId?localFocus.id:null;
 const setFocus=(id:string|null)=>setLocalFocus({model:props.model,external:props.focusedNodeId,id});
 const presentation=useMemo(()=>treemapPresentation(props.model,{width:props.width,height:props.height,maxDepth:props.maxDepth,maxElements:props.maxElements,decoration:true}),[props.model,props.width,props.height,props.maxDepth,props.maxElements]);
 const layout=presentation.layout;
 useEffect(()=>{
  try{if(!canvas.current)return;configureCanvas(canvas.current,props.width,props.height,props.devicePixelRatio);const context=canvas.current.getContext('2d');if(!context)throw Error('CANVAS_UNAVAILABLE');paintTreemap(context,presentation.model,layout,{display,language:props.language,locale:regional.settings.locale,theme:props.theme??'light',colorMode:props.colorMode,referenceTime:props.referenceTime,selectedNodeId:props.selectedNodeId,focusedNodeId:focus??props.focusedNodeId,disabled:props.disabled});painted.current=presentation;setFailed(false);props.onGeometry?.(layout.rectangles);}
  catch{painted.current=null;setFailed(true);props.onGeometry?.([]);}
  return()=>{painted.current=null;props.onGeometry?.([]);};
 },[display,regional.settings.locale,presentation,focus,layout,props.model,props.width,props.height,props.devicePixelRatio,props.language,props.theme,props.colorMode,props.referenceTime,props.selectedNodeId,props.focusedNodeId,props.disabled,props.onGeometry]);
 // Interaction accepts only geometry belonging to the currently painted immutable model.
 const enabled=()=>!props.disabled&&!failed&&painted.current===presentation;
 const at=(x:number,y:number):HitTarget|null=>{if(!enabled()||!canvas.current)return null;const point=pointerPoint(x,y,canvas.current.getBoundingClientRect(),props.width,props.height);return point?hitTest(layout.rectangles,point):null;};
 const activate=(target:HitTarget|null,open=false)=>{if(!target||!enabled())return;setFocus(target.kind==='model'?target.nodeId:target.groupId);activateTreemap(props.model,target,open,props);};
 const focused=focus&&presentation.model.nodes[focus]?focus:props.focusedNodeId;
 const hoverNode=hover?presentation.model.nodes[hover]:undefined;
 return <div className="treemap-canvas" style={{position:'relative',width:props.width,height:props.height}}>
  <canvas ref={canvas} role="img" tabIndex={props.disabled||failed?-1:0} aria-label={props.language==='de'?'Disk Map – hierarchische Speicherbelegung':'Disk Map — hierarchical storage usage'} aria-disabled={props.disabled||failed}
   onClick={event=>activate(at(event.clientX,event.clientY))} onDoubleClick={event=>activate(at(event.clientX,event.clientY),true)}
   onPointerMove={event=>{if(event.pointerType==='touch')return;const target=at(event.clientX,event.clientY),id=target?(target.kind==='model'?target.nodeId:target.groupId):null;setHover(id);props.onPreview?.(target?.kind==='model'&&props.model.nodes[target.nodeId].kind!=='group'?target.nodeId:null);}}
   onPointerLeave={()=>{setHover(null);props.onPreview?.(null);}}
   onKeyDown={event=>{if(!enabled())return;if(['Home','End','ArrowDown','ArrowUp','ArrowLeft','ArrowRight'].includes(event.key)){event.preventDefault();const id=keyboardTarget(presentation.model,focused,event.key);setFocus(id);if(id&&props.model.nodes[id]?.kind!=='group'&&props.model.nodes[id])props.onFocus?.(id);}else if(event.key==='Enter'||event.key===' '){event.preventDefault();const target=layout.rectangles.find(r=>r.nodeId===focused)?.target;activate(target??null,event.key==='Enter'&&event.altKey);}}}
   style={{display:failed?'none':'block'}}/>
  {hoverNode&&!props.disabled&&!failed&&<div role="tooltip" style={{position:'absolute',left:4,bottom:4,maxWidth:'calc(100% - 8px)',pointerEvents:'none',background:props.theme==='dark'?'#171d26':'#ffffff',color:props.theme==='dark'?'#ffffff':'#111827',padding:4,overflowWrap:'anywhere'}}>{hoverNode.name} · {nodeKindLabel(hoverNode.kind,props.language)} · {regional.integer(hoverNode.weight)} B{hoverNode.unknown?' + ?':''} · {presentation.model.nodes[hoverNode.parentId??'']?.name??''}</div>}
  {failed&&<p role="status">{props.language==='de'?'Karte nicht verfügbar. Die Daten bleiben in der Liste erreichbar.':'Map unavailable. Data remains available in the list.'}</p>}
 </div>;
}
