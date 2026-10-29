import {IndexSpaceHelp} from './features/recovery/RecoveryHelp';
import {LocalNotices} from './features/notifications/LocalNotices';
import {createNoticeSession,type NoticeSession,type LocalNotice,type NoticeOutcome} from './features/notifications/notices';
import {HelpSettings} from './features/settings/HelpSettings';
import {ConfigurationImport} from './features/settings/ConfigurationImport';
import {DiagnosticExport} from './features/settings/DiagnosticExport';
import {ConfigurationExport} from './features/settings/ConfigurationExport';
import {PermissionStatus} from './features/settings/PermissionStatus';
import {GlobalExclusionSettings} from './features/settings/GlobalExclusionSettings';
import {DisplayContext,DisplayEffects,defaultDisplay} from './features/settings/display';
import {PersonalDisplaySettings,type PersonalChange} from './features/settings/PersonalDisplaySettings';
import {FileStationIntegrationTest} from './FileStationIntegrationTest';
import {ThemeContext,ThemeSettings,useResolvedTheme} from './theme';
import {RegionalTime,RegionalNumber} from './regional-format';
import {RegionalContext,regionalSettings} from './regional-format';
import {RegionalSettings,type RegionalChoice} from './RegionalSettings';
import {readRegionalContext} from './regional-context';
import type {UserPreference} from './contracts';
import { StatusObserver } from './features/scan-status/status-observer';
import { readStatus, readProgressObservation, StatusReadError, type StatusSnapshot, type ProgressObservation } from './features/scan-status/status-client';
import { SearchResultList } from './features/searchlist/SearchResultList';
import { FilterTotals } from './features/searchlist/FilterTotals';
import { SourceAvailabilityNotice } from './SourceAvailabilityNotice';
import { SearchFilterControls } from './features/searchlist/SearchFilterControls';
import { SnapshotStatus } from './features/snapshotstatus/SnapshotStatus';
import type { ContentState } from './features/snapshotstatus/snapshot-status';
import { AnalysisNodeList } from './features/analysisinteraction/AnalysisNodeList';
import { ReaderAcquisitions } from './reader-acquisition';
import { defaultView, AnalysisController, emptyAnalysis, type ConfirmedAnalysis } from './AnalysisController';
import { PreferenceClient } from './preference-client';
import { requestRestore } from './view-state-api';
import { NavigationBar } from './NavigationBar';
import { readMetricAvailability, readAnalysis, readSearchList, effectiveAnalysisFilter, type AnalysisHierarchy, type AnalysisNode } from './analysis-query';
import { adaptHierarchy } from './visualization/shared/adapter';
import { GroupInspectionPanel } from './visualization/shared/GroupInspectionPanel';
import { readGroupPage } from './visualization/shared/group-client';
import { groupDirectChildren } from './visualization/shared/groups';
import type { HitTarget } from './visualization/shared/interaction';
import type { MapModel } from './visualization/shared/model';
import type { PreferenceView } from './contracts';
import { AnalysisShell, AnalysisWorkspace, resolveProfileSelection, type AnalysisArea } from './AnalysisShell';
import { OnboardingWizard } from './features/onboarding/OnboardingWizard';
import { ProfileEditor } from './ProfileEditor';
import { ProfileOverview } from './ProfileOverview';
import { loadProfileSummaries, type ManagementReceipt } from './profile-management';
import { loadScopeInventory, checkSelectedScope } from './scope-api';
import { SchedulerTemplate } from './SchedulerTemplate';
import { DiagnosticsSettings } from './DiagnosticsSettings';
import { StorageSettings, type RetentionIntentRef } from './StorageSettings';
import { ScanStatusPanel } from './features/scan-status/ScanStatusPanel';
import { IntegritySettings, type IntegrityIntentRef } from './IntegritySettings';
import { requestJobControl, checkedControl } from './job-control';
import type { ControlIntent, ControlStatus } from './job-control';
import { requestReader, settlePinnedRead, retainReaderIntent } from './snapshot-reader';
import type { ViewPin, ReaderIntent } from './snapshot-reader';
import { HistoricalNodeDetails, checkedPaths } from './NodeDetails';
import type { NodePaths } from './NodeDetails';
import { useEffect, useRef, useState } from 'react';
import { RadialCanvas } from './visualization/radial/RadialCanvas';
import { TreemapCanvas } from './visualization/treemap/TreemapCanvas';
import { AccessibleMapInfo } from './visualization/shared/AccessibleMapInfo';
import { Legend } from './visualization/shared/Legend';
import { renderBudget, type DetailLevel } from './visualization/shared/render-budget';
import type { ColorMode } from './visualization/shared/colors';
import type { Job, Node, Profile, ScanBoundary, PackageUsage, ScanBoundaryReport } from './contracts';
import { parseBytes } from './contracts';
import { currentToken, SessionDenied } from './session';
import { requestScan, scanIntent } from './actions';
import type { ScanIntent, PendingScan } from './actions';
import { FileStationFailure, openFileStation } from './dsmFileStation';
import type { FileStationTarget } from './dsmFileStation';
import de from './locales/de.json';
import en from './locales/en.json';

// Selection does not change hierarchy contents; preserve DOM targets across fast clicks.
function hierarchyIdentity(analysis: ConfirmedAnalysis): string {
  return analysis.state.view && analysis.pin ? JSON.stringify({ ...analysis.state.view, selectedNodeId: null, viewId: analysis.pin.viewId }) : '';
}

type Messages = typeof de;
interface ProfileList { profiles: { profile: Profile; activeSnapshotId: string | null; latestJobId: string | null }[]; dsmBuild: string }
interface SnapshotResponse {
  state: 'complete' | 'partial' | null; startedAt: string | null; endedAt: string | null;
  snapshotId: string | null; nodes: Node[]; truncated: boolean;
  scanBoundaries: ScanBoundary[]; scanBoundaryCount: string; scanBoundariesTruncated: boolean; packageUsage: PackageUsage;
  profileRevision: number | null; currentProfileRevision: number; profileChanged: boolean; capturedProfileName: string | null;
}
interface Snapshot extends SnapshotResponse { snapshotId: string; profileRevision: number; capturedProfileName: string }
interface ReaderResponse { view: SnapshotResponse; lease: {id:string;snapshotId:string;expiresAt:string}|null; newCompleteSnapshotId:string|null; selection:{nodeId:string|null;fallback:'none'|'ancestor'|'scope-unavailable'} }
interface Outcome { control?:ControlStatus|null; job: Job; snapshotState: string; snapshotEndedAt: string | null; coverageErrors: number }
type ReadCode = 'SNAPSHOT_MISMATCH' | 'PROFILE_REBIND_REQUIRED' | 'INDEX_REFRESH_REQUIRED' | 'PATH_ENCODING_UNSUPPORTED' | 'FILE_STATION_UNAVAILABLE';
class ReadFailure extends Error { constructor(readonly status: number, readonly code: ReadCode | null = null) { super('READ_FAILED'); } }

// Read only a small error envelope, retaining an allowlisted code rather than upstream diagnostic text.
async function readErrorCode(response: Response): Promise<ReadCode | null> {
  if (response.status !== 409 || !response.body) return null;
  const reader = response.body.getReader();
  try {
    const data = new Uint8Array(4096);
    let length = 0;
    while (true) {
      const part = await reader.read();
      if (part.done) break;
      if (length + part.value.byteLength > data.byteLength) return null;
      data.set(part.value, length); length += part.value.byteLength;
    }
    const body = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(data.subarray(0, length))) as { contractVersion?: unknown; error?: { code?: unknown } } | null;
    if (body?.contractVersion !== 1) return null;
    const code = body.error?.code;
    return code === 'SNAPSHOT_MISMATCH' || code === 'PROFILE_REBIND_REQUIRED' || code === 'INDEX_REFRESH_REQUIRED' || code === 'PATH_ENCODING_UNSUPPORTED' || code === 'FILE_STATION_UNAVAILABLE' ? code : null;
  } catch { return null; }
  finally { try { await reader.cancel(); } catch { /* The request may already be aborted. */ } reader.releaseLock(); }
}

// Only bundled messages reach the UI; malformed, unknown and mismatched errors remain generic.
// Select grammar from the exact count without rounding historical metadata.
export function unknownSizeCountLabel(count: bigint, language: 'de'|'en'): string {
  return language === 'de' ? (count === 1n ? 'Datei mit unbekannter Größe' : 'Dateien mit unbekannter Größe') : (count === 1n ? 'file with unknown size' : 'files with unknown size');
}

export function readFailureMessage(error: unknown, messages: Messages): string {
  if (error instanceof FileStationFailure) {
    switch (error.code) {
      case 'BUILD_UNSUPPORTED': return messages['probe.launcherBuild'];
      case 'UNAVAILABLE': return messages['probe.launcherUnavailable'];
      case 'TARGET_INVALID': return messages['probe.fileStationUnavailable'];
      case 'LAUNCH_FAILED': return messages['probe.launcherFailed'];
    }
  }
  if (error instanceof Error && error.message === 'METRIC_UNAVAILABLE') return messages['probe.unknown'];
  if (error instanceof Error && error.message === 'QUEUE_FULL') return messages['scan.queueFull'];
  if (error instanceof Error && error.message === 'VIEW_EXPIRED') return messages['view.expired'];
  if (error instanceof Error && error.message === 'VIEW_LIMIT') return messages['view.limit'];
  if (error instanceof Error && error.message === 'JOB_CONFLICT') return messages['scan.jobConflict'];
  if (error instanceof Error && error.message === 'IDEMPOTENCY_CONFLICT') return messages['scan.replayConflict'];
  if (!(error instanceof ReadFailure)) return messages['probe.error'];
  switch (error.code) {
    case 'SNAPSHOT_MISMATCH': return messages['probe.stale'];
    case 'PROFILE_REBIND_REQUIRED': return messages['probe.rebind'];
    case 'INDEX_REFRESH_REQUIRED': return messages['probe.reindex'];
    case 'PATH_ENCODING_UNSUPPORTED': return messages['probe.pathEncoding'];
    case 'FILE_STATION_UNAVAILABLE': return messages['probe.fileStationUnavailable'];
    default: return messages['probe.error'];
  }
}

// Every view reads the existing authenticated CGI boundary; callers provide IDs, never source paths.
export async function readView<T>(parameters: Record<string, string>, signal: AbortSignal, request: typeof fetch = fetch, token = currentToken()): Promise<T> {
  if (!token) throw new SessionDenied('SESSION_REQUIRED');
  const response = await request(`./api.cgi?${new URLSearchParams(parameters)}`, {
    credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: AbortSignal.any([signal, AbortSignal.timeout(8000)]),
    headers: { Accept: 'application/json', 'X-SYNO-TOKEN': token },
  });
  if (response.status === 401 || response.status === 403) throw new SessionDenied('SESSION_REQUIRED');
  if (!response.ok) throw new ReadFailure(response.status, await readErrorCode(response));
  const body = await response.json() as { contractVersion?: unknown; data?: unknown };
  if (body?.contractVersion !== 1 || body.data === null || typeof body.data !== 'object') throw new Error('INVALID_RESPONSE');
  return body.data as T;
}

// Historical metadata uses a bounded POST with the current session and exact reader identity.
export async function readLeasedPaths(input:{contractVersion:1;profileId:string;viewId:string;snapshotId:string;nodeId:string},signal:AbortSignal,request:typeof fetch=fetch,token=currentToken()):Promise<NodePaths>{
 if(!token)throw new SessionDenied('SESSION_REQUIRED');
 const response=await request('./api.cgi?action=leased-paths',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(8000)]),headers:{'Content-Type':'application/json',Accept:'application/json','X-SYNO-TOKEN':token,'Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(input)});
 if(response.status===401||response.status===403)throw new SessionDenied('SESSION_REQUIRED');
 if(!response.body)throw new Error('INVALID_RESPONSE');
 const reader=response.body.getReader();let bytes=0;const chunks:Uint8Array[]=[];
 try{for(;;){const next=await reader.read();if(next.done)break;bytes+=next.value.byteLength;if(bytes>65536){await reader.cancel();throw new Error('INVALID_RESPONSE');}chunks.push(next.value);}}finally{reader.releaseLock();}
 const raw=new Uint8Array(bytes);let offset=0;for(const chunk of chunks){raw.set(chunk,offset);offset+=chunk.byteLength;}
 let body;try{body=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(raw));}catch{throw new Error('INVALID_RESPONSE');}
 if(!response.ok){if(body?.contractVersion===1&&body.error?.code==='VIEW_EXPIRED')throw new Error('VIEW_EXPIRED');throw new ReadFailure(response.status);}
 if(body?.contractVersion!==1)throw new Error('INVALID_RESPONSE');
 const result=checkedPaths(body.data,input.snapshotId,input.nodeId);
 if(result.fileStationPath!==null)throw new Error('INVALID_RESPONSE');return result;
}

// Lease identity and snapshot payload are validated as one indivisible response.
export function checkedReader(response:ReaderResponse,pin:ViewPin|null):Snapshot|null{
 const data=checkedSnapshot(response.view);
 if(data){if(!response.lease||response.lease.snapshotId!==data.snapshotId||typeof response.lease.id!=='string'||!validSnapshotTime(response.lease.expiresAt)||(pin&&(pin.snapshotId!==data.snapshotId||pin.viewId!==response.lease.id)))throw new Error('VIEW_RESPONSE_MISMATCH');}
 else if(response.lease!==null||pin)throw new Error('VIEW_RESPONSE_MISMATCH');
 if(response.newCompleteSnapshotId!==null&&(typeof response.newCompleteSnapshotId!=='string'||!response.newCompleteSnapshotId||response.newCompleteSnapshotId===data?.snapshotId))throw new Error('VIEW_RESPONSE_MISMATCH');
 if(!response.selection||!['none','ancestor','scope-unavailable'].includes(response.selection.fallback)||(response.selection.nodeId!==null&&typeof response.selection.nodeId!=='string'))throw new Error('VIEW_RESPONSE_MISMATCH');return data;
}

// Snapshot times come from the selected immutable header, never from the latest job.
function validSnapshotTime(value: string | null): boolean {
  return typeof value === 'string' && value.length <= 64 && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
}

// Reject mixed generations and invalid byte representations before rendering an index response.
export function checkedSnapshot(value: SnapshotResponse): Snapshot | null {
  checkedStorage(value);
  if (!Number.isSafeInteger(value.currentProfileRevision) || value.currentProfileRevision < 1 || typeof value.profileChanged !== 'boolean') throw new Error('INVALID_PROFILE_REVISION');
  if (value.snapshotId === null && value.state === null && value.startedAt === null && value.endedAt === null && Array.isArray(value.nodes) && value.nodes.length === 0 && value.truncated === false && value.profileRevision === null && value.capturedProfileName === null && value.profileChanged === false && value.scanBoundaryCount === '0' && value.scanBoundaries.length === 0 && value.scanBoundariesTruncated === false) return null;
  if (!Number.isSafeInteger(value.profileRevision) || (value.profileRevision ?? 0) < 1 || typeof value.capturedProfileName !== 'string' || !value.capturedProfileName || value.profileChanged !== (value.profileRevision !== value.currentProfileRevision)) throw new Error('INVALID_PROFILE_REVISION');
  if (typeof value.snapshotId !== 'string' || !value.snapshotId || !Array.isArray(value.nodes) || value.nodes.length > 200 || typeof value.truncated !== 'boolean') throw new Error('INVALID_SNAPSHOT');
  if (!['complete', 'partial'].includes(value.state ?? '') || !validSnapshotTime(value.startedAt) || !validSnapshotTime(value.endedAt) || Date.parse(value.endedAt!) < Date.parse(value.startedAt!)) throw new Error('INVALID_SNAPSHOT_TIME');
  const seen = new Set<string>();
  for (const node of value.nodes) {
    if (node.snapshotId !== value.snapshotId || typeof node.id !== 'string' || !node.id || seen.has(node.id) || typeof node.originalName !== 'string' || typeof node.relativePath !== 'string' || !['file', 'directory', 'symlink', 'fifo', 'socket', 'blockDevice', 'characterDevice'].includes(node.kind)) throw new Error('INVALID_NODE');
    seen.add(node.id); parseBytes(node.logicalBytes); parseBytes(node.allocatedBytes);
    if (node.linkCount !== null && (!Number.isSafeInteger(node.linkCount) || node.linkCount < 1)) throw new Error('INVALID_LINK_COUNT');
    if (!['file', 'directory'].includes(node.kind) && (node.logicalBytes !== null || node.allocatedBytes !== null)) throw new Error('INVALID_SPECIAL_SIZE');
  }
  return value as Snapshot;
}

// Validate bounded coverage and independent package measurements even when no scan exists yet.
export function checkedStorage(value: Pick<SnapshotResponse, 'scanBoundaries' | 'scanBoundaryCount' | 'scanBoundariesTruncated' | 'packageUsage'>) {
  checkedBoundaryList(value.scanBoundaries, value.scanBoundaryCount, value.scanBoundariesTruncated);
  const usage = value.packageUsage;
  if (!usage || usage.method !== 'package-state-stat' || !['complete', 'partial', 'unavailable'].includes(usage.state) || typeof usage.measuredAt !== 'string' || usage.measuredAt.length > 64 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(usage.measuredAt) || !Number.isFinite(Date.parse(usage.measuredAt))) throw new Error('INVALID_PACKAGE_USAGE');
  parseBytes(usage.logicalBytes); parseBytes(usage.allocatedBytes);
  if ((usage.state === 'complete' && (usage.logicalBytes === null || usage.allocatedBytes === null)) || (usage.state !== 'complete' && (usage.logicalBytes !== null || usage.allocatedBytes !== null))) throw new Error('INVALID_PACKAGE_USAGE');
  return value;
}

// This exhaustive mapping accepts only the versioned backend reason vocabulary.
const boundaryReasons = {
  PACKAGE_STATE_EXCLUDED: 'boundary.package',
  OUTSIDE_SCOPE_MOUNT: 'boundary.mount',
  SNAPSHOT_TREE_EXCLUDED: 'boundary.snapshot',
  SUBVOLUME_CLASSIFICATION_UNAVAILABLE: 'boundary.unclassified',
  SOURCE_UNREADABLE: 'boundary.unreadable',
  SOURCE_MISSING: 'boundary.missing',
  SOURCE_TRANSIENT: 'boundary.transient',
  SOURCE_CHANGED: 'boundary.changed',
  SOURCE_IO_ERROR: 'boundary.io',
  SOURCE_LOCKED: 'boundary.locked',
  SOURCE_NOT_MOUNTED: 'boundary.notMounted',
  SOURCE_UNVERIFIED: 'boundary.unverified',
  DEPTH_LIMIT: 'boundary.depth',
  PROFILE_EXCLUDED: 'boundary.profileExcluded',
  SCOPE_CHANGED: 'boundary.scopeChanged',
  SCOPE_VALIDATION_UNAVAILABLE: 'boundary.scopeUnavailable',
} as const satisfies Record<ScanBoundary['reasonCode'], keyof Messages>;

// Intentional exclusions describe the selected method rather than a failed observation.
export function intentionalBoundary(code: ScanBoundary['reasonCode']): boolean {
  return code === 'PACKAGE_STATE_EXCLUDED' || code === 'SNAPSHOT_TREE_EXCLUDED' || code === 'PROFILE_EXCLUDED';
}

// One bounded validator is shared by active-snapshot and separate terminal-job reports.
function checkedBoundaryList(boundaries: ScanBoundary[], total: string, truncated: boolean): void {
  const count = parseBytes(total);
  if (count === null || !Array.isArray(boundaries) || boundaries.length > 32 || typeof truncated !== 'boolean' || count < BigInt(boundaries.length) || truncated !== (count > BigInt(boundaries.length))) throw new Error('INVALID_BOUNDARIES');
  if (new TextEncoder().encode(JSON.stringify(boundaries)).length > 8192) throw new Error('INVALID_BOUNDARIES');
  for (const boundary of boundaries) {
    if (!boundary || typeof boundary.targetId !== 'string' || !boundary.targetId || (boundary.nodeId !== null && (typeof boundary.nodeId !== 'string' || !boundary.nodeId)) || typeof boundary.relativePath !== 'string' || !Object.hasOwn(boundaryReasons, boundary.reasonCode)) throw new Error('INVALID_BOUNDARIES');
  }
}

// Partial reports are bound to the loaded job result, never to the active complete snapshot.
export function checkedScanBoundaryReport(value: ScanBoundaryReport, expected: { jobId: string; snapshotId: string; state: string; profileRevision: number }): ScanBoundaryReport {
  if (!value || value.jobId !== expected.jobId || value.snapshotId !== expected.snapshotId || value.state !== expected.state || !['complete', 'partial'].includes(value.state) || value.profileRevision !== expected.profileRevision || !Number.isSafeInteger(value.profileRevision) || value.profileRevision < 1 || typeof value.endedAt !== 'string' || value.endedAt.length > 64 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value.endedAt) || !Number.isFinite(Date.parse(value.endedAt))) throw new Error('BOUNDARY_REPORT_MISMATCH');
  checkedBoundaryList(value.boundaries, value.count, value.truncated);
  if (value.state === 'complete' && value.boundaries.some(boundary => !intentionalBoundary(boundary.reasonCode))) throw new Error('INVALID_COMPLETE_COVERAGE');
  return value;
}

// The latest loaded terminal result has its own heading, identity and end time.
export function ScanBoundaryNotes({ report, messages }: { report: ScanBoundaryReport; messages: Messages }) {
  return <section aria-label={messages['boundary.latestTitle']}><h3>{messages['boundary.latestTitle']}</h3>
    <p>{messages['probe.job']}: {report.jobId} · {report.state === 'partial' ? messages['boundary.partialResult'] : messages['boundary.completeResult']}</p>
    <p>{messages['boundary.endedAt']}: <RegionalTime value={report.endedAt}/> · {messages['snapshot.profileRevision']}: {report.profileRevision}</p>
    <p>{messages['boundary.separate']}</p><BoundaryList boundaries={report.boundaries} count={report.count} truncated={report.truncated} messages={messages} />
  </section>;
}

// Shared rendering does not merge counts from different generations.
function BoundaryList({ boundaries, count, truncated, messages }: { boundaries: ScanBoundary[]; count: string; truncated: boolean; messages: Messages }) {
  return <><p>{messages['boundary.count']}: {count}</p><p>{messages['boundary.records']}</p><p>{messages['boundary.liveSources']}</p>
    {truncated && <p className="notice">{messages['boundary.truncated']}</p>}
    {boundaries.length > 0 && <ul>{boundaries.map((boundary, index) => <li key={index}><strong>{messages[boundaryReasons[boundary.reasonCode]]}</strong><span> · {messages['boundary.target']}: {boundary.targetId} · {boundary.relativePath}</span><small>{messages[intentionalBoundary(boundary.reasonCode) ? 'boundary.intentional' : 'boundary.gap']}</small><p>{messages[`${boundaryReasons[boundary.reasonCode]}.help`]}</p></li>)}</ul>}
  </>;
}

// Explicit labels never classify special objects as normal file payloads.
export function nodeType(node: Node, messages: Messages): string {
  return messages[({ file: 'probe.file', directory: 'probe.directory', symlink: 'probe.symlink', fifo: 'node.fifo', socket: 'node.socket', blockDevice: 'node.blockDevice', characterDevice: 'node.characterDevice' } as const)[node.kind]];
}

// Link counts apply to regular files; familiar directory names remain cautious presentation hints.
export function nodeHints(node: Node, messages: Messages): string[] {
  const hints: string[] = [];
  if (node.kind === 'file' && node.linkCount !== null && node.linkCount > 1) hints.push(messages['node.hardlink']);
  if (node.kind === 'symlink') hints.push(messages['node.symlink']);
  if (['fifo', 'socket', 'blockDevice', 'characterDevice'].includes(node.kind)) hints.push(messages['node.special']);
  if (node.kind === 'directory' && node.originalName === '#recycle') hints.push(messages['node.recycle']);
  if (node.kind === 'directory' && node.originalName === '@eaDir') hints.push(messages['node.eaDir']);
  return hints;
}

// Package usage is a separate current measurement and is never added to the snapshot file sums.
export function StorageNotes({ data, messages }: { data: Pick<SnapshotResponse, 'scanBoundaries' | 'scanBoundaryCount' | 'scanBoundariesTruncated' | 'packageUsage'>; messages: Messages }) {
  return <section aria-label={messages['storage.title']}><h3>{messages['storage.title']}</h3>
    <p>{messages['storage.separate']}</p><dl><dt>{messages['probe.logical']}</dt><dd>{bytes(data.packageUsage.logicalBytes, messages)}</dd><dt>{messages['probe.allocated']}</dt><dd>{bytes(data.packageUsage.allocatedBytes, messages)}</dd><dt>{messages['storage.measuredAt']}</dt><dd><RegionalTime value={data.packageUsage.measuredAt}/></dd></dl>
    <p>{messages[({ complete: 'storage.complete', partial: 'storage.partial', unavailable: 'storage.unavailable' } as const)[data.packageUsage.state]]}</p>
    <h3>{messages['boundary.title']}</h3><BoundaryList boundaries={data.scanBoundaries} count={data.scanBoundaryCount} truncated={data.scanBoundariesTruncated} messages={messages} />
  </section>;
}

interface RevealSelection { profileId: string; snapshotId: string; nodeId: string; viewId?: string }
interface PendingParent { selection: RevealSelection; parentPath: string }

// Every click resolves the same identifiers again; a recovery notice never launches navigation itself.
export async function revealSelection(selection: RevealSelection, confirmedParent: string | null, dsmBuild: string, signal: AbortSignal,
  read: (parameters: Record<string, string>, signal: AbortSignal) => Promise<FileStationTarget> = readView,
  launch: typeof openFileStation = openFileStation): Promise<PendingParent | null> {
  signal.throwIfAborted();
  const target = await read({ action: 'filestation', ...selection }, signal);
  signal.throwIfAborted();
  if (target.noticeCode === 'FILE_REMOVED_PARENT_AVAILABLE') {
    if (target.kind !== 'directory' || typeof target.fileStationPath !== 'string' || target.parentPath !== undefined) throw new Error('INVALID_RECOVERY_TARGET');
    // If the surviving parent changed after the notice, obtain a new explicit user decision.
    if (confirmedParent !== target.fileStationPath) return { selection, parentPath: target.fileStationPath };
  } else if (confirmedParent !== null) throw new Error('RECOVERY_TARGET_CHANGED');
  await launch(target, { dsmBuild, signal });
  return null;
}

// Recovery is an explicit inline action, with no modal or automatic navigation.
export function ParentNotice({ messages, disabled, confirm }: { messages: Messages; disabled: boolean; confirm: () => void }) {
  return <div className="notice"><p role="status">{messages['probe.removedParent']}</p><button disabled={disabled} onClick={confirm}>{messages['probe.openParent']}</button></div>;
}

// Exact byte strings remain exact; missing measurements are never converted to zero.
// Visible numbers use the personal format; canonical byte strings stay unchanged.
function bytes(value: string | null, messages: Messages) { return value === null ? messages['probe.unknown'] : <><RegionalNumber value={value}/> B</>; }

// The table intentionally presents measured index rows, without implying completed map functionality.
export function IndexTable({ snapshot, messages, open, details, disabled, detailsDisabled = disabled, selectedNode, select, selectionDisabled = false }: {detailsDisabled?:boolean;selectionDisabled?:boolean;selectedNode?:string|null;select?:(node:Node)=>void; snapshot: Snapshot; messages: Messages; open: (node: Node) => void; details?: (node: Node) => void; disabled: boolean }) {
  return <><p className="snapshot-id">{messages['probe.snapshot']}: {messages['probe.saved']}</p>
    <p>{messages['snapshot.interval']}: <RegionalTime value={snapshot.startedAt}/> – <RegionalTime value={snapshot.endedAt}/></p>
    {snapshot.state === 'partial' && <p role="status" className="notice">{messages['snapshot.partialWarning']}</p>}
    <dl><dt>{messages['snapshot.capturedProfile']}</dt><dd>{snapshot.capturedProfileName}</dd><dt>{messages['snapshot.profileRevision']}</dt><dd>{snapshot.profileRevision}</dd><dt>{messages['snapshot.currentRevision']}</dt><dd>{snapshot.currentProfileRevision}</dd></dl>
    {snapshot.profileChanged && <p role="status" className="notice">{messages['snapshot.profileChanged']}</p>}
    {snapshot.truncated && <p role="status" className="notice">{messages['probe.truncated']}</p>}
    <p>{messages['node.pathSums']}</p><p>{messages['node.reflink']}</p>
    <div className="table-scroll"><table><thead><tr><th>{messages['probe.name']}</th><th>{messages['probe.type']}</th><th>{messages['probe.logical']}</th><th>{messages['probe.allocated']}</th><th>{messages['probe.action']}</th></tr></thead>
      <tbody>{snapshot.nodes.map(node => <tr key={node.id} aria-selected={selectedNode===node.id}><td>{select&&<button disabled={selectionDisabled} onClick={()=>select(node)}>{selectedNode===node.id?messages['view.selected']:messages['view.select']}</button>}{node.originalName}<small>{node.relativePath}</small></td><td>{nodeType(node, messages)}{nodeHints(node, messages).map(hint => <small key={hint}>{hint}</small>)}</td><td>{bytes(node.logicalBytes, messages)}</td><td>{bytes(node.allocatedBytes, messages)}</td><td>{details && <button disabled={detailsDisabled} onClick={() => details(node)}>{messages['paths.show']}</button>}{(node.kind === 'file' || node.kind === 'directory') && <button disabled={disabled} onClick={() => open(node)}>{node.kind === 'file' ? messages['probe.showFile'] : messages['probe.open']}</button>}</td></tr>)}</tbody></table></div></>;
}

// The persisted job mode is authoritative, including deduplicated submissions and later polling.
export function scanModeLabel(mode: string, messages: Messages): string {
  if (!['full', 'rebuild', 'reconcile'].includes(mode)) throw new Error('INVALID_SCAN_MODE');
  return messages[({ full: 'scan.full', rebuild: 'scan.rebuild', reconcile: 'scan.reconcile' } as const)[mode as ScanIntent['mode']]];
}

// Scan mutations and index-only reads have separate controls and explicit costs.
export function ScanControls({ messages, busy, hasSnapshot, pending, start, reload, scansDisabled = false }: { scansDisabled?: boolean; messages: Messages; busy: boolean; hasSnapshot: boolean; pending: PendingScan | null; start: (mode: ScanIntent['mode']) => void; reload: () => void }) {
  return <div className="scan-controls"><div className="probe-actions">
    {(!hasSnapshot || pending?.mode === 'full') && <button disabled={scansDisabled || busy || (pending !== null && pending.mode !== 'full')} onClick={() => start('full')}>{messages['scan.full']}</button>}
    <button disabled={scansDisabled || busy || (pending !== null && pending.mode !== 'rebuild')} onClick={() => start('rebuild')}>{messages['scan.rebuild']}</button>
    <button disabled={scansDisabled || busy || (pending !== null && pending.mode !== 'reconcile')} onClick={() => start('reconcile')}>{messages['scan.reconcile']}</button>
    <button disabled={busy} onClick={reload}>{messages['probe.refresh']}</button></div>
    {/* Cost explanations stay available without pushing the map below its controls. */}
    <details className="scan-help"><summary>{messages['help.scanTitle']}</summary>
      <p>{messages['scan.cost']}</p><p>{messages['scan.reloadOnly']}</p>
    </details>
    {pending && !busy && <p role="status">{messages['scan.retry']}: {scanModeLabel(pending.mode, messages)}</p>}
  </div>;
}

// Trigger labels describe the first accepted origin; a later join never replaces that evidence.
export function jobTriggerLabel(trigger: unknown, messages: Messages): string {
  if (trigger === 'ui') return messages['job.trigger.ui'];
  if (trigger === 'cli') return messages['job.trigger.cli'];
  if (trigger === 'scheduler') return messages['job.trigger.scheduler'];
  return messages['probe.unknown'];
}

// Status reflects the job returned by the service, never the last clicked button.
export function ScanJobStatus({ job, deduplicated, messages, language }: { language:'de'|'en';job: Job; deduplicated: boolean; messages: Messages }) {
  return <><p role="status">{messages['probe.job']}: {messages[`job.${job.state}`]} · {scanModeLabel(job.scanMode, messages)} · {messages['job.trigger']}: {jobTriggerLabel(job.trigger, messages)} · {messages['probe.entries']}: {job.entriesVisited} · {job.bytesObserved} B</p>
    {deduplicated && <p role="status">{messages['scan.deduplicated']}</p>}
    {/* Interrupted service work needs a new user-selected scan, never a fictional resume. */}
    {job.state === 'interrupted' && <p>{messages['scan.interruptedHelp']}</p>}
    {/* Only allowlisted storage codes select a fixed recovery explanation. */}
    <IndexSpaceHelp language={language} warnings={job.warnings??[]}/></>;
}

// Controls remain available while the scan is busy; reached pause and requested pause are distinct.
export function JobControls({job,control,pending,sending,messages,send}:{job:Job;control:ControlStatus|null;pending:ControlIntent|null;sending:boolean;messages:Messages;send:(action:ControlIntent['action'])=>void}) {
 if(!jobPolling(job.state))return null;
 const requested=control?.requested;
 const disabled=(action:ControlIntent['action'])=>sending||(pending!==null&&pending.action!==action)||requested==='cancel';
 return <div className="job-controls">
  {job.state!=='queued'&&job.state!=='paused'&&requested!=='pause'&&<button disabled={disabled('pause')} onClick={()=>send('pause')}>{messages['control.pause']}</button>}
  {(job.state==='paused'||requested==='pause')&&<button disabled={disabled('resume')} onClick={()=>send('resume')}>{messages['control.resume']}</button>}
  <button disabled={disabled('cancel')} onClick={()=>send('cancel')}>{messages['control.cancel']}</button>
  <p>{messages['control.cooperative']}</p>
  {requested==='pause'&&job.state!=='paused'&&<p role="status">{messages['control.pausePending']}</p>}
  {requested==='cancel'&&<p role="status">{messages['control.cancelPending']}</p>}
  {pending&&!sending&&<button onClick={()=>send(pending.action)}>{messages['control.retry']}</button>}
 </div>;
}

// Old outcomes remain readable but cannot acquire an invented capture provenance.
export function PartialControls({ job, snapshot, messages, busy, select, back }: { job: Outcome | null; snapshot: Snapshot | null; messages: Messages; busy: boolean; select: () => void; back: () => void }) {
  return <>
    {job?.snapshotState === 'partial' && validSnapshotTime(job.snapshotEndedAt) && snapshot?.snapshotId !== job.job.resultSnapshotId && <button disabled={busy} onClick={select}>{messages['snapshot.openPartial']}</button>}
    {job?.snapshotState === 'partial' && !validSnapshotTime(job.snapshotEndedAt) && <p role="status" className="notice">{messages['snapshot.partialUnqualified']}</p>}
    {snapshot?.state === 'partial' && <button disabled={busy} onClick={back}>{messages['snapshot.backComplete']}</button>}
  </>;
}

// Empty workspaces retain global discovery without a competing profile observer.
function UnselectedStatus({language,denied,profileNames,onSelectProfile}:{language:'de'|'en';denied:()=>void;profileNames:Readonly<Record<string,string>>;onSelectProfile:(id:string)=>void}){
 const [snapshot,setSnapshot]=useState<StatusSnapshot|null>(null);
 const [health,setHealth]=useState<'current'|'retrying'|'stopped'>('retrying');
 const owner=useRef<StatusObserver|null>(null);
 useEffect(()=>{
  const lifecycle=new StatusObserver(async signal=>{const value=await readStatus(signal);signal.throwIfAborted();setSnapshot(value);},setHealth,error=>error instanceof StatusReadError?[408,429,500,502,503,504].includes(error.status):error instanceof TypeError||error instanceof DOMException&&error.name==='TimeoutError',error=>{if(error instanceof SessionDenied)denied();});
  owner.current=lifecycle;
  const visibility=()=>lifecycle.visibility(document.visibilityState==='hidden');
  if(typeof document!=='undefined'){document.addEventListener?.('visibilitychange',visibility);if(document.visibilityState==='hidden')lifecycle.visibility(true);else lifecycle.start();}else lifecycle.start();
  return()=>{lifecycle.stop();if(typeof document!=='undefined')document.removeEventListener?.('visibilitychange',visibility);};
 },[]);
 // The same presenter receives global observations without starting a second profile poll.
 return <ScanStatusPanel language={language} snapshot={snapshot} health={health} progress={null} profileNames={profileNames} onSelectProfile={onSelectProfile} onRetry={()=>owner.current?.start()}/>;
}

// Each selected profile owns its request lifecycle, preventing late results from crossing selections.
function ProfileView({ onNotice, configurationEpoch = 0, configurationLocked = false, onSettingsDirty, display = defaultDisplay, defaultMap = 'treemap', theme = 'light', profileNames = {}, onStatusProfile, analysis, controller, saveView, area, profile, integrityIntent, profileName, retentionIntent, language, profileId, latestJobId, dsmBuild, messages, denied, pin, onPin }: {onNotice:(outcome:NoticeOutcome,report?:ScanBoundaryReport)=>void;configurationEpoch?:number;configurationLocked?:boolean;onSettingsDirty?:(dirty:boolean)=>void;display?:UserPreference['display'];defaultMap?:UserPreference['map'];theme?:'light'|'dark';profileNames?:Readonly<Record<string,string>>;onStatusProfile?:(id:string)=>void;analysis:ConfirmedAnalysis;controller:AnalysisController;saveView:()=>void;area:AnalysisArea;profile:Profile;integrityIntent:IntegrityIntentRef;profileName:string;retentionIntent:RetentionIntentRef;language:'de'|'en';pin?:ViewPin;onPin?:(pin:ViewPin|null)=>void; profileId: string; latestJobId: string | null; dsmBuild: string; messages: Messages; denied: () => void }) {
  const viewPin=useRef<ViewPin|null>(analysis.pin?.profileId===profileId?analysis.pin:pin?.profileId===profileId?pin:null);
  // Explicit index removal keeps its released analysis closed until a user opens a snapshot.
  const analysisClosed=useRef(false);
  const acquireID=useRef(crypto.randomUUID());
  const acquisitions=useRef(new ReaderAcquisitions());
  const initialized=useRef(false);
  const pendingView=useRef<ReaderResponse|null>(null);
  const switchIntent=useRef<ReaderIntent|null>(null);
  const [newSnapshot,setNewSnapshot]=useState<string|null>(null);
  const [expired,setExpired]=useState(false);
  // Selection is owned by the complete confirmed view, never by an independent node state.
  const selectedNode=analysis.state.view?.profileId===profileId?analysis.state.view.selectedNodeId:null;
  // Publish wire data and its immutable map model together after the same identity check.
  const [hierarchyBundle,setHierarchyBundle]=useState<{identity:string;wire:AnalysisHierarchy;model:MapModel}|null>(null);
  // Reject the previous view synchronously, before the effect starts its replacement query.
  const currentHierarchy=hierarchyBundle?.identity===hierarchyIdentity(analysis)?hierarchyBundle:null;
  const hierarchy=currentHierarchy?.wire??null;
  const mapModel=currentHierarchy?.model??null;
  const [hierarchyPending,setHierarchyPending]=useState(false);
  const [metricUnavailable,setMetricUnavailable]=useState(false);
  // Rendering derives its snapshot and selectors from the same atomic confirmed publication.
  const snapshot=analysis.pin?.profileId===profileId&&analysis.reader?checkedReader(analysis.reader,analysis.pin):null;
  const [storage, setStorage] = useState<Pick<SnapshotResponse, 'scanBoundaries' | 'scanBoundaryCount' | 'scanBoundariesTruncated' | 'packageUsage'> | null>(null);
  const [job, setJob] = useState<Outcome | null>(null);
  const [lastBoundaries, setLastBoundaries] = useState<ScanBoundaryReport | null>(null);
  const [boundaryFailure, setBoundaryFailure] = useState(false);
  const [busy, setBusy] = useState(true);
  const [notice, setNotice] = useState('');
  const [pendingParent, setPendingParent] = useState<PendingParent | null>(null);
  const alive = useRef<AbortController | null>(null);
  const intent = useRef<PendingScan | null>(null);
  const [deduplicated, setDeduplicated] = useState(false);
  const controlIntent=useRef<ControlIntent|null>(null);
  const controlSending=useRef(false);
  const [controlling,setControlling]=useState(false);
  // Explicit retries are independent from persisted navigation identity and history.
  const [hierarchyRetry,setHierarchyRetry]=useState(0);
  // Color selection only affects presentation, preserving the confirmed query and geometry.
  const [colorMode,setColorMode]=useState<ColorMode>(display.colorMode);
  const [detail,setDetail]=useState<DetailLevel>(display.detail);
  useEffect(()=>{setColorMode(display.colorMode);},[display.colorMode]);
  useEffect(()=>{setDetail(display.detail);},[display.detail]);
  const [mapFocus,setMapFocus]=useState<string|null>(null);
  // Virtual remainder inspection never enters persisted backend navigation or filesystem actions.
  const [inspectedGroup,setInspectedGroup]=useState<{identity:string;target:HitTarget}|null>(null);
  const groupTarget=inspectedGroup?.identity===hierarchyIdentity(analysis)&&mapModel?inspectedGroup.target:null;
  const selectedMapNode=groupTarget?(groupTarget.kind==='model'?groupTarget.nodeId:groupTarget.groupId):selectedNode;
  // Measurements belong to the mounted viewport, never to a placeholder size.
  const [mapViewport,setMapViewport]=useState<HTMLDivElement|null>(null);
  const [measuredViewport,setMeasuredViewport]=useState<{element:HTMLDivElement;snapshotId:string|undefined;width:number;height:number}|null>(null);
  const mapSize=area==='overview'&&measuredViewport?.element===mapViewport&&measuredViewport?.snapshotId===snapshot?.snapshotId?measuredViewport:null;
  // Display scaling remains local; confirmed personal theme comes from the shared provider.
  const [canvasEnvironment,setCanvasEnvironment]=useState<{devicePixelRatio:number;theme:'light'|'dark'}>({devicePixelRatio:1,theme:'light'});
  useEffect(()=>{
    const update=()=>setCanvasEnvironment({devicePixelRatio:window.devicePixelRatio||1,theme:'light'});
    update();window.addEventListener('resize',update);
    return()=>window.removeEventListener('resize',update);
  },[]);
  const budget=mapSize?renderBudget({kind:analysis.state.view?.map??'treemap',detail,...mapSize}):null;
  // Admit the first positive content box immediately; debounce only later resizes.
  useEffect(()=>{
    const element=mapViewport;
    setMeasuredViewport(null);
    if(!element||area!=='overview'||typeof ResizeObserver==='undefined')return;
    let active=true,measured=false;
    let timer:ReturnType<typeof setTimeout>|undefined;
    const observer=new ResizeObserver(entries=>{
      if(!active)return;
      const box=entries.find(entry=>entry.target===element)?.contentRect;
      if(!box)return;
      clearTimeout(timer);
      if(!Number.isFinite(box.width)||!Number.isFinite(box.height)||box.width<=0||box.height<=0){
        measured=false;setMeasuredViewport(null);return;
      }
      const width=Math.max(1,Math.min(16384,Math.round(box.width))),height=Math.max(1,Math.min(16384,Math.round(box.height)));
      const publish=()=>{if(active)setMeasuredViewport(old=>old?.element===element&&old.width===width&&old.height===height?old:{element,snapshotId:snapshot?.snapshotId,width,height});};
      if(!measured){measured=true;publish();}else timer=setTimeout(publish,150);
    });
    observer.observe(element);
    return()=>{active=false;observer.disconnect();clearTimeout(timer);};
  },[mapViewport,area,snapshot?.snapshotId]);

  const locked = useRef(true);
  const readSequence = useRef(0);
  // User transitions own publication and the busy lock independently of job polling.
  const transition = useRef({ epoch: 0, active: false, selectionOnly: false });
  const beginTransition = (selectionOnly = false) => {
    transition.current.active = true;
    transition.current.selectionOnly = selectionOnly;
    const epoch = ++transition.current.epoch;
    ++readSequence.current;
    controller.cancel();
    return epoch;
  };
  const endTransition = (epoch: number, signal?: AbortSignal) => {
    if (transition.current.epoch !== epoch) return;
    transition.current.active = false;
    transition.current.selectionOnly = false;
    locked.current = false;
    if (!signal?.aborted) setBusy(false);
  };
  const observer = useRef<StatusObserver | null>(null);
  const [statusSnapshot,setStatusSnapshot]=useState<StatusSnapshot|null>(null);
  const [progressObservation,setProgressObservation]=useState<ProgressObservation|null>(null);
  // One owner covers both job reads and terminal refreshes; replacement aborts all old effects.
  const pollOwner = useRef<{ id: string; request: AbortController; terminal: Outcome | null } | null>(null);
  // Start admission invalidates every outstanding control receipt before its transport settles.
  const jobMutationEpoch = useRef(0);
  const [pollFailure, setPollFailure] = useState<'retrying' | 'stopped' | null>(null);
  // Generation invalidation aborts pending path reads and external navigation before their effects.
  const analysisRequest=useRef<AbortController|null>(null);
  const fail = (error: unknown) => {
    setPendingParent(null);
    if (alive.current?.signal.aborted) return;
    if (error instanceof SessionDenied) { denied(); return; }
    if(error instanceof Error && error.message==='VIEW_EXPIRED'){setExpired(true);pendingView.current=null;switchIntent.current=null;}
    setNotice(readFailureMessage(error, messages));
  };
  const load = async (signal: AbortSignal, selection: Outcome | null = null, owner?: number) => {
    if (transition.current.active && owner !== transition.current.epoch) return;
    const epoch = transition.current.epoch;
    const ownsRead = () => epoch === transition.current.epoch && (!transition.current.active || owner === epoch);
    if(analysisClosed.current&&!selection)return;
    if(selection){setPendingParent(null);}
    const sequence = ++readSequence.current;
    let opened:string|null=null;let retained=false;
    let finishAcquisition:((retained:boolean)=>void)|undefined;
    try {
      const current=viewPin.current;
      const viewID=selection ? crypto.randomUUID() : current?.viewId??acquireID.current;
      if(selection||!current){
        opened=viewID;
        finishAcquisition=acquisitions.current.begin(viewID,()=>{
          // Another accepted request may have transferred ownership before its local publication.
          if(controller.current.pin?.viewId===viewID||viewPin.current?.viewId===viewID||pendingView.current?.lease?.id===viewID)return;
          // Released IDs are server tombstones and cannot be reused for a later acquisition.
          if(acquireID.current===viewID)acquireID.current=crypto.randomUUID();
          void requestReader({contractVersion:1,operation:'release',profileId,viewId:viewID},AbortSignal.timeout(8000)).catch(()=>{});
        });
      }
      const response=await requestReader<ReaderResponse>({contractVersion:1,operation:selection||!current?'open':'read',profileId,viewId:viewID,...(selection?{jobId:selection.job.id}:{})},signal);
      if(opened&&response.lease&&response.lease.id!==opened)throw new Error('SNAPSHOT_MISMATCH');
      const data=checkedReader(response,selection?null:current);
      if(selection&&(!data||data.state!=='partial'||data.snapshotId!==selection.job.resultSnapshotId||data.endedAt!==selection.snapshotEndedAt))throw new Error('PARTIAL_SELECTION_MISMATCH');
      if(!signal.aborted&&sequence===readSequence.current&&ownsRead()){
        if(!selection&&!current&&initialized.current&&data){pendingView.current=response;retained=true;setNewSnapshot(data.snapshotId);return;}
        const old=viewPin.current;
        if(selection&&data)controller.rememberPartial(data.snapshotId,selection.job.id);
        if(!await controller.accept(profileId,response,signal,controller.current.state.view?.profileId!==profileId&&response.view.snapshotId?{...defaultView(profileId,response.view.snapshotId),map:defaultMap}:undefined,controller.current.state.view?.profileId!==profileId?'navigate':'replace'))return;
        if(signal.aborted||sequence!==readSequence.current||!ownsRead())return;
        retained=true;
        if(response.lease&&data){viewPin.current={profileId,viewId:response.lease.id,snapshotId:data.snapshotId,nodeId:controller.current.state.view?.selectedNodeId??null};onPin?.(viewPin.current);}
        else {viewPin.current=null;onPin?.(null);}
        setStorage(checkedStorage(response.view));setNewSnapshot(response.newCompleteSnapshotId);setExpired(false);initialized.current=true;
        if(selection)saveView();
        if(selection&&old&&old.viewId!==viewID)void requestReader({contractVersion:1,operation:'release',profileId,viewId:old.viewId},signal).catch(()=>{});
        return true;
      }
    } catch (error) {
      if (signal.aborted || sequence !== readSequence.current || !ownsRead()) return;
      if(error instanceof Error && error.message==='VIEW_EXPIRED')setExpired(true);
      if (error instanceof ReadFailure && error.status === 404) { controller.clear();setStorage(null); return; }
      throw error;
    } finally {
      // Superseded opens cannot release a coalesced lease still needed by another load.
      finishAcquisition?.(retained);
    }
  };
  // Read this independently: a partial result must not replace the active complete generation.
  const loadLastBoundaries = async (outcome: Outcome, signal: AbortSignal) => {
    setLastBoundaries(null); setBoundaryFailure(false);
    if (outcome.job.state !== 'completed') return;
    if (!outcome.job.resultSnapshotId) { setBoundaryFailure(true); return; }
    try {
      const value = await readView<ScanBoundaryReport>({ action: 'scan-boundaries', profileId, jobId: outcome.job.id }, signal);
      const report = checkedScanBoundaryReport(value, { jobId: outcome.job.id, snapshotId: outcome.job.resultSnapshotId, state: outcome.snapshotState, profileRevision: outcome.job.profileRevision });
      if (!signal.aborted) { setLastBoundaries(report); return report; }
    } catch (error) {
      if (signal.aborted) return;
      if (error instanceof SessionDenied) throw error;
      setBoundaryFailure(true);
    }
  };
  useEffect(() => {
    const controller = new AbortController(); alive.current = controller;
    // Re-read the latest backend job after focus reauthentication instead of retaining job metadata.
    void poll(latestJobId ?? '', controller.signal);
    void load(controller.signal).catch(fail).finally(() => {
      if (!controller.signal.aborted && !transition.current.active) { locked.current = false; setBusy(false); }
    });
    const visibility=()=>observer.current?.visibility(document.visibilityState==='hidden');
    if(typeof document!=='undefined')document.addEventListener?.('visibilitychange',visibility);
    return () => { controller.abort(); pollOwner.current?.request.abort(); observer.current?.stop(); if(typeof document!=='undefined')document.removeEventListener?.('visibilitychange',visibility); };
    // ProfileView is keyed by profile ID, so its request lifetime matches one immutable selection.
  }, [profileId]);
  const poll = async (id: string, signal: AbortSignal, terminal: Outcome | null = null) => {
    pollOwner.current?.request.abort(); observer.current?.stop();
    const owner = { id, request: new AbortController(), terminal };
    pollOwner.current = owner;
    let refreshedTerminal:string|null=null;
    const lifecycleSignal=AbortSignal.any([signal,owner.request.signal]);
    // Existing mutation/control owner identity remains authoritative across observation rounds.
    const live=()=>!lifecycleSignal.aborted&&pollOwner.current===owner;
    const run = async (roundSignal:AbortSignal):Promise<void> => {
      const requestSignal=AbortSignal.any([lifecycleSignal,roundSignal]);
      const current=()=>live()&&!requestSignal.aborted;
      const epoch=transition.current.epoch;
      const ownsCompletion=()=>current()&&epoch===transition.current.epoch&&!transition.current.active;
      if(owner.id){
        const outcome=owner.terminal??await readView<Outcome>({action:'job',id:owner.id},requestSignal);
        if(!current())return;
        if(outcome.job?.id!==owner.id||outcome.job.profileId!==profileId)throw Error('JOB_MISMATCH');
        scanModeLabel(outcome.job.scanMode,messages);
        if(!['queued','preflight','scanning','paused','aggregating','validating','publishing','completed','failed','cancelled','interrupted'].includes(outcome.job.state))throw Error('INVALID_JOB_STATE');
        outcome.control=checkedControl(outcome.control);setJob(outcome);
        if(!jobPolling(outcome.job.state)){
          owner.terminal=outcome;intent.current=null;controlIntent.current=null;
          if(ownsCompletion()&&refreshedTerminal!==outcome.job.id){
            // Qualified terminal observations feed one session-owned, deduplicated notice store.
            onNotice(outcome);
            const refreshSignal=AbortSignal.any([requestSignal,AbortSignal.timeout(8000)]);
            let report:ScanBoundaryReport|undefined;
            if(outcome.job.state==='completed') [,report]=await Promise.all([load(refreshSignal),loadLastBoundaries(outcome,refreshSignal)]);else await load(refreshSignal);
            refreshSignal.throwIfAborted();
            if(ownsCompletion()){
              if(report)onNotice(outcome,report);
              refreshedTerminal=outcome.job.id;locked.current=false;setBusy(false);
            }
          }
        }
      }
      // Queue and history are separate observations, published together only after both qualify.
      const status=await readStatus(requestSignal);if(!current())return;
      const progress=await readProgressObservation(profileId,owner.id||null,requestSignal);if(!current())return;
      setStatusSnapshot(status);setProgressObservation(progress);
      const queued=[...(status.active?[status.active]:[]),...status.queued].find(j=>j.profileId===profileId)?.jobId;
      const newest=progress.history[0];
      const laterHistory=newest&&(!owner.id||owner.terminal&&Date.parse(newest.createdAt)>=Date.parse(owner.terminal.job.createdAt))?newest.jobId:undefined;
      const external=queued??laterHistory;
      if(external&&external!==owner.id){++jobMutationEpoch.current;controlIntent.current=null;owner.id=external;owner.terminal=null;refreshedTerminal=null;}
    };
    const retryable=(error:unknown)=>error instanceof ReadFailure||error instanceof StatusReadError?[408,429,500,502,503,504].includes(error.status):error instanceof TypeError||error instanceof DOMException&&error.name==='TimeoutError';
    const lifecycle=new StatusObserver(run,state=>{
      if(!live())return;setPollFailure(state==='current'?null:state);
      if(state!=='current'&&!transition.current.active){locked.current=false;setBusy(false);}
    },retryable,error=>{if(!live())return;if(error instanceof SessionDenied){owner.request.abort();setPollFailure(null);}fail(error);});
    observer.current=lifecycle;setPollFailure('retrying');
    if(typeof document!=='undefined'&&document.visibilityState==='hidden')lifecycle.visibility(true);else lifecycle.start();
  };
  const start = async (mode: ScanIntent['mode']) => {
    const signal = alive.current?.signal;
    if (!profile.active || !signal || signal.aborted || locked.current || (intent.current !== null && intent.current.mode !== mode)) return;
    // Suspend the old read owner before a new mutation intent can be admitted.
    const previousPoll = pollOwner.current; previousPoll?.request.abort(); pollOwner.current = null; ++jobMutationEpoch.current; observer.current?.stop(); setPollFailure('retrying');
    locked.current = true; setBusy(true); setNotice(''); setPendingParent(null);  setLastBoundaries(null); setBoundaryFailure(false); setDeduplicated(false);
    // Retain the same key on uncertain delivery; no automatic retry creates another user intent.
    try {
      intent.current = scanIntent(intent.current, mode);
      const response = await requestScan({ contractVersion: 1, profileId, mode: intent.current.mode }, intent.current.key, AbortSignal.any([signal, AbortSignal.timeout(8000)])) as { contractVersion: number; data: Outcome & { deduplicated: boolean } };
      if (signal.aborted) return;
      if (response.contractVersion !== 1 || response.data?.job?.profileId !== profileId || !response.data.job.id || typeof response.data.deduplicated !== 'boolean') throw new Error('INVALID_SCAN_RESPONSE');
      scanModeLabel(response.data.job.scanMode, messages);
      setDeduplicated(response.data.deduplicated);
      controlIntent.current = null;
      setJob(response.data); await poll(response.data.job.id, signal);
    } catch (error) {
      // A definite rejection has no unresolved new action; transport uncertainty retains the original intent.
      if (error instanceof Error && ['JOB_CONFLICT', 'QUEUE_FULL', 'IDEMPOTENCY_CONFLICT', 'INVALID_REQUEST'].includes(error.message)) {
        intent.current = null;
        if (previousPoll && !signal.aborted) void poll(previousPoll.id, signal, previousPoll.terminal);
      }
      locked.current = false; if (!signal.aborted) { setBusy(false); fail(error); } }
  };
  // Job control has its own request lock; ordinary scan-busy state must not disable abort or resume.
  const controlJob=async(action:ControlIntent['action'])=>{
    const signal=alive.current?.signal;
    const controlledOwner = pollOwner.current;
    if(!signal||signal.aborted||!job||controlSending.current||!controlledOwner||controlledOwner.request.signal.aborted||controlledOwner.id!==job.job.id)return;
    const mutationEpoch = jobMutationEpoch.current;
    const controlledID = job.job.id;
    const ownsControl = () => !signal.aborted && !controlledOwner.request.signal.aborted && pollOwner.current === controlledOwner && controlledOwner.id === controlledID && jobMutationEpoch.current === mutationEpoch;
    if(controlIntent.current&&(controlIntent.current.action!==action||controlIntent.current.jobId!==job.job.id))return;
    controlIntent.current??={jobId:job.job.id,action,key:crypto.randomUUID()};
    controlSending.current=true;setControlling(true);
    const pending=controlIntent.current;
    try{
      const status=await requestJobControl(pending,AbortSignal.any([signal,AbortSignal.timeout(8000)]));
      if(!ownsControl())return;
      const id=pending.jobId;controlIntent.current=null;
      setJob(previous=>previous?.job.id===id?{...previous,control:status}:previous);
      setNotice(messages['control.accepted']);
      // A pre-control read must not overwrite the newly acknowledged control version.
      void poll(id, signal);
    }catch(error){
      if(!ownsControl())return;
      if(error instanceof Error&&['JOB_CONFLICT','IDEMPOTENCY_CONFLICT','INVALID_REQUEST'].includes(error.message)){controlIntent.current=null;setNotice(messages['control.conflict']);}
      else fail(error);
    }finally{controlSending.current=false;if(!signal.aborted)setControlling(false);}
  };
  // Refreshes share the action lock so repeated clicks cannot race scan publication.
  const refresh = async () => {
    const signal = alive.current?.signal;
    if (!signal || signal.aborted || locked.current) return;
    const owner = beginTransition();
    locked.current = true; setBusy(true); setNotice(''); setPendingParent(null);
    try { if(analysisClosed.current){analysisClosed.current=false;initialized.current=false;} const [accepted]=await Promise.all([load(signal, null, owner), job ? loadLastBoundaries(job, signal) : Promise.resolve()]);
      if(accepted&&!signal.aborted&&transition.current.epoch===owner)setHierarchyRetry(value=>value+1); } catch (error) { fail(error); }
    finally { endTransition(owner, signal); }
  };
  // Returning from a partial is an explicit reset to the complete default, even if none exists.
  const backComplete = async () => {
    const signal=alive.current?.signal;if(!signal||signal.aborted||locked.current)return;
    analysisClosed.current=false;
    const old=viewPin.current;viewPin.current=null;acquireID.current=crypto.randomUUID();initialized.current=false;pendingView.current=null;setPendingParent(null);
    const owner = beginTransition();
    locked.current=true;setBusy(true);
    try{await load(signal, null, owner);if(old)void requestReader({contractVersion:1,operation:'release',profileId,viewId:old.viewId},signal).catch(()=>{});}catch(error){fail(error);}finally{endTransition(owner, signal);}
  };
  // Selecting a partial is an explicit read and never changes the server's complete pointer.
  const selectPartial = async () => {
    const signal = alive.current?.signal;
    if (!signal || signal.aborted || locked.current || !job || job.snapshotState !== 'partial' || !validSnapshotTime(job.snapshotEndedAt)) return;
    const owner = beginTransition();
    locked.current = true; setBusy(true); setNotice('');
    try { analysisClosed.current=false; await load(signal, job, owner); } catch (error) { fail(error); }
    finally { endTransition(owner, signal); }
  };
  // Verified unusability invalidates only the matching displayed generation, never another profile.
  const snapshotUnavailable = (ownerProfileId:string,snapshotId:string,quality:string) => {
    if(ownerProfileId!==profileId)return;
    const current=viewPin.current;
    if(pendingView.current?.lease?.snapshotId===snapshotId){pendingView.current=null;setNewSnapshot(null);}
    if(current?.snapshotId!==snapshotId)return;
    // Invalidation owns clearing the cancelled action; its late finally cannot unlock a successor.
    analysisRequest.current?.abort();analysisRequest.current=null;locked.current=false;setBusy(false);
    ++readSequence.current;analysisClosed.current=true;controller.clear();viewPin.current=null;onPin?.(null);
    setStorage(null);setPendingParent(null);setNewSnapshot(null);setExpired(true);
    const reason=language==='de'?({corrupt:'beschädigt',unsupported:'nicht unterstütztes Format',deleting:'Löschung ausstehend',deleted:'gelöscht',unverified:'Integrität nicht bestätigt'} as Record<string,string>)[quality]:({corrupt:'corrupt',unsupported:'unsupported format',deleting:'deletion pending',deleted:'deleted',unverified:'integrity not confirmed'} as Record<string,string>)[quality];
    setNotice(language==='de'?`Dieser Datenstand ist nicht nutzbar: ${reason??'nicht bestätigt'}. Prüfe die Wiederherstellung oder wähle einen anderen gespeicherten Stand.`:`This snapshot is unavailable: ${reason??'not verified'}. Review recovery or select another stored snapshot.`);
    const signal=alive.current?.signal;
    if(signal&&!signal.aborted)void requestReader({contractVersion:1,operation:'release',profileId,viewId:current.viewId},signal).catch(()=>{});
  };
  // A confirmed deletion releases only this browser's matching leases; other readers stay protected.
  const beforeIndexDelete = async (snapshotId:string,ownerProfileId:string) => {
    if(ownerProfileId!==profileId)return;
    const signal=alive.current?.signal;if(!signal||signal.aborted||locked.current)throw new Error('VIEW_BUSY');
    const current=viewPin.current;const pending=pendingView.current?.lease;
    const matches=Boolean(current?.snapshotId===snapshotId||pending?.snapshotId===snapshotId);
    if(!matches)return;
    locked.current=true;setBusy(true);++readSequence.current;
    try{
      if(current?.snapshotId===snapshotId)await requestReader({contractVersion:1,operation:'release',profileId,viewId:current.viewId},signal);
      if(pending?.snapshotId===snapshotId&&pending.id!==current?.viewId)await requestReader({contractVersion:1,operation:'release',profileId,viewId:pending.id},signal);
      signal.throwIfAborted();
      if(pending?.snapshotId===snapshotId)pendingView.current=null;
      if(!current||current.snapshotId===snapshotId){
        analysisClosed.current=true;controller.clear();viewPin.current=null;onPin?.(null);acquireID.current=crypto.randomUUID();
        setStorage(null);setPendingParent(null);setNewSnapshot(null);
      }
    }finally{locked.current=false;if(!signal.aborted)setBusy(false);}
  };
  // A user click is the only path that replaces the pinned generation.
  const adopt = async (targetSnapshot?:string) => {
    const signal=alive.current?.signal;if(!signal||signal.aborted||locked.current)return;
    const owner = beginTransition();
    locked.current=true;setBusy(true);setPendingParent(null);const sequence=++readSequence.current;
    try{
      const old=viewPin.current;const nextID=crypto.randomUUID();
      let candidate:ReaderIntent;
      if(!targetSnapshot&&pendingView.current?.lease){candidate={contractVersion:1,operation:'read',profileId,viewId:pendingView.current.lease.id};}
      else if(expired||!old){candidate={contractVersion:1,operation:'open',profileId,viewId:nextID,...(targetSnapshot?{snapshotId:targetSnapshot}:{})};}
      else {if(!targetSnapshot&&!newSnapshot&&!switchIntent.current)return;candidate={contractVersion:1,operation:'switch',profileId,viewId:old.viewId,newViewId:nextID,snapshotId:targetSnapshot??newSnapshot??undefined,...(selectedNode?{nodeId:selectedNode}:{})};}
      // An uncertain response preserves the entire request, even if background pointers or selection change.
      switchIntent.current=retainReaderIntent(switchIntent.current,candidate);
      const response=await requestReader<ReaderResponse>(switchIntent.current,signal);
      pendingView.current=null;
      const data=checkedReader(response,null);
      if(signal.aborted||sequence!==readSequence.current)return;
      if(!await controller.accept(profileId,response,signal,undefined,'navigate'))return;
      if(signal.aborted||sequence!==readSequence.current)return;
      analysisClosed.current=false;
      switchIntent.current=null;setStorage(checkedStorage(response.view));setNewSnapshot(response.newCompleteSnapshotId);setExpired(false);setNotice(response.selection.fallback==='ancestor'?messages['view.ancestor']:response.selection.fallback==='scope-unavailable'?messages['view.scopeUnavailable']:'');
      viewPin.current=response.lease&&data?{profileId,viewId:response.lease.id,snapshotId:data.snapshotId,nodeId:controller.current.state.view?.selectedNodeId??null}:null;
      onPin?.(viewPin.current);saveView();
      if(old&&old.viewId!==response.lease?.id)void requestReader({contractVersion:1,operation:'release',profileId,viewId:old.viewId},signal).catch(()=>{});
    }catch(error){fail(error);}finally{endTransition(owner, signal);}
  };
  // Visible periodic reads renew only this pin; they never adopt a newly published pointer.
  useEffect(()=>{const interval=setInterval(()=>{const signal=alive.current?.signal;if(signal&&!signal.aborted&&document.visibilityState==='visible'&&!expired){const current=viewPin.current;const epoch=transition.current.epoch;if(transition.current.active)return;if(locked.current&&current){void settlePinnedRead(requestReader<ReaderResponse>({contractVersion:1,operation:'read',profileId,viewId:current.viewId},signal),()=>!signal.aborted&&!transition.current.active&&transition.current.epoch===epoch&&viewPin.current?.viewId===current.viewId,value=>{checkedReader(value,current);setNewSnapshot(value.newCompleteSnapshotId);},fail);}else if(!locked.current)void load(signal).catch(fail);}},30000);return()=>clearInterval(interval);},[profileId,expired]);
  // Cross-profile history and restore publish one qualified bundle before the child reads anything.
  useEffect(()=>{
    if(analysis.pin?.profileId!==profileId||!analysis.reader)return;
    viewPin.current=analysis.pin;
setStorage(checkedStorage(analysis.reader.view));
    setNewSnapshot(analysis.reader.newCompleteSnapshotId);setExpired(false);
    analysisRequest.current?.abort();setPendingParent(null);
  },[analysis.pin?.viewId,analysis.state.epoch]);
  // Secondary readers wait for this exact first hierarchy attempt, including budget retries.
  // A fresh owner prevents an A-B-A publication from inheriting an earlier completion.
  // A single transition may hand its in-flight read to the confirmed hierarchy effect.
  // The promise always settles as data, so a discarded early rejection has no side effects.
  const preparedHierarchy=useRef<null|{key:string;signal:AbortSignal;abort:()=>void;result:Promise<{value:AnalysisHierarchy|null}|{error:unknown}>}>(null);
  useEffect(()=>()=>{preparedHierarchy.current?.abort();preparedHierarchy.current=null;},[]);
  const hierarchyKey = hierarchyIdentity(analysis);
  const attemptKey=JSON.stringify([hierarchyKey,budget?.depth??null,budget?.elementBudget??null,hierarchyRetry]);
  const hierarchyAdmission=useRef({key:attemptKey});
  if(hierarchyAdmission.current.key!==attemptKey)hierarchyAdmission.current={key:attemptKey};
  const attemptOwner=hierarchyAdmission.current;
  const [settledHierarchyOwner,setSettledHierarchyOwner]=useState<typeof attemptOwner|null>(null);
  const secondaryReadsReady=!!budget&&!!hierarchyKey&&settledHierarchyOwner===attemptOwner;
  // Availability belongs to this publication and lease, including ABA transitions and session teardown.
  const metricKey=JSON.stringify([hierarchyIdentity(analysis),analysis.state.epoch]);
  const [metricAvailability,setMetricAvailability]=useState<{key:string;state:'ready'|'unavailable';allocated:boolean}|null>(null);
  const [metricRetry,setMetricRetry]=useState(0);
  const currentMetricAvailability=metricAvailability?.key===metricKey?metricAvailability:null;
  useEffect(()=>{
    const view=analysis.state.view,pin=analysis.pin,session=alive.current?.signal;
    if(!secondaryReadsReady||!view||!pin||view.profileId!==profileId||!session||session.aborted||expired||busy||area!=='overview')return;
    const request=new AbortController(),signal=AbortSignal.any([session,request.signal]);
    const owns=()=>!signal.aborted&&!transition.current.active&&JSON.stringify([hierarchyIdentity(controller.current),controller.current.state.epoch])===metricKey;
    setMetricAvailability(null);
    void readMetricAvailability(view,pin,signal).then(value=>{if(owns())setMetricAvailability({key:metricKey,state:'ready',allocated:value.allocated});}).catch(error=>{
      if(!owns())return;
      setMetricAvailability({key:metricKey,state:'unavailable',allocated:false});
      if(error instanceof SessionDenied||error instanceof Error&&error.message==='VIEW_EXPIRED')fail(error);
    });
    return()=>request.abort();
  },[metricKey,profileId,expired,busy,area,metricRetry,secondaryReadsReady]);
  useEffect(()=>{
    const view=analysis.state.view;const pin=analysis.pin;
    if(!view||!pin||view.profileId!==profileId||!budget){setHierarchyBundle(null);setHierarchyPending(false);return;}
    const request=new AbortController();const identity=hierarchyKey;
    const prepared=preparedHierarchy.current;
    preparedHierarchy.current=null;
    const handed=prepared?.key===attemptKey&&!prepared.signal.aborted?prepared:null;
    if(prepared&&!handed)prepared.abort();
    let first=true;let timer:ReturnType<typeof setTimeout>|undefined;
    setHierarchyBundle(null);setHierarchyPending(true);setMetricUnavailable(false);
    const run=async()=>{try{
      // Consume the preparation once; pending retries use the ordinary current-view path.
      const carried=first&&handed?await handed.result:null;first=false;
      if(request.signal.aborted||handed?.signal.aborted)return;
      if(carried&&'error' in carried)throw carried.error;
      const result=carried&&'value' in carried?carried.value:await readAnalysis(view,pin,request.signal,fetch,budget);
      if(request.signal.aborted||hierarchyIdentity(controller.current)!==identity)return;
      const model=result?adaptHierarchy(result,{snapshotId:view.snapshotId,rootId:view.rootId,metric:view.metric,mode:view.filterMode,maxNodes:budget.elementBudget}):null;
      setHierarchyBundle(result&&model?{identity,wire:result,model}:null);setHierarchyPending(result===null);
      if(result===null)timer=setTimeout(()=>{void run();},1500);
    }catch(error){if(!request.signal.aborted&&hierarchyIdentity(controller.current)===identity){setHierarchyPending(false);setMetricUnavailable(error instanceof Error&&error.message==='METRIC_UNAVAILABLE');fail(error);}}finally{
      // Pending is a completed attempt too: filter preparation must not starve independent search.
      if(!request.signal.aborted&&hierarchyAdmission.current===attemptOwner&&hierarchyIdentity(controller.current)===identity)setSettledHierarchyOwner(attemptOwner);
    }};
    void run();return()=>{request.abort();handed?.abort();clearTimeout(timer);};
  },[hierarchyKey,profileId,hierarchyRetry,budget?.depth,budget?.elementBudget,attemptOwner]);
  // Only row selection and drill requests may supersede an unfinished selection.
  const navigate=async(view:PreferenceView,history:'navigate'|'back'|'forward'='navigate',intent:'navigation'|'selection'|'drill'='navigation')=>{
    if(expired||switchIntent.current!==null||locked.current&&!(transition.current.active&&transition.current.selectionOnly&&intent!=='navigation'))return;
    const owner = beginTransition(intent==='selection');
    locked.current=true;setBusy(true);setPendingParent(null);analysisRequest.current?.abort();
    const previous=controller.current;const current=previous.state.view;const session=alive.current?.signal;
    // Only root navigation within the current qualified container model/breadcrumb is eligible.
    const known=mapModel?.nodes[view.rootId]??previous.navigation?.breadcrumb.find(part=>part.id===view.rootId);
    const eligible=intent!=='selection'&&!!budget&&!!session&&!session.aborted&&!!current&&!!previous.pin&&view.rootId!==current.rootId&&
      !!known&&['nas','volume','share','other','directory'].includes(known.kind)&&
      hierarchyIdentity({...previous,state:{...previous.state,view:{...view,rootId:current.rootId}}})===hierarchyIdentity(previous);
    preparedHierarchy.current?.abort();preparedHierarchy.current=null;
    try{await controller.move(view,history,eligible?(target,pin,signal)=>{
      const request=new AbortController();const bound=AbortSignal.any([session!,request.signal]);
      // Navigation owns cancellation until confirmation; the hierarchy effect owns it afterwards.
      const cancel=()=>request.abort();signal.addEventListener('abort',cancel,{once:true});
      if(signal.aborted)cancel();
      const identity=hierarchyIdentity({...previous,state:{...previous.state,view:target},pin});
      const preparation={key:JSON.stringify([identity,budget!.depth,budget!.elementBudget,hierarchyRetry]),signal:bound,abort:()=>request.abort(),
        result:readAnalysis(target,pin,bound,fetch,budget!).then(value=>({value}),error=>({error}))};
      preparedHierarchy.current=preparation;
      return{confirm:()=>signal.removeEventListener('abort',cancel),discard:()=>{signal.removeEventListener('abort',cancel);preparation.abort();if(preparedHierarchy.current===preparation)preparedHierarchy.current=null;}};
    }:undefined);if(transition.current.epoch===owner)saveView();}catch(error){if(transition.current.epoch===owner)fail(error);}finally{endTransition(owner, alive.current?.signal);}
  };
  const changeView=(patch:Partial<PreferenceView>)=>{const view=controller.current.state.view;if(view)void navigate({...view,...patch});};
  const open = async (selection: RevealSelection, confirmedParent: string | null = null) => {
    const signal = alive.current?.signal;
    if (!signal || !snapshot || (snapshot.state === 'partial' || expired) || signal.aborted || locked.current || selection.profileId !== profileId || selection.snapshotId !== snapshot.snapshotId) return;
    // Preserve the exact lease of the selected historical generation through parent confirmation.
    const pin=viewPin.current;if(!pin||pin.snapshotId!==selection.snapshotId||selection.viewId&&selection.viewId!==pin.viewId)return;
    selection={...selection,viewId:pin.viewId};
    locked.current = true; setBusy(true); setNotice(''); setPendingParent(null);
    const request=new AbortController();analysisRequest.current?.abort();analysisRequest.current=request;
    const bounded=AbortSignal.any([signal,request.signal]);const sequence=readSequence.current;
    // Every late result and external effect belongs to this exact request and lease.
    const owns=()=>!bounded.aborted&&analysisRequest.current===request&&sequence===readSequence.current&&viewPin.current?.viewId===pin.viewId;
    let openedKind:FileStationTarget["kind"]|null=null;
    try {
      const pending = await revealSelection(selection, confirmedParent, dsmBuild, bounded, async(parameters, readSignal)=>{const target=await readView<FileStationTarget>(parameters,readSignal);if(!owns())throw new DOMException("Superseded", "AbortError");return target;},async(target,options)=>{if(!owns())throw new DOMException("Superseded","AbortError");const result=await openFileStation(target,options);openedKind=target.kind;return result;});
      if (!bounded.aborted&&sequence===readSequence.current&&viewPin.current?.snapshotId===selection.snapshotId) {
        setPendingParent(pending);
        if (!pending) setNotice(openedKind === 'file' ? messages['probe.parentRequested'] : messages['probe.openRequested']);
      }
    } catch (error) { if(owns())fail(error); }
    finally { if(analysisRequest.current===request&&sequence===readSequence.current){locked.current=false;if(!signal.aborted)setBusy(false);} }
  };
  // Both maps and qualified child pages select through the same confirmed controller.
  const selectNode = (node: Pick<AnalysisNode,'id'|'kind'|'realNodeId'>) => {
    const view=controller.current.state.view;if(!view)return;
    if(node.kind==='group')return;
    setInspectedGroup(null);setMapFocus(node.id);
    void navigate({...view,selectedNodeId:node.realNodeId??node.id},'navigate','selection');
  };
  const selectionDisabled = expired || switchIntent.current !== null || (busy && !jobPolling(job?.job.state));
  // General navigation and external effects remain locked during selection qualification.
  const rowInteractionDisabled = expired || switchIntent.current !== null || (selectionDisabled && !(transition.current.active && transition.current.selectionOnly));
  // Validate local groups against this exact model; group identifiers never become navigation roots.
  const inspectGroup=(target:HitTarget)=>{
    if(!mapModel||rowInteractionDisabled)return;
    if(target.kind==='model'){if(mapModel.nodes[target.nodeId]?.kind!=='group')return;setMapFocus(target.nodeId);}
    else {try{if(groupDirectChildren(mapModel,target.parentId,target.memberIds).id!==target.groupId)return;}catch{return;}}
    setInspectedGroup({identity:hierarchyIdentity(analysis),target});
  };
  const selectMapNode=(id:string)=>{const node=mapModel?.nodes[id];if(node)selectNode(node);};
  const openMapNode=(id:string)=>{
    const node=mapModel?.nodes[id],view=controller.current.state.view;
    if(node&&view&&node.id!==view.rootId&&['nas','volume','share','other','directory'].includes(node.kind))void navigate({...view,rootId:id,selectedNodeId:null},'navigate','drill');
  };
  // Empty and no-result states require a complete, qualified query rather than zero bytes alone.
  const sourceGap = snapshot?.scanBoundaries.some(boundary => /^(SOURCE_|SCOPE_)/.test(boundary.reasonCode)) ?? false;
  const rootSummary = hierarchy?.nodes.find(node => node.id === hierarchy.rootId);
  const emptyRecordedRoot = hierarchy !== null && !hierarchy.truncated && hierarchy.nodes.length <= 1 &&
    !rootSummary?.hasMore && hierarchy.originalSummary.fileCount === '0' && hierarchy.originalSummary.specialCount === '0' &&
    BigInt(hierarchy.originalSummary.directoryCount) <= 1n && !sourceGap && snapshot?.state === 'complete';
  const filtered = analysis.state.view?.filter;
  const hasFilter = !!analysis.state.view?.search.text || !!filtered && (filtered.categories.length > 0 || !!filtered.text || !!filtered.objectKinds?.length ||
    filtered.minSize !== undefined || filtered.maxSize !== undefined || !!filtered.modifiedFrom || !!filtered.modifiedTo);
  const noFilterMatches = !!hierarchy && hasFilter && hierarchy.matchSummary.fileCount === '0' &&
    hierarchy.matchSummary.directoryCount === '0' && hierarchy.matchSummary.specialCount === '0';
  const contentState: ContentState = expired || metricUnavailable ? 'unavailable' : !snapshot ?
    (busy ? 'loading' : storage ? 'no-snapshot' : 'unavailable') : hierarchyPending ? 'loading' : !hierarchy ? 'unavailable' :
    noFilterMatches ? 'no-hits' : emptyRecordedRoot ? 'empty' : 'data';
  // Conflicting remembered predicates require an explicit filter action, never silent replacement.
  let filterTextConflict=false;try{if(analysis.state.view)effectiveAnalysisFilter(analysis.state.view);}catch{filterTextConflict=true;}
  const scopeLabel = profile.scope && ['nas','volumes','shares','mixed'].includes(profile.scope.kind) ? messages[`shell.scope.${profile.scope.kind}`] : messages['shell.scopeUnknown'];
  return <>
    {/* The scan owner stays mounted; the native disclosure only changes presentation. */}
    <details className="scan-tools" open={!snapshot||intent.current!==null}><summary>{language==='de'?'Datenstand und Scan':'Snapshot and scan'}</summary>
    <div className="analysis-toolbar" aria-label={messages['shell.snapshotStatus']}>
      <div className="analysis-context"><strong>{profileName}</strong><span>{scopeLabel}</span><span>{messages['shell.snapshotStatus']}: {snapshot ? messages[snapshot.state === 'partial' ? 'snapshot.partialWarning' : 'probe.saved'] : busy ? messages['shell.loadingSnapshot'] : storage ? messages['probe.noSnapshot'] : messages['shell.snapshotUnavailable']}</span>
        {snapshot && <RegionalTime value={snapshot.endedAt} language={language}/>}
      </div>
      <ScanControls messages={messages} busy={busy} scansDisabled={!profile.active} hasSnapshot={snapshot !== null} pending={intent.current} start={mode => { void start(mode); }} reload={() => { void refresh(); }} />
    </div>
    </details>
      {!profile.active && <p className="notice">{messages['shell.inactiveHelp']}</p>}
    <SnapshotStatus language={language}
      snapshot={snapshot ? {snapshotId:snapshot.snapshotId,state:snapshot.state as 'complete'|'partial',startedAt:snapshot.startedAt!,endedAt:snapshot.endedAt!,profileRevision:snapshot.profileRevision} : null}
      currentRevision={snapshot?.currentProfileRevision ?? profile.revision} freshnessSeconds={profile.freshnessSeconds}
      newCompleteSnapshotId={newSnapshot} expired={expired} contentState={contentState}
      selectionFallback={analysis.reader?.selection.fallback ?? 'none'} job={job?.job ?? null} sourceGap={sourceGap}
      partialAvailable={job?.snapshotState==='partial' && validSnapshotTime(job.snapshotEndedAt) && snapshot?.snapshotId!==job.job.resultSnapshotId}
      partialUnqualified={job?.snapshotState==='partial' && !validSnapshotTime(job.snapshotEndedAt)}
      completeAvailable={snapshot?.state==='complete' || newSnapshot!==null} pendingSwitch={switchIntent.current!==null} disabled={busy}
      onAdopt={()=>{void adopt();}} onOpenPartial={()=>{void selectPartial();}} onBackComplete={()=>{void backComplete();}} />
    {notice && <p role="status" className="notice">{notice}</p>}
    {pollFailure && <div role="status" className="notice"><p>{language === 'de' ? 'Auftragsanzeige möglicherweise veraltet. ' : 'Job display may be out of date. '}{pollFailure === 'retrying' ? language === 'de' ? 'Lesender Abruf wird erneut versucht.' : 'Retrying the read request.' : language === 'de' ? 'Automatischer Abruf angehalten.' : 'Automatic reads stopped.'}</p>{pollFailure === 'stopped' && <button onClick={() => { const owner = pollOwner.current; const signal = alive.current?.signal; if (owner && signal && !signal.aborted) void poll(owner.id, signal, owner.terminal); }}>{language === 'de' ? 'Auftragsstatus erneut lesen' : 'Read job status again'}</button>}</div>}
    {analysis.state.view&&analysis.pin?.profileId===profileId&&<NavigationBar language={language} view={analysis.state.view} navigation={analysis.navigation} canBack={analysis.state.back.length>0} canForward={analysis.state.forward.length>0} disabled={selectionDisabled}
      onBack={()=>{const next=analysis.state.back.at(-1);if(next)void navigate(next,'back');}} onForward={()=>{const next=analysis.state.forward.at(-1);if(next)void navigate(next,'forward');}}
      onRoot={()=>changeView({rootId:'nas',selectedNodeId:null})} onParent={()=>{if(analysis.navigation?.parentId)changeView({rootId:analysis.navigation.parentId,selectedNodeId:null});}}
      onBreadcrumb={rootId=>changeView({rootId,selectedNodeId:null})} onMap={map=>changeView({map})} />}
    {area==='overview'&&analysis.state.view&&analysis.pin&&<>
      {filterTextConflict&&<p role="alert">{language==='de'?'Die gespeicherte Ansicht enthält unterschiedliche Textbedingungen. Übernimm eine gemeinsame Suche oder entferne die Filter.':'The saved view has conflicting text conditions. Apply one search or remove the filters.'}</p>}
      <SearchFilterControls language={language} view={analysis.state.view} disabled={selectionDisabled}
        onChange={view=>{void navigate(view);}} />
    </>}
    <section className="analysis-area" hidden={area !== 'overview'} aria-label={messages['shell.overview']}>
      {pendingParent && <ParentNotice messages={messages} disabled={busy} confirm={() => { void open(pendingParent.selection, pendingParent.parentPath); }} />}
      {newSnapshot && snapshot?.state==='complete' && <p role="status" className="notice">{messages['view.historicalActions']}</p>}
      <AnalysisWorkspace messages={messages} orientation={<>
        <p>{profileName}</p><p>{scopeLabel}</p><h4>{messages['shell.roots']}</h4><p>{messages['shell.rootsHint']}</p>
        {analysis.navigation?.breadcrumb.map(item=><p key={item.id}>{item.name}</p>)}
      </>} details={<HistoricalNodeDetails language={language}
        selection={!groupTarget && analysis.state.view && analysis.pin && selectedNode &&
          analysis.state.view.profileId === profileId && analysis.pin.profileId === profileId &&
          analysis.state.view.snapshotId === analysis.pin.snapshotId ? {
            profileId,viewId:analysis.pin.viewId,snapshotId:analysis.pin.snapshotId,
            rootId:analysis.state.view.rootId,nodeId:selectedNode,
            metric:analysis.state.view.metric,epoch:analysis.state.epoch
          } : null}
        disabled={selectionDisabled || area !== 'overview'} onSessionDenied={denied}
        fileStationDisabled={busy||expired||snapshot?.state!=='complete'}
        onFileStation={selection=>{void open({profileId:selection.profileId,viewId:selection.viewId,snapshotId:selection.snapshotId,nodeId:selection.nodeId});}}
        onUnavailable={code=>fail(new Error(code))} />}>

        {snapshot ? <>
          {metricUnavailable&&<p role="status">{language==='de'?'Die belegte Größe ist für diesen Datenstand nicht methodisch bestätigt.':'Allocated size is not method-qualified for this snapshot.'} <button onClick={()=>changeView({metric:'logical'})}>{language==='de'?'Logische Größe verwenden':'Use logical size'}</button></p>}
          {analysis.state.view&&<>
            {/* Keep all display controls together; disclosures contain explanation only. */}
            <div className="map-tools">
            <label>{language==='de'?'Größenmetrik':'Size metric'} <select aria-describedby="size-metric-explanation" value={analysis.state.view.metric}
              disabled={busy||expired||area!=='overview'||configurationLocked} onChange={event=>{
                // Old handlers cannot apply availability from a previous publication or bypass a lock.
                if(!alive.current||alive.current.signal.aborted||locked.current||expired||configurationLocked||area!=='overview'||JSON.stringify([hierarchyIdentity(controller.current),controller.current.state.epoch])!==metricKey)return;
                const metric=event.target.value;if(metric!=='logical'&&metric!=='allocated')return;
                if(metric==='allocated'&&!(currentMetricAvailability?.state==='ready'&&currentMetricAvailability.allocated))return;
                changeView({metric});
              }}>
              <option value="logical">{language==='de'?'Dateigröße (logisch)':'File size (logical)'}</option>
              <option value="allocated" disabled={!(currentMetricAvailability?.state==='ready'&&currentMetricAvailability.allocated)}>{language==='de'?'Gemeldete Blockbelegung':'Reported allocated blocks'}</option>
            </select></label>
            <label>{language==='de'?'Farbmodus':'Color mode'} <select value={colorMode} onChange={event=>setColorMode(event.target.value as ColorMode)}>
              <option value="folder">{language==='de'?'Ordnerzweig':'Folder branch'}</option>
              <option value="filetype">{language==='de'?'Dateityp':'File type'}</option>
              {/* Retained timestamp intent is visible but cannot be selected for a new view. */}
              {colorMode==='mtime'&&<option value="mtime" disabled>{language==='de'?'Änderungsalter nicht mehr verfügbar':'Modification age unavailable'}</option>}
            </select></label>
            <label>{language==='de'?'Detailgrad':'Detail level'} <select value={detail} onChange={event=>setDetail(event.target.value==='auto'||event.target.value==='max'?event.target.value:Number(event.target.value) as DetailLevel)}>
              <option value="auto">{language==='de'?'Automatisch':'Automatic'}</option>
              {[1,2,3,4,5,6,7,8].map(depth=><option key={depth} value={depth}>{depth}</option>)}
              <option value="max">{language==='de'?'Maximal (begrenzt)':'Maximum (bounded)'}</option>
            </select></label>
            </div>
            {(!currentMetricAvailability||currentMetricAvailability.state==='unavailable'||!currentMetricAvailability.allocated)&&<div className="metric-availability">
            <p role="status">{!currentMetricAvailability?(language==='de'?'Verfügbarkeit der Blockbelegung wird geprüft.':'Checking allocation availability.'):currentMetricAvailability.state==='unavailable'?(language==='de'?'Verfügbarkeit unbekannt: Prüfung nicht erfolgreich.':'Availability unknown: check failed.'):!currentMetricAvailability.allocated?(language==='de'?'Blockbelegung nicht verfügbar: historische Messmethode nicht qualifiziert.':'Allocation unavailable: historical measurement method is not qualified.'):(language==='de'?'Historische Blockbelegung verfügbar.':'Historical allocation available.')}
              {currentMetricAvailability?.state==='unavailable'&&<button disabled={busy||expired||configurationLocked} onClick={()=>setMetricRetry(value=>value+1)}>{language==='de'?'Erneut prüfen':'Check again'}</button>}
            </p>
            </div>}
            <details className="map-display-help"><summary>{language==='de'?'Legende und Darstellungshinweise':'Legend and display information'}{analysis.state.view.metric==='allocated'?(language==='de'?' · keine zugesicherte Löschersparnis':' · no guaranteed deletion savings'):''}</summary>
            <p id="size-metric-explanation">{language==='de'?'Dateigröße: erfasste Dateilänge (st_size). Blockbelegung: historisch gemeldete Blöcke × 512; kein exklusiver oder freigebbarer Speicher.':'File size: recorded length (st_size). Allocation: historically reported blocks × 512; not exclusive or reclaimable storage.'}</p>
            {budget&&<p>{language==='de'?'Darstellungsgrenze':'Display limit'}: {budget.elementBudget} · {language==='de'?'Tiefe':'Depth'}: {budget.depth}</p>}
            <Legend mode={colorMode} metric={analysis.state.view.metric} language={language} referenceTime={snapshot.endedAt??snapshot.startedAt} />
            </details>
          </>}
          <div ref={setMapViewport} className="map-viewport" style={{width:'100%',height:'clamp(300px, 50vh, 700px)'}}>
            {mapModel&&mapSize&&budget&&analysis.state.view?.map==='treemap'&&<TreemapCanvas model={mapModel} width={mapSize.width} height={mapSize.height}
              devicePixelRatio={canvasEnvironment.devicePixelRatio} theme={theme} language={language}
              colorMode={colorMode} referenceTime={snapshot.endedAt??snapshot.startedAt} selectedNodeId={selectedMapNode} focusedNodeId={mapFocus}
              disabled={rowInteractionDisabled||area!=='overview'||metricUnavailable} maxDepth={budget.depth} maxElements={budget.elementBudget}
              onSelect={selectMapNode} onFocus={setMapFocus} onOpen={openMapNode} onGroup={inspectGroup} />}
            {mapModel&&mapSize&&budget&&analysis.state.view?.map==='radial'&&<RadialCanvas model={mapModel} width={mapSize.width} height={mapSize.height}
              devicePixelRatio={canvasEnvironment.devicePixelRatio} theme={theme} language={language}
              colorMode={colorMode} referenceTime={snapshot.endedAt??snapshot.startedAt} selectedNodeId={selectedMapNode} focusedNodeId={mapFocus}
              disabled={rowInteractionDisabled||area!=='overview'||metricUnavailable} maxDepth={budget.depth} maxSegments={budget.elementBudget}
              onSelect={selectMapNode} onFocus={setMapFocus} onOpen={openMapNode} onGroup={inspectGroup}
              canGoUp={!!analysis.navigation?.parentId} onUp={()=>{if(analysis.navigation?.parentId)changeView({rootId:analysis.navigation.parentId,selectedNodeId:null});}} />}
          </div>
          {hierarchyPending&&<p role="status">{messages['probe.loading']}</p>}
          {analysis.state.view&&<FilterTotals language={language} metric={analysis.state.view.metric} mode={analysis.state.view.filterMode}
            originalSummary={hierarchy?.originalSummary??null} matchSummary={hierarchy?.matchSummary??null} denominatorSummary={hierarchy?.denominator??null} />}
          {/* Historical availability is separate from measured hierarchy totals. */}
          <SourceAvailabilityNotice value={hierarchy?.sourceAvailability} language={language} />
          {analysis.state.view&&analysis.pin&&!filterTextConflict&&<SearchResultList language={language} view={analysis.state.view} pin={analysis.pin}
            selectedNodeId={selectedMapNode} disabled={!secondaryReadsReady||rowInteractionDisabled||area!=='overview'||metricUnavailable}
            fileStationDisabled={busy||expired||snapshot.state==='partial'}
            loadPage={(cursor,signal)=>readSearchList(analysis.state.view!,analysis.pin!,cursor,signal)}
            onSelect={selectNode} onDetails={selectNode} onSessionDenied={denied} onUnavailable={code=>fail(new Error(code))}
            onOpen={node=>{const view=controller.current.state.view;if(view&&node.id!==view.rootId)void navigate({...view,rootId:node.id,selectedNodeId:null},'navigate','drill');}}
            onFileStation={node=>{if(node.realNodeId)void open({profileId,snapshotId:snapshot.snapshotId,nodeId:node.realNodeId});}} />}
          {mapModel&&<AccessibleMapInfo model={mapModel} selectedId={selectedMapNode} focusedId={mapFocus}
            language={language} disabled={rowInteractionDisabled||area!=='overview'||metricUnavailable}
            onFocus={setMapFocus} onSelect={id=>{const node=hierarchy?.nodes.find(node=>node.id===id);if(node)selectNode(node);}}
            onGroup={id=>inspectGroup({kind:'model',nodeId:id})}
            onOpen={id=>{const node=hierarchy?.nodes.find(node=>node.id===id),view=controller.current.state.view;if(node&&node.kind!=='group'&&view&&id!==view.rootId)void navigate({...view,rootId:id,selectedNodeId:null},'navigate','drill');}} />}
          {groupTarget&&mapModel&&analysis.state.view&&analysis.pin&&<GroupInspectionPanel
            model={mapModel} target={groupTarget} identity={hierarchyIdentity(analysis)} language={language}
            disabled={rowInteractionDisabled||area!=='overview'||metricUnavailable} onClose={()=>setInspectedGroup(null)} onSelect={selectNode}
            onOpen={node=>{const view=controller.current.state.view;if(view&&node.id!==view.rootId&&['nas','volume','share','other','directory'].includes(node.kind))void navigate({...view,rootId:node.id,selectedNodeId:null},'navigate','drill');}}
            onError={fail} loadPage={(continuation,cursor,signal)=>readGroupPage(analysis.state.view!,analysis.pin!,continuation,cursor,signal)} />}

          {hierarchy&&mapModel&&<>{<p>{mapModel.original.knownBytes.toString()} B · {mapModel.original.unknownFileCount.toString()} {unknownSizeCountLabel(mapModel.original.unknownFileCount,language)}</p>}
          {hierarchy.truncated&&<p role="status">{language==='de'?'Begrenzter Ausschnitt; öffne einen Unterordner für dessen Inhalt.':'Bounded excerpt; open a subfolder to inspect its contents.'}</p>}
          {analysis.state.view&&analysis.pin&&<AnalysisNodeList
            identity={{profileId,viewId:analysis.pin.viewId,snapshotId:snapshot.snapshotId,rootId:analysis.state.view.rootId,metric:analysis.state.view.metric,epoch:analysis.state.epoch}}
            nodes={hierarchy.nodes} selectedNodeId={selectedMapNode} language={language}
            disabled={rowInteractionDisabled||area!=='overview'} fileStationDisabled={busy||expired||snapshot.state==='partial'}
            onSelect={selectNode} onDetails={selectNode} onSessionDenied={denied} onUnavailable={code=>fail(new Error(code))}
            onOpen={node=>{const view=controller.current.state.view;if(view&&node.id!==view.rootId)void navigate({...view,rootId:node.id,selectedNodeId:null},'navigate','drill');}}
            onFileStation={node=>{if(node.realNodeId)void open({profileId,snapshotId:snapshot.snapshotId,nodeId:node.realNodeId});}} />}</>}
        </> : <div className="analysis-empty"><h4>{busy ? messages['shell.loadingSnapshot'] : storage ? messages['shell.noSnapshotTitle'] : messages['shell.snapshotUnavailable']}</h4>{storage && <p>{messages['shell.noSnapshotHelp']}</p>}</div>}

      </AnalysisWorkspace>
    </section>
    <section className="analysis-area" hidden={area !== 'status'} aria-label={messages['shell.status']}>
      {/* The presenter cannot authorize controls from an unconfirmed observation. */}
      <ScanStatusPanel language={language} snapshot={statusSnapshot} health={pollFailure??'current'} progress={progressObservation}
        selectedProfileName={profileName} profileNames={profileNames} onSelectProfile={onStatusProfile} onRetry={()=>observer.current?.start()}
        controls={job ? <><ScanJobStatus language={language} job={job.job} deduplicated={deduplicated} messages={messages} /><JobControls job={job.job} control={job.control??null} pending={controlIntent.current} sending={controlling} messages={messages} send={action=>{void controlJob(action);}} /></> : <p>{messages['shell.noJob']}</p>}/>
      {lastBoundaries && <ScanBoundaryNotes report={lastBoundaries} messages={messages} />}
      {boundaryFailure && <p role="status" className="notice">{messages['boundary.latestUnavailable']}</p>}
    </section>
    <section className="analysis-area" hidden={area !== 'profiles'} aria-label={messages['shell.profiles']}>
      <SchedulerTemplate profileId={profileId} language={language} onSessionDenied={denied} />
    </section>
    <section className="analysis-area" hidden={area !== 'settings'} aria-label={messages['shell.settings']}>
      {storage && <StorageNotes data={storage} messages={messages} />}
      {/* The settings probe shares the exact historical selection and the existing guarded launcher. */}
      {area==='settings'&&<FileStationIntegrationTest language={language} dsmBuild={dsmBuild}
        selection={!groupTarget&&analysis.state.view&&analysis.pin&&selectedNode&&
          analysis.state.view.profileId===profileId&&analysis.pin.profileId===profileId&&analysis.state.view.snapshotId===analysis.pin.snapshotId?{
            profileId,viewId:analysis.pin.viewId,snapshotId:analysis.pin.snapshotId,rootId:analysis.state.view.rootId,nodeId:selectedNode,metric:analysis.state.view.metric,epoch:analysis.state.epoch
          }:null}
        disabled={busy||selectionDisabled||snapshot?.state!=='complete'} onSessionDenied={denied} onUnavailable={code=>fail(new Error(code))}
        onTest={selection=>{void open({profileId:selection.profileId,viewId:selection.viewId,snapshotId:selection.snapshotId,nodeId:selection.nodeId});}}/>}
      {area==='settings'&&pendingParent&&<ParentNotice messages={messages} disabled={busy||expired} confirm={()=>{void open(pendingParent.selection,pendingParent.parentPath);}}/>}
      {area==='settings'&&<PermissionStatus language={language} scope={profile.scope} identity={profile.id+':'+profile.revision} disabled={busy||switchIntent.current!==null} onSessionDenied={denied}/> }
      <IntegritySettings pendingIntent={integrityIntent} profileId={profileId} profileName={profileName} language={language} onSessionDenied={denied} onBeforeDelete={beforeIndexDelete} onSnapshotUnavailable={snapshotUnavailable} onRebuild={()=>{void start('rebuild');}} disabled={busy||switchIntent.current!==null} />
      <StorageSettings key={configurationEpoch} onDirty={onSettingsDirty} pendingIntent={retentionIntent} profileId={profileId} language={language} onSessionDenied={denied} disabled={configurationLocked||busy||switchIntent.current!==null} onChooseSnapshot={id=>{void adopt(id);}} />
    </section>
  </>;

}

// The vertical probe lists actual stored profiles and never creates defaults or starts scans implicitly.
export function Workbench({ language, selected, onSelect, denied: onDenied, pin, onPin, rememberedView, onAnalysis, onLanguageResolved }: {onLanguageResolved?:(language:'auto'|'de'|'en')=>void;rememberedView?:PreferenceView;onAnalysis?:(profileId:string,pin:ViewPin|null,view:PreferenceView|null)=>void;pin?:ViewPin;onPin?:(pin:ViewPin|null)=>void; language: 'de' | 'en'; selected: string; onSelect: (id: string) => void; denied: () => void }) {
  const messages = language === 'en' ? en : de;
  // Retention is global, so a profile remount must retry the same uncertain mutation.
  const retentionIntent = useRef<RetentionIntentRef['current']>(null);
  // Integrity retries retain the original profile and confirmed snapshot across profile remounts.
  const integrityIntent = useRef<IntegrityIntentRef['current']>(null);
  const [catalogue, setCatalogue] = useState<ProfileList | null>(null);
  const [error, setError] = useState(false);
  const [area, setArea] = useState<AnalysisArea>('overview');
  const [wizard,setWizard]=useState(false);
  const [fallback, setFallback] = useState(false);
  const [editor,setEditor]=useState<{identity:string;profile:Profile|null}|null>(null);
  const editorRef=useRef(editor);editorRef.current=editor;
  const editorDirty=useRef(false);

  const [managementBusy,setManagementBusy]=useState(false);
  const [editorGuard,setEditorGuard]=useState(false);
  const editorSession=useRef(crypto.randomUUID());
  const catalogueSequence=useRef(0);
  const catalogueRequest=useRef<AbortController|null>(null);
  const freshCatalogue=async(signal:AbortSignal)=>{
    const value=await readView<ProfileList>({action:'profiles'},signal);
    if(!Array.isArray(value.profiles)||typeof value.dsmBuild!=='string'||value.profiles.some(item=>typeof item.profile?.id!=='string'||typeof item.profile.name!=='string'))throw new Error('INVALID_PROFILES');
    return value;
  };
  const editorSaved=(profile:Profile,identity:string)=>{
    const request=new AbortController();catalogueRequest.current?.abort();catalogueRequest.current=request;const sequence=++catalogueSequence.current;
    void freshCatalogue(request.signal).then(value=>{
      if(request.signal.aborted||sequence!==catalogueSequence.current||!workbenchAlive.current)return;
      setCatalogue(value);setError(false);
      if(editorRef.current?.identity===identity){setEditor({identity,profile});editorDirty.current=false;setEditorGuard(false);}
    }).catch(error=>{if(request.signal.aborted||!workbenchAlive.current)return;if(error instanceof SessionDenied)denied();else setError(true);});
  };

  const workbenchAlive=useRef(true);
  // Notices belong to the authenticated Workbench lifetime, not a selected profile mount.
  const noticeSession=useRef<NoticeSession|null>(null);
  if(!noticeSession.current)noticeSession.current=createNoticeSession();
  const [localNotices,setLocalNotices]=useState<readonly LocalNotice[]>([]);
  const observeNotice=(outcome:NoticeOutcome,report?:ScanBoundaryReport)=>{
    if(workbenchAlive.current&&noticeSession.current!.observe(outcome,report))setLocalNotices(noticeSession.current!.snapshot());
  };
  // Revoke notice admission synchronously before the parent session gate changes its UI.
  const denied=()=>{noticeSession.current!.dispose();setLocalNotices([]);onDenied();};
  const dismissNotice=(id:number)=>{
    if(!workbenchAlive.current)return;
    noticeSession.current!.dismiss(id);setLocalNotices(noticeSession.current!.snapshot());
  };
  const [analysis,setAnalysis]=useState<ConfirmedAnalysis>(emptyAnalysis);
  const [preferenceStatus,setPreferenceStatus]=useState('loading');
  // Bootstrap retries are explicit and have a separate lifetime from preference mutations.
  const bootstrapOwner = useRef<AbortController | null>(null);
  const bootstrapRunning = useRef(false);
  const [bootstrapStatus,setBootstrapStatus] = useState<'loading'|'failed'|'ready'>('loading');
  const preference=useRef<PreferenceClient|null>(null);
  if(!preference.current)preference.current=new PreferenceClient();
  const controllerRef=useRef<AnalysisController|null>(null);
  const chosenProfile=useRef<string|null>(null);
  const callbacks=useRef({onSelect,onPin,onAnalysis,onLanguageResolved});callbacks.current={onSelect,onPin,onAnalysis,onLanguageResolved};
  if(!controllerRef.current)controllerRef.current=new AnalysisController(next=>{
    setAnalysis(next);
    const view=next.state.view;
    if(view){if(callbacks.current.onAnalysis)callbacks.current.onAnalysis(view.profileId,next.pin,view);else{callbacks.current.onSelect(view.profileId);callbacks.current.onPin?.(next.pin);}}
    else callbacks.current.onPin?.(null);
    if(view&&chosenProfile.current===view.profileId){chosenProfile.current=null;queueMicrotask(()=>{if(workbenchAlive.current)saveView();});}
  },(reader,current)=>{checkedReader(reader,current);checkedStorage(reader.view);});
  const controller=controllerRef.current;
  const [regionalPreference,setRegionalPreference]=useState<UserPreference|null>(null);
  const [nasTimeZone,setNasTimeZone]=useState<string|null>(null);
  const [regionalDirty,setRegionalDirty]=useState(false);
  const [themeDirty,setThemeDirty]=useState(false);
  const [displayDirty,setDisplayDirty]=useState(false);
  const [templateDirty,setTemplateDirty]=useState(false);
  const [storageDirty,setStorageDirty]=useState(false);
  // Import owns navigation until its immutable receipt and refreshed providers are reconciled.
  const [importDirty,setImportDirty]=useState(false);
  const [configurationEpoch,setConfigurationEpoch]=useState(0);
  const effectiveTheme=useResolvedTheme(regionalPreference?.theme??'auto');
  const confirmPreference=(value:UserPreference)=>{setRegionalPreference(value);callbacks.current.onLanguageResolved?.(value.language);};
  useEffect(()=>{const owner=new AbortController();const timer=setTimeout(()=>owner.abort(),5000);
    void readRegionalContext(owner.signal).then(value=>{if(!owner.signal.aborted&&workbenchAlive.current)setNasTimeZone(value.nasTimeZone);}).catch(error=>{if(!owner.signal.aborted&&workbenchAlive.current&&error instanceof SessionDenied)denied();}).finally(()=>clearTimeout(timer));
    return()=>{owner.abort();clearTimeout(timer);};},[]);
  const saveRegional=async(change:RegionalChoice)=>{const client=preference.current!;setPreferenceStatus('saving');
    try{const value=await client.writeRegional(change);if(!workbenchAlive.current)return;confirmPreference(value);setPreferenceStatus('idle');flushSave();}
    catch(error){if(workbenchAlive.current){if(error instanceof SessionDenied)denied();else setPreferenceStatus(client.status);}throw error;}};

  // Theme writes use the same serialized preference owner as regional settings and navigation.
  const saveTheme=async(theme:UserPreference['theme'])=>{const client=preference.current!;setPreferenceStatus('saving');
    try{const value=await client.writeTheme(theme);if(!workbenchAlive.current)return;confirmPreference(value);setPreferenceStatus('idle');flushSave();}
    catch(error){if(workbenchAlive.current){if(error instanceof SessionDenied)denied();else setPreferenceStatus(client.status);}throw error;}};

  // The same preference owner serializes all personal groups and retains unrelated view state.
  const savePersonal=async(change:PersonalChange)=>{const client=preference.current!;setPreferenceStatus('saving');
    try{const value=await (change.group==='display'?client.writeDisplay(change.display):change.group==='map'?client.writeMap(change.map):client.writePreferredProfile(change.profileId));if(!workbenchAlive.current)return;confirmPreference(value);setPreferenceStatus('idle');flushSave();}
    catch(error){if(workbenchAlive.current){if(error instanceof SessionDenied)denied();else setPreferenceStatus(client.status);}throw error;}};
  // Only explicit confirmed navigation queues a save; bootstrap and fallback never enqueue one.
  const queuedSave=useRef<PreferenceView|null|undefined>(undefined);
  const flushSave=()=>{
    const client=preference.current!;if(!workbenchAlive.current||client.status!=='idle'||!client.preference||queuedSave.current===undefined)return;
    const view=queuedSave.current;queuedSave.current=undefined;setPreferenceStatus('saving');
    void client.writeLastView(view).then(()=>{if(workbenchAlive.current){setPreferenceStatus('idle');flushSave();}}).catch(error=>{
      if(!workbenchAlive.current)return;
      if(error instanceof SessionDenied)denied();else{if(client.status==='conflict')queuedSave.current=undefined;setPreferenceStatus(client.status==='idle'?'error':client.status);}
    });
  };
  const saveView=()=>{queuedSave.current=structuredClone(controller.current.state.view);flushSave();};
  const retryPreference=()=>{const client=preference.current!;setPreferenceStatus('saving');void client.retry().then(value=>{if(workbenchAlive.current){confirmPreference(value);setPreferenceStatus('idle');flushSave();}}).catch(error=>{if(!workbenchAlive.current)return;if(error instanceof SessionDenied)denied();else setPreferenceStatus(client.status);});};
  const reloadPreference=()=>{void preference.current!.read().then(value=>{if(workbenchAlive.current){confirmPreference(value);setPreferenceStatus('idle');}}).catch(error=>{if(!workbenchAlive.current)return;if(error instanceof SessionDenied)denied();else setPreferenceStatus('error');});};
  // Use the same preference owner as navigation so appearance CAS cannot race a local view save.
  const changeLanguage=async(choice:'auto'|'de'|'en')=>{
    const client=preference.current!;setPreferenceStatus('saving');
    try{const value=await client.writeLanguage(choice);if(!workbenchAlive.current)return;confirmPreference(value);setPreferenceStatus('idle');flushSave();}
    catch(error){if(workbenchAlive.current){if(error instanceof SessionDenied)denied();else setPreferenceStatus(client.status==='idle'?'error':client.status);}throw error;}
  };
  const chooseProfile=(id:string)=>{if(managementBusy||regionalDirty||themeDirty||displayDirty||templateDirty||storageDirty||importDirty)return false;if(editorDirty.current){setEditorGuard(true);setArea('profiles');return false;}controller.cancel();chosenProfile.current=id;setFallback(false);onSelect(id);return true;};
  // Catalogue replacement is sequence-bound; confirmed deletion releases only this view's lease.
  const reloadCatalogue=async(signal:AbortSignal)=>{
    const sequence=++catalogueSequence.current;
    const value=await freshCatalogue(signal);
    if(!signal.aborted&&workbenchAlive.current&&sequence===catalogueSequence.current){setCatalogue(value);setError(false);}
  };
  // Refresh a confirmed import without selecting a profile, acquiring a reader or starting a scan.
  const importedConfiguration = async () => {
    const request=new AbortController();catalogueRequest.current?.abort();catalogueRequest.current=request;
    const sequence=++catalogueSequence.current;
    const signal=AbortSignal.any([request.signal,AbortSignal.timeout(15000)]);
    try {
      const [value,prefs]=await Promise.all([freshCatalogue(signal),preference.current!.read(signal)]);
      signal.throwIfAborted();
      if(!workbenchAlive.current||sequence!==catalogueSequence.current)throw Error('HANDOFF_CANCELLED');
      setCatalogue(value);setError(false);confirmPreference(prefs);setPreferenceStatus('idle');
      // Only shared settings providers remount; the existing analysis lease remains pinned.
      setConfigurationEpoch(epoch=>epoch+1);
    } catch(error) {
      // A superseded or expired read cannot invalidate the current authenticated workspace.
      const owns=workbenchAlive.current&&sequence===catalogueSequence.current&&!signal.aborted;
      request.abort();
      if(owns&&error instanceof SessionDenied)denied();
      throw error;
    }
  };
  const managementChanged=(receipt:ManagementReceipt)=>{
    if(receipt.operation==='delete'){
      if(controller.current.pin?.profileId===receipt.profileId){
        const old=controller.current.pin;controller.clear();
        void requestReader({contractVersion:1,operation:'release',profileId:old.profileId,viewId:old.viewId},AbortSignal.timeout(8000)).catch(()=>{});
      }
      if(selected===receipt.profileId)onSelect('');
      if(editorRef.current?.profile?.id===receipt.profileId){setEditor(null);editorDirty.current=false;}
    }
    const request=new AbortController();catalogueRequest.current?.abort();catalogueRequest.current=request;
    void reloadCatalogue(request.signal).catch(error=>{if(request.signal.aborted||!workbenchAlive.current)return;if(error instanceof SessionDenied)denied();else setError(true);});
  };
  const openEditor=(profile:Profile|null)=>{
    if(managementBusy)return;
    if(editorDirty.current){setEditorGuard(true);return;}
    setEditor({identity:crypto.randomUUID(),profile});setEditorGuard(false);
  };
  // Publish only a fully qualified bootstrap; a successful auth check alone is not a catalogue.
  const bootstrap = async () => {
    if (!workbenchAlive.current || bootstrapRunning.current) return;
    bootstrapOwner.current?.abort();
    const owner = new AbortController(); bootstrapOwner.current = owner; bootstrapRunning.current = true;
    const signal = AbortSignal.any([owner.signal, AbortSignal.timeout(20000)]);
    const owns = () => workbenchAlive.current && bootstrapOwner.current === owner && !owner.signal.aborted;
    const active = () => owns() && !signal.aborted;
    // One absolute deadline bounds every stage, including transports that ignore AbortSignal.
    let rejectAbort!: () => void;
    const aborted = new Promise<never>((_, reject) => {
      rejectAbort = () => reject(signal.reason);
      signal.addEventListener('abort', rejectAbort, { once: true });
    });
    const bounded = <T,>(operation: Promise<T>): Promise<T> => Promise.race([operation, aborted]);
    setBootstrapStatus('loading'); setError(false); setPreferenceStatus('loading');
    try {
      const prefs=await bounded(preference.current!.read(signal));
      if (!active()) { signal.throwIfAborted(); return; }
      confirmPreference(prefs);
      const value=await bounded(readView<ProfileList>({action:'profiles'},signal));
      if (!active()) { signal.throwIfAborted(); return; }
      if(!Array.isArray(value.profiles)||typeof value.dsmBuild!=='string'||value.profiles.some(item=>typeof item.profile?.id!=='string'||typeof item.profile.name!=='string'))throw new Error('INVALID_PROFILES');
      // Retained identities require the same live server qualification on every attempt.
      let retained=false;
      if(rememberedView&&pin&&rememberedView.profileId===pin.profileId&&rememberedView.snapshotId===pin.snapshotId){
        try{
          const reader=await bounded(requestReader<ReaderResponse>({contractVersion:1,operation:'read',profileId:pin.profileId,viewId:pin.viewId},signal));
          if (!active()) { signal.throwIfAborted(); return; }
          retained=await bounded(controllerRef.current!.accept(pin.profileId,reader,signal,rememberedView));
        }catch(error){
          if(!active()||error instanceof SessionDenied)throw error;
          setFallback(true);
        }
      }
      if (!active()) { signal.throwIfAborted(); return; }
      if(!retained){
        const restored=await bounded(requestRestore(crypto.randomUUID(),prefs.revision,signal));
        if (!active()) { signal.throwIfAborted(); return; }
        if(restored.resolvedView&&restored.reader&&restored.navigation){controllerRef.current!.restored(restored.resolvedView,restored.reader,restored.navigation);}
        else{const next=resolveProfileSelection(value.profiles.map(item=>item.profile),restored.profileId??selected);onSelect(next);}
        if(restored.reasons.length>0)setFallback(true);
      }
      if (!active()) { signal.throwIfAborted(); return; }
      setCatalogue(value);setError(false);setPreferenceStatus('idle');setBootstrapStatus('ready');
    } catch(error) {
      if (!owns()) return;
      if(!signal.aborted&&error instanceof SessionDenied){owner.abort();denied();}
      else{setError(true);setPreferenceStatus('error');setBootstrapStatus('failed');}
    } finally {
      signal.removeEventListener('abort', rejectAbort);
      if (bootstrapOwner.current === owner) bootstrapRunning.current = false;
    }
  };
  useEffect(() => {
    void bootstrap();
    return()=>{workbenchAlive.current=false;noticeSession.current!.dispose();bootstrapOwner.current?.abort();catalogueRequest.current?.abort();controllerRef.current!.cancel();preference.current!.dispose();};
    // The surrounding session gate owns reauthentication and remounting.
  }, []);
  // A stale or removed ID never mounts a reader or silently starts a replacement scan.
  const current = catalogue?.profiles.find(item => item.profile.id === selected);
  return <DisplayContext.Provider value={regionalPreference?.display??defaultDisplay}><DisplayEffects/><ThemeContext.Provider value={effectiveTheme}><RegionalContext.Provider value={regionalSettings(language,regionalPreference?.locale as 'de-DE'|'en-US'|'en-GB'|null??null,regionalPreference?.timeZone??null,nasTimeZone)}><AnalysisShell area={area} onArea={next=>{if(!managementBusy&&!wizard&&!regionalDirty&&!themeDirty&&!displayDirty&&!templateDirty&&!storageDirty&&!importDirty)setArea(next);}} messages={messages} picker={catalogue?.profiles.length ?
    <label className="profile-select">{messages['probe.profile']}<select disabled={wizard} value={current ? selected : ''} onChange={event => {chooseProfile(event.target.value);}}>
      {!current && <option value="">{messages['shell.profileUnavailable']}</option>}
      {catalogue.profiles.map(item => <option key={item.profile.id} value={item.profile.id}>{item.profile.name}{item.profile.active === false ? ` · ${messages['shell.inactive']}` : ''}</option>)}
    </select></label> : <strong>{messages['shell.title']}</strong>}>
    <LocalNotices notices={localNotices} language={language} onDismiss={dismissNotice}/>
    {/* Keep the observer mounted while exposing profile actions only in the status area. */}
    {catalogue&&(!current||wizard)&&<div hidden={area!=='status'||wizard}><UnselectedStatus language={language} denied={denied} profileNames={Object.fromEntries(catalogue.profiles.map(item=>[item.profile.id,item.profile.name]))} onSelectProfile={id=>{if(!wizard&&area==='status'&&chooseProfile(id))setArea('status');}}/></div>}
    {/* Setup remains reachable without occupying the map header with a permanent action. */}
    <details className="setup-tools" hidden={!!catalogue?.profiles.length&&area!=='settings'} open={!catalogue?.profiles.length}><summary>{language==='de'?'Einrichtung':'Setup'}</summary>
    <button type="button" disabled={!catalogue||managementBusy||editorDirty.current||regionalDirty||themeDirty||displayDirty||templateDirty||storageDirty||importDirty||wizard} onClick={()=>{controller.cancel();setWizard(true);}}>{language==='de'?'Einrichtung öffnen':'Open setup'}</button>
    </details>
    {wizard&&catalogue&&<OnboardingWizard language={language} sessionIdentity={editorSession.current} dsmBuild={catalogue.dsmBuild} onLanguageChange={changeLanguage} onSessionDenied={denied} onClose={()=>setWizard(false)}
      loadProfile={async(id,signal)=>(await freshCatalogue(signal)).profiles.find(item=>item.profile.id===id)?.profile??null}
      onProfileSaved={profile=>editorSaved(profile,'onboarding')}
      onStarted={async(profileId,jobId)=>{
        // Mount the job view only after its accepted job identity is present in the fresh catalogue.
        const request=new AbortController();catalogueRequest.current?.abort();catalogueRequest.current=request;const sequence=++catalogueSequence.current;
        try{const value=await freshCatalogue(request.signal);if(request.signal.aborted||!workbenchAlive.current||sequence!==catalogueSequence.current)throw Error('HANDOFF_CANCELLED');
          if(!value.profiles.some(item=>item.profile.id===profileId))throw Error('PROFILE_UNAVAILABLE');
          setCatalogue({...value,profiles:value.profiles.map(item=>item.profile.id===profileId?{...item,latestJobId:jobId}:item)});
          setError(false);controller.clear();onSelect(profileId);setArea('status');setWizard(false);
        }catch(error){if(!request.signal.aborted&&workbenchAlive.current&&error instanceof SessionDenied)denied();throw error;}
      }}/>}
    {preferenceStatus==='uncertain'&&<p role="status">{language==='de'?'Speicherung unbestätigt. Wiederholen sendet dieselbe Änderung.':'Saving is unconfirmed. Retry sends the same change.'} <button onClick={retryPreference}>{language==='de'?'Erneut versuchen':'Retry'}</button></p>}
    {preferenceStatus==='conflict'&&<p role="status">{language==='de'?'Die Einstellungen wurden anderweitig geändert. Lade den aktuellen Stand vor weiteren Änderungen.':'Preferences changed elsewhere. Read the current revision before further changes.'} <button onClick={reloadPreference}>{language==='de'?'Einstellungen neu lesen':'Read preferences again'}</button></p>}
    {preferenceStatus==='error'&&<p role="alert">{language==='de'?'Gespeicherte Ansicht nicht verfügbar.':'Saved view unavailable.'}</p>}
    {bootstrapStatus === 'failed' && <button type="button" onClick={() => { void bootstrap(); }}>{language === 'de' ? 'Einstieg erneut laden' : 'Retry opening workspace'}</button>}
    {error ? <p role="alert">{messages['probe.error']}</p> : catalogue === null ? <p role="status">{messages['probe.loading']}</p> : !catalogue.profiles.length ? <div className="analysis-empty"><h2>{messages['shell.noProfilesTitle']}</h2><p>{messages['shell.noProfilesHelp']}</p></div> : <>
      {fallback && <p role="status" className="notice">{messages['shell.profileFallback']}</p>}
      {current && !wizard && <ProfileView onNotice={observeNotice} configurationEpoch={configurationEpoch} configurationLocked={importDirty} onSettingsDirty={setStorageDirty} display={regionalPreference?.display??defaultDisplay} defaultMap={regionalPreference?.map??'treemap'} theme={effectiveTheme.theme} profileNames={Object.fromEntries(catalogue.profiles.map(item=>[item.profile.id,item.profile.name]))} onStatusProfile={id=>{if(chooseProfile(id))setArea('status');}} analysis={analysis} controller={controller} saveView={saveView} area={area} profile={current.profile} integrityIntent={integrityIntent} profileName={current.profile.name} retentionIntent={retentionIntent} language={language} pin={analysis.pin?.profileId===selected?analysis.pin:pin} onPin={onPin} key={selected} profileId={selected} latestJobId={current.latestJobId ?? null} dsmBuild={catalogue.dsmBuild} messages={messages} denied={denied} />}
    </>}
    <section className="analysis-area" hidden={wizard||area !== 'profiles'}>
      {catalogue&&<ProfileOverview profiles={catalogue.profiles} language={language} sessionIdentity={editorSession.current} disabled={editor!==null} loadSummary={loadProfileSummaries} onReload={reloadCatalogue} onOpen={id=>{if(chooseProfile(id))setArea('overview');}} onEdit={openEditor} onCreate={()=>openEditor(null)} onChanged={managementChanged} onBusyChange={setManagementBusy} onSessionDenied={denied}/>}
      {editorGuard&&<p role="status">{language==='de'?'Schließe den Profilentwurf mit Speichern oder Verwerfen, bevor du das Analyseprofil wechselst.':'Save or discard the profile draft before changing the analysis profile.'}</p>}
      {editor&&<ProfileEditor language={language} profile={editor.profile} editorIdentity={editor.identity} sessionIdentity={editorSession.current} loadInventory={loadScopeInventory} checkScope={checkSelectedScope}
        loadProfile={async(id,signal)=>(await freshCatalogue(signal)).profiles.find(item=>item.profile.id===id)?.profile??null}
        onDirtyChange={dirty=>{editorDirty.current=dirty;}} onSaved={editorSaved} onSessionDenied={denied}
        onCancel={()=>{setEditor(null);editorDirty.current=false;setEditorGuard(false);}} />}
    </section>
    <section className="analysis-area" hidden={wizard||area !== 'settings'}><GlobalExclusionSettings key={configurationEpoch} language={language} disabled={wizard||importDirty} onSessionDenied={denied} onDirty={setTemplateDirty}/>{templateDirty&&<p role="status">{language==='de'?'Speichere oder verwirf die globale Vorlage vor dem Bereichswechsel.':'Save or discard the global template before changing sections.'}</p>}<PersonalDisplaySettings value={regionalPreference} profiles={catalogue?.profiles.map(p=>({id:p.profile.id,name:p.profile.name}))??[]} language={language} disabled={importDirty||preferenceStatus!=='idle'} onSave={savePersonal} onDirty={setDisplayDirty}/>{displayDirty&&<p role="status">{language==='de'?'Speichere oder verwirf die Standardansicht vor dem Bereichswechsel.':'Save or discard view defaults before changing sections.'}</p>}<ThemeSettings value={regionalPreference} language={language} disabled={importDirty||preferenceStatus!=='idle'} onSave={saveTheme} onDirty={setThemeDirty}/>{themeDirty&&<p role="status">{language==='de'?'Speichere oder verwirf die Darstellung vor dem Bereichswechsel.':'Save or discard appearance before changing sections.'}</p>}<RegionalSettings value={regionalPreference} language={language} nasTimeZone={nasTimeZone} disabled={importDirty||preferenceStatus!=='idle'} onSave={saveRegional} onDirty={setRegionalDirty}/>{regionalDirty&&<p role="status">{language==='de'?'Speichere oder verwirf die Formateinstellungen vor dem Bereichswechsel.':'Save or discard regional settings before changing sections.'}</p>}{/* Export belongs to the authenticated settings surface and discards private results when left. */}{area==='settings'&&<ConfigurationExport language={language} disabled={wizard||bootstrapStatus!=='ready'||preferenceStatus!=='idle'} sessionIdentity={editorSession.current} onSessionDenied={denied}/>}{/* Import serializes settings edits and keeps the confirmed receipt when a refresh needs retrying. */}{area==='settings'&&<ConfigurationImport language={language} disabled={wizard||bootstrapStatus!=='ready'||preferenceStatus!=='idle'||regionalDirty||themeDirty||displayDirty||templateDirty||storageDirty||managementBusy} sessionIdentity={editorSession.current} onDirty={setImportDirty} onSessionDenied={denied} onApplied={importedConfiguration}/>}{area==='settings'&&<DiagnosticExport language={language} disabled={wizard||bootstrapStatus!=='ready'} sessionIdentity={editorSession.current} onSessionDenied={denied}/>}<DiagnosticsSettings language={language} onSessionDenied={denied} />{/* Bundled help and provenance remain available without an external request. */}{area==='settings'&&<HelpSettings language={language}/>}</section>
  </AnalysisShell></RegionalContext.Provider></ThemeContext.Provider></DisplayContext.Provider>;
}

// A background scan does not disable selection in the independently pinned analysis.
export function jobPolling(state:string|undefined):boolean { return ['queued','preflight','scanning','paused','aggregating','validating','publishing'].includes(state??''); }
