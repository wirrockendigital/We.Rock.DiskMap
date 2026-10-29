import {useEffect,useState} from 'react';
import type {PreferenceView} from '../../contracts';
import {searchDraft,applySearchDraft,presetDraft,removeChip,clearSearchFilters,categories,type SearchDraft,type FilterChipKey} from './filter-model';
import {listMessages} from './messages';
export interface SearchFilterControlsProps {language:'de'|'en';view:PreferenceView;disabled:boolean;onChange:(view:PreferenceView)=>void}
// Input text is edited locally until explicit submission; selection changes do not erase unsaved input.
export function SearchFilterControls(p:SearchFilterControlsProps){
 const m=listMessages[p.language],key=JSON.stringify([p.view.profileId,p.view.snapshotId,p.view.rootId,p.view.search,p.view.filter,p.view.filterMode,p.view.sort,p.view.direction]);
 const [draft,setDraft]=useState(()=>searchDraft(p.view)),[error,setError]=useState(false);
 useEffect(()=>{setDraft(searchDraft(p.view));setError(false);},[key]);
 const change=<K extends keyof SearchDraft>(key:K,value:SearchDraft[K])=>setDraft(d=>({...d,[key]:value}));
 const commit=(d:SearchDraft)=>{if(p.disabled)return;try{const next=applySearchDraft(p.view,d);setError(false);p.onChange(next);}catch{setError(true);}};
 const chips:Array<{key:FilterChipKey;label:string}>=[];
 if(p.view.search.text)chips.push({key:'search',label:`${m.search}: ${p.view.search.text}`});
 if(p.view.filter.text)chips.push({key:'stored-filter-text',label:`${m.filterText}: ${p.view.filter.text}`});
 for(const [field,label] of [['minSize',m.min],['maxSize',m.max],['modifiedFrom',m.from],['modifiedTo',m.to]] as const)if(p.view.filter[field]!==undefined)chips.push({key:field,label:`${label}: ${p.view.filter[field]}`});
 for(const c of p.view.filter.categories)chips.push({key:`category:${c}`,label:m[c as typeof categories[number]]??m.otherUnknown});
 for(const k of p.view.filter.objectKinds??[])chips.push({key:`kind:${k}`,label:m[k]});
 // Retained unsupported choices stay visible until the user explicitly resets them.
 const unsupported=!!p.view.filter.modifiedFrom||!!p.view.filter.modifiedTo||p.view.sort==='mtime';
 return <section className="search-controls" aria-label={m.title}>
  {unsupported&&<p role="alert">{p.language==='de'?'Diese gespeicherte Ansicht verwendet Datumsinformationen, die nicht mehr erfasst werden. Bitte Filter zurücksetzen.':'This saved view uses timestamps that are no longer collected. Reset the filters to continue.'}</p>}
  <form onSubmit={e=>{e.preventDefault();commit(draft);}}><fieldset disabled={p.disabled}><legend>{m.title}</legend>
   <div className="search-primary">
   <label>{m.search}<input type="search" value={draft.text} maxLength={512} onChange={e=>change('text',e.target.value)}/></label>
   <label>{m.field}<select value={draft.field} onChange={e=>change('field',e.target.value as SearchDraft['field'])}>{(['both','name','path'] as const).map(x=><option key={x} value={x}>{m[x]}</option>)}</select></label>
   <button type="submit">{m.apply}</button>
   </div>
   {/* Native disclosure keeps every draft control mounted while collapsed. */}
   <details className="search-advanced"><summary>{m.advanced}</summary>
   <div className="search-fields">
   <p>{m.literal}</p>{p.view.filter.text&&<p>{p.language==='de'?'Anwenden ersetzt den zusätzlichen Textfilter durch den Suchtext dieses Formulars.':'Apply replaces the additional text filter with this form’s search text.'}</p>}
   <label>{m.scope}<select value={draft.scope} onChange={e=>change('scope',e.target.value as SearchDraft['scope'])}><option value="recursive">{m.recursive}</option><option value="direct">{m.direct}</option></select></label>
   {(['min','max'] as const).map(field=><label key={field}>{m[field]}<input value={draft[field]} inputMode={field==='min'||field==='max'?'numeric':undefined} onChange={e=>change(field,e.target.value)}/></label>)}
   {/* Compact snapshots retain names and sizes, so date-based controls are omitted. */}
   <fieldset><legend>{m.category}</legend>{categories.map(c=><label key={c}><input type="checkbox" checked={draft.categories.includes(c)} onChange={e=>change('categories',e.target.checked?[...draft.categories,c]:draft.categories.filter(x=>x!==c))}/>{m[c]}</label>)}</fieldset>
   <fieldset><legend>{m.object}</legend>{(['file','directory'] as const).map(k=><label key={k}><input type="checkbox" checked={draft.kinds.includes(k)} onChange={e=>change('kinds',e.target.checked?[...draft.kinds,k]:draft.kinds.filter(x=>x!==k))}/>{m[k]}</label>)}</fieldset>
   <label>{m.mode}<select value={draft.mode} onChange={e=>change('mode',e.target.value as SearchDraft['mode'])}><option value="highlight">{m.highlight}</option><option value="hits-only">{m.hits}</option></select></label>
   <label>{m.sort}<select value={draft.sort} onChange={e=>change('sort',e.target.value as SearchDraft['sort'])}>{(['name','size','type'] as const).map(x=><option key={x} value={x}>{m[x]}</option>)}</select></label>
   <label>{m.direction}<select value={draft.direction} onChange={e=>change('direction',e.target.value as SearchDraft['direction'])}><option value="asc">{m.asc}</option><option value="desc">{m.desc}</option></select></label>
   </div>
   {(['large','videos'] as const).map(preset=><button key={preset} type="button" onClick={()=>commit(presetDraft(draft,preset))}>{m[preset]}</button>)}
   </details>
  </fieldset></form>
  {error&&<p role="alert">{m.invalid}</p>}
  <div className="filter-chips" aria-label={m.title}>{chips.map(chip=><button key={chip.key} type="button" disabled={p.disabled} onClick={()=>{if(!p.disabled)p.onChange(removeChip(p.view,chip.key));}} aria-label={`${m.remove}: ${chip.label}`}>{chip.label} ×</button>)}<button type="button" disabled={p.disabled} onClick={()=>{if(!p.disabled)p.onChange(clearSearchFilters(p.view));}}>{m.reset}</button></div>
 </section>;
}
