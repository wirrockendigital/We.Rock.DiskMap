import {RegionalTime} from './regional-format';
import {useEffect,useId,useRef,useState} from 'react';
import type {Scope} from './contracts';
import {SessionDenied} from './session';
import {checkedScopeCheck,checkedScopeInventory,emptySelection,normalizedSelection,scopeSelectionKey,selectionCheckState,toggleSelection,type CheckState,type ScopeCheck,type ScopeInventory,type ScopeSelectionState} from './scope-selection';

export const scopeSelectionText={
 de:{title:'Scan-Bereich auswählen',refresh:'Bereiche neu erfassen',loading:'Bereiche werden geprüft …',failed:'Die Bereichsinformation ist nicht verfügbar. Eine fehlgeschlagene Prüfung bedeutet nicht, dass ein Bereich leer ist.',empty:'Keine lokalen Datenvolumes erkannt.',nas:'Gesamtes NAS',dynamic:'Gesamtes NAS berücksichtigt bei folgenden Läufen neu erkannte lokale Datenvolumes im Rahmen vorhandener Leserechte. Externe und entfernte Mounts gehören nicht automatisch dazu.',fixed:'Explizit ausgewählte Volumes und Freigaben bleiben eine feste Auswahl.',volume:'Ganzes Volume',sharesOnly:'Alle angezeigten Freigaben sind nicht dasselbe wie das ganze Volume: Weitere Bereiche außerhalb von Freigaben bleiben dabei ungewählt.',unmapped:'Freigaben ohne bestätigte Volume-Zuordnung',summary:'Normalisierte Auswahl',none:'Noch kein Bereich ausgewählt.',missing:'Eine ausgewählte Identität ist nicht mehr im aktuellen Inventar. Prüfe die Auswahl und setze sie bei Bedarf zurück; eine andere Identität wird nicht über den Namen zugeordnet.',reset:'Auswahl zurücksetzen',reduced:'Die Gesamtauswahl wurde auf ausdrücklich gewählte Bereiche reduziert. Nach Abwahl einer Freigabe sind weitere Bereiche außerhalb der verbleibenden Freigaben dieses Volumes nicht mehr enthalten.',check:'Lesbarkeit der gewählten Wurzeln prüfen',checking:'Wurzeln werden geprüft …',unverified:'Für diese Auswahl liegt noch keine aktuelle Wurzelvorprüfung vor.',readable:'Wurzel lesbar',checked:'Zeitpunkt der Wurzelvorprüfung',limit:'Die Vorprüfung prüft nur die ausgewählten Wurzeln. Unterordner können andere Rechte haben oder sich später ändern; erst der Scan erfasst diese Fehler.',help:'Bei fehlenden Leserechten: Prüfe in DSM die Leseberechtigung der betroffenen Freigabe für den systeminternen DiskMap-Paketbenutzer. Vergib nur erforderliche Leserechte. Diese App ändert keine Berechtigungen. Bei gesperrten oder fehlenden Zielen prüfe deren Verfügbarkeit in DSM.',overlap:'Freigaben innerhalb eines gewählten ganzen Volumes werden nur einmal erfasst.',draft:'Diese Auswahl ist eine Vorschau. Sie speichert kein Profil und startet keinen Scan.',states:{reachable:'Erreichbar',unmounted:'Nicht eingehängt',locked:'Gesperrt',unreadable:'Nicht lesbar',missing:'Fehlend',unverified:'Nicht verifiziert'}},
 en:{title:'Select scan scope',refresh:'Refresh available scopes',loading:'Checking available scopes …',failed:'Scope information is unavailable. A failed check does not mean a scope is empty.',empty:'No local data volumes detected.',nas:'Entire NAS',dynamic:'Entire NAS includes newly discovered local data volumes on subsequent runs within existing read permissions. External and remote mounts are not automatically included.',fixed:'Explicitly selected volumes and shares remain a fixed selection.',volume:'Whole volume',sharesOnly:'All listed shares are not the same as the whole volume: Other areas outside shares remain unselected.',unmapped:'Shares without a verified volume mapping',summary:'Normalized selection',none:'No scope selected yet.',missing:'A selected identity is no longer in the current inventory. Review or reset the selection; a different identity is never matched by name.',reset:'Reset selection',reduced:'The whole-scope selection was reduced to explicit scopes. After deselecting a share, other areas outside the remaining shares on that volume are no longer included.',check:'Check selected root readability',checking:'Checking roots …',unverified:'No current root precheck is available for this selection.',readable:'Root readable',checked:'Root precheck time',limit:'The precheck checks selected roots only. Subfolders can have different permissions or change later; the scan records those errors.',help:'For missing read permissions: Check the affected shared folder’s read permission for the internal DiskMap package user in DSM. Grant only the required read access. This app does not change permissions. For locked or missing targets, check their availability in DSM.',overlap:'Shares inside a selected whole volume are scanned only once.',draft:'This selection is a preview. It does not save a profile or start a scan.',states:{reachable:'Reachable',unmounted:'Not mounted',locked:'Locked',unreadable:'Not readable',missing:'Missing',unverified:'Unverified'}}
};
interface Props {
 language:'de'|'en';
 loadInventory:(signal:AbortSignal)=>Promise<ScopeInventory>;
 checkScope:(scope:Scope,signal:AbortSignal)=>Promise<ScopeCheck>;
 value?:Scope|null;
 disabled?:boolean;
 editorIdentity?:string;
 onChange?:(scope:Scope|null)=>void;
 onSessionDenied:()=>void;
}
// Native checkboxes provide Space/Tab behavior; indeterminate is both visual and accessible.
export function ScopeCheckbox({state,label,onChange,disabled=false}:{state:CheckState;label:string;onChange:(checked:boolean)=>void;disabled?:boolean}){
 const input=useRef<HTMLInputElement>(null);const id=useId();
 useEffect(()=>{if(input.current)input.current.indeterminate=state==='mixed';},[state]);
 return <label htmlFor={id} style={{overflowWrap:'anywhere'}}><input ref={input} id={id} type="checkbox" checked={state==='checked'} aria-checked={state==='mixed'?'mixed':state==='checked'} disabled={disabled} onChange={e=>onChange(e.target.checked)}/>{label}</label>;
}
// Discovery and precheck use the existing authenticated service, injected by the composition owner.
export function ScopeSelection(props:Props){return <ScopeSelectionBody key={props.editorIdentity??'standalone'} {...props}/>;}
// Controlled values are the single authoritative draft; inventory refresh never clears them.
export function ScopeSelectionBody({language,loadInventory,checkScope,onChange,onSessionDenied,value,disabled=false}:Props){
 const t=scopeSelectionText[language];const[inventory,setInventory]=useState<ScopeInventory|null>(null);
 const[localSelection,setSelection]=useState<ScopeSelectionState>(emptySelection);
 const controlled=value!==undefined;
 const selection:ScopeSelectionState=controlled?selectionFromScope(value??null,inventory):localSelection;const[report,setReport]=useState<ScopeCheck|null>(null);
 const[busy,setBusy]=useState<'inventory'|'check'|null>(null);const[failed,setFailed]=useState(false);const[reduced,setReduced]=useState(false);
 const request=useRef<AbortController|null>(null);const sequence=useRef(0);
 let scope:Scope|null=null,invalid=false;if(inventory){try{scope=normalizedSelection(selection,inventory);}catch{invalid=true;}}
 const invalidate=()=>{request.current?.abort();sequence.current++;setReport(null);setFailed(false);setBusy(null);};
 useEffect(()=>()=>{request.current?.abort();sequence.current++;},[]);
 useEffect(()=>{invalidate();},[value===undefined?'uncontrolled':JSON.stringify(value),disabled]);
 // A refresh never silently drops selected identities; unresolved selections require an explicit reset.
 const refresh=async()=>{
  if(disabled)return;invalidate();const c=new AbortController();request.current=c;const ticket=sequence.current;setBusy('inventory');setInventory(null);if(!controlled)onChange?.(null);
  try{const data=checkedScopeInventory(await loadInventory(c.signal));if(c.signal.aborted||ticket!==sequence.current)return;setInventory(data);if(!controlled){try{onChange?.(normalizedSelection(selection,data));}catch{onChange?.(null);}}}
  catch(e){if(!c.signal.aborted&&ticket===sequence.current){if(e instanceof SessionDenied){setInventory(null);setSelection(emptySelection());onSessionDenied();}else setFailed(true);}}
  finally{if(!c.signal.aborted&&ticket===sequence.current)setBusy(null);}
 };
 // Each selection change invalidates the previous result even when transport cancellation arrives late.
 const toggle=(kind:'nas'|'volume'|'share',target:string,checked:boolean)=>{
  if(!inventory||disabled||invalid)return;invalidate();const result=toggleSelection(selection,inventory,kind,target,checked);if(!controlled)setSelection(result.selection);setReduced(result.reduced);try{onChange?.(normalizedSelection(result.selection,inventory));}catch{onChange?.(null);}
 };
 const reset=()=>{if(disabled)return;invalidate();setSelection(emptySelection());setReduced(false);onChange?.(null);};
 const check=async()=>{
  if(!scope||!inventory||disabled)return;invalidate();const c=new AbortController();request.current=c;const ticket=sequence.current,captured=scope;setBusy('check');
  try{const data=checkedScopeCheck(await checkScope(captured,c.signal),captured,inventory);if(!c.signal.aborted&&ticket===sequence.current)setReport(data);}
  catch(e){if(!c.signal.aborted&&ticket===sequence.current){if(e instanceof SessionDenied){setInventory(null);setSelection(emptySelection());onChange?.(null);onSessionDenied();}else setFailed(true);}}
  finally{if(!c.signal.aborted&&ticket===sequence.current)setBusy(null);}
 };
 const name=(id:string)=>inventory?.volumes.find(x=>x.id===id)?.name??inventory?.shares.find(x=>x.id===id)?.name??id;
 const checkedReport=scope&&report&&scopeSelectionKey(scope)===scopeSelectionKey(report.scope)?report:null;
 const shareRow=(s:ScopeInventory['shares'][number])=><li key={s.id}><ScopeCheckbox state={selectionCheckState(selection,inventory!,'share',s.id)} label={`${s.name} — ${t.states[s.state]}`} onChange={checked=>toggle('share',s.id,checked)}/></li>;
 return <section aria-label={t.title} style={{minWidth:0,overflowWrap:'anywhere'}}>
  <h3>{t.title}</h3>{controlled&&value&&value.targetIds.length>0&&!inventory&&<ul>{value.targetIds.map(id=><li key={id}>{id}</li>)}</ul>}<p>{t.draft}</p><button type="button" onClick={()=>void refresh()} disabled={disabled||busy==='inventory'}>{busy==='inventory'?t.loading:t.refresh}</button>
  {failed&&<p role="alert">{t.failed}</p>}
  {inventory&&<><p>{inventory.observedAt}</p><fieldset disabled={disabled||invalid}><legend>{t.title}</legend>
   <ScopeCheckbox state={selectionCheckState(selection,inventory,'nas')} label={t.nas} onChange={checked=>toggle('nas','',checked)}/>
   <ul>{inventory.volumes.map(v=><li key={v.id}><ScopeCheckbox state={selectionCheckState(selection,inventory,'volume',v.id)} label={`${v.name} — ${t.volume} — ${t.states[v.state]}`} onChange={checked=>toggle('volume',v.id,checked)}/><ul>{inventory.shares.filter(s=>s.volumeId===v.id).map(shareRow)}</ul></li>)}</ul>
   {inventory.volumes.length===0&&<p>{t.empty}</p>}
   {inventory.shares.some(s=>s.volumeId===null)&&<><h4>{t.unmapped}</h4><ul>{inventory.shares.filter(s=>s.volumeId===null).map(shareRow)}</ul></>}
  </fieldset><p>{selection.nas?t.dynamic:t.fixed}</p><p>{t.sharesOnly}</p><p>{t.overlap}</p>
  {reduced&&<p role="status">{t.reduced}</p>}<h4>{t.summary}</h4>
  {invalid?<div role="alert"><p>{t.missing}</p><ul>{value?.targetIds.map(id=><li key={id}>{name(id)}</li>)}</ul></div>:scope?<ul>{scope.kind==='nas'?<li>{t.nas}</li>:scope.targetIds.map(id=><li key={id}>{name(id)}{inventory.volumes.some(v=>v.id===id)?` — ${t.volume}`:''}</li>)}</ul>:<p>{t.none}</p>}
  <button type="button" onClick={reset} disabled={disabled}>{t.reset}</button><button type="button" onClick={()=>void check()} disabled={disabled||!scope||invalid||busy!==null}>{busy==='check'?t.checking:t.check}</button>
  {checkedReport?<div role="status"><p>{t.checked}: <RegionalTime value={checkedReport.checkedAt} language={language}/></p><ul>{checkedReport.roots.map(r=><li key={r.targetId}>{name(r.targetId)} — {r.state==='reachable'?t.readable:t.states[r.state]}</li>)}</ul>{checkedReport.issues.length>0&&<ul>{checkedReport.issues.map((x,n)=><li key={`${x.targetId}-${n}`}>{name(x.targetId)} — {t.states[x.state]}</li>)}</ul>}</div>:<p>{t.unverified}</p>}
  <p>{t.limit}</p><p>{t.help}</p></>}
 </section>;
}

// Missing typed IDs remain in the selection and force explicit recovery rather than name-based replacement.
export function selectionFromScope(scope:Scope|null,inventory:ScopeInventory|null):ScopeSelectionState {
 if(!scope)return emptySelection();if(scope.kind==='nas')return {nas:true,volumeIds:[],shareIds:[]};
 const volumeIds=scope.targetIds.filter(id=>scope.kind==='volumes'||scope.kind==='mixed'&&inventory?.volumes.some(v=>v.id===id));
 return {nas:false,volumeIds,shareIds:scope.targetIds.filter(id=>!volumeIds.includes(id))};
}
