import {RegionalTime} from './regional-format';
import {useEffect,useRef,useState} from 'react';
import {currentToken,SessionDenied} from './session';

const ids=/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const states=['queued','preflight','scanning','paused','aggregating','validating','publishing','completed','failed','cancelled','interrupted'];
export interface SchedulerReceipt {profileId:string;jobId:string;createdAt:string;state:string;trigger:'cli'|'scheduler';snapshotId:string|null;quality:'complete'|'partial'|'none'}
// Only stored, profile-bound local invocations qualify as observed receipts.
export function checkedSchedulerReceipt(value:unknown,profileId:string,jobId:string):SchedulerReceipt {
 const o=value as {job:{id:string;profileId:string;trigger:string;state:string;createdAt:string;resultSnapshotId:string|null};snapshotState:string};const j=o?.job;
 if(!j||!ids.test(jobId)||j.id!==jobId||j.profileId!==profileId||!['cli','scheduler'].includes(j.trigger)||!states.includes(j.state)||typeof j.createdAt!=='string'||!Number.isFinite(Date.parse(j.createdAt))||(j.resultSnapshotId!==null&&(typeof j.resultSnapshotId!=='string'||!ids.test(j.resultSnapshotId))))throw new Error('INVALID_RECEIPT');
 const quality=j.state==='completed'&&j.resultSnapshotId!==null?(o.snapshotState==='complete'?'complete':o.snapshotState==='partial'?'partial':'none'):'none';
 return {profileId,jobId,createdAt:j.createdAt,state:j.state,trigger:j.trigger as 'cli'|'scheduler',snapshotId:quality==='none'?null:j.resultSnapshotId,quality};
}
// A bounded history is an observation window, never a query of DSM task configuration.
export function latestLocalInvocation(value:unknown,profileId:string):string|null {
 const d=value as {jobs:{jobId:string;profileId:string;trigger:string;createdAt:string}[];truncated:boolean};
 if(!d||!Array.isArray(d.jobs)||d.jobs.length>50||typeof d.truncated!=='boolean')throw new Error('INVALID_HISTORY');
 let latest:typeof d.jobs[number]|null=null;
 for(const j of d.jobs){if(!j||j.profileId!==profileId||typeof j.jobId!=='string'||!ids.test(j.jobId)||!['cli','ui','scheduler'].includes(j.trigger)||typeof j.createdAt!=='string'||!Number.isFinite(Date.parse(j.createdAt)))throw new Error('INVALID_HISTORY');if(j.trigger!=='ui'&&(!latest||Date.parse(j.createdAt)>Date.parse(latest.createdAt)))latest=j;}
 return latest?.jobId??null;
}
// Responses are bounded before JSON parsing and session rejection never exposes their bodies.
export async function schedulerObservationRead(action:string,signal:AbortSignal):Promise<unknown>{
 const token=currentToken();if(!token)throw new SessionDenied();
 const r=await fetch(`./api.cgi?action=${action}`,{credentials:'same-origin',cache:'no-store',redirect:'error',signal,headers:{Accept:'application/json','X-SYNO-TOKEN':token}});
 if(r.status===401||r.status===403)throw new SessionDenied();if(!r.ok||!r.body)throw new Error('OBSERVATION_UNAVAILABLE');
 const reader=r.body.getReader();const chunks:Uint8Array[]=[];let bytes=0;
 try{for(;;){const p=await reader.read();if(p.done)break;bytes+=p.value.byteLength;if(bytes>65536)throw new Error('OBSERVATION_LIMIT');chunks.push(p.value);}}finally{await reader.cancel();reader.releaseLock();}
 const raw=new Uint8Array(bytes);let offset=0;for(const part of chunks){raw.set(part,offset);offset+=part.byteLength;}
 const body=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));if(body.contractVersion!==1)throw new Error('INVALID_OBSERVATION');return body.data;
}
export const schedulerGuideText={de:{
 title:'Einrichtung und Test · DSM 7.4.1 (Build 90080)',steps:[
 'Öffne Systemsteuerung → Aufgabenplaner → Erstellen → Geplante Aufgabe → Benutzerdefiniertes Skript.',
 'Allgemein: Vergib einen verständlichen Aufgabennamen, zum Beispiel „DiskMap – Speicher aktualisieren“. Wähle root für den lokalen Aufgabenaufruf. Der laufende DiskMap-Dienst scannt mit dem eingeschränkten Paketkonto; root durchsucht keine Dateien.',
 'Zeitplan: Wähle die gewünschten Tage und die Uhrzeit. Prüfe die Zeitzone des NAS. Sommerzeit und Zeitzonenwechsel steuert DSM; DiskMap berechnet keinen eigenen nächsten Termin.',
 'Aufgabeneinstellungen: Füge den vollständig kopierten Aufruf unverändert in „Benutzerdefiniertes Skript“ ein. Es sind keine eigenen Shell-Befehle, Passwörter oder Sitzungstokens erforderlich.',
 'Speichere die Aufgabe. Kontrolliere ihre Aktivierung im DSM-Aufgabenplaner; das Kopieren der Vorlage aktiviert keine Aufgabe.',
 'Merke unten den Testbeginn. Wähle die Aufgabe in DSM aus und klicke „Ausführen“. Bestätige den Test in DSM und prüfe danach hier den empfangenen Auftrag sowie sein Ergebnis.',
 'Wiederhole „Ausführen“ bei geschlossener DiskMap-Oberfläche. Öffne sie anschließend erneut und aktualisiere die Beobachtung. Das Paket muss laufen; ein gestopptes Paket wird nicht automatisch gestartet.'
 ],mark:'Testbeginn merken',marked:'Testbeginn (Zeit dieses Browsers)',refresh:'Beobachtung aktualisieren',observed:'Letzter beobachteter lokaler Aufruf',none:'In den letzten höchstens 50 Aufträgen wurde kein lokaler Aufruf gefunden.',unknown:'Noch keine Beobachtung geladen.',error:'Die Beobachtung ist gerade nicht verfügbar. Prüfe, ob das Paket läuft, und versuche es erneut.',old:'Dieser Auftrag liegt vor dem gemerkten Testbeginn und bestätigt den neuen Test nicht.',new:'Der Auftrag wurde nach dem gemerkten Zeitpunkt erfasst. Vergleiche ihn mit deinem DSM-Test; eine Zuordnung zur konkreten DSM-Aufgabe wird nicht behauptet.',clock:'Die Testmarke verwendet die Browserzeit. Prüfe bei abweichenden NAS-Zeiten zusätzlich die angezeigte Auftrags-ID.',complete:'Vollständiger Datenstand veröffentlicht',partial:'Mit Erfassungslücken beendet; kein neuer vollständiger Datenstand bestätigt',pending:'Empfangen; Ergebnis noch nicht vollständig veröffentlicht',failed:'Beendet ohne bestätigten vollständigen Datenstand. Prüfe den Auftragsverlauf und das DSM-Ausführungsergebnis.',limits:'Dies ist eine historische Beobachtung. Sie bestätigt weder die Existenz noch die Aktivierung oder den nächsten Termin eines DSM-Zeitplans. Ein CLI-Aufruf kann auch manuell ausgelöst worden sein.',job:'Auftrag',snapshot:'Datenstand',phases:['Wartend','Vorprüfung','Erfassung','Pausiert','Aggregation','Validierung','Veröffentlichung','Abgeschlossen','Fehlgeschlagen','Abgebrochen','Unterbrochen']
 },en:{title:'Setup and test · DSM 7.4.1 (build 90080)',steps:[
 'Open Control Panel → Task Scheduler → Create → Scheduled Task → User-defined script.',
 'General: Enter a descriptive task name, such as “DiskMap – update storage”. Select root for the local task invocation. The running DiskMap service performs the scan with the restricted package account; root does not scan files.',
 'Schedule: Choose the required days and time. Check the NAS time zone. DSM handles daylight saving and time-zone changes; DiskMap does not calculate a separate next run.',
 'Task Settings: Paste the entire copied command unchanged into “User-defined script”. No custom shell commands, passwords or session tokens are needed.',
 'Save the task. Check its enabled state in DSM Task Scheduler; copying the template does not enable a task.',
 'Mark the test start below. Select the task in DSM and click “Run”. Confirm the test in DSM, then check the received job and its result here.',
 'Repeat “Run” with the DiskMap window closed. Reopen it afterwards and refresh the observation. The package must be running; a stopped package is not started automatically.'
 ],mark:'Mark test start',marked:'Test start (this browser’s clock)',refresh:'Refresh observation',observed:'Last observed local invocation',none:'No local invocation was found among the latest 50 jobs at most.',unknown:'No observation loaded yet.',error:'The observation is currently unavailable. Check that the package is running and try again.',old:'This job predates the marked test start and does not confirm the new test.',new:'This job was recorded after the marked time. Compare it with your DSM test; attribution to a particular DSM task is not asserted.',clock:'The marker uses browser time. If NAS time differs, also check the displayed job ID.',complete:'Complete snapshot published',partial:'Finished with coverage gaps; no new complete snapshot confirmed',pending:'Received; result not fully published yet',failed:'Ended without a confirmed complete snapshot. Check job history and the DSM execution result.',limits:'This is a historical observation. It confirms neither the existence nor the enabled state or next run of a DSM schedule. A CLI invocation may also have been started manually.',job:'Job',snapshot:'Snapshot',phases:['Queued','Preflight','Scanning','Paused','Aggregating','Validating','Publishing','Completed','Failed','Cancelled','Interrupted']}
};
// Manual reads keep setup inert and abort stale observations when the selected profile changes.
export function SchedulerGuide({profileId,language,onSessionDenied}:{profileId:string;language:'de'|'en';onSessionDenied:()=>void}){
 // Local guidance language is transient and never changes the application's global preference.
 const [guideLanguage,setGuideLanguage]=useState<'de'|'en'>(language);
 useEffect(()=>{setGuideLanguage(language);},[language]);
 const t=schedulerGuideText[guideLanguage];const [receipt,setReceipt]=useState<SchedulerReceipt|null>(null);const[status,setStatus]=useState<'unknown'|'none'|'error'|'loaded'>('unknown');const[busy,setBusy]=useState(false);const[marker,setMarker]=useState<string|null>(null);const request=useRef<AbortController|null>(null);const selected=useRef(profileId);selected.current=profileId;
 useEffect(()=>{request.current?.abort();setReceipt(null);setStatus('unknown');setBusy(false);setMarker(null);return()=>request.current?.abort();},[profileId]);
 const refresh=async()=>{request.current?.abort();const c=new AbortController();request.current=c;const id=profileId;setBusy(true);setReceipt(null);setStatus('unknown');try{
 const history=await schedulerObservationRead(`history&profileId=${encodeURIComponent(id)}`,c.signal);if(c.signal.aborted||selected.current!==id)return;const jobId=latestLocalInvocation(history,id);
 const next=jobId?checkedSchedulerReceipt(await schedulerObservationRead(`job&id=${encodeURIComponent(jobId)}`,c.signal),id,jobId):null;
 if(!c.signal.aborted&&selected.current===id){setReceipt(next);setStatus(next?'loaded':'none');}
 }catch(e){if(!c.signal.aborted&&selected.current===id){setReceipt(null);if(e instanceof SessionDenied){setStatus('unknown');setMarker(null);onSessionDenied();}else setStatus('error');}}finally{if(!c.signal.aborted&&selected.current===id)setBusy(false);}};
 const current=receipt?.profileId===profileId?receipt:null;
 return <section><label>{guideLanguage==='de'?'Sprache dieser Anleitung':'Guide language'} <select aria-label="Guide language / Sprache dieser Anleitung" value={guideLanguage} onChange={event=>{const next=event.target.value;if(next==='de'||next==='en')setGuideLanguage(next);}}><option value="de">Deutsch</option><option value="en">English</option></select></label><h4>{t.title}</h4><ol>{t.steps.map(s=><li key={s}>{s}</li>)}</ol><button onClick={()=>setMarker(new Date().toISOString())}>{t.mark}</button>{marker&&<p>{t.marked}: <RegionalTime value={marker} language={guideLanguage}/>. {t.clock}</p>}<button disabled={busy} onClick={()=>void refresh()}>{t.refresh}</button>{status!=='loaded'&&<p role="status">{t[status]}</p>}{current&&<section aria-label={t.observed}><h5>{t.observed}</h5><p><RegionalTime value={current.createdAt} language={guideLanguage}/> · {t.job}: {current.jobId} · {t.phases[states.indexOf(current.state)]}</p>{marker&&<p>{Date.parse(current.createdAt)<Date.parse(marker)?t.old:t.new}</p>}<p>{current.quality==='complete'?t.complete:current.quality==='partial'?t.partial:['completed','failed','cancelled','interrupted'].includes(current.state)?t.failed:t.pending}</p>{current.snapshotId&&<p>{t.snapshot}: {current.snapshotId}</p>}</section>}<p>{t.limits}</p></section>;
}
