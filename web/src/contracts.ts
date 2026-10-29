// Shared version-one wire DTOs; bytes stay decimal strings across the browser boundary.
export type Id = string;
export type Bytes = string;
export type Instant = string;
export type SourceState = 'reachable' | 'unmounted' | 'locked' | 'unreadable' | 'missing' | 'unverified';
// Mixed selection combines stable volume and share identities before disjoint normalization.
export type Scope = { kind: 'nas' | 'volumes' | 'shares' | 'mixed' | 'folder'; targetIds: Id[]; relativePath?: string };
export type Measurement = { value: Bytes | null; source: string; measuredAt: Instant; method: string };
export interface Volume {
  id: Id; name: string; mountPath: string; identityEvidence: string;
  filesystem: string | null; state: SourceState;
  capacity: Measurement; used: Measurement; available: Measurement;
}
export interface Share {
  // Unknown encryption state remains null until confirmed by DSM metadata.
  encrypted: boolean | null; locked: boolean | null; identityEvidence: string;
  id: Id; name: string; volumeId: Id; physicalPath: string;
  fileStationPath: string | null; state: SourceState; readable: boolean;
}
export interface Profile {
  id: Id; revision: number; name: string; scope: Scope; active: boolean;
  exclusions: { kind: 'path' | 'pattern'; value: string }[];
  resourceMode: 'gentle' | 'balanced' | 'fast' | 'custom';
  limits: { workers: number; ioBytesPerSecond: Bytes | null; cpuPercent: number | null };
  freshnessSeconds: number;
}
export type JobState = 'queued' | 'preflight' | 'scanning' | 'paused' | 'aggregating' |
  'validating' | 'publishing' | 'completed' | 'cancelled' | 'interrupted' | 'failed';
export interface Job {
  id: Id; profileId: Id; profileRevision: number; capturedProfile: Profile;
  trigger: 'ui' | 'scheduler' | 'cli'; state: JobState;
  scanMode: 'full' | 'rebuild' | 'reconcile'; createdAt: Instant;
  startedAt: Instant | null; endedAt: Instant | null;
  entriesVisited: string; bytesObserved: Bytes; warnings: ApiError[];
  resultSnapshotId: Id | null; scannerVersion: string; schemaVersion: number;
}
export interface Snapshot {
  id: Id; jobId: Id; profileId: Id; profileRevision: number;
  scopeManifest: { volumeId: Id; shareId: Id | null; rootPath: string }[];
  startedAt: Instant; endedAt: Instant; publishedAt: Instant | null;
  state: 'complete' | 'partial' | 'corrupt'; logicalBytes: Bytes;
  allocatedBytes: Bytes | null; storageBytes: Bytes;
  methods: string[]; coverage: Coverage[]; schemaVersion: number;
}
export interface Node {
  snapshotId: Id; id: Id; parentId: Id | null; originalName: string;
  kind: 'file' | 'directory' | 'symlink' | 'fifo' | 'socket' | 'blockDevice' | 'characterDevice'; volumeId: Id; shareId: Id | null;
  relativePath: string; logicalBytes: Bytes | null; allocatedBytes: Bytes | null;
  modifiedAt: Instant | null; createdAt: Instant | null;
  ownerId: string | null; linkCount: number | null; linkGroupId: Id | null;
}
export interface Coverage {
  snapshotId: Id; rootId: Id; state: 'readable' | 'excluded' | 'missing' | 'error';
  knownEntries: string | null; reasonCode: string | null; measurementLimits: string[];
}
export interface UserPreference {
 workspace?:PreferenceWorkspace;
  version: 2; lastView: PreferenceView | null;
  display: PreferenceDisplay;
  dsmUserId: Id; revision: number; language: 'de' | 'en' | 'auto';
  theme: 'light' | 'dark' | 'auto'; preferredProfileId: Id | null;
  map: 'treemap' | 'radial'; locale: string | null; timeZone: string | null;
}
// Personal preferences are server-owned; remembered identities require independent restore validation.
export interface PreferenceFilter {
  categories: string[]; minSize?: Bytes; maxSize?: Bytes; modifiedFrom?: string; modifiedTo?: string;
  objectKinds?: ('file' | 'directory')[]; text?: string; field?: 'name' | 'path' | 'both';
}
export interface PreferenceSearch { text: string; field: 'name' | 'path' | 'both'; scope: 'direct' | 'recursive' }
export interface PreferenceView {
  profileId: Id; snapshotId: Id; rootId: Id; selectedNodeId: Id | null;
  map: 'treemap' | 'radial'; metric: 'logical' | 'allocated'; filterMode: 'highlight' | 'hits-only';
  filter: PreferenceFilter; search: PreferenceSearch; sort: 'name' | 'size' | 'mtime' | 'type'; direction: 'asc' | 'desc';
}
export interface PreferenceAppearance {
  language: 'auto' | 'de' | 'en'; theme: 'auto' | 'light' | 'dark'; map: 'treemap' | 'radial';
  locale: string | null; timeZone: string | null;
}
// Display preferences are a complete versioned group, with no implicit missing-field defaults.
export interface PreferenceDisplay {
  units: 'decimal' | 'binary'; colorMode: 'folder' | 'filetype' | 'mtime';
  detail: 'auto' | 'max' | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8;
  labels: boolean; percentages: boolean; hierarchyBorders: 'normal' | 'subtle';
  reducedMotion: 'auto' | 'reduce';
}
export type PreferenceRequest = { contractVersion: 2; expectedRevision: number } & (
  { group: 'workspace'; workspace:PreferenceWorkspace } |
  { group: 'display'; display: PreferenceDisplay } |
  { group: 'appearance'; appearance: PreferenceAppearance } |
  { group: 'preferredProfile'; preferredProfile: { profileId: Id | null } } |
  { group: 'lastView'; view: { value: PreferenceView | null } }
);

export interface ApiError { code: string; messageKey: string; requestId: Id; retryable: boolean }
export type Envelope<T> = { contractVersion: 1; requestId: Id; data: T };

// Schema-seven aggregates count regular path entries; missing type keys denote zero vectors.
export interface MetricAggregate { knownBytes: Bytes; unknownFileCount: Bytes }
export interface TypeAggregate { fileCount: Bytes; logical: MetricAggregate; allocated: MetricAggregate }
export interface ContentAggregate {
  fileCount: Bytes; directoryCount: Bytes; specialCount: Bytes;
  logical: MetricAggregate; allocated: MetricAggregate; types: Record<string, TypeAggregate>;
}
export interface DirectoryAggregate { direct: ContentAggregate; subtree: ContentAggregate }
// Configured suffixes use ASCII folding and exact non-ASCII UTF-8, without normalization.
export interface ExtensionMapping { extension: string; category: string }
export interface FileTypeMapping { version: number; revision: number; assignments: ExtensionMapping[] }

// Job phase history records bounded durable transitions, independently of snapshot freshness.
export interface JobPhase { state: JobState; enteredAt: Instant }
export interface StoredDataStatus {
  snapshotId: Id | null; quality: 'complete' | 'corrupt' | 'missing' | 'unverified' | 'unsupported';
  freshness: 'current' | 'stale' | 'unknown'; endedAt: Instant | null; ageSeconds: Bytes | null;
}

// Reject unsupported envelopes before any consumer reads their payload.
export function assertVersion(value: {contractVersion: number}): void {
  if (value.contractVersion !== 1) throw new Error('CONTRACT_VERSION_UNSUPPORTED');
}

// Convert byte values only to BigInt, preserving unknown values explicitly.
export function parseBytes(value: unknown): bigint | null {
  if (value === null) return null;
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value)) throw new Error('INVALID_BYTES');
  return BigInt(value);
}

// Boundary examples are display-only; full raw paths remain private generation records.
export type ScanBoundary = {
 targetId: Id; nodeId: Id | null;
 reasonCode: 'PACKAGE_STATE_EXCLUDED' | 'OUTSIDE_SCOPE_MOUNT' | 'SNAPSHOT_TREE_EXCLUDED' | 'SUBVOLUME_CLASSIFICATION_UNAVAILABLE' | 'SOURCE_UNREADABLE' | 'SOURCE_MISSING' | 'SOURCE_TRANSIENT' | 'SOURCE_CHANGED' | 'SOURCE_IO_ERROR' | 'SOURCE_LOCKED' | 'SOURCE_NOT_MOUNTED' | 'SOURCE_UNVERIFIED' | 'DEPTH_LIMIT' | 'PROFILE_EXCLUDED' | 'SCOPE_CHANGED' | 'SCOPE_VALIDATION_UNAVAILABLE';
 relativePath: string;
};
// Partial or unavailable measurements have null totals, never invented zero bytes.
export type PackageUsage = {
 logicalBytes: Bytes | null; allocatedBytes: Bytes | null; measuredAt: Instant;
 state: 'complete' | 'partial' | 'unavailable'; method: 'package-state-stat';
};

// Partial-job boundaries are explicitly separate from the active complete snapshot.
export type ScanBoundaryReport = {
 jobId: Id; snapshotId: Id; state: 'complete' | 'partial'; endedAt: Instant;
 profileRevision: number; boundaries: ScanBoundary[]; count: Bytes; truncated: boolean;
};


// Workspace state is private server-side presentation data, not an authorization source.
export interface PreferenceWorkspace {
 // An absent selection uses the default mixed presentation.
 sizeUnit?:'TB'|'GB'|'MB'|'mixed'|'hidden';
 // Missing on older saved workspaces means the share status remains visible.
 showShareStatus?:boolean;
 source?:Scope|null;scanRecycle?:boolean;scanSystem?:boolean;
 jobs?:string[]|null;zoom?:{snapshotId?:string;root:string;scale:number;x:number;y:number}|null;
 left:boolean;right:boolean;leftWidth:number;rightWidth:number;expanded:string[];
 treeSort:'name'|'size';treeDirection:'asc'|'desc';treeScroll:number;listScroll:number;filterOpen:boolean;
 list:{text:string;recursive:boolean;kind:'all'|'file'|'directory';category:string;sort:'name'|'size'|'type';direction:'asc'|'desc';foldersFirst:boolean};
 showRecycle:boolean;showSystem:boolean;
}
