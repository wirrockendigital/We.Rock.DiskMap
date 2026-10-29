// Presentation data stays independent of DSM credentials, profile IDs and scan transports.
export interface StorageNode {
  // System and recycle descendants retain category presentation without changing source permissions.
  muted?:boolean;hidden?:boolean;
  // Category totals describe the original index even when geometry hides those files.
  normalBytes?:string;recycleBytes?:string;hiddenBytes?:string;
  // Availability comes from the loaded index, independent of future scan preferences.
  indexedRecycle?:boolean;indexedSystem?:boolean;
  id: string;
  name: string;
  kind: 'volume' | 'share' | 'folder' | 'file';
  // A qualified parent identity links sibling presentation colors without revealing paths.
  colorParentId?: string;
  bytes?: number;
  // Exact recorded totals remain independent of bounded chart children.
  bytesExact?: string;
  path?: string;
  // Recorded file category and parent are list presentation metadata only.
  // Opaque map cells represent recorded aggregate contents, never invented files.
  aggregated?: boolean;
  // Detail regions route to a real directory and never authorize file actions.
  detailTarget?:string;
  fileCount?: string;
  category?: string;
  parentId?: string | null;
  unavailable?: boolean;
  capacity?: number;
  children?: StorageNode[];
}
export type Language = 'de' | 'en';
export const branchColors = ['#64dcb5', '#58bed4', '#8f93f0', '#d58bd3', '#e8ba80', '#92c783', '#8b9caf'];

// Aggregate leaf sizes once per small presentation tree; no filesystem traversal occurs here.
export function sizeOf(node: StorageNode): number {
  return node.bytesExact !== undefined ? Number(node.bytesExact) : node.bytes ?? node.children?.reduce((sum, child) => sum + sizeOf(child), 0) ?? 0;
}
export function formatSize(bytes: number, language: Language = 'de'): string {
  const unit = bytes >= 1e12 ? 'TB' : bytes >= 1e9 ? 'GB' : bytes >= 1e6 ? 'MB' : 'KB';
  const divisor = {TB: 1e12, GB: 1e9, MB: 1e6, KB: 1e3}[unit];
  return new Intl.NumberFormat(language, {maximumFractionDigits: 1}).format(bytes / divisor) + ' ' + unit;
}
export function pathTo(nodes: StorageNode[], id: string): StorageNode[] {
  for (const node of nodes) {
    if (node.id === id) return [node];
    const child = pathTo(node.children ?? [], id);
    if (child.length) return [node, ...child];
  }
  return [];
}

// Format decimal byte counts without first rounding them through a floating-point number.
export function formatNodeSize(node: StorageNode, language: Language = 'de'): string {
  if (node.unavailable && node.bytesExact === undefined) return '—';
  if (node.bytesExact === undefined) return formatSize(sizeOf(node), language);
  const bytes = BigInt(node.bytesExact);
  const [unit, divisor] = bytes >= 1000000000000n ? ['TB', 1000000000000n] as const : bytes >= 1000000000n ? ['GB', 1000000000n] as const : bytes >= 1000000n ? ['MB', 1000000n] as const : bytes >= 1000n ? ['KB', 1000n] as const : ['B', 1n] as const;
  const tenths = (bytes * 10n + divisor / 2n) / divisor;
  return new Intl.NumberFormat(language).format(tenths / 10n) + (tenths % 10n ? (language === 'de' ? ',' : '.') + String(tenths % 10n) : '') + ' ' + unit;
}
