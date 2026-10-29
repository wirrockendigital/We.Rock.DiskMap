import {readFileSync, readdirSync} from 'node:fs';
import {join} from 'node:path';
import {execFileSync} from 'node:child_process';

// Metadata is generated from local locked inputs, never from a network service.
export function createAboutMetadata(root, {sourceId = null, go = process.env.GO || 'go', goCache, goRoot} = {}) {
  const read = path => readFileSync(join(root, path), 'utf8');
  const pkg = JSON.parse(read('web/package.json'));
  const lock = JSON.parse(read('web/package-lock.json'));
  const info = read('packaging/INFO');
  const packageVersion = /^version="([^"]+)"$/m.exec(info)?.[1];
  if (!/^\d+\.\d+\.\d+$/.test(pkg.version) || !packageVersion || !new RegExp(`^${pkg.version.replaceAll('.', '\\.')}-[0-9]+$`).test(packageVersion) || lock.version !== pkg.version || lock.packages[''].version !== pkg.version) throw new Error('ABOUT_VERSION_MISMATCH');
  if (sourceId !== null && !/^sha256:[a-f0-9]{64}$/.test(sourceId)) throw new Error('ABOUT_SOURCE_INVALID');
  const components = [];
  const licenses = [];
  // Preserve complete license notices as literal text; React escapes their rendering.
  const license = (component, name, text) => {
    if (!text.trim() || Buffer.byteLength(text) > 128 * 1024) throw new Error('ABOUT_LICENSE_INVALID');
    licenses.push({component, name, text});
  };
  license('We.Rock.DiskMap', 'LICENSE', read('LICENSE'));
  for (const name of Object.keys(pkg.dependencies)) {
    if (lock.packages[`node_modules/${name}`]?.version !== pkg.dependencies[name]) throw new Error('ABOUT_COMPONENT_MISMATCH');
  }
  // Include transitive production dependencies such as React's scheduler, not development tooling.
  for (const [location, pinned] of Object.entries(lock.packages).filter(([location, value]) => location && !value.dev).sort(([a], [b]) => a.localeCompare(b))) {
    if (!/^node_modules\/(?:@[a-z0-9._-]+\/)?[a-z0-9._-]+$/.test(location)) throw new Error('ABOUT_COMPONENT_PATH_INVALID');
    const name = location.slice('node_modules/'.length);
    const installed = JSON.parse(read(`web/${location}/package.json`));
    if (installed.name !== name || installed.version !== pinned.version) throw new Error('ABOUT_COMPONENT_MISMATCH');
    components.push({name, version: pinned.version, kind: 'ui'});
    license(name, 'LICENSE', read(`web/${location}/LICENSE`));
  }
  const mod = read('go.mod');
  const goVersion = /^go ([0-9.]+)$/m.exec(mod)?.[1];
  if (!goVersion) throw new Error('ABOUT_GO_VERSION_INVALID');
  // Only inspect an already installed toolchain and module cache; no download command is used.
  const env = (!goCache || !goRoot) ? JSON.parse(execFileSync(go, ['env', '-json', 'GOMODCACHE', 'GOROOT', 'GOVERSION'], {encoding: 'utf8', timeout: 10000, env: {...process.env, GOTOOLCHAIN: 'local', GOPROXY: 'off'}})) : {};
  if (env.GOVERSION && env.GOVERSION !== `go${goVersion}`) throw new Error('ABOUT_TOOLCHAIN_MISMATCH');
  const cache = goCache || env.GOMODCACHE;
  const toolchain = goRoot || env.GOROOT;
  components.push({name: 'Go', version: goVersion, kind: 'toolchain'});
  license('Go', 'LICENSE', readFileSync(join(toolchain, 'LICENSE'), 'utf8'));
  const modules = [...mod.matchAll(/^\s+([a-z0-9./-]+) (v[^\s]+)(?: \/\/ indirect)?$/gm)];
  if (!modules.length) throw new Error('ABOUT_MODULES_MISSING');
  for (const [, name, version] of modules) {
    const dir = join(cache, `${name}@${version}`);
    const names = readdirSync(dir).filter(n => /^LICENSE(?:[.-][A-Za-z0-9_-]+)*$/.test(n)).sort();
    if (!names.length) throw new Error('ABOUT_LICENSE_MISSING');
    components.push({name, version, kind: 'backend'});
    for (const filename of names) license(name, filename, readFileSync(join(dir, filename), 'utf8'));
  }
  const result = {version: 1, appVersion: pkg.version, packageVersion, sourceId, components, licenses};
  if (Buffer.byteLength(JSON.stringify(result)) > 512 * 1024) throw new Error('ABOUT_METADATA_LIMIT');
  return result;
}
