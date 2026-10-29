import test from 'node:test';
import assert from 'node:assert/strict';
import {dsmCapabilities, resolveLanguage, resolveTheme, schedulerInformation, outsideV1} from '../../web/src/dsm-capabilities.ts';

// Missing DSM signals use the mandated browser/English policy without reading a global DSM object.
test('language fallback and manual precedence', () => {
  for (const tag of ['de-DE', 'de-CH', 'DE-at']) assert.deepEqual(resolveLanguage('auto', tag), {language:'de',source:'browser'});
  assert.deepEqual(resolveLanguage('auto', 'en-GB'), {language:'en',source:'browser'});
  for (const tag of [null, undefined, '', 'de_DE', 'fr-FR', {}, 'x'.repeat(129)]) assert.deepEqual(resolveLanguage('auto', tag), {language:'en',source:'fallback'});
  assert.deepEqual(resolveLanguage('en', 'de-DE'), {language:'en',source:'manual'});
  assert.deepEqual(resolveLanguage('de', null), {language:'de',source:'manual'});
});

// Theme source is explicit; truthy arbitrary values cannot impersonate a system preference.
test('theme fallback and manual precedence', () => {
  assert.deepEqual(resolveTheme('auto', true), {theme:'dark',source:'browser-system'});
  assert.deepEqual(resolveTheme('auto', false), {theme:'light',source:'browser-system'});
  for (const signal of [undefined, null, 'dark', 1]) assert.deepEqual(resolveTheme('auto', signal), {theme:'light',source:'fallback'});
  assert.deepEqual(resolveTheme('light', true), {theme:'light',source:'manual'});
  assert.deepEqual(resolveTheme('dark', false), {theme:'dark',source:'manual'});
});

// Qualification is build-exact and immutable; it does not fabricate task or runtime availability.
test('build boundaries, missing tasks and deferred capabilities', () => {
  const measured=dsmCapabilities('90080');
  assert.equal(measured.folderHandoff.status,'qualified-probe');
  assert.equal(measured.localScheduledScan.status,'qualified-probe');
  for (const build of ['', '90081', '7.4.1', '90080-beta']) {
    const other=dsmCapabilities(build);
    assert.equal(other.folderHandoff.status,'unqualified');
    assert.equal(other.localScheduledScan.status,'unqualified');
  }
  for (const key of ['sessionLanguage','sessionTheme']) assert.equal(measured[key].status,'unqualified');
  for (const key of ['fileHighlight','systemNotifications','schedulerDiscovery','schedulerCreation','filesystemEvents','restartContinuation','indexVolumeMigration']) assert.equal(measured[key].status,'deferred-p1');
  assert.deepEqual(schedulerInformation(), {source:'unavailable', tasks:null, nextRun:null});
  assert.throws(()=>{measured.systemNotifications.status='qualified-probe'},TypeError);
  assert(outsideV1.includes('non-admin-index-access'));
  assert(outsideV1.includes('content-duplicates'));
  assert(!outsideV1.includes('folderHandoff'));
  assert(!outsideV1.includes('localScheduledScan'));
});
