import {useEffect,useRef,useState} from 'react';
import {SessionDenied} from '../../session';
import {TemplateClient,type GlobalTemplate,type TemplateRule} from './template-client';
export type GlobalExclusionSettingsProps={language:'de'|'en';disabled:boolean;onDirty:(dirty:boolean)=>void;onSessionDenied:()=>void};
// This global draft edits only the shared copy source, never effective rules in existing profiles.
export function GlobalExclusionSettings(p:GlobalExclusionSettingsProps){
 const [client,setClient]=useState<TemplateClient|null>(null),[base,setBase]=useState<GlobalTemplate|null>(null),[draft,setDraft]=useState<TemplateRule[]>([]),[busy,setBusy]=useState(false),[error,setError]=useState(false),[tick,setTick]=useState(0);const epoch=useRef(0),callbacks=useRef(p);callbacks.current=p;const de=p.language==='de';
 const dirty=!!base&&JSON.stringify(base.rules)!==JSON.stringify(draft),blocked=p.disabled||busy||client?.unresolved||client?.status==='conflict';
 // A pending receipt is dirty even if the visible fields equal the previous saved value.
 useEffect(()=>{p.onDirty(dirty||busy||!!client?.unresolved);return()=>p.onDirty(false);},[dirty,busy,tick,p.onDirty,client]);
 async function run(action:()=>Promise<GlobalTemplate>,initial=false){if(!initial&&(busy||p.disabled))return;const current=epoch.current;setBusy(true);setError(false);try{const result=await action();if(current!==epoch.current)return;setBase(result);setDraft(structuredClone(result.rules));}catch(e){if(current!==epoch.current)return;setError(true);if(e instanceof SessionDenied){setBase(null);setDraft([]);callbacks.current.onSessionDenied();}}finally{if(current===epoch.current){setBusy(false);setTick(v=>v+1);}}}
 // Each effect lifetime owns a fresh client, including React StrictMode setup replay.
 useEffect(()=>{const owned=new TemplateClient();epoch.current++;setClient(owned);void run(()=>owned.read(),true);return()=>{epoch.current++;owned.dispose();};},[]);
 const change=(index:number,value:TemplateRule)=>{if(!blocked)setDraft(rows=>rows.map((row,i)=>i===index?value:row));};
 return <section className="workspace-panel settings-panel global-exclusion-settings" aria-label={de?'Globale Ausschlussvorlage':'Global exclusion template'}><h3>{de?'Globale Ausschlussvorlage':'Global exclusion template'}</h3>
 <p>{de?'Gilt für alle Administratoren. Profile übernehmen die Vorlage nur bewusst als Kopie. Speichern oder Zurücksetzen ändert keine bestehenden Profile, Aufträge oder Datenstände.':'Shared by all administrators. Profiles use this template only when explicitly copied. Saving or resetting changes no existing profiles, jobs or snapshots.'}</p>
 {base&&<p>{de?'Revision':'Revision'}: {base.revision}</p>}
 {draft.map((r,i)=><div className="workspace-rule" key={i}><label>{de?'Regeltyp':'Rule type'} {i+1}<select disabled={blocked} value={r.kind} onChange={e=>change(i,{...r,kind:e.target.value as TemplateRule['kind']})}><option value="path">{de?'Relativer Pfad':'Relative path'}</option><option value="pattern">{de?'Muster':'Pattern'}</option></select></label><label>{de?'Regel':'Rule'} {i+1}<input disabled={blocked} maxLength={4096} value={r.value} onChange={e=>change(i,{...r,value:e.target.value})}/></label><button disabled={blocked} onClick={()=>setDraft(rows=>rows.filter((_,j)=>j!==i))}>{de?'Regel entfernen':'Remove rule'} {i+1}</button></div>)}
 <p>{de?'Pfade sind relativ zur jeweiligen Wurzel; Muster verwenden *, ? oder ** als vollständige Pfadkomponente. Syntax und UTF-8-Größen prüft der Server.':'Paths are relative to each root; patterns use *, ? or ** as a complete path component. The server validates syntax and UTF-8 limits.'}</p>
 <button disabled={blocked||!base||draft.length>=256} onClick={()=>setDraft(rows=>[...rows,{kind:'pattern',value:''}])}>{de?'Regel hinzufügen':'Add rule'}</button>
 <button disabled={blocked||!base||!dirty} onClick={()=>void run(()=>client!.save(base!.revision,draft))}>{de?'Vorlage speichern':'Save template'}</button>
 <button disabled={blocked||!dirty} onClick={()=>{setDraft(structuredClone(base!.rules));setError(false);}}>{de?'Verwerfen':'Discard'}</button>
 <button disabled={blocked||!base} onClick={()=>setDraft([])}>{de?'Vorlage auf Standardwerte setzen (leer)':'Reset template to defaults (empty)'}</button>
 {client?.status==='uncertain'&&<button disabled={busy||p.disabled} onClick={()=>void run(()=>client!.retry())}>{de?'Unbestätigtes Speichern exakt wiederholen':'Retry unconfirmed save exactly'}</button>}
 {(!base||client?.status==='conflict')&&<button disabled={busy||p.disabled||client?.unresolved} onClick={()=>void run(()=>client!.read())}>{de?'Servervorlage laden und lokalen Entwurf ersetzen':'Load server template and replace local draft'}</button>}
 <p role="status">{busy?(de?'Vorlage wird übertragen.':'Transferring template.'):client?.status==='uncertain'?(de?'Speichern unbestätigt; nur derselbe Versuch darf wiederholt werden.':'Save unconfirmed; only the same request may be retried.'):client?.status==='conflict'?(de?'Revisionskonflikt. Servervorlage ausdrücklich neu laden.':'Revision conflict. Explicitly reload the server template.'):dirty?(de?'Ungespeicherter Entwurf.':'Unsaved draft.'):(de?'Keine ungespeicherten Änderungen.':'No unsaved changes.')}</p>
 {error&&<p role="alert">{de?'Vorlage nicht bestätigt. Prüfe Regeln, Sitzung und Speicherstatus.':'Template not confirmed. Check rules, session and save status.'}</p>}
 </section>;
}
