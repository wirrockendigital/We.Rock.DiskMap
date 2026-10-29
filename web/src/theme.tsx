import {createContext,useContext,useEffect,useState} from 'react';
import {resolveTheme} from './dsm-capabilities';
import type {UserPreference} from './contracts';
export const ThemeContext=createContext<ReturnType<typeof resolveTheme>>({theme:'light',source:'fallback'});
export const useTheme=()=>useContext(ThemeContext);
// Only the confirmed personal choice is applied. DSM has no qualified signal in the current capability matrix.
export function useResolvedTheme(choice:UserPreference['theme']) {
 const [system,setSystem]=useState<boolean|undefined>(undefined);
 useEffect(()=>{if(typeof window.matchMedia!=='function')return;const media=window.matchMedia('(prefers-color-scheme: dark)');
 const update=()=>setSystem(media.matches);update();media.addEventListener('change',update);return()=>media.removeEventListener('change',update);},[]);
 const resolved=resolveTheme(choice,system);
 useEffect(()=>{if(typeof document==='undefined'||!document.documentElement)return;const root=document.documentElement;root.dataset.theme=resolved.theme;
 return()=>{delete root.dataset.theme;};},[resolved.theme]);
 return resolved;
}
// Draft edits do not change paint or persistence before an acknowledged Save.
export function ThemeSettings({value,language,disabled,onSave,onDirty}:{value:UserPreference|null;language:'de'|'en';disabled:boolean;onSave:(theme:UserPreference['theme'])=>Promise<void>;onDirty:(dirty:boolean)=>void}) {
 const [draft,setDraft]=useState<UserPreference['theme']>(value?.theme??'auto'),[failed,setFailed]=useState(false);
 const actual=useTheme(),de=language==='de',dirty=draft!==(value?.theme??'auto');
 useEffect(()=>{setDraft(value?.theme??'auto');setFailed(false);},[value?.theme]);
 useEffect(()=>{onDirty(dirty);return()=>onDirty(false);},[dirty,onDirty]);
 const change=(theme:UserPreference['theme'])=>{if(!disabled&&['auto','light','dark'].includes(theme)){setDraft(theme);setFailed(false);}};
 const source=actual.source==='manual'?(de?'Persönliche Auswahl':'Personal choice'):actual.source==='browser-system'?(de?'Browser-/Systempräferenz':'Browser/system preference'):(de?'Standard Hell (Signal nicht verfügbar)':'Default light (signal unavailable)');
 return <section className="workspace-panel settings-panel theme-settings"><h3>{de?'Persönliche Darstellung':'Personal appearance'}</h3>
 <label>{de?'Farbschema':'Theme'} <select aria-label={de?'Farbschema':'Theme'} disabled={disabled||!value} value={draft} onChange={e=>change(e.target.value as UserPreference['theme'])}><option value="auto">{de?'Automatisch':'Automatic'}</option><option value="light">{de?'Hell':'Light'}</option><option value="dark">{de?'Dunkel':'Dark'}</option></select></label>
 <p role="status">{de?'Aktive Quelle: ':'Active source: '}{source}. {de?'DSM-Theme-Signal nicht qualifiziert.':'DSM theme signal is not qualified.'}</p>
 <p>{de?'Gilt nur für dein Konto. Hierarchiegrenzen bleiben sichtbar. Reduzierte Bewegung lässt sich unter Visualisierung einstellen.':'Applies only to your account. Hierarchy boundaries remain visible. Reduced motion can be set under visualization.'}</p>
 <button disabled={disabled||!value||!dirty} onClick={()=>void onSave(draft).catch(()=>setFailed(true))}>{de?'Darstellung speichern':'Save appearance'}</button>
 <button disabled={disabled||!dirty} onClick={()=>change(value?.theme??'auto')}>{de?'Darstellung verwerfen':'Discard appearance'}</button>
 <button disabled={disabled||!value} onClick={()=>change('auto')}>{de?'Darstellung auf Automatik zurücksetzen':'Reset appearance to automatic'}</button>
 {failed&&<p role="alert">{de?'Darstellung nicht bestätigt. Prüfe den Speicherstatus und wiederhole die Änderung.':'Appearance not confirmed. Check the save status and retry the change.'}</p>}
 </section>;
}
