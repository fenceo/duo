# Embedded helper used only through selected WSL/SSH transports. Requests and
# responses travel through stdin/stdout; credentials never occur in argv.
import base64
import json
import os
import pathlib
import stat
import sys
import tempfile

LIMIT = 4 * 1024 * 1024

def main():
    request = json.loads(sys.stdin.buffer.read(24 * 1024 * 1024))
    engine = request['engine']
    names = {'codex': ('auth.json', 'config.toml'),
             'claude': ('.credentials.json', 'settings.json')}[engine]
    key = 'CODEX_HOME' if engine == 'codex' else 'CLAUDE_CONFIG_DIR'
    root = pathlib.Path(request.get('directory') or os.environ.get(key) or str(pathlib.Path.home() / ('.' + engine)))
    if not root.is_absolute():
        raise ValueError('absolute directory required')
    for parent in (root, *root.parents):
        if parent.is_symlink() or (parent.exists() and not parent.is_dir()):
            raise ValueError('redirecting directory')

    def read():
        result = {}
        for name in names:
            path = root / name
            try:
                info = path.lstat()
            except FileNotFoundError:
                result[name] = None
                continue
            if not stat.S_ISREG(info.st_mode) or info.st_size > LIMIT:
                raise ValueError('invalid account file')
            with path.open('rb') as stream:
                raw = stream.read(LIMIT + 1)
            if len(raw) > LIMIT:
                raise ValueError('oversized account file')
            result[name] = base64.b64encode(raw).decode('ascii')
        return result

    if request['operation'] == 'read':
        print(json.dumps(read()))
        return
    if request['operation'] != 'write':
        raise ValueError('unsupported operation')
    os.umask(0o077)
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    lock = root / '.duo-account-sync.lock'
    with lock.open('xb'):
        pass
    try:
        old = read()
        if old != request['expected']:
            raise ValueError('concurrent account change')
        data = request['files']
        if set(data) != set(names):
            raise ValueError('invalid file set')
        next_data = {name: None if data[name] is None else base64.b64decode(data[name], validate=True) for name in names}
        if any(raw is not None and len(raw) > LIMIT for raw in next_data.values()):
            raise ValueError('oversized new account file')
        backup = pathlib.Path(tempfile.mkdtemp(prefix='.duo-account-backup-', dir=str(root)))
        for name in names:
            if old[name] is not None:
                (backup / name).write_bytes(base64.b64decode(old[name]))
            if next_data[name] is not None:
                (backup / (name + '.new')).write_bytes(next_data[name])
        changed = []
        try:
            for name in names:
                path = root / name
                if next_data[name] is None:
                    if path.exists():
                        path.unlink()
                else:
                    os.replace(str(backup / (name + '.new')), str(path))
                changed.append(name)
        except Exception:
            for name in changed:
                path = root / name
                if old[name] is None:
                    if path.exists():
                        path.unlink()
                else:
                    path.write_bytes(base64.b64decode(old[name]))
            raise
        print('{}')
    finally:
        lock.unlink()

try:
    main()
except Exception:
    # Never echo filenames, parsed values or authentication material.
    sys.stderr.write('Account file operation failed.\n')
    sys.exit(1)
