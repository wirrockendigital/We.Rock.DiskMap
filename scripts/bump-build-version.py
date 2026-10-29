#!/usr/bin/env python3
"""Allocate the next patch version before each native package build."""
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent


def bump(root):
    """Fail on version drift rather than overwriting an inconsistent release identity."""
    package_path, lock_path, info_path = (root / p for p in ('web/package.json', 'web/package-lock.json', 'packaging/INFO'))
    package, lock = json.loads(package_path.read_text()), json.loads(lock_path.read_text())
    info = info_path.read_text()
    current = package['version']
    match = re.fullmatch(r'(\d+)\.(\d+)\.(\d+)', current)
    package_version = re.search(r'^version="([^"]+)"$', info, re.M)
    if not match or not package_version or not re.fullmatch(re.escape(current) + r'-\d+', package_version[1]) or lock['version'] != current or lock['packages']['']['version'] != current:
        raise ValueError('Build version inputs disagree')
    version = f'{int(match[1])}.{int(match[2])}.{int(match[3]) + 1}'
    package['version'] = lock['version'] = lock['packages']['']['version'] = version
    # Dependencies and all unrelated metadata remain unchanged.
    package_path.write_text(json.dumps(package, indent=2) + '\n')
    lock_path.write_text(json.dumps(lock, indent=2) + '\n')
    info_path.write_text(re.sub(r'^version="[^"]+"$', f'version="{version}-1"', info, flags=re.M))
    return version


if __name__ == '__main__':
    print(bump(ROOT))
