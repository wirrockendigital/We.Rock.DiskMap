import {RegionalNumber} from '../../regional-format';
import {useEffect,useRef,useState} from 'react';
import type {MapModel,MapNode,Continuation} from './model';
import type {HitTarget} from './interaction';
import {groupDirectChildren,groupContinuation} from './groups';

export interface GroupInspectionPage {snapshotId:string;rootId:string;metric:MapModel['metric'];mode:MapModel['mode'];items:readonly MapNode[];nextCursor:string|null;workLimited:boolean}
export interface GroupInspectionPanelProps {
 model:MapModel;target:HitTarget;identity:string;language:'de'|'en';disabled:boolean;
 onClose:()=>void;onSelect:(node:MapNode)=>void;onOpen:(node:MapNode)=>void;onError?:(error:unknown)=>void;
 loadPage:(continuation:Continuation,cursor:string,signal:AbortSignal)=>Promise<GroupInspectionPage|null>;
}
// A keyed scope drops all local paging and pending responses when the authoritative view changes.
export function GroupInspectionPanel(p:GroupInspectionPanelProps){
 const key=JSON.stringify([p.identity,p.target]);return <GroupContents key={key} {...p}/>;
}
export function GroupContents(p:GroupInspectionPanelProps){
 const latest=useRef(p),mounted=useRef(true);latest.current=p;const close=useRef<HTMLButtonElement>(null);
 const [offset,setOffset]=useState(0),[cursor,setCursor]=useState<string|null>(null),[previous,setPrevious]=useState<string[]>([]),[retry,setRetry]=useState(0);
 const [page,setPage]=useState<GroupInspectionPage|null>(null),[state,setState]=useState<'loading'|'ready'|'pending'|'error'>('loading');
 const de=p.language==='de';
 let members:readonly string[]=[],continuation:Continuation|null=null,weight=0n,count=0n,valid=true;
 try{if(p.target.kind==='display-group'){const group=groupDirectChildren(p.model,p.target.parentId,p.target.memberIds);if(group.id!==p.target.groupId)throw Error('INVALID_MAP_GROUP');members=group.memberIds;weight=group.weight;count=group.directChildren;}
 else{continuation=groupContinuation(p.model,p.target.nodeId);weight=p.model.nodes[p.target.nodeId].weight;count=continuation.directChildren;}}catch{valid=false;}
 const requestCursor=cursor??continuation?.cursor??'',binding=JSON.stringify([p.identity,p.target,requestCursor]);
 // Restore the initiating keyboard target when this non-modal inspection closes.
 useEffect(()=>{mounted.current=true;const origin=typeof document==='undefined'?null:document.activeElement;close.current?.focus();return()=>{mounted.current=false;if(typeof HTMLElement!=='undefined'&&origin instanceof HTMLElement&&origin.isConnected)origin.focus();};},[]);
 useEffect(()=>{
  if(!valid||!continuation||p.disabled)return;
  let current=true;const controller=new AbortController();setPage(null);setState('loading');
  // Timeout invalidates both late data and late authentication side effects.
  const timer=setTimeout(()=>{controller.abort();if(current)setState('error');},8000);
  void latest.current.loadPage(continuation,requestCursor,controller.signal).then(value=>{
   if(!current||controller.signal.aborted)return;
   if(value===null){setState('pending');return;}
   if(value.snapshotId!==p.model.snapshotId||value.rootId!==continuation.rootId||value.metric!==p.model.metric||value.mode!==p.model.mode||value.items.length>100||new Set(value.items.map(n=>n.id)).size!==value.items.length||value.items.some(n=>n.parentId!==continuation.rootId||n.kind==='group')||(value.nextCursor===requestCursor&&!(value.workLimited&&value.items.length===0)))throw Error('INVALID_GROUP_PAGE');
   setPage(value);setState('ready');
  }).catch(error=>{if(!current||controller.signal.aborted)return;setState('error');latest.current.onError?.(error);}).finally(()=>clearTimeout(timer));
  return()=>{current=false;controller.abort();clearTimeout(timer);};
 },[binding,p.model,p.disabled,retry]);
 // Preserve exact integer counts when selecting the localized singular form.
 const countLabel=count===1n?(de?'direktes Element':'direct entry'):(de?'direkte Elemente':'direct entries');
 const rows=continuation?page?.items??[]:members.slice(offset,offset+100).map(id=>p.model.nodes[id]);
 // No group identifier or unqualified row reaches a backend-facing callback.
 const act=(node:MapNode,open=false)=>{if(!mounted.current||latest.current.disabled||node.kind==='group')return;if(open&&!['nas','volume','share','other','directory'].includes(node.kind))return;(open?latest.current.onOpen:latest.current.onSelect)(node);};
 return <section aria-label={de?'Darstellungsgruppe':'Display group'} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();latest.current.onClose();}}}>
  <h4>{de?'Gruppierte direkte Elemente':'Grouped direct entries'}</h4>
  <button ref={close} type="button" onClick={()=>latest.current.onClose()}>{de?'Gruppeninformation schließen':'Close group information'}</button>
  {!valid?<p role="status">{de?'Diese Gruppe ist nicht mehr verfügbar.':'This group is no longer available.'}</p>:<>
   <p>{p.model.metric==='logical'?(de?'Logische Größe':'Logical size'):(de?'Belegte Größe':'Allocated size')}: <RegionalNumber value={weight.toString()} language={p.language}/> B · <RegionalNumber value={count.toString()} language={p.language}/> {countLabel}</p>
   <p>{de?'Reine Darstellungsgruppe desselben Elternknotens, ohne eigenen Dateipfad. Die Liste zeigt direkte Elemente, keine addierbaren Vorfahren und Nachfahren.':'Display-only group of one parent, without its own filesystem path. The list contains direct entries, not additive ancestors and descendants.'}</p>
   {continuation&&state!=='ready'&&<p role="status">{state==='loading'?(de?'Gruppenliste wird geladen.':'Loading group list.'):state==='pending'?(de?'Die gefilterte Liste wird vorbereitet.':'The filtered list is being prepared.'):(de?'Gruppenliste derzeit nicht verfügbar.':'Group list is currently unavailable.')}</p>}
   {continuation&&(state==='pending'||state==='error'||page?.workLimited&&page.nextCursor===requestCursor&&page.items.length===0)&&<button disabled={p.disabled} onClick={()=>{if(!latest.current.disabled)setRetry(n=>n+1);}}>{de?'Erneut versuchen':'Retry'}</button>}
   <ul>{rows.map(node=><li key={node.id}><span>{node.name} · <RegionalNumber value={node.weight.toString()} language={p.language}/> B{node.unknown?' + ?':''}</span> <button disabled={p.disabled} onClick={()=>act(node)}>{de?'Auswählen':'Select'}</button>{['nas','volume','share','other','directory'].includes(node.kind)&&<button disabled={p.disabled} onClick={()=>act(node,true)}>{de?'Hier öffnen':'Open here'}</button>}</li>)}</ul>
   {page?.workLimited&&<p role="status">{de?'Begrenzter Ausschnitt; weitere Elemente können folgen.':'Bounded excerpt; more entries may follow.'}</p>}
   <p aria-live="polite">{continuation?`${de?'Elemente auf dieser Seite':'Entries on this page'}: ${rows.length}`:`${members.length?offset+1:0}–${Math.min(offset+100,members.length)} / ${members.length}`}</p>
   <button disabled={p.disabled||(continuation?previous.length===0:offset===0)||!!continuation&&state==='loading'} onClick={()=>{if(latest.current.disabled)return;if(continuation){if(!previous.length)return;setCursor(previous[previous.length-1]);setPrevious(previous.slice(0,-1));}else setOffset(Math.max(0,offset-100));}}>{de?'Vorherige Seite':'Previous page'}</button>
   <button disabled={p.disabled||(continuation?!page?.nextCursor||page.nextCursor===requestCursor:offset+100>=members.length)||!!continuation&&state==='loading'} onClick={()=>{if(latest.current.disabled)return;if(continuation){if(!page?.nextCursor||page.nextCursor===requestCursor)return;setPrevious([...previous,requestCursor].slice(-64));setCursor(page.nextCursor);}else setOffset(Math.min(Math.max(0,members.length-1),offset+100));}}>{de?'Nächste Seite':'Next page'}</button>
  </>}
 </section>;
}
