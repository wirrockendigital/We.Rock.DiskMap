import type { LocalNotice, NoticeCode } from './notices';

// All broad notice copy is static and deliberately excludes profile, target and path strings.
const copy: Record<'de' | 'en', Record<NoticeCode, readonly [string, string]>> = {
  de: {
    SCAN_PARTIAL: ['Der Scan hat Erfassungslücken.', 'Prüfe die Erfassungsgrenzen im Scanstatus. Öffne teilweise Daten ausdrücklich.'],
    SCAN_FAILED: ['Der Scan ist fehlgeschlagen.', 'Prüfe den Scanstatus und die lokale Diagnose. Ein vorhandener vollständiger Stand bleibt separat verfügbar.'],
    SCAN_INTERRUPTED: ['Der Scan wurde unterbrochen.', 'Prüfe den Scanstatus. Starte einen neuen Scan erst ausdrücklich.'],
    TARGET_MISSING: ['Ein Scanziel fehlt oder ist nicht eingehängt.', 'Prüfe die Bereichsauswahl und den Einhängestatus in DSM. Fehlende Daten bedeuten nicht 0 Byte.'],
    TARGET_LOCKED: ['Ein Scanziel ist gesperrt.', 'Entsperre die Freigabe in DSM und prüfe den Bereich erneut.'],
    TARGET_UNREADABLE: ['Ein Scanziel ist nicht lesbar.', 'Prüfe die Leserechte für den Paketdienst in DSM. Erweitere Rechte nur für den benötigten Bereich.'],
  },
  en: {
    SCAN_PARTIAL: ['The scan has coverage gaps.', 'Check coverage boundaries in scan status. Open partial data explicitly.'],
    SCAN_FAILED: ['The scan failed.', 'Check scan status and local diagnostics. An existing complete snapshot remains separately available.'],
    SCAN_INTERRUPTED: ['The scan was interrupted.', 'Check scan status. Start a new scan only explicitly.'],
    TARGET_MISSING: ['A scan target is missing or unmounted.', 'Check the selected scope and mount status in DSM. Missing data does not mean zero bytes.'],
    TARGET_LOCKED: ['A scan target is locked.', 'Unlock the share in DSM and check the scope again.'],
    TARGET_UNREADABLE: ['A scan target is unreadable.', 'Check read permissions for the package service in DSM. Grant access only to the required scope.'],
  },
};

// This is an app-local status region, not a DSM notification transport or a second job poller.
export function LocalNotices({ notices, language, onDismiss }: { notices: readonly LocalNotice[]; language: 'de' | 'en'; onDismiss: (id: number) => void }) {
  return <section aria-label={language === 'de' ? 'Lokale Meldungen' : 'Local notices'} aria-live="polite" aria-relevant="additions">
    {notices.map(notice => <article key={notice.id}><strong>{copy[language][notice.code][0]}</strong><p>{copy[language][notice.code][1]}</p>
      <details><summary>{language === 'de' ? 'Technische Kennung' : 'Technical code'}</summary><code>{notice.code}</code></details>
      <button type="button" onClick={() => onDismiss(notice.id)}>{language === 'de' ? 'Meldung schließen' : 'Dismiss notice'}</button>
    </article>)}
  </section>;
}
