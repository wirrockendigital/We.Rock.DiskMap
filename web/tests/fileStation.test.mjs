import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, writeFileSync, rmSync, cpSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { createRequire } from 'node:module';

// Compile the production adapter and its real session boundary with the pinned compiler.
const web = fileURLToPath(new URL('../', import.meta.url));
mkdirSync(join(web, '.test-build'), { recursive: true });
const output = mkdtempSync(join(web, '.test-build/filestation-'));
let openFileStation;
try {
  writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
  for (const name of ['dsmFileStation.ts', 'session.ts']) cpSync(join(web, 'src', name), join(output, name));
  execFileSync(join(web, 'node_modules/.bin/tsc'), ['--ignoreConfig', '--target', 'ES2022',
    '--module', 'Node16', '--moduleResolution', 'Node16', '--strict', '--skipLibCheck',
    '--outDir', join(output, 'compiled'), join(output, 'dsmFileStation.ts')], { stdio: 'pipe' });
  ({ openFileStation } = createRequire(import.meta.url)(join(output, 'compiled/dsmFileStation.js')));
} finally { rmSync(output, { recursive: true, force: true }); }

// Each scenario owns its fake DSM state and records exact structured launcher arguments.
function fixture() {
  const calls = [];
  const sds = { Session: { SynoToken: 'synthetic' }, AppLaunch(name, parameters) {
    assert.equal(this, sds); calls.push({ name, parameters });
  } };
  const controller = new AbortController();
  const options = { dsmBuild: '90080', signal: controller.signal, host: { parent: { SYNO: { SDS: sds } } },
    request: async (url, init) => {
      assert.equal(url, './api.cgi?action=session');
      assert.equal(init.headers['X-SYNO-TOKEN'], 'synthetic');
      return new Response(JSON.stringify({ contractVersion: 1, data: { authenticated: true } }));
    } };
  return { calls, sds, controller, options };
}

test('folder path reaches native DSM unchanged, including special characters', async () => {
  for (const path of ['/share/Unicode ä 日本', '/share/100%/# "quoted" \'name\'', '/share/%2F%2E%2E']) {
    const f = fixture();
    assert.deepEqual(await openFileStation({ kind: 'directory', fileStationPath: path }, f.options), { requestedPath: path, highlighted: false });
    assert.deepEqual(f.calls, [{ name: 'SYNO.SDS.App.FileStation3.Instance', parameters: { opendir: path } }]);
  }
});

test('file opens only its exact parent without claiming highlighting', async () => {
  const f = fixture();
  const result = await openFileStation({ kind: 'file', fileStationPath: '/share/deep/a%#".txt', parentPath: '/share/deep' }, f.options);
  assert.deepEqual(result, { requestedPath: '/share/deep', highlighted: false });
  assert.deepEqual(f.calls[0].parameters, { opendir: '/share/deep' });
});

// A removed file never masquerades as an existing file or permits an unverified notice type.
test('removed-file notice permits only the verified directory target', async () => {
  const f = fixture();
  await openFileStation({ kind: 'directory', fileStationPath: '/share/parent', noticeCode: 'FILE_REMOVED_PARENT_AVAILABLE' }, f.options);
  assert.deepEqual(f.calls[0].parameters, { opendir: '/share/parent' });
  for (const target of [
    { kind: 'file', fileStationPath: '/share/file', noticeCode: 'FILE_REMOVED_PARENT_AVAILABLE' },
    { kind: 'directory', fileStationPath: '/share', noticeCode: 'UNVERIFIED_PARENT' },
  ]) {
    const blocked = fixture();
    blocked.options.request = async () => assert.fail('invalid notice must not query the session');
    await assert.rejects(openFileStation(target, blocked.options), /TARGET_INVALID/);
    assert.equal(blocked.calls.length, 0);
  }
});

test('unsupported build and malformed target fail before session or launcher', async () => {
  const targets = [null, { kind: 'other', fileStationPath: '/share' },
    ...['/', 'relative', '/share/', '/share//x', '/share/../x', '/share/./x', '/share/a\n'].map(fileStationPath => ({ kind: 'directory', fileStationPath })),
    { kind: 'file', fileStationPath: '/file' }, { kind: 'file', fileStationPath: '/share/file', parentPath: '/other' }];
  for (const target of targets) {
    const f = fixture(); f.options.request = async () => { assert.fail('unexpected request'); };
    await assert.rejects(openFileStation(target, f.options)); assert.equal(f.calls.length, 0);
  }
  const f = fixture(); f.options.dsmBuild = '90081';
  await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, f.options), /BUILD_UNSUPPORTED/);
});

test('denied or invalid session never launches and parent capability is checked again', async () => {
  for (const response of [new Response('', { status: 401 }), new Response('', { status: 403 }), new Response('{}')]) {
    const f = fixture(); f.options.request = async () => response;
    await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, f.options)); assert.equal(f.calls.length, 0);
  }
  const f = fixture(); const valid = f.options.request;
  f.options.request = async (...args) => { delete f.sds.AppLaunch; return valid(...args); };
  await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, f.options), /UNAVAILABLE/);
});

test('inaccessible parent and cancellation fail closed', async () => {
  const f = fixture(); f.options.host = { get parent() { throw new Error('cross-origin'); } };
  await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, f.options), /UNAVAILABLE/);
  const g = fixture(); const valid = g.options.request;
  g.options.request = async (...args) => { g.controller.abort(); return valid(...args); };
  await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, g.options)); assert.equal(g.calls.length, 0);
});

// DSM exceptions must not expose its internal diagnostic payload to the app's status channel.
test('launcher exceptions become a fixed local failure without claiming navigation success', async () => {
  const f = fixture();
  f.sds.AppLaunch = () => { throw new Error('/private/path token=synthetic-private-diagnostic'); };
  await assert.rejects(openFileStation({ kind: 'directory', fileStationPath: '/share' }, f.options), error => {
    assert.equal(error.code, 'LAUNCH_FAILED');
    assert.equal(error.message, 'FILE_STATION_LAUNCH_FAILED');
    assert.ok(!JSON.stringify(error).includes('private'));
    return true;
  });
});

// The current token is read for each reveal, and a session ending during that check never launches.
test('launcher reads a fresh session token and refuses logout while awaiting authorization', async () => {
  const f = fixture();
  f.sds.Session.SynoToken = 'fresh-token';
  f.options.request = async (_url,init) => {
    assert.equal(init.headers['X-SYNO-TOKEN'],'fresh-token');
    f.sds.Session.SynoToken = '';
    return new Response('',{status:401});
  };
  await assert.rejects(openFileStation({kind:'file',fileStationPath:'/share/file'},f.options),/SESSION_REQUIRED/);
  assert.equal(f.calls.length,0);
});
