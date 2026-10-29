import {readCachedSnapshotShareStatus} from '../snapshot-status-cache';
import {useEffect,useState} from 'react';
import {TrashIcon,LayersIcon,CounterClockwiseClockIcon} from '@radix-ui/react-icons';
import type {RecycleState,ScopeInventory} from '../scope-selection';
import {currentToken} from '../session';
import {readDriveVersioning,unknownDriveStatus,type DriveVersioningInventory,type DriveVersioningStatus} from '../drive-versioning';
import {unknownSnapshotStatus,type SnapshotShareStatus} from '../snapshot-usage';
import type {Language} from './model';

export type StatusShare=ScopeInventory['shares'][number];
// One Drive inventory serves the toolbar and every visible share; navigation does not refetch it.
export function useDriveVersioning(enabled:boolean,revision:string){
 // Static rendering has no DSM window and must remain an unknown observation.
 const token=enabled&&typeof window!=='undefined'?currentToken():null;
 const [observed,setObserved]=useState<{token:string;revision:string;value:DriveVersioningInventory}|null>(null);
 useEffect(()=>{
  if(!enabled||!token)return;const controller=new AbortController();
  void readDriveVersioning(controller.signal).then(value=>{if(!controller.signal.aborted)setObserved({token,revision,value});}).catch(()=>{if(!controller.signal.aborted)setObserved(null);});
  return()=>controller.abort();
 },[enabled,token,revision]);
 const inventory=observed?.token===token&&observed?.revision===revision?observed.value:null;
 return (share:string):DriveVersioningStatus=>inventory?.shares.get(share)??{...unknownDriveStatus(),state:inventory?.state==='denied'?'denied':'unknown'};
}

// Present status as a focusable explanation, independent of visibility controls or DSM mutations.
export function StatusIcon({kind,state,title}:{kind:'recycle'|'snapshot'|'drive';state:RecycleState;title:string}){
 const Icon=kind==='recycle'?TrashIcon:kind==='snapshot'?LayersIcon:CounterClockwiseClockIcon;
 return <span className={`disk-share-status-icon is-${state}`} data-share-status={kind} data-state={state} role="img" aria-label={title} title={title} tabIndex={0}><Icon aria-hidden="true"/>{state==='unknown'&&<small aria-hidden="true">?</small>}</span>;
}

// Keep non-team-folder, disabled versioning and unavailable observations distinct in both languages.
export function driveDescription(status:DriveVersioningStatus,language:Language){
 const de=language==='de',prefix=status.personal?(de?'Persönliche Drive-Ordner (My Drive): ':'Personal Drive folders (My Drive): '):'';
 return prefix+(status.state==='denied'?(de?'Keine Berechtigung zum Lesen der Drive-Versionierung.':'No permission to read Drive versioning.'):status.state==='unknown'?(de?'Drive-Versionierungsstatus nicht ermittelbar.':'Drive versioning status is unavailable.'):status.teamFolder===false?(de?'Kein aktivierter Drive-Teamordner.':'Not an enabled Drive Team Folder.'):status.state==='missing'?(de?'Drive-Versionierung deaktiviert.':'Drive versioning disabled.'):(de?`Drive-Versionierung aktiviert · maximal ${status.maxVersions} Versionen.`:`Drive versioning enabled · maximum ${status.maxVersions} versions.`));
}
export function DriveVersioningIcon({status,language}:{status:DriveVersioningStatus;language:Language}){
 return <StatusIcon kind="drive" state={status.state} title={driveDescription(status,language)}/>;
}

// Only mounted share rows read snapshot status; ordinary folders never create status requests.
export function ShareStatusIcons({share,drive,language}:{share:StatusShare;drive:DriveVersioningStatus;language:Language}){
 const token=typeof window!=='undefined'?currentToken():null,[observed,setObserved]=useState<{name:string;token:string|null;status:SnapshotShareStatus}|null>(null);
 useEffect(()=>{const controller=new AbortController();
  void readCachedSnapshotShareStatus(share.name,controller.signal).then(status=>{if(!controller.signal.aborted)setObserved({name:share.name,token,status});}).catch(()=>{if(!controller.signal.aborted)setObserved(null);});
  return()=>controller.abort();
 },[share.name,token]);
 const snap=observed?.name===share.name&&observed.token===token?observed.status:unknownSnapshotStatus(),de=language==='de',recycle=share.recycleState??'unknown';
 const recycleTitle=de?{readable:'Papierkorb vorhanden und lesbar.',missing:'Kein Papierkorb vorhanden.',denied:'Papierkorb vorhanden, aber keine Leserechte für die App.',unknown:'Papierkorbstatus nicht ermittelbar.'}[recycle]:{readable:'Recycle bin exists and is readable.',missing:'No recycle bin exists.',denied:'Recycle bin exists, but the app has no read access.',unknown:'Recycle bin status is unavailable.'}[recycle];
 const snapTitle=snap.state==='denied'?(de?'Keine Berechtigung zum Lesen des Snapshot-Status.':'No permission to read snapshot status.'):snap.state==='unknown'?(de?'Snapshot-Status nicht ermittelbar.':'Snapshot status is unavailable.'):snap.supported===false?(de?'Snapshots für diese Freigabe nicht unterstützt.':'Snapshots are not supported for this share.'):(de?`Snapshot-Zeitplan ${snap.scheduled?'aktiviert':'deaktiviert'} · ${snap.count} Snapshots vorhanden.`:`Snapshot schedule ${snap.scheduled?'enabled':'disabled'} · ${snap.count} snapshots present.`);
 return <span className="disk-tree-share-status"><StatusIcon kind="recycle" state={recycle} title={recycleTitle}/><StatusIcon kind="snapshot" state={snap.state} title={snapTitle}/><DriveVersioningIcon status={drive} language={language}/></span>;
}
