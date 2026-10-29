import {useSizeFormatter} from './size-unit';
import {useEffect,useRef,useState} from 'react';
import {LayersIcon,Cross2Icon} from '@radix-ui/react-icons';
import {readSnapshotUsage,readSnapshotShareStatus,unknownSnapshotStatus,type SnapshotUsage,type SnapshotShareStatus} from '../snapshot-usage';
import {type Language} from './model';
// Reports are share-wide estimates and never contribute to the logical-file treemap total.
export function SnapshotUsageButton({share,language}:{share?:string;language:Language}){
 const formatNodeSize=useSizeFormatter();
 const [open,setOpen]=useState(false),[result,setResult]=useState<SnapshotUsage|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState(false);
 const [observation,setObservation]=useState<{share:string;status:SnapshotShareStatus}|null>(null);
 // Bind every observation to its share; an old reply must never color a newly selected share.
 const status=observation&&observation.share===share?observation.status:unknownSnapshotStatus();
 useEffect(()=>{if(!share)return;const controller=new AbortController();
  void readSnapshotShareStatus(share,controller.signal).then(value=>{if(!controller.signal.aborted)setObservation({share,status:value});}).catch(()=>{if(!controller.signal.aborted)setObservation({share,status:unknownSnapshotStatus()});});
  return()=>controller.abort();
 },[share]);
 const dialog=useRef<HTMLDialogElement>(null),de=language==='de';
 useEffect(()=>{setOpen(false);setResult(null);},[share]);
 useEffect(()=>{if(status.state==='missing'||status.state==='denied')setOpen(false);},[status.state]);
 useEffect(()=>{if(!open||!share)return;const controller=new AbortController();dialog.current?.showModal();setLoading(true);setError(false);setResult(null);
  void readSnapshotUsage(share,controller.signal).then(value=>{if(!controller.signal.aborted)setResult(value);}).catch(()=>{if(!controller.signal.aborted)setError(true);}).finally(()=>{if(!controller.signal.aborted)setLoading(false);});
  return()=>{controller.abort();dialog.current?.close();};
 },[open,share]);
 const date=(value:number)=>new Intl.DateTimeFormat(language,{dateStyle:'medium',timeStyle:'short'}).format(new Date(value*1000));
 // Match the recycle icon: neutral when readable, red on denied access, crossed out when absent.
 const description=status.state==='missing'?(status.supported===false?(de?'Snapshots werden für diese Freigabe nicht unterstützt.':'Snapshots are not supported for this share.'):(de?'Kein Snapshot-Zeitplan aktiviert und keine Snapshots vorhanden.':'No snapshot schedule enabled and no snapshots present.')):status.state==='denied'?(de?'Keine Berechtigung zum Lesen des Snapshot-Status.':'No permission to read snapshot status.'):status.state==='readable'?`${de?'Snapshot-Zeitplan':'Snapshot schedule'}: ${status.scheduled?(de?'aktiviert':'enabled'):(de?'deaktiviert':'disabled')} · ${status.count} ${de?'Snapshots vorhanden':'snapshots present'}.`:(de?'Snapshot-Status noch nicht ermittelbar.':'Snapshot status is not yet available.');
 const title=share?description:(de?'Freigabe auswählen':'Select a shared folder');
 return <><span className={'disk-recycle-'+status.state} data-snapshot-state={status.state} title={title}><button type="button" disabled={!share||status.state==='missing'||status.state==='denied'} aria-label={`${de?'Snapshot-Speicherbericht':'Snapshot storage report'}: ${title}`} onClick={()=>setOpen(true)}><LayersIcon aria-hidden="true"/></button></span>
 {open&&<dialog ref={dialog} className="disk-dialog" aria-labelledby="snapshot-usage-title" onCancel={e=>{e.preventDefault();setOpen(false);}}><header className="disk-dialog-header"><h2 id="snapshot-usage-title">{de?'Snapshot-Speicher':'Snapshot storage'} · {share}</h2><button className="disk-icon-button" aria-label={de?'Schließen':'Close'} onClick={()=>setOpen(false)}><Cross2Icon/></button></header><div className="disk-delete-content"><p>{description}</p>
 {loading?<p role="status">{de?'DSM-Bericht wird geladen …':'Loading DSM report …'}</p>:error?<p role="alert">{de?'Der Snapshot-Bericht ist momentan nicht verfügbar. Prüfe Snapshot Replication und deine DSM-Sitzung.':'The snapshot report is unavailable. Check Snapshot Replication and your DSM session.'}</p>:result?<><h3>{formatNodeSize({id:'snapshot-estimate',name:'',kind:'folder',bytesExact:result.bytes},language)} {de?'geschätzt':'estimated'}</h3><p>{de?'Zeitraum':'Period'}: {date(result.from)} – {date(result.to)}</p><p>{de?'Berechnet am':'Calculated'}: {date(result.calculatedAt)}</p><p>{de?'DSM-Schätzung für die gesamte Freigabe. Gemeinsam genutzte Datenblöcke verhindern eine eindeutige Zuordnung zu einzelnen Dateien. Dieser Wert wird nicht zur Dateikarte addiert.':'DSM estimate for the entire shared folder. Shared data blocks prevent exact attribution to individual files. This value is not added to the file map.'}</p></>:<p>{de?'Noch kein abgeschlossener DSM-Bericht vorhanden. In Snapshot Replication die Freigabe wählen und unter „Snapshot → Größe berechnen“ einen Bericht erstellen.':'No completed DSM report is available. Select the share in Snapshot Replication and use Snapshot → Calculate Size.'}</p>}
 </div></dialog>}</>;
}
