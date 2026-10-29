import {useSizeFormatter} from './size-unit';
import type {ReactNode} from 'react';
import {FileTextIcon,TrashIcon} from '@radix-ui/react-icons';
import type {RecycleState} from '../scope-selection';
import type {PreferenceWorkspace} from '../contracts';
import {type Language,type StorageNode} from './model';

// These presentation switches never change scan inclusion or launch a new scan.
export function CategoryToolbar({root,language,workspace,onChange,children,access,recycleState}:{children?:ReactNode;recycleState?:RecycleState;access?:{recycle:boolean;system:boolean};root?:StorageNode;language:Language;workspace:PreferenceWorkspace;onChange?:(patch:Partial<PreferenceWorkspace>)=>void}){
 const formatNodeSize=useSizeFormatter();
 const de=language==='de';
 const categories=[
  {key:'showRecycle' as const,available:root?.indexedRecycle,Icon:TrashIcon,label:de?'Papierkörbe':'Recycle bins'},
  {key:'showSystem' as const,available:root?.indexedSystem,Icon:FileTextIcon,label:de?'Systemdateien':'System files'},
 ];
 return <header className="disk-category-toolbar">
  <strong aria-label={de?'Angezeigte Größe':'Displayed size'}>{root?formatNodeSize(root,language):'—'}</strong>
  <div className="disk-category-actions">
   {categories.map(({key,available,Icon,label})=>{
    const recycle=key==='showRecycle',state=recycleState??'unknown';
    const enabled=!!available&&!!onChange&&(!recycle||state!=='missing'),pressed=enabled&&workspace[key];
    const denied=recycle?state==='denied':access?.system;
    // Permission status is independent of visibility and scan inclusion.
    const base=denied?`${label}: ${de?'Im letzten Scan teilweise nicht lesbar. Berechtigungen prüfen und aktualisieren.':'Partly unreadable in the last scan. Check permissions and refresh.'}`:!available?`${label}: ${de?'nicht im geladenen Index verfügbar':'not available in the loaded index'}`:`${label} ${pressed?(de?'ausblenden':'hide'):(de?'einblenden':'show')}`;
    // Current presence and access are separate from the stored index and visibility preference.
    const description=state==='missing'?(de?'Auf dieser Freigabe ist kein Papierkorb vorhanden.':'This share has no recycle bin.'):state==='denied'?(de?'Papierkorb vorhanden, aber keine Leserechte für die App.':'Recycle bin exists, but the app has no read access.'):state==='readable'?(de?'Papierkorb vorhanden und lesbar.':'Recycle bin exists and is readable.'):(de?'Papierkorbstatus noch nicht bekannt.':'Recycle bin status is not yet known.');
    const title=recycle?`${label}: ${description} ${!available?(de?'Nicht im geladenen Index enthalten.':'Not included in the loaded index.'):(pressed?(de?'Ausblenden':'Hide'):(de?'Einblenden':'Show'))}`:base;
    const className=recycle?'disk-recycle-'+state:denied?'disk-category-denied':undefined;
    // The wrapper keeps the explanation available even when its native button is disabled.
    return <span key={key} className={className} data-recycle-state={recycle?state:undefined} title={title}><button type="button" aria-label={recycle&&(state==='denied'||state==='missing')||denied?title:label} aria-pressed={!!pressed} disabled={!enabled} onClick={()=>onChange?.({[key]:!workspace[key]})}><Icon aria-hidden="true"/></button></span>;
   })}
   {children}
  </div>
 </header>;
}
