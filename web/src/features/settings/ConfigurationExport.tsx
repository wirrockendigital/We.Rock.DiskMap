import {useEffect,useRef,useState} from 'react';
import {SessionDenied} from '../../session';
import {ConfigurationExportClient} from './configuration-export-client';
export interface ConfigurationExportProps {language:'de'|'en';disabled:boolean;sessionIdentity:string;onSessionDenied:()=>void}
// An export remains in memory until explicitly downloaded and is discarded with its session lifetime.
export function ConfigurationExport(p:ConfigurationExportProps){
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[ready,setReady]=useState<{url:string;count:number;bytes:number;identity:string}|null>(null);
 const client=useRef<ConfigurationExportClient|null>(null),url=useRef<string|null>(null),epoch=useRef(0),pending=useRef(false),current=useRef(p);current.current=p;const de=p.language==='de',identityAtRender=p.sessionIdentity;
 const clear=()=>{if(url.current)URL.revokeObjectURL(url.current);url.current=null;setReady(null);};
 // Effects own clients so StrictMode replay cannot reuse a disposed reader.
 useEffect(()=>{const owned=new ConfigurationExportClient();client.current=owned;epoch.current++;pending.current=false;setBusy(false);setError(null);clear();return()=>{epoch.current++;pending.current=false;owned.dispose();if(client.current===owned)client.current=null;if(url.current)URL.revokeObjectURL(url.current);url.current=null;};},[p.sessionIdentity,p.disabled]);
 async function prepare(){
  if(identityAtRender!==current.current.sessionIdentity||current.current.disabled||pending.current||!client.current)return;
  const owned=client.current,sequence=epoch.current,identity=current.current.sessionIdentity;pending.current=true;setBusy(true);setError(null);clear();
  const live=()=>sequence===epoch.current&&identity===current.current.sessionIdentity&&!current.current.disabled;
  try{const result=await owned.read();if(!live())return;const blob=new Blob([result.json],{type:'application/json;charset=utf-8'});const objectURL=URL.createObjectURL(blob);url.current=objectURL;setReady({url:objectURL,count:result.bundle.profiles.length,bytes:blob.size,identity});}
  catch(e){if(!live())return;clear();if(e instanceof SessionDenied){current.current.onSessionDenied();return;}setError(e instanceof Error&&e.message==='CONFIGURATION_TOO_LARGE'?'large':'unavailable');}
  finally{if(live()){pending.current=false;setBusy(false);}}
 }
 const result=!p.disabled&&ready?.identity===p.sessionIdentity?ready:null;
 return <section className="workspace-panel settings-panel configuration-export-settings" aria-label={de?'Konfiguration exportieren':'Export configuration'}><h3>{de?'Konfiguration exportieren':'Export configuration'}</h3>
 <p>{de?'Die Datei enthält Profile, Ausschlüsse sowie geeignete globale und persönliche Einstellungen. Profilnamen und Pfade können privat sein. Der Export ist nicht anonym; bewahre ihn geschützt auf.':'The file contains profiles, exclusions and eligible global and personal settings. Profile names and paths may be private. This export is not anonymous; store it securely.'}</p>
 <p>{de?'Passwörter, Sitzungen, Dateiinhalte und Scan-Indizes gehören nicht zum Export. Das Erstellen startet keinen Scan und ändert keine Einstellungen.':'Passwords, sessions, file contents and scan indexes are excluded. Preparing the export starts no scan and changes no settings.'}</p>
 <button disabled={p.disabled||busy} onClick={()=>void prepare()}>{de?'Exportdatei vorbereiten':'Prepare export file'}</button>
 {busy&&<p role="status">{de?'Konfiguration wird gelesen.':'Reading configuration.'}</p>}
 {result&&<><p role="status">{de?`${result.count} ${result.count===1?'Profil':'Profile'} · ${result.bytes} Bytes bereit.`:`${result.count} ${result.count===1?'profile':'profiles'} · ${result.bytes} bytes ready.`}</p><a href={result.url} download="we-rock-diskmap-configuration.json" onClick={event=>{if(current.current.disabled||current.current.sessionIdentity!==result.identity||url.current!==result.url)event.preventDefault();}}>{de?'JSON-Konfiguration herunterladen':'Download JSON configuration'}</a><button disabled={p.disabled||busy} onClick={()=>{if(!current.current.disabled&&!pending.current)clear();}}>{de?'Vorbereitete Datei verwerfen':'Discard prepared file'}</button></>}
 {error&&<p role="alert">{error==='large'?(de?'Die Konfiguration überschreitet die Exportgrenze von 4 MiB. Es wurde keine Datei vorbereitet.':'The configuration exceeds the 4 MiB export limit. No file was prepared.'):(de?'Export nicht verfügbar. Prüfe die Sitzung und versuche es erneut.':'Export unavailable. Check the session and try again.')}</p>}
 </section>;
}
