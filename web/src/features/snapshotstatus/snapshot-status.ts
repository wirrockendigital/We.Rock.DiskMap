// Status derives only from the selected, qualified snapshot, never from the latest job's snapshot.
export interface SelectedSnapshot {
 snapshotId:string;state:'complete'|'partial';startedAt:string;endedAt:string;profileRevision:number;
}
export type ContentState='loading'|'unavailable'|'no-snapshot'|'empty'|'no-hits'|'bounded-empty'|'data';
export type Freshness='current'|'stale'|'unknown';
export interface SnapshotStatusInput {
 snapshot:SelectedSnapshot|null;currentRevision:number;freshnessSeconds:number;
 newCompleteSnapshotId:string|null;expired:boolean;
 selectionFallback:'none'|'ancestor'|'scope-unavailable';
 contentState:ContentState;
}
// Invalid/future times are unknown; an overflow-prone duration conversion is unnecessary.
export function selectedFreshness(snapshot:SelectedSnapshot|null,seconds:number,now:number):Freshness {
 if(!snapshot||!Number.isSafeInteger(seconds)||seconds<=0||!Number.isFinite(now))return 'unknown';
 const start=Date.parse(snapshot.startedAt),end=Date.parse(snapshot.endedAt);
 if(!Number.isFinite(start)||!Number.isFinite(end)||start>end||now<end)return 'unknown';
 return (now-end)/1000<=seconds?'current':'stale';
}
// A newer complete publication never changes the selected snapshot or its quality.
export function snapshotStatus(input:SnapshotStatusInput,now:number) {
 return {freshness:selectedFreshness(input.snapshot,input.freshnessSeconds,now),
  profileChanged:input.snapshot!==null&&input.snapshot.profileRevision!==input.currentRevision,
  newerComplete:input.newCompleteSnapshotId!==null&&input.newCompleteSnapshotId!==input.snapshot?.snapshotId,
  partial:input.snapshot?.state==='partial',contentState:input.contentState};
}
