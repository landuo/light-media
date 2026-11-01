#!/usr/bin/env python3
"""PREPARED ONLY. S13 numeric strings ordinary installed SDK through two normal hosts and
strict owned fixed HTTP protocol fixtures. Never a real daemon/download.
No Go/source imports, forwarding, CONNECT, proxy edits, or mutation retry.
"""
import argparse
import base64
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
ROOT = AUDIT.parents[2]
BINDINGS = AUDIT / 'execution-bindings.json'
SERVER, WORKER, E = (Path(p).resolve() for p in (args.server, args.worker, args.output))
INITIAL_ENV = os.environ.copy()
ENV = INITIAL_ENV.copy()
peer = thread = proc = log = cookie = None
lock = threading.Lock()
index, owned_workers, process_workers = {}, {}, {}
peer_origin = ''
origin = 'http://127.0.0.1:39123'
PLUGIN = 'OwnedTRFinal'
API = '/api/v1/plugin/' + PLUGIN + '/tr-final'
DOWNLOADER = 'owned-tr-final-tr'
CAPABILITIES = ['downloader.transmission.set_tags', 'downloader.transmission.change']
peer_phase = None

SOURCE = ROOT / 'evidence/s13-tr-numeric-source01/stdout.json'
SOURCE_SHA = '1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d'
BASE = '808148abfad0b7fe230c426387a3b7b49eeedb18'
CANDIDATE = ROOT / 'target-drafts/s13-tr-numeric01/native02/preparation-seal.json'
SOURCE_IDS = ('numeric-underscores-all-settings', 'numeric-unicode-nd-and-space',
              'numeric-float-exponent-underscores', 'int-adjacent-underscores')
source_cases = {}
OBS = {'status': 'FAIL', 'kind': 'S13 numeric source strings / normal installed SDK / fixed owned HTTP protocol',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [],
       'physical_plugin_wire_counters': {'measured': False, 'available_from_ordinary_API': False},
       'measured_layers': ['worker logical SDK callback counters', 'owned endpoint HTTP requests and entity-body bytes'],
       'not_claimed': ['real daemon/download/NAS', 'filesystem or data deletion',
                       'wall-clock behavior of a real downloader', 'browser/UI', 'full S13/platform/delivery']}





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


def preflight():
    global source_cases
    check(INITIAL_ENV.get('S13_TR_NUMERIC_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER == ROOT / 'runtime/s13-tr-numeric-server02'
          and WORKER == ROOT / 'runtime/s13-tr-numeric-fixture02',
          'exact owned coordinator-frozen S13 binaries required')
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
    check(binding.get('base') == BASE and binding.get('candidate_sha256') == sha(CANDIDATE),
          'current clean-base/candidate binding changed')
    check(sha(SOURCE) == SOURCE_SHA, 'complete original raw changed')
    source = json.loads(SOURCE.read_text())
    check(source.get('case_count') == 14 and source.get('source_commit') == '8a83efeacd3e26b82be495e17d50146e31c96a60',
          'original oracle scope changed')
    source_cases = {case['id']: case for case in source['cases'] if case['id'] in SOURCE_IDS}
    check(set(source_cases) == set(SOURCE_IDS), 'fixed original cases absent')
    rows = {x['path']: x['sha256'] for x in binding['inputs']}
    check(rows.get(str(SOURCE)) == SOURCE_SHA and rows.get(str(CANDIDATE)) == sha(CANDIDATE), 'source/candidate entries absent')
    check(rows.get(str(SERVER)) == args.expected_server_sha and rows.get(str(WORKER)) == args.expected_worker_sha,
          'actual binary entries absent from binding')
    for item in binding['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    check(E.parent == ROOT / 'test-instances' and E.name == 's13-tr-numeric-normal02'
          and not E.exists(), 'exclusive new S13 test-instance required')
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


ROUTES = {('GET', '/healthz'), ('POST', '/api/auth/login'), ('GET', '/api/plugins/markets'), ('POST', '/api/plugins/install'), ('GET', API), ('POST', API)}


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
    call('POST', '/api/auth/login', {'username': 'owned-s13-tr-final', 'password': password}, authenticated=False)
    _, settings = call('GET', '/api/plugins/markets')
    check(settings['data']['plugin_statistic_share'] is False
          and settings['data']['plugin_server_url'] == '', 'owned settings isolation differs')


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


def selected_counts():
    peer_counts()  # Fail closed on every rejected/unknown peer request.
    with lock:
        result = {}
        for row in OBS['peer_calls']:
            key = row.get('selected_operation')
            if key:
                result[key] = result.get(key, 0) + 1
        return result


def callbacks(attempt, failed=0):
    counts = attempt.get('host_calls', {})
    check(counts.get('total') == 1 and counts.get('succeeded') == 1 - failed
          and counts.get('failed') == failed, 'actual logical SDK callbacks differ')
    check(counts.get('input_json_bytes', 0) > 0, 'logical input measurement missing')
    check(counts.get('output_json_bytes', -1) == 0 if failed else counts.get('output_json_bytes', 0) > 0,
          'logical output measurement differs')
    return counts


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
        try:
            check(self.path == '/transmission/rpc', 'POST route not whitelisted')
            raw = self.common(128 << 10, basic_auth)
            check(self.headers.get('Content-Type') == 'application/json', 'TR JSON encoding differs')
            document = json.loads(raw)
            check(type(document) is dict and set(document) == {'method', 'arguments'}
                  and type(document['arguments']) is dict, 'exact legacy TR envelope required')
            method, arguments = document['method'], document['arguments']
            with lock:
                phase = peer_phase
            case_id = SOURCE_IDS[1] if phase == 'cold' else phase
            check(case_id in SOURCE_IDS[:3], 'RPC forbidden for original preparation-error phase')
            row = {'method': 'POST', 'path': self.path, 'phase': phase,
                   'request_body_bytes': len(raw), 'rpc_method': method, 'arguments': arguments}
            if method == 'session-get':
                check(arguments == {}, 'fixed session arguments differ')
                response = {'result': 'success', 'arguments': {'rpc-version': 15}}
            else:
                check(method == 'torrent-set', 'unselected TR method')
                calls = [x for x in source_cases[case_id]['trace'] if x['event'] == 'recording_client_call']
                check(len(calls) == 1 and calls[0]['kwargs']['ids'] == 'owned-numeric', 'original call absent')
                expected = dict(calls[0]['kwargs'])
                expected['ids'] = ['a' * 40]  # Only fixed owned native selector adaptation.
                check(arguments == expected and set(arguments) == set(expected), 'original numeric wire values differ')
                for key, kind in calls[0]['python_types'].items():
                    if key == 'ids':
                        continue
                    check(type(arguments[key]) is {'bool': bool, 'int': int, 'float': float}[kind],
                          'original numeric JSON token type differs: ' + key)
                row['selected_operation'] = case_id
                row['original_source_case'] = case_id
                response = {'result': 'success', 'arguments': {}}
            self.emit(row, 200, json.dumps(response).encode())
        except BaseException as error:
            self.reject('POST rejected: ' + type(error).__name__)


def change_command(case_id):
    check(case_id in SOURCE_IDS, 'outside fixed original numeric inputs')
    values = source_cases[case_id]['input']['args']
    command = {'kind': 'transmission', 'op': 'change', 'downloader_id': DOWNLOADER,
               'hash_string': 'a' * 40}
    for field in ('upload_limit', 'download_limit', 'ratio_limit', 'seeding_time_limit'):
        command[field] = values.get(field)
    return command


def query(label, operations, expected_delta, phase, worker=None, ok=True, continue_on_error=False):
    global peer_phase
    check(phase in SOURCE_IDS or phase == 'cold',
          'unknown finite phase')
    before = selected_counts()
    with lock:
        peer_phase = phase
        first_peer = len(OBS['peer_calls'])
    _, result = call('POST', API, {'operations': operations, 'continue_on_error': continue_on_error})
    bind_worker(result.get('pid'))
    after = selected_counts()
    delta = {k: after.get(k, 0)-before.get(k, 0) for k in set(before)|set(after)
             if after.get(k, 0) != before.get(k, 0)}
    row = {'label': label, 'host_pid': proc.pid, 'phase': phase, 'response': result,
           'selected_delta': delta, 'peer_entry_start': first_peer, 'peer_entry_end': len(OBS['peer_calls'])}
    OBS['queries'].append(row)
    save()
    check(delta == expected_delta, 'selected no-replay request count differs: ' + label)
    if phase == SOURCE_IDS[3]:
        check(first_peer == len(OBS['peer_calls']), 'preparation/noargs branch issued HTTP')
    else:
        actual_rpc = OBS['peer_calls'][first_peer:]
        check(len(actual_rpc) == 2 and [x.get('rpc_method') for x in actual_rpc] == ['session-get', 'torrent-set'],
              'success must issue exactly one initialization and one mutation; no replay')
    check(result.get('ok') is ok and result.get('completed') == len(operations)
          and result.get('requested') == len(operations) and len(result.get('results', [])) == len(operations),
          'actual SDK aggregate differs: ' + label)
    previous = [q['response'] for q in OBS['queries'][:-1] if q['response'].get('pid') == result['pid']]
    check(result.get('invocation') == (previous[-1]['invocation']+1 if previous else 1), 'invocation sequence differs')
    if worker is None:
        check(not previous and result['invocation'] == 1, 'new worker already invoked')
    else:
        check(result['pid'] == worker and bool(previous), 'warm recovery replaced worker')
    check(type(result.get('init_count')) is int and result['init_count'] >= 1, 'missing init receipt')
    if previous:
        check(result['init_count'] == previous[-1]['init_count'], 'warm worker reinitialized')
    return result


def source_bool(attempt, expected):
    check(attempt.get('ok') is True and type(attempt.get('result')) is bool
          and attempt['result'] is expected and attempt.get('result_kind') == 'source_bool'
          and attempt.get('source_truthy') is expected, 'typed source bool differs')
    callbacks(attempt)


preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
tr_password = secrets.token_urlsafe(24)
tr_username = 'owned-tr-final'
basic_auth = 'Basic ' + base64.b64encode((tr_username + ':' + tr_password).encode()).decode()
worker_bytes = WORKER.read_bytes()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == CAPABILITIES, 'worker two-cap manifest differs')
    check(manifest.get('apis') == [{'key': 'tr_final', 'path': '/tr-final', 'methods': ['POST'],
                                   'auth': 'bear', 'body': 'json', 'body_required': True}], 'worker API differs')
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
    bootstrap = {'username': 'owned-s13-tr-final', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [], 'roots': [], 'log_level': 'WARN',
        'downloaders': [{'id': DOWNLOADER, 'kind': 'tr', 'name': 'Owned synthetic TR final',
                         'base_url': peer_origin, 'enabled': True,
                         'username': tr_username, 'password': tr_password}]}}
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
    first = query('original_numeric_underscores', [change_command(SOURCE_IDS[0])], {SOURCE_IDS[0]: 1}, SOURCE_IDS[0])
    warm_worker = first['pid']; source_bool(first['results'][0], True)
    invalid = query('original_adjacent_underscore_ValueError_zero_HTTP', [change_command(SOURCE_IDS[3])], {}, SOURCE_IDS[3], warm_worker, ok=False)
    attempt = invalid['results'][0]
    check(attempt.get('error_code') == 'source_exception' and attempt.get('source_error') == 'ValueError'
          and attempt.get('ok') is False and 'result' not in attempt, 'outside-try source error was swallowed')
    callbacks(attempt)
    recovered = query('same_worker_unicode_after_source_error', [change_command(SOURCE_IDS[1])], {SOURCE_IDS[1]: 1}, SOURCE_IDS[1], warm_worker)
    source_bool(recovered['results'][0], True)
    exponent = query('original_exponent_float_token', [change_command(SOURCE_IDS[2])], {SOURCE_IDS[2]: 1}, SOURCE_IDS[2], warm_worker)
    source_bool(exponent['results'][0], True)
    before_cold = peer_counts(); first_host = proc.pid
    stop(); start()
    check(proc.pid != first_host, 'cold host is not new process')
    registered()
    check(peer_counts() == before_cold, 'cold startup re-fetched artifacts/contacted downloader')
    cold = query('cold_same_DB_new_worker_unicode', [change_command(SOURCE_IDS[1])], {SOURCE_IDS[1]: 1}, 'cold')
    check(cold['pid'] != warm_worker, 'cold worker not new')
    source_bool(cold['results'][0], True)
    stop()
    counts = peer_counts()
    check(counts == {'GET /index': 1, 'GET /worker': 1, 'POST /transmission/rpc': 8}
          and all(r.get('response_completed') for r in OBS['peer_calls']), 'finite request totals differ')
    attempts = [a for q in OBS['queries'] for a in q['response']['results']]
    check(len(attempts) == 5 and sum(a['host_calls']['total'] for a in attempts) == 5
          and sum(a['host_calls']['failed'] for a in attempts) == 0, 'SDK callback totals differ')
    check(sum(bool(a.get('ok')) for a in attempts) == 4 and len(OBS['queries']) == 5,
          'original four successes/one source error outcomes differ')
    app_log = DATA / 'logs/app.jsonl'
    check(app_log.is_file() and not app_log.is_symlink() and app_log.stat().st_size < 2*1024*1024,
          'owned actual log missing/exceeds bound')
    logs = [json.loads(x) for x in app_log.read_text().splitlines() if x]
    relevant = [r for r in logs if r.get('plugin_id') == PLUGIN and r.get('event') == 'downloader_command']
    check(relevant == [], 'numeric conversion/source ValueError unexpectedly emitted command logs')
    (E / 'owned-plugin-log-projection.json').write_text(json.dumps(relevant, ensure_ascii=False, indent=2)+'\n')
    OBS['log_projection_sha256'] = sha(E / 'owned-plugin-log-projection.json')
    for path in [app_log, *E.glob('host-*.log')]:
        raw = path.read_bytes()
        check(not any(secret.encode() in raw for secret in (password, tr_password, basic_auth)),
              'synthetic credential appeared in log; private evidence requires review')
    OBS['actual_endpoint_request_counts'] = counts
    OBS['source_raw_sha256'] = SOURCE_SHA
    OBS['selected_source_ids'] = list(SOURCE_IDS)
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    text = str(error)
    for secret in (password, tr_password, basic_auth): text = text.replace(secret, '[owned-secret]')
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
