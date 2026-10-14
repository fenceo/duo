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
    method = request.get('method') or 'GET'
    if method not in ('GET', 'POST'):
        raise ValueError('invalid method')
    body = None
    if method == 'POST':
        headers['Content-Type'] = 'application/json'
        body = json.dumps(request['payload']).encode('utf-8')
    timeout = request.get('timeout_seconds') or 12
    if not isinstance(timeout, int) or not 1 <= timeout <= 25:
        raise ValueError('invalid timeout')
    if request['api'] == 'anthropic-messages':
        if request.get('bearer_auth'):
            headers['Authorization'] = 'Bearer ' + request['api_key']
        else:
            headers['x-api-key'] = request['api_key']
        headers['anthropic-version'] = '2023-06-01'
    else:
        headers['Authorization'] = 'Bearer ' + request['api_key']
    opener = urllib.request.build_opener(NoRedirect())
    try:
        response = opener.open(urllib.request.Request(request['url'], data=body, headers=headers, method=method), timeout=timeout)
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
