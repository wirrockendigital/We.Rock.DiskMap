import {useCallback,useEffect,useMemo,useRef,useState} from 'react';
import type {Scope} from '../contracts';
import {recycleStateForScope} from './recycle-state';
import {loadScopeFolders} from '../scope-api';
import {SessionDenied} from '../session';
import type {useNativeDiskMap,NativeStorageNode} from './useNativeDiskMap';
import type {StorageNode} from './model';
import type {DirectoryPage} from './DirectoryTree';
import {ScopeObservationQueue,type ScopeObservation} from './scope-observations';
import {visibleCoverage} from './visible-coverage';
import {scopeStamp,type ScanStamp} from './scan-freshness';

// Live navigation identities are distinct from recorded node IDs and cannot become file-action targets.
const sourceId=(s:Scope)=>'source:'+s.kind+':'+s.targetIds.join(',')+':'+(s.relativePath??'.');
export function useSourceNavigation(api:ReturnType<typeof useNativeDiskMap>,denied:()=>void){
 const latest=useRef(api);latest.current=api;const deny=useRef(denied);deny.current=denied;
 const [observations,setObservations]=useState(new Map<string,ScopeObservation>());
 // Each scan refresh owns an epoch; old folder reads cannot repopulate its metadata.
 const observationQueue=useRef(new ScopeObservationQueue(undefined,(...args)=>latest.current.debugTiming?.(...args)));
 const observationEpoch=useRef(0),observationRequest=useRef(0),observationVersions=useRef(new Map<string,{epoch:number;request:number}>());
 const observe=useCallback(async(scopes:{shareId:string;path:string}[],signal:AbortSignal,epoch=observationEpoch.current)=>{
  if(epoch!==observationEpoch.current)return;
  const request=++observationRequest.current;
  // Commit the complete batch set together so parent totals never describe a partial response.
  const rows=await observationQueue.current.read(scopes,signal);
  signal.throwIfAborted();if(epoch!==observationEpoch.current)return;
  setObservations(old=>{
   if(epoch!==observationEpoch.current)return old;
   const next=new Map(old);
   for(const row of rows){
    const key=row.shareId+':'+row.path,previous=old.get(key),version=observationVersions.current.get(key);
    // Capture time wins over response order; equal observations use the newer request.
    if(version?.epoch===epoch){
     const before=Date.parse(previous?.endedAt??''),after=Date.parse(row.endedAt??'');
     if(Number.isFinite(before)&&Number.isFinite(after)?after<before||after===before&&request<version.request:request<version.request)continue;
    }
    next.set(key,row);observationVersions.current.set(key,{epoch,request});
   }
   return next;
  });
 },[]);
 const nodes=useRef(new Map<string,NativeStorageNode>()),sequence=useRef(0),alive=useRef(true);
 const [selection,setSelection]=useState<NativeStorageNode|null>(null),[missing,setMissing]=useState(false),[loading,setLoading]=useState(false),[error,setError]=useState(''),[observationError,setObservationError]=useState(false),[refreshKey,setRefreshKey]=useState(0);
 const create=(name:string,scope:Scope,kind:StorageNode['kind'],sourceKind:string,unavailable=false):NativeStorageNode=>{
  const n:NativeStorageNode={id:sourceId(scope),name,kind,sourceKind,scope,unavailable};nodes.current.set(n.id,n);return n;
 };
 const volumes=useMemo(()=>[...(api.sources?.volumes??[])].sort((a,b)=>a.name.localeCompare(b.name,undefined,{numeric:true})).map(v=>{
  const n=create(v.name.replace(/^volume(\d+)$/i,'Volume $1'),{kind:'volumes',targetIds:[v.id]},'volume','volume',v.state!=='reachable');
  n.children=(api.sources?.shares??[]).filter(s=>s.volumeId===v.id&&s.readable).map(s=>create(s.name,{kind:'shares',targetIds:[s.id]},'share','share'));return n;
 }),[api.sources]);
 // Refresh saved root metadata when a scan changes, never on ordinary map navigation.
 const summaryKey=api.summaries.map(s=>s.profileId+':'+s.endedAt+':'+s.lastAttempt?.endedAt).join('|');
 useEffect(()=>{
  const controller=new AbortController(),epoch=++observationEpoch.current;
  observationQueue.current.close();observationQueue.current=new ScopeObservationQueue(undefined,(...args)=>latest.current.debugTiming?.(...args));
  const wanted=new Map<string,{shareId:string;path:string}>();
  for(const s of api.sources?.shares??[])if(s.readable)wanted.set(s.id+':.',{shareId:s.id,path:'.'});
  for(const n of nodes.current.values()){const s=n.scope;if(s&&['folder','shares'].includes(s.kind)&&api.sources?.shares.some(share=>share.id===s.targetIds[0]&&share.readable))wanted.set(s.targetIds[0]+':'+(s.relativePath??'.'),{shareId:s.targetIds[0],path:s.relativePath??'.'});}
  const scopes=[...wanted.values()],roots=scopes.filter(s=>s.path==='.'),folders=scopes.filter(s=>s.path!=='.');
  setObservationError(false);
  // Retain the last qualified values while a transient read fails, then recover without UI actions.
  // Root totals recover first; previously expanded folders cannot delay the entire NAS total.
  const refresh=async()=>{try{if(roots.length)await observe(roots,controller.signal,epoch);for(let i=0;i<folders.length;i+=32)await observe(folders.slice(i,i+32),controller.signal,epoch);}catch(e){if(controller.signal.aborted)return;if(e instanceof SessionDenied)deny.current();else setObservationError(true);}};
  if(scopes.length)void refresh();else setObservations(new Map());
  return()=>{controller.abort();observationQueue.current.close();observationEpoch.current++;};
 },[api.sources,summaryKey,observe,refreshKey]);
 const root=useMemo(()=>{const node=create('NAS',{kind:'nas',targetIds:[]},'volume','nas');node.children=volumes;return node;},[volumes]);
 // Reconstruct only qualified inventory paths; no name or scan result survives a session unmount.
 const pathFor=(node:NativeStorageNode):NativeStorageNode[]=>{
  if(node.scope?.kind==='nas')return[root];const scope=node.scope;if(!scope)return[root];
  const share=api.sources?.shares.find(s=>s.id===scope.targetIds[0]),volume=volumes.find(v=>v.scope!.targetIds[0]===(scope.kind==='volumes'?scope.targetIds[0]:share?.volumeId));
  if(!volume)return[root];if(scope.kind==='volumes')return[root,volume];
  const base=volume.children?.find(n=>(n as NativeStorageNode).scope!.targetIds[0]===share?.id) as NativeStorageNode|undefined;if(!base)return[root,volume];
  const path=[root,volume,base];let relative='';
  if(scope.kind==='folder'&&scope.relativePath!=='.')for(const name of (scope.relativePath??'').split('/')){relative=relative?relative+'/'+name:name;path.push(create(name,{kind:'folder',targetIds:[share!.id],relativePath:relative},'folder','directory'));}
  return path;
 };
 const infer=():NativeStorageNode=>{
  const path=api.path,shareIndex=path.findIndex(n=>n.sourceKind==='share'),share=shareIndex>=0?api.sources?.shares.find(s=>s.id===path[shareIndex].shareId||s.name===path[shareIndex].name):undefined;
  if(share){const tail=path.slice(shareIndex+1).map(n=>n.name).filter(n=>n!=='.');return create(tail.at(-1)??share.name,tail.length?{kind:'folder',targetIds:[share.id],relativePath:tail.join('/')}:{kind:'shares',targetIds:[share.id]},tail.length?'folder':'share',tail.length?'directory':'share');}
  const volume=path.find(n=>n.sourceKind==='volume');return volumes.find(v=>v.name===volume?.name||v.scope!.targetIds[0]===volume?.volumeId)??root;
 };
 const selected=selection??infer(),path=useMemo(()=>pathFor(selected),[selected.id,api.path,volumes]);
 const read=useCallback(async(nodeId:string,cursor:string,signal:AbortSignal):Promise<DirectoryPage>=>{
  const epoch=observationEpoch.current,node=nodes.current.get(nodeId),scope=node?.scope;if(!node||!scope)throw Error('SCOPE_UNAVAILABLE');
  if(scope.kind==='nas'||scope.kind==='volumes')return {rootId:nodeId,items:node.children??[],nextCursor:null};
  try{const page=await loadScopeFolders(scope.targetIds[0],scope.relativePath??'.',cursor||null,signal);signal.throwIfAborted();
   const items=page.entries.map(entry=>{const s:Scope={kind:'folder',targetIds:[scope.targetIds[0]],relativePath:entry.relativePath};const n:NativeStorageNode={id:sourceId(s),name:entry.name,kind:'folder',sourceKind:'directory',scope:s};nodes.current.set(n.id,n);return n;});
   // Child metadata persists beyond the selected map, avoiding disappearing sibling sizes.
   // Render folder names independently; missing metadata must not block navigation.
   void observe([...(!cursor?[{shareId:scope.targetIds[0],path:scope.relativePath??'.'}]:[]),...page.entries.map(e=>({shareId:scope.targetIds[0],path:e.relativePath}))],signal,epoch).catch(e=>{if(!signal.aborted&&epoch===observationEpoch.current){if(e instanceof SessionDenied)deny.current();else setObservationError(true);}});
   return {rootId:nodeId,items,nextCursor:page.nextCursor};
  }catch(e){if(!signal.aborted&&e instanceof SessionDenied)deny.current();throw e;}
 },[]);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;sequence.current++;nodes.current.clear();};},[]);
 const navigate=useCallback(async(node:StorageNode)=>{
  const source=nodes.current.get(node.id);if(!source?.scope||source.unavailable)return;
  latest.current.setWorkspace({source:source.scope});const seq=++sequence.current;setSelection(source);setMissing(true);setLoading(true);setError('');
  try{const found=await latest.current.openScope(source.scope);if(alive.current&&seq===sequence.current)setMissing(!found);}
  catch(e){if(alive.current&&seq===sequence.current){if(e instanceof SessionDenied)deny.current();else setError('Der gespeicherte Bereich konnte nicht geladen werden.');}}
  finally{if(alive.current&&seq===sequence.current)setLoading(false);}
 },[]);
 // Restore once after inventory and profile metadata are ready, including unscanned folders.
 const restored=useRef(false);
 useEffect(()=>{if(restored.current||!api.layoutReady||!api.sources||!api.summariesReady||api.loading)return;restored.current=true;const s=api.workspace.source;if(!s)return;
  const target=s.kind==='nas'?root:s.kind==='volumes'?volumes.find(v=>v.scope?.targetIds[0]===s.targetIds[0]):api.sources.shares.find(v=>v.id===s.targetIds[0]&&v.readable);
  if(!target)return;const name=s.kind==='folder'?s.relativePath!.split('/').at(-1)!:target.name;
  // Preserve canonical inventory children: a childless restore node would erase sibling shares on the next tree reload.
  const node=s.kind==='nas'?root:s.kind==='volumes'?volumes.find(v=>v.scope!.targetIds[0]===s.targetIds[0])!:create(name,s,s.kind==='shares'?'share':'folder',s.kind);void navigate(node);
 },[api.layoutReady,api.sources,api.summariesReady,api.loading]);
 const clear=()=>{latest.current.setWorkspace({source:null});sequence.current++;setSelection(null);setMissing(false);setLoading(false);setError('');};
 const observation=useCallback((node:StorageNode)=>{const scope=(node as NativeStorageNode).scope??nodes.current.get(node.id)?.scope;const known=scope&&['shares','folder'].includes(scope.kind)?observations.get(scope.targetIds[0]+':'+(scope.relativePath??'.')):undefined;return known?visibleCoverage(known,api.workspace):undefined;},[observations,api.workspace.showRecycle,api.workspace.showSystem]);
 // Use unfiltered scan metadata so hiding a category cannot hide its recorded access failure.
 const categoryAccess=useMemo(()=>{
  const scope=selected.scope;if(!scope)return {recycle:false,system:false};
  const rows=['shares','folder'].includes(scope.kind)?[observations.get(scope.targetIds[0]+':'+(scope.relativePath??'.'))]:(api.sources?.shares??[]).filter(s=>scope.kind==='nas'||scope.targetIds.includes(s.volumeId??'')).map(s=>observations.get(s.id+':.'));
  return {recycle:rows.some(r=>r?.unreadableRecycle),system:rows.some(r=>r?.unreadableSystem)};
 },[observations,selected.id,api.sources]);
 const stamp=(node:StorageNode):ScanStamp=>{const known=observation(node);if(known)return {state:known.state,endedAt:known.endedAt};const scope=(node as NativeStorageNode).scope??nodes.current.get(node.id)?.scope;return scope&&api.sources?scopeStamp(scope,api.profiles,api.summaries,api.sources,api.summariesReady):{state:'unknown',endedAt:null};};
 // An opened reader is stronger evidence than profile-scope inference for the selected folder.
 let selectedStamp=stamp(selected);const view=api.analysis.reader?.view;
 if(!observation(selected)&&!missing&&!loading&&view?.snapshotId&&view.endedAt){selectedStamp={endedAt:view.endedAt,state:view.state==='partial'?'partial':'complete'};
  if(selected.scope?.kind==='nas'&&api.profiles.find(p=>p.profile.id===api.profileId)?.profile.scope.kind!=='nas')selectedStamp={state:'partial',endedAt:null};
 }
 const marked=(node:StorageNode)=>node.id===selected.id?selectedStamp:stamp(node);
 // Latest recorded source metadata owns tree sizes; an older displayed map is never a fallback.
 const size=useCallback((node:StorageNode)=>{
  const known=observation(node);if(known)return known.bytes??undefined;
  const scope=(node as NativeStorageNode).scope;
  if(scope&&['nas','volumes'].includes(scope.kind)){
   const shares=(api.sources?.shares??[]).filter(s=>s.readable&&(scope.kind==='nas'||s.volumeId!==null&&scope.targetIds.includes(s.volumeId)));
   const values=shares.map(s=>observations.get(s.id+':.'));
   // Every member must have a measured value before the aggregate can be displayed.
   if(values.length&&values.every(v=>v?.bytes!==null&&v?.bytes!==undefined))return values.reduce((sum,v)=>sum+BigInt(v!.bytes!),0n).toString();
  }
  return undefined;
 },[observation,observations,api.sources]);
 // Rebuild cached root rows only when inventory membership changes, preserving saved expansion.
 const inventoryKey=JSON.stringify([api.sources?.volumes.map(v=>[v.id,v.state]),api.sources?.shares.map(s=>[s.id,s.volumeId,s.readable])]);
 return {recycleState:recycleStateForScope(selected.scope,api.sources),categoryAccess,revision:summaryKey+inventoryKey,observation,size,root,path,selected,read,navigate,clear,missing,loading,error,stamp:marked,selectedStamp,observationError,refreshObservations:()=>setRefreshKey(k=>k+1),
  rescan:(node:StorageNode)=>{const n=nodes.current.get(node.id);if(n?.scope&&!n.unavailable){if(!latest.current.status?.active&&!latest.current.status?.queued.length)clear();void latest.current.scanScope(n.scope,n.name,true);}},
 };
}
