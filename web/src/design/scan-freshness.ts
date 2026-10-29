import type {Scope} from '../contracts';
import type {ScopeInventory} from '../scope-selection';
import type {ProfileCardSummary} from '../profile-management';
import type {NativeProfileEntry} from './useNativeDiskMap';
export interface ScanStamp {state:'unscanned'|'complete'|'partial'|'unknown';endedAt:string|null}
export interface FreshnessPreference {value:number;unit:'minutes'|'hours'|'days'|'weeks'|'months'}
export const defaultFreshness=():FreshnessPreference=>({value:7,unit:'days'});
const units={minutes:60,hours:3600,days:86400,weeks:604800,months:2592000};
// Months are fixed 30-day periods so the displayed aging threshold is deterministic.
export const freshnessSeconds=(p:FreshnessPreference)=>p.value*units[p.unit];
export const validFreshness=(p:FreshnessPreference)=>!!p&&Number.isInteger(p.value)&&p.value>=1&&p.value<=999&&Object.hasOwn(units,p.unit);
const key=(user:string)=>'we-rock-diskmap.freshness.v1:'+encodeURIComponent(user);
export function readFreshness(user:string,storage:Pick<Storage,'getItem'>):FreshnessPreference{try{const p=JSON.parse(storage.getItem(key(user))??'null');return validFreshness(p)?p:defaultFreshness();}catch{return defaultFreshness();}}
export function writeFreshness(user:string,p:FreshnessPreference,storage:Pick<Storage,'setItem'>){if(!user||!validFreshness(p))throw Error('INVALID_PREFERENCE');storage.setItem(key(user),JSON.stringify(p));}
// Scope containment does not merge snapshots or confer access; it only selects a candidate reader.
export function scopeCovers(parent:Scope,child:Scope,inventory:ScopeInventory):boolean{
 if(parent.kind==='nas')return true;if(child.kind==='nas')return false;
 if(child.kind==='volumes')return child.targetIds.every(id=>['volumes','mixed'].includes(parent.kind)&&parent.targetIds.includes(id));
 return child.targetIds.every(id=>{
  const volume=inventory.shares.find(s=>s.id===id)?.volumeId;
  if(volume&&['volumes','mixed'].includes(parent.kind)&&parent.targetIds.includes(volume))return true;
  if(!parent.targetIds.includes(id))return false;
  if(parent.kind==='shares'||parent.kind==='mixed')return true;
  if(parent.kind!=='folder'||child.kind!=='folder')return false;
  const p=parent.relativePath??'.',c=child.relativePath??'.';return p==='.'||c===p||c.startsWith(p+'/');
 });
}
export const scopeEqual=(a:Scope,b:Scope)=>a.kind===b.kind&&(a.relativePath??'.')===(b.relativePath??'.')&&[...a.targetIds].sort().join('|')===[...b.targetIds].sort().join('|');
// Only matching captured revisions can establish an exact successful scope; partial scans stay explicit.
export function scopeStamp(scope:Scope,profiles:NativeProfileEntry[],summaries:ProfileCardSummary[],inventory:ScopeInventory,ready:boolean):ScanStamp{
 if(!ready)return {state:'unknown',endedAt:null};
 const candidates:ScanStamp[]=[];let descendant=false;
 for(const entry of profiles){
  if(!entry.profile.active)continue;const p=entry.profile,s=summaries.find(x=>x.profileId===p.id);if(!s)continue;
  const partial=s.lastAttempt?.state==='completed'&&s.lastAttempt.snapshotState==='partial'?s.lastAttempt.endedAt:null;
  const complete=s.quality==='complete'&&s.capturedRevision===p.revision?s.endedAt:null;
  const endedAt=partial&&(!complete||Date.parse(partial)>Date.parse(complete))?partial:complete;if(!endedAt)continue;
  if(scopeCovers(p.scope,scope,inventory))candidates.push({endedAt,state:endedAt===partial?'partial':'complete'});
  else if(scopeCovers(scope,p.scope,inventory))descendant=true;
 }
 candidates.sort((a,b)=>Date.parse(b.endedAt!)-Date.parse(a.endedAt!));
 return candidates[0]??{state:descendant?'partial':'unscanned',endedAt:null};
}
export function freshnessDisplay(stamp:ScanStamp,preference:FreshnessPreference,language:'de'|'en',now=Date.now()){
 const de=language==='de',time=stamp.endedAt?Date.parse(stamp.endedAt):NaN;
 if(stamp.state==='unscanned')return {color:'transparent',label:de?'Ungescannt':'Not scanned',title:de?'Noch nicht gescannt':'Not scanned yet'};
 if(!Number.isFinite(time))return {color:'transparent',label:stamp.state==='partial'?(de?'Teilweise':'Partial'):(de?'Unbekannt':'Unknown'),title:de?'Kein vollständiger Scanzeitpunkt bekannt':'No complete scan time available'};
 const age=Math.max(0,now-time),ratio=Math.min(1,age/(freshnessSeconds(preference)*1000));
 const color=`hsl(${Math.round(130*(1-ratio))} 72% 57%)`;
 const minutes=Math.floor(age/60000),hours=Math.floor(minutes/60),days=Math.floor(hours/24);
 const relative=minutes<1?(de?'Gerade eben':'Just now'):new Intl.RelativeTimeFormat(de?'de':'en',{numeric:'always'}).format(-(days>=7?Math.floor(days/7):days>=1?days:hours>=1?hours:minutes),days>=7?'week':days>=1?'day':hours>=1?'hour':'minute');
 // Freshness is independent of coverage; warnings are rendered separately.
 const label=relative;
 return {color,label,title:`${relative}${stamp.state==='partial'?(de?' · Abdeckung nicht vollständig bestätigt':' · Coverage not fully confirmed'):''}`};
}
export function scanTimestamp(stamp:ScanStamp,language:'de'|'en'){
 const de=language==='de';if(!stamp.endedAt)return stamp.state==='unscanned'?(de?'Ordner noch nicht gescannt':'Folder not scanned yet'):stamp.state==='partial'?(de?'Nur Teilbereiche gescannt':'Only some areas scanned'):(de?'Scanzeitpunkt unbekannt':'Scan time unknown');
 const date=new Date(stamp.endedAt);return (de?'Ordner zuletzt gescannt am ':'Folder last scanned on ')+date.toLocaleDateString(de?'de-DE':'en-GB')+(de?' um ':' at ')+date.toLocaleTimeString(de?'de-DE':'en-GB',{hour:'2-digit',minute:'2-digit'})+(de?' Uhr':'')+(stamp.state==='partial'?(de?' · Teilabdeckung':' · Partial coverage'):'');
}
