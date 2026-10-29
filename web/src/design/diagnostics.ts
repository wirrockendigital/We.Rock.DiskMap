// Diagnostics retain only fixed event/code identifiers; never paths, names, credentials or response bodies.
export const errorMessages:Record<string,[string,string]>={
 FILTER_PREPARATION_TIMEOUT:['Die Filteraufbereitung dauert zu lange. Bitte erneut laden oder den Filter eingrenzen.','Filter preparation timed out. Reload or narrow the filter.'],
 MAP_LAYOUT_FAILED:['Die Dateikarte konnte nicht aufgebaut werden. Der gespeicherte Scan bleibt erhalten. Öffne den Bereich erneut.','The file map could not be built. The saved scan is retained. Reopen the source.'],
 SERVICE_UNAVAILABLE:['Der Dienst ist nicht erreichbar. Bitte lade die Ansicht erneut.','The service is unavailable. Reload the view.'],
 SCOPE_UNAVAILABLE:['Der gewählte Bereich ist nicht erreichbar oder nicht lesbar. Prüfe Volume und Freigaberechte.','The source is unavailable or unreadable. Check the volume and shared-folder permissions.'],
 VIEW_EXPIRED:['Der gespeicherte Ansichtsstand ist abgelaufen. Bitte erneut laden.','The view expired. Reload it.'],
 SNAPSHOT_MISMATCH:['Der Datenstand hat sich geändert. Bitte erneut laden.','The snapshot changed. Reload it.'],
 JOB_CONFLICT:['Für diesen Bereich läuft bereits ein Scan. Warte auf dessen Abschluss.','A scan is already running for this source. Wait for it to finish.'],
 QUEUE_FULL:['Die Scanwarteschlange ist voll. Versuche es nach Abschluss eines Laufs erneut.','The scan queue is full. Retry after a scan finishes.'],
 QUERY_BUDGET_EXCEEDED:['Die Ansicht enthält zu viele Einträge. Öffne einen kleineren Ordner.','This view contains too many entries. Open a smaller folder.'],
 PROFILE_REBIND_REQUIRED:['Die Quelle hat sich geändert. Wähle Volume oder Ordner erneut aus.','The source changed. Select the volume or folder again.'],
 PROFILE_RELOAD_REQUIRED:['Die Scankonfiguration wurde geändert. Bitte erneut laden.','The scan configuration changed. Reload it.'],
 PROFILE_SCOPE_INVALID:['Dieser Scanbereich ist nicht mehr gültig. Wähle ihn erneut aus.','This scan source is no longer valid. Select it again.'],
 INCOMPLETE_COVERAGE:['Scan abgeschlossen, aber nicht vollständig: Einige Bereiche sind nicht lesbar oder liegen außerhalb der Scan-Grenzen.','Scan finished with incomplete coverage: some areas are unreadable or outside the scan boundaries.'],
 SCAN_FAILED:['Der Scan ist fehlgeschlagen. Ein vorhandener gespeicherter Stand bleibt erhalten. Prüfe die Hinweise unter Einstellungen → Debug.','The scan failed. Any previous saved result is retained. See Settings → Debug.'],
 SCAN_CANCELLED:['Der Scan wurde abgebrochen. Ein vorhandener gespeicherter Stand bleibt erhalten.','The scan was cancelled. Any previous saved result is retained.'],
 SCAN_INTERRUPTED:['Der Scan wurde unterbrochen. Starte bei Bedarf einen Rescan.','The scan was interrupted. Start a rescan if needed.'],
 STATUS_UNAVAILABLE:['Der Scanstatus ist vorübergehend nicht abrufbar. Er wird erneut abgefragt.','Scan status is temporarily unavailable and will be retried.'],
};
export const safeErrorCode=(error:unknown):string=>{
 if(error instanceof Error&&/^(INVALID_MAP_|DUPLICATE_MAP_|MAP_)/.test(error.message))return 'MAP_LAYOUT_FAILED';
 const code=error&&typeof error==='object'&&'code' in error?error.code:undefined;
 if(typeof code==='string'&&Object.hasOwn(errorMessages,code))return code;
 return error instanceof Error&&Object.hasOwn(errorMessages,error.message)?error.message:'SERVICE_UNAVAILABLE';
};
export const explainError=(code:string,language:'de'|'en'):string=>(errorMessages[code]??errorMessages.SERVICE_UNAVAILABLE)[language==='de'?0:1];
// Only allowlisted events and numeric observations enter the byte-bounded session log.
export type DebugLevel='info'|'warning'|'error'|'critical';
export type DebugFilter='all'|DebugLevel;
const levels:Record<string,DebugLevel>={DEBUG_ENABLED:'info',SOURCE_OPEN:'info',CACHE_REUSED:'info',SCAN_SUBMITTED:'info',SCAN_COMPLETE:'info',SCAN_PARTIAL:'warning',SCAN_FAILED:'error',REQUEST_FAILED:'error',STATUS_UNAVAILABLE:'warning',STATUS_RECOVERED:'info',SCAN_PHASE:'info',SCAN_PROGRESS:'info',SCAN_COUNTERS_UNCHANGED:'warning',STATE_INTEGRITY_ERROR:'critical',QUERY_TIMING:'info',QUERY_TIMING_FAILED:'warning'};
// Fixed stages and bounded numeric fields keep request traces free of private selectors and URLs.
export type TimingStage='STARTUP'|'READER_OPEN'|'HIERARCHY'|'MAP_MANIFEST'|'MAP_CHUNKS'|'MAP_FIRST_FRAME'|'MAP_BUILD'|'MAP_LAYOUT'|'SCOPE_BATCH';
export interface TimingFields {status?:number;count?:number;attempt?:number;pending?:number;failed?:number}
export type TimingReporter=(stage:TimingStage,millis:number,fields?:TimingFields)=>void;
const timingStages=new Set(['STARTUP','READER_OPEN','HIERARCHY','MAP_MANIFEST','MAP_CHUNKS','MAP_FIRST_FRAME','MAP_BUILD','MAP_LAYOUT','SCOPE_BATCH']);
const phases=new Set(['queued','preflight','scanning','paused','aggregating','validating','publishing','completed','cancelled','failed','interrupted']);
interface LogEntry {text:string;level:DebugLevel;bytes:number}
export class DiagnosticLog {
 enabled=false;limitMB=10;filter:DebugFilter='all';bytes=0;private rows:LogEntry[]=[];private head=0;private installation='default';
 private observation={job:'',state:'',signature:'',changedAt:0,loggedAt:0,warned:false};
 constructor(private changed:()=>void){}
 get count(){return this.rows.length-this.head;}
 // Rendering is bounded independently of retained log capacity; export includes all matching events.
 get entries(){return this.selected().slice(-500).map(e=>e.text);}
 private selected(){return this.rows.slice(this.head).filter(e=>this.filter==='all'||e.level===this.filter);}
 exportText(){return this.selected().map(e=>e.text).join('\n');}
 async configure(signal:AbortSignal){
  try{const n=Number(localStorage.getItem('diskmap.debug.limitMB'));if(Number.isInteger(n)&&n>=1&&n<=100)this.limitMB=n;}catch{}
  try{const r=await fetch('./debug-config.json',{signal,cache:'no-store',credentials:'same-origin'});if(!r.ok)return;const raw=await r.text();if(raw.length>1024)return;const c=JSON.parse(raw);if(typeof c.enabled!=='boolean'||typeof c.installation!=='string'||!/^[a-zA-Z0-9_-]{1,80}$/.test(c.installation))return;this.installation=c.installation;let saved:string|null=null;try{saved=localStorage.getItem('diskmap.debug');}catch{}this.enabled=saved===this.installation+':off'?false:saved===this.installation+':on'?true:c.enabled;if(this.enabled)this.add('DEBUG_ENABLED');else this.changed();}catch{}
 }
 set(enabled:boolean){this.enabled=enabled;this.clear();try{localStorage.setItem('diskmap.debug',this.installation+(enabled?':on':':off'));}catch{}if(enabled)this.add('DEBUG_ENABLED');}
 clear(){this.rows=[];this.head=0;this.bytes=0;this.observation={job:'',state:'',signature:'',changedAt:0,loggedAt:0,warned:false};this.changed();}
 setFilter(value:DebugFilter){if(!['all','info','warning','error','critical'].includes(value))return;this.filter=value;this.changed();}
 setLimit(value:number){if(!Number.isInteger(value)||value<1||value>100)return;this.limitMB=value;try{localStorage.setItem('diskmap.debug.limitMB',String(value));}catch{}this.trim();this.changed();}
 private trim(){while(this.bytes>this.limitMB*1000000&&this.head<this.rows.length)this.bytes-=this.rows[this.head++].bytes;if(this.head>4096){this.rows=this.rows.slice(this.head);this.head=0;}}
 add(event:string,code?:string){this.append(code==='SNAPSHOT_MISMATCH'?'STATE_INTEGRITY_ERROR':event,code&&Object.hasOwn(errorMessages,code)?' '+code:'');}
 // Never serialize caller objects: arbitrary keys, strings, NaN and negative values are discarded.
 timing(stage:TimingStage,millis:number,fields:TimingFields={}){
  if(!timingStages.has(stage)||!Number.isFinite(millis)||millis<0)return;
  const values=['status','count','attempt','pending','failed'].flatMap(key=>{const n=fields[key as keyof TimingFields];return typeof n==='number'&&Number.isSafeInteger(n)&&n>=0&&n<=1000000?[key+'='+n]:[];});
  this.append(fields.failed===1?'QUERY_TIMING_FAILED':'QUERY_TIMING',' stage='+stage+' ms='+Math.min(Math.round(millis),3600000)+(values.length?' '+values.join(' '):''));
 }
 private append(event:string,details:string){if(!this.enabled||!Object.hasOwn(levels,event))return;const level=levels[event],text=new Date().toISOString()+' ['+level.toUpperCase()+'] '+event+details;const bytes=new TextEncoder().encode(text+'\n').byteLength;this.rows.push({text,level,bytes});this.bytes+=bytes;this.trim();this.changed();}
 // Phase transitions are immediate; counters are sampled every ten seconds, including finalization heartbeats.
 observe(job:string,state:string,values:Record<string,unknown>,now=Date.now()){
  if(!this.enabled||!phases.has(state))return;
  const fields=['entries','files','directories','knownLogicalBytes','warnings','elapsedSeconds'];
  const data=fields.flatMap(key=>typeof values[key]==='string'&&/^\d{1,30}$/.test(values[key] as string)?[key+'='+values[key]]:[]).join(' ');
  const signature=fields.filter(k=>k!=='elapsedSeconds').map(k=>values[k]).join('|');
  const old=this.observation,transition=old.job!==job||old.state!==state;
  if(transition){this.append('SCAN_PHASE',' phase='+state);this.observation={job,state,signature,changedAt:now,loggedAt:0,warned:false};
   // Accept only the scanner's fixed diagnostic vocabulary; arbitrary server messages stay private.
   if(state==='failed'&&Array.isArray(values.errorCodes))for(const code of values.errorCodes.slice(0,8))if(typeof code==='string'&&/^SCAN_(ANCESTRY|NODE_WRITE|AGGREGATE|AGGREGATE_FINISH|TRAVERSAL)_FAILED(?:_INVALID_GENERATION|_SQLITE_\d{1,5})?$/.test(code))this.append('SCAN_FAILED',' '+code);
  }
  const o=this.observation;
  if(signature!==o.signature){o.signature=signature;o.changedAt=now;o.warned=false;}
  if(transition||now-o.loggedAt>=10000){this.append('SCAN_PROGRESS',' phase='+state+' '+data);o.loggedAt=now;}
  // Stable scan counters are suspicious only during traversal, not during derived-index calculation.
  if(state==='scanning'&&!o.warned&&now-o.changedAt>=60000){this.append('SCAN_COUNTERS_UNCHANGED',' seconds=60');o.warned=true;}
 }
}
