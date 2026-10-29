import { assertVersion, parseBytes, type Envelope } from './contracts.js';

// These assertions run after TypeScript compilation and fail the process on drift.
function check(condition: boolean, message: string): void { if (!condition) throw new Error(message); }
const wire: Envelope<{ logicalBytes: string; unknownBytes: null; id: string }> = {
  contractVersion: 1, requestId: 'test', data: { logicalBytes: '9007199254740993', unknownBytes: null, id: 'opaque-A' },
};
const copy = JSON.parse(JSON.stringify(wire)) as typeof wire;
assertVersion(copy);
check(copy.data.id === 'opaque-A', 'identity changed');
check(parseBytes(copy.data.logicalBytes) === 9007199254740993n, 'precision lost');
check(parseBytes(copy.data.unknownBytes) === null, 'unknown became zero');
check(parseBytes('0') === 0n, 'zero changed');
for (const invalid of [0, '-1', '01', '1e9', undefined]) {
  let rejected = false;
  try { parseBytes(invalid); } catch { rejected = true; }
  check(rejected, 'invalid bytes accepted');
}
let rejected = false;
try { assertVersion({ contractVersion: 2 }); } catch { rejected = true; }
check(rejected, 'unknown contract version accepted');
