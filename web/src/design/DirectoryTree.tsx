import {useSizeFormatter} from './size-unit';
import {categoryVisible} from './category-visibility';
import type {PreferenceWorkspace} from '../contracts';
import {useEffect,useMemo,useRef,useState,type CSSProperties,type ReactNode} from 'react';
import {ChevronDownIcon,ChevronRightIcon} from '@radix-ui/react-icons';
import {StorageIcon} from './StorageIcon';
import {freshnessDisplay,type ScanStamp,type FreshnessPreference} from './scan-freshness';
import {type Language,type StorageNode} from './model';
export interface DirectoryPage {items:StorageNode[];nextCursor:string|null;rootId:string}
export type DirectoryLoader=(nodeId:string,cursor:string,signal:AbortSignal)=>Promise<DirectoryPage>;
interface Props {shareStatus?:(node:StorageNode)=>ReactNode;workspace?:PreferenceWorkspace;onWorkspace?:(patch:Partial<PreferenceWorkspace>)=>void;size?:(node:StorageNode)=>string|undefined;root:StorageNode;path:StorageNode[];selectedId:string;language:Language;load?:DirectoryLoader;onNavigate:(node:StorageNode)=>void;onContextMenu?:(node:StorageNode,x:number,y:number)=>void;stamp?:(node:StorageNode)=>ScanStamp;freshness?:FreshnessPreference}
// Flatten only expanded branches and mount a viewport-sized set of rows, even for huge sibling sets.
export function DirectoryTree(props:Props){
 const formatNodeSize=useSizeFormatter();
 const host=useRef<HTMLDivElement>(null),requests=useRef(new Map<string,AbortController>()),alive=useRef(true),restoring=useRef(true);
 const [children,setChildren]=useState(new Map<string,StorageNode[]>()),[errors,setErrors]=useState(new Set<string>()),[local,setLocal]=useState<string[]>([]);
 const [viewport,setViewport]=useState({top:props.workspace?.treeScroll??0,height:600});
 const expanded=props.workspace?.expanded??local,expandedKey=expanded.join('|'),de=props.language==='de',rowHeight=34;
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;for(const r of requests.current.values())r.abort();requests.current.clear();};},[]);
 // Missing size metadata stays unknown; an unscanned or unreadable tree is never labeled empty.
 // Accumulated pages share one global comparator; names render before later metadata arrives.
 async function read(node:StorageNode){
  if(requests.current.has(node.id))return;const request=new AbortController();requests.current.set(node.id,request);
  setErrors(old=>{const next=new Set(old);next.delete(node.id);return next;});
  try{
   let page=props.load?await props.load(node.id,'',request.signal):{items:node.children??[],nextCursor:null,rootId:node.id};const all=[...page.items],seen=new Set<string>();
   const publish=()=>{if(alive.current&&!request.signal.aborted)setChildren(old=>new Map(old).set(node.id,all.filter(n=>n.kind!=='file'&&!n.unavailable)));};publish();
   while(page.nextCursor&&props.load){if(seen.has(page.nextCursor))throw Error('CURSOR_CYCLE');seen.add(page.nextCursor);page=await props.load(page.rootId,page.nextCursor,request.signal);request.signal.throwIfAborted();all.push(...page.items);publish();}
   if(alive.current&&!request.signal.aborted)setChildren(old=>new Map(old).set(node.id,all.filter(n=>n.kind!=='file'&&!n.unavailable)));
  }catch{if(alive.current&&!request.signal.aborted)setErrors(old=>new Set(old).add(node.id));}
  finally{requests.current.delete(node.id);}
 }
 const rows=useMemo(()=>{
  const open=new Set([props.root.id,...expanded]),collator=new Intl.Collator(props.language,{numeric:true,sensitivity:'base'}),out:{node:StorageNode;level:number}[]=[],pending=[{node:props.root,level:0}];
  const compare=(a:StorageNode,b:StorageNode)=>{let order=0;if(props.workspace?.treeSort==='size'){const x=props.size?.(a)??a.bytesExact,y=props.size?.(b)??b.bytesExact;if(x===undefined||y===undefined){if(x!==y)return x===undefined?1:-1;}else order=BigInt(x)<BigInt(y)?-1:BigInt(x)>BigInt(y)?1:0;}if(!order)order=collator.compare(a.name,b.name)||a.id.localeCompare(b.id);return props.workspace?.treeDirection==='desc'?-order:order;};
  while(pending.length){const item=pending.pop()!;if(item.level>0&&!categoryVisible(item.node,props.workspace))continue;out.push(item);if(!open.has(item.node.id))continue;const list=[...(children.get(item.node.id)??[])];const index=props.path.findIndex(p=>p.id===item.node.id),next=index<0?undefined:props.path[index+1];if(next&&next.name!=='.'&&!list.some(n=>n.id===next.id))list.push(next);list.sort(compare);for(let i=list.length-1;i>=0;i--)pending.push({node:list[i],level:item.level+1});}
  return out;
 },[props.root,props.path,props.language,props.size,props.workspace?.treeSort,props.workspace?.treeDirection,props.workspace?.showRecycle,props.workspace?.showSystem,expandedKey,children]);
 useEffect(()=>{for(const {node} of rows)if((node.id===props.root.id||expanded.includes(node.id))&&!children.has(node.id)&&!errors.has(node.id))void read(node);},[rows,expandedKey,children,errors]);
 useEffect(()=>{const element=host.current;if(!element)return;const observer=new ResizeObserver(()=>setViewport(old=>({...old,height:element.clientHeight})));observer.observe(element);return()=>observer.disconnect();},[]);
 // Restore after asynchronous expansion has provided enough rows; user scrolling takes ownership.
 useEffect(()=>{const element=host.current;if(!element||!restoring.current)return;const target=props.workspace?.treeScroll??0;element.scrollTop=target;if(rows.length*rowHeight-element.clientHeight>=target)restoring.current=false;},[rows.length]);
 const toggle=(id:string)=>{if(errors.has(id)){const node=rows.find(r=>r.node.id===id)?.node;if(node)void read(node);return;}const next=expanded.includes(id)?expanded.filter(n=>n!==id):[...expanded,id];if(props.onWorkspace)props.onWorkspace({expanded:next});else setLocal(next);};
 const first=Math.max(0,Math.floor(viewport.top/rowHeight)-8),last=Math.min(rows.length,Math.ceil((viewport.top+viewport.height)/rowHeight)+8);
 return <div ref={host} className="disk-directory-tree" data-status={!!props.shareStatus} data-size-unit={props.workspace?.sizeUnit??'mixed'} style={{'--tree-size-width':({TB:'8ch',GB:'10ch',MB:'13ch',mixed:'10ch',hidden:'0px'}[props.workspace?.sizeUnit??'mixed'])} as CSSProperties} role="tree" aria-label={de?'Verzeichnisbaum':'Directory tree'} onWheel={()=>{restoring.current=false;}} onPointerDown={()=>{restoring.current=false;}} onScroll={event=>{const top=event.currentTarget.scrollTop;setViewport(old=>({...old,top}));if(!restoring.current)props.onWorkspace?.({treeScroll:Math.round(top)});}}>
  <div style={{height:rows.length*rowHeight,position:'relative'}}>{rows.slice(first,last).map(({node,level},offset)=>{
   const open=node.id===props.root.id||expanded.includes(node.id),stamp=props.stamp?.(node),mark=stamp&&props.freshness?freshnessDisplay(stamp,props.freshness,props.language):null,bytes=props.size?.(node)??node.bytesExact;
   // A partial scan reports a lower bound; an unreadable zero must never look like an empty folder.
   const partial=stamp?.state==='partial',sizeLabel=bytes!==undefined?formatNodeSize({...node,bytesExact:bytes},props.language):'—';
   const sizeText=partial&&sizeLabel&&bytes!==undefined?(BigInt(bytes)>0?sizeLabel:'—'):sizeLabel;
   const sizeTitle=partial?(de?'Unvollständig erfasst: Angezeigt ist nur die lesbare Datenmenge. Nicht erfasste Bereiche fehlen in der Summe.':'Incomplete scan: only readable data is counted. Unrecorded areas are missing from this total.'):bytes===undefined?(de?'Größe nicht erfasst':'Size not recorded'):undefined;
   return <div key={node.id} role="treeitem" aria-level={level+1} aria-expanded={open} aria-selected={node.id===props.selectedId} className={`disk-tree-row${node.id===props.selectedId?' is-selected':''}`} style={{position:'absolute',top:(first+offset)*rowHeight,height:rowHeight,left:0,right:0,paddingLeft:0}} onContextMenu={event=>{if(props.onContextMenu){event.preventDefault();props.onContextMenu(node,event.clientX,event.clientY);}}}>
    <span className="disk-tree-label" style={{paddingLeft:8+level*17}}><button className="disk-tree-toggle" aria-label={(errors.has(node.id)?(de?'Erneut laden: ':'Retry: '):open?(de?'Zuklappen: ':'Collapse: '):(de?'Aufklappen: ':'Expand: '))+node.name} onClick={()=>toggle(node.id)}>{errors.has(node.id)?'!':open?<ChevronDownIcon/>:<ChevronRightIcon/>}</button>
    <button className="disk-tree-name" onClick={()=>props.onNavigate(node)} title={node.name}><StorageIcon kind={level===0?'nas':node.kind==='volume'?'volume':'folder'}/><span>{node.name}</span></button></span>
    {/* Share indicators are siblings of the navigation button and never appear on folders. */}
    <span className="disk-tree-status-cell">{node.kind==='share'&&props.shareStatus?.(node)}</span>
    <span className="disk-tree-bytes" data-partial={partial||undefined} title={sizeTitle} aria-label={partial?sizeTitle:undefined}>{sizeText}</span>
    <span className="disk-tree-age" title={mark?.title}>{mark&&<i style={{background:mark.color}} aria-label={mark.title}/>}</span>
   </div>;
  })}</div>
 </div>;
}
