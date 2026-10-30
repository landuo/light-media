"""Actual production HTTP -> full market discovery -> normal product mirror-prefix HTTP + real workers.

Environment paths name a compiled server/workers, private new output directory,
and an independently executed complete-source oracle. No external forwarding.
"""
import copy, hashlib, http.cookiejar, json, os, pathlib, secrets, shutil, signal, socket, subprocess, time, urllib.request, urllib.parse
from owned_http_mirror import OwnedHTTPMirror, assert_clean_environment
assert_clean_environment()

HERE = pathlib.Path(__file__).resolve().parent
OUT = pathlib.Path(os.environ['MARKET_SELECTION_OUTPUT']).resolve(); OUT.mkdir()
SERVER = pathlib.Path(os.environ['MARKET_SELECTION_SERVER']).resolve()
BIN = pathlib.Path(os.environ['MARKET_SELECTION_BIN_DIR']).resolve()
SOURCE = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_RESULT']).resolve()
inputs = json.loads((HERE / 'upstream-inputs.json').read_text())
source = json.loads(SOURCE.read_text())
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
driver_sha256 = sha(pathlib.Path(__file__))
assert source['input_sha256'] == sha(HERE / 'upstream-inputs.json')
expected = {row['name']: row for row in source['observations']}
versions = {'OwnedA1': '2.0', 'OwnedA': '12.0'}
indexes = {}
for pid, version in versions.items():
    worker = BIN / pid
    manifest = json.loads(subprocess.check_output([str(worker), '--manifest']))
    assert manifest['plugin_id'] == pid and manifest['version'] == version
    indexes[pid] = {'schema_version': 1, 'plugin_id': pid, 'current_version': version, 'history': {version: pid + ' release ' + version}, 'releases': [{'version': version, 'manifest': manifest, 'artifacts': [{'platform': 'linux-amd64', 'format': 'executable', 'url': pid, 'sha256': sha(worker), 'executable_sha256': sha(worker), 'size': worker.stat().st_size, 'host_contract': 'light-media.sdk-base/v1'}]}]}
state = {}
def response(host, path):
    assert host == 'raw.githubusercontent.com'
    parts = path.split('?', 1)[0].strip('/').split('/')
    assert len(parts) == 4 and parts[0] == 'Owned' and parts[2] == 'main'
    repo, filename = parts[1], parts[3]
    assert repo in state['packages']
    if filename == 'go-plugins.v1.json':
        native = state['native_indexes'].get(repo)
        if not native:
            return 404, '{}'
        declarations = []
        for pid in native:
            declaration = copy.deepcopy(indexes[pid])
            declaration['releases'][0]['artifacts'][0]['url'] = 'https://raw.githubusercontent.com/Owned/' + repo + '/main/' + pid
            declarations.append(declaration)
        return 200, json.dumps({'schema_version': 1, 'plugins': declarations})
    if filename in versions:
        return 200, (BIN / filename).read_bytes()
    assert filename in ('package.json', 'package.v2.json')
    return 200, json.dumps(state['packages'][repo]['higher' if filename == 'package.v2.json' else 'base'])
proxy = OwnedHTTPMirror(response)
home = OUT / 'home'; home.mkdir()
runtime = OUT / 'runtime'; runtime.mkdir()
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
origin = 'http://127.0.0.1:' + str(port)
username, password, token = 'owned-selector', secrets.token_urlsafe(24), secrets.token_hex(32)
env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'LANG': 'C.UTF-8', 'TZ': 'UTC', 'DEV': 'false', 'LIGHT_MEDIA_SAFE_MODE': 'false', 'MOVIEPILOT_DOCKER': 'false'}
bootstrap = {'username': username, 'password': password, 'settings': {'api_token': token, 'github_proxy': proxy.url, 'plugin_markets': [], 'plugin_statistic_share': False, 'plugin_server_url': ''}}
setup = subprocess.run([str(SERVER), '--data-dir', str(runtime), '--bootstrap-stdin'], input=json.dumps(bootstrap).encode(), env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=30)
assert setup.returncode == 0, 'owned production bootstrap failed'
cookie = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookie))
def call(method, path, body=None):
    request = urllib.request.Request(origin + path, data=None if body is None else json.dumps(body).encode(), method=method, headers={'Content-Type': 'application/json'})
    with opener.open(request, timeout=45) as result:
        return json.load(result)
def project(rows):
    return [{'id': row['instance_id'], 'version': row['version'], 'repo': 'local' if row.get('repo_url', '').startswith('local://') else row.get('repo_url')} for row in rows]
observed = []; local_roots = {}; process = None; log = (OUT / 'server-runtime.log').open('wb')
def start():
    global process
    process = subprocess.Popen([str(SERVER), '--listen', '127.0.0.1:' + str(port), '--data-dir', str(runtime)], env=env, stdout=log, stderr=log)
    for _ in range(300):
        try:
            call('GET', '/api/auth/status'); break
        except Exception:
            assert process.poll() is None, 'owned production server exited'
            time.sleep(.05)
    else:
        raise AssertionError('owned production server not ready')
    call('POST', '/api/auth/login', {'username': username, 'password': password})
    configured = call('GET', '/api/plugins/markets')['data']
    assert configured['plugin_statistic_share'] is False and configured['plugin_server_url'] == ''
def stop():
    if process is not None and process.poll() is None:
        process.send_signal(signal.SIGTERM); process.wait(timeout=25)
def configure(case, local_paths):
    state.clear(); state.update(case)
    answer = call('PUT', '/api/plugins/markets', {'plugin_markets': case['markets'], 'plugin_local_repo_paths': local_paths})
    assert answer['data']['success'] is True
try:
    start()
    for number, case in enumerate(inputs['cases']):
        local_paths = []
        if case['local']:
            local = OUT / ('local-' + str(number)); local.mkdir()
            declarations = []
            for pid in case['local']:
                declarations.append(copy.deepcopy(indexes[pid]))
                shutil.copyfile(BIN / pid, local / pid)
            (local / 'go-plugins.v1.json').write_text(json.dumps({'schema_version': 1, 'plugins': declarations}))
            local_paths.append(str(local)); local_roots[case['name']] = local
        configure(case, local_paths)
        before = len(proxy.requests)
        raw = call('GET', '/api/plugins?state=market&force=true')
        actual = project(raw['data'])
        want = [{k: row[k] for k in ('id', 'version', 'repo')} for row in expected[case['name']]['market_rows']]
        observed.append({'name': case['name'], 'raw_market_response': raw, 'actual': actual, 'expected': want, 'matches': actual == want, 'requests': proxy.requests[before:]})
        assert actual == want, 'whole production market differs: ' + case['name'] + ': ' + json.dumps(actual)
        if case['native_indexes']:
            assert len(raw['data']) == 1 and raw['data'][0]['available'] is False, 'uninstallable higher metadata must not secretly lose to native base'
    # Exact local selection must retain both identities even when the outer
    # market's delimiter-free key shows only one. Install both actual workers.
    local_case = next(row for row in inputs['cases'] if row['local'])
    local = local_roots[local_case['name']]
    configure(local_case, [str(local)])
    installs = []
    for pid, version in versions.items():
        repository = 'local://' + pid + '?' + urllib.parse.urlencode({'path': str(local)})
        installed = call('POST', '/api/plugins/install', {'plugin_id': pid, 'repo_url': repository, 'release_version': version, 'force': True})
        assert installed['data']['success'] is True
        echo = call('GET', '/api/v1/plugin/' + pid + '/echo?apikey=' + token)
        assert echo['version'] == version and echo['init_count'] == 1 and echo['nonce'] != process.pid
        history = call('GET', '/api/plugins/history/' + pid + '?force=true')['data']
        assert history['history'] == source['history_cases']['local'][pid]['history'] and history['latest_version'] == version
        assert history['repo_url'] == repository
        installs.append({'id': pid, 'version': version, 'init_count': echo['init_count'], 'worker_pid': echo['nonce'], 'history': history['history'], 'exact_local_repo': True})
    assert len({row['worker_pid'] for row in installs}) == 2
    configure(inputs['cases'][0], [])
    online_history = []
    for pid in versions:
        history = call('GET', '/api/plugins/history/' + pid + '?force=true')['data']
        want = source['history_cases']['online'][pid]
        assert history['history'] == want['history'], 'online history must use the globally selected declaration, not a suppressed colliding ID'
        online_history.append({'id': pid, 'history': history['history'], 'source_history': want['history']})
    configure(local_case, [str(local)])
    stop(); cookie.clear(); start()
    cold = []
    for pid, version in versions.items():
        echo = call('GET', '/api/v1/plugin/' + pid + '/echo?apikey=' + token)
        history = call('GET', '/api/plugins/history/' + pid + '?force=true')['data']
        assert echo['version'] == version and echo['init_count'] == 1
        assert history['history'] == source['history_cases']['local'][pid]['history']
        cold.append({'id': pid, 'new_worker_process': echo['nonce'] != next(row['worker_pid'] for row in installs if row['id'] == pid), 'history': history['history']})
    assert all(row['new_worker_process'] for row in cold)
    assert not proxy.rejected, 'unrelated network attempts must not be counted as clean fixture acceptance'
    result = {'server_sha256': sha(SERVER), 'worker_sha256': {pid: sha(BIN / pid) for pid in versions}, 'driver_sha256': driver_sha256, 'input_sha256': sha(HERE / 'upstream-inputs.json'), 'source_result_sha256': sha(SOURCE), 'cases': observed, 'actual_local_installs': installs, 'online_history': online_history, 'cold': cold, 'rejected_network_attempts': proxy.rejected, 'isolated_settings': {'plugin_statistic_share': False, 'plugin_server_url': ''}, 'boundary': 'Real production server/HTTP/TLS/discovery/actual standard workers; source full-component oracle separate. Proxy fixture only serves owned bytes and never forwards. Runtime account/DB/private TLS key are not deliverables.'}
    (OUT / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'production_upstream_cases': len(observed), 'actual_workers': len(installs), 'cold_workers': len(cold), 'result_sha256': sha(OUT / 'result.json')}))
finally:
    stop(); log.close(); proxy.close()
