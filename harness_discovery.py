# Executed inside the selected WSL/SSH user's login environment. The URL and
# credential arrive on stdin; neither request values nor remote errors are logged.
import base64
import json
import sys
import urllib.error
import urllib.request

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

try:
    request = json.loads(sys.stdin.buffer.read(24000))
    headers = {'Accept': 'application/json'}
    if request['api'] == 'anthropic-messages':
        headers['x-api-key'] = request['api_key']
        headers['anthropic-version'] = '2023-06-01'
    else:
        headers['Authorization'] = 'Bearer ' + request['api_key']
    opener = urllib.request.build_opener(NoRedirect())
    try:
        response = opener.open(urllib.request.Request(request['url'], headers=headers), timeout=12)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        status = response.code
        raw = response.read(4 * 1024 * 1024 + 1) if status == 200 else b''
    if len(raw) > 4 * 1024 * 1024:
        raise ValueError('oversized response')
    print(json.dumps({'status': status, 'body': base64.b64encode(raw).decode('ascii')}))
except Exception:
    sys.stderr.write('Harness model discovery failed.\n')
    sys.exit(1)
