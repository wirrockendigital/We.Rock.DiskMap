import {useRef} from 'react';
// Pointer capture keeps resizing stable outside the narrow divider; keyboard input uses the same bounds.
export function PaneResizeHandle({side,width,onWidth}:{side:'left'|'right';width:number;onWidth:(width:number)=>void}){
 const drag=useRef<{x:number;width:number}|null>(null),minimum=side==='left'?180:240;
 const change=(next:number)=>onWidth(Math.round(Math.max(minimum,Math.min(side==='left'?700:800,window.innerWidth*.4,next))));
 return <div className={'disk-pane-resize '+side} role="separator" aria-label={side==='left'?'Breite des Verzeichnisbaums':'Breite der Dateiliste'} aria-orientation="vertical" aria-valuenow={width} aria-valuemin={minimum} aria-valuemax={side==='left'?700:800} tabIndex={0}
 onPointerDown={e=>{if(e.button!==0)return;drag.current={x:e.clientX,width};e.currentTarget.setPointerCapture(e.pointerId);e.preventDefault();}}
 onPointerMove={e=>{if(drag.current)change(drag.current.width+(e.clientX-drag.current.x)*(side==='left'?1:-1));}}
 onPointerUp={e=>{drag.current=null;if(e.currentTarget.hasPointerCapture(e.pointerId))e.currentTarget.releasePointerCapture(e.pointerId);}}
 onLostPointerCapture={()=>{drag.current=null;}}
 onKeyDown={e=>{if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();change(width+(e.key==='ArrowRight'?20:-20)*(side==='left'?1:-1));}}}/>;
}
