import type { Job, ScanBoundaryReport } from '../../contracts';

// Only fixed classifications cross the presentation boundary; raw API errors never do.
export type NoticeCode = 'SCAN_PARTIAL' | 'SCAN_FAILED' | 'SCAN_INTERRUPTED' | 'TARGET_MISSING' | 'TARGET_LOCKED' | 'TARGET_UNREADABLE';
export type LocalNotice = Readonly<{ id: number; code: NoticeCode }>;
export type NoticeOutcome = Readonly<{ job: Pick<Job, 'id' | 'state'> & Partial<Pick<Job, 'warnings'>>; snapshotState: string; coverageErrors: number }>;
export type NoticeSession = ReturnType<typeof createNoticeSession>;
export const NOTICE_LIMIT = 8;
export const NOTICE_DEDUP_LIMIT = 512;

// The authenticated Workbench owns one instance across profile remounts and disposes it on logout.
export function createNoticeSession() {
  const seen = new Set<string>();
  let notices: readonly LocalNotice[] = Object.freeze([]), sequence = 0, disposed = false;
  function add(jobId: string, code: NoticeCode) {
    const key = `${jobId}:${code}`;
    // Saturation suppresses new notices instead of evicting dedup keys and repeating old warnings.
    if (seen.has(key) || seen.size >= NOTICE_DEDUP_LIMIT) return;
    seen.add(key);
    notices = Object.freeze([...notices.slice(-(NOTICE_LIMIT - 1)), Object.freeze({ id: ++sequence, code })]);
  }
  return {
    observe(outcome: NoticeOutcome, report?: ScanBoundaryReport): boolean {
      const previous = notices;
      if (disposed || !/^[A-Za-z0-9_-]{1,128}$/.test(outcome.job.id)) return false;
      const state = outcome.job.state;
      if (state === 'failed') add(outcome.job.id, 'SCAN_FAILED');
      else if (state === 'interrupted') add(outcome.job.id, 'SCAN_INTERRUPTED');
      else if (state === 'completed' && (outcome.snapshotState === 'partial' || outcome.coverageErrors > 0)) add(outcome.job.id, 'SCAN_PARTIAL');
      const target = (code: string) => {
        if (code === 'SOURCE_MISSING' || code === 'SOURCE_NOT_MOUNTED') add(outcome.job.id, 'TARGET_MISSING');
        else if (code === 'SOURCE_LOCKED') add(outcome.job.id, 'TARGET_LOCKED');
        else if (code === 'SOURCE_UNREADABLE') add(outcome.job.id, 'TARGET_UNREADABLE');
      };
      // Only allowlisted codes are consumed; warning messages can contain private source details.
      for (const warning of outcome.job.warnings ?? []) target(warning.code);
      // Boundary reports must already be qualified by the host against their snapshot and revision.
      if (report?.jobId === outcome.job.id) for (const boundary of report.boundaries) target(boundary.reasonCode);
      return previous !== notices;
    },
    snapshot: () => notices,
    dismiss(id: number): readonly LocalNotice[] {
      notices = Object.freeze(notices.filter(notice => notice.id !== id));
      return notices;
    },
    // A disposed instance never accepts late success or error observations from its old session.
    dispose() { disposed = true; seen.clear(); notices = Object.freeze([]); },
  };
}
