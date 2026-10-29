#!/usr/bin/env python3
"""Assemble deterministic native SPK files for the explicitly recorded target."""
import gzip
import hashlib
import io
import re
import json
from pathlib import Path
import struct
import sys
import tarfile
from source_manifest import manifest
from native_targets import target_profile

ROOT = Path(__file__).resolve().parent.parent
ICON_SIZES = (16, 24, 32, 48, 64, 72, 128, 256)


def uninstall_wizard(package):
    """Return the deterministic DSM-native wizard, without bundling another framework."""
    source = (package/'wizard'/'uninstall.js').read_text()
    ui = [{'custom_render_fn': source, 'custom_render_name': 'diskmap_remove'}]
    return ('WIZARD_UIFILES/uninstall_uifile', json.dumps(ui, ensure_ascii=False, separators=(',', ':')).encode(), 0o644)


def access_wizard(package, operation):
    """Explain the administrator/package-account boundary for both entry paths."""
    source = (package/'wizard'/'install.js').read_text()
    ui = [{'custom_render_fn': source, 'custom_render_name': 'diskmap_access'}]
    return (f'WIZARD_UIFILES/{operation}_uifile', json.dumps(ui, ensure_ascii=False, separators=(',', ':')).encode(), 0o644)


def archive(files):
    """Keep paths, ownership, ordering and timestamps stable across builds."""
    names = [name for name, _, _ in files]
    if len(names) != len(set(names)) or any(name.startswith('/') or '..' in Path(name).parts for name in names):
        raise ValueError('Duplicate or unsafe archive member')
    if any(Path(name).name.lower().startswith('agent') and Path(name).suffix.lower() == '.md' for name in names):
        raise ValueError('Local governance cannot be packaged')
    # Explicit directory modes must not inherit DSM's restrictive installer umask.
    directories = {str(parent) for name in names for parent in Path(name).parents if str(parent) != '.'}
    if directories.intersection(names):
        raise ValueError('Archive file conflicts with a parent directory')
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w', format=tarfile.USTAR_FORMAT) as tar:
        for name in sorted(directories):
            info = tarfile.TarInfo(name)
            info.type, info.mode, info.mtime = tarfile.DIRTYPE, 0o755, 0
            info.uid = info.gid = 0
            tar.addfile(info)
        for name, data, mode in sorted(files):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), mode, 0
            info.uid = info.gid = 0
            tar.addfile(info, io.BytesIO(data))
    return stream.getvalue()


def bind_ui_entrypoints(files):
    """Give mutable boot documents immutable URLs despite DSM's mtime/size ETags."""
    originals = {name: (data, mode) for name, data, mode in files}
    if len(originals) != len(files):
        raise ValueError('Duplicate UI payload member')
    for required in ('ui/index.html', 'ui/DiskMap.js', 'ui/config'):
        if required not in originals:
            raise ValueError('Missing UI bootstrap input: '+required)
    # Hash actual shipped bytes, including generated metadata, rather than only source files.
    entry = 'index-'+hashlib.sha256(originals['ui/index.html'][0]).hexdigest()+'.html'
    script, script_mode = originals.pop('ui/DiskMap.js')
    old_url = b'/webman/3rdparty/we-rock-diskmap/index.html'
    if script.count(old_url) != 1:
        raise ValueError('Missing or ambiguous application entry URL')
    script = script.replace(old_url, b'/webman/3rdparty/we-rock-diskmap/'+entry.encode())
    launcher = 'DiskMap-'+hashlib.sha256(script).hexdigest()+'.js'
    originals['ui/'+launcher] = (script, script_mode)
    originals['ui/'+entry] = originals.pop('ui/index.html')
    config, config_mode = originals['ui/config']
    registration = json.loads(config)
    registration[launcher] = registration.pop('DiskMap.js')
    originals['ui/config'] = (json.dumps(registration, sort_keys=True, indent=2).encode()+b'\n', config_mode)
    return [(name, data, mode) for name, (data, mode) in sorted(originals.items())]


def icon(size):
    """Use the reviewed local artwork without an image dependency during packaging."""
    if size not in ICON_SIZES:
        raise ValueError('Unsupported application icon size')
    data = (ROOT/f'packaging/assets/icons/DiskMap_{size}.png').read_bytes()
    if (data[:16] != b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR'
            or len(data) < 33 or struct.unpack('!II', data[16:24]) != (size, size)
            or data[24:26] != b'\x08\x06'):
        raise ValueError('Expected RGBA application icon at its declared size')
    return data


def bind_ui_icons(files):
    """Bind every registered icon size to one content-addressed artwork family."""
    artwork = {size: icon(size) for size in ICON_SIZES}
    identity = hashlib.sha256(b''.join(artwork.values())).hexdigest()[:16]
    template = f'images/DiskMap-{identity}_{{0}}.png'
    result = []
    found = False
    for name, data, mode in files:
        if name == 'ui/config':
            registration = json.loads(data)
            for applications in registration.values():
                for application in applications.values():
                    if application.get('icon') != 'images/DiskMap_{0}.png':
                        raise ValueError('Unexpected application icon template')
                    application['icon'] = template
            data = json.dumps(registration, sort_keys=True, indent=2).encode()+b'\n'
            found = True
        result.append((name, data, mode))
    if not found:
        raise ValueError('Missing application icon registration')
    result.extend((f'ui/'+template.replace('{0}', str(size)), data, 0o644)
                  for size, data in artwork.items())
    return result


def outputs():
    """Read a closed set of generated binaries and UI assets without following symlinks."""
    result = {}
    for base in ('dist/bin', 'dist/ui'):
        directory = ROOT/base
        if directory.is_symlink() or not directory.is_dir():
            raise SystemExit('Missing or linked build output')
        for path in sorted(directory.rglob('*')):
            if path.is_symlink():
                raise SystemExit('Linked build output')
            if path.is_file():
                if any(part.startswith('.') for part in path.relative_to(directory).parts):
                    raise SystemExit('Hidden build output')
                result[str(path.relative_to(ROOT))] = path.read_bytes()
    if {p for p in result if p.startswith('dist/bin/')} != {'dist/bin/diskmapd', 'dist/bin/diskmapctl'}:
        raise SystemExit('Incomplete or unexpected binaries')
    if 'dist/ui/index.html' not in result or 'dist/ui/api.cgi' not in result:
        raise SystemExit('Incomplete UI outputs')
    for name in ('dist/bin/diskmapd', 'dist/bin/diskmapctl', 'dist/ui/api.cgi'):
        data = result[name]
        if len(data) < 64 or data[:6] != b'\x7fELF\x02\x01' or int.from_bytes(data[18:20], 'little') != 62:
            raise SystemExit('Expected Linux x86-64 ELF: '+name)
        # A native package must not depend on a system ELF interpreter or shared libraries.
        offset = int.from_bytes(data[32:40], 'little')
        entry_size = int.from_bytes(data[54:56], 'little')
        count = int.from_bytes(data[56:58], 'little')
        if not count or entry_size < 56 or offset+count*entry_size > len(data):
            raise SystemExit('Invalid ELF program headers: '+name)
        for index in range(count):
            kind = int.from_bytes(data[offset+index*entry_size:offset+index*entry_size+4], 'little')
            if kind in (2, 3):
                raise SystemExit('Dynamic ELF runtime dependency: '+name)
    return result


def output_record(source, data, target):
    """Record source identity and hashes rather than trusting timestamps or output directories."""
    return {'sourceId': source['sourceId'], 'target': target,
            'files': {name: hashlib.sha256(value).hexdigest() for name, value in sorted(data.items())}}


def preflight_script(data, unpacked_size, target):
    """Allocate room for two expanded copies and 64 MiB of package-local installation overhead."""
    data = data.replace(b'REPLACED_TARGET_MODEL', target_profile(target)['model'].encode())
    marker = b'required_kib=REPLACED_BY_PACKAGER'
    if data.count(marker) != 1:
        raise ValueError('Preflight size marker missing or ambiguous')
    required = (2*unpacked_size+1023)//1024 + 65536
    return data.replace(marker, f'required_kib={required}'.encode())


def main():
    """Select only explicit payload trees; exclude local governance and metadata."""
    args = sys.argv[1:]
    sealing = len(args) == 2 and args[0] == '--seal'
    if not sealing and len(args) != 1:
        raise SystemExit('Explicit recorded target required')
    target = args[-1]
    profile = target_profile(target)
    package = ROOT / 'packaging'
    source = manifest()
    if source != json.loads((ROOT/'dist/source-before.json').read_text()):
        raise SystemExit('Source changed since compilation; rebuild the candidate')
    if source != json.loads((ROOT/'dist/source-after.json').read_text()):
        raise SystemExit('Incomplete compilation source evidence')
    generated = outputs()
    record = output_record(source, generated, target)
    seal = ROOT/'dist/build-output.json'
    if sealing:
        seal.write_text(json.dumps(record, sort_keys=True, indent=2)+'\n')
        return
    if record != json.loads(seal.read_text()):
        raise SystemExit('Generated bytes differ from completed build; rebuild')
    files = [(name.removeprefix('dist/'), data, 0o755 if name.startswith('dist/bin/') or name == 'dist/ui/api.cgi' else 0o644) for name, data in generated.items()]
    for directory, prefix in [(package/'ui', 'ui')]:
        for path in sorted(directory.rglob('*')):
            if path.is_symlink():
                raise SystemExit('Linked package resource')
            if path.is_file():
                if path.name.lower().startswith('agent') and path.suffix.lower() == '.md':
                    raise SystemExit('Local governance cannot be packaged')
                if path.name.startswith('.'):
                    raise SystemExit('Unexpected hidden payload file')
                files.append((f'{prefix}/{path.relative_to(directory)}', path.read_bytes(), 0o755 if prefix == 'bin' else 0o644))
    files.append(('LICENSE', (ROOT/'LICENSE').read_bytes(), 0o644))
    files.append(('build-manifest.json', json.dumps(source, sort_keys=True, indent=2).encode(), 0o644))
    files.append(('build-output.json', json.dumps(record, sort_keys=True, indent=2).encode(), 0o644))
    files = bind_ui_icons(files)
    # Hashed boot paths prevent stale HTML from referencing removed chunk names after upgrades.
    files = bind_ui_entrypoints(files)
    payload = gzip.compress(archive(files), mtime=0)
    info_text = (package/'INFO').read_text().replace('arch="kvmx64"', f'arch="{profile["arch"]}"').replace('model="synology_kvmx64_virtualdsm"', f'model="{profile["model"]}"')
    info = info_text.encode() + f'checksum="{hashlib.md5(payload).hexdigest()}"\n'.encode()
    # DSM 7 uses a 64-pixel Package Center icon; 72 remains a desktop resource.
    outer = [('INFO', info, 0o644), ('package.tgz', payload, 0o644), ('PACKAGE_ICON.PNG', icon(64), 0o644), ('PACKAGE_ICON_256.PNG', icon(256), 0o644)]
    for path in sorted((package/'conf').iterdir()):
        json.loads(path.read_text())
        outer.append((f'conf/{path.name}', path.read_bytes(), 0o644))
    outer.append(uninstall_wizard(package))
    outer.extend(access_wizard(package, operation) for operation in ('install', 'upgrade'))
    for hook in ('preinst', 'postinst', 'preuninst', 'postuninst', 'preupgrade', 'postupgrade', 'start-stop-status'):
        path = package/'scripts'/hook
        data = path.read_bytes() if path.exists() else b'#!/bin/sh\n# No additional resource changes are needed for this lifecycle stage.\nexit 0\n'
        if hook == 'preinst':
            data = preflight_script(data, sum(len(value) for _, value, _ in files), target)
        outer.append((f'scripts/{hook}', data, 0o755))
    metadata = dict(re.findall(r'^([a-z_]+)="([^"\n]*)"$', info_text, re.M))
    if metadata.get('package') != 'we-rock-diskmap' or metadata.get('arch') != profile['arch'] or not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+-[0-9]+', metadata.get('version', '')):
        raise SystemExit('Invalid package identity')
    result = ROOT/f"dist/{metadata['package']}-{metadata['version']}-{metadata['arch']}.spk"
    assembled = archive(outer)
    if manifest() != source:
        raise SystemExit('Source changed during packaging; rebuild')
    result.write_bytes(assembled)
    digest = hashlib.sha256(result.read_bytes()).hexdigest()
    result.with_suffix('.spk.sha256').write_text(f'{digest}  {result.name}\n')
    result.with_suffix('.sources.json').write_text(json.dumps(source, sort_keys=True, indent=2) + '\n')
    print(f'{digest}  {result.name}')


if __name__ == '__main__':
    main()
