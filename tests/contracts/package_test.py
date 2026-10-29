"""Exercise archive failure boundaries with isolated in-memory malicious archives."""
import importlib.util
import io
import tarfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location('package_gate', Path(__file__).parents[2] / 'scripts/quality/package.py')
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class PackageGateTest(unittest.TestCase):
    """Reject unsafe content before any filesystem extraction or installation."""
    def archive(self, name, kind=tarfile.REGTYPE):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode='w') as archive:
            entry = tarfile.TarInfo(name)
            entry.type = kind
            archive.addfile(entry)
        buffer.seek(0)
        return tarfile.open(fileobj=buffer)

    def test_unsafe_paths_and_local_governance(self):
        for name in ['../escape', '/absolute', 'nested/AGENTS.md', 'agent-local.MD']:
            with self.subTest(name=name), self.archive(name) as archive:
                with self.assertRaises(ValueError):
                    gate.members(archive)

    def test_links_rejected(self):
        with self.archive('link', tarfile.SYMTYPE) as archive:
            with self.assertRaises(ValueError):
                gate.members(archive)

    def test_regular_entry(self):
        with self.archive('ui/index.html') as archive:
            self.assertIn('ui/index.html', gate.members(archive))

    def test_dsm7_icon_dimensions_and_alpha(self):
        # Reject the former 72px Package Center size and invalid image payloads.
        root = Path(__file__).resolve().parents[2]/'packaging/assets/icons'
        gate.check_icon((root/'DiskMap_64.png').read_bytes(), 64)
        gate.check_icon((root/'DiskMap_256.png').read_bytes(), 256)
        for data in [(root/'DiskMap_72.png').read_bytes(), b'not a PNG']:
            with self.assertRaises(ValueError):
                gate.check_icon(data, 64)

    def test_help_parent_resources_resolve_inside_ui_only(self):
        # The bilingual help uses one shared stylesheet in its parent directory.
        self.assertEqual(gate.local_resource_path('ui/help/enu/index.html', '../style.css'), 'ui/help/style.css')
        with self.assertRaises(ValueError):
            gate.local_resource_path('ui/help/enu/index.html', '../../../../escape')
