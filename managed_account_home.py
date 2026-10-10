# Only creates a fresh inventory directory in the selected user's home.
# Configuration is passed through stdin, never argv or diagnostic output.
import base64
import json
import os
import pathlib
import re
import sys

def main():
    request = json.loads(sys.stdin.buffer.read(256 * 1024))
    identifier = request['id']
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,48}', identifier):
        raise ValueError('invalid identifier')
    name = {'codex': 'config.toml', 'claude': 'settings.json'}[request['engine']]
    if set(request['files']) != {name}:
        raise ValueError('invalid files')
    content = base64.b64decode(request['files'][name], validate=True)
    if len(content) > 128 * 1024:
        raise ValueError('oversized configuration')
    root = pathlib.Path.home() / '.local' / 'share' / 'duo' / 'engine-accounts' / identifier
    for parent in (root, *root.parents):
        if parent.is_symlink() or (parent.exists() and not parent.is_dir()):
            raise ValueError('redirecting directory')
    os.umask(0o077)
    root.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    root.mkdir(mode=0o700)  # No reuse or overwrite, including after cancellation.
    with (root / name).open('xb') as stream:
        stream.write(content)
    print(json.dumps({'directory': str(root)}))

try:
    main()
except Exception:
    sys.stderr.write('Duo account directory setup failed\n')
    sys.exit(1)
