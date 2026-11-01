#!/usr/bin/env python3
"""PREPARED ONLY. S11 ordinary active SDK through two normal hosts, real installed
concurrent worker self-provider dispatch; only owned artifact GET endpoints.
No native provider operation or provider credentials, forwarding, CONNECT, environment
rewrite, Go/source execution or mutation retry is authorized by this driver.
"""
import argparse
import datetime
import hashlib
import http.client
import http.cookies
import http.server
import json
import os
import secrets
import shutil
import signal
import subprocess
import threading
import time
import urllib.parse
from pathlib import Path

P = argparse.ArgumentParser(description=__doc__)
for name in ('server', 'worker', 'output', 'expected-server-sha',
             'expected-worker-sha', 'expected-driver-sha', 'expected-bindings-sha'):
    P.add_argument('--' + name, required=True)
args = P.parse_args()
AUDIT = Path(__file__).resolve().parent
ROOT = AUDIT.parents[1]
BINDINGS = AUDIT / 'execution-bindings.json'
SERVER, WORKER, E = (Path(p).resolve() for p in (args.server, args.worker, args.output))
INITIAL_ENV = os.environ.copy()
ENV = INITIAL_ENV.copy()
peer = thread = proc = log = cookie = None
lock = threading.Lock()
index, owned_workers, process_workers = {}, {}, {}
peer_origin = ''
origin = 'http://127.0.0.1:39123'
PLUGIN = 'OwnedMediaRecognition'
API = '/api/v1/plugin/' + PLUGIN + '/media-recognize'
CAPABILITIES = ['media.recognize']
ROUTES = {('GET', '/healthz'), ('POST', '/api/auth/login'), ('GET', '/api/plugins/markets'), ('POST', '/api/plugins/install'), ('GET', API), ('POST', API)}
OBS = {'status': 'FAIL', 'kind': 'S11 normal host / installed SDK / plugin-first self-provider only',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [],
       'physical_plugin_wire_counters': {'measured': False, 'available_from_ordinary_API': False},
       'measured_layers': ['worker logical SDK callback counters', 'self-module observations', 'owned artifact HTTP requests'],
       'not_claimed': ['native TMDB/provider/HTTP/cache freshness', 'real site/media/downloader/NAS',
                       'arbitrary MetaObject state', 'in-flight cancellation', 'browser/UI', 'full S11/platform/delivery']}

# Synthetic owned fixture fields, not an original source oracle.
OWNED_MODEL = json.loads('{"recognize_cache_hit":true,"source":"themoviedb","media_id":"900043","scrape_source":null,"type":"电影","title":"Owned active recognition","en_title":null,"hk_title":null,"tw_title":null,"sg_title":null,"year":"2026","season":null,"tmdb_id":900043,"imdb_id":null,"tvdb_id":null,"douban_id":null,"bangumi_id":null,"anilist_id":null,"anidb_id":null,"collection_id":null,"original_language":null,"original_title":"Owned original title","release_date":null,"backdrop_path":null,"poster_path":null,"logo_path":null,"vote_average":null,"overview":"Owned plugin model; not a native provider result","genre_ids":[],"names":["Owned active recognition","Owned original title"],"seasons":{},"season_info":[],"season_years":{},"category":"owned","tmdb_info":{"id":900043,"title":"Owned embedded provider-shaped payload","owned_precise_integer":900719925474099312345},"douban_info":{},"bangumi_info":{},"anilist_info":{},"directors":[],"actors":[],"adult":false,"created_by":[],"episode_run_time":[],"genres":[],"first_air_date":null,"homepage":null,"languages":[],"last_air_date":null,"networks":[],"number_of_episodes":null,"number_of_seasons":null,"origin_country":[],"original_name":null,"production_companies":[],"production_countries":[],"spoken_languages":[],"release_dates":[],"status":null,"tagline":null,"vote_count":null,"popularity":null,"runtime":null,"next_episode_to_air":{},"content_rating":null,"episode_groups":[],"episode_group":null}')

def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def check(ok, message):
    if not ok:
        raise AssertionError(message)

def save():
    with lock:
        raw = json.dumps(OBS, ensure_ascii=True, indent=2) + '\n'
    (E / 'result.json').write_text(raw)

def reserve():
    row = {'at': now(), 'scratch_free': shutil.disk_usage(E).free,
           'tmp_free': shutil.disk_usage('/tmp').free}
    with (E / 'resources.jsonl').open('a') as output:
        output.write(json.dumps(row) + '\n')
    check(row['scratch_free'] >= 1 << 30 and row['tmp_free'] >= 3 * (1 << 29),
          'actual free-space reserve reached')

def identity(pid):
    try:
        raw = Path('/proc/' + str(pid) + '/stat').read_text()
    except FileNotFoundError:
        return None
    fields = raw[raw.rfind(')') + 2:].split()
    return {'ppid': int(fields[1]), 'starttime': fields[19]}

def held_host():
    check(proc is not None and proc.poll() is None, 'held owned host stopped')
    row = OBS['processes'][-1]
    check(identity(proc.pid) == row['identity'], 'held host PID/starttime identity changed')
    check(Path(os.readlink('/proc/' + str(proc.pid) + '/exe')).resolve() == SERVER,
          'held host executable changed')

def exact_owned_listener():
    # Existing accepted exact39123 seam: only known child's descriptor inodes
    # and its IPv4 /proc table, never socket/IPv6/netlink or unrelated PID probes.
    held_host()
    sockets = set()
    for fd in (Path('/proc') / str(proc.pid) / 'fd').iterdir():
        try:
            target = os.readlink(fd)
        except FileNotFoundError:
            continue
        if target.startswith('socket:[') and target.endswith(']'):
            sockets.add(target[8:-1])
    for line in (Path('/proc') / str(proc.pid) / 'net/tcp').read_text().splitlines()[1:]:
        fields = line.split()
        if fields[1] == '0100007F:%04X' % 39123 and fields[3] == '0A' and fields[9] in sockets:
            return fields[9]
    return None

def call(method, path, body=None, authenticated=True, expected=200):
    global cookie
    reserve()
    check((method, path) in ROUTES, 'outside six exact owned method/path pairs')
    target = urllib.parse.urlsplit(origin + path)
    check(target.scheme == 'http' and target.netloc == '127.0.0.1:39123'
          and target.hostname == '127.0.0.1' and target.port == 39123
          and target.path == path.split('?', 1)[0]
          and not target.query
          and not target.fragment
          and target.username is None and target.password is None,
          'exact owned controller origin required')
    check(exact_owned_listener() is not None, 'exact listener not held by owned host; no request')
    headers = {'Content-Type': 'application/json'}
    if authenticated and path not in ('/healthz', '/api/auth/login'):
        check(cookie is not None, 'owned login cookie missing')
        headers['Cookie'] = cookie
    row = {'at': now(), 'host_pid': proc.pid, 'method': method, 'path': path,
           'authenticated': authenticated}
    OBS['calls'].append(row)
    timeout = 30
    row.update(timeout_seconds=timeout, started_monotonic=time.monotonic())
    connection = http.client.HTTPConnection('127.0.0.1', 39123, timeout=timeout)
    try:
        connection.request(method, path,
                           body=None if body is None else json.dumps(body).encode(), headers=headers)
        response = connection.getresponse()
        raw = response.read(2 * 1024 * 1024 + 1)
        row['status'] = response.status
        check(len(raw) <= 2 * 1024 * 1024, 'owned API response exceeds finite bound')
        if path == '/api/auth/login':
            row['private_headers_body_not_retained'] = True
            if response.status == 200:
                jar = http.cookies.SimpleCookie()
                for value in response.headers.get_all('Set-Cookie', []):
                    jar.load(value)
                check(jar, 'owned session cookie missing')
                cookie = '; '.join(v.key + '=' + v.value for v in jar.values())
        else:
            name = 'response-%03d.body' % len(OBS['calls'])
            (E / name).write_bytes(raw)
            row.update(raw_file=name, sha256=hashlib.sha256(raw).hexdigest())
        check(response.status not in (301, 302, 303, 307, 308), 'redirect refused; no alternate request')
        if expected is not None:
            check(response.status == expected, 'unexpected owned API HTTP ' + str(response.status) + ' ' + path)
        return response.status, json.loads(raw)
    except BaseException as error:
        row['transport_or_validation_error_class'] = type(error).__name__
        raise
    finally:
        row['ended_monotonic'] = time.monotonic()
        connection.close()

def peer_counts():
    with lock:
        check(not OBS.get('peer_errors'), 'unexpected owned artifact/downloader request')
        result = {}
        for row in OBS['peer_calls']:
            key = row['method'] + ' ' + row['path']
            result[key] = result.get(key, 0) + 1
        return result

def bind_worker(pid):
    check(type(pid) is int and pid > 0 and pid not in (os.getpid(), proc.pid), 'invalid returned worker PID')
    before = identity(pid)
    check(before is not None and before['ppid'] == proc.pid, 'worker is not direct child of held host')
    link = Path('/proc/' + str(pid) + '/exe')
    path = Path(os.readlink(link)).resolve()
    check(path.is_relative_to(DATA) and sha(link) == args.expected_worker_sha,
          'worker not owned installed executable')
    check(identity(pid) == before, 'worker identity changed during binding')
    row = dict(before, pid=pid, executable=str(path), executable_sha256=args.expected_worker_sha,
               host_pid=proc.pid)
    owned_workers[(pid, before['starttime'])] = row
    process_workers[(pid, before['starttime'])] = row
    OBS.setdefault('worker_identities', []).append(row)

def wait_workers(rows):
    deadline = time.monotonic() + 5
    for row in rows.values():
        while True:
            current = identity(row['pid'])
            if current is None or current['starttime'] != row['starttime']:
                break
            check(time.monotonic() < deadline, 'owned worker remained after graceful host stop')
            time.sleep(.02)

def stop():
    global proc, log
    if proc is None:
        return
    current, row = proc, OBS['processes'][-1]
    if current.poll() is None:
        held_host()
        current.send_signal(signal.SIGTERM)
    try:
        code = current.wait(timeout=30)
    except subprocess.TimeoutExpired:
        row['forced_stop'] = True
        os.killpg(current.pid, signal.SIGKILL)
        code = current.wait(timeout=5)
    row['exit'], proc = code, None
    if log is not None:
        log.close()
        log = None
    check(code == 0 and not row.get('forced_stop'), 'normal host did not stop naturally')
    wait_workers(process_workers)

def registered():
    deadline = time.monotonic() + 20
    while True:
        status, _ = call('GET', API, expected=None)
        if status == 405:
            return
        check(status == 404 and time.monotonic() < deadline,
              'commands API registration failed; no rejected/transport retry')
        time.sleep(.05)

def callbacks(attempt, failed=0):
    counts = attempt.get('host_calls', {})
    check(counts.get('total') == 1 and counts.get('succeeded') == 1 - failed
          and counts.get('failed') == failed, 'actual logical SDK callbacks differ')
    check(counts.get('input_json_bytes', 0) > 0, 'logical input measurement missing')
    check(counts.get('output_json_bytes', -1) == 0 if failed else counts.get('output_json_bytes', 0) > 0,
          'logical output measurement differs')
    return counts

def start():
    global proc, log, cookie, process_workers
    reserve()
    cookie, process_workers = None, {}
    check(len(OBS['processes']) < 2, 'only two sequential normal hosts')
    logfile = E / ('host-%d.log' % (len(OBS['processes']) + 1))
    log = logfile.open('xb')
    proc = subprocess.Popen([str(SERVER), '--listen', '127.0.0.1:39123', '--data-dir', str(DATA)],
                            env=ENV, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    row = {'pid': proc.pid, 'identity': identity(proc.pid), 'normal_not_safe': True,
           'same_private_data': str(DATA)}
    OBS['processes'].append(row)
    check(row['identity'] is not None and row['identity']['ppid'] == os.getpid(), 'host child binding differs')
    held_host()
    check(sha('/proc/' + str(proc.pid) + '/exe') == args.expected_server_sha, 'host executable SHA differs')
    row['executable_sha256'] = args.expected_server_sha
    save()
    deadline = time.monotonic() + 30
    while True:
        check(proc.poll() is None and time.monotonic() < deadline, 'held host startup failed/timed out')
        if b'light-media listening at 127.0.0.1:39123' in logfile.read_bytes():
            inode = exact_owned_listener()
            if inode is not None:
                row['owned_listener_inode'] = inode
                break
        time.sleep(.05)  # Metadata/log readiness only; never a network retry.
    _, health = call('GET', '/healthz', authenticated=False)
    check(health.get('data', {}).get('status') == 'ok', 'owned DB health failed')
    call('POST', '/api/auth/login', {'username': 'owned-s11-id', 'password': password}, authenticated=False)
    _, settings = call('GET', '/api/plugins/markets')
    check(settings['data']['plugin_statistic_share'] is False
          and settings['data']['plugin_server_url'] == '', 'owned settings isolation differs')

class Peer(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *unused):
            pass

    def reply(self, status, raw, kind='application/json'):
            self.send_response(status)
            self.send_header('Content-Type', kind)
            self.send_header('Content-Length', str(len(raw)))
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(raw)
            self.close_connection = True

    def reject(self, reason):
            with lock:
                OBS.setdefault('peer_errors', []).append(reason)
            self.reply(502, b'{}')

    def common(self, maximum, credential):
            check(self.headers.get('Host') == urllib.parse.urlsplit(peer_origin).netloc, 'owned Host mismatch')
            check(not self.headers.get('Transfer-Encoding'), 'chunked request not admitted')
            check(not self.headers.get('Proxy-Authorization') and not self.headers.get('Cookie')
                  and not self.headers.get('X-API-Key'), 'unexpected credential class')
            check(self.headers.get('Authorization') == credential, 'synthetic authentication mismatch')
            length = self.headers.get('Content-Length', '0')
            check(length.isascii() and length.isdigit() and int(length) <= maximum, 'request entity bound differs')
            self.connection.settimeout(20)
            raw = self.rfile.read(int(length))
            check(len(raw) == int(length), 'incomplete owned request body')
            return raw

    def emit(self, row, status, raw, kind='application/json'):
            row.update(at=now(), status=status, response_bytes=len(raw), reply_monotonic=time.monotonic())
            with lock:
                OBS['peer_calls'].append(row)
            self.reply(status, raw, kind)
            with lock:
                row.update(response_completed=True, completed_monotonic=time.monotonic())

    def do_CONNECT(self):
            self.reject('CONNECT forbidden')

    def do_PUT(self):
            self.reject('PUT forbidden')

    def do_DELETE(self):
            self.reject('DELETE forbidden')

    def do_HEAD(self):
            self.reject('HEAD forbidden')

    def do_GET(self):
        try:
            check(self.path in ('/index', '/worker'), 'GET route not whitelisted')
            check(self.common(0, None) == b'', 'artifact GET body forbidden')
            raw = json.dumps(index).encode() if self.path == '/index' else worker_bytes
            self.emit({'method': 'GET', 'path': self.path, 'request_body_bytes': 0}, 200, raw,
                      'application/json' if self.path == '/index' else 'application/octet-stream')
        except BaseException as error:
            self.reject('GET rejected: ' + type(error).__name__)

    def do_POST(self):
        self.reject('POST forbidden: artifact-only peer')

    do_OPTIONS = do_POST
    do_PATCH = do_POST
    do_TRACE = do_POST


def preflight():
    check(INITIAL_ENV.get('S11_ID_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER == ROOT / 'runtime/s11-id-server01'
          and WORKER == ROOT / 'runtime/s11-id-fixture01', 'exact new owned S11 binaries required')
    check(sha(WORKER) == args.expected_worker_sha and sha(SERVER) == args.expected_server_sha
          and sha(__file__) == args.expected_driver_sha, 'program/driver SHA mismatch')
    check(BINDINGS.is_file() and sha(BINDINGS) == args.expected_bindings_sha,
          'coordinator actual execution bindings required')
    binding = json.loads(BINDINGS.read_text())
    check(binding.get('status') == 'COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION'
          and binding.get('server_sha256') == args.expected_server_sha
          and binding.get('worker_sha256') == args.expected_worker_sha
          and binding.get('driver_sha256') == args.expected_driver_sha,
          'actual new server/worker/driver binding differs')
    rows = {x['path']: x['sha256'] for x in binding['inputs']}
    check(rows.get(str(SERVER)) == args.expected_server_sha and rows.get(str(WORKER)) == args.expected_worker_sha,
          'actual binary entries absent from binding')
    for item in binding['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    check(E.parent == ROOT / 'test-instances' and E.name.startswith('s11-id-normal')
          and not E.exists(), 'exclusive new S11 test-instance required')
    for name in ('PLUGIN_MARKET', 'LIGHT_MEDIA_MEDIA_ROOTS'):
        check(not ENV.get(name), 'inherited endpoint/mount conflicts; no rewrite')
    for name in ('DEV', 'LIGHT_MEDIA_SAFE_MODE', 'MOVIEPILOT_SAFE_MODE'):
        check(name not in ENV or ENV[name].lower() in ('0', 'false', 'f', 'no', 'n', 'off'),
              'inherited startup mode conflicts; no rewrite')
    E.parent.mkdir(mode=0o700, exist_ok=True)
    E.mkdir(mode=0o700, exist_ok=False)
    OBS['bindings_sha256'] = args.expected_bindings_sha
    OBS['execution_environment'] = 'inherited after coordinator grant marker; no proxy or startup rewrites'
    reserve()


def check_module_delta(attempt, expected):
    before, after = attempt.get('before', {}), attempt.get('after', {})
    check(set(before) == set(after) == {'state', 'declaration', 'recognize'}, 'module counters missing')
    check(all(type(before[k]) is int and type(after[k]) is int
              and after[k] - before[k] == expected for k in before), 'actual self-module counters differ')


def model_return(attempt):
    check(attempt.get('ok') is True and attempt.get('result_kind') == 'full_raw_media_info'
          and type(attempt.get('result')) is dict, 'typed raw MediaInfo return missing')
    fields = attempt['result']
    check(all(k in fields and fields[k] == v for k, v in OWNED_MODEL.items()),
          'owned complete model fields were lost or changed')
    check(set(fields) == set(OWNED_MODEL) | {'detail_link', 'title_year'},
          'unexpected incomplete/extra live model projection fields')
    check(fields['detail_link'] == 'https://www.themoviedb.org/movie/900043'
          and fields['title_year'] == 'Owned active recognition (2026)', 'existing live descriptors differ')
    check(type(fields['tmdb_info']['owned_precise_integer']) is int
          and fields['tmdb_info']['owned_precise_integer'] == 900719925474099312345,
          'complete nested raw integer was rounded')
    callbacks(attempt)
    check_module_delta(attempt, 1)


def none_return(attempt):
    check(attempt.get('ok') is True and attempt.get('result_kind') == 'source_none'
          and 'result' in attempt and attempt['result'] is None, 'false terminal did not return source None')
    callbacks(attempt)
    check_module_delta(attempt, 1)


def query(label, operations, worker=None, ok=True, continue_on_error=False):
    check(label in ('cache_default_false_null', 'false_terminal_none',
                    'errors_then_same_worker_recovery', 'cold_same_DB'), 'unknown owned business label')
    for operation in operations:
        check(operation in ({'op': 'module_hit'}, {'op': 'module_hit', 'cache': False},
                            {'op': 'module_hit', 'cache': None}, {'op': 'module_false'},
                            {'op': 'unsupported_type'}, {'op': 'pre_canceled'}),
              'outside fixed non-native recognition operations')
    check(len(OBS['queries']) < 4, 'only four fixed business calls')
    before = peer_counts()
    _, result = call('POST', API, {'operations': operations, 'continue_on_error': continue_on_error})
    bind_worker(result.get('pid'))
    after = peer_counts()
    OBS['queries'].append({'label': label, 'host_pid': proc.pid, 'response': result,
                           'artifact_counts_before': before, 'artifact_counts_after': after})
    save()
    check(before == after == {'GET /index': 1, 'GET /worker': 1}, 'business callback fetched artifacts or unknown route')
    check(result.get('ok') is ok and result.get('completed') == len(operations)
          and result.get('requested') == len(operations) and len(result.get('results', [])) == len(operations)
          and not result.get('interrupted'), 'typed SDK aggregate differs: ' + label)
    previous = [q['response'] for q in OBS['queries'][:-1] if q['response'].get('pid') == result['pid']]
    check(result.get('invocation') == (previous[-1]['invocation']+1 if previous else 1), 'invocation sequence differs')
    if worker is None:
        check(not previous and result['invocation'] == 1, 'new worker already invoked')
    else:
        check(result['pid'] == worker and bool(previous), 'warm error recovery replaced worker')
    check(result.get('init_count') == 1, 'owned worker initialized more than once')
    check(result.get('succeeded') == sum(a.get('ok') is True for a in result['results'])
          and result.get('failed') == sum(a.get('ok') is False for a in result['results']),
          'typed result counts differ')
    return result


def check_trace(result, expected):
    trace = result.get('module_trace')
    check(type(trace) is list and len(trace) == len(expected), 'module observation count differs')
    for sequence, (row, (identity_value, cache)) in enumerate(zip(trace, expected), 1):
        check(row.get('sequence') == sequence, 'module observation order differs')
        frame = row.get('input', {})
        check(set(frame) == {'args', 'kwargs'} and frame['args'] == [], 'ordinary module frame differs')
        check(frame['kwargs'] == {'meta': None, 'mtype': '电影', 'source': 'themoviedb', 'mediaid': None,
                                 'tmdbid': identity_value, 'doubanid': None, 'bangumiid': None,
                                 'anilistid': None, 'episode_group': None, 'cache': cache},
              'source default/null/false or normalized ID changed')
        check(row.get('returned') == ('owned_complete_model' if identity_value == 900043 else 'source_false_terminal')
              and identity_value in (900043, 900044), 'native/unknown module branch reached')


preflight()
OBS['started_at'] = now()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
api_token = secrets.token_urlsafe(32)
worker_bytes = WORKER.read_bytes()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == CAPABILITIES, 'worker fixed single-cap manifest differs')
    check(manifest.get('apis') == [{'key': 'media_recognize', 'path': '/media-recognize', 'methods': ['POST'],
                                   'auth': 'bear', 'body': 'json', 'body_required': True}], 'worker API differs')
    check(all(m in manifest.get('methods', []) for m in ('get_state', 'get_module', 'recognize_owned')),
          'self-provider methods missing')
    (E / 'worker-manifest.json').write_bytes(completed.stdout)
    peer = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    peer.daemon_threads = True
    peer_origin = 'http://127.0.0.1:' + str(peer.server_address[1])
    OBS['owned_peer_origin'] = peer_origin
    index.update({'schema_version': 1, 'plugin_id': PLUGIN, 'current_version': '1.0',
                  'releases': [{'version': '1.0', 'manifest': manifest, 'artifacts': [{
                      'platform': 'linux-amd64', 'url': peer_origin + '/worker',
                      'sha256': args.expected_worker_sha, 'executable_sha256': args.expected_worker_sha,
                      'size': len(worker_bytes), 'format': 'executable',
                      'host_contract': 'light-media.sdk-resources/v1'}]}]})
    thread = threading.Thread(target=peer.serve_forever, daemon=True)
    thread.start()
    bootstrap = {'username': 'owned-s11-id', 'password': password, 'settings': {
        'api_token': api_token, 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [], 'roots': [], 'log_level': 'WARN',
        'downloaders': [], 'tmdb': {'api_key': '', 'access_token': ''}}}
    boot = subprocess.run([str(SERVER), '--data-dir', str(DATA), '--bootstrap-stdin'],
                          input=json.dumps(bootstrap).encode(), capture_output=True, env=ENV, timeout=30)
    del bootstrap
    OBS['bootstrap_exit'] = boot.returncode
    check(boot.returncode == 0 and b'bootstrap_complete' in boot.stdout,
          'owned bootstrap failed; credentials/output withheld')
    start()
    _, installed = call('POST', '/api/plugins/install', {'plugin_id': PLUGIN, 'repo_url': peer_origin + '/index'})
    check(installed.get('data', {}).get('success') is True, 'normal installation business failure')
    registered()
    check(peer_counts() == {'GET /index': 1, 'GET /worker': 1}, 'unexpected request at install')
    first = query('cache_default_false_null', [{'op': 'module_hit'}, {'op': 'module_hit', 'cache': False},
                                             {'op': 'module_hit', 'cache': None}])
    warm_worker = first['pid']
    for attempt in first['results']: model_return(attempt)
    check_trace(first, [(900043, True), (900043, False), (900043, None)])
    empty = query('false_terminal_none', [{'op': 'module_false'}], warm_worker)
    none_return(empty['results'][0])
    check_trace(empty, [(900043, True), (900043, False), (900043, None), (900044, True)])
    recovered = query('errors_then_same_worker_recovery', [{'op': 'unsupported_type'}, {'op': 'pre_canceled'},
                                                          {'op': 'module_hit'}], warm_worker,
                      ok=False, continue_on_error=True)
    denied, canceled, success = recovered['results']
    check(denied.get('ok') is False and denied.get('error_code') == 'host_operation_failed'
          and 'result' not in denied and not denied.get('result_kind'), 'host boundary error was swallowed')
    callbacks(denied, failed=1); check_module_delta(denied, 0)
    check(canceled.get('ok') is False and canceled.get('error_code') == 'context_canceled'
          and 'result' not in canceled and not canceled.get('result_kind')
          and canceled.get('host_calls') == {'total': 0, 'succeeded': 0, 'failed': 0,
                                             'input_json_bytes': 0, 'output_json_bytes': 0},
          'pre-canceled SDK call reached host or became successful None')
    check_module_delta(canceled, 0); model_return(success)
    check_trace(recovered, [(900043, True), (900043, False), (900043, None), (900044, True), (900043, True)])
    before_cold = peer_counts(); first_host = proc.pid
    stop(); start()
    check(proc.pid != first_host, 'cold host is not new process')
    registered()
    check(peer_counts() == before_cold, 'cold startup re-fetched artifacts')
    cold = query('cold_same_DB', [{'op': 'module_hit'}])
    check(cold['pid'] != warm_worker, 'cold worker not new')
    model_return(cold['results'][0]); check_trace(cold, [(900043, True)])
    stop()
    counts = peer_counts()
    check(counts == {'GET /index': 1, 'GET /worker': 1}
          and all(r.get('response_completed') for r in OBS['peer_calls']), 'fixed artifact request totals differ')
    attempts = [a for q in OBS['queries'] for a in q['response']['results']]
    check(len(OBS['queries']) == 4 and len(attempts) == 8
          and sum(a['host_calls']['total'] for a in attempts) == 7
          and sum(a['host_calls']['succeeded'] for a in attempts) == 6
          and sum(a['host_calls']['failed'] for a in attempts) == 1,
          'actual logical SDK callback totals differ')
    for path in [DATA / 'logs/app.jsonl', *E.glob('host-*.log')]:
        if path.is_file():
            check(not path.is_symlink() and path.stat().st_size < 2*1024*1024, 'owned log outside finite bound')
            raw = path.read_bytes()
            check(not any(secret.encode() in raw for secret in (password, api_token)),
                  'synthetic credential appeared in log; private evidence requires review')
    OBS['actual_artifact_request_counts'] = counts
    OBS['actual_typed_attempts'] = 8
    OBS['actual_host_callbacks'] = {'total': 7, 'succeeded': 6, 'failed': 1, 'pre_canceled_zero_callback': 1}
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    text = str(error)
    for secret in (password, api_token): text = text.replace(secret, '[owned-secret]')
    OBS['error'] = text
finally:
    try:
        stop()
    except BaseException as error:
        OBS['status'] = 'FAIL'
        OBS['cleanup_error'] = type(error).__name__
    for (pid, starttime), row in owned_workers.items():
        current = identity(pid)
        if current is not None and current['starttime'] == starttime:
            OBS['status'] = 'FAIL'
            OBS.setdefault('forced_worker_cleanup', []).append(row)
            try: os.kill(pid, signal.SIGTERM)
            except ProcessLookupError: pass
    if peer is not None:
        if thread is not None and thread.is_alive(): peer.shutdown()
        peer.server_close()
    if thread is not None: thread.join(timeout=3)
    OBS['input_drift'] = [label for label, path, digest in (
        ('server', SERVER, args.expected_server_sha), ('worker', WORKER, args.expected_worker_sha),
        ('driver', Path(__file__), args.expected_driver_sha), ('bindings', BINDINGS, args.expected_bindings_sha))
        if not path.is_file() or sha(path) != digest]
    for item in json.loads(BINDINGS.read_text())['inputs']:
        if not Path(item['path']).is_file() or sha(item['path']) != item['sha256']:
            OBS['input_drift'].append(item['path'])
    OBS['environment_unchanged'] = os.environ.copy() == INITIAL_ENV and ENV == INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'): OBS['status'] = 'FAIL'
    try: reserve()
    except BaseException as error:
        OBS['status'] = 'FAIL'; OBS['terminal_reserve_error'] = type(error).__name__
    OBS['finished_at'] = now()
    save()
    (E / 'driver.exit').write_text('0\n' if OBS['status'] == 'PASS' else '1\n')
    print(json.dumps({'status': OBS['status'], 'hosts': [x['pid'] for x in OBS['processes']],
                     'queries': len(OBS['queries']), 'error': OBS.get('error'), 'evidence': str(E)}))
raise SystemExit(0 if OBS['status'] == 'PASS' else 1)
