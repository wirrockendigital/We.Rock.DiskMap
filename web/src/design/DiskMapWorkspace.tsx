import {SizeUnitContext,formatUnitBytes,nextSizeUnit,type SizeUnit} from './size-unit';
import {SetupSurface} from './SetupSurface';
import {DriveVersioningIcon,ShareStatusIcons,useDriveVersioning,type StatusShare} from './ShareStatusIcons';
import {SnapshotUsageButton} from './SnapshotUsageButton';
import type {PreferenceWorkspace} from '../contracts';
import {defaultWorkspace} from './workspace-state';
import {PaneResizeHandle} from './PaneResizeHandle';
import {useEffect,useRef,useState,type ReactNode,type CSSProperties} from 'react';
import {ArrowLeftIcon,ChevronRightIcon,Cross2Icon,GearIcon,MagnifyingGlassIcon,ClockIcon,ReloadIcon,ViewVerticalIcon,ColumnsIcon,EyeOpenIcon,EyeClosedIcon} from '@radix-ui/react-icons';
import {renderBudget} from '../visualization/shared/render-budget';
import type {AnalysisRenderBudget} from '../analysis-query';
import {bundledAbout} from '../AboutData';
import type {DebugFilter} from './diagnostics';
import {scanTimestamp,freshnessDisplay,freshnessSeconds,defaultFreshness,type ScanStamp,type FreshnessPreference} from './scan-freshness';
import {CategoryToolbar} from './CategoryToolbar';
import {FileListPanel} from './FileListPanel';
import type {FileListLoader} from './list-options';
import {DirectoryTree,type DirectoryLoader} from './DirectoryTree';
import {defaultSidebars,type SidebarPreference} from './layout-preference';
import {ScanQueue,type ScanQueueState} from './ScanQueue';
import {NotificationCenter} from './NotificationCenter';
import {FileContextMenu} from './FileContextMenu';
import {ScanPreviewSurface} from './ScanPreviewSurface';
import type {ScanPreview} from './scan-preview';
import {StorageChart} from './StorageChart';
import {pathTo,sizeOf,type Language,type StorageNode} from './model';

// Live bindings expose confirmed data and actions while the design preview remains self-contained.
export interface LiveWorkspace {
 setup?:'permissions'|'first';inventoryRefreshing?:boolean;inventoryError?:boolean;refreshInventory?:()=>void;
 statusShare?:(node:StorageNode)=>StatusShare|undefined;shareStatusRevision?:string;snapshotShareName?:string;categoryAccess?:{recycle:boolean;system:boolean};recycleState?:import('../scope-selection').RecycleState;
 storedLanguage?:Language;storedTheme?:'dark'|'light';saveLanguage?:(v:Language)=>void;saveTheme?:(v:'dark'|'light')=>void;
 selectedId?:string|null;selectFile?:(id:string)=>void;
 showCoverage?:()=>void;
 resetIndex?:(node:StorageNode|null)=>void;
 mapRoot?:StorageNode;mapPreparing?:boolean;loadGeometry?:import('./chart-layout').GeometryLoader;
 workspace?:PreferenceWorkspace;onWorkspace?:(patch:Partial<PreferenceWorkspace>)=>void;
 completion?:{jobId:string;name:string;state:string}|null;
 readFileList?:FileListLoader;treeSize?:(node:StorageNode)=>string|undefined;
 mapSnapshotId?:string;treeRevision?:string;treeRoot?:StorageNode;treePath?:StorageNode[];treeSelectedId?:string;readTree?:DirectoryLoader;treeNavigate?:(n:StorageNode)=>void;treeRescan?:(n:StorageNode)=>void;treeReveal?:(n:StorageNode)=>void;treeStamp?:(n:StorageNode)=>ScanStamp;
 freshness?:FreshnessPreference;setFreshness?:(p:FreshnessPreference)=>void;selectedStamp?:ScanStamp;unscanned?:boolean;
 scanNas?:()=>void;
 queue?:ScanQueueState;removeQueuedScan?:(id:string)=>void;
 scanPreview?:ScanPreview;
 sidebars?:SidebarPreference;layoutReady?:boolean;onSidebars?:(value:SidebarPreference)=>void;readChildren?:DirectoryLoader;snapshotKey?:string;groupEntries?:StorageNode[];current:StorageNode|null; path:StorageNode[]; entries:StorageNode[]; volumes:StorageNode[];
 openPartial?:()=>void;debugEnabled?:boolean;setDebug?:(enabled:boolean)=>void;clearDebug?:()=>void;debugLog?:string;debugLimit?:number;debugFilter?:DebugFilter;debugBytes?:number;debugCount?:number;setDebugLimit?:(n:number)=>void;setDebugFilter?:(f:DebugFilter)=>void;exportDebug?:()=>string;
 messageScope?:string;cacheStale?:boolean;cacheNotice?:string;scanProgress?:{label:string;entries:string|null;bytes:string|null;paused:boolean;phase?:string;elapsed?:string|null;observedAt?:string|null};
 scanning?:boolean; loading:boolean; busy:boolean; error:string; statusText:string; canBack:boolean;
 navigate:(node:StorageNode)=>void; back:()=>void; search:(text:string)=>void;
 scan:(node:StorageNode)=>void; rescan:()=>void; canRescan:boolean;
 reveal?:(node:StorageNode)=>void; open?:(node:StorageNode)=>void; remove?:(node:StorageNode)=>void;
 browse?:(node:StorageNode)=>Promise<StorageNode[]>; nextPage?:()=>void;
 debug:string; sourceName:string; map:'radial'|'treemap';
 refresh?:()=>void; retryMutation?:()=>void; cancelScan?:()=>void;
 readSelectedPath?:(nodeId:string,signal:AbortSignal)=>Promise<string>;
 onHover?:(nodeId:string|null)=>void; hoverPath?:string; onMap?:(map:'radial'|'treemap')=>void;
 onBudget?:(budget:AnalysisRenderBudget|null)=>void; onLanguage?:(language:Language)=>void;
}
// Vector letterforms remain readable and cannot wrap inside the narrow tree toolbar.
function NameSortIcon(){return <svg width="20" height="20" viewBox="0 0 20 20" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"><path d="M2 8 4.5 2 7 8M3 6h3M2 12h5l-5 6h5M13 3v14m-3-3 3 3 3-3"/></svg>;}
interface WorkspaceProps {volumes:StorageNode[];initialId:string; native?:boolean; live?:LiveWorkspace; children?:ReactNode}
const text={
  de:{storage:'Speicher',scan:'Scannen',choose:'Ordner wählen',settings:'Einstellungen',search:'Suchen',close:'Schließen',back:'Zurück',folders:'Ordner',files:'Dateien',used:'Belegt',free:'Frei',of:'von',preview:'Interaktive Vorschau · Beispieldaten',hint:'Ordner öffnen, um genauer hinzusehen.',radial:'Radial',map:'Disk Map',appearance:'Darstellung',dark:'Dunkel',light:'Hell',language:'Sprache',view:'Kartenansicht',cancel:'Abbrechen',scanning:'Vorschau: Scan läuft',done:'Ansicht geladen',noResults:'Keine passenden Dateien oder Ordner',empty:'Dieser Ordner ist leer',details:'Details',location:'Speicherort',size:'Größe',type:'Typ',file:'Datei',folder:'Ordner',volume:'Volume',share:'Freigabe',results:'Suchergebnisse',searchHint:'Name suchen',help:'Über diese Vorschau',helpText:'Hier kannst du die neue Oberfläche ausprobieren. Auswahl und Scannen verwenden Beispieldaten. Auf deinem NAS wird nichts verändert.',removeSearch:'Suche schließen'},
  en:{storage:'Storage',scan:'Scan',choose:'Choose folder',settings:'Settings',search:'Search',close:'Close',back:'Back',folders:'folders',files:'files',used:'Used',free:'Free',of:'of',preview:'Interactive preview · Example data',hint:'Open a folder to take a closer look.',radial:'Radial',map:'Disk Map',appearance:'Appearance',dark:'Dark',light:'Light',language:'Language',view:'Map view',cancel:'Cancel',scanning:'Preview: scanning',done:'View loaded',noResults:'No matching files or folders',empty:'This folder is empty',details:'Details',location:'Location',size:'Size',type:'Type',file:'File',folder:'Folder',volume:'Volume',share:'Shared folder',results:'Search results',searchHint:'Search by name',help:'About this preview',helpText:'Try the new interface with example data. Selecting folders and scanning do not change anything on your NAS.',removeSearch:'Close search'},
};

// Native dialogs provide modal focus containment; closing restores the initiating control.
function SettingsDialog({open,onClose,language,onLanguage,theme,onTheme,live}:{open:boolean;onClose:()=>void;language:Language;onLanguage:(v:Language)=>void;theme:'dark'|'light';onTheme:(v:'dark'|'light')=>void;live?:LiveWorkspace}){
 const ref=useRef<HTMLDialogElement>(null),t=text[language];
 const [tab,setTab]=useState('appearance');
 // Keep the draft synchronous across numeric edits and unit selection.
 const ageDraft=useRef(String(live?.freshness?.value??7));
 useEffect(()=>{ageDraft.current=String(live?.freshness?.value??7);},[live?.freshness?.value]);
 useEffect(()=>{if(!open)return;const prior=document.activeElement as HTMLElement|null;ref.current?.showModal();return()=>{ref.current?.close();prior?.focus();};},[open]);
 return <dialog ref={ref} aria-label={t.settings} className="disk-dialog disk-settings" onCancel={e=>{e.preventDefault();onClose();}}>
  <header className="disk-dialog-header"><h2>{t.settings}</h2><button className="disk-icon-button" aria-label={t.close} onClick={onClose}><Cross2Icon/></button></header>
  <nav className="disk-settings-tabs" aria-label={t.settings}>{[['appearance',t.appearance],['scans','Scans'],['info','Info'],['debug','Debug']].map(([id,label])=><button key={id} aria-pressed={tab===id} onClick={()=>setTab(id)}>{label}</button>)}</nav>
  {tab==='appearance'&&<><div className="disk-setting-row"><span>{t.appearance}</span><div className="disk-view-toggle"><button aria-pressed={theme==='light'} onClick={()=>onTheme('light')}>{t.light}</button><button aria-pressed={theme==='dark'} onClick={()=>onTheme('dark')}>{t.dark}</button></div></div>
  <label className="disk-setting-row"><span>{t.language}</span><select value={language} onChange={e=>onLanguage(e.target.value as Language)}><option value="de">Deutsch</option><option value="en">English</option></select></label></>}
  {tab==='scans'&&<div className="disk-settings-content">
   <button disabled={live?.busy||live?.scanning} onClick={()=>live?.resetIndex?.(null)}>{language==='de'?'Kompletten Index löschen':'Delete entire index'}</button>
   <h3>{language==='de'?'Scannen und in den Index aufnehmen':'Scan and include in index'}</h3>
   <label className="disk-setting-row"><span>{language==='de'?'Papierkörbe':'Recycle bins'}</span><input type="checkbox" checked={live?.workspace?.scanRecycle??true} onChange={e=>live?.onWorkspace?.({scanRecycle:e.target.checked,...(!e.target.checked?{showRecycle:false}:{})})}/></label>
   <label className="disk-setting-row"><span>{language==='de'?'Systemdateien und Systemordner':'System files and folders'}</span><input type="checkbox" checked={live?.workspace?.scanSystem??true} onChange={e=>live?.onWorkspace?.({scanSystem:e.target.checked,...(!e.target.checked?{showSystem:false}:{})})}/></label>
   <p>{language==='de'?'Gilt für den nächsten Scan. Vorhandene Ergebnisse bleiben bis zur Aktualisierung unverändert.':'Applies to the next scan. Existing results remain unchanged until refreshed.'}</p>
   <h3>{language==='de'?'In Karten und Listen anzeigen':'Show in maps and lists'}</h3>
   <label className="disk-setting-row"><span>{language==='de'?'Papierkörbe':'Recycle bins'}</span><input type="checkbox" disabled={live?.workspace?.scanRecycle===false} checked={live?.workspace?.showRecycle??false} onChange={e=>live?.onWorkspace?.({showRecycle:e.target.checked})}/></label>
   <label className="disk-setting-row"><span>{language==='de'?'Systemdateien und Systemordner':'System files and folders'}</span><input type="checkbox" disabled={live?.workspace?.scanSystem===false} checked={live?.workspace?.showSystem??false} onChange={e=>live?.onWorkspace?.({showSystem:e.target.checked})}/></label>
   <p>{language==='de'?'Nur im Index enthaltene Bereiche können angezeigt werden. Eingeblendete Inhalte erscheinen in Graustufen.':'These areas remain indexed. Visible contents use grayscale.'}</p>
   <label className="disk-setting-row"><span>{language==='de'?'Scan gilt als veraltet nach':'Scan becomes stale after'}</span><div className="disk-age-inputs">
    <input aria-label={language==='de'?'Altersgrenze':'Age threshold'} type="number" min="1" max="999" defaultValue={live?.freshness?.value??7} onChange={e=>{ageDraft.current=e.target.value;const value=Number(e.target.value);if(Number.isInteger(value)&&value>=1&&value<=999)live?.setFreshness?.({...live.freshness!,value});}} onBlur={e=>{const value=Number(e.target.value);if(Number.isInteger(value)&&value>=1&&value<=999)live?.setFreshness?.({...live.freshness!,value});else e.target.value=String(live?.freshness?.value??7);}}/>
    <select aria-label={language==='de'?'Zeiteinheit':'Time unit'} value={live?.freshness?.unit??'days'} onChange={e=>live?.setFreshness?.({value:Number(ageDraft.current)||live.freshness?.value||7,unit:e.target.value as FreshnessPreference['unit']})}>{[['minutes','Minuten','Minutes'],['hours','Stunden','Hours'],['days','Tagen','Days'],['weeks','Wochen','Weeks'],['months','Monaten','Months']].map(([v,de,en])=><option key={v} value={v}>{language==='de'?de:en}</option>)}</select>
   </div></label>
   <div className="disk-age-gradient"/><div className="disk-age-legend"><span>{language==='de'?'Aktuell':'Current'}</span><span>{language==='de'?'Wird älter':'Aging'}</span><span>{language==='de'?'Veraltet':'Stale'}</span></div>
   <p>{language==='de'?'Ein grauer Kreis bedeutet: noch nicht gescannt oder Scanzeitpunkt unbekannt. Teilabdeckung bleibt ausdrücklich gekennzeichnet.':'A gray circle means not scanned or scan time unknown. Partial coverage remains explicitly marked.'}</p>
   <small>{language==='de'?'Monate entsprechen jeweils 30 Tagen. Die Einstellung gilt für deinen Benutzer in diesem Browser.':'Months are 30-day periods. This preference applies to your account in this browser.'}</small>
  </div>}
  {tab==='info'&&<div className="disk-settings-content"><h3>We.Rock.DiskMap v{bundledAbout?.appVersion??'—'}</h3><p>{language==='de'?'Speicher analysieren, Ordner erkunden und Dateien in DSM verwalten.':'Analyze storage, explore folders and manage files in DSM.'}</p></div>}
  {tab==='debug'&&<div className="disk-settings-content">
   <label className="disk-setting-row"><span>{language==='de'?'Debugprotokoll aktivieren':'Enable debug log'}</span><input type="checkbox" checked={live?.debugEnabled??false} disabled={!live?.setDebug} onChange={e=>live?.setDebug?.(e.target.checked)}/></label>
   <p>{language==='de'?'Standardmäßig aus. Ereignisse dieser Sitzung ohne Zugangsdaten oder Dateipfade. Ist das Limit erreicht, entfallen die ältesten Einträge. Ausschalten oder Schließen der App löscht das Protokoll.':'Off by default. Session events without credentials or file paths. Oldest entries are removed at the limit. Disabling or closing the app clears the log.'}</p>
   <label className="disk-setting-row"><span>{language==='de'?'Speicherlimit (MB)':'Size limit (MB)'}</span><input aria-label={language==='de'?'Speicherlimit in MB':'Size limit in MB'} type="number" min="1" max="100" step="1" defaultValue={live?.debugLimit??10} onBlur={e=>{const n=Number(e.target.value);if(Number.isInteger(n)&&n>=1&&n<=100)live?.setDebugLimit?.(n);else e.target.value=String(live?.debugLimit??10);}}/></label>
   <small>{language==='de'?'Standard: 10 MB · Einstellbar: 1–100 MB':'Default: 10 MB · Range: 1–100 MB'}</small>
   {live?.debugEnabled&&<>
    <label className="disk-setting-row"><span>{language==='de'?'Stufe anzeigen':'Show level'}</span><select value={live.debugFilter??'all'} onChange={e=>live.setDebugFilter?.(e.target.value as DebugFilter)}>{[['all','Alles','All'],['info','Hinweis','Info'],['warning','Warnung','Warning'],['error','Fehler','Error'],['critical','Kritisch','Critical']].map(([value,de,en])=><option key={value} value={value}>{language==='de'?de:en}</option>)}</select></label>
    <p>{live.debugCount??0} {language==='de'?'Ereignisse gespeichert':'events retained'} · {((live.debugBytes??0)/1000000).toFixed(2)} / {live.debugLimit??10} MB</p>
    <div className="disk-debug-actions"><button onClick={live.clearDebug}>{language==='de'?'Leeren':'Clear'}</button><button onClick={()=>{
     // Export only the already sanitized, filtered session log; revoke the temporary URL after download.
     const url=URL.createObjectURL(new Blob([live.exportDebug?.()??''],{type:'text/plain;charset=utf-8'}));const link=document.createElement('a');link.href=url;link.download='diskmap-debug.log';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }}>{language==='de'?'Gefiltertes Protokoll exportieren':'Export filtered log'}</button></div>
    <h3>{language==='de'?'Ereignisse':'Events'}</h3><small>{language==='de'?'Anzeige: letzte 500 passende Einträge. Der Export enthält alle gespeicherten Treffer.':'Display: latest 500 matching entries. Export includes all retained matches.'}</small><pre className="disk-debug-output">{live.debugLog||'—'}</pre>
    <details><summary>{language==='de'?'Technischer Status':'Technical status'}</summary><pre className="disk-debug-output">{live.debug}</pre></details>
   </>}
  </div>}
  {!live&&<details className="disk-help"><summary>{t.help}</summary><p>{t.helpText}</p></details>}
 </dialog>;
}

// The UI owns only presentation state; example scan timing is deliberately isolated from the live app.
export function DiskMapWorkspace({volumes,initialId,native=false,live,children}:WorkspaceProps){
 const [language,setLanguage]=useState<Language>('de'),[theme,setTheme]=useState<'dark'|'light'>('dark');
 useEffect(()=>{if(live?.storedLanguage)setLanguage(live.storedLanguage);},[live?.storedLanguage]);
 useEffect(()=>{if(live?.storedTheme)setTheme(live.storedTheme);},[live?.storedTheme]);
 const [currentId,setCurrentId]=useState(initialId),[history,setHistory]=useState<string[]>([]),[hover,setHover]=useState<string|null>(null);
 const [previewMode,setPreviewMode]=useState<'radial'|'treemap'>('treemap'),[settings,setSettings]=useState(false);
 // Layout is presentation-only and never submits a scan or changes the selected source.
 // A rescan keeps its last qualified map usable; detailed progress remains available on demand.
 const [showScanDetails,setShowScanDetails]=useState(false);
 useEffect(()=>{if(!live?.scanning)setShowScanDetails(false);},[live?.scanning]);
 const [previewSidebars,setPreviewSidebars]=useState(defaultSidebars);
 const sidebars=live?.sidebars??previewSidebars;
 const [previewUnit,setPreviewUnit]=useState<SizeUnit>('mixed');
 const [previewShareStatus,setPreviewShareStatus]=useState(true);
 const workspace=live?.workspace??{...defaultWorkspace(),sizeUnit:previewUnit,showShareStatus:previewShareStatus};
 // Repeated field clicks reverse the remembered direction; field changes use natural defaults.
 const treeSortDirection=(field:'name'|'size')=>workspace.treeSort===field?(workspace.treeDirection==='asc'?'desc':'asc'):(field==='name'?'asc':'desc');
 const sortTree=(field:'name'|'size')=>live?.onWorkspace?.({treeSort:field,treeDirection:treeSortDirection(field)});
 // Describe both the current order and the next click without adding another toolbar control.
 const treeSortHint=(field:'name'|'size')=>{
  const direction=treeSortDirection(field),active=workspace.treeSort===field;
  if(language==='de')return (active?(workspace.treeDirection==='asc'?'Aufsteigend':'Absteigend')+' · ':'')+'Klick: '+(direction==='asc'?'aufsteigend':'absteigend')+' sortieren';
  return (active?(workspace.treeDirection==='asc'?'Ascending':'Descending')+' · ':'')+'Click to sort '+(direction==='asc'?'ascending':'descending');
 };
 const sizeUnit=workspace.sizeUnit??'mixed';
 // Keep every primary size label bound to the same remembered display setting.
 const formatNodeSize=(node:StorageNode,lang:Language)=>formatUnitBytes(node.bytesExact??(node.unavailable?undefined:String(Math.round(sizeOf(node)))),sizeUnit,lang);
 const formatSize=(bytes:number,lang:Language)=>formatUnitBytes(String(Math.round(bytes)),sizeUnit,lang);
 // The per-user workspace remembers status visibility independently of scan settings.
 const showShareStatus=workspace.showShareStatus??true;
 const driveStatus=useDriveVersioning(!!live,live?.shareStatusRevision??'');
 const setSidebars=(value:SidebarPreference)=>live?.onSidebars?live.onSidebars(value):setPreviewSidebars(value);
 const [searching,setSearching]=useState(false),[,setQuery]=useState(''),[selectedFile,setSelectedFile]=useState<StorageNode|null>(null);
 const [treeContext,setTreeContext]=useState<{node:StorageNode;x:number;y:number}|null>(null);
 const [,setAgeTick]=useState(0);useEffect(()=>{const t=setInterval(()=>setAgeTick(n=>n+1),60000);return()=>clearInterval(t);},[]);
 const [context,setContext]=useState<{node:StorageNode;x:number;y:number}|null>(null);
 const [scan,setScan]=useState<{target:StorageNode;progress:number}|null>(null),[notice,setNotice]=useState('');
 const mapArea=useRef<HTMLElement>(null);
 const scanTimer=useRef<ReturnType<typeof setInterval>|null>(null), searchRef=useRef<HTMLInputElement>(null);
 const t=text[language],fallback:StorageNode={id:'empty',name:live?.sourceName||(language==='de'?'Quelle auswählen':'Choose a source'),kind:'folder',children:[],unavailable:true};
 const path=live?.path??pathTo(volumes,currentId),current=live?live.current??fallback:path.at(-1)??volumes[0]??fallback,volume=path[0]??current;
 // A newly selected source cannot retain a file inspector from the previous view.
 useEffect(()=>{setSelectedFile(null);setContext(null);},[current.id,live?.snapshotKey]);
 // Restored selections are resolved only inside the currently qualified map tree.
 useEffect(()=>{if(!live?.selectedId||!live.mapRoot)return;const pending=[live.mapRoot];while(pending.length){const n=pending.pop()!;if(n.id===live.selectedId){if(n.kind==='file')setSelectedFile(n);return;}for(const child of n.children??[])pending.push(child);}},[live?.selectedId,live?.mapRoot]);
 // Resolve only the selected path, independently of hover and without expanding the map.
 const [resolvedSelection,setResolvedSelection]=useState<{id:string;scope:string;path:string}|null>(null);
 const selectionScope=[live?.snapshotKey,current.id].join(':');
 const selectedId=selectedFile?.id,knownSelectedPath=selectedFile?.path;
 const readSelectedPath=live?.readSelectedPath,confirmedSelectedId=live?.selectedId;
 useEffect(()=>{
  if(!selectedId||knownSelectedPath||!readSelectedPath||confirmedSelectedId!==selectedId)return;
  const controller=new AbortController();
  void readSelectedPath(selectedId,controller.signal).then(value=>{
   if(!controller.signal.aborted)setResolvedSelection({id:selectedId,scope:selectionScope,path:value});
  }).catch(()=>{/* A failed path lookup must not display another file's path. */});
  return()=>controller.abort();
 },[selectedId,knownSelectedPath,confirmedSelectedId,readSelectedPath,selectionScope]);
 const selectionPath=knownSelectedPath||(resolvedSelection&&resolvedSelection.id===selectedId&&resolvedSelection.scope===selectionScope?resolvedSelection.path:'');
 const inspectedFile=selectedFile?{...selectedFile,path:selectionPath||undefined}:null;

 const total=sizeOf(current),volumeSize=sizeOf(volume),capacity=volume.capacity??volumeSize;
 // Recorded totals can be positive while category switches hide every drawable file.
 const mapTotal=live?.mapRoot?sizeOf(live.mapRoot):total;
 const hiddenOnly=!!live?.mapRoot&&mapTotal===0&&Number(live.mapRoot.hiddenBytes??0)>0;
 const hasCachedMap=!!live?.current&&total>0;
 const [showLivePreview,setShowLivePreview]=useState(false);
 useEffect(()=>{setShowLivePreview(false);},[live?.messageScope]);
 const hasPreview=!!live?.scanPreview&&BigInt(live.scanPreview.knownLogicalBytes)>0n;
 const showPreview=hasPreview&&!showScanDetails&&(!hasCachedMap||showLivePreview);
 const showScanCenter=!!live?.scanProgress&&(!hasCachedMap||showScanDetails);
 const changeHover=(id:string|null)=>{if(id===hover)return;setHover(id);live?.onHover?.(id);};
 // The confirmed native view owns both the selected map and its render budget.
 const mode=live?.map??previewMode;
 const changeMap=(value:'radial'|'treemap')=>{if(live)live.onMap?.(value);else setPreviewMode(value);};
 const navigate=(node:StorageNode)=>{
  if(node.kind==='file'){setSelectedFile(node);live?.selectFile?.(node.id);return;}
  if(live){if(node.aggregated)live.onWorkspace?.({right:true});live.navigate(node);setHover(null);setSelectedFile(null);setQuery('');setSearching(false);return;}
  setHistory(old=>[...old,current.id]);setCurrentId(node.id);setHover(null);setQuery('');setSearching(false);setSelectedFile(null);
 };
 const back=()=>{if(live){live.back();setHover(null);setSelectedFile(null);return;}const id=history.at(-1)??path.at(-2)?.id;if(id){setCurrentId(id);setHistory(old=>old.slice(0,-1));setHover(null);setQuery('');setSelectedFile(null);}};
 useEffect(()=>()=>{if(scanTimer.current)clearInterval(scanTimer.current);},[]);
 useEffect(()=>{if(searching)searchRef.current?.focus();},[searching]);
 useEffect(()=>{document.documentElement.lang=language;live?.onLanguage?.(language);},[language,live?.onLanguage]);
 // Measure the actual DSM map pane before requesting a bounded hierarchy.
 useEffect(()=>{
  if(!mapArea.current||!live?.onBudget)return;
  let timer:ReturnType<typeof setTimeout>|undefined;
  const callback=live.onBudget;
  const observer=new ResizeObserver(([entry])=>{clearTimeout(timer);timer=setTimeout(()=>callback(renderBudget({kind:mode,detail:mode==='treemap'?'max':'auto',width:entry.contentRect.width,height:entry.contentRect.height})),120);});
  observer.observe(mapArea.current);
  return()=>{observer.disconnect();clearTimeout(timer);};
 },[mode,live?.onBudget]);
 const startPreviewScan=(target:StorageNode)=>{
  if(live){live.scan(target);return;}
  if(native)return;
  setScan({target,progress:0});setNotice('');
  // This animation previews the interaction, never submits a scan request or synthesizes live receipts.
  let progress=0;scanTimer.current=setInterval(()=>{
   progress=Math.min(100,progress+10);setScan({target,progress});
   if(progress===100){clearInterval(scanTimer.current!);scanTimer.current=null;setScan(null);navigate(target);setNotice(t.done);}
  },160);
 };
 const cancelScan=()=>{if(scanTimer.current)clearInterval(scanTimer.current);scanTimer.current=null;setScan(null);};
 // The file list owns its paginated query independently of chart detail.
 const listKey=(live?.snapshotKey??'preview')+':'+current.id;
 const entries=live?.entries??current.children??[];
 // Hover temporarily takes precedence; leaving the map restores the selected file path.
 const footerPath=(hover?(live?.hoverPath||entries.find(n=>n.id===hover)?.path):'')||selectionPath||(selectedFile?(language==='de'?'Pfad wird geladen …':'Loading path …'):current.path??path.map(n=>n.name).join(' / '));
 // Header actions use the selected live scope, independent of the displayed cached generation.
 const headerPath=live?.treePath??path,atNas=headerPath.length===1&&headerPath[0]?.name==='NAS';
 const stamp=live?.selectedStamp??{state:'unknown' as const,endedAt:null};
 const age=freshnessDisplay({...stamp,state:stamp.endedAt?'complete':stamp.state},live?.freshness??defaultFreshness(),language);
 const stale=!!stamp.endedAt&&Date.now()-Date.parse(stamp.endedAt)>=freshnessSeconds(live?.freshness??defaultFreshness())*1000;
 const ageLabel=stamp.endedAt?age.label+(language==='de'?' gescannt':' scanned')+(stale?(language==='de'?' · Veraltet':' · Stale'):''):age.label;
 const running=!!scan||!!live?.scanning;
 const headerNavigate=live?.treeNavigate??navigate;


 return <SizeUnitContext.Provider value={sizeUnit}><main className="disk-workspace" data-theme={theme} data-layout="list" data-left={!live?.setup&&sidebars.left} data-right={!live?.setup&&(sidebars.right||searching)} data-map={mode} data-search={searching}>
  <header className="disk-toolbar">
   <button className="disk-icon-button" disabled={!!live?.setup} aria-label={language==='de'?'Ordnerbaum umschalten':'Toggle directory tree'} aria-pressed={sidebars.left} onClick={()=>setSidebars({...sidebars,left:!sidebars.left})}><ViewVerticalIcon/></button>
   <button className="disk-icon-button" aria-label={t.back} title={t.back} disabled={live?!live.canBack:!history.length&&path.length<2} onClick={back}><ArrowLeftIcon/></button>
   <nav className="disk-crumbs" aria-label={t.location}>{headerPath.map((node,index)=><span key={node.id}>{index>0&&<ChevronRightIcon/>}<button aria-current={node.id===current.id?'location':undefined} onClick={()=>headerNavigate(node)} disabled={node.id===headerPath.at(-1)?.id}>{node.name}</button></span>)}</nav>
   <div className="disk-header-scan">
    {running?<><button className="disk-scan-age" onClick={()=>setShowScanDetails(v=>!v)} title={live?.scanProgress?.label??t.scanning}><ReloadIcon className="disk-spin"/><span>{live?.scanProgress?.label??t.scanning}{live?.scanProgress?.entries?' · '+Number(live.scanProgress.entries).toLocaleString(language):''}</span></button><button className="disk-header-refresh" disabled={live?live.busy||!live.cancelScan:false} onClick={()=>live?live.cancelScan?.():cancelScan()}>{t.cancel}</button></>:<>
     <span className="disk-scan-age" style={{color:age.color==='transparent'?'var(--disk-muted)':age.color}} title={scanTimestamp(stamp,language)}><ClockIcon/><span>{live?.setup?(language==='de'?'Einrichtung':'Setup'):ageLabel}</span></span>
     {stamp.state==='partial'&&<button className="disk-header-coverage" onClick={live?.showCoverage} title={language==='de'?'Nicht alle Bereiche vollständig erfasst':'Not all areas fully recorded'}>{language==='de'?'Teilabdeckung':'Partial'}</button>}

    </>}
     {!atNas&&<button className="disk-header-refresh" disabled={live?live.busy||live.loading||!live.canRescan:false} title={language==='de'?'Diesen Bereich neu scannen':'Scan this area again'} onClick={()=>live?live.rescan():startPreviewScan(current)}><ReloadIcon/>{live?.unscanned||stamp.state==='unscanned'?(language==='de'?'Scannen':'Scan'):(language==='de'?'Aktualisieren':'Refresh')}</button>}
   </div>
   <div className="disk-toolbar-actions">
    <div className="disk-view-toggle disk-toolbar-map" aria-label={t.view}><button aria-pressed={mode==='radial'} onClick={()=>changeMap('radial')}>{t.radial}</button><button aria-pressed={mode==='treemap'} onClick={()=>changeMap('treemap')}>{t.map}</button></div>
    <button className="disk-icon-button" aria-label={t.search} title={t.search} aria-pressed={searching} onClick={()=>{setSearching(!searching);setQuery('');live?.search('');}}><MagnifyingGlassIcon/></button>
    <button className="disk-icon-button" aria-label={t.settings} title={t.settings} onClick={()=>setSettings(true)}><GearIcon/></button>
    {live?.queue&&live.removeQueuedScan&&<ScanQueue queue={live.queue} busy={live.busy||!!live.retryMutation} remove={live.removeQueuedScan} language={language}/>}
    <NotificationCenter completion={live?.completion} language={language} source={live?.messageScope??'preview'} status={live?.statusText||notice} error={live?.error??''} actions={live&&<>
     {live.error&&live.refresh&&<button onClick={live.refresh}>{language==='de'?'Erneut laden':'Reload'}</button>}
     {live.retryMutation&&<button onClick={live.retryMutation}>{language==='de'?'Status klären':'Resolve status'}</button>}
    </>}/>
    {/* Keep the preview switch available after informational notifications expire. */}
    {hasPreview&&hasCachedMap&&<button className="disk-primary-button" onClick={()=>{setShowLivePreview(v=>!v);setShowScanDetails(false);setContext(null);setSelectedFile(null);}}>{showLivePreview?(language==='de'?'Gespeicherte Karte':'Recorded map'):(language==='de'?'Scanvorschau':'Scan preview')}</button>}
    <button className="disk-primary-button disk-scan-nas" disabled={live?(live.busy||live.setup==='permissions'||!live.scanNas):running} title={language==='de'?'Alle für das Paketkonto lesbaren NAS-Bereiche vollständig scannen':'Fully scan all NAS areas readable by the package account'} onClick={()=>live?live.scanNas?.():startPreviewScan(volumes[0])}><ReloadIcon/>{language==='de'?'Scanne NAS':'Scan NAS'}</button>
    <button className="disk-icon-button" disabled={!!live?.setup} aria-label={language==='de'?'Dateiliste umschalten':'Toggle file list'} aria-pressed={sidebars.right} onClick={()=>{setSidebars({...sidebars,right:!sidebars.right});if(searching){setSearching(false);setQuery('');live?.search('');}}}><ColumnsIcon/></button>
   </div>
  </header>
  {scan&&<div className="disk-progress" role="status"><ReloadIcon className="disk-spin"/><span>{t.scanning} · {scan.target.name}</span><progress max="100" value={scan.progress}/><span>{scan.progress}%</span><button onClick={cancelScan}>{t.cancel}</button></div>}
  <div className="disk-body" style={{'--disk-left-width':workspace.leftWidth+'px','--disk-right-width':workspace.rightWidth+'px'} as CSSProperties}>
   {sidebars.left&&<aside className="disk-tree-pane"><header className="disk-tree-tools">
    <button className="disk-icon-button" title={treeSortHint('name')} aria-description={treeSortHint('name')} aria-label={language==='de'?'Nach Name sortieren':'Sort by name'} aria-pressed={workspace.treeSort==='name'} onClick={()=>sortTree('name')}><NameSortIcon/></button>
    <button className="disk-icon-button" title={treeSortHint('size')} aria-description={treeSortHint('size')} aria-label={language==='de'?'Nach Größe sortieren':'Sort by size'} aria-pressed={workspace.treeSort==='size'} onClick={()=>sortTree('size')}><span aria-hidden="true">▥</span></button>
    <button type="button" className="disk-icon-button disk-tree-status-toggle" title={language==='de'?'Freigabenstatus ein-/ausblenden':'Show/hide share status'} aria-label={language==='de'?'Freigabenstatus ein-/ausblenden':'Show/hide share status'} aria-pressed={showShareStatus} onClick={()=>live?.onWorkspace?live.onWorkspace({showShareStatus:!showShareStatus}):setPreviewShareStatus(value=>!value)}>{showShareStatus?<EyeOpenIcon/>:<EyeClosedIcon/>}</button>
    <button type="button" className="disk-icon-button disk-size-unit-toggle" title={language==='de'?'Größeneinheit wechseln: TB / GB / MB / Mixed / keine Anzeige':'Change size unit: TB / GB / MB / Mixed / hidden'} aria-label={(language==='de'?'Größeneinheit: ':'Size unit: ')+(sizeUnit==='hidden'?(language==='de'?'keine Anzeige':'hidden'):sizeUnit==='mixed'?'Mixed':sizeUnit)} onClick={()=>{const next=nextSizeUnit(sizeUnit);if(live?.onWorkspace)live.onWorkspace({sizeUnit:next});else setPreviewUnit(next);}}>{sizeUnit==='hidden'?'—':sizeUnit==='mixed'?'Mix':sizeUnit}</button>
   </header><PaneResizeHandle side="left" width={workspace.leftWidth} onWidth={leftWidth=>live?.onWorkspace?.({leftWidth})}/>
    {(!live||live.layoutReady)&&(live?.treeRoot??(live?path[0]:volumes[0]))?<DirectoryTree shareStatus={showShareStatus&&live?node=>{const share=live.statusShare?.(node);return share?<ShareStatusIcons key={share.id} share={share} drive={driveStatus(share.name)} language={language}/>:null;}:undefined} workspace={workspace} onWorkspace={live?.onWorkspace} key={live?.treeRoot?'live-inventory:'+live.treeRevision:live?.snapshotKey??'preview'} root={live?.treeRoot??(live?{...path[0],name:'NAS'}:volumes[0])} path={live?.treePath??path} selectedId={live?.treeSelectedId??current.id} language={language} load={live?.readTree??live?.readChildren} onNavigate={live?.treeNavigate??navigate} onContextMenu={live?.treeRescan?(node,x,y)=>setTreeContext({node,x,y}):undefined} size={live?.treeSize} stamp={live?.treeStamp} freshness={live?.freshness}/>:<p className="disk-tree-status">{language==='de'?'Wähle einen gespeicherten Scan oder scanne deinen Speicher.':'Choose a recorded scan or scan your storage.'}</p>}
   </aside>}
   <section ref={mapArea} className="disk-map-area" aria-label={t.view}>
    {live?.setup?<SetupSurface language={language} mode={live.setup} refreshing={!!live.inventoryRefreshing} failed={!!live.inventoryError} refresh={()=>live.refreshInventory?.()} shares={(live.treeRoot?.children??[]).flatMap(v=>v.children??[])} select={n=>live.treeNavigate?.(n)}/>:live?.unscanned?<div className="disk-empty"><h2>{live.loading?(language==='de'?'Wird geladen …':'Loading …'):(language==='de'?'Kein gespeicherter Scan für diesen Bereich':'No stored scan for this area')}</h2><p>{current.name}</p>{!live.loading&&<button className="disk-primary-button" disabled={live.busy} onClick={()=>live.treeRescan?.(current)}>{language==='de'?'Ordner scannen':'Scan folder'}</button>}</div>:showPreview&&live?.scanPreview?<ScanPreviewSurface preview={live.scanPreview} language={language} mode={mode}/>:showScanCenter&&live?.scanProgress?<div className="disk-scan-center" role="status">
      <div className={`disk-scan-ring${live.scanProgress.paused?' is-paused':''}`}><svg aria-hidden="true" viewBox="0 0 200 200"><circle className="disk-ring-track" cx="100" cy="100" r="88"/><circle className="disk-ring-activity" cx="100" cy="100" r="88"/></svg><div><strong>{live.scanProgress.bytes!==null?formatNodeSize({id:'progress',name:'',kind:'folder',bytesExact:live.scanProgress.bytes},language):'…'}</strong><small>{language==='de'?'erfasst':'observed'}</small></div></div>
      <h2>{live.scanProgress.label}</h2><p>{live.sourceName}</p>
      {live.scanProgress.entries!==null&&<p>{new Intl.NumberFormat(language).format(BigInt(live.scanProgress.entries))} {language==='de'?'Einträge':'entries'}</p>}
      <small>{['aggregating','validating','publishing'].includes(live.scanProgress.phase??'')?(language==='de'?'Scan abgeschlossen. Das gespeicherte Ergebnis wird geprüft; die Dateizähler ändern sich dabei nicht.':'Scan complete. The stored result is being validated; file counters remain unchanged.'):(language==='de'?'Gesamtmenge noch unbekannt · Dateigrößen aus Metadaten':'Total size unknown · File sizes from metadata')}</small>
      <small>{live.scanProgress.elapsed!==null&&live.scanProgress.elapsed!==undefined?`${language==='de'?'Laufzeit':'Elapsed'}: ${Math.floor(Number(live.scanProgress.elapsed)/60)} min ${Number(live.scanProgress.elapsed)%60} s · `:''}{live.scanProgress.observedAt?`${language==='de'?'Status abgefragt':'Status checked'}: ${new Date(live.scanProgress.observedAt).toLocaleTimeString(language)}`:(language==='de'?'Status derzeit nicht erreichbar':'Status currently unavailable')}</small>
      {live.cacheNotice&&<small>{live.cacheNotice}</small>}
      {live.cancelScan&&<button onClick={live.cancelScan} disabled={live.busy}>{t.cancel}</button>}
    </div>:total>0&&live&&!live.mapRoot?<div className="disk-empty" role="status"><h2>{live.mapPreparing?(language==='de'?'Dateikarte wird vorbereitet …':'Preparing file map …'):(language==='de'?'Dateikarte wird geladen …':'Loading file map …')}</h2>{live.mapPreparing&&<p>{language==='de'?'Vorhandene Scandaten werden für die Darstellung aufbereitet.':'Preparing the existing scan data for display.'}</p>}</div>:mapTotal>0?<StorageChart savedZoom={live?.workspace?.zoom?.snapshotId===live?.mapSnapshotId?live?.workspace?.zoom:null} onZoom={zoom=>live?.onWorkspace?.({zoom:{...zoom,snapshotId:live.mapSnapshotId}})} loadGeometry={live?.loadGeometry} root={live?.mapRoot??current} mode={mode} language={language} hovered={hover} selectedId={selectedFile?.id??live?.selectedId} onHover={changeHover} onOpen={navigate} onContextMenu={(node,x,y)=>setContext({node,x,y})}/>:<div className="disk-empty"><h2>{live&&!live.current?(live.openPartial?(language==='de'?'Scan mit Einschränkungen':'Scan with limited coverage'):live.scanning?(language==='de'?'Dein Speicher wird analysiert':'Analyzing your storage'):live.loading?(language==='de'?'Wird geladen …':'Loading …'):(language==='de'?'Wähle deinen Speicher':'Choose your storage')):hiddenOnly?(language==='de'?'Keine sichtbaren Dateien':'No visible files'):t.empty}</h2><p>{live&&!live.current?(live.openPartial?(language==='de'?'Öffne das Teilergebnis, um die lesbaren Bereiche zu sehen.':'Open the partial result to view the readable areas.'):live.scanning?(language==='de'?'Die Karte erscheint, sobald der Scan fertig ist.':'The map will appear when the scan finishes.'):(language==='de'?'Volume oder Ordner wählen und scannen.':'Choose a volume or folder and scan.')):hiddenOnly?(language==='de'?'Die erfassten Dateien sind durch die Anzeigeoptionen für Papierkörbe oder Systemdateien ausgeblendet.':'Recorded files are hidden by the recycle-bin or system-file display options.'):current.name}</p></div>}

   </section>
   {(sidebars.right||searching)&&<aside className="disk-side"><PaneResizeHandle side="right" width={workspace.rightWidth} onWidth={rightWidth=>live?.onWorkspace?.({rightWidth})}/>
    <CategoryToolbar recycleState={live?.recycleState} access={live?.categoryAccess} root={live?.unscanned?undefined:live?.mapRoot??(!live?current:undefined)} language={language} workspace={workspace} onChange={live?.onWorkspace}>{live&&<><SnapshotUsageButton key={live.snapshotShareName} share={live.snapshotShareName} language={language}/>{live.snapshotShareName&&<DriveVersioningIcon status={driveStatus(live.snapshotShareName)} language={language}/>}</>}</CategoryToolbar>

    {(!live||live.layoutReady)&&<FileListPanel workspace={workspace} onWorkspace={live?.onWorkspace} root={current} identity={listKey} language={language} load={live?.readFileList} selected={inspectedFile} onSelect={navigate} onHover={changeHover} onContext={(node,x,y)=>setContext({node,x,y})} focusSearch={searching}/>}

    {!live&&<div className="disk-summary"><div><span>{volume.name}</span><span>{formatSize(capacity-volumeSize,language)} {t.free.toLocaleLowerCase(language)}</span></div><div className="disk-capacity-bar"><span style={{width:`${Math.min(100,volumeSize/capacity*100)}%`}}/></div><small>{formatSize(volumeSize,language)} {t.of} {formatSize(capacity,language)} {t.used.toLocaleLowerCase(language)}</small></div>}
   </aside>}

  </div>
  {/* The path already includes the filename; the trailing field shows only its size. */}
  <footer className="disk-footer"><span className="disk-brand">We.Rock.DiskMap v{bundledAbout?.appVersion??'—'}</span><span className="disk-hover-path" title={footerPath}>{footerPath}</span><span className="disk-selection-size">{selectedFile?formatNodeSize(selectedFile,language):formatNodeSize({...current,bytesExact:live?.mapRoot?.normalBytes??current.bytesExact},language)}</span></footer>
  {treeContext&&<FileContextMenu {...treeContext} language={language} onClose={()=>setTreeContext(null)} rescanOnly onReveal={live?.treeReveal&&!live.busy&&!treeContext.node.unavailable&&['share','folder'].includes(treeContext.node.kind)?()=>live.treeReveal!(treeContext.node):undefined} onReset={!live?.busy&&!live?.scanning?()=>live?.resetIndex?.(treeContext.node):undefined} onRescan={!live?.busy&&!treeContext.node.unavailable?()=>live?.treeRescan?.(treeContext.node):undefined}/>}
  <FileContextMenu {...(context??{node:current,x:0,y:0})} open={!!context} language={language} onClose={()=>setContext(null)} onReveal={context&&live?.reveal&&!context.node.unavailable?()=>live.reveal!(context.node):undefined} onOpen={context&&live?.open&&context.node.kind==='file'?()=>live.open!(context.node):undefined} onDelete={context&&live?.remove&&context.node.kind==='file'&&!context.node.unavailable?()=>live.remove!(context.node):undefined}/>
  {/* Native action dialogs inherit the same theme and input styles as the workspace. */}
  {children}
  <SettingsDialog open={settings} onClose={()=>setSettings(false)} language={language} onLanguage={value=>{setLanguage(value);live?.saveLanguage?.(value);}} theme={theme} onTheme={value=>{setTheme(value);live?.saveTheme?.(value);}} live={live}/>
 </main></SizeUnitContext.Provider>;
}
