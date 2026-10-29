#!/usr/bin/env python3
"""Fingerprint explicit compilation inputs without including local agent governance."""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def manifest():
    """Return a stable source identity for uncommitted, private test candidates."""
    paths = [ROOT/name for name in ('LICENSE', 'go.mod', 'go.sum', 'Makefile', 'web/package.json', 'web/package-lock.json', 'web/tsconfig.json', 'web/vite.config.ts', 'web/index.html', 'scripts/build.sh', 'scripts/native_targets.py', 'scripts/package-spk.py', 'scripts/source_manifest.py', 'scripts/about-metadata.mjs')]
    paths.append(ROOT/'scripts/bump-build-version.py')
    for name in ('cmd', 'internal', 'web/src', 'packaging'):
        paths.extend(p for p in (ROOT/name).rglob('*') if p.is_file())
    entries = {}
    for path in sorted(set(paths)):
        relative = str(path.relative_to(ROOT))
        if path.name.startswith('.') or (path.name.lower().startswith('agent') and path.suffix.lower() == '.md'):
            raise ValueError('Unexpected local file in build input')
        entries[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
    identity = hashlib.sha256(json.dumps(entries, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    return {'sourceId': f'sha256:{identity}', 'files': entries}


if __name__ == '__main__':
    print(json.dumps(manifest(), sort_keys=True, indent=2))
