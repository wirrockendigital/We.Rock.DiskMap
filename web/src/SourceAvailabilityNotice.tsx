import {sourceRecoveryHelp} from './features/recovery/source-help';
import type { QuerySourceAvailability } from './query-provenance';

// This historical observation has no filesystem actions or inferred byte totals.
export function SourceAvailabilityNotice({value,language}:{value:QuerySourceAvailability|undefined;language:'de'|'en'}) {
  if (!value || value.total === '0') return null;
  const de = language === 'de';
  return <section aria-label={de?'Historische Quellverfügbarkeit':'Historical source availability'}>
    <h4>{de?'Nicht vollständig erfasste Quellbereiche':'Source areas without full recorded content'}</h4>
    <p>{de?'Befunde dieses Datenstands, keine Prüfung der aktuellen Erreichbarkeit.':'Observations from this snapshot, not a check of current accessibility.'}</p>
    <p>{de?'Erfasste Befunde':'Recorded observations'}: {value.total}</p>
    <ul>{value.records.map((r,i)=><li key={i}>{r.name || (de?'Name nicht erfasst':'Name not recorded')} — {r.reason==='content-not-recorded'&&(de?'Für diesen Bereich wurde kein Inhalt erfasst. ':'No content was recorded for this area. ')}{sourceRecoveryHelp(r.provenance.state,language)}</li>)}</ul>
    {value.truncated&&<p>{de?'Begrenzter Ausschnitt der historischen Befunde.':'Bounded excerpt of the historical observations.'}</p>}
  </section>;
}
