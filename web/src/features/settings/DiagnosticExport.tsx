import {useEffect,useRef,useState} from 'react';
import {SessionDenied} from '../../session';
import {DiagnosticExportClient,type DiagnosticPreview} from './diagnostic-export-client';
export interface DiagnosticExportProps {language:'de'|'en';disabled:boolean;sessionIdentity:string;onSessionDenied:()=>void}
// Preview, consent and download belong to one authenticated component lifetime, entirely in RAM.
export function DiagnosticExport(p:DiagnosticExportProps){
 const [privatePaths,setPrivatePaths]=useState(false),[consent,setConsent]=useState(false),[preview,setPreview]=useState<DiagnosticPreview|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(false),[url,setURL]=useState<string|null>(null);
 const current=useRef(p);current.current=p;const client=useRef<DiagnosticExportClient|null>(null),epoch=useRef(0),pending=useRef(false),objectURL=useRef<string|null>(null);const de=p.language==='de';
 // Event handlers are bound to their rendered choices, never to a later session or preview.
 const renderToken={identity:p.sessionIdentity};const rendered=useRef(renderToken);rendered.current=renderToken;const previewOwner=useRef<string|null>(null),urlOwner=useRef<string|null>(null);
 const eventLive=()=>rendered.current===renderToken&&current.current.sessionIdentity===renderToken.identity&&!current.current.disabled&&!pending.current;
 const clear=()=>{if(objectURL.current)URL.revokeObjectURL(objectURL.current);objectURL.current=null;previewOwner.current=null;urlOwner.current=null;setURL(null);setPreview(null);setConsent(false);};
 useEffect(()=>{const owned=new DiagnosticExportClient();client.current=owned;epoch.current++;pending.current=false;setBusy(false);setError(false);setPrivatePaths(false);clear();return()=>{epoch.current++;owned.dispose();if(client.current===owned)client.current=null;if(objectURL.current)URL.revokeObjectURL(objectURL.current);objectURL.current=null;};},[p.sessionIdentity,p.disabled]);
 async function run(download:boolean){
  if(!eventLive()||!client.current)return;
  if(download&&(!preview||previewOwner.current!==renderToken.identity))return;
  const owned=client.current,sequence=epoch.current,identity=current.current.sessionIdentity;const live=()=>epoch.current===sequence&&current.current.sessionIdentity===identity&&!current.current.disabled;
  pending.current=true;setBusy(true);setError(false);
  try{if(download&&preview){const bytes=await owned.download(preview,consent);if(!live())return;const next=URL.createObjectURL(new Blob([new Uint8Array(bytes)],{type:'application/zip'}));if(objectURL.current)URL.revokeObjectURL(objectURL.current);objectURL.current=next;urlOwner.current=identity;setURL(next);}else{clear();const result=await owned.preview(privatePaths);if(live()){previewOwner.current=identity;setPreview(result);}}}
  catch(e){if(!live())return;clear();if(e instanceof SessionDenied){current.current.onSessionDenied();return;}setError(true);}
  finally{if(live()){pending.current=false;setBusy(false);}}
 }
 return <section className="workspace-panel settings-panel diagnostic-export-settings" aria-label={de?'Diagnoseexport':'Diagnostic export'}><h3>{de?'Diagnoseexport':'Diagnostic export'}</h3>
 <p>{de?'Die lokale ZIP-Datei enthält technische Versionen, Konfigurationszähler und beobachtete Komponentenfehler. Unbekannte Werte bleiben unbekannt. Eine qualifizierte DSM-Fähigkeit bezeichnet nur die dokumentierte Probe des angegebenen Builds. Es erfolgt kein automatischer Versand.':'The local ZIP contains technical versions, configuration counts and observed component faults. Unknown values remain unknown. A qualified DSM capability refers only to the documented probe of the stated build. Nothing is sent automatically.'}</p>
 <label><input type="checkbox" checked={privatePaths} disabled={p.disabled||busy} onChange={e=>{if(!eventLive())return;clear();setPrivatePaths(e.target.checked);}}/>{de?'Private konfigurierte Pfade in die Vorschau aufnehmen':'Include private configured paths in the preview'}</label>
 <button disabled={p.disabled||busy} onClick={()=>void run(false)}>{de?'Vorschau erstellen':'Prepare preview'}</button>
 {!p.disabled&&previewOwner.current===p.sessionIdentity&&preview&&<><pre aria-label={de?'Inhalt der Diagnosevorschau':'Diagnostic preview contents'} style={{maxHeight:300,overflow:'auto',whiteSpace:'pre-wrap',overflowWrap:'anywhere'}}>{JSON.stringify(preview.document,null,2)}</pre>
 <p>{de?`${preview.includedPrivatePaths} private Pfade enthalten; ${preview.omittedPrivatePaths} ausgelassen.`:`${preview.includedPrivatePaths} private paths included; ${preview.omittedPrivatePaths} omitted.`}</p>
 {preview.includePrivatePaths&&<label><input type="checkbox" checked={consent} disabled={busy} onChange={e=>{if(eventLive())setConsent(e.target.checked);}}/>{de?'Ich stimme zu, die angezeigten privaten Pfade in die lokale Datei aufzunehmen.':'I consent to including the displayed private paths in the local file.'}</label>}
 <button disabled={busy||(preview.includePrivatePaths&&!consent)} onClick={()=>void run(true)}>{de?'ZIP-Datei vorbereiten':'Prepare ZIP file'}</button><button disabled={busy} onClick={()=>{if(eventLive())clear();}}>{de?'Vorschau verwerfen':'Discard preview'}</button></>}
 {!p.disabled&&urlOwner.current===p.sessionIdentity&&url&&<a href={url} download="diskmap-diagnostics.zip" onClick={e=>{if(!eventLive()||urlOwner.current!==renderToken.identity||objectURL.current!==url)e.preventDefault();}}>{de?'Diagnose-ZIP herunterladen':'Download diagnostic ZIP'}</a>}
 {busy&&<p role="status">{de?'Diagnose wird vorbereitet.':'Preparing diagnostics.'}</p>}{error&&<p role="alert">{de?'Diagnoseexport nicht verfügbar oder Vorschau abgelaufen. Erstelle eine neue Vorschau.':'Diagnostic export unavailable or preview expired. Prepare a new preview.'}</p>}
 </section>;
}
