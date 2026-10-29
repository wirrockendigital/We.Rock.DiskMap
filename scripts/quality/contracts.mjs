import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// Compare the authoritative DTO block with the actual exported TypeScript declarations.
export function checkContracts(root = process.cwd()) {
  const specification = readFileSync(resolve(root, 'docs/architecture/contracts.md'), 'utf8');
  const block = specification.match(/```ts\n([\s\S]*?)\n```/);
  if (!block) throw new Error('CONTRACT_SPEC_MISSING');
  const actual = readFileSync(resolve(root, 'web/src/contracts.ts'), 'utf8');
  const declarations = actual.slice(actual.indexOf('export type Id'), actual.indexOf('// Reject unsupported'));
  const normalize = value => value.replace(/\bexport\s+/g, '').replace(/\s+/g, ' ').trim();
  if (normalize(block[1]) !== normalize(declarations)) throw new Error('CONTRACT_DRIFT');
}
