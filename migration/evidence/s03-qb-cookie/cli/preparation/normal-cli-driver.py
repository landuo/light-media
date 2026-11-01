#!/usr/bin/env python3
"""PREPARED ONLY: ordinary cmd/server -> installed existing SDK fixture ->
fixed owned qB Cookie protocol. No real daemon/download, upstream forwarding,
CONNECT, proxy/environment edit, source import, hidden build or write retry.
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
BASE = '60206809a9c46b9f11d8fb67590a889e24919df6'
CANDIDATE = ROOT / 'target-drafts/s03-qb-cookie01/candidate07.json'
CANDIDATE_SHA = '891130b2e513f7fd74cf5be2bf8707aebe398f8559750c09e37f973bbc7eebbd'
INITIAL_ENV = os.environ.copy()
ENV = INITIAL_ENV.copy()
peer = thread = proc = log = cookie = None
lock = threading.Lock()
index, owned_workers, process_workers = {}, {}, {}
peer_origin = ''
origin = 'http://127.0.0.1:39123'
PLUGIN = 'OwnedDownloaderCommands'
API = '/api/v1/plugin/' + PLUGIN + '/commands'
OWNED_URL = 'https://owned-cookie.invalid/owned.torrent'  # Payload only; never fetched.
FIRST_COOKIE = 'sid=owned-new; token="owned value"'
QB_DEFAULTS = {'autoTMM': ['False'], 'sequentialDownload': ['False'], 'firstLastPiecePrio': ['False']}
CAPABILITIES = ['downloader.' + kind + '.' + operation
                for kind in ('qbittorrent', 'transmission')
                for operation in ('add', 'start', 'recheck', 'delete_keep_files')]
QB_LOGIN = ('POST', '/api/v2/auth/login')
QB_VERSION = ('GET', '/api/v2/app/webapiVersion')
QB_READ = ('GET', '/api/v2/app/cookies')
QB_SET = ('POST', '/api/v2/app/setCookies')
QB_ADD = ('POST', '/api/v2/torrents/add')
NORMAL_STEPS = [QB_LOGIN, QB_VERSION, QB_VERSION, QB_READ, QB_VERSION, QB_SET, QB_VERSION, QB_ADD]
PHASES = {
    'modern': {'version': '2.11.3', 'sid': 'owned-new', 'steps': NORMAL_STEPS},
    'old': {'version': '2.11.2', 'sid': 'owned-new', 'steps': [QB_LOGIN, QB_VERSION, QB_VERSION, QB_ADD]},
    'selected-failure': {'version': '2.11.3', 'sid': 'owned-failed', 'steps': NORMAL_STEPS[:6]},
    'recovery': {'version': '2.11.3', 'sid': 'owned-recovered', 'steps': NORMAL_STEPS},
    'cold': {'version': '2.11.3', 'sid': 'owned-new', 'steps': NORMAL_STEPS},
}
phase, phase_offset = 'install', 0
cookie_store = [
    {'domain': 'other.invalid', 'path': '/', 'name': 'keep', 'value': 'owned-other', 'expirationDate': 0},
    {'domain': 'owned-cookie.invalid', 'name': 'sid', 'value': 'owned-old'},
]
OBS = {'status': 'FAIL', 'kind': 'S03 URL-cookie ordinary normal hosts / installed SDK / owned qB HTTP',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [], 'store_observations': [],
       'physical_plugin_wire_counters': {'measured': False, 'available_from_ordinary_API': False},
       'measured_layers': ['worker logical SDK callback counters', 'owned endpoint HTTP requests and entity-body bytes'],
       'not_claimed': ['real qB daemon/download/NAS', 'source vendor HTTP runtime oracle',
                       'UI/browser/Meta', 'all Cookie or PEP440 inputs', 'full S03/platform completion'],
       'scenario_basis': 'same five calls as App01; handwritten protocol expectations, source17 separate'}


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
    check(INITIAL_ENV.get('S03_QB_COOKIE_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER == ROOT / 'runtime/s03-qb-cookie-server01'
          and (WORKER.is_relative_to(ROOT / 'runtime') or WORKER.is_relative_to(ROOT / 'evidence')),
          'exact new server and coordinator-owned existing worker required')
    check(sha(WORKER) == args.expected_worker_sha and sha(SERVER) == args.expected_server_sha
          and sha(__file__) == args.expected_driver_sha and sha(CANDIDATE) == CANDIDATE_SHA,
          'program/driver/candidate SHA mismatch')
    check(BINDINGS.is_file() and sha(BINDINGS) == args.expected_bindings_sha,
          'coordinator actual execution bindings required')
    binding = json.loads(BINDINGS.read_text())
    check(binding.get('status') == 'COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION'
          and binding.get('server_sha256') == args.expected_server_sha
          and binding.get('worker_sha256') == args.expected_worker_sha
          and binding.get('worker_path') == str(WORKER)
          and binding.get('driver_sha256') == args.expected_driver_sha
          and binding.get('base') == BASE and binding.get('candidate_manifest_sha256') == CANDIDATE_SHA,
          'actual clean-base/candidate/program binding differs')
    rows = {x['path']: x['sha256'] for x in binding['inputs']}
    check(rows.get(str(SERVER)) == args.expected_server_sha and rows.get(str(WORKER)) == args.expected_worker_sha
          and rows.get(str(CANDIDATE)) == CANDIDATE_SHA, 'actual program/candidate entries absent')
    clean = binding.get('clean_view_proof', {})
    check(type(clean.get('path')) is str and type(clean.get('sha256')) is str
          and rows.get(clean['path']) == clean['sha256'], 'reviewed clean-view proof not bound')
    for item in binding['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    check(E.parent == ROOT / 'test-instances' and E.name.startswith('s03-qb-cookie-')
          and not E.exists(), 'exclusive new cookie test-instance required')
    for name in ('PLUGIN_MARKET', 'LIGHT_MEDIA_MEDIA_ROOTS'):
        check(not ENV.get(name), 'inherited endpoint/mount conflicts; no rewrite')
    for name in ('DEV', 'LIGHT_MEDIA_SAFE_MODE', 'MOVIEPILOT_SAFE_MODE'):
        check(name not in ENV or ENV[name].lower() in ('0', 'false', 'f', 'no', 'n', 'off'),
              'inherited startup mode conflicts; no rewrite')
    E.parent.mkdir(mode=0o700, exist_ok=True)
    E.mkdir(mode=0o700, exist_ok=False)
    OBS['bindings_sha256'] = args.expected_bindings_sha
    OBS['execution_environment'] = 'inherited after coordinator grant marker; no proxy or startup rewrites'
    OBS['clean_view_proof'] = clean
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


ROUTES = {('GET', '/healthz'), ('POST', '/api/auth/login'),
          ('GET', '/api/plugins/markets'), ('POST', '/api/plugins/install'),
          ('GET', API), ('POST', API)}


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
    connection = http.client.HTTPConnection('127.0.0.1', 39123, timeout=30)
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
        connection.close()


def expected_store(sid):
    return [
        {'domain': 'other.invalid', 'path': '/', 'name': 'keep', 'value': 'owned-other', 'expirationDate': 0},
        {'domain': 'owned-cookie.invalid', 'path': '/', 'name': 'sid', 'value': sid},
        {'domain': 'owned-cookie.invalid', 'path': '/', 'name': 'token', 'value': 'owned value'},
    ]


def compact(value):
    return json.dumps(value, ensure_ascii=True, separators=(',', ':'))


def set_phase(name):
    global phase, phase_offset
    with lock:
        if phase in PHASES:
            check(phase_offset == len(PHASES[phase]['steps']), 'previous finite peer phase incomplete')
        check(name in PHASES or name == 'idle', 'invalid private fixture phase')
        phase, phase_offset = name, 0


def store_observation(label, sid):
    with lock:
        check(cookie_store == expected_store(sid), 'owned Cookie store/order differs: ' + label)
        OBS['store_observations'].append({
            'label': label, 'rows': len(cookie_store),
            'ordered_json_sha256': hashlib.sha256(compact(cookie_store).encode()).hexdigest(),
            'field_order': [list(x) for x in cookie_store],
            'exact_synthetic_expected_equal': True,
        })


class Peer(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *unused):
        pass  # Never log raw headers, cookies, credentials or entity bodies.

    def reply(self, status, raw, kind='application/json', session=False):
        self.send_response(status)
        self.send_header('Content-Type', kind)
        self.send_header('Content-Length', str(len(raw)))
        self.send_header('Connection', 'close')
        if session:
            self.send_header('Set-Cookie', 'SID=' + qb_session + '; Path=/; HttpOnly')
        self.end_headers()
        self.wfile.write(raw)
        self.close_connection = True

    def reject(self, reason):
        with lock:
            OBS.setdefault('peer_errors', []).append(reason)
        self.reply(502, b'{}')

    def common(self, maximum, authenticated=False):
        check(self.headers.get('Host') == urllib.parse.urlsplit(peer_origin).netloc, 'owned Host mismatch')
        check(not self.headers.get('Transfer-Encoding'), 'chunked request not admitted')
        check(not self.headers.get('Proxy-Authorization') and not self.headers.get('Authorization')
              and not self.headers.get('X-API-Key'), 'unexpected credential class')
        check(self.headers.get('Cookie') == ('SID=' + qb_session if authenticated else None),
              'fixed synthetic session differs')
        length = self.headers.get('Content-Length', '0')
        check(length.isascii() and length.isdigit() and int(length) <= maximum, 'request entity bound differs')
        self.connection.settimeout(20)  # Only this accepted owned server connection.
        raw = self.rfile.read(int(length))
        check(len(raw) == int(length), 'incomplete owned request body')
        return raw

    def consume(self, method):
        global phase_offset
        with lock:
            check(phase in PHASES, 'unexpected qB request outside an explicit business call')
            cfg = PHASES[phase]
            check(phase_offset < len(cfg['steps']) and cfg['steps'][phase_offset] == (method, self.path),
                  'fixed qB request order differs; no retry or alternate endpoint permitted')
            phase_offset += 1
            return phase, phase_offset, cfg

    def emit(self, row, status, raw, kind='application/json', session=False):
        row.update(at=now(), status=status, response_bytes=len(raw))
        with lock:
            OBS['peer_calls'].append(row)
        self.reply(status, raw, kind, session)
        with lock:
            row['response_completed'] = True

    def do_GET(self):
        try:
            check(self.path in ('/index', '/worker', QB_VERSION[1], QB_READ[1]), 'GET route not whitelisted')
            artifact = self.path in ('/index', '/worker')
            raw = self.common(0, authenticated=not artifact)
            check(raw == b'', 'GET body forbidden')
            row = {'method': 'GET', 'path': self.path, 'request_body_bytes': 0}
            if artifact:
                check(phase == 'install', 'artifact refetch after installation forbidden')
                result, kind = (json.dumps(index).encode(), 'application/json') if self.path == '/index' else (worker_bytes, 'application/octet-stream')
            else:
                name, step, cfg = self.consume('GET')
                row.update(phase=name, step=step)
                if self.path == QB_VERSION[1]:
                    result, kind = cfg['version'].encode(), 'text/plain'
                else:
                    with lock:
                        result = compact(cookie_store).encode()
                    kind = 'application/json'
            self.emit(row, 200, result, kind)
        except BaseException as error:
            self.reject('GET rejected: ' + type(error).__name__)

    def do_POST(self):
        global cookie_store
        try:
            check(self.path in (QB_LOGIN[1], QB_SET[1], QB_ADD[1]), 'POST route not whitelisted')
            raw = self.common(128 << 10, authenticated=self.path != QB_LOGIN[1])
            check(self.headers.get('Content-Type') == 'application/x-www-form-urlencoded', 'fixed qB form encoding differs')
            form = urllib.parse.parse_qs(raw.decode('ascii'), keep_blank_values=True, strict_parsing=True)
            name, step, cfg = self.consume('POST')
            row = {'method': 'POST', 'path': self.path, 'request_body_bytes': len(raw), 'phase': name, 'step': step}
            if self.path == QB_LOGIN[1]:
                check(form == {'username': [qb_user], 'password': [qb_password]}, 'synthetic login fields differ')
                self.emit(row, 200, b'Ok.', 'text/plain', session=True)
            elif self.path == QB_SET[1]:
                wanted = expected_store(cfg['sid'])
                check(form == {'cookies': [compact(wanted)]}, 'exact ordered compact Cookie JSON differs')
                # Deliberate possible selected effect BEFORE the fixed 500.
                # This is this peer's owned memory, never an actual daemon store.
                with lock:
                    cookie_store = json.loads(form['cookies'][0])
                failed = name == 'selected-failure'
                row.update(selected_operation='setCookies500' if failed else 'setCookies',
                           owned_memory_committed=True,
                           ordered_json_sha256=hashlib.sha256(form['cookies'][0].encode()).hexdigest())
                self.emit(row, 500 if failed else 200,
                          b'owned selected response failure' if failed else b'ignored False', 'text/plain')
            else:
                wanted = dict(QB_DEFAULTS, urls=[OWNED_URL])
                if name == 'old':
                    wanted['cookie'] = [FIRST_COOKIE]
                check(form == wanted, 'fixed add fields/per-add Cookie presence differs')
                row.update(selected_operation='add', per_add_cookie_present='cookie' in form)
                self.emit(row, 200, b'{"added_torrent_ids":[9007199254740993]}')
        except BaseException as error:
            self.reject('POST rejected: ' + type(error).__name__)

    def do_CONNECT(self):
        self.reject('CONNECT forbidden')

    def do_PUT(self):
        self.reject('PUT forbidden')

    def do_DELETE(self):
        self.reject('DELETE forbidden')

    def do_HEAD(self):
        self.reject('HEAD forbidden')


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
    call('POST', '/api/auth/login', {'username': 'owned-s03', 'password': password}, authenticated=False)
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


def query(label, operations, expected_delta, worker=None, ok=True, continue_on_error=False, probe=False):
    before = selected_counts()
    _, result = call('POST', API, {'operations': operations, 'continue_on_error': continue_on_error,
                                  'probe_previous': probe})
    bind_worker(result.get('pid'))
    after = selected_counts()
    delta = {k: after.get(k, 0) - before.get(k, 0) for k in set(before) | set(after)
             if after.get(k, 0) != before.get(k, 0)}
    OBS['queries'].append({'label': label, 'host_pid': proc.pid, 'response': result,
                          'selected_before': before, 'selected_after': after, 'selected_delta': delta})
    save()
    check(delta == expected_delta, 'actual owned endpoint selected-request counts differ: ' + label)
    check(result.get('ok') is ok and result.get('completed') == len(operations)
          and result.get('requested') == len(operations) and len(result.get('results', [])) == len(operations),
          'actual SDK aggregate differs: ' + label)
    previous = [row['response'] for row in OBS['queries'][:-1] if row['response'].get('pid') == result['pid']]
    check(result.get('invocation') == (previous[-1]['invocation'] + 1 if previous else 1), 'invocation sequence differs')
    if worker is None:
        check(result.get('had_previous') is False, 'fresh worker already had a business invocation')
    else:
        check(result['pid'] == worker and result.get('had_previous') is True, 'warm worker replaced on recovery')
    check(type(result.get('init_count')) is int and result['init_count'] >= 1, 'worker init receipt missing')
    if previous:
        check(result['init_count'] == previous[-1]['init_count'], 'warm worker unexpectedly reinitialized')
    if probe:
        old = result.get('previous_probe', {})
        check(old.get('label') == 'previous_invocation_host_caller_denied'
              and old.get('available') is True and old.get('denied') is True
              and old.get('callback_attempts') == 1 and old.get('output_json_bytes') == 0
              and old.get('source_invocation') == result['invocation'] - 1
              and old.get('private_frame_operation') == 'invoke'
              and old.get('error_code') in ('invalid_host_call', 'PLUGIN_CALL_CLOSED'),
              'previous HostCaller denial not actually observed')
        # expected_delta above excludes the prior successful add. That
        # independently observes no new selected endpoint request from the probe.
    return result


def callbacks(attempt, *, total=1, begin=0, append=0, submit=0, abort=0, invoke=1, failed=0):
    counts = attempt.get('host_calls', {})
    expected = {'total': total, 'begin': begin, 'append': append, 'submit': submit,
                'abort': abort, 'invoke': invoke, 'other': 0, 'succeeded': total-failed, 'failed': failed}
    check(all(counts.get(k) == value for k, value in expected.items()), 'actual logical SDK callbacks differ')
    check(counts.get('input_json_bytes', 0) > 0 and counts.get('max_input_json_bytes', 0) > 0,
          'logical payload measurements missing')
    return counts


def operation(sid):
    return {'kind': 'qbittorrent', 'op': 'add', 'downloader_id': 'owned-command-qb',
            'args': {'content': OWNED_URL, 'cookie': 'sid=' + sid + '; token="owned value"'}}


def finish_phase(name, expected_delta, before):
    counts = peer_counts()
    delta = {k: counts.get(k, 0) - before.get(k, 0) for k in set(counts) | set(before)
             if counts.get(k, 0) != before.get(k, 0)}
    check(delta == expected_delta, 'actual owned HTTP phase counts differ: ' + name)
    with lock:
        check(phase == name and phase_offset == len(PHASES[name]['steps']), 'finite phase incomplete')
    return counts


def qb_success(attempt):
    check(attempt.get('ok') is True and attempt.get('result') == {'success': True, 'ids': ['9007199254740993']}
          and attempt.get('source_truthy') is True and attempt.get('result_kind') == 'qbittorrent_source_tuple',
          'qB source tuple/precise ID projection differs')


def normal_delta():
    return {'POST /api/v2/auth/login': 1, 'GET /api/v2/app/webapiVersion': 4,
            'GET /api/v2/app/cookies': 1, 'POST /api/v2/app/setCookies': 1, 'POST /api/v2/torrents/add': 1}


preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
qb_user, qb_password, qb_session = 'owned-cookie-user', secrets.token_urlsafe(24), secrets.token_urlsafe(24)
worker_bytes = WORKER.read_bytes()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == CAPABILITIES, 'existing worker eight-cap manifest differs')
    check(manifest.get('apis') == [{'key': 'commands', 'path': '/commands', 'methods': ['POST'],
                                   'auth': 'bear', 'body': 'json', 'body_required': True}], 'worker API differs')
    (E / 'worker-manifest.json').write_bytes(completed.stdout)
    # HTTPServer is explicitly AF_INET by its standard constructor and literal IPv4 bind.
    # No alternate family/route retry on failure.
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
    bootstrap = {'username': 'owned-s03', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [], 'roots': [],
        'downloaders': [{'id': 'owned-command-qb', 'kind': 'qb', 'name': 'Owned synthetic qB Cookie',
                         'base_url': peer_origin, 'enabled': True, 'shared_paths': True,
                         'username': qb_user, 'password': qb_password}]}}
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

    before = peer_counts(); set_phase('modern')
    first = query('modern_ordered_merge_then_add_without_cookie', [operation('owned-new')], {'setCookies': 1, 'add': 1})
    warm_worker = first['pid']
    qb_success(first['results'][0]); callbacks(first['results'][0])
    check(first['succeeded'] == 1 and first['failed'] == 0, 'first completion counters differ')
    finish_phase('modern', normal_delta(), before); store_observation('modern', 'owned-new')

    before = peer_counts(); set_phase('old')
    fallback = query('old_version_per_add_cookie_and_stale_callback_denied', [operation('owned-new')], {'add': 1}, warm_worker, probe=True)
    qb_success(fallback['results'][0]); callbacks(fallback['results'][0])
    probe = fallback['previous_probe']
    check(probe.get('kind') == 'qbittorrent' and probe.get('operation') == 'add', 'probe did not capture prior add')
    finish_phase('old', {'POST /api/v2/auth/login': 1, 'GET /api/v2/app/webapiVersion': 2,
                         'POST /api/v2/torrents/add': 1}, before)
    store_observation('old_store_unchanged', 'owned-new')

    before = peer_counts(); set_phase('selected-failure')
    failed = query('selected_set500_no_add_no_replay', [operation('owned-failed')], {'setCookies500': 1}, warm_worker, ok=False)
    attempt = failed['results'][0]
    check(attempt.get('ok') is False and attempt.get('error_code') == 'host_operation_failed'
          and 'result' not in attempt and 'source_truthy' not in attempt
          and failed['succeeded'] == 0 and failed['failed'] == 1,
          'selected failure became source False/success')
    callbacks(attempt, failed=1)
    finish_phase('selected-failure', {'POST /api/v2/auth/login': 1, 'GET /api/v2/app/webapiVersion': 3,
                                    'GET /api/v2/app/cookies': 1, 'POST /api/v2/app/setCookies': 1}, before)
    store_observation('selected_failure_retains_possible_owned_side_effect', 'owned-failed')
    OBS['selected_failure'] = {'owned_memory_changed_before_500': True, 'selected_requests': 1,
                               'add_requests': 0, 'automatic_write_replays': 0,
                               'sdk_error_code': attempt['error_code'], 'no_source_result_returned': True}

    before = peer_counts(); set_phase('recovery')
    recovery = query('explicit_same_worker_recovery_not_a_retry', [operation('owned-recovered')], {'setCookies': 1, 'add': 1}, warm_worker)
    qb_success(recovery['results'][0]); callbacks(recovery['results'][0])
    finish_phase('recovery', normal_delta(), before); store_observation('recovery', 'owned-recovered')
    before_cold = peer_counts(); first_host = proc.pid
    set_phase('idle'); stop(); start()
    check(proc.pid != first_host, 'cold host is not a new process')
    registered()
    check(peer_counts() == before_cold, 'cold startup refetched artifact or replayed native work')
    store_observation('cold_init_unchanged', 'owned-recovered')

    before = peer_counts(); set_phase('cold')
    cold = query('cold_same_DB_new_worker_explicit_add', [operation('owned-new')], {'setCookies': 1, 'add': 1})
    check(cold['pid'] != warm_worker and cold['init_count'] == first['init_count']
          and cold['succeeded'] == 1 and cold['failed'] == 0, 'cold worker lifecycle differs')
    qb_success(cold['results'][0]); callbacks(cold['results'][0])
    finish_phase('cold', normal_delta(), before); store_observation('cold_explicit_add', 'owned-new')
    set_phase('idle'); stop()
    counts = peer_counts()
    expected = {'GET /index': 1, 'GET /worker': 1, 'POST /api/v2/auth/login': 5,
                'GET /api/v2/app/webapiVersion': 17, 'GET /api/v2/app/cookies': 4,
                'POST /api/v2/app/setCookies': 4, 'POST /api/v2/torrents/add': 4}
    check(counts == expected and all(row.get('response_completed') for row in OBS['peer_calls']),
          'finite HTTP totals/incomplete responses differ')
    check(len(OBS['queries']) == 5 and sum(row['method'] == 'POST' and row['path'] == '/api/plugins/install'
          for row in OBS['calls']) == 1, 'five business calls / one install differ')
    for path in E.glob('host-*.log'):
        data = path.read_bytes()
        check(not any(secret.encode() in data for secret in (password, qb_password, qb_session)),
              'synthetic credential appeared in host log; private evidence requires review')
    OBS['actual_endpoint_request_counts'] = counts
    OBS['current_sdk_invokes'] = sum(x['results'][0]['host_calls']['invoke'] for x in [first, fallback, failed, recovery, cold])
    OBS['old_callback_attempts'] = probe['callback_attempts']
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    text = str(error)
    for secret in (password, qb_password, qb_session):
        text = text.replace(secret, '[owned-secret]')
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
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
    if peer is not None:
        if thread is not None and thread.is_alive():
            peer.shutdown()
        peer.server_close()
    if thread is not None:
        thread.join(timeout=3)
    OBS['input_drift'] = [label for label, path, digest in (
        ('server', SERVER, args.expected_server_sha), ('worker', WORKER, args.expected_worker_sha),
        ('driver', Path(__file__), args.expected_driver_sha), ('bindings', BINDINGS, args.expected_bindings_sha))
        if not path.is_file() or sha(path) != digest]
    for item in json.loads(BINDINGS.read_text())['inputs']:
        if not Path(item['path']).is_file() or sha(item['path']) != item['sha256']:
            OBS['input_drift'].append(item['path'])
    OBS['environment_unchanged'] = os.environ.copy() == INITIAL_ENV and ENV == INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):
        OBS['status'] = 'FAIL'
    try:
        reserve()
    except BaseException as error:
        OBS['status'] = 'FAIL'
        OBS['terminal_reserve_error'] = type(error).__name__
    OBS['finished_at'] = now()
    save()
    (E / 'driver.exit').write_text('0\n' if OBS['status'] == 'PASS' else '1\n')
    print(json.dumps({'status': OBS['status'], 'hosts': [x['pid'] for x in OBS['processes']],
                      'queries': len(OBS['queries']), 'error': OBS.get('error'), 'evidence': str(E)}))
raise SystemExit(0 if OBS['status'] == 'PASS' else 1)
