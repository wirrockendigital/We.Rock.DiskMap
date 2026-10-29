// Public historical provenance is descriptive only and never grants path or reveal authority.
export interface QueryProvenance {
  version: 1;
  authority: 'native-inventory' | 'explicit-roots' | 'synthetic-fixture';
  sourceId: string | null;
  qualification: 'qualified' | 'unavailable' | 'explicit';
  state: 'reachable' | 'missing' | 'unreadable' | 'locked' | 'unmounted' | 'unverified' | 'unavailable';
}
// Reject unknown fields so private descriptor bindings cannot cross this public boundary.
export function validQueryProvenance(value: unknown): value is QueryProvenance {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const p = value as QueryProvenance;
  if (Object.keys(p).length !== 5 || !Object.keys(p).every(k => ['version','authority','sourceId','qualification','state'].includes(k)) ||
      p.version !== 1 || !(p.sourceId === null || typeof p.sourceId === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(p.sourceId)) ||
      !['reachable','missing','unreadable','locked','unmounted','unverified','unavailable'].includes(p.state)) return false;
  if (p.authority === 'native-inventory') return p.qualification === 'unavailable' || p.qualification === 'qualified' && p.state === 'reachable' && p.sourceId !== null;
  return ['explicit-roots','synthetic-fixture'].includes(p.authority) && p.qualification === 'explicit';
}
// Ordinary recorded nodes may omit provenance; newly projected source containers must carry it.
export function validNodeProvenance(node: { kind: string; provenance?: QueryProvenance }): boolean {
  return node.provenance === undefined ? !['share','other'].includes(node.kind) : validQueryProvenance(node.provenance);
}

// Availability records describe unmeasured historical areas without inventing numeric content.
export interface QuerySourceAvailability {
  total: string;
  truncated: boolean;
  records: {name:string;provenance:QueryProvenance;reason:'source-unavailable'|'content-not-recorded'}[];
}
// Counts and truncation refer to all matching captured records before the bounded projection.
export function validSourceAvailability(value: unknown): value is QuerySourceAvailability {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const a = value as QuerySourceAvailability;
  if (Object.keys(a).length !== 3 || !Object.keys(a).every(k => ['total','truncated','records'].includes(k)) ||
      typeof a.total !== 'string' || !/^(0|[1-9][0-9]{0,127})$/.test(a.total) || typeof a.truncated !== 'boolean' ||
      !Array.isArray(a.records) || a.records.length > 32 || BigInt(a.total) < BigInt(a.records.length) ||
      a.truncated !== (BigInt(a.total) > BigInt(a.records.length))) return false;
  if (!a.records.every(r => r && typeof r === 'object' && Object.keys(r).length === 3 &&
      Object.keys(r).every(k => ['name','provenance','reason'].includes(k)) && typeof r.name === 'string' &&
      validQueryProvenance(r.provenance) && ['source-unavailable','content-not-recorded'].includes(r.reason))) return false;
  return new TextEncoder().encode(JSON.stringify(a)).byteLength <= 16384;
}
