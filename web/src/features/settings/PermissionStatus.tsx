import {useEffect,useRef,useState} from 'react';
import type {Scope} from '../../contracts';
import {RegionalTime} from '../../regional-format';
import {loadScopeInventory,checkSelectedScope} from '../../scope-api';
import {checkedScopeInventory,checkedScopeCheck,scopeSelectionKey,type ScopeInventory,type ScopeCheck,type ScopeState} from '../../scope-selection';
import {SessionDenied} from '../../session';
const names:Record<ScopeState,[string,string]>={reachable:['Wurzel lesbar','Root readable'],unreadable:['Nicht lesbar','Unreadable'],missing:['Fehlt','Missing'],locked:['Gesperrt','Locked'],unmounted:['Nicht eingebunden','Unmounted'],unverified:['Nicht bestätigt','Unverified']};
// Observations are explicit root probes, never recursive scans or permission changes.
export function PermissionStatus({language,scope,identity,disabled,onSessionDenied}:{language:'de'|'en';scope:Scope;identity:string;disabled:boolean;onSessionDenied:()=>void}){
 const key=identity+':'+scopeSelectionKey(scope),de=language==='de';
 const [result,setResult]=useState<{key:string;inventory:ScopeInventory;check:ScopeCheck}|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(false),[page,setPage]=useState(0);
 const active=useRef<AbortController|null>(null),current=useRef(key),callbacks=useRef(onSessionDenied);current.current=key;callbacks.current=onSessionDenied;
 useEffect(()=>{setResult(null);setError(false);setBusy(false);setPage(0);return()=>{active.current?.abort();active.current=null;};},[key]);
 const run=async()=>{
  if(disabled||busy)return;active.current?.abort();const c=new AbortController();active.current=c;setBusy(true);setError(false);setResult(null);setPage(0);
  // A bounded deadline also settles transports that ignore cancellation; late results have no effects.
  let timedOut=false;const timer=setTimeout(()=>{timedOut=true;c.abort();},10000);let abort=()=>{};const timeout=new Promise<never>((_,reject)=>{abort=()=>reject(new Error('PERMISSION_ABORTED'));c.signal.addEventListener('abort',abort,{once:true});});
  try{const value=await Promise.race([(async()=>{const inventory=checkedScopeInventory(await loadScopeInventory(c.signal));c.signal.throwIfAborted();const check=checkedScopeCheck(await checkSelectedScope(scope,c.signal),scope,inventory);return{key,inventory,check};})(),timeout]);if(current.current===key&&active.current===c&&!c.signal.aborted)setResult(value);}
  catch(e){if(current.current===key&&active.current===c){if(!c.signal.aborted&&e instanceof SessionDenied)callbacks.current();else if(!c.signal.aborted||timedOut)setError(true);}}
  finally{clearTimeout(timer);c.signal.removeEventListener('abort',abort);if(current.current===key&&active.current===c)setBusy(false);}
 };
 const value=result?.key===key?result:null,rows=value?.check.roots??[],pages=Math.max(1,Math.ceil(rows.length/50)),shown=Math.min(page,pages-1);
 return <section className="workspace-panel settings-panel permission-settings" aria-label={de?'Berechtigungen des Scan-Profils':'Scan profile permissions'}><h3>{de?'Berechtigungen des Scan-Profils':'Scan profile permissions'}</h3>
 <p>{de?'Prüft die aktuell gespeicherten Profilwurzeln. Eine lesbare Wurzel garantiert keine lesbaren Unterordner. Die Analyse bleibt auf ihrem gespeicherten Datenstand.':'Checks the currently saved profile roots. A readable root does not guarantee readable descendants. The analysis remains on its recorded snapshot.'}</p>
 <button disabled={disabled||busy} onClick={()=>void run()}>{de?'Wurzelzugriff prüfen':'Check root access'}</button>
 <p>{de?'Letzte Prüfung:':'Last check:'} {value?<RegionalTime value={value.check.checkedAt} language={language}/>:de?'Nicht bestätigt':'Unverified'}</p>
 {busy&&<p role="status">{de?'Wurzeln werden geprüft.':'Checking roots.'}</p>}{error&&<p role="alert">{de?'Prüfung nicht verfügbar. Es wird keine Lesbarkeit angenommen.':'Check unavailable. Readability is not assumed.'}</p>}
 {value&&<><ul>{rows.slice(shown*50,(shown+1)*50).map(root=>{const name=[...value.inventory.volumes,...value.inventory.shares].find(x=>x.id===root.targetId)?.name??root.targetId;return <li key={root.targetId}>{name}: {names[root.state][de?0:1]}</li>;})}</ul>{rows.length===0&&<p>{de?'Keine aktuellen Wurzeln bestätigt.':'No current roots confirmed.'}</p>}{value.check.issues.length>0&&<p role="status">{de?'Weitere Zugriffs- oder Zuordnungsbefunde:':'Additional access or mapping observations:'} {value.check.issues.length}</p>}{pages>1&&<nav aria-label={de?'Wurzelseiten':'Root pages'}><button disabled={disabled||busy||shown===0} onClick={()=>setPage(shown-1)}>{de?'Zurück':'Previous'}</button><span>{shown+1} / {pages}</span><button disabled={disabled||busy||shown+1===pages} onClick={()=>setPage(shown+1)}>{de?'Weiter':'Next'}</button></nav>}</>}
 <p>{de?'Prüfe bei fehlendem Zugriff die gezielten Leserechte des Paketdienstes auf die betroffene Freigabe in DSM. Gesperrte oder nicht eingebundene Ziele zuerst dort prüfen. Diese App vergibt keine pauschalen ACL- oder Administratorrechte.':'For denied access, check the package service’s specific read permissions on the affected share in DSM. Check locked or unmounted targets there first. This app grants no blanket ACL or administrator permissions.'}</p>
 </section>;
}
