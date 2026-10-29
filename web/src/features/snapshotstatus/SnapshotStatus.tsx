import {RegionalTime} from '../../regional-format';
import {useEffect,useState} from 'react';
import {snapshotStatus,type SnapshotStatusInput} from './snapshot-status';
import {snapshotMessages} from './messages';
export interface SnapshotStatusProps extends SnapshotStatusInput {
 language:'de'|'en';job:{state:string}|null;sourceGap:boolean;
 partialAvailable:boolean;partialUnqualified:boolean;completeAvailable:boolean;
 pendingSwitch:boolean;disabled:boolean;
 onAdopt:()=>void;onOpenPartial:()=>void;onBackComplete:()=>void;
}
const jobStates=['queued','preflight','scanning','paused','aggregating','validating','publishing','completed','failed','cancelled','interrupted'] as const;
// This presentational component never fetches, adopts, persists or replaces a lease by itself.
export function SnapshotStatus(props:SnapshotStatusProps){
 const [now,setNow]=useState(()=>Date.now());
 useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),30000);return()=>clearInterval(timer);},[]);
 const m=snapshotMessages[props.language],status=snapshotStatus(props,now),snapshot=props.snapshot;
 const jobKey=jobStates.find(state=>state===props.job?.state);
 // Active, failed, interrupted and unknown jobs remain visible above the map.
 const visibleJob=!!props.job&&(!jobKey||!['completed','cancelled'].includes(jobKey));
 return <section aria-label={m.title} className="snapshot-status">
  {snapshot&&<>
   <p role="status" className="snapshot-summary"><strong>{m.title}</strong>: {status.partial?m.partial:m.complete} · {m[status.freshness]}</p>
   {status.profileChanged&&<p role="status">{m.changed}</p>}
  </>}
  {visibleJob&&<p>{m.job}: {props.job?(jobKey?m[jobKey]:m.unknownJob):m.noJob}</p>}
  {props.sourceGap&&<p role="status">{m.sourceGap}</p>}
  {props.contentState!=='data'&&<p role="status">{m[props.contentState]}</p>}
  {(status.newerComplete||props.expired||props.pendingSwitch)&&<p role="status">{props.pendingSwitch?m.pending:props.expired?m.expired:m.newer}{' '}
   <button type="button" disabled={props.disabled} onClick={props.onAdopt}>{props.pendingSwitch?m.retry:props.expired?m.reopen:m.adopt}</button></p>}
  {props.partialAvailable&&<button type="button" disabled={props.disabled} onClick={props.onOpenPartial}>{m.openPartial}</button>}
  {props.partialUnqualified&&<p role="status">{m.unqualified}</p>}
  {status.partial&&<button type="button" disabled={props.disabled} onClick={props.onBackComplete}>{props.completeAvailable?m.back:m.backEmpty}</button>}
  {props.selectionFallback!=='none'&&<p role="status">{props.selectionFallback==='ancestor'?m.ancestor:m.scope}</p>}
  {/* Generic metadata stays accessible while warnings and actions remain outside the disclosure. */}
  <details className="snapshot-details"><summary>{props.language==='de'?'Details zum Datenstand':'Snapshot details'}</summary>
   {snapshot&&<>
    <dl><dt>{m.interval}</dt><dd><RegionalTime value={snapshot.startedAt} language={props.language}/> – <RegionalTime value={snapshot.endedAt} language={props.language}/></dd>
    <dt>{m.revision}</dt><dd>{snapshot.profileRevision}</dd></dl>
   <p>{m.recorded} {m.freshness}</p>
   </>}
   {!visibleJob&&<p>{m.job}: {props.job?(jobKey?m[jobKey]:m.unknownJob):m.noJob}</p>}
   {props.contentState==='data'&&<p>{m.data}</p>}
   <p>{m.source}</p><p>{m.retained}</p>
  </details>
 </section>;
}
