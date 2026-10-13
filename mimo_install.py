# Selected WSL/SSH installer. Only the official executable is extracted;
# Apple's packaging metadata is validated and ignored, never installed.
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.request

LIMIT = 400 * 1024 * 1024


def extract_binary(archive, root):
    with tarfile.open(archive, 'r:gz') as package:
        binary = None
        metadata = False
        for index, member in enumerate(package):
            if index >= 4:
                raise ValueError('unexpected payload')
            if member.isdir() and member.name in ('.', './'):
                continue
            if member.name in ('._mimo', './._mimo'):
                if metadata or not member.isfile() or not 8 <= member.size <= 1024 * 1024:
                    raise ValueError('invalid metadata')
                with package.extractfile(member) as stream:
                    if stream.read(4) != b'\x00\x05\x16\x07':
                        raise ValueError('invalid AppleDouble metadata')
                metadata = True
                continue
            if member.name not in ('mimo', './mimo') or binary is not None or not member.isfile() or not 0 < member.size <= LIMIT:
                raise ValueError('unexpected payload')
            binary = member
        if binary is None:
            raise ValueError('missing binary')
        target = root / 'mimo'
        with package.extractfile(binary) as source, target.open('xb') as out:
            shutil.copyfileobj(source, out)
        if target.stat().st_size != binary.size:
            raise ValueError('incomplete binary')
        target.chmod(0o700)
        return target


def install(identifier):
    stage = 'prepare'
    try:
        if not re.fullmatch(r'[A-Za-z0-9_-]{1,48}', identifier):
            raise ValueError('invalid id')
        base = 'https://mimocode.cnbj1.mi-fds.com/mimocode/mimocode/releases/'

        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                raise ValueError('redirect denied')

        opener = urllib.request.build_opener(NoRedirect)
        stage = 'version-download'
        with opener.open(base + 'latest', timeout=25) as response:
            version = response.read(100).decode().strip()
            if version.startswith('v'):
                version = version[1:]
        if not re.fullmatch(r'\d+\.\d+\.\d+', version):
            raise ValueError('invalid version')
        machine = platform.machine().lower()
        arch = 'x64-baseline' if machine in ('x86_64', 'amd64') else 'arm64' if machine in ('aarch64', 'arm64') else None
        if not arch:
            raise ValueError('unsupported architecture')
        if platform.libc_ver()[0] == 'musl' or any(pathlib.Path('/lib').glob('ld-musl-*.so.1')):
            arch += '-musl'
        stage = 'directory'
        root = pathlib.Path.home() / '.local/share/duo/engine-tools' / identifier
        for parent in (root, *root.parents):
            if parent.is_symlink() or (parent.exists() and not parent.is_dir()):
                raise ValueError('redirecting directory')
        os.umask(0o077)
        root.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        root.mkdir(mode=0o700)
        archive = root / 'mimo.tar.gz'
        stage = 'binary-download'
        with opener.open(base + 'v' + version + '/mimocode-linux-' + arch + '.tar.gz', timeout=60) as response, archive.open('xb') as target:
            count = 0
            while True:
                part = response.read(1024 * 1024)
                if not part:
                    break
                count += len(part)
                if count > LIMIT:
                    raise ValueError('oversized archive')
                target.write(part)
        stage = 'extract'
        binary = extract_binary(archive, root)
        stage = 'verify'
        got = subprocess.check_output([str(binary), '--version'], timeout=20, stderr=subprocess.DEVNULL).decode().strip()
        if got != version:
            raise ValueError('version mismatch')
        print(json.dumps({'path': str(binary)}))
        return 0
    except Exception as error:
        # Never return raw exception text: proxy settings may include credentials.
        print(json.dumps({'error': stage, 'reason': type(error).__name__}))
        return 1


if __name__ == '__main__':
    sys.exit(install(sys.argv[1]))
