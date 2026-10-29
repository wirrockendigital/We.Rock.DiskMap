import {useEffect,useRef,useState} from 'react';
import type {UserPreference} from './contracts';
export type RegionalChoice=Pick<UserPreference,'language'|'locale'|'timeZone'>;
// A local draft never persists until Save; reset changes only this named regional group.
export function RegionalSettings({value,language,disabled,onSave,onDirty,nasTimeZone}:{value:UserPreference|null;language:'de'|'en';disabled:boolean;onSave:(change:RegionalChoice)=>Promise<void>;onDirty:(dirty:boolean)=>void;nasTimeZone:string|null}) {
 const choice=(v:UserPreference|null):RegionalChoice=>({language:v?.language??'auto',locale:v?.locale??null,timeZone:v?.timeZone??null});
 const [draft,setDraft]=useState<RegionalChoice>(()=>choice(value)),[notice,setNotice]=useState(false);
 const de=language==='de',dirty=JSON.stringify(draft)!==JSON.stringify(choice(value));
 // Unrelated preference revisions must not discard an unsaved regional draft.
 const baseline=useRef(JSON.stringify(choice(value)));
 useEffect(()=>{const next=JSON.stringify(choice(value));if(next!==baseline.current){setDraft(choice(value));setNotice(false);baseline.current=next;}},[value?.language,value?.locale,value?.timeZone]);
 useEffect(()=>{onDirty(dirty);return()=>onDirty(false);},[dirty,onDirty]);
 const set=(next:RegionalChoice)=>{if(!disabled){setDraft(next);setNotice(false);}};
 return <section className="workspace-panel settings-panel regional-settings"><h3>{de?'Persönliche Sprache und Formate':'Personal language and formats'}</h3>
 <p>{de?'Gilt nur für dein DSM-Konto. Andere Einstellungen, Profile und Datenstände bleiben erhalten.':'Applies only to your DSM account. Other preferences, profiles and snapshots are preserved.'}</p>
 <label>{de?'Sprache':'Language'} <select disabled={disabled||!value} value={draft.language} onChange={e=>set({...draft,language:e.target.value as RegionalChoice['language']})}><option value="auto">{de?'Automatisch':'Automatic'}</option><option value="de">Deutsch</option><option value="en">English</option></select></label>
 <label>{de?'Zahlen- und Datumsformat':'Number and date format'} <select disabled={disabled||!value} value={draft.locale??''} onChange={e=>set({...draft,locale:e.target.value||null})}><option value="">{de?'Nach Sprache':'Follow language'}</option><option value="de-DE">Deutsch (Deutschland)</option><option value="en-US">English (US)</option><option value="en-GB">English (UK)</option></select></label>
 <label>{de?'Persönliche IANA-Zeitzone (leer: NAS)':'Personal IANA time zone (empty: NAS)'} <input disabled={disabled||!value} maxLength={128} placeholder="Europe/Amsterdam" value={draft.timeZone??''} onChange={e=>set({...draft,timeZone:e.target.value||null})}/></label>
 <p>{nasTimeZone?(de?'NAS-Zeitzone: ':'NAS time zone: ')+nasTimeZone:(de?'NAS-Zeitzone nicht verfügbar. Ohne persönliche Auswahl wird UTC ausdrücklich angezeigt.':'NAS time zone unavailable. Without a personal choice, UTC is shown explicitly.')}</p>
 <p>{de?'Automatische Sprache: DSM nur bei qualifiziertem Signal, sonst Browser, sonst Englisch. Zeitplanung und Sommerzeitregeln verwaltet DSM.':'Automatic language: qualified DSM signal, otherwise browser, otherwise English. DSM manages scheduling and daylight-saving rules.'}</p>
 <button disabled={disabled||!value||!dirty} onClick={()=>void onSave(draft).catch(()=>setNotice(true))}>{de?'Speichern':'Save'}</button>
 <button disabled={disabled||!dirty} onClick={()=>set(choice(value))}>{de?'Verwerfen':'Discard'}</button>
 <button disabled={disabled||!value} onClick={()=>set({language:'auto',locale:null,timeZone:null})}>{de?'Sprache und Formate auf Standardwerte setzen':'Reset language and formats to defaults'}</button>
 {notice&&<p role="alert">{de?'Speichern nicht bestätigt. Prüfe die Eingabe oder den Speicherstatus und wiederhole die Änderung.':'Saving is not confirmed. Check the input or save status and retry the change.'}</p>}
 </section>;
}
