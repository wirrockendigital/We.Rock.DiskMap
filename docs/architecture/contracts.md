# DiskMap data contracts

This document defines the shared data types used by the DiskMap service and interface. The TypeScript declarations below match the public frontend contract and are checked during quality validation.

The service returns versioned JSON responses. Byte counts are decimal strings to preserve full integer precision; `null` means the value is unknown. Published scan generations are immutable. An incomplete scan must retain its coverage state rather than present missing data as zero.

## Shared types

```ts
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
```

## Data integrity

The service verifies source scope and permissions before scan work or file handoff. A new scan result replaces the selected saved result only after validation. Readers can continue using an earlier generation while a refresh runs. File Station actions require the current DSM session and its permissions.
