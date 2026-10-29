import {useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import type {Language, StorageNode} from './model';

export interface FileContextMenuProps {
  open?: boolean;
  node: StorageNode;
  x: number;
  y: number;
  language: Language;
  onClose: () => void;
  onReveal?: () => void;
  onOpen?: () => void;
  onDelete?: () => void;
  onRescan?: () => void;
  onReset?: () => void;
  rescanOnly?: boolean;
}

// Closing unmounts the menu so listeners and focus ownership are released immediately.
export function FileContextMenu({open=true,...props}:FileContextMenuProps) {
  return open?<FileContextMenuContents {...props}/>:null;
}

// The caller owns every action; unavailable integrations remain explicit and inert.
function FileContextMenuContents({node,x,y,language,onClose,onReveal,onOpen,onDelete,onRescan,onReset,rescanOnly}:FileContextMenuProps) {
  const menu=useRef<HTMLDivElement>(null),items=useRef<Array<HTMLButtonElement|null>>([]);
  const latestClose=useRef(onClose);latestClose.current=onClose;
  const [position,setPosition]=useState({left:x,top:y});
  const explanation=useId(),de=language==='de';
  const actions=rescanOnly||onRescan?[{label:de?'Rescanne Ordner':'Rescan folder',run:onRescan},...(onReset?[{label:de?'Index für diesen Ordner löschen':'Delete index for this folder',run:onReset}]:[]),...(onReveal?[{label:de?'In File Station anzeigen':'Show in File Station',run:onReveal}]:[])]:[
    {label:de?'In File Station anzeigen':'Show in File Station',run:onReveal},
    {label:de?'Öffnen':'Open',run:onOpen},
    {label:de?'Löschen':'Delete',run:onDelete},
  ];

  // Measure the real menu, including translated labels, before clamping it to the viewport.
  useLayoutEffect(()=>{
    const place=()=>{const rect=menu.current?.getBoundingClientRect();if(!rect)return;
      setPosition({left:Math.max(8,Math.min(x,window.innerWidth-rect.width-8)),top:Math.max(8,Math.min(y,window.innerHeight-rect.height-8))});};
    place();window.addEventListener('resize',place);return()=>window.removeEventListener('resize',place);
  },[x,y,language,node.name]);

  // Restore focus only while it is still inside this menu; outside clicks retain their target.
  useEffect(()=>{
    const element=menu.current,origin=document.activeElement instanceof HTMLElement?document.activeElement:null;
    items.current[0]?.focus({preventScroll:true});
    const outside=(event:PointerEvent)=>{if(element&&!element.contains(event.target as Node))latestClose.current();};
    document.addEventListener('pointerdown',outside,true);
    return()=>{document.removeEventListener('pointerdown',outside,true);if(element?.contains(document.activeElement)&&origin?.isConnected)origin.focus({preventScroll:true});};
  },[node.id]);

  // Keep the target name accessible without repeating it as a visible heading.
  return <div ref={menu} className="disk-context-menu" role="menu" aria-label={node.name}
    style={{position:'fixed',left:position.left,top:position.top,zIndex:1000,width:'max-content',minWidth:220,maxWidth:'calc(100vw - 16px)',maxHeight:'calc(100dvh - 16px)',overflow:'auto',padding:8,border:'1px solid var(--disk-line)',borderRadius:9,background:'var(--disk-panel)',color:'var(--disk-text)',boxShadow:'0 12px 32px #0005'}}
    onKeyDown={event=>{
      if(event.key==='Escape'){event.preventDefault();event.stopPropagation();onClose();return;}
      if(event.key==='Tab'){onClose();return;}
      const current=items.current.findIndex(item=>item===document.activeElement);
      const target=event.key==='Home'?0:event.key==='End'?actions.length-1:event.key==='ArrowDown'?(current+1)%actions.length:event.key==='ArrowUp'?(current-1+actions.length)%actions.length:null;
      if(target!==null){event.preventDefault();items.current[target]?.focus({preventScroll:true});}
    }}>
    {actions.map((action,index)=><button key={index} ref={element=>{items.current[index]=element;}} type="button" role="menuitem" tabIndex={-1}
      aria-disabled={!action.run} aria-describedby={!action.run?explanation:undefined}
      style={{display:'flex',width:'100%',padding:'9px',border:0,borderRadius:5,background:'transparent',color:action.run?'var(--disk-text)':'var(--disk-muted)',textAlign:'left'}}
      onClick={()=>{if(!action.run)return;onClose();action.run();}}>{action.label}</button>)}
    {actions.some(action=>!action.run)&&<p id={explanation} style={{padding:'6px 9px 2px',margin:0,color:'var(--disk-muted)',fontSize:12}}>{de?'Diese Aktion ist für diese Datei nicht verfügbar.':'This action is not available for this file.'}</p>}
  </div>;
}
