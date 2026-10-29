import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Check key parity, non-empty required messages, and literal message lookups.
export function checkLocales(root = process.cwd()) {
  const directory = resolve(root, 'web/src/locales');
  const catalogues = ['de', 'en'].map(locale => JSON.parse(readFileSync(resolve(directory, `${locale}.json`), 'utf8')));
  const keys = Object.keys(catalogues[0]).sort();
  if (!keys.length || JSON.stringify(keys) !== JSON.stringify(Object.keys(catalogues[1]).sort())) throw new Error('LOCALE_KEYS_MISMATCH');
  for (const catalogue of catalogues) for (const key of keys) {
    if (typeof catalogue[key] !== 'string' || !catalogue[key].trim()) throw new Error(`LOCALE_TEXT_MISSING:${key}`);
  }
  const walk = directory => readdirSync(directory, { withFileTypes: true }).flatMap(entry =>
    entry.isDirectory() ? walk(resolve(directory, entry.name)) : /\.[jt]sx?$/.test(entry.name) ? [resolve(directory, entry.name)] : []);
  for (const file of walk(resolve(root, 'web/src'))) {
    for (const match of readFileSync(file, 'utf8').matchAll(/messages\[['"]([^'"]+)['"]\]/g)) {
      if (!keys.includes(match[1])) throw new Error(`LOCALE_KEY_UNKNOWN:${match[1]}`);
    }
  }
}
