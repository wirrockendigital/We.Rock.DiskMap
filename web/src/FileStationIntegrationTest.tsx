import {useEffect,useRef,useState} from 'react';
import {dsmCapabilities} from './dsm-capabilities';
import {requestNodeDetail,detailSelectionKey,type DetailSelection,type HistoricalDetail} from './node-details-client';
import {SessionDenied} from './session';

export interface FileStationIntegrationTestProps {
 language:'de'|'en';dsmBuild:string;selection:DetailSelection|null;disabled:boolean;
 onTest:(selection:DetailSelection)=>void;onSessionDenied:()=>void;onUnavailable?:(code:string)=>void;
}
// The settings probe qualifies the existing selection; only its explicit button may launch File Station.
export function FileStationIntegrationTest(p:FileStationIntegrationTestProps){
 const key=p.selection?detailSelectionKey(p.selection):'',current=useRef('');
 current.current=p.disabled?'':key;
 const callbacks=useRef(p);callbacks.current=p;
 const [result,setResult]=useState<{key:string;value:HistoricalDetail}|null>(null),[error,setError]=useState(''),[retry,setRetry]=useState(0);
 const supported=dsmCapabilities(p.dsmBuild).folderHandoff.status==='qualified-probe',de=p.language==='de';
 useEffect(()=>{
  const owner=new AbortController();setResult(null);setError('');
  if(!p.selection||p.disabled||!supported)return()=>owner.abort();
  // A finite read deadline cannot turn a late authentication failure into a current session event.
  const timer=setTimeout(()=>{owner.abort();if(current.current===key)setError(key);},8000);
  void requestNodeDetail(p.selection,'details','',owner.signal).then(value=>{
   if(!owner.signal.aborted&&current.current===key)setResult({key,value});
  }).catch(e=>{
   if(owner.signal.aborted||current.current!==key)return;
   if(e instanceof SessionDenied){callbacks.current.onSessionDenied();return;}
   setError(key);
   if(e instanceof Error&&['VIEW_EXPIRED','SNAPSHOT_MISMATCH','INDEX_REFRESH_REQUIRED'].includes(e.message))callbacks.current.onUnavailable?.(e.message);
  }).finally(()=>clearTimeout(timer));
  return()=>{owner.abort();clearTimeout(timer);};
 },[key,p.disabled,supported,retry]);
 const value=p.selection&&result?.key===key&&!p.disabled?result.value.detail:null;
 const eligible=!!value&&value.snapshotState==='complete'&&!value.virtual&&!!value.node&&['file','directory'].includes(value.kind);
 return <section className="workspace-panel settings-panel file-station-settings" aria-label={de?'File-Station-Integrationstest':'File Station integration test'}>
  <h3>{de?'File Station testen':'Test File Station'}</h3>
  <p>{supported?(de?'Ordnerübergabe für diesen DSM-Build qualifiziert. Automatische Dateimarkierung ist nicht qualifiziert.':'Folder handoff is qualified for this DSM build. Automatic file selection is not qualified.'):(de?'Für diesen DSM-Build ist die Übergabe nicht qualifiziert.':'Handoff is not qualified for this DSM build.')}</p>
  <p>{de?'Der Test verwendet nur das ausgewählte Objekt der geöffneten Ansicht und prüft das aktuelle Ziel erneut.':'The test uses only the selected object of the open view and checks its current target again.'}</p>
  {!p.selection?<p>{de?'Wähle zuerst eine reale Datei oder einen Ordner in der Analyse aus.':'First select a real file or folder in the analysis.'}</p>:value?<><p>{value.name}</p><p>{de?'Datenstand: ':'Snapshot: '}{value.snapshotId}</p>{!eligible&&<p>{de?'Nur reale Dateien und Ordner eines vollständigen Datenstands sind testbar.':'Only real files and folders of a complete snapshot can be tested.'}</p>}</>:error===key?<><p role="alert">{de?'Auswahl konnte nicht bestätigt werden.':'Selection could not be qualified.'}</p><button type="button" disabled={p.disabled} onClick={()=>setRetry(x=>x+1)}>{de?'Auswahl erneut prüfen':'Check selection again'}</button></>:supported&&<p role="status">{de?'Auswahl wird geprüft.':'Checking selection.'}</p>}
  <button type="button" disabled={!supported||p.disabled||!eligible} onClick={()=>{
   // A previously rendered button never acquires authority over a newer selection or lease.
   if(current.current===key&&!callbacks.current.disabled&&dsmCapabilities(callbacks.current.dsmBuild).folderHandoff.status==='qualified-probe'&&eligible&&p.selection)callbacks.current.onTest(p.selection);
  }}>{value?.kind==='file'?(de?'Test: übergeordneten Ordner öffnen':'Test: open parent folder'):(de?'Test: ausgewählten Ordner öffnen':'Test: open selected folder')}</button>
 </section>;
}
