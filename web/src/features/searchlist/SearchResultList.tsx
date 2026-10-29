import {RegionalBytes} from '../../regional-format';
import {SessionDenied} from '../../session';
import {useEffect,useRef,useState} from 'react';
import type {PreferenceView} from '../../contracts';
import type {ViewPin} from '../../snapshot-reader';
import type {AnalysisNode} from '../../analysis-query';
import {nodeCapabilities} from '../analysisinteraction/interaction';
import {checkedSearchListPage,initialPagePosition,listIdentity,nextPagePosition,previousPagePosition,resultNode,type SearchListPage} from './types';
import {listMessages} from './messages';
export interface SearchResultListProps {
 language:'de'|'en';view:PreferenceView;pin:ViewPin;selectedNodeId:string|null;disabled:boolean;fileStationDisabled?:boolean;
 loadPage:(cursor:string,signal:AbortSignal)=>Promise<SearchListPage>;
 onSelect:(node:AnalysisNode)=>void;onOpen:(node:AnalysisNode)=>void;onDetails:(node:AnalysisNode)=>void;onFileStation:(node:AnalysisNode)=>void;
 onSessionDenied:()=>void;onUnavailable?:(code:string)=>void;
}
// Selection is central; only one bounded result page and cursor history live here.
export function SearchResultList(props:SearchResultListProps){
 const key=listIdentity(props);return <BoundSearchResultList key={key} {...props}/>;
}
export function BoundSearchResultList(props:SearchResultListProps){
 const t=listMessages[props.language],refs=useRef(props);refs.current=props;
 const [position,setPosition]=useState(initialPagePosition),[page,setPage]=useState<SearchListPage|null>(null),[error,setError]=useState(false),[retry,setRetry]=useState(0);
 const key=listIdentity(props);
 // Callback identity is deliberately excluded: ordinary parent renders must not restart reads.
 useEffect(()=>{if(props.disabled)return;const abort=new AbortController();let current=true;setPage(null);setError(false);
  const timer=setTimeout(()=>{if(current)setError(true);abort.abort();},8000);
  void refs.current.loadPage(position.cursor,abort.signal).then(raw=>{if(current&&!abort.signal.aborted)setPage(checkedSearchListPage(raw,refs.current));}).catch(cause=>{
   if(!current)return;setError(true);if(abort.signal.aborted)return;const code=cause instanceof Error?cause.message:'';
   if(cause instanceof SessionDenied||code==='SESSION_DENIED'||code==='SESSION_REQUIRED')refs.current.onSessionDenied();else if(['VIEW_EXPIRED','SNAPSHOT_MISMATCH'].includes(code))refs.current.onUnavailable?.(code);
  }).finally(()=>clearTimeout(timer));
  return()=>{current=false;clearTimeout(timer);abort.abort();};
 },[key,position.cursor,retry,props.disabled]);
 return <section aria-label={t.list}><h3>{t.list}</h3><p>{t.canvas}</p>
 {error?<p role="alert">{t.failed} <button disabled={props.disabled} onClick={()=>setRetry(x=>x+1)}>{t.retry}</button></p>:!page?<p role="status">{t.loading}</p>:<>
 {!page.items.length&&<p role="status">{page.hasMore?t.bounded:t.none}</p>}{page.workLimited&&<p>{t.limited}</p>}
 {/* Bound both axes while retaining every result and keeping paging outside the scroll region. */}
 <div className="table-scroll" role="region" aria-label={t.list} tabIndex={props.disabled?-1:0}><table aria-label={t.list}><thead><tr><th>{t.name}</th><th>{t.path}</th><th>{t.type}</th><th>{t.size}</th><th>{t.details}</th></tr></thead><tbody>
 {page.items.map(item=>{const node=resultNode(item),caps=nodeCapabilities(node);return <tr key={item.nodeId} aria-selected={props.selectedNodeId===item.nodeId}>
 <td><button disabled={props.disabled} aria-pressed={props.selectedNodeId===item.nodeId} onClick={()=>props.onSelect(node)} onDoubleClick={()=>caps.doubleClick?props.onOpen(node):props.onSelect(node)}>{item.name}</button></td>
 <td style={{maxWidth:320,overflowWrap:'anywhere'}}>{item.path}</td><td>{item.kind==='file'?`${t.file} · ${item.classification?.category?t[item.classification.category as 'image']:t.unknown}`:item.kind==='directory'?t.directory:({symlink:props.language==='de'?'Symbolischer Link':'Symbolic link',fifo:'FIFO',socket:'Socket',blockDevice:props.language==='de'?'Blockgerät':'Block device',characterDevice:props.language==='de'?'Zeichengerät':'Character device'}[item.kind]??t.unknown)}</td><td>{item.size===null?t.unknown:<><RegionalBytes value={item.size} language={props.language}/></>}</td>
 <td><button disabled={props.disabled} onClick={()=>props.onDetails(node)}>{t.details}</button>{caps.open&&<button disabled={props.disabled} onClick={()=>props.onOpen(node)}>{t.open}</button>}{caps.fileStation&&<button disabled={props.disabled||props.fileStationDisabled} onClick={()=>props.onFileStation(node)}>{t.fileStation}</button>}</td></tr>;})}
 </tbody></table></div><nav aria-label={t.list}><button disabled={props.disabled||position.number===1} onClick={()=>setPosition(initialPagePosition())}>{t.first}</button><button disabled={props.disabled||!position.back.length} onClick={()=>setPosition(previousPagePosition(position))}>{t.previous}</button><span>{t.page} {position.number}</span><button disabled={props.disabled||!page.hasMore} onClick={()=>setPosition(nextPagePosition(position,page.nextCursor))}>{t.next}</button></nav></>}
 </section>;
}
