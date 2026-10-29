import test from 'node:test';
import assert from 'node:assert/strict';
import { assertBytes, roundTripHierarchy, validateHierarchy } from '../../docs/architecture/contracts-v1.mjs';

// Keep fixtures independent of generated application code to test the shared wire contract.
function fixture() {
  return { contractVersion: 1, snapshotId: 'snapshot-A', nodes: [
    { kind: 'directory', id: 'root', snapshotId: 'snapshot-A', parentId: null, logicalBytes: '9007199254740993', allocatedBytes: null },
    { kind: 'file', id: 'child', snapshotId: 'snapshot-A', parentId: 'root', logicalBytes: '0', allocatedBytes: '18446744073709551615' },
  ] };
}

test('wire round trip preserves zero, null, opaque ids and integers above 2^53', () => {
  assert.deepEqual(roundTripHierarchy(fixture()), fixture());
});
test('decimal bytes reject numbers, negatives, exponents and ambiguous encodings', () => {
  for (const value of [0, 9007199254740992, '-1', '01', '1e9', '', undefined, ' 1', 1n]) {
    assert.throws(() => assertBytes(value), /INVALID_BYTES/);
  }
});
test('contract version mismatch fails closed', () => {
  for (const version of [0, 2, '1', undefined]) {
    assert.throws(() => validateHierarchy({ ...fixture(), contractVersion: version }), /CONTRACT_VERSION_UNSUPPORTED/);
  }
});
test('cross-snapshot nodes and foreign parents are rejected', () => {
  const cross = fixture(); cross.nodes[1].snapshotId = 'snapshot-B';
  assert.throws(() => validateHierarchy(cross), /SNAPSHOT_MISMATCH/);
  const foreign = fixture(); foreign.nodes[1].parentId = 'foreign';
  assert.throws(() => validateHierarchy(foreign), /PARENT_MISSING/);
});
test('duplicate ids, self cycles and multi-node cycles are rejected', () => {
  const duplicate = fixture(); duplicate.nodes[1].id = 'root';
  assert.throws(() => validateHierarchy(duplicate), /NODE_ID_INVALID/);
  const self = fixture(); self.nodes[0].parentId = 'root';
  assert.throws(() => validateHierarchy(self), /HIERARCHY_CYCLE/);
  const cycle = fixture(); cycle.nodes[0].parentId = 'child';
  assert.throws(() => validateHierarchy(cycle), /HIERARCHY_CYCLE/);
});
test('deep valid hierarchies do not require recursive traversal', () => {
  const graph = fixture(); graph.nodes = Array.from({ length: 20000 }, (_, i) => ({
    kind: 'directory', id: String(i), snapshotId: graph.snapshotId, parentId: i ? String(i - 1) : null,
    logicalBytes: '0', allocatedBytes: null,
  }));
  assert.equal(validateHierarchy(graph).nodes.length, 20000);
});

test('special object kinds retain null payloads and reject invented zero', () => {
 for (const kind of ['fifo', 'socket', 'blockDevice', 'characterDevice']) {
  const graph = fixture(); Object.assign(graph.nodes[1], {kind, logicalBytes: null, allocatedBytes: null});
  assert.equal(validateHierarchy(graph), graph);
  graph.nodes[1].logicalBytes = '0'; assert.throws(() => validateHierarchy(graph), /NODE_KIND_INVALID/);
 }
 const graph = fixture(); graph.nodes[1].kind = 'other'; assert.throws(() => validateHierarchy(graph), /NODE_KIND_INVALID/);
});
