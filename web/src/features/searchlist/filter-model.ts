import type {PreferenceView} from '../../contracts';
import {exactBytes} from '../../size-metrics';
export const categories=['image','video','audio','document','archive','diskImage','database','otherUnknown'] as const;
export interface SearchDraft {text:string;field:'name'|'path'|'both';scope:'direct'|'recursive';min:string;max:string;from:string;to:string;categories:string[];kinds:('file'|'directory')[];mode:'highlight'|'hits-only';sort:PreferenceView['sort'];direction:PreferenceView['direction']}
// Form drafts are local input only; applying produces a single complete central view update.
export function searchDraft(view:PreferenceView):SearchDraft {
 return{text:view.search.text,field:view.search.field,scope:view.search.scope,min:view.filter.minSize??'',max:view.filter.maxSize??'',from:view.filter.modifiedFrom??'',to:view.filter.modifiedTo??'',categories:[...view.filter.categories],kinds:[...(view.filter.objectKinds??[])],mode:view.filterMode,sort:view.sort,direction:view.direction};
}
// Compact snapshots accept retained size/name/type predicates only.
export function applySearchDraft(view:PreferenceView,d:SearchDraft):PreferenceView {
 if(d.from||d.to||d.sort==='mtime'||new TextEncoder().encode(d.text).length>512||!['name','path','both'].includes(d.field)||!['direct','recursive'].includes(d.scope)||!['highlight','hits-only'].includes(d.mode)||!['name','size','mtime','type'].includes(d.sort)||!['asc','desc'].includes(d.direction)||d.categories.some(x=>!categories.includes(x as typeof categories[number]))||new Set(d.categories).size!==d.categories.length||d.kinds.some(x=>!['file','directory'].includes(x))||new Set(d.kinds).size!==d.kinds.length)throw Error('INVALID_FILTER');
 const min=d.min===''?undefined:exactBytes(d.min),max=d.max===''?undefined:exactBytes(d.max);
 if(min!==undefined&&max!==undefined&&min!>max!)throw Error('INVALID_FILTER');

 return{...structuredClone(view),filter:{categories:[...d.categories],...(d.kinds.length?{objectKinds:[...d.kinds]}:{}),...(min!==undefined?{minSize:d.min}:{}),...(max!==undefined?{maxSize:d.max}:{})},search:{text:d.text,field:d.field,scope:d.scope},filterMode:d.mode,sort:d.sort,direction:d.direction};
}
// Presets replace their own rule while preserving independent filter predicates.
export function presetDraft(d:SearchDraft,preset:'large'|'videos'):SearchDraft {
 const out=structuredClone(d);
 if(preset==='large')out.min='1000000001';
 else if(preset==='videos')out.categories=['video'];

 return out;
}
export type FilterChipKey='search'|'stored-filter-text'|'minSize'|'maxSize'|'modifiedFrom'|'modifiedTo'|`category:${string}`|`kind:${string}`;
// Removing one chip preserves all other confirmed selectors, including snapshot and root.
export function removeChip(view:PreferenceView,key:FilterChipKey):PreferenceView {
 const next=structuredClone(view);
 if(key==='search')next.search.text='';
 else if(key==='stored-filter-text'){delete next.filter.text;delete next.filter.field;}
 else if(key.startsWith('category:'))next.filter.categories=next.filter.categories.filter(x=>x!==key.slice(9));
 else if(key.startsWith('kind:'))next.filter.objectKinds=next.filter.objectKinds?.filter(x=>x!==key.slice(5));
 else delete next.filter[key as 'minSize'|'maxSize'|'modifiedFrom'|'modifiedTo'];return next;
}
export function clearSearchFilters(view:PreferenceView):PreferenceView {
 // Reset also removes an unsupported remembered timestamp sort explicitly.
 return{...structuredClone(view),sort:view.sort==='mtime'?'size':view.sort,filter:{categories:[]},search:{...view.search,text:''}};
}
