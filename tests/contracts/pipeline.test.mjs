import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, cpSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { checkContracts } from '../../scripts/quality/contracts.mjs';
import { checkLocales } from '../../scripts/quality/locales.mjs';

// Fault injection uses isolated copies and never mutates the integration checkout.
test('contract drift and missing required translation fail, then recover', () => {
  const root = mkdtempSync(join(tmpdir(), 'diskmap-quality-'));
  try {
    mkdirSync(join(root, 'docs/architecture'), { recursive: true });
    mkdirSync(join(root, 'web'), { recursive: true });
    cpSync('docs/architecture/contracts.md', join(root, 'docs/architecture/contracts.md'));
    cpSync('web/src', join(root, 'web/src'), { recursive: true });
    checkContracts(root); checkLocales(root);
    const contract = join(root, 'web/src/contracts.ts');
    const original = readFileSync(contract, 'utf8');
    writeFileSync(contract, original.replace('type Bytes = string', 'type Bytes = number'));
    assert.throws(() => checkContracts(root), /CONTRACT_DRIFT/);
    writeFileSync(contract, original); checkContracts(root);
    const locale = join(root, 'web/src/locales/en.json');
    const messages = JSON.parse(readFileSync(locale, 'utf8'));
    const title = messages['app.title']; messages['app.title'] = '';
    writeFileSync(locale, JSON.stringify(messages));
    assert.throws(() => checkLocales(root), /LOCALE_TEXT_MISSING/);
    messages['app.title'] = title; writeFileSync(locale, JSON.stringify(messages)); checkLocales(root);
    delete messages['app.title']; writeFileSync(locale, JSON.stringify(messages));
    assert.throws(() => checkLocales(root), /LOCALE_KEYS_MISMATCH/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
