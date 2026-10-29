import {useEffect,useRef,useState} from 'react';
import type {Profile,Scope} from './contracts';
import {ExclusionPreview,exclusionIntentKey} from './ExclusionPreview';
import type {Preview,Template} from './exclusion-preview';
import {ScopeSelection} from './ScopeSelection';
import type {ScopeCheck,ScopeInventory} from './scope-selection';
import {SessionDenied} from './session';
import {ProfileActionClient,configurationKey,encodeProfileSave,newProfileConfiguration,profileConfiguration,type ProfileConfiguration} from './profile-actions';

export interface ProfileEditorProps {
 language:'de'|'en';profile:Profile|null;editorIdentity:string;sessionIdentity:string;
 disabled?:boolean;initialConfiguration?:ProfileConfiguration;onDraftChange?:(draft:ProfileConfiguration,validated:boolean)=>void;onSaveConfiguration?:(draft:ProfileConfiguration)=>Promise<Profile>;
 loadInventory:(signal:AbortSignal)=>Promise<ScopeInventory>;checkScope:(scope:Scope,signal:AbortSignal)=>Promise<ScopeCheck>;
 loadProfile:(profileId:string,signal:AbortSignal)=>Promise<Profile|null>;
 onSaved:(profile:Profile,editorIdentity:string)=>void;onCancel:()=>void;onSessionDenied:()=>void;onDirtyChange?:(dirty:boolean)=>void;
}
const words={
 de:{title:'Scan-Profil bearbeiten',create:'Neues Scan-Profil',name:'Name',active:'Profil aktiv',mode:'Ressourcenmodus',gentle:'Schonend',balanced:'Ausgeglichen',fast:'Schnell',fastHelp:'Schnell erzeugt mehr Last. Es bleibt bei einem Scan-Arbeiter; CPU- und Bandbreitenlimits sind nicht verfügbar.',limits:'Ein Scan-Arbeiter. CPU-/Bandbreitenlimits: nicht festgelegt.',fresh:'Aktualitätsziel in Sekunden',rules:'Ausschlüsse',path:'Relativer Pfad',pattern:'Muster',add:'Regel hinzufügen',remove:'Regel entfernen',syntax:'Pfade sind relativ zur Scan-Wurzel. Muster verwenden * und ?, ** als vollständige Pfadkomponente. Ein abschließender / gilt nur für Ordner. Die verbindliche Syntaxprüfung erfolgt beim Speichern.',save:'Speichern',saving:'Wird gespeichert …',discard:'Verwerfen',cancel:'Schließen',retry:'Speichern erneut bestätigen',uncertain:'Der Ausgang ist noch unklar. Wiederhole exakt denselben Auftrag, bevor du den Entwurf veränderst oder schließt.',conflict:'Das Profil wurde geändert oder kann derzeit nicht geändert werden. Lade den aktuellen Stand; deine Änderungen werden nicht automatisch darübergeschrieben.',reload:'Aktuellen Stand übernehmen',dirty:'Du hast ungespeicherte Änderungen. Entwurf behalten oder verwerfen?',keep:'Entwurf behalten',switch:'Verwerfen und wechseln',version:'Bearbeitete Konfigurationsversion',retained:'Bestehende Scans behalten ihre erfasste Konfiguration. Umbenennen behält die technische Profil-ID.',error:'Speichern ist fehlgeschlagen. Prüfe Eingaben, aktuelle Bereiche und Profilversion.',saved:'Profil gespeichert. Es wurde kein Scan gestartet.',large:'Der vollständige Auftrag überschreitet 16.384 UTF-8-Bytes. Reduziere Auswahl oder Regeln; nichts wird automatisch abgeschnitten.',invalid:'Prüfe Name, Bereichsauswahl, Regeln, Ressourcen und Aktualitätsziel.',size:'Auftragsgröße',bytes:'UTF-8-Bytes',scopeHelp:'Fehlende Ziele bleiben sichtbar. Prüfe das aktuelle Inventar vor dem Speichern; die App erteilt keine Leserechte.',old:'Eine neue Profilauswahl oder Version liegt vor.'},
 en:{title:'Edit scan profile',create:'New scan profile',name:'Name',active:'Profile active',mode:'Resource mode',gentle:'Gentle',balanced:'Balanced',fast:'Fast',fastHelp:'Fast causes additional load. One scan worker remains in use; CPU and bandwidth limits are unavailable.',limits:'One scan worker. CPU/bandwidth limits: unset.',fresh:'Freshness target in seconds',rules:'Exclusions',path:'Relative path',pattern:'Pattern',add:'Add rule',remove:'Remove rule',syntax:'Paths are relative to the scan root. Patterns use * and ?, with ** as a complete path component. A trailing / applies only to directories. Authoritative syntax validation occurs when saving.',save:'Save',saving:'Saving …',discard:'Discard',cancel:'Close',retry:'Confirm save again',uncertain:'The outcome is uncertain. Repeat the exact request before changing or closing this draft.',conflict:'The profile changed or cannot currently be changed. Reload the current version; your draft will not overwrite it automatically.',reload:'Adopt current version',dirty:'You have unsaved changes. Keep or discard this draft?',keep:'Keep draft',switch:'Discard and switch',version:'Edited configuration revision',retained:'Existing scans keep their captured configuration. Renaming preserves the technical profile ID.',error:'Save failed. Check inputs, current scopes and the profile revision.',saved:'Profile saved. No scan was started.',large:'The complete request exceeds 16,384 UTF-8 bytes. Reduce the selection or rules; nothing is silently truncated.',invalid:'Check the name, scope selection, rules, resources and freshness target.',size:'Request size',bytes:'UTF-8 bytes',scopeHelp:'Missing targets stay visible. Check the current inventory before saving; the app does not grant read permissions.',old:'A new profile selection or version is available.'}
};
// A session change erases every draft and pending intent; profile changes are handled inside the dirty guard.
export function ProfileEditor(props:ProfileEditorProps){return <EditorSession key={props.sessionIdentity} {...props}/>;}
// Configuration is one owned draft; the controlled scope picker cannot silently replace it on refresh.
export function EditorSession(p:ProfileEditorProps){
 const t=words[p.language],incoming=JSON.stringify([p.editorIdentity,p.profile]);
 const[base,setBase]=useState(()=>p.profile?structuredClone(p.profile):null),[identity,setIdentity]=useState(p.editorIdentity),[accepted,setAccepted]=useState(incoming),[ignored,setIgnored]=useState('');
 const[draft,setDraft]=useState<ProfileConfiguration>(()=>p.profile?profileConfiguration(p.profile):p.initialConfiguration?profileConfiguration(p.initialConfiguration):newProfileConfiguration());
 const[adopted,setAdopted]=useState<Template|null>(null),[checked,setChecked]=useState<{key:string;value:Preview}|null>(null);
 const[status,setStatus]=useState(''),[busy,setBusy]=useState(false),[closeQuestion,setCloseQuestion]=useState(false),[denied,setDenied]=useState(false);
 const client=useRef(new ProfileActionClient()),alive=useRef(true),reloadRequest=useRef<AbortController|null>(null);
 const original=base?profileConfiguration(base):newProfileConfiguration(),dirty=configurationKey(draft)!==configurationKey(original)||adopted!==null;
 const exclusionKey=exclusionIntentKey(base?.id??null,identity,draft.exclusions,adopted),preview=checked?.key===exclusionKey?checked.value:null;
 const needsCheck=adopted!==null||JSON.stringify(draft.exclusions)!==JSON.stringify(original.exclusions);
 const effectiveDraft={...draft,exclusions:preview?.valid?preview.effectiveExclusions:draft.exclusions};
 const ruleBlocked=needsCheck&&!preview?.valid;
 // Wizard persistence receives explicit effective rules only after their preview is qualified.
 useEffect(()=>{p.onDraftChange?.(profileConfiguration(effectiveDraft),!ruleBlocked);},[draft,preview,ruleBlocked,p.onDraftChange]);
 const blocked=!!p.disabled||busy||client.current.unresolved,switched=incoming!==accepted&&incoming!==ignored;
 // Cancellation invalidates late completions without declaring an in-flight server write cancelled.
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;reloadRequest.current?.abort();client.current.dispose();};},[]);
 useEffect(()=>{p.onDirtyChange?.(!denied&&(dirty||blocked));},[dirty,blocked,denied,p.onDirtyChange]);
 const replace=(profile:Profile|null,nextIdentity:string,key:string)=>{client.current.dispose();client.current=new ProfileActionClient();setBase(profile?structuredClone(profile):null);setDraft(profile?profileConfiguration(profile):newProfileConfiguration());setIdentity(nextIdentity);setAccepted(key);setIgnored('');setStatus('');setCloseQuestion(false);setAdopted(null);setChecked(null);};
 useEffect(()=>{if(!denied&&switched&&!dirty&&!blocked)replace(p.profile,p.editorIdentity,incoming);},[incoming,dirty,blocked,switched,denied]);
 let validation='',size:number|null=null;try{const raw=encodeProfileSave(base,effectiveDraft);size=new TextEncoder().encode(raw).length;}catch(e){validation=e instanceof Error?e.message:'INVALID_REQUEST';}
 const change=(patch:Partial<ProfileConfiguration>)=>{if(blocked)return;setDraft(old=>({...old,...patch}));setStatus('');};
 const sessionDenied=()=>{setDenied(true);setDraft(newProfileConfiguration());setBase(null);setAccepted('');setIgnored('');setIdentity('');setStatus('');client.current.dispose();reloadRequest.current?.abort();p.onSessionDenied();};
 const save=async(retry=false)=>{
  if(busy||!retry&&(blocked||ruleBlocked))return;setBusy(true);setStatus('');
  try{const result=await(p.onSaveConfiguration?p.onSaveConfiguration(profileConfiguration(effectiveDraft)):retry?client.current.retry():client.current.save(base,effectiveDraft));if(!alive.current)return;setBase(result);setDraft(profileConfiguration(result));setAdopted(null);setChecked(null);setStatus('saved');p.onSaved(result,identity);}
  catch(e){if(!alive.current)return;if(e instanceof SessionDenied){sessionDenied();}else setStatus(client.current.status==='uncertain'?'uncertain':client.current.status==='conflict'?'conflict':'error');}
  finally{if(alive.current)setBusy(false);}
 };
 // A conflict reload consults the live catalogue, rather than relabeling the old props as current.
 const reload=async()=>{if(!base||blocked)return;const c=new AbortController();reloadRequest.current=c;setBusy(true);try{const current=await p.loadProfile(base.id,c.signal);if(!alive.current||c.signal.aborted)return;if(!current||current.id!==base.id){setStatus('error');return;}replace(current,identity,incoming);}catch(e){if(!alive.current||c.signal.aborted)return;if(e instanceof SessionDenied)sessionDenied();else setStatus('error');}finally{if(alive.current&&!c.signal.aborted)setBusy(false);}};
 const cancel=()=>{if(blocked)return;if(dirty)setCloseQuestion(true);else p.onCancel();};
 if(denied)return null;
 return <section className="workspace-panel profile-editor" aria-label={base?t.title:t.create} style={{minWidth:0,overflowWrap:'anywhere'}}>
  <h2>{base?t.title:t.create}</h2>{base&&<p>{t.version}: {base.revision}</p>}<p>{t.retained}</p>
  {switched&&<div role="alert"><p>{t.old} {t.dirty}</p><button type="button" disabled={blocked} onClick={()=>replace(p.profile,p.editorIdentity,incoming)}>{t.switch}</button><button type="button" disabled={blocked} onClick={()=>setIgnored(incoming)}>{t.keep}</button></div>}
  <form onSubmit={e=>{e.preventDefault();if(!validation&&!ruleBlocked)void save();}}>
   <fieldset disabled={blocked}><legend>{t.title}</legend>
    <label>{t.name}<input value={draft.name} onChange={e=>change({name:e.target.value})}/></label>
    <label><input type="checkbox" checked={draft.active} onChange={e=>change({active:e.target.checked})}/>{t.active}</label>
    <ScopeSelection language={p.language} value={draft.scope.kind==='shares'&&draft.scope.targetIds.length===0?null:draft.scope} editorIdentity={identity} disabled={blocked} loadInventory={p.loadInventory} checkScope={p.checkScope} onSessionDenied={sessionDenied} onChange={scope=>change({scope:scope??{kind:'shares',targetIds:[]}})}/>
    <p>{t.scopeHelp}</p><label>{t.mode}<select value={draft.resourceMode} onChange={e=>change({resourceMode:e.target.value as Profile['resourceMode'],limits:{workers:1,ioBytesPerSecond:null,cpuPercent:null}})}>{(['gentle','balanced','fast'] as const).map(mode=><option key={mode} value={mode}>{t[mode]}</option>)}</select></label><p>{t.limits}</p>{draft.resourceMode==='fast'&&<p role="note">{t.fastHelp}</p>}
    <label>{t.fresh}<input type="number" min="0" step="1" value={Number.isFinite(draft.freshnessSeconds)?draft.freshnessSeconds:''} onChange={e=>change({freshnessSeconds:e.target.value===''?NaN:Number(e.target.value)})}/></label>
    <fieldset><legend>{t.rules}</legend><p>{t.syntax}</p>{draft.exclusions.map((rule,i)=><div className="workspace-rule" key={i}><label>{t.rules} {i+1}<select value={rule.kind} onChange={e=>change({exclusions:draft.exclusions.map((r,n)=>n===i?{...r,kind:e.target.value as 'path'|'pattern'}:r)})}><option value="path">{t.path}</option><option value="pattern">{t.pattern}</option></select></label><label>{t.rules} {i+1}<input value={rule.value} onChange={e=>change({exclusions:draft.exclusions.map((r,n)=>n===i?{...r,value:e.target.value}:r)})}/></label><button type="button" onClick={()=>change({exclusions:draft.exclusions.filter((_,n)=>n!==i)})}>{t.remove} {i+1}</button></div>)}<button type="button" disabled={draft.exclusions.length>=256} onClick={()=>change({exclusions:[...draft.exclusions,{kind:'path',value:''}]})}>{t.add}</button></fieldset>
    <ExclusionPreview language={p.language} profileId={base?.id??null} identity={identity} local={draft.exclusions} adopted={adopted} disabled={blocked} onAdopt={v=>{if(!blocked){setAdopted(v);setChecked(null);setStatus('');}}} onValidated={(key,value)=>setChecked({key,value})} onSessionDenied={sessionDenied}/>
   </fieldset>
   {validation&&<p role="alert">{validation==='PROFILE_REQUEST_TOO_LARGE'?t.large:t.invalid}</p>}{size!==null&&<p>{t.size}: {size} / 16384 {t.bytes}</p>}
   <button type="submit" disabled={blocked||ruleBlocked||!!validation||!dirty||client.current.status==='conflict'}>{busy?t.saving:t.save}</button><button type="button" disabled={blocked||!dirty} onClick={()=>{setDraft(original);setAdopted(null);setChecked(null);setStatus('');}}>{t.discard}</button><button type="button" disabled={blocked} onClick={cancel}>{t.cancel}</button>
  </form>
  {status==='uncertain'&&<div role="alert"><p>{t.uncertain}</p><button type="button" disabled={busy} onClick={()=>void save(true)}>{t.retry}</button></div>}
  {status==='conflict'&&<div role="alert"><p>{t.conflict}</p><button type="button" disabled={busy} onClick={()=>void reload()}>{t.reload}</button></div>}
  {status==='error'&&<p role="alert">{t.error}</p>}{status==='saved'&&<p role="status">{t.saved}</p>}
  {closeQuestion&&<div role="alert"><p>{t.dirty}</p><button type="button" disabled={blocked} onClick={()=>{if(blocked||client.current.unresolved)return;setDraft(original);p.onCancel();}}>{t.discard}</button><button type="button" disabled={blocked} onClick={()=>{if(blocked||client.current.unresolved)return;setCloseQuestion(false);}}>{t.keep}</button></div>}
 </section>;
}
