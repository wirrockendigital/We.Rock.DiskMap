import {useDisplay} from './features/settings/display';
import {createContext,useContext} from 'react';
import {formatByteSize,type SizeUnit} from './size-metrics';
export interface RegionalSettings {locale:'de-DE'|'en-US'|'en-GB';timeZone:string;zoneSource:'personal'|'system-zoneinfo'|'unavailable'}
export const RegionalContext=createContext<RegionalSettings|null>(null);
// Presentation never changes stored timestamps or canonical decimal byte values.
export function regionalSettings(language:'de'|'en',locale:RegionalSettings['locale']|null,timeZone:string|null,nasTimeZone:string|null):RegionalSettings {
 return {locale:locale??(language==='de'?'de-DE':'en-US'),timeZone:timeZone??nasTimeZone??'UTC',zoneSource:timeZone?'personal':nasTimeZone?'system-zoneinfo':'unavailable'};
}
// Integer formatting uses BigInt throughout, including values above JavaScript's safe integer range.
export function regionalInteger(value:string|bigint|number,locale:string):string {
 const raw=String(value);if(!/^-?(0|[1-9][0-9]*)$/.test(raw)||typeof value==='number'&&!Number.isSafeInteger(value))return '—';
 return new Intl.NumberFormat(locale,{maximumFractionDigits:0}).format(BigInt(raw));
}
// Include the zone identifier and per-instant offset so both occurrences of a DST overlap are distinct.
export function regionalTimestamp(value:string|null|undefined,settings:RegionalSettings):string {
 if(!value||!/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(value)||!Number.isFinite(Date.parse(value)))return '—';
 return new Intl.DateTimeFormat(settings.locale,{timeZone:settings.timeZone,year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',timeZoneName:'longOffset',hourCycle:'h23'}).format(new Date(value))+` [${settings.timeZone}]`;
}
// Localize the already rounded decimal label without converting the exact byte amount to Number.
export function regionalSize(value:string|null,unit:SizeUnit,locale:string):string {
 const out=formatByteSize(value,unit);if(out.label===null)return '—';const decimal=out.label.slice(0,-unit.length-1),[whole,fraction]=decimal.split('.');
 const separator=new Intl.NumberFormat(locale).formatToParts(1.1).find(p=>p.type==='decimal')?.value??'.';
 return regionalInteger(whole,locale)+(fraction?separator+fraction:'')+' '+unit;
}
// Components share the confirmed personal settings; standalone surfaces use explicit language and UTC.
export function useRegionalFormat(language:'de'|'en'='en') {
 const display=useDisplay(),context=useContext(RegionalContext),settings=context??regionalSettings(language,null,null,null);
 return {settings,integer:(v:string|bigint|number)=>regionalInteger(v,settings.locale),timestamp:(v:string|null|undefined)=>regionalTimestamp(v,settings),size:(v:string|null,u:SizeUnit)=>regionalSize(v,display.units==='binary'?(u==='GB'?'GiB':u==='TB'?'TiB':u):u,settings.locale)};
}
// Leaf presenters keep regional formatting separate from data ownership and query identities.
export function RegionalTime({value,language='en'}:{value:string|null|undefined;language?:'de'|'en'}){const f=useRegionalFormat(language);return <time dateTime={value??undefined}>{f.timestamp(value)}</time>;}
export function RegionalNumber({value,language='en'}:{value:string|number|bigint;language?:'de'|'en'}){return <>{useRegionalFormat(language).integer(value)}</>;}
export function RegionalSize({value,unit='GB',language='en'}:{value:string|null;unit?:SizeUnit;language?:'de'|'en'}){return <>{useRegionalFormat(language).size(value,unit)}</>;}

// Text-only timestamp presenter is valid inside native option elements.
export function RegionalTimeText({value,language='en'}:{value:string|null|undefined;language?:'de'|'en'}){return <>{useRegionalFormat(language).timestamp(value)}</>;}

// Percentage inputs are exact decimal labels; localizing never changes their numerator or denominator.
export function RegionalPercentage({value,language='en'}:{value:string;language?:'de'|'en'}){const display=useDisplay(),f=useRegionalFormat(language);if(!display.percentages)return null;const match=/^(0|[1-9][0-9]*)\.([0-9]+) %$/.exec(value);if(!match)return <>{value}</>;const separator=new Intl.NumberFormat(f.settings.locale).formatToParts(1.1).find(p=>p.type==='decimal')?.value??'.';return <>{f.integer(match[1])+separator+match[2]+' %'}</>;}

// Preferred units add a readable scale while the title preserves the exact byte count.
export function RegionalBytes({value,language='en'}:{value:string|null;language?:'de'|'en'}){const f=useRegionalFormat(language);return <span title={value===null?undefined:`${value} B`}>{f.size(value,'GB')}{value!==null&&<> ({f.integer(value)} B)</>}</span>;}
