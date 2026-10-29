import {useSizeFormatter} from './size-unit';
import {categoryVisible} from './category-visibility';
import type {PreferenceWorkspace} from '../contracts';
import {useEffect,useRef,useState} from 'react';
import {MagnifyingGlassIcon,MixerHorizontalIcon,ChevronRightIcon} from '@radix-ui/react-icons';
import {StorageIcon} from './StorageIcon';
import {type StorageNode,type Language,sizeOf} from './model';
import {categoryLabels,defaultListOptions,type FileListLoader,type FileListPage,type ListOptions} from './list-options';
interface Props {workspace?:PreferenceWorkspace;onWorkspace?:(patch:Partial<PreferenceWorkspace>)=>void;root:StorageNode;identity:string;language:Language;load?:FileListLoader;selected:StorageNode|null;onSelect:(node:StorageNode)=>void;onHover:(id:string|null)=>void;onContext:(node:StorageNode,x:number,y:number)=>void;focusSearch?:boolean}
// The disclosure stays in document flow; collapsing it never resets active selectors.
export function FileListPanel({root,identity,language,load,selected,onSelect,onHover,onContext,focusSearch,workspace,onWorkspace}:Props){
 const formatNodeSize=useSizeFormatter();
 const de=language==='de',input=useRef<HTMLInputElement>(null);
 const [options,setOptions]=useState(()=>workspace?.list??defaultListOptions()),[expanded,setExpanded]=useState(workspace?.filterOpen??false),[stored,setPage]=useState<(FileListPage&{queryKey:string})|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState(false),[errorCode,setErrorCode]=useState(''),[cursor,setCursor]=useState(''),[retry,setRetry]=useState(0);
 // A new scope hides old rows synchronously, before effects or network replies run.
 const available=!root.unavailable;
 const visibilityKey=JSON.stringify([workspace?.showRecycle??false,workspace?.showSystem??false]);
 const queryKey=JSON.stringify([identity,options,available,visibilityKey]),page=available&&stored?.queryKey===queryKey?stored:null;
 const restoreScroll=useRef<number|null>(workspace?.listScroll??0);
 const [viewport,setViewport]=useState({top:0,height:400});
 const request=useRef<AbortController|null>(null),sequence=useRef(0),list=useRef<HTMLDivElement>(null),sentinel=useRef<HTMLDivElement>(null);
 const change=(patch:Partial<ListOptions>)=>{const next={...options,...patch};setOptions(next);onWorkspace?.({list:next,listScroll:0});setCursor('');};
 useEffect(()=>{setCursor('');setPage(null);},[identity,visibilityKey]);
 useEffect(()=>{if(focusSearch)input.current?.focus();},[focusSearch]);
 const key=JSON.stringify([identity,options,cursor,retry,visibilityKey]);
 // Abort old pages immediately; only the current query may publish rows.
 useEffect(()=>{
  request.current?.abort();const controller=new AbortController();request.current=controller;const seq=++sequence.current;
  // An unscanned scope is an expected empty state, not a failed native query.
  if(!available){setPage(null);setLoading(false);setError(false);setErrorCode('');setCursor('');return()=>controller.abort();}
  if(!cursor){setPage(null);if(list.current)list.current.scrollTop=0;setViewport(v=>({...v,top:0}));}setLoading(true);setError(false);
  const timer=setTimeout(()=>{void (async()=>{
   try{
    let result:FileListPage;
    if(load)result=await load(options,cursor,controller.signal);
    else{
     // Local preview filtering is limited to its explicit fixture; native data always uses the server.
     const collect=(n:StorageNode,prefix=''):StorageNode[]=>(n.children??[]).flatMap(c=>[{...c,path:prefix+c.name},...(options.recursive?collect(c,prefix+c.name+'/'):[])]);
     const pattern=options.text.toLocaleLowerCase();const escaped=pattern.split('*').map(s=>s.replace(/[.*+?^${}()|[\]\\]/g,'\\$&'));const regex=pattern.includes('*')?new RegExp('^'+escaped.join('.*')+'$','i'):null;
     const rows=collect(root).filter(n=>(options.kind==='all'||(options.kind==='file')===(n.kind==='file'))&&(!options.category||n.category===options.category)&&(!pattern||(regex?regex.test(n.name)||regex.test(n.path??''):((n.name+' '+n.path).toLocaleLowerCase().includes(pattern)))));
     rows.sort((a,b)=>{if(options.foldersFirst&&(a.kind==='file')!==(b.kind==='file'))return a.kind==='file'?1:-1;const v=options.sort==='size'?sizeOf(a)-sizeOf(b):(options.sort==='name'?a.name:a.category??a.kind).localeCompare(options.sort==='name'?b.name:b.category??b.kind);return options.direction==='desc'?-v:v;});
     const start=Number(cursor)||0;result={items:rows.slice(start,start+50),nextCursor:rows.length>start+50?String(start+50):null};
    }
    // Keep the server cursor even for an empty visible page; the sentinel continues loading.
    result={...result,items:result.items.filter(n=>categoryVisible(n,workspace,root.path??root.name))};
    if(result.nextCursor&&result.nextCursor===cursor)throw Error('INVALID_RESPONSE');
    if(!controller.signal.aborted&&seq===sequence.current)setPage(old=>{
     // Preserve loaded rows and remove overlapping identities when appending a cursor batch.
     if(!cursor||!old||old.queryKey!==queryKey)return {...result,queryKey};
     const known=new Set(old.items.map(n=>n.id));return {queryKey,items:[...old.items,...result.items.filter(n=>!known.has(n.id))],nextCursor:result.nextCursor};
    });
   }catch(e){if(!controller.signal.aborted&&seq===sequence.current){setError(true);setErrorCode(e instanceof Error&&/^[A-Z_]+$/.test(e.message)?e.message:'LIST_UNAVAILABLE');}}finally{if(!controller.signal.aborted&&seq===sequence.current)setLoading(false);}
  })();},options.text?220:0);
  return()=>{clearTimeout(timer);controller.abort();};
 },[key,load,available]);
 // Load ahead within the scrolling list, including when a short batch leaves it unfilled.
 useEffect(()=>{
  if(loading||error||!page?.nextCursor||!sentinel.current||!list.current)return;
  const observer=new IntersectionObserver(entries=>{if(entries.some(e=>e.isIntersecting))setCursor(page.nextCursor!);},{root:list.current,rootMargin:'160px 0px'});
  observer.observe(sentinel.current);return()=>observer.disconnect();
 },[page,loading,error]);
 const filtered=!!options.text||options.kind!=='all'||!!options.category;
 const category=(node:StorageNode)=>node.kind!=='file'?(de?'Ordner':'Folder'):(categoryLabels[node.category??'otherUnknown']??categoryLabels.otherUnknown)[de?0:1];
 // Unknown sizes retain an explanatory tooltip instead of claiming the folder is empty.
 // Only visible rows enter the DOM; loaded metadata retains stable cursor order.
 const rowHeight=options.recursive?50:38,rows=page?.items??[];
 const start=Math.max(0,Math.floor(viewport.top/rowHeight)-6),end=Math.min(rows.length,Math.ceil((viewport.top+viewport.height)/rowHeight)+6);
 useEffect(()=>{
  if(!list.current)return;const observer=new ResizeObserver(([e])=>setViewport(v=>({...v,height:e.contentRect.height})));
  observer.observe(list.current);return()=>observer.disconnect();
 },[]);
 // Fetch sequential pages until the saved viewport exists; no offset can skip unobserved rows.
 useEffect(()=>{
  const target=restoreScroll.current;if(target===null||!list.current||!page||loading||error)return;
  if(rows.length*rowHeight<target+viewport.height&&page.nextCursor){setCursor(page.nextCursor);return;}
  list.current.scrollTop=target;setViewport(v=>({...v,top:list.current?.scrollTop??0}));restoreScroll.current=null;
 },[page,loading,error,rows.length,rowHeight]);
 const selectedIndex=selected?rows.findIndex(n=>n.id===selected.id):-1;
 useEffect(()=>{
  if(!selected||!list.current)return;const index=selectedIndex;
  if(index>=0){const top=index*rowHeight;if(top<list.current.scrollTop||top+rowHeight>list.current.scrollTop+list.current.clientHeight)list.current.scrollTop=top;}
 },[selected?.id,selectedIndex,rowHeight]);
 return <>
  <div className="disk-list-tools">
   {/* Keep the disclosure beside search; advanced scope belongs inside it. */}
   <div className="disk-list-search-row"><div className="disk-search"><MagnifyingGlassIcon/><input ref={input} aria-label={de?'Name, Pfad oder Dateiendung suchen':'Search name, path or extension'} placeholder={de?'Name, Pfad oder *.zip suchen':'Search name, path or *.zip'} maxLength={512} value={options.text} onChange={e=>change({text:e.target.value})}/></div><button className="disk-icon-button" aria-label={de?'Filtern und Sortieren':'Filter and sort'} aria-expanded={expanded} aria-controls="disk-inline-filters" onClick={()=>{setExpanded(!expanded);onWorkspace?.({filterOpen:!expanded});}}><MixerHorizontalIcon/>{filtered&&<i className="disk-filter-active"/>}</button></div>
   <div className="disk-inline-filters" data-open={expanded} id="disk-inline-filters" inert={!expanded} aria-hidden={!expanded}><div><section>
    <h3>{de?'Filtern & Sortieren':'Filter & sort'}</h3>
    <label><span>{de?'Unterordner einbeziehen':'Include subfolders'}</span><input type="checkbox" checked={options.recursive} onChange={e=>change({recursive:e.target.checked})}/></label>
    <label>{de?'Anzeigen':'Show'}<select aria-label={de?'Anzeigen':'Show'} value={options.kind} onChange={e=>change({kind:e.target.value as ListOptions['kind']})}><option value="all">{de?'Alle':'All'}</option><option value="file">{de?'Dateien':'Files'}</option><option value="directory">{de?'Ordner':'Folders'}</option></select></label>
    <label>{de?'Art':'Type'}<select aria-label={de?'Art':'Type'} value={options.category} onChange={e=>change({category:e.target.value})}><option value="">{de?'Alle Arten':'All types'}</option>{Object.entries(categoryLabels).map(([id,n])=><option key={id} value={id}>{n[de?0:1]}</option>)}</select></label>
    <label>{de?'Sortieren nach':'Sort by'}<select aria-label={de?'Sortieren nach':'Sort by'} value={options.sort} onChange={e=>change({sort:e.target.value as ListOptions['sort']})}><option value="size">{de?'Größe':'Size'}</option><option value="name">Name</option><option value="type">{de?'Art':'Type'}</option></select></label>
    <label>{de?'Reihenfolge':'Order'}<select aria-label={de?'Reihenfolge':'Order'} value={options.direction} onChange={e=>change({direction:e.target.value as ListOptions['direction']})}><option value="asc">{de?'Aufsteigend':'Ascending'}</option><option value="desc">{de?'Absteigend':'Descending'}</option></select></label>
    <label><span>{de?'Ordner zuerst':'Folders first'}</span><input type="checkbox" checked={options.foldersFirst} onChange={e=>change({foldersFirst:e.target.checked})}/></label>
    {filtered&&<button className="disk-more" onClick={()=>change({text:'',kind:'all',category:''})}>{de?'Filter zurücksetzen':'Reset filters'}</button>}
   </section></div></div>
  </div>
  <div className="disk-list-columns"><span>Name</span><span>{de?'Art':'Type'}</span><span title={de?'Gesamte indexierte Größe einschließlich enthaltener Papierkörbe':'Total indexed size including contained recycle bins'}>{de?'Größe gesamt':'Total size'}</span></div>
   {/* A chart selection remains explicit even when outside the loaded rows or filter. */}
   {selected&&categoryVisible(selected,workspace,root.path??root.name)&&!page?.items.some(n=>n.id===selected.id)&&<div className="disk-selection-note"><strong>{de?'In der Karte ausgewählt':'Selected in map'}</strong><span>{selected.name} · {formatNodeSize(selected,language)}</span><small>{selected.path??(de?'Pfad wird geladen …':'Loading path …')}</small><small>{de?'Noch nicht geladen oder außerhalb des Filters':'Not loaded yet or outside the filter'}</small></div>}
  <div ref={list} className="disk-folder-list" data-loaded-count={rows.length} onScroll={e=>{const top=e.currentTarget.scrollTop;setViewport(v=>({...v,top}));if(restoreScroll.current===null)onWorkspace?.({listScroll:Math.round(top)});}} aria-label={de?'Dateiliste':'File list'} aria-busy={loading}>
   <div style={{height:start*rowHeight}} aria-hidden="true"/>
   {rows.slice(start,end).map(node=><button style={{height:rowHeight,minHeight:rowHeight,boxSizing:'border-box'}} className={'disk-row'+(selected?.id===node.id?' is-selected':'')} key={node.id} title={node.path??node.name} onClick={()=>onSelect(node)} onPointerEnter={()=>onHover(node.id)} onPointerLeave={()=>onHover(null)} onContextMenu={e=>{e.preventDefault();onContext(node,e.clientX,e.clientY);}}><StorageIcon kind={node.kind==='file'?'file':'folder'}/><span className="disk-folder-name">{node.name}{options.recursive&&node.path&&<small>{node.path}</small>}</span><span className="disk-file-category">{category(node)}</span><span className="disk-folder-size" title={node.unavailable&&node.bytesExact===undefined?(de?'Größe nicht erfasst; Bereich möglicherweise nicht lesbar oder vom Scan ausgeschlossen.':'Size not recorded; area may be unreadable or excluded from the scan.'):undefined}>{formatNodeSize(node,language)}</span>{node.kind!=='file'&&<ChevronRightIcon className="disk-folder-chevron"/>}</button>)}
   <div style={{height:Math.max(0,rows.length-end)*rowHeight}} aria-hidden="true"/>
   <div ref={sentinel} className="disk-list-sentinel" aria-hidden="true"/>
   {available&&loading&&<p className="disk-no-results" role="status">{de?'Wird geladen …':'Loading …'}</p>}
   {available&&error&&<button data-error={errorCode} className="disk-more" onClick={()=>setRetry(v=>v+1)}>{de?'Liste konnte nicht geladen werden. Filter eingrenzen oder erneut versuchen.':'Could not load list. Narrow filters or retry.'}</button>}
   {!available&&<p className="disk-no-results" role="status">{de?'Noch kein Scan vorhanden. Wähle einen Bereich und starte einen Scan.':'No scan yet. Choose an area and start a scan.'}</p>}
   {available&&!loading&&!error&&page?.items.length===0&&!page.nextCursor&&<p className="disk-no-results">{root.unavailable?(de?'Für diesen Bereich liegt kein anzeigbarer Scanstand vor.':'No viewable scan is available for this area.'):(de?'Keine passenden Einträge':'No matching entries')}</p>}
  </div>

 </>;
}
