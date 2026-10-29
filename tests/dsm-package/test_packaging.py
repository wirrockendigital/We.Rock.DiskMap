"""Exercise package construction helpers and hooks without building or installing an SPK."""
import importlib.util
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import stat
import struct
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT/'scripts'))
spec = importlib.util.spec_from_file_location('package_spk', ROOT/'scripts/package-spk.py')
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)


class PackagingTests(unittest.TestCase):
    """Test deterministic containers, build-byte binding and safe preflight failure."""

    def test_boot_urls_change_for_equal_size_updates(self):
        # DSM's zero-mtime ETag collides for these two same-size HTML documents.
        config = (ROOT/'packaging/ui/config').read_bytes()
        script = (ROOT/'packaging/ui/DiskMap.js').read_bytes()
        versions = []
        for html in [b'<script src="./assets/old.js"></script>',
                     b'<script src="./assets/new.js"></script>']:
            inputs = [('ui/config', config, 0o644), ('ui/DiskMap.js', script, 0o644),
                      ('ui/index.html', html, 0o644)]
            bound = package.bind_ui_entrypoints(inputs)
            self.assertEqual(bound, package.bind_ui_entrypoints(list(reversed(inputs))))
            files = {name: data for name, data, _ in bound}
            registration = json.loads(files['ui/config'])
            launcher = next(iter(registration))
            entry = 'index-'+hashlib.sha256(html).hexdigest()+'.html'
            self.assertIn(('/webman/3rdparty/we-rock-diskmap/'+entry).encode(), files['ui/'+launcher])
            self.assertEqual(launcher, 'DiskMap-'+hashlib.sha256(files['ui/'+launcher]).hexdigest()+'.js')
            self.assertEqual(files['ui/'+entry], html)
            self.assertNotIn('ui/index.html', files)
            self.assertNotIn('ui/DiskMap.js', files)
            self.assertIn('SYNO.SDS.App.DiskMap.Instance', registration[launcher])
            self.assertIn('SYNO.SDS.App.DiskMap.HelpInstance', registration[launcher])
            versions.append((launcher, entry, len(html)))
        self.assertEqual(versions[0][2], versions[1][2])
        self.assertNotEqual(versions[0][:2], versions[1][:2])
        with self.assertRaises(ValueError):
            package.bind_ui_entrypoints(inputs+inputs)
        with self.assertRaises(ValueError):
            package.bind_ui_entrypoints(inputs[1:])

    def test_access_wizard_and_package_privileges(self):
        # Both installation paths explain the same restricted runtime identity.
        for operation in ('install', 'upgrade'):
            name, raw, mode = package.access_wizard(ROOT/'packaging', operation)
            self.assertEqual(name, f'WIZARD_UIFILES/{operation}_uifile')
            ui = json.loads(raw)[0]
            self.assertEqual(ui['custom_render_name'], 'diskmap_access')
            self.assertIn('ausschließlich von DSM-Administratoren', ui['custom_render_fn'])
            self.assertIn('All administrators share the same scan data', ui['custom_render_fn'])
            self.assertEqual(mode, 0o644)
        self.assertEqual(json.loads((ROOT/'packaging/conf/privilege').read_text()), {'defaults': {'run-as': 'package'}})

    def test_archive_reproducible_and_unique(self):
        files = [('b', b'payload', 0o755), ('a', b'metadata', 0o644)]
        first = package.archive(files)
        self.assertEqual(first, package.archive(list(reversed(files))))
        with tarfile.open(fileobj=io.BytesIO(first)) as archive:
            self.assertEqual(archive.getnames(), ['a', 'b'])
            for item in archive:
                self.assertEqual((item.uid, item.gid, item.mtime), (0, 0, 0))
        for invalid in [files+files, [('../escape', b'', 0o644)], [('AGENTS.md', b'', 0o644)]]:
            with self.assertRaises(ValueError):
                package.archive(invalid)

    def test_public_ui_directories_do_not_inherit_private_umask(self):
        # Extraction plus the real install hook must restore public traversal under umask 077.
        raw = package.archive([('ui/images/icon.png', b'fixture', 0o644)])
        with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
            for name in ['ui', 'ui/images']:
                self.assertEqual(archive.getmember(name).mode, 0o755)
        with tempfile.TemporaryDirectory() as temp:
            artifact = Path(temp)/'fixture.tar'
            artifact.write_bytes(raw)
            # Python's data filter deliberately omits directory modes; use native tar here.
            result = subprocess.run(['tar', '-xf', str(artifact), '-C', temp],
                                    umask=0o077, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            result = subprocess.run(['sh', str(ROOT/'packaging/scripts/postinst')],
                                    env=dict(os.environ, SYNOPKG_PKGDEST=temp), capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            for name in ['ui', 'ui/images']:
                self.assertEqual(stat.S_IMODE((Path(temp)/name).stat().st_mode), 0o755)

    def test_postinst_repairs_only_public_resource_directories(self):
        # Existing upgrades must repair old 0700 directories without widening private state.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in ['ui/images', 'var/private']:
                (root/name).mkdir(parents=True)
                (root/name).chmod(0o700)
            (root/'ui').chmod(0o700)
            result = subprocess.run(['sh', str(ROOT/'packaging/scripts/postinst')],
                                    env=dict(os.environ, SYNOPKG_PKGDEST=temp), capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            for name in ['ui', 'ui/images']:
                self.assertEqual(stat.S_IMODE((root/name).stat().st_mode), 0o755)
            self.assertEqual(stat.S_IMODE((root/'var/private').stat().st_mode), 0o700)

    def test_outputs_reject_missing_foreign_and_changed_bytes(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(package, 'ROOT', Path(temp)):
            root = Path(temp)
            (root/'dist/bin').mkdir(parents=True)
            (root/'dist/ui').mkdir()
            with self.assertRaises(SystemExit):
                package.outputs()
            elf = bytearray(120)
            elf[:6] = b'\x7fELF\x02\x01'
            elf[18:20] = (62).to_bytes(2, 'little')
            elf[32:40] = (64).to_bytes(8, 'little')
            elf[54:56] = (56).to_bytes(2, 'little')
            elf[56:58] = (1).to_bytes(2, 'little')
            elf[64:68] = (1).to_bytes(4, 'little')
            for name in ['bin/diskmapd', 'bin/diskmapctl', 'ui/api.cgi']:
                (root/'dist'/name).write_bytes(elf)
            (root/'dist/ui/index.html').write_text('<main>fixture</main>')
            data = package.outputs()
            original = package.output_record({'sourceId':'first'}, data, 'vdsm-native-spk-test')
            self.assertNotEqual(original, package.output_record({'sourceId':'second'}, data, 'vdsm-native-spk-test'))
            (root/'dist/ui/index.html').write_text('changed')
            self.assertNotEqual(original, package.output_record({'sourceId':'first'}, package.outputs(), 'vdsm-native-spk-test'))
            (root/'dist/bin/old-binary').write_bytes(elf)
            with self.assertRaises(SystemExit):
                package.outputs()
            (root/'dist/bin/old-binary').unlink()
            dynamic = bytearray(elf)
            dynamic[64:68] = (3).to_bytes(4, 'little')
            (root/'dist/bin/diskmapd').write_bytes(dynamic)
            with self.assertRaises(SystemExit):
                package.outputs()
            (root/'dist/bin/diskmapd').write_text('wrong architecture')
            with self.assertRaises(SystemExit):
                package.outputs()

    def test_preflight_rejects_arch_build_space_and_unknown_destination(self):
        # Only literal system paths are replaced in a temporary fixture; production has no test bypass.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            volume = root/'volume1'
            volume.mkdir()
            config = root/'synoinfo.conf'
            config.write_text('unique="synology_kvmx64_virtualdsm"\n')
            script = package.preflight_script((ROOT/'packaging/scripts/preinst').read_bytes(), 1024, 'vdsm-native-spk-test').decode()
            script = script.replace('/etc.defaults/synoinfo.conf', str(config)).replace("'^/volume[1-9][0-9]*$'", "'^"+str(root)+"/volume[1-9][0-9]*$'")
            hook = root/'preinst'
            hook.write_text(script)
            binaries = root/'tools'
            binaries.mkdir()
            for name, content in [('uname', '#!/bin/sh\nprintf "%s\\n" "$FIXTURE_ARCH"\n'), ('df', '#!/bin/sh\nprintf "Filesystem 1024-blocks Used Available Capacity Mounted\\nfixture 999999 0 %s 0%% /fixture\\n" "$FIXTURE_FREE"\n')]:
                path = binaries/name
                path.write_text(content)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(binaries)+':/usr/bin:/bin', SYNOPKG_DSM_VERSION_MAJOR='7', SYNOPKG_DSM_VERSION_BUILD='90080', SYNOPKG_PKGDEST_VOL=str(volume), FIXTURE_ARCH='x86_64', FIXTURE_FREE='65538')
            good = subprocess.run(['/bin/sh',str(hook)],env=env,capture_output=True)
            self.assertEqual(good.returncode,0,good.stderr)
            for changes in [dict(FIXTURE_ARCH='aarch64'),dict(SYNOPKG_DSM_VERSION_BUILD='90081'),dict(SYNOPKG_DSM_VERSION_BUILD=''),dict(SYNOPKG_DSM_VERSION_MAJOR='8'),dict(FIXTURE_FREE='65537'),dict(FIXTURE_FREE='unknown'),dict(SYNOPKG_PKGDEST_VOL='')]:
                result = subprocess.run(['/bin/sh',str(hook)],env=dict(env,**changes),capture_output=True)
                self.assertNotEqual(result.returncode,0,changes)
            production = package.preflight_script((ROOT/'packaging/scripts/preinst').read_bytes(), 1024, 'ds1621xs-production-spk').decode()
            production = production.replace('/etc.defaults/synoinfo.conf', str(config)).replace("'^/volume[1-9][0-9]*$'", "'^"+str(root)+"/volume[1-9][0-9]*$'")
            hook.write_text(production)
            self.assertNotEqual(subprocess.run(['/bin/sh', str(hook)], env=env, capture_output=True).returncode, 0)
            config.write_text('unique="synology_broadwellnk_1621xs+"\n')
            self.assertEqual(subprocess.run(['/bin/sh', str(hook)], env=env, capture_output=True).returncode, 0)
            config.write_text('unique="another_model"\n')
            self.assertNotEqual(subprocess.run(['/bin/sh',str(hook)],env=env,capture_output=True).returncode,0)

    def test_icons_and_budget_are_deterministic(self):
        # Every shipped size keeps the artwork's transparent rounded silhouette.
        self.assertEqual(package.icon(72), package.icon(72))
        for size in package.ICON_SIZES:
            data = package.icon(size)
            self.assertTrue(data.startswith(b'\x89PNG\r\n\x1a\n'))
            self.assertEqual(struct.unpack('!II', data[16:24]), (size, size))
            self.assertEqual(data[24:26], b'\x08\x06')
        with self.assertRaises(ValueError):
            package.icon(63)
        with self.assertRaises(ValueError):
            package.preflight_script(b'no marker', 100, 'vdsm-native-spk-test')

    def test_registered_icons_resolve_and_artwork_updates_change_urls(self):
        # Main and help registrations must resolve all sizes without stale cache URLs.
        config = (ROOT/'packaging/ui/config').read_bytes()
        inputs = [('ui/config', config, 0o644)]
        bound = package.bind_ui_icons(inputs)
        self.assertEqual(bound, package.bind_ui_icons(inputs))
        files = {name: data for name, data, _ in bound}
        registration = json.loads(files['ui/config'])['DiskMap.js']
        templates = {entry['icon'] for entry in registration.values()}
        self.assertEqual(len(templates), 1)
        template = next(iter(templates))
        for size in package.ICON_SIZES:
            self.assertEqual(files['ui/'+template.format(size)], package.icon(size))
        original_icon = package.icon
        # Equal-length content changes need a new URL even with deterministic mtimes.
        with patch.object(package, 'icon', side_effect=lambda size: original_icon(size)[:-1]+b'X'):
            changed = {name: data for name, data, _ in package.bind_ui_icons(inputs)}
        changed_template = next(iter(json.loads(changed['ui/config'])['DiskMap.js'].values()))['icon']
        self.assertNotEqual(template, changed_template)
        with self.assertRaises(ValueError):
            package.bind_ui_icons([])


if __name__ == '__main__':
    unittest.main()
