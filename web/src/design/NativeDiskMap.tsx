import {CoverageDialog} from './CoverageDialog';
import {IndexResetDialog} from './IndexResetDialog';
import type {Scope} from '../contracts';
import {useCallback,useEffect,useRef,useState} from 'react';
import {DiskMapWorkspace,type LiveWorkspace} from './DiskMapWorkspace';
import {useNativeDiskMap,type NativeStorageNode} from './useNativeDiskMap';
import {type Language,type StorageNode} from './model';
import {type AnalysisRenderBudget} from '../analysis-query';
import {loadScopeFolders,resolveScopeFileStation} from '../scope-api';
import {showInFileStation,deleteSingleFile,readDeleteStatus,DSMFileActionFailure,type VerifiedFileTarget} from '../dsmFileActions';
import {explainError} from './diagnostics';
import {cachedResultLabel} from './scan-cache';
import {useSourceNavigation} from './useSourceNavigation';
import {freshnessSeconds} from './scan-freshness';
import type {ListOptions} from './list-options';
import {SessionDenied} from '../session';

export interface NativeDiskMapProps {onSessionDenied:()=>void;sessionIdentity:string}
type Confirmation={nodeId:string;name:string;path:string;key:string};
// A keyed session subtree prevents old callbacks from borrowing a newly authenticated owner.
export function NativeDiskMap(props:NativeDiskMapProps){return <NativeWorkspace key={props.sessionIdentity} {...props}/>;}
function NativeWorkspace({sessionIdentity,onSessionDenied}:NativeDiskMapProps){
 const [budget,setBudget]=useState<AnalysisRenderBudget|null>(null),[language,setLanguage]=useState<Language>('de');
 const api=useNativeDiskMap({sessionIdentity,onSessionDenied,budget});
 const source=useSourceNavigation(api,onSessionDenied),currentSource=useRef(source);currentSource.current=source;
 const [message,setMessage]=useState(''),[working,setWorking]=useState(false),[confirmation,setConfirmation]=useState<Confirmation|null>(null);
 const dialog=useRef<HTMLDialogElement>(null),operation=useRef<AbortController|null>(null),alive=useRef(true),keyRef=useRef('');
 const key=JSON.stringify([sessionIdentity,api.profileId,api.analysis.pin?.viewId,api.analysis.pin?.snapshotId,api.analysis.state.epoch]);keyRef.current=key;
 const [coverageOpen,setCoverageOpen]=useState(false);
 const [rebuildTarget,setRebuildTarget]=useState<{name:string;run:()=>void}|null>(null);
 const [resetTarget,setResetTarget]=useState<{scope:Scope|null;name:string}|null>(null);
 const de=language==='de';
 const [hoverPath,setHoverPath]=useState('');
 // Hover paths are scoped to the current authenticated view and bounded independently.
 const hoverCache=useRef(new Map<string,string>());
 const hover=useRef<{timer:ReturnType<typeof setTimeout>|null;controller:AbortController|null}>({timer:null,controller:null});
 const currentApi=useRef(api);currentApi.current=api;
 const searchText=useRef('');const languageRef=useRef(language);languageRef.current=language;
 const deniedRef=useRef(onSessionDenied);deniedRef.current=onSessionDenied;
 // Hover requests have their own cancellation owner and never mutate the confirmed view.
 const onHover=useCallback((nodeId:string|null)=>{
  if(hover.current.timer)clearTimeout(hover.current.timer);hover.current.controller?.abort();setHoverPath('');
  if(!nodeId)return;const cached=hoverCache.current.get(nodeId);if(cached!==undefined){setHoverPath(cached);return;}const identity=keyRef.current;const controller=new AbortController();hover.current.controller=controller;
  hover.current.timer=setTimeout(()=>{void currentApi.current.recordedPaths(nodeId,AbortSignal.any([controller.signal,AbortSignal.timeout(8000)])).then(paths=>{
   if(alive.current&&!controller.signal.aborted&&keyRef.current===identity){const text=paths.nasPath.text+(paths.nasPath.encoding==='escaped-bytes'?' [escaped-bytes]':'');hoverCache.current.set(nodeId,text);if(hoverCache.current.size>256)hoverCache.current.delete(hoverCache.current.keys().next().value!);setHoverPath(text);}
  }).catch(error=>{if(alive.current&&!controller.signal.aborted&&keyRef.current===identity&&error instanceof SessionDenied)onSessionDenied();});},180);
 },[onSessionDenied]);
 // Selection paths use the existing qualified reader and have their own cancellation signal.
 const readSelectedPath=useCallback(async(nodeId:string,signal:AbortSignal)=>{
  try{const paths=await currentApi.current.recordedPaths(nodeId,signal);return paths.nasPath.text+(paths.nasPath.encoding==='escaped-bytes'?' [escaped-bytes]':'');}
  catch(error){if(!signal.aborted&&error instanceof SessionDenied)deniedRef.current();throw error;}
 },[]);
 // Snapshot qualification happens inside the owner; keep the callback stable across map responses.
 const readChildren=useCallback((nodeId:string,cursor:string,signal:AbortSignal)=>nodeId.startsWith('source:')?currentSource.current.read(nodeId,cursor,signal):currentApi.current.readChildren(nodeId,cursor,signal),[]);
 const readFileList=useCallback((options:ListOptions,cursor:string,signal:AbortSignal)=>currentSource.current.missing||currentSource.current.loading?Promise.resolve({items:[],nextCursor:null}):currentApi.current.readFileList(options,cursor,signal),[source.missing,source.loading]);
 const onMap=useCallback((map:'radial'|'treemap')=>{void currentApi.current.setMap(map);},[]);
 const search=useCallback((text:string)=>{searchText.current=text;void currentApi.current.search(text);},[]);
 const nextPage=useCallback(()=>{const a=currentApi.current;if(a.searchPage?.hasMore)void a.search(searchText.current,a.searchPage.nextCursor);else void a.nextGroup();},[]);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;operation.current?.abort();hover.current.controller?.abort();if(hover.current.timer)clearTimeout(hover.current.timer);};},[]);
 // Any change of qualified view invalidates pending targets and late responses.
 useEffect(()=>{operation.current?.abort();setWorking(false);setConfirmation(null);setMessage('');hoverCache.current.clear();onHover(null);},[key,onHover]);
 useEffect(()=>{if(confirmation&&!dialog.current?.open)dialog.current?.showModal();else if(!confirmation&&dialog.current?.open)dialog.current.close();},[confirmation]);
 function begin(){operation.current?.abort();const controller=new AbortController();operation.current=controller;setWorking(true);return controller;}
 function owns(controller:AbortController,identity=key){return alive.current&&!controller.signal.aborted&&operation.current===controller&&keyRef.current===identity;}
 function failed(error:unknown){
  if(error instanceof SessionDenied){onSessionDenied();return;}
  setMessage(error instanceof DSMFileActionFailure&&error.code==='OUTCOME_UNKNOWN'
   ?(de?'Löschergebnis unbekannt. Nicht erneut löschen; prüfe die Datei in File Station.':'Deletion outcome unknown. Do not retry; inspect the file in File Station.')
   :(de?'Die Aktion konnte nicht bestätigt werden.':'The action could not be confirmed.'));
 }
 async function reveal(node:StorageNode){
  if(working||api.busy)return;const controller=begin();
  try{const target=await api.actionTarget(node.id,controller.signal);if(!owns(controller))return;
   if(target.noticeCode==='FILE_REMOVED_PARENT_AVAILABLE'){setMessage(de?'Die Datei fehlt. Du kannst den Elternordner in File Station prüfen.':'The file is missing. You can inspect its parent folder in File Station.');return;}
   await showInFileStation(target,{signal:controller.signal,dsmBuild:api.dsmBuild??''});if(!owns(controller))return;
   // Successful opening needs no duplicate notification.
   setMessage('');
  }catch(error){if(owns(controller))failed(error);}finally{if(owns(controller))setWorking(false);}
 }
 // Tree nodes are live inventory identities, never IDs from the currently selected scan.
 async function revealTree(node:StorageNode){
  const scope=(node as NativeStorageNode).scope;
  if(!scope||working||api.busy)return;const controller=begin();
  try{const target=await resolveScopeFileStation(scope,controller.signal);if(!owns(controller))return;
   await showInFileStation(target,{signal:controller.signal,dsmBuild:api.dsmBuild??''});
  }catch(error){if(owns(controller))failed(error);}finally{if(owns(controller))setWorking(false);}
 }
 async function askDelete(node:StorageNode){
  if(working||api.busy||node.kind!=='file')return;const controller=begin();
  try{const target=await api.actionTarget(node.id,controller.signal);if(!owns(controller))return;
   if(target.kind!=='file'||target.noticeCode)throw Error('TARGET_CHANGED');
   setConfirmation({nodeId:node.id,name:node.name,path:target.fileStationPath,key});
  }catch(error){if(owns(controller))failed(error);}finally{if(owns(controller))setWorking(false);}
 }
 async function confirmDelete(){
  const selected=confirmation;if(!selected||selected.key!==key||working)return;const controller=begin();let sent=false;
  try{
   const target=await api.actionTarget(selected.nodeId,controller.signal);if(!owns(controller))return;
   if(target.kind!=='file'||target.noticeCode||target.fileStationPath!==selected.path)throw Error('TARGET_CHANGED');
   const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(30000)]);
   sent=true;
   const started=await deleteSingleFile(target as VerifiedFileTarget,{signal,dsmBuild:api.dsmBuild??'',confirmed:true});
   // A start receipt is not completion. Poll reads only, within one absolute deadline.
   for(let count=0;count<30;count++){
    signal.throwIfAborted();if(!owns(controller))return;
    const status=await readDeleteStatus(started.taskId,{signal,dsmBuild:api.dsmBuild??''});
    signal.throwIfAborted();if(!owns(controller))return;
    if(status.finished){setConfirmation(null);setMessage(de?'Datei gelöscht. Scanne erneut, um den gespeicherten Index zu aktualisieren.':'File deleted. Scan again to update the recorded index.');return;}
    await new Promise<void>((resolve,reject)=>{const stop=()=>{clearTimeout(timer);reject(new DOMException('Aborted','AbortError'));};const timer=setTimeout(()=>{signal.removeEventListener('abort',stop);resolve();},1000);signal.addEventListener('abort',stop,{once:true});});
   }
   throw new DSMFileActionFailure('OUTCOME_UNKNOWN');
  }catch(error){if(owns(controller)){setConfirmation(null);failed(sent&&!(error instanceof DSMFileActionFailure&&error.code==='REJECTED')?new DSMFileActionFailure('OUTCOME_UNKNOWN'):error);}}
  finally{if(owns(controller))setWorking(false);}
 }
 const browse=useCallback(async(node:StorageNode):Promise<StorageNode[]>=>{
  const source=node as NativeStorageNode,scope=source.scope;
  if(!scope||!['shares','folder'].includes(scope.kind)||scope.targetIds.length!==1) return node.children??[];
  const controller=begin();const identity=keyRef.current;const de=languageRef.current==='de';
  try{
   const signal=AbortSignal.any([controller.signal,AbortSignal.timeout(30000)]);
   const entries:NativeStorageNode[]=[],cursors=new Set<string>(),paths=new Set<string>();let after:string|null=null;
   // Bound pagination across one directory; a partial listing is explicitly labelled.
   for(let page=0;page<32;page++){
    const result=await loadScopeFolders(scope.targetIds[0],scope.kind==='folder'?scope.relativePath!:'.',after,signal);
    if(!owns(controller,identity))throw new DOMException('Superseded','AbortError');
    for(const entry of result.entries){
     if(paths.has(entry.relativePath))throw Error('DUPLICATE_FOLDER');paths.add(entry.relativePath);
     if(entries.length<1000)entries.push({id:`scope:${scope.targetIds[0]}:${entry.relativePath}`,name:entry.name,kind:'folder',sourceKind:'directory',scope:{kind:'folder',targetIds:[scope.targetIds[0]],relativePath:entry.relativePath}});
    }
    if(!result.nextCursor&&paths.size<=1000)return entries;
    if(!result.nextCursor)break;
    if(cursors.has(result.nextCursor))throw Error('CURSOR_CYCLE');cursors.add(result.nextCursor);after=result.nextCursor;
    if(entries.length>=1000)break;
   }
   setMessage(de?'Die Ordnerliste ist auf 1.000 Einträge begrenzt.':'The folder list is limited to 1,000 entries.');return entries;
  }catch(error){if(owns(controller,identity)&&error instanceof SessionDenied)deniedRef.current();throw error;}
  finally{if(owns(controller,identity))setWorking(false);}
 },[]);
 const currentJob=api.status?.active??api.status?.queued[0];
 const active=api.progress?.current?.jobId===currentJob?.jobId?api.progress?.current:null;
 const scanning=!!currentJob;
 const snapshot=api.analysis.reader?.view;
 // Every recorded result is a point-in-time observation, including one just completed.
 const cacheNotice=snapshot?.snapshotId?cachedResultLabel(snapshot.endedAt,language)+(snapshot.state==='partial'?(de?' · Unvollständig':' · Incomplete'):''):'';
 // Keep transport state codes in Debug; the main view uses concise human labels.
 const scanStates:Record<string,string>=de?{queued:'Scan wartet',preflight:'Scan wird vorbereitet',paused:'Scan pausiert',aggregating:'Scan abgeschlossen – Ordnersummen werden berechnet',validating:'Scan abgeschlossen – Ergebnis wird geprüft',publishing:'Ergebnis wird gespeichert',preparing:'Scan wird vorbereitet',scanning:'Scan läuft',finalizing:'Scan wird gespeichert',completed:'Scan abgeschlossen',cancelled:'Scan abgebrochen',failed:'Scan fehlgeschlagen',interrupted:'Scan unterbrochen'}:{queued:'Scan queued',preflight:'Preparing scan',paused:'Scan paused',aggregating:'Scan complete – calculating folder totals',validating:'Scan complete – validating result',publishing:'Saving result',preparing:'Preparing scan',scanning:'Scanning',finalizing:'Saving scan',completed:'Scan complete',cancelled:'Scan cancelled',failed:'Scan failed',interrupted:'Scan interrupted'};
 // Routine progress, cache age and partial coverage already have persistent UI indicators.
 const resultNotice=api.terminalCode&&['SCAN_FAILED','SCAN_INTERRUPTED'].includes(api.terminalCode)?explainError(api.terminalCode,language):'';
 const statusText=message||(api.pending?(de?'Ergebnis einer Aktion noch ungeklärt. Bitte Status klären.':'An action outcome is unresolved. Please resolve its status.'):'');
 // Search rows retain canonical IDs; recorded paths are not live-action authority.
 const entries:StorageNode[]=searchText.current.trim()&&api.searchPage?api.searchPage.items.map(n=>({id:n.nodeId,name:n.name,kind:n.kind==='file'?'file':'folder',bytesExact:n.size??undefined,unavailable:n.size===null||!['file','directory'].includes(n.kind)})):api.entries;
 // Snapshot estimates always belong to the containing share, even while browsing its descendants.
 const snapshotShareName=api.sources?.shares.find(s=>s.id===source.selected.scope?.targetIds[0])?.name;
 const live:LiveWorkspace={
  // Show setup only after inventory is known; a failed request is never an empty NAS.
  setup:api.sources&&!api.loading&&!scanning?(api.sources.shares.some(s=>s.readable)?(!api.profiles.some(p=>p.activeSnapshotId||p.latestJobId)&&source.selected.scope?.kind==='nas'?'first':undefined):'permissions'):undefined,
  inventoryRefreshing:api.inventoryRefreshing,inventoryError:api.inventoryError,refreshInventory:()=>void api.refreshInventory(),
  // Resolve status rows by their inventory identity, never by a same-named subdirectory.
  shareStatusRevision:api.sources?.observedAt,statusShare:node=>{const n=node as NativeStorageNode;return node.kind==='share'?api.sources?.shares.find(s=>s.id===n.scope?.targetIds[0]):undefined;},
  snapshotShareName,categoryAccess:source.categoryAccess,recycleState:source.recycleState,
  storedLanguage:api.language,storedTheme:api.theme,saveLanguage:value=>{void api.setAppearance('language',value);},saveTheme:value=>{void api.setAppearance('theme',value);},
  selectedId:api.analysis.state.view?.selectedNodeId,selectFile:id=>{void api.selectFile(id);},
  queue:{active:api.status?.active?{id:api.status.active.jobId,name:api.profiles.find(p=>p.profile.id===api.status!.active!.profileId)?.profile.name??'Scan'}:null,waiting:(api.status?.queued??[]).map(j=>({id:j.jobId,name:api.profiles.find(p=>p.profile.id===j.profileId)?.profile.name??'Scan',position:j.queuePosition})),unavailable:!api.status||!!api.statusError},removeQueuedScan:id=>{void api.removeQueuedScan(id);},
  showCoverage:()=>setCoverageOpen(true),
  resetIndex:node=>{const scope=(node as NativeStorageNode|null)?.scope;if(node&&!scope)return;setResetTarget({scope:scope??null,name:node?.name??(de?'Gesamter Index':'Entire index')});},
  mapSnapshotId:api.analysis.pin?.snapshotId,mapRoot:api.mapTree??undefined,mapPreparing:api.mapPreparing,loadGeometry:api.loadGeometry,workspace:api.workspace,onWorkspace:api.setWorkspace,completion:api.completion,treeSize:source.size,treeRevision:source.revision,treeRoot:api.sources?source.root:undefined,treePath:source.path,treeSelectedId:source.selected.id,readTree:source.read,treeNavigate:node=>{if(!working&&!api.busy)void source.navigate(node);},treeRescan:node=>{if(!working&&!api.busy)setRebuildTarget({name:node.name,run:()=>source.rescan(node)});},treeStamp:source.stamp,treeReveal:revealTree,
  // The global action always targets the authenticated user’s NAS scope and forces a full scan.
  scanNas:api.sources&&!api.pending?()=>{if(!working&&!api.busy){setRebuildTarget({name:'NAS',run:()=>{if(!scanning)source.clear();void api.scanScope({kind:'nas',targetIds:[]},'NAS',true);}});}}:undefined,
  freshness:api.freshness,setFreshness:api.setFreshness,selectedStamp:source.selectedStamp,unscanned:source.missing||source.loading,
  scanPreview:scanning&&api.preview?.jobId===currentJob?.jobId?api.preview.preview??undefined:undefined,sidebars:api.sidebars,layoutReady:api.layoutReady,onSidebars:api.setSidebars,readChildren,readFileList,snapshotKey:[sessionIdentity,api.analysis.pin?.snapshotId].join(':'),groupEntries:api.groupEntries,messageScope:api.profileId??'',cacheStale:!!snapshot?.snapshotId&&(snapshot.state==='partial'||!snapshot.endedAt||!Number.isFinite(Date.parse(snapshot.endedAt))||Date.now()-Date.parse(snapshot.endedAt)>1000*freshnessSeconds(api.freshness)),scanning,cacheNotice,openPartial:api.partial&&api.analysis.pin?.snapshotId!==api.partial.snapshotId?()=>void api.openPartial():undefined,debugEnabled:api.debugEnabled,setDebug:api.setDebug,clearDebug:api.clearDebug,debugLog:api.debugEntries.join('\n'),debugLimit:api.debugLimit,debugFilter:api.debugFilter,debugBytes:api.debugBytes,debugCount:api.debugCount,setDebugLimit:api.setDebugLimit,setDebugFilter:api.setDebugFilter,exportDebug:api.exportDebug,scanProgress:scanning?{label:scanStates[currentJob.state]??(de?'Scan läuft':'Scanning'),entries:active?.measured?active.entries:null,bytes:active?.measured?active.knownLogicalBytes:null,paused:currentJob.state==='paused',phase:currentJob.state,elapsed:active?.elapsedSeconds??null,observedAt:api.statusError?null:api.progress?.observedAt??null}:undefined,current:source.missing||source.loading?{...source.selected,unavailable:true}:api.current,path:source.missing||source.loading?source.path:api.path,entries,volumes:api.volumes,loading:api.loading||source.loading,busy:api.busy||working,error:(source.observationError?(de?'Größen konnten nicht aktualisiert werden. Bitte erneut laden.':'Sizes could not be refreshed. Please reload.'):'')||source.error||(api.error?explainError(api.error,language):resultNotice),statusText,canBack:api.canBack,
  navigate:node=>{if(!working){if(node.id.startsWith('source:'))void source.navigate(node);else{source.clear();void api.navigate(node.id,node.kind);}}},back:()=>{if(!working){source.clear();void api.back();}},search,
  scan:node=>{const n=node as NativeStorageNode;if(n.scope&&!n.unavailable&&!working)void api.scanScope(n.scope,n.name);},rescan:()=>{if(!working)setRebuildTarget({name:source.selected.name,run:()=>source.rescan(source.selected)});},canRescan:!!api.sources&&!api.pending,
  refresh:()=>{if(!working){source.refreshObservations();void api.refresh();}},retryMutation:api.pending?()=>{if(!working)void api.retryMutation();}:undefined,cancelScan:scanning?()=>{if(!working)void api.cancelScan();}:undefined,
  map:api.analysis.state.view?.map??'treemap',reveal,remove:askDelete,browse,onMap,onHover,hoverPath,readSelectedPath,nextPage:api.searchPage?.hasMore||api.groupPage?.nextCursor?nextPage:undefined,debug:JSON.stringify({app:'We.Rock.DiskMap',dsmBuild:api.dsmBuild,profile:api.profileId,snapshot:api.analysis.pin?.snapshotId??null,map:api.analysis.state.view?.map??null,renderedNodes:api.model?.order.length??0,partial:api.model?.state==='partial',truncated:api.model?.truncated??false,error:api.error,statusError:api.statusError,viewer:'unavailable'},null,2),sourceName:api.profiles.find(p=>p.profile.id===(currentJob?.profileId??api.profileId))?.profile.name??'',onBudget:setBudget,onLanguage:setLanguage,
 };
 return <DiskMapWorkspace native volumes={api.volumes} initialId={api.volumes[0]?.id??''} live={live}>
  {coverageOpen&&<CoverageDialog language={language} reasons={source.observation(source.selected)?.reasons??[]} count={source.observation(source.selected)?.reasonCount??0} onClose={()=>setCoverageOpen(false)}/>}
  {rebuildTarget&&<IndexResetDialog rebuild name={rebuildTarget.name} language={language} onCancel={()=>setRebuildTarget(null)} onConfirm={()=>{const target=rebuildTarget;setRebuildTarget(null);target.run();}}/>}
  {resetTarget&&<IndexResetDialog name={resetTarget.name} language={language} onCancel={()=>setResetTarget(null)} onConfirm={()=>{const target=resetTarget;setResetTarget(null);source.clear();void api.resetIndex(target.scope);}}/>}
  <dialog ref={dialog} className="disk-dialog disk-delete-dialog" aria-labelledby="native-delete-title" aria-describedby="native-delete-description" onCancel={event=>{event.preventDefault();if(!working)setConfirmation(null);}}>
   <header className="disk-dialog-header"><h2 id="native-delete-title">{de?'Datei löschen?':'Delete file?'}</h2></header>
   <div className="disk-delete-content">
    <p className="disk-delete-name">{confirmation?.name}</p>
    <p className="disk-delete-path">{confirmation?.path}</p>
    <p id="native-delete-description">{de?'Diese Datei wird endgültig gelöscht. Das kann nicht rückgängig gemacht werden.':'This file will be permanently deleted. This cannot be undone.'}</p>
   </div>
   <footer className="disk-delete-actions">
    <button type="button" autoFocus disabled={working} onClick={()=>setConfirmation(null)}>{de?'Abbrechen':'Cancel'}</button>
    <button className="disk-delete-confirm" type="button" disabled={working||!confirmation||confirmation.key!==key} onClick={()=>void confirmDelete()}>{working?(de?'Bitte warten …':'Please wait …'):(de?'Datei löschen':'Delete file')}</button>
   </footer>
  </dialog>
 </DiskMapWorkspace>;
}
