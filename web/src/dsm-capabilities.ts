// G08 records qualification, not runtime success or a substitute for server authorization.
export type Qualification = 'qualified-probe' | 'unqualified' | 'deferred-p1';
export type Capability = Readonly<{ status: Qualification; evidence: string; fallback: string }>;

// Only the measured build can inherit the narrow G04/G05 feasibility evidence.
export function dsmCapabilities(build: string): Readonly<Record<string, Capability>> {
  const measured = build === '90080';
  const capability = (status: Qualification, evidence: string, fallback: string): Capability => Object.freeze({ status, evidence, fallback });
  return Object.freeze({
    sessionLanguage: capability('unqualified', 'G08: no qualified session-language adapter', 'browser-language-then-en'),
    sessionTheme: capability('unqualified', 'G08: no qualified session-theme adapter', 'browser-system-then-light'),
    folderHandoff: capability(measured ? 'qualified-probe' : 'unqualified', measured ? 'G04: docs/testing/filestation-contract.md; DSM 7.4.1-90080' : 'No evidence for this build', 'copy-path-and-explain-unavailable'),
    localScheduledScan: capability(measured ? 'qualified-probe' : 'unqualified', measured ? 'G05: docs/testing/scheduler.md; DSM 7.4.1-90080' : 'No evidence for this build', 'manual-dsm-template-and-test'),
    fileHighlight: capability('deferred-p1', 'G04: parent-folder navigation only', 'open-parent-folder'),
    systemNotifications: capability('deferred-p1', 'G08: no qualified notification adapter', 'in-app-status-and-errors'),
    schedulerDiscovery: capability('deferred-p1', 'G08: no qualified app scheduler-read adapter', 'unknown-tasks-and-next-run'),
    schedulerCreation: capability('deferred-p1', 'G08: no qualified app scheduler-write adapter', 'manual-dsm-template-and-test'),
    filesystemEvents: capability('deferred-p1', 'G08: no qualified event-completeness contract', 'explicit-or-scheduled-scan'),
    restartContinuation: capability('deferred-p1', 'G05: interruption handling is not continuation', 'interrupted-status-and-new-scan'),
    indexVolumeMigration: capability('deferred-p1', 'PRD 22.4: migration not accepted', 'fixed-package-data-location'),
  });
}

// A malformed or unsupported browser language has the documented English fallback.
export function resolveLanguage(manual: 'de' | 'en' | 'auto', browserLanguage: unknown, dsm?: Readonly<{qualified:boolean;language:unknown}>): Readonly<{ language: 'de' | 'en'; source: 'manual' | 'dsm' | 'browser' | 'fallback' }> {
  if (manual === 'de' || manual === 'en') return { language: manual, source: 'manual' };
  // Only an independently qualified DSM signal can override the browser; unsupported means English.
  if (dsm?.qualified) {
    const language=dsm.language==='ger'||dsm.language==='de'?'de':dsm.language==='enu'||dsm.language==='en'?'en':null;
    return language?{language,source:'dsm'}:{language:'en',source:'fallback'};
  }
  if (typeof browserLanguage === 'string' && browserLanguage.length <= 128) {
    try {
      const language = new Intl.Locale(browserLanguage).language;
      if (language === 'de' || language === 'en') return { language, source: 'browser' };
    } catch { /* Invalid language tags are unavailable input, never an execution failure. */ }
  }
  return { language: 'en', source: 'fallback' };
}

// PRD 22.2 permits the browser/system preference while the DSM adapter is unqualified.
export function resolveTheme(manual: 'light' | 'dark' | 'auto', systemDark: unknown): Readonly<{ theme: 'light' | 'dark'; source: 'manual' | 'browser-system' | 'fallback' }> {
  if (manual === 'light' || manual === 'dark') return { theme: manual, source: 'manual' };
  if (typeof systemDark === 'boolean') return { theme: systemDark ? 'dark' : 'light', source: 'browser-system' };
  return { theme: 'light', source: 'fallback' };
}

// Unknown task inventory is not an empty inventory and never implies a next execution time.
export function schedulerInformation() {
  return Object.freeze({ source: 'unavailable' as const, tasks: null, nextRun: null });
}

// These are explicit product boundaries, not dormant settings exposed to users.
export const outsideV1 = Object.freeze([
  'historical-comparison', 'saved-analysis-views', 'analysis-export', 'visual-reports',
  'btrfs-exclusive-analysis', 'content-duplicates', 'smb-handoff', 'external-drives',
  'other-nas-platforms', 'non-admin-index-access',
] as const);
