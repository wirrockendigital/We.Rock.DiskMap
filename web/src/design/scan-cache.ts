import type {ProfileConfiguration} from '../profile-actions';
import {configurationKey} from '../profile-actions';
import type {NativeProfileEntry} from './useNativeDiskMap';

// Snapshot identity includes every scan setting except the human-readable profile name.
export function matchingCachedProfile(profiles:NativeProfileEntry[],configuration:ProfileConfiguration):NativeProfileEntry|undefined {
 const key=configurationKey({...configuration,name:''});
 return [...profiles].reverse().find(p=>p.activeSnapshotId&&p.profile.active&&configurationKey({...p.profile,name:''})===key);
}

// Display the captured completion time, never the current clock or a fabricated example date.
export function cachedResultLabel(endedAt:string|null|undefined,language:'de'|'en'):string {
 if(!endedAt||!Number.isFinite(Date.parse(endedAt)))return language==='de'?'Gespeichertes Ergebnis · Scanzeitpunkt unbekannt':'Cached result · Scan time unknown';
 const date=new Date(endedAt),locale=language==='de'?'de-DE':'en-GB';
 const day=new Intl.DateTimeFormat(locale,{day:'2-digit',month:'2-digit',year:'numeric'}).format(date);
 const time=new Intl.DateTimeFormat(locale,{hour:'2-digit',minute:'2-digit'}).format(date);
 return language==='de'?`Ergebnis aus dem Cache vom ${day} um ${time} Uhr`:`Cached result from ${day} at ${time}`;
}

// Failed attempts without a published result must not hide an older readable profile.
export function hasRecordedResult(profile:NativeProfileEntry,summary:import('../profile-management').ProfileCardSummary|undefined,summariesReady:boolean):boolean {
 return !!profile.activeSnapshotId||!!summary?.snapshotId||summary?.lastAttempt?.snapshotState==='partial'||summary?.lastAttempt?.snapshotState==='complete'||(!summariesReady&&!!profile.latestJobId);
}
