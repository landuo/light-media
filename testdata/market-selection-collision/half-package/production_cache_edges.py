"""Real authenticated market HTTP + normal product mirror-prefix HTTP, source-derived cache edges.

This test does not install workers; worker installation/cold recovery is covered
by the separate production_upstream runner. Private runtime data stay local.
"""
import hashlib, http.cookiejar, json, os, pathlib, secrets, signal, socket, subprocess, sys, time, urllib.request

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))
from owned_http_mirror import OwnedHTTPMirror, assert_clean_environment
assert_clean_environment()
OUT = pathlib.Path(os.environ['MARKET_SELECTION_OUTPUT']).resolve(); OUT.mkdir()
SERVER = pathlib.Path(os.environ['MARKET_SELECTION_SERVER']).resolve()
SOURCE = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_RESULT']).resolve()
INPUT = HERE / 'cache-edge-inputs.json'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
inputs, source = json.loads(INPUT.read_text()), json.loads(SOURCE.read_text())
assert source['input_sha256'] == sha(INPUT)
expected = {s['name']: s for s in source['sequences']}
state = {'bands': {}}
native_index = None
if os.environ.get('MARKET_SELECTION_NATIVE_WORKER'):
    worker = pathlib.Path(os.environ['MARKET_SELECTION_NATIVE_WORKER']).resolve()
    manifest = json.loads(subprocess.check_output([str(worker), '--manifest']))
    assert manifest['plugin_id'] == 'OwnedA1' and manifest['version'] == '2.0'
    native_index = {'schema_version': 1, 'plugin_id': 'OwnedA1', 'current_version': '2.0', 'releases': [{'version': '2.0', 'manifest': manifest, 'artifacts': [{'platform': 'linux-amd64', 'format': 'executable', 'url': 'https://raw.githubusercontent.com/Owned/NativeSwitch/main/OwnedA1', 'sha256': sha(worker), 'executable_sha256': sha(worker), 'size': worker.stat().st_size, 'host_contract': 'light-media.sdk-base/v1'}]}]}
def response(host, path):
    assert host == 'raw.githubusercontent.com'
    parts = path.split('?', 1)[0].strip('/').split('/')
    assert len(parts) == 4 and parts[0] == 'Owned' and parts[2] == 'main'
    filename = parts[3]
    if filename == 'go-plugins.v1.json':
        if state.get('native'):
            assert native_index is not None
            return 200, json.dumps(native_index)
        return 404, '{}'
    assert filename in ('package.json', 'package.v2.json')
    band = 'higher' if filename == 'package.v2.json' else 'base'
    selected = state['bands'][band]
    if 'version' not in selected:
        return selected['status'], selected.get('body') or '{}'
    pid = 'OwnedHigher' if band == 'higher' else 'OwnedBase'
    return selected['status'], json.dumps({pid: {'name': pid, 'version': selected['version'], 'v2': True}})

proxy = OwnedHTTPMirror(response)
home, runtime = OUT / 'home', OUT / 'runtime'; home.mkdir(); runtime.mkdir()
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
origin = 'http://127.0.0.1:' + str(port)
username, password = 'owned-cache-edges', secrets.token_urlsafe(24)
env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'LANG': 'C.UTF-8', 'TZ': 'UTC', 'DEV': 'false', 'LIGHT_MEDIA_SAFE_MODE': 'false', 'MOVIEPILOT_DOCKER': 'false'}
bootstrap = {'username': username, 'password': password, 'settings': {'api_token': secrets.token_hex(32), 'github_proxy': proxy.url, 'plugin_markets': [], 'plugin_statistic_share': False, 'plugin_server_url': ''}}
setup = subprocess.run([str(SERVER), '--data-dir', str(runtime), '--bootstrap-stdin'], input=json.dumps(bootstrap).encode(), env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=30)
assert setup.returncode == 0
cookie = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookie))
def call(method, path, body=None):
    request = urllib.request.Request(origin + path, data=None if body is None else json.dumps(body).encode(), method=method, headers={'Content-Type': 'application/json'})
    with opener.open(request, timeout=45) as response:
        return json.load(response)
def project(rows):
    return [{'id': row['instance_id'], 'version': row['version'], 'repo': row['repo_url']} for row in rows]

process = None
log = (OUT / 'server-runtime.log').open('wb')
observed, native_transitions = [], []
try:
    process = subprocess.Popen([str(SERVER), '--data-dir', str(runtime), '--listen', '127.0.0.1:' + str(port)], env=env, stdout=log, stderr=log)
    for _ in range(400):
        try:
            call('GET', '/api/auth/status'); break
        except Exception:
            assert process.poll() is None, 'owned server exited'
            time.sleep(.025)
    else:
        raise AssertionError('owned server did not become ready')
    call('POST', '/api/auth/login', {'username': username, 'password': password})
    configured = call('GET', '/api/plugins/markets')['data']
    assert configured['plugin_statistic_share'] is False and configured['plugin_server_url'] == ''
    for sequence in inputs['sequences']:
        assert call('PUT', '/api/plugins/markets', {'plugin_markets': [sequence['repository']], 'plugin_local_repo_paths': []})['data']['success'] is True
        for number, phase in enumerate(sequence['phases']):
            state['bands'] = phase['bands']
            before = len(proxy.requests)
            raw = call('GET', '/api/plugins/market?details=true&force=' + str(phase['force']).lower())
            rows = project(raw['data']['items'])
            want = expected[sequence['name']]['phases'][number]
            requests = proxy.requests[before:]
            assert rows == want['rows'], sequence['name'] + ':' + phase['name']
            if not want['requests']:
                assert not requests, 'source ordinary cache hit must issue zero HTTP requests'
            if phase['force'] and phase['name'] != 'initial' and ('500' in sequence['name'] or 'malformed' in sequence['name'] or 'failure' in sequence['name']):
                assert raw['data']['sources'][0]['success'] is False and rows, 'partial error must remain visible without hiding successful cards'
            observed.append({'sequence': sequence['name'], 'phase': phase['name'], 'raw': raw, 'expected': want['rows'], 'requests': requests, 'matches': True})
    if native_index is not None:
        repository = 'https://github.com/Owned/NativeSwitch'
        assert call('PUT', '/api/plugins/markets', {'plugin_markets': [repository], 'plugin_local_repo_paths': []})['data']['success'] is True
        state['bands'] = {'higher': {'status': 200, 'version': '1.0'}, 'base': {'status': 200, 'version': '1.0'}}
        for name, native, force, ids in [('source-route', False, True, ['OwnedHigher', 'OwnedBase']), ('native-added-ordinary-cache', True, False, ['OwnedHigher', 'OwnedBase']), ('force-discovers-native', True, True, ['OwnedA1']), ('native-removed-ordinary-cache', False, False, ['OwnedA1']), ('force-discovers-source-again', False, True, ['OwnedHigher', 'OwnedBase'])]:
            state['native'] = native
            before = len(proxy.requests)
            raw = call('GET', '/api/plugins/market?details=true&force=' + str(force).lower())
            assert [x['instance_id'] for x in raw['data']['items']] == ids, name
            if not force:
                assert len(proxy.requests) == before
            native_transitions.append({'name': name, 'raw': raw, 'requests': proxy.requests[before:]})
    assert not proxy.rejected
    result = {'server_sha256': sha(SERVER), 'driver_sha256': sha(pathlib.Path(__file__)), 'input_sha256': sha(INPUT), 'source_result_sha256': sha(SOURCE), 'observations': observed, 'native_format_route_transitions': native_transitions, 'native_worker_sha256': None if native_index is None else sha(worker), 'rejected': proxy.rejected, 'boundary': __doc__, 'isolated_settings': {'plugin_statistic_share': False, 'plugin_server_url': ''}}
    (OUT / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps({'actual_http_phases': len(observed), 'all_match': True, 'result_sha256': sha(OUT / 'result.json')}))
finally:
    if process is not None and process.poll() is None:
        process.send_signal(signal.SIGTERM); process.wait(timeout=25)
    log.close(); proxy.close()
