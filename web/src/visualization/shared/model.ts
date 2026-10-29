import type {QueryProvenance, QuerySourceAvailability} from '../../query-provenance';

// Wire fields are the existing query.Response contract, not a second source inventory.
export interface WireSummary { knownBytes:string; unknownFileCount:string; fileCount:string; directoryCount:string; specialCount:string }
export interface HierarchyNodeInput {
 // The server alone qualifies this optional visual key; absence means unknown.
 colorBranchKey?:string|null;
 id:string; parentId:string|null; realNodeId:string|null; name:string; kind:string; virtual:boolean;
 summary:WireSummary; originalSummary?:WireSummary; matchSummary?:WireSummary; matchKind?:string; hasMore:boolean;
 provenance?:QueryProvenance; classification?:unknown; modifiedAt?:string|null; volumeId?:string; shareId?:string|null;
 groupReason?:string; groupedChildren?:string; listRootId?:string; afterCursor?:string;
}
export interface HierarchyInput {
 snapshotId:string; rootId:string; metric:string; mode?:string; nodes:HierarchyNodeInput[];
 state:string; truncated:boolean; selectionOrder?:string; depth?:number; elementBudget?:number;
 originalSummary:WireSummary; matchSummary:WireSummary; denominator?:WireSummary;
 matchState?:string; filter?:unknown; coverage?:unknown[]; sourceAvailability?:QuerySourceAvailability;
}
export const counters = ['knownBytes','unknownFileCount','fileCount','directoryCount','specialCount'] as const;
export type Summary = Readonly<Record<typeof counters[number],bigint>>;
export type NodeKind = 'nas'|'volume'|'share'|'other'|'directory'|'file'|'symlink'|'fifo'|'socket'|'blockDevice'|'characterDevice'|'group';
export interface Continuation { readonly rootId:string; readonly cursor:string; readonly reason:string; readonly directChildren:bigint }
// Parent identity is immutable source topology. Groups never receive a filesystem path or reveal authority.
export interface MapNode {
 readonly colorBranchKey?:string|null;
 readonly id:string; readonly parentId:string|null; readonly realNodeId:string|null; readonly name:string;
 readonly kind:NodeKind; readonly virtual:boolean; readonly children:readonly string[];
 readonly summary:Summary; readonly original:Summary; readonly match:Summary; readonly matchKind:string;
 readonly weight:bigint; readonly unknown:boolean; readonly hasMore:boolean; readonly opaque:boolean;
 readonly provenance?:Readonly<QueryProvenance>; readonly classification:unknown; readonly modifiedAt:string|null;
 readonly volumeId?:string; readonly shareId?:string|null; readonly continuation:Continuation|null;
 readonly canReveal:boolean;
}
export interface MapModel {
 readonly version:1; readonly snapshotId:string; readonly rootId:string; readonly metric:'logical'|'allocated';
 readonly mode:'highlight'|'hits-only'; readonly state:'complete'|'partial'; readonly truncated:boolean;
 readonly selectionOrder:'recorded-rowid'; readonly nodes:Readonly<Record<string,MapNode>>; readonly order:readonly string[];
 readonly original:Summary; readonly match:Summary; readonly denominator:Summary; readonly matchState:'empty'|'no-match'|'matches';
 readonly coverage:readonly unknown[]; readonly sourceAvailability:Readonly<QuerySourceAvailability>; readonly filter:unknown;
}
export interface HierarchyIdentity {snapshotId:string;rootId:string;metric:'logical'|'allocated';mode:'highlight'|'hits-only';maxNodes:number}
// Canonical decimal parsing never rounds through JavaScript's number representation.
export function parseSummary(value:WireSummary):Summary {
 const out={} as Record<typeof counters[number],bigint>;
 for(const key of counters){const v=value?.[key];if(typeof v!=='string'||!/^(0|[1-9][0-9]{0,127})$/.test(v))throw Error('INVALID_MAP_SUMMARY');out[key]=BigInt(v);}
 if(out.unknownFileCount>out.fileCount)throw Error('INVALID_MAP_SUMMARY');
 return Object.freeze(out);
}
export function addSummaries(values:readonly Summary[]):Summary {
 const sum={knownBytes:0n,unknownFileCount:0n,fileCount:0n,directoryCount:0n,specialCount:0n};
 for(const v of values)for(const key of counters)sum[key]+=v[key];return Object.freeze(sum);
}
export function equalSummary(a:Summary,b:Summary):boolean{return counters.every(key=>a[key]===b[key]);}
// Clone only bounded JSON data; callers cannot mutate the snapshot by retaining input references.
export function immutableJSON(value:unknown,maxBytes=65536):unknown {
 const raw=JSON.stringify(value);if(raw===undefined||new TextEncoder().encode(raw).length>maxBytes)throw Error('INVALID_MAP_METADATA');
 const copy:unknown=JSON.parse(raw);const stack:unknown[]=[copy];let count=0;
 while(stack.length){const item=stack.pop();if(++count>10000)throw Error('INVALID_MAP_METADATA');if(item&&typeof item==='object'){Object.freeze(item);stack.push(...Object.values(item));}}
 return copy;
}
