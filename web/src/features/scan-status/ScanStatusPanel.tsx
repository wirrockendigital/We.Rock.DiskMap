import type { ReactNode } from 'react';
import { ProgressHistory } from '../../ProgressHistory';
import { RegionalTime } from '../../regional-format';
import type { ProgressObservation, StatusJob, StatusSnapshot } from './status-client';

export interface ScanStatusPanelProps {
  language: 'de' | 'en';
  snapshot: StatusSnapshot | null;
  health: 'current' | 'retrying' | 'stopped';
  progress: ProgressObservation | null;
  selectedProfileName?: string;
  profileNames: Readonly<Record<string, string>>;
  controls?: ReactNode;
  onRetry: () => void;
  onSelectProfile?: (id: string) => void;
}

const text = {
  de: { title:'Scan-Status', current:'Aktueller Auftragsstatus', retrying:'Status derzeit nicht aktuell. Erneute Abfrage läuft.', stopped:'Status nicht verfügbar. Angezeigte frühere Beobachtungen sind nicht aktuell.', retry:'Auftragsstatus erneut lesen', observed:'Zuletzt beobachtet', active:'Aktiver Auftrag auf dem NAS', idle:'Kein aktiver Auftrag zum Beobachtungszeitpunkt.', queue:'Warteschlange', empty:'Keine wartenden Aufträge zum Beobachtungszeitpunkt.', unknown:'Noch keine bestätigte Beobachtung.', profile:'Profil', revision:'Profilrevision', mode:'Scanart', trigger:'Auslöser', phase:'Phase', accepted:'Angenommen', started:'Gestartet', open:'Profil im Scan-Status öffnen', selected:'Ausgewähltes Profil', previous:'Frühere Beobachtung', noHistory:'Wähle ein Profil für Laufhistorie und Steueraktionen.' },
  en: { title:'Scan status', current:'Current job status', retrying:'Status is not current. Retrying the observation.', stopped:'Status unavailable. Earlier observations shown here are not current.', retry:'Read job status again', observed:'Last observed', active:'Active job on the NAS', idle:'No active job at the observation time.', queue:'Queue', empty:'No queued jobs at the observation time.', unknown:'No confirmed observation yet.', profile:'Profile', revision:'Profile revision', mode:'Scan mode', trigger:'Trigger', phase:'Phase', accepted:'Accepted', started:'Started', open:'Open profile in scan status', selected:'Selected profile', previous:'Earlier observation', noHistory:'Select a profile for run history and controls.' },
};
const values: Record<string, [string,string]> = {
  queued:['Wartend','Queued'], preflight:['Vorprüfung','Preflight'], scanning:['Erfassung','Scanning'], paused:['Pausiert','Paused'], aggregating:['Aggregation','Aggregating'], validating:['Validierung','Validating'], publishing:['Veröffentlichung','Publishing'],
  full:['Vollscan','Full scan'], rebuild:['Neuaufbau','Rebuild'], reconcile:['Abgleich','Reconcile'], ui:['App','App'], cli:['Befehlszeile','Command line'], scheduler:['DSM-Aufgabenplaner','DSM Task Scheduler'],
};

// This component only presents qualified observations; the existing owner controls transport and mutations.
export function ScanStatusPanel({language,snapshot,health,progress,selectedProfileName,profileNames,controls,onRetry,onSelectProfile}:ScanStatusPanelProps) {
  const t=text[language],current=health==='current'&&snapshot!==null,label=(value:string)=>values[value]?.[language==='de'?0:1]??'—';
  const job=(value:StatusJob)=><>
    <dl className="scan-job-facts"><dt>{t.profile}</dt><dd>{profileNames[value.profileId]??value.profileId}</dd>
      <dt>{t.revision}</dt><dd>{value.profileRevision}</dd><dt>{t.phase}</dt><dd>{label(value.state)}</dd>
      <dt>{t.mode}</dt><dd>{label(value.scanMode)}</dd><dt>{t.trigger}</dt><dd>{label(value.trigger)}</dd>
      <dt>{t.accepted}</dt><dd><RegionalTime value={value.acceptedAt} language={language}/></dd>
      {value.startedAt&&<><dt>{t.started}</dt><dd><RegionalTime value={value.startedAt} language={language}/></dd></>}
    </dl>
    {onSelectProfile&&Object.hasOwn(profileNames,value.profileId)&&<button disabled={!current} onClick={()=>onSelectProfile(value.profileId)}>{t.open}: {profileNames[value.profileId]}</button>}
  </>;
  return <section className="workspace-panel scan-status-panel" aria-label={t.title}>
    <h2>{t.title}</h2><p role="status">{current?t.current:health==='current'?t.unknown:health==='retrying'?t.retrying:t.stopped}</p>
    {health==='stopped'&&<button onClick={onRetry}>{t.retry}</button>}
    {snapshot?<><p>{t.observed}: <RegionalTime value={snapshot.observedAt} language={language}/>{!current&&` · ${t.previous}`}</p>
      <h3>{t.active}</h3>{snapshot.active?job(snapshot.active):<p>{t.idle}</p>}
      <h3>{t.queue} ({snapshot.queued.length})</h3>{snapshot.queued.length?<ol className="scan-queue" aria-label={t.queue}>{snapshot.queued.map(value=><li key={value.jobId} value={value.queuePosition}>{job(value)}</li>)}</ol>:<p>{t.empty}</p>}
    </>:<p>{t.unknown}</p>}
    {selectedProfileName!==undefined?<><h3>{t.selected}: {selectedProfileName}</h3>
      {/* Stale observations never authorize a new control action, including a queued retry. */}
      <fieldset disabled={!current} aria-label={t.selected}>{controls}</fieldset>
      <ProgressHistory language={language} current={progress?.current??null} history={progress?.history??[]} limited={progress?.limited??false} failed={!current}/>
    </>:<p>{t.noHistory}</p>}
  </section>;
}
