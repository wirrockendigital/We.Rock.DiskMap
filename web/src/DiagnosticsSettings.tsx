import { useEffect, useRef, useState } from 'react';
import { currentToken, SessionDenied } from './session';

// Enforce the byte budget before decoding; errors never reflect response content.
export async function readDiagnosticsBody(response:Response):Promise<unknown>{
 const reader=response.body?.getReader();if(!reader)throw new Error('DIAGNOSTICS_UNAVAILABLE');
 const chunks:Uint8Array[]=[];let size=0;
 try{for(;;){const part=await reader.read();if(part.done)break;size+=part.value.byteLength;if(size>65536){await reader.cancel();throw new Error('DIAGNOSTICS_LIMIT');}chunks.push(part.value);}}
 finally{reader.releaseLock();}
 const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.byteLength;}
 return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));
}

// The view accepts only typed operational observations and never renders arbitrary response fields.
export interface DiagnosticsInfo {
 statusVersion:1;observedAt:string;backend:'available'|'stopping'|'busy';phase:string;elapsedMillis:number|null;warnings:number|null;
 queue:{state:'available'|'unavailable';waiting:number|null;capacity:16};
 controlDatabase:{availability:'available'|'unavailable';integrity:'unknown'};
 storage:{state:'available'|'low'|'unavailable';freeBytes:string|null;reserveBytes:string};
 generations:{observation:'recorded'|'unavailable';sampled:number;truncated:boolean;healthy:number;corrupt:number;unsupported:number;unverified:number;deleting:number;deleted:number};
 capabilities:{localAPI:boolean;snapshotReads:boolean;jobControl:boolean;integrityChecks:boolean;scanAvailable:boolean|null;dsmBuild:string|null;dsmFileStation:string;dsmScheduledScan:string};reasonCodes:string[];
 versions:{schema:number;scanner:number;aggregate:number;go:string};
}
const phases=['idle','unknown','queued','preflight','scanning','paused','aggregating','validating','publishing','completed','failed','cancelled','interrupted'];
const integer=(n:unknown):n is number=>Number.isSafeInteger(n)&&Number(n)>=0;
const count=(n:unknown)=>n===null||integer(n);
const bytes=(n:unknown):n is string=>typeof n==='string'&&/^(0|[1-9][0-9]{0,39})$/.test(n);
// Unknown measurements stay null; inconsistent availability/count pairs fail closed.
export function checkedDiagnostics(value:unknown):DiagnosticsInfo{
 const input=value as DiagnosticsInfo;
 // JSON numbers beyond the browser's exact range are unknown measurements, never rounded displays.
 const measured=(n:unknown):unknown=>typeof n==='number'&&Number.isFinite(n)&&Number.isInteger(n)&&n>Number.MAX_SAFE_INTEGER?null:n;
 const d=input&&{...input,elapsedMillis:measured(input.elapsedMillis),warnings:measured(input.warnings)} as DiagnosticsInfo;
 if(!d||d.statusVersion!==1||typeof d.observedAt!=='string'||d.observedAt.length>64||!Number.isFinite(Date.parse(d.observedAt))||!['available','stopping','busy'].includes(d.backend)||!phases.includes(d.phase)||!count(d.elapsedMillis)||!count(d.warnings))throw new Error('INVALID_DIAGNOSTICS');
 if(!d.queue||!['available','unavailable'].includes(d.queue.state)||d.queue.capacity!==16||!count(d.queue.waiting)||(d.queue.waiting!==null&&d.queue.waiting>16)||(d.queue.state==='unavailable'&&d.queue.waiting!==null))throw new Error('INVALID_DIAGNOSTICS');
 if(!d.controlDatabase||!['available','unavailable'].includes(d.controlDatabase.availability)||d.controlDatabase.integrity!=='unknown')throw new Error('INVALID_DIAGNOSTICS');
 if(!d.storage||!['available','low','unavailable'].includes(d.storage.state)||!bytes(d.storage.reserveBytes)||(d.storage.freeBytes!==null&&!bytes(d.storage.freeBytes))||(d.storage.state==='unavailable'&&d.storage.freeBytes!==null))throw new Error('INVALID_DIAGNOSTICS');
 if(!d.generations||!['recorded','unavailable'].includes(d.generations.observation)||typeof d.generations.truncated!=='boolean'||!integer(d.generations.sampled)||d.generations.sampled>128)throw new Error('INVALID_DIAGNOSTICS');
 for(const key of ['healthy','corrupt','unsupported','unverified','deleting','deleted'] as const)if(!integer(d.generations[key])||d.generations[key]>d.generations.sampled)throw new Error('INVALID_DIAGNOSTICS');
 if(!d.versions||![d.versions.schema,d.versions.scanner,d.versions.aggregate].every(integer)||typeof d.versions.go!=='string'||!/^go[0-9]+\.[0-9]+(?:\.[0-9]+)?$/.test(d.versions.go))throw new Error('INVALID_DIAGNOSTICS');
 const c=d.capabilities;
 if(!c||![c.localAPI,c.snapshotReads,c.jobControl,c.integrityChecks].every(x=>typeof x==='boolean')||(c.scanAvailable!==null&&typeof c.scanAvailable!=='boolean')||(c.dsmBuild!==null&&(typeof c.dsmBuild!=='string'||! /^[0-9]{1,10}$/.test(c.dsmBuild)))||![c.dsmFileStation,c.dsmScheduledScan].every(x=>['unknown','unqualified','qualified-probe'].includes(x)))throw new Error('INVALID_DIAGNOSTICS');
 if(!Array.isArray(d.reasonCodes)||d.reasonCodes.length>16||d.reasonCodes.some(x=>!['COMPONENT_BUSY','BACKEND_STOPPING','CONTROL_DATABASE_UNAVAILABLE','QUEUE_UNAVAILABLE','INDEX_SPACE_LOW','INDEX_SPACE_UNAVAILABLE','SCANNER_UNAVAILABLE'].includes(x)))throw new Error('INVALID_DIAGNOSTICS');
 return d;
}
const texts={de:{title:'Dienstdiagnose',refresh:'Status aktualisieren',error:'Diagnose derzeit nicht verfügbar.',unknown:'Unbekannt',backend:'Dienst',phase:'Auftragsphase',elapsed:'Laufzeit (ms)',warnings:'Warnungen',queue:'Wartende Aufträge',database:'Steuerdatenbank erreichbar',integrity:'Datenbankintegrität',storage:'Speicherreserve',free:'Freier Platz',reserve:'Mindestreserve',recorded:'Gespeicherte Prüfergebnisse; keine neue Integritätsprüfung.',limited:'Begrenzte Stichprobe',versions:'Formatversionen',observed:'Beobachtet',healthy:'Gesund',corrupt:'Beschädigt',unsupported:'Nicht unterstützt',unverified:'Ungeprüft',deleting:'Löschung läuft',deleted:'Gelöscht'},en:{title:'Service diagnostics',refresh:'Refresh status',error:'Diagnostics currently unavailable.',unknown:'Unknown',backend:'Service',phase:'Job phase',elapsed:'Elapsed time (ms)',warnings:'Warnings',queue:'Queued jobs',database:'Control database reachable',integrity:'Database integrity',storage:'Storage reserve',free:'Free space',reserve:'Minimum reserve',recorded:'Recorded check results; no new integrity check.',limited:'Bounded sample',versions:'Format versions',observed:'Observed',healthy:'Healthy',corrupt:'Corrupt',unsupported:'Unsupported',unverified:'Unverified',deleting:'Deletion running',deleted:'Deleted'}};
const labels:Record<string,[string,string]>={available:['Verfügbar','Available'],unavailable:['Nicht verfügbar','Unavailable'],stopping:['Wird beendet','Stopping'],busy:['Beschäftigt','Busy'],low:['Niedrig','Low'],idle:['Leerlauf','Idle'],unknown:['Unbekannt','Unknown'],queued:['Wartend','Queued'],preflight:['Vorprüfung','Preflight'],scanning:['Erfassung','Scanning'],paused:['Pausiert','Paused'],aggregating:['Aggregation','Aggregating'],validating:['Validierung','Validating'],publishing:['Veröffentlichung','Publishing'],completed:['Beendet','Completed'],failed:['Fehlgeschlagen','Failed'],cancelled:['Abgebrochen','Cancelled'],interrupted:['Unterbrochen','Interrupted']};
// Manual observation does not poll, mutate state, or interfere with the current analysis view.
export function DiagnosticsSettings({language,onSessionDenied}:{language:'de'|'en';onSessionDenied:()=>void}){
 const t=texts[language];const[data,setData]=useState<DiagnosticsInfo|null>(null);const[busy,setBusy]=useState(false);const[error,setError]=useState(false);const request=useRef<AbortController|null>(null);
 useEffect(()=>()=>{request.current?.abort();},[]);
 const load=async()=>{request.current?.abort();const c=new AbortController();request.current=c;setBusy(true);setError(false);
 try{const token=currentToken();if(!token)throw new SessionDenied();const response=await fetch('./api.cgi?action=diagnostics',{credentials:'same-origin',cache:'no-store',redirect:'error',signal:c.signal,headers:{Accept:'application/json','X-SYNO-TOKEN':token}});if(response.status===401||response.status===403)throw new SessionDenied();if(!response.ok)throw new Error('DIAGNOSTICS_UNAVAILABLE');const envelope=await readDiagnosticsBody(response) as {contractVersion?:unknown;data?:unknown};if(envelope.contractVersion!==1)throw new Error('INVALID_DIAGNOSTICS');const next=checkedDiagnostics(envelope.data);if(!c.signal.aborted)setData(next);
 }catch(e){if(!c.signal.aborted){setData(null);if(e instanceof SessionDenied){onSessionDenied();}else setError(true);}}finally{if(!c.signal.aborted)setBusy(false);}};
 const label=(value:string)=>labels[value]?.[language==='de'?0:1]??t.unknown;
 const capability=(value:string)=>value==='qualified-probe'?(language==='de'?'Begrenzte Probe bestätigt':'Limited probe qualified'):value==='unqualified'?(language==='de'?'Nicht qualifiziert':'Unqualified'):t.unknown;
 const bool=(value:boolean|null)=>value===null?t.unknown:value?(language==='de'?'Ja':'Yes'):(language==='de'?'Nein':'No');
 const number=(value:number|null)=>value===null?t.unknown:String(value);
 return <details className="workspace-panel settings-panel diagnostics-settings"><summary>{t.title}</summary><button disabled={busy} onClick={()=>void load()}>{t.refresh}</button>{error&&<p role="status">{t.error}</p>}{data&&<><dl>{([[t.backend,label(data.backend)],[t.phase,label(data.phase)],[t.elapsed,number(data.elapsedMillis)],[t.warnings,number(data.warnings)],[t.queue,data.queue.waiting===null?t.unknown:`${data.queue.waiting} / ${data.queue.capacity}`],[t.database,label(data.controlDatabase.availability)],[t.integrity,t.unknown],[t.storage,label(data.storage.state)],[t.free,data.storage.freeBytes===null?t.unknown:`${data.storage.freeBytes} B`],[t.reserve,`${data.storage.reserveBytes} B`]] as const).map(([name,value])=><div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}</dl><p>{t.recorded}</p>{data.generations.observation==='unavailable'?<p>{t.unknown}</p>:<><p>{t.limited}: {data.generations.sampled}{data.generations.truncated?' +':''}</p><dl>{(['healthy','corrupt','unsupported','unverified','deleting','deleted'] as const).map(key=><div key={key}><dt>{t[key]}</dt><dd>{data.generations[key]}</dd></div>)}</dl></>}<dl><dt>DSM Build</dt><dd>{data.capabilities.dsmBuild??t.unknown}</dd><dt>File Station</dt><dd>{capability(data.capabilities.dsmFileStation)}</dd><dt>{language==='de'?'Lokaler Aufgabenplaner-Aufruf':'Local scheduled scan'}</dt><dd>{capability(data.capabilities.dsmScheduledScan)}</dd><dt>{language==='de'?'Scan derzeit verfügbar':'Scan currently available'}</dt><dd>{bool(data.capabilities.scanAvailable)}</dd><dt>{language==='de'?'Implementierte Schnittstellen: API / Lesen / Steuerung / Integritätsprüfung':'Implemented interfaces: API / reads / controls / integrity'}</dt><dd>{[data.capabilities.localAPI,data.capabilities.snapshotReads,data.capabilities.jobControl,data.capabilities.integrityChecks].map(bool).join(' / ')}</dd></dl><p>{language==='de'?'Verfügbarkeit ersetzt keine Profil- oder Quellenprüfung.':'Availability does not replace profile or source validation.'}</p><p>{t.versions}: {data.versions.schema} / {data.versions.scanner} / {data.versions.aggregate} · {data.versions.go}</p><p>{t.observed}: {data.observedAt}</p></>}</details>;
}
