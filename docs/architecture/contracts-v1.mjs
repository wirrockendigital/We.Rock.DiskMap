// Executable reference for the version-one wire invariants; product adapters share these fixtures.
export const CONTRACT_VERSION = 1;

// Preserve unknown values and reject lossy JSON numbers or noncanonical decimal strings.
export function assertBytes(value) {
  if (value !== null && (typeof value !== 'string' || !/^(0|[1-9][0-9]*)$/.test(value))) {
    throw new Error('INVALID_BYTES');
  }
  return value;
}

// Validate graph identity before any UI or persistence consumer accepts a hierarchy.
export function validateHierarchy(envelope) {
  if (envelope?.contractVersion !== CONTRACT_VERSION) throw new Error('CONTRACT_VERSION_UNSUPPORTED');
  if (typeof envelope.snapshotId !== 'string' || !envelope.snapshotId || !Array.isArray(envelope.nodes)) {
    throw new Error('INVALID_REQUEST');
  }
  const nodes = new Map();
  for (const node of envelope.nodes) {
    if (typeof node.id !== 'string' || !node.id || nodes.has(node.id)) throw new Error('NODE_ID_INVALID');
    if (node.snapshotId !== envelope.snapshotId) throw new Error('SNAPSHOT_MISMATCH');
    if (node.parentId !== null && (typeof node.parentId !== 'string' || !node.parentId)) throw new Error('PARENT_INVALID');
    if (!['file', 'directory', 'symlink', 'fifo', 'socket', 'blockDevice', 'characterDevice'].includes(node.kind)) throw new Error('NODE_KIND_INVALID');
    if (['symlink', 'fifo', 'socket', 'blockDevice', 'characterDevice'].includes(node.kind) && (node.logicalBytes !== null || node.allocatedBytes !== null)) throw new Error('NODE_KIND_INVALID');
    assertBytes(node.logicalBytes);
    assertBytes(node.allocatedBytes);
    nodes.set(node.id, node);
  }
  const complete = new Set();
  for (const node of nodes.values()) {
    const path = new Set();
    let current = node;
    while (current && !complete.has(current.id)) {
      if (path.has(current.id)) throw new Error('HIERARCHY_CYCLE');
      path.add(current.id);
      if (current.parentId !== null && !nodes.has(current.parentId)) throw new Error('PARENT_MISSING');
      current = current.parentId === null ? null : nodes.get(current.parentId);
    }
    for (const id of path) complete.add(id);
  }
  return envelope;
}

// JSON round trips must preserve decimal bytes, explicit null, and opaque identifiers exactly.
export function roundTripHierarchy(envelope) {
  validateHierarchy(envelope);
  return validateHierarchy(JSON.parse(JSON.stringify(envelope)));
}
