"""Verify patch allocation and drift refusal with isolated release metadata."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('build_version', Path(__file__).resolve().parents[2]/'scripts/bump-build-version.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class BuildVersionTests(unittest.TestCase):
    def test_each_attempt_allocates_patch_and_preserves_dependencies(self):
        """Two native attempts consume separate identities even without an installation receipt."""
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root/'web').mkdir()
            (root/'packaging').mkdir()
            (root/'web/package.json').write_text(json.dumps({'version':'0.4.0','dependencies':{'react':'pinned'}}))
            (root/'web/package-lock.json').write_text(json.dumps({'version':'0.4.0','packages':{'':{'version':'0.4.0'},'node_modules/react':{'version':'pinned'}}}))
            (root/'packaging/INFO').write_text('package="we-rock-diskmap"\nversion="0.4.0-4"\n')
            self.assertEqual(module.bump(root), '0.4.1')
            self.assertEqual(module.bump(root), '0.4.2')
            self.assertEqual(json.loads((root/'web/package.json').read_text())['dependencies'], {'react':'pinned'})
            self.assertIn('version="0.4.2-1"', (root/'packaging/INFO').read_text())
            before = (root/'web/package.json').read_bytes()
            (root/'packaging/INFO').write_text('version="9.9.9-1"\n')
            with self.assertRaises(ValueError):
                module.bump(root)
            self.assertEqual((root/'web/package.json').read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
