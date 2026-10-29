import {RegionalNumber} from '../../regional-format';
import {nodeKindLabel} from '../../visualization/shared/labels';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { AnalysisNode } from '../../analysis-query';
import { copyPathText } from '../../NodeDetails';
import { requestNodeDetail, settleDetail, type HistoricalDetail } from '../../node-details-client';
import { SessionDenied } from '../../session';
import { interactionKey, nodeCapabilities, nodeInteraction, selectionID, type InteractionIdentity } from './interaction';
import { interactionMessages } from './messages';

export interface AnalysisNodeListProps {
 identity:InteractionIdentity; nodes:AnalysisNode[]; selectedNodeId:string|null; language:'de'|'en'; disabled:boolean; fileStationDisabled:boolean;
 onSelect:(node:AnalysisNode)=>void; onOpen:(node:AnalysisNode)=>void; onDetails:(node:AnalysisNode)=>void; onFileStation:(node:AnalysisNode)=>void;
 onSessionDenied:()=>void; onUnavailable?:(code:string)=>void;
}
// The list is an accessible consumer of the same adapter contract as future map hit tests.
export function AnalysisNodeList(p:AnalysisNodeListProps) {
 // DOM identity excludes selection epochs; requests still use the complete identity below.
 const t=interactionMessages[p.language],key=JSON.stringify([p.identity.profileId,p.identity.viewId,p.identity.snapshotId,p.identity.rootId]);
 // Only the visible page mounts stateful rows; paging never changes the central selection or data.
 const selectedIndex=p.selectedNodeId===null?-1:p.nodes.findIndex(n=>selectionID(n)===p.selectedNodeId);
 const selectionPage=selectedIndex<0?0:Math.floor(selectedIndex/100);
 const [paging,setPaging]=useState({key,selected:p.selectedNodeId,index:selectedIndex,page:selectionPage});
 const changed=paging.key!==key||paging.selected!==p.selectedNodeId||paging.index!==selectedIndex;
 const lastPage=Math.max(0,Math.ceil(p.nodes.length/100)-1);
 const page=Math.min(changed?selectionPage:paging.page,lastPage),start=page*100,end=Math.min(start+100,p.nodes.length);
 // Synchronize external selection and identity changes without snapping back on manual page changes.
 useEffect(()=>{setPaging({key,selected:p.selectedNodeId,index:selectedIndex,page:selectionPage});},[key,p.selectedNodeId,selectedIndex]);
 const move=(delta:number)=>{if(!p.disabled)setPaging({key,selected:p.selectedNodeId,index:selectedIndex,page:Math.max(0,Math.min(lastPage,page+delta))});};
 const de=p.language==='de';
 return <section aria-label={t.list}>
  <nav aria-label={de?'Seiten der Objektliste':'Object list pages'}>
   <button type="button" disabled={p.disabled||page===0} onClick={()=>move(-1)}>{de?'Vorherige Seite':'Previous page'}</button>
   <span role="status" aria-live="polite">{de?'Einträge':'Items'} {p.nodes.length?start+1:0}–{end} {de?'von':'of'} {p.nodes.length}</span>
   <button type="button" disabled={p.disabled||page===lastPage} onClick={()=>move(1)}>{de?'Nächste Seite':'Next page'}</button>
  </nav>
  {/* Keep the complete page keyboard-accessible without expanding the surrounding map workspace. */}
  <div className="table-scroll" role="region" aria-label={t.list} tabIndex={p.disabled?-1:0}>
   <table aria-label={t.list}><thead><tr><th>{t.name}</th><th>{t.type}</th><th>{t.bytes}</th><th>{t.actions}</th></tr></thead><tbody>{p.nodes.slice(start,end).map(node=><AnalysisNodeRow key={`${key}:${node.id}`} {...p} node={node}/>)}</tbody></table>
  </div>
 </section>;
}
// Transient UI belongs to one qualified node; neither hover nor menu state changes the central view.
export function AnalysisNodeRow(p:AnalysisNodeListProps & {node:AnalysisNode}) {
 const t=interactionMessages[p.language],node=p.node,caps=nodeCapabilities(node),key=`${interactionKey(p.identity)}:${node.id}`;
 const current=useRef(''),latest=useRef(p);latest.current=p;current.current=p.disabled?'':key;
 const [menu,setMenu]=useState(false),[preview,setPreview]=useState<'hover'|'copy'|null>(null),[detail,setDetail]=useState<HistoricalDetail|null>(null),[notice,setNotice]=useState('');
 const anchor=useRef<HTMLButtonElement>(null),copyRef=useRef<HTMLElement>(null),menuRef=useRef<HTMLDivElement>(null),request=useRef<AbortController|null>(null),hoverTimer=useRef<ReturnType<typeof setTimeout>|undefined>(undefined);
 const adapter=useMemo(()=>nodeInteraction(node,{current:()=>current.current===key,select:n=>latest.current.onSelect(n),open:n=>latest.current.onOpen(n)}),[key]);
 useEffect(()=>{current.current=latest.current.disabled?'':key;setMenu(false);setPreview(null);setDetail(null);setNotice('');return()=>{current.current='';request.current?.abort();clearTimeout(hoverTimer.current);};},[adapter,key]);
 useEffect(()=>{if(p.disabled){request.current?.abort();clearTimeout(hoverTimer.current);setMenu(false);setPreview(null);setDetail(null);setNotice('');}},[p.disabled,adapter]);
 useEffect(()=>{if(menu)menuRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();},[menu]);
 // A non-modal copy panel returns focus on explicit close without trapping the surrounding list.
 useEffect(()=>{if(preview!=='copy')return;copyRef.current?.focus();return()=>{if(anchor.current?.isConnected)anchor.current.focus();};},[preview]);
 const closePreview=()=>{request.current?.abort();clearTimeout(hoverTimer.current);setPreview(null);setDetail(null);setNotice('');};
 const showPreview=(intent:'hover'|'copy')=>{
  if(current.current!==key||!caps.selectable)return;
  request.current?.abort();const controller=new AbortController();request.current=controller;
  const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(8000)]);
  setPreview(intent);setDetail(null);setNotice(t.loading);
  // The existing detail client validates every returned identity, path encoding and byte value.
  void settleDetail(requestNodeDetail({...p.identity,nodeId:selectionID(node)},'details','',signal),controller.signal,key,()=>current.current,value=>{setDetail(value);setNotice('');},error=>{
   if(error instanceof SessionDenied){closePreview();latest.current.onSessionDenied();return;}
   setNotice(t.failed);if(error instanceof Error&&['VIEW_EXPIRED','SNAPSHOT_MISMATCH','INDEX_REFRESH_REQUIRED'].includes(error.message))latest.current.onUnavailable?.(error.message);
  });
 };
 const closeMenu=()=>{setMenu(false);anchor.current?.focus();};
 const action=(run:()=>void)=>{if(current.current!==key)return;closeMenu();closePreview();run();};
 const startMenu=()=>{if(current.current!==key||!caps.selectable)return;closePreview();setMenu(true);};
 const copy=async()=>{
  if(current.current!==key||!detail?.paths||!caps.copy)return;
  const signal=request.current?.signal,path=detail.paths.nasPath.text;
  try{await copyPathText(path);if(!signal?.aborted&&current.current===key)setNotice(t.copied);}catch{if(!signal?.aborted&&current.current===key)setNotice(t.failed);}
 };
 return <tr aria-selected={p.selectedNodeId===selectionID(node)} onContextMenu={event=>{if(caps.selectable){event.preventDefault();startMenu();}}}>
  <td style={{overflowWrap:'anywhere'}}>{caps.selectable?<button type="button" disabled={p.disabled} aria-pressed={p.selectedNodeId===selectionID(node)}
   onClick={event=>{if(event.detail===0)adapter.select();else adapter.click();}} onDoubleClick={()=>adapter.doubleClick()}
   onKeyDown={event=>{if(event.key==='ContextMenu'||event.key==='F10'&&event.shiftKey){event.preventDefault();startMenu();}}}
   onPointerEnter={event=>{if(event.pointerType==='mouse'&&!menu){clearTimeout(hoverTimer.current);hoverTimer.current=setTimeout(()=>showPreview('hover'),180);}}}
   onPointerLeave={()=>{clearTimeout(hoverTimer.current);if(preview==='hover')closePreview();}}
   style={{whiteSpace:'normal',overflowWrap:'anywhere',textAlign:'start'}}>{node.name}</button>:<span>{node.name} — {t.aggregate}</span>}</td>
  <td>{nodeKindLabel(node.kind,p.language)}</td><td><RegionalNumber value={node.summary.knownBytes} language={p.language}/> B{node.summary.unknownFileCount!=='0'?` + ${t.unknown}: ${node.summary.unknownFileCount}`:''}</td>
  <td>{caps.selectable&&<><button type="button" disabled={p.disabled} onClick={()=>action(()=>p.onDetails(node))}>{t.details}</button>{caps.open&&<button type="button" disabled={p.disabled} onClick={()=>action(()=>adapter.open())}>{t.open}</button>}<button ref={anchor} type="button" disabled={p.disabled} aria-haspopup="menu" aria-expanded={menu} onClick={()=>menu?closeMenu():startMenu()}>{t.actions}</button></>}
   {menu&&<div ref={menuRef} role="menu" aria-label={`${t.actions}: ${node.name}`} style={{position:'fixed',insetInlineEnd:16,top:80,zIndex:50,background:'Canvas',color:'CanvasText',border:'1px solid',padding:12,display:'grid',maxWidth:'min(32rem, calc(100vw - 32px))'}} onKeyDown={event=>{
    if(event.key==='Escape'){event.preventDefault();closeMenu();return;}
    if(['ArrowDown','ArrowUp','Home','End'].includes(event.key)){event.preventDefault();const buttons=Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')??[]);const i=buttons.indexOf(document.activeElement as HTMLButtonElement),next=event.key==='Home'?0:event.key==='End'?buttons.length-1:(i+(event.key==='ArrowDown'?1:-1)+buttons.length)%buttons.length;buttons[next]?.focus();}
    if(event.key==='Tab')setMenu(false);
   }}>
    <button type="button" role="menuitem" onClick={()=>action(()=>adapter.select())}>{t.select}</button>
    <button type="button" role="menuitem" onClick={()=>action(()=>p.onDetails(node))}>{t.details}</button>
    {caps.open&&<button type="button" role="menuitem" onClick={()=>action(()=>adapter.open())}>{t.open}</button>}
    {caps.fileStation&&<button type="button" role="menuitem" disabled={p.fileStationDisabled||p.disabled} onClick={()=>{if(!latest.current.fileStationDisabled)action(()=>p.onFileStation(node));}}>{t.fileStation}</button>}
    {caps.copy&&<button type="button" role="menuitem" onClick={()=>{closeMenu();showPreview('copy');}}>{t.copy}</button>}
    <button type="button" role="menuitem" onClick={closeMenu}>{t.close}</button>
   </div>}
   {preview&&<aside ref={copyRef} tabIndex={preview==='copy'?-1:undefined} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();closePreview();anchor.current?.focus();}}} role={preview==='copy'?'dialog':'tooltip'} aria-label={t.preview} style={{position:'fixed',insetInlineEnd:16,bottom:16,zIndex:51,background:'Canvas',color:'CanvasText',border:'1px solid',padding:12,maxWidth:'min(40rem, calc(100vw - 32px))',maxHeight:'50vh',overflow:'auto',overflowWrap:'anywhere',pointerEvents:preview==='hover'?'none':undefined}}>
    <strong>{detail?.detail.name??node.name}</strong><p>{nodeKindLabel(node.kind,p.language)} · <RegionalNumber value={node.summary.knownBytes} language={p.language}/> B</p>
    {detail?.detail.node&&<dl><dt>{t.volume}</dt><dd>{detail.detail.node.volumeId}</dd><dt>{t.share}</dt><dd>{detail.detail.node.shareId??t.unknown}</dd></dl>}
    {detail?.paths&&<><p>{t.path}: {detail.paths.nasPath.text}</p><p>{t.historical}</p>{detail.paths.nasPath.encoding==='escaped-bytes'&&<p>{t.escaped}</p>}</>}
    <p role="status">{notice}</p>{preview==='copy'&&<><button type="button" disabled={!detail?.paths||p.disabled} onClick={()=>void copy()}>{t.copy}</button><button type="button" onClick={closePreview}>{t.close}</button></>}
   </aside>}
  </td>
 </tr>;
}
