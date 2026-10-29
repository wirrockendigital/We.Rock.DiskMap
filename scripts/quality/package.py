"""Read-only native SPK content gate; installation acceptance remains a DSM test."""
import argparse
import hashlib
import io
import json
import posixpath
import re
import struct
import tarfile
from pathlib import PurePosixPath, Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from native_targets import target_profile


def members(archive):
    """Reject traversal, duplicate names, special files, and escaping links."""
    result = {}
    for member in archive.getmembers():
        path = PurePosixPath(member.name)
        if path.is_absolute() or '..' in path.parts:
            raise ValueError('Unsafe archive path')
        name = str(path)
        if name == '.':
            continue
        if name in result:
            raise ValueError('Duplicate archive path')
        if not (member.isfile() or member.isdir()):
            raise ValueError('Archive links and special files are forbidden')
        if re.match(r'agent.*\.md$', path.name, re.I):
            raise ValueError('Local governance file packaged')
        result[name] = member
    return result


def check(path, target):
    """Validate the declared platform and local UI references without extracting files."""
    profile = target_profile(target)
    with tarfile.open(path, 'r:*') as outer:
        entries = members(outer)
        for required in ['INFO', 'package.tgz', 'scripts/start-stop-status', 'conf/privilege']:
            if required not in entries:
                raise ValueError(f'Missing SPK entry: {required}')
        info = outer.extractfile(entries['INFO']).read().decode()
        for key, value in [('package', 'we-rock-diskmap'), ('arch', profile['arch']), ('model', profile['model'])]:
            if not re.search(rf'^{key}="{re.escape(value)}"\s*$', info, re.M):
                raise ValueError(f'Unexpected INFO {key}')
        json.loads(outer.extractfile(entries['conf/privilege']).read())
        # Check DSM 7 Package Center assets independently from the packager.
        for name, size in [('PACKAGE_ICON.PNG', 64), ('PACKAGE_ICON_256.PNG', 256)]:
            if name not in entries:
                raise ValueError('Missing Package Center icon')
            check_icon(outer.extractfile(entries[name]).read(), size)
        payload = outer.extractfile(entries['package.tgz']).read()
        with tarfile.open(fileobj=io.BytesIO(payload), mode='r:gz') as inner:
            files = members(inner)
            # Public UI directory modes are part of the artifact's native icon contract.
            if 'ui' not in files or not files['ui'].isdir():
                raise ValueError('Missing explicit public UI directory')
            for name, member in files.items():
                if (name == 'ui' or name.startswith('ui/')) and member.isdir() and member.mode != 0o755:
                    raise ValueError('Public UI directory must be traversable')
            for required in ['bin/diskmapd', 'bin/diskmapctl', 'ui/config']:
                if required not in files:
                    raise ValueError(f'Missing payload entry: {required}')
            for name in ['bin/diskmapd', 'bin/diskmapctl']:
                binary = inner.extractfile(files[name]).read(20)
                if binary[:5] != b'\x7fELF\x02' or binary[5:6] != b'\x01' or binary[18:20] != b'\x3e\x00':
                    raise ValueError('Expected Linux x86-64 ELF')
                if not files[name].mode & 0o111:
                    raise ValueError('Binary is not executable')
            # Resolve both main and help icons exactly as DSM's template does.
            registration = json.loads(inner.extractfile(files['ui/config']).read())
            for applications in registration.values():
                for application in applications.values():
                    template = application['icon']
                    if not re.fullmatch(r'images/DiskMap-[0-9a-f]{16}_\{0\}\.png', template):
                        raise ValueError('Invalid application icon template')
                    for size in (16, 24, 32, 48, 64, 72, 128, 256):
                        resource = 'ui/'+template.format(size)
                        if resource not in files:
                            raise ValueError('Missing application icon resource')
                        check_icon(inner.extractfile(files[resource]).read(), size)
            html_files = [name for name in files if name.endswith('.html')]
            if not html_files:
                raise ValueError('Missing local application HTML')
            for name in html_files:
                html = inner.extractfile(files[name]).read().decode()
                for reference in re.findall(r'(?:src|href)=["\']([^"\']+)', html):
                    if reference.startswith(('http:', 'https:', '//')):
                        raise ValueError('Remote UI resource dependency')
                    if reference.startswith(('#', 'data:')):
                        continue
                    candidate = local_resource_path(name, reference)
                    if candidate not in files:
                        raise ValueError(f'Missing offline resource: {candidate}')
    with open(path, 'rb') as artifact:
        digest = hashlib.file_digest(artifact, 'sha256').hexdigest()
    return {'sha256': digest, 'target': target, 'platform': profile['arch'], 'status': 'content-checked', 'dsmAcceptance': False}


def check_icon(data, size):
    """Require the declared PNG dimensions and retained RGBA transparency."""
    if (len(data) < 33 or data[:16] != b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR'
            or struct.unpack('!II', data[16:24]) != (size, size)
            or data[24:26] != b'\x08\x06'):
        raise ValueError('Invalid DSM icon dimensions or format')


def local_resource_path(document, reference):
    """Resolve legitimate help parent references while rejecting payload escapes."""
    candidate = posixpath.normpath(str(PurePosixPath(document).parent / reference.split('?')[0].split('#')[0]))
    if not candidate.startswith('ui/'):
        raise ValueError('Offline resource escapes UI payload')
    return candidate


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('spk')
    parser.add_argument('--target', required=True)
    args = parser.parse_args()
    print(json.dumps(check(args.spk, args.target)))
