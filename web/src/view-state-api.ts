import { validNodeProvenance } from './query-provenance';
import type { PreferenceView, Node, ScanBoundary, PackageUsage } from './contracts';
import type { NavigationState } from './NavigationBar';
import { currentToken, SessionDenied } from './session';
import { responseJSON, validView } from './preference-client';

// The existing reader payload is retained intact for the workbench's detailed snapshot validation.
export interface RestoredSnapshot {
  state: 'complete' | 'partial' | null; startedAt: string | null; endedAt: string | null;
  snapshotId: string | null; nodes: Node[]; truncated: boolean;
  scanBoundaries: ScanBoundary[]; scanBoundaryCount: string; scanBoundariesTruncated: boolean; packageUsage: PackageUsage;
  profileRevision: number | null; currentProfileRevision: number; profileChanged: boolean; capturedProfileName: string | null;
}
export interface RestoredReader {
  view: RestoredSnapshot; lease: { id: string; snapshotId: string; expiresAt: string } | null;
  newCompleteSnapshotId: string | null; selection: { nodeId: string | null; fallback: 'none' | 'ancestor' | 'scope-unavailable' };
}
export interface RestoreResult {
  preferenceRevision: number; profileId: string | null; resolvedView: PreferenceView | null;
  reader: RestoredReader | null; navigation: NavigationState | null; reasons: string[];
}
const reasons = ['ROOT_UNAVAILABLE','ROOT_ANCESTOR','SELECTION_UNAVAILABLE','SELECTION_OUTSIDE_ROOT','PROFILE_UNAVAILABLE','NO_PROFILE','NO_SNAPSHOT','SNAPSHOT_UNAVAILABLE'];
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value);
const nullableID = (value: unknown) => value === null || id(value);
const validReasons = (value: unknown): value is string[] => Array.isArray(value) && value.length <= reasons.length && new Set(value).size === value.length && value.every(x => reasons.includes(x));
const invalid = (): never => { throw new Error('VIEW_RESPONSE_INVALID'); };
// Canonical roots can legitimately replace an unavailable requested root, but only with an explicit reason.
export function checkedNavigation(value: unknown, snapshotId: string, rootId?: string): NavigationState {
  const nav = value as NavigationState;
  if (!nav || nav.snapshotId !== snapshotId || !id(nav.rootId) || !nullableID(nav.parentId) || !nullableID(nav.selectedNodeId) ||
    !validReasons(nav.reasons) || !Array.isArray(nav.breadcrumb) || nav.breadcrumb.length < 1 || nav.breadcrumb.length > 256 ||
    new TextEncoder().encode(JSON.stringify(nav)).length > 16384) return invalid();
  if (rootId && rootId !== 'nas' && nav.rootId !== rootId && !nav.reasons.some(x => x === 'ROOT_UNAVAILABLE' || x === 'ROOT_ANCESTOR')) return invalid();
  const seen = new Set<string>();
  for (const crumb of nav.breadcrumb) {
    if (!crumb || !id(crumb.id) || seen.has(crumb.id) || typeof crumb.name !== 'string' || !['nas','volume','share','other','directory'].includes(crumb.kind) || !validNodeProvenance(crumb)) return invalid();
    seen.add(crumb.id);
  }
  if ((nav.parentId === null && (nav.breadcrumb.length !== 1 || nav.breadcrumb[0].kind !== 'nas')) || nav.breadcrumb.at(-1)?.id !== nav.rootId || (nav.parentId !== null && nav.breadcrumb.at(-2)?.id !== nav.parentId)) return invalid();
  return nav;
}
// Superseded navigation must cancel its backoff immediately, without leaving timers or listeners.
function navigationBackoff(delay: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', abort); };
    const abort = () => { finish(); reject(new Error('VIEW_REQUEST_ABORTED')); };
    const timer = setTimeout(() => { finish(); resolve(); }, delay);
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
}
// This request only resolves qualified server identities; it never saves preferences implicitly.
async function request(action: 'view-restore' | 'view-navigation', body: { viewId: string; [key: string]: unknown }, signal: AbortSignal, transport: typeof fetch): Promise<unknown> {
  const active = () => { if (signal.aborted) throw new Error('VIEW_REQUEST_ABORTED'); };
  active();
  try {
  const token = currentToken(); if (!token) throw new SessionDenied('SESSION_REQUIRED');
  let response: Response;
  for (let attempt = 0; ; attempt++) {
  active();
  if (currentToken() !== token) throw new SessionDenied('SESSION_REQUIRED');
  response = await transport(`./api.cgi?action=${action}`, { method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-SYNO-TOKEN': token, 'Idempotency-Key': body.viewId }, body: JSON.stringify(body) });
  active();
  // Only the read-only navigation endpoint retries transient service unavailability.
  if (action !== 'view-navigation' || response.status !== 503 || attempt >= 2) break;
  await response.body?.cancel();
  await navigationBackoff(attempt === 0 ? 100 : 250, signal);
  }
  active();
  if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED');
  const envelope = await responseJSON(response) as { contractVersion?: number; data?: unknown; error?: { code?: string } };
  if (signal.aborted) throw new Error('VIEW_REQUEST_ABORTED');
  if (envelope.contractVersion !== 1) return invalid();
  if (!response.ok) {
    const code = envelope.error?.code;
    if (code && ['VIEW_EXPIRED','VIEW_LIMIT','SNAPSHOT_MISMATCH','PREFERENCE_CONFLICT','IDEMPOTENCY_CONFLICT','QUERY_BUDGET_EXCEEDED','PROFILE_NOT_FOUND'].includes(code)) throw new Error(code);
    throw new Error('VIEW_UNAVAILABLE');
  }
  return envelope.data;
  } catch (error) { active(); throw error; }
}
export async function requestRestore(viewId: string, expectedRevision: number, signal: AbortSignal, transport: typeof fetch = fetch): Promise<RestoreResult> {
  if (!id(viewId) || !Number.isInteger(expectedRevision) || expectedRevision < 0 || expectedRevision > 2147483647) return invalid();
  const result = await request('view-restore', { contractVersion: 1, viewId, expectedPreferenceRevision: expectedRevision }, signal, transport) as RestoreResult;
  if (signal.aborted) throw new Error('VIEW_REQUEST_ABORTED');
  if (!result || result.preferenceRevision !== expectedRevision || !nullableID(result.profileId) || !validReasons(result.reasons) || !validView(result.resolvedView)) return invalid();
  if (result.resolvedView === null) {
    if (result.reader !== null || result.navigation !== null) return invalid();
    return result;
  }
  const v = result.resolvedView, r = result.reader;
  if (result.profileId !== v.profileId || !r || !r.view || !r.lease || r.lease.id !== viewId || r.lease.snapshotId !== v.snapshotId ||
    r.view.snapshotId !== v.snapshotId || !Number.isFinite(Date.parse(r.lease.expiresAt)) || !['complete','partial'].includes(r.view.state ?? '') ||
    !Array.isArray(r.view.nodes) || r.view.nodes.length > 10000 || typeof r.view.truncated !== 'boolean' ||
    !r.selection || !nullableID(r.selection.nodeId) || !['none','ancestor','scope-unavailable'].includes(r.selection.fallback) || !nullableID(r.newCompleteSnapshotId)) return invalid();
  for (const node of r.view.nodes) if (!node || node.snapshotId !== v.snapshotId || !id(node.id)) return invalid();
  const nav = checkedNavigation(result.navigation, v.snapshotId);
  if (nav.rootId !== v.rootId || nav.selectedNodeId !== v.selectedNodeId) return invalid();
  return result;
}
export async function requestNavigation(profileId: string, viewId: string, snapshotId: string, rootId: string, selectedNodeId: string | null,
  signal: AbortSignal, transport: typeof fetch = fetch): Promise<NavigationState> {
  if (![profileId,viewId,snapshotId,rootId].every(id) || !nullableID(selectedNodeId)) return invalid();
  const value = await request('view-navigation', { contractVersion: 1, profileId, viewId, snapshotId, rootId, selectedNodeId }, signal, transport);
  if (signal.aborted) throw new Error('VIEW_REQUEST_ABORTED');
  const nav = checkedNavigation(value, snapshotId, rootId);
  if (nav.selectedNodeId !== selectedNodeId && !(selectedNodeId !== null && nav.selectedNodeId === null && nav.reasons.some(x => x === 'SELECTION_UNAVAILABLE' || x === 'SELECTION_OUTSIDE_ROOT'))) return invalid();
  return nav;
}
