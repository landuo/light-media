#!/usr/bin/env python3
"""PREPARED ONLY. S04 two normal hosts, installed ordinary SDK, exact owned
SQLite statistics readback. No real site, DB writes by driver, forwarding,
CONNECT handler, downloader endpoint, proxy edit or mutation retry.
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
import sqlite3
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
PLUGIN = 'OwnedSiteAccess'
API = '/api/v1/plugin/' + PLUGIN + '/access'
CAPABILITIES = ['site.access.success', 'site.access.fail']
DOMAINS = ('owned-site-access.invalid', 'owned-site-access-second.invalid')
COLUMNS = ('id', 'domain', 'success', 'fail', 'seconds', 'lst_state', 'lst_mod_date', 'note')
SELECT_OWNED = ('SELECT id,domain,success,fail,seconds,lst_state,lst_mod_date,note '
                'FROM source_site_access_statistic WHERE domain IN (?,?) ORDER BY id')
OBS = {'status': 'FAIL', 'kind': 'S04 normal hosts / installed SDK / read-only owned SQLite observations',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [], 'database_observations': [],
       'physical_plugin_wire_counters': {'measured': False, 'available_from_ordinary_API': False},
       'measured_layers': ['worker HostCaller attempt counts and logical JSON bytes',
                           'exact owned table eight columns after completed ordinary calls'],
       'not_claimed': ['source differential rerun', 'real site or network request to a site',
                       'arbitrary table/DAO access', 'SQLite fault injection', 'browser/UI',
                       'full S04/platform/delivery']}

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
    check(INITIAL_ENV.get('S04_ACCESS_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER.is_relative_to(ROOT / 'runtime') and WORKER.is_relative_to(ROOT / 'runtime'),
          'owned coordinator-frozen binaries required')
    check(args.expected_server_sha not in ('b999c2d36100289983bab3e6955e23b327b7b7733f6821227cd43c7e77eb7bca',
                                           '08fb05ab8c1dc8fc29754b2239ad4c9507a18c387fafd2cf0c801353d2f93f03'),
          'old S02/S03 server cannot supply new capabilities')
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
    check(E.parent == ROOT / 'test-instances' and E.name.startswith('s04-access-')
          and not E.exists(), 'exclusive new S04 test-instance required')
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

class Peer(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *unused):
        pass  # Never log headers, credentials or raw bodies.

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

    def parse_request(self):
        if not super().parse_request():
            with lock:
                OBS.setdefault('peer_errors', []).append('invalid artifact request syntax')
            return False
        if self.command != 'GET' or self.path not in ('/index', '/worker'):
            self.reject('only two owned artifact GET paths are allowed')
            return False
        return True

    def do_GET(self):
        try:
            check(self.headers.get('Host') == urllib.parse.urlsplit(peer_origin).netloc,
                  'owned artifact Host mismatch')
            check(not self.headers.get('Transfer-Encoding') and
                  not any(self.headers.get(k) for k in ('Authorization', 'Proxy-Authorization', 'Cookie', 'X-API-Key')),
                  'artifact credentials or transfer encoding forbidden')
            length = self.headers.get('Content-Length', '0')
            check(length.isascii() and length.isdigit() and int(length) == 0, 'artifact GET body forbidden')
            self.connection.settimeout(20)
            raw, kind = ((json.dumps(index).encode(), 'application/json') if self.path == '/index'
                         else (worker_bytes, 'application/octet-stream'))
            row = {'at': now(), 'method': 'GET', 'path': self.path, 'request_body_bytes': 0,
                   'status': 200, 'response_bytes': len(raw)}
            with lock:
                OBS['peer_calls'].append(row)
            self.reply(200, raw, kind)
            with lock:
                row['response_completed'] = True
        except BaseException as error:
            self.reject('artifact GET rejected: ' + type(error).__name__)


def peer_counts():
    with lock:
        check(not OBS.get('peer_errors'), 'unexpected owned artifact request')
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

def read_owned_rows(label):
    # Exact path is statically bound to OpenStore(dir): store.go light-media.db.
    # URI mode=ro sees committed WAL state; do not use immutable (stale WAL).
    database = DATA / 'light-media.db'
    check(database.is_file() and database.resolve() == database,
          'owned Store database path missing or changed')
    connection = sqlite3.connect(database.as_uri() + '?mode=ro', uri=True, timeout=5)
    try:
        def authorize(action, first, second, database_name, source):
            if action == sqlite3.SQLITE_SELECT:
                return sqlite3.SQLITE_OK
            if (action == sqlite3.SQLITE_READ and database_name == 'main'
                    and first == 'source_site_access_statistic' and second in COLUMNS and source is None):
                return sqlite3.SQLITE_OK
            return sqlite3.SQLITE_DENY
        connection.set_authorizer(authorize)
        records = connection.execute(SELECT_OWNED, DOMAINS).fetchall()
    finally:
        connection.close()
    rows = [dict(zip(COLUMNS, record)) for record in records]
    check(len(rows) <= 2 and len({r['domain'] for r in rows}) == len(rows),
          'owned rows duplicated or outside finite observation')
    for row in rows:
        check(row['domain'] in DOMAINS and type(row['id']) is int and row['id'] > 0,
              'owned row identity/domain differs')
        for field in ('success', 'fail', 'seconds', 'lst_state'):
            check(type(row[field]) is int or (field == 'seconds' and row[field] is None),
                  'owned ordinary integer row differs')
        check(row['note'] is None or type(row['note']) is str, 'owned note storage is not text/null')
    OBS['database_observations'].append({'label': label, 'at': now(), 'path': str(database),
                                        'read_only': True, 'table': 'source_site_access_statistic',
                                        'columns': list(COLUMNS), 'domains': list(DOMAINS), 'rows': rows})
    return {row['domain']: row for row in rows}


def note(row):
    return None if row['note'] is None else json.loads(row['note'])


def timestamp_in_window(row, window):
    value = row['lst_mod_date']
    check(type(value) is str, 'owned timestamp must be source-format text')
    parsed = datetime.datetime.strptime(value, '%Y-%m-%d %H:%M:%S')
    check(parsed.strftime('%Y-%m-%d %H:%M:%S') == value and window[0] <= value <= window[1],
          'owned local-naive second timestamp outside actual call window')


def operation(name, domain=DOMAINS[0], seconds=None):
    if name == 'invalid_wire':
        return {'op': name}
    out = {'op': name, 'domain': domain}
    if name == 'success':
        out['seconds'] = seconds
    return out


def callbacks(attempt, failed=False):
    counts = attempt.get('host_calls', {})
    check(counts.get('total') == 1 and counts.get('succeeded') == (0 if failed else 1)
          and counts.get('failed') == (1 if failed else 0)
          and type(counts.get('input_json_bytes')) is int and counts['input_json_bytes'] > 0
          and counts.get('output_json_bytes') == (0 if failed else 4),
          'actual HostCaller attempt/JSON-byte counts differ')


def query(label, operations, worker=None, ok=True, continue_on_error=False, probe=False):
    before = read_owned_rows(label + ':before')
    lower = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    _, result = call('POST', API, {'operations': operations, 'continue_on_error': continue_on_error,
                                  'probe_previous': probe})
    upper = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    bind_worker(result.get('pid'))
    after = read_owned_rows(label + ':after')
    OBS['queries'].append({'label': label, 'host_pid': proc.pid, 'response': result,
                          'local_naive_call_window': [lower, upper], 'before': before, 'after': after})
    save()
    check(result.get('ok') is ok and result.get('completed') == len(operations)
          and result.get('requested') == len(operations) and len(result.get('results', [])) == len(operations),
          'actual SDK aggregate differs: ' + label)
    previous = [q['response'] for q in OBS['queries'][:-1] if q['response'].get('pid') == result['pid']]
    check(result.get('invocation') == (previous[-1]['invocation'] + 1 if previous else 1),
          'invocation sequence differs')
    check(result.get('init_count') == 1, 'owned worker init count differs')
    if worker is None:
        check(not previous and result['invocation'] == 1, 'new worker already has an invocation')
    else:
        check(result['pid'] == worker and bool(previous), 'warm worker replaced on recovery')
    check(result.get('succeeded') == sum(item['op'] != 'invalid_wire' for item in operations)
          and result.get('failed') == sum(item['op'] == 'invalid_wire' for item in operations),
          'SDK per-operation completion counts differ')
    for i, (given, attempt) in enumerate(zip(operations, result['results'])):
        invalid = given['op'] == 'invalid_wire'
        check(attempt.get('index') == i and attempt.get('op') == given['op']
              and attempt.get('domain') == (DOMAINS[0] if invalid else given['domain'])
              and attempt.get('ok') is (not invalid) and attempt.get('source_none') is (not invalid),
              'actual operation/null-result projection differs')
        check(attempt.get('error_code', '') == ('host_operation_failed' if invalid else ''),
              'host error classification differs')
        callbacks(attempt, failed=invalid)
    if probe:
        old = result.get('previous_probe', {})
        check(old.get('label') == 'previous_invocation_fixed_frame' and old.get('available') is True
              and old.get('denied') is True and old.get('error_code') in ('invalid_host_call', 'PLUGIN_CALL_CLOSED'),
              'previous HostCaller denial not observed')
        callbacks(old, failed=True)
    else:
        check(result.get('previous_probe') is None, 'unrequested previous caller probe')
    check(peer_counts() == {'GET /index': 1, 'GET /worker': 1}, 'business call contacted artifact peer')
    return result, before, after, (lower, upper)


def values(row, success, fail, seconds, state):
    check((row['success'], row['fail'], row['seconds'], row['lst_state']) == (success, fail, seconds, state),
          'owned committed counters/seconds/state differ')

preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
worker_bytes = WORKER.read_bytes()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == CAPABILITIES, 'worker two-cap manifest differs')
    check(manifest.get('apis') == [{'key': 'access', 'path': '/access', 'methods': ['POST'],
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
    # Preserve the inherited start() helper verbatim; this synthetic username
    # is just a fixture label in this new exclusive S04 database, not an account reuse.
    bootstrap = {'username': 'owned-s03', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [], 'roots': [], 'downloaders': []}}
    boot = subprocess.run([str(SERVER), '--data-dir', str(DATA), '--bootstrap-stdin'],
                          input=json.dumps(bootstrap).encode(), capture_output=True, env=ENV, timeout=30)
    del bootstrap
    OBS['bootstrap_exit'] = boot.returncode
    check(boot.returncode == 0 and b'bootstrap_complete' in boot.stdout,
          'owned bootstrap failed; credentials/output withheld')
    start()
    check(read_owned_rows('initial_empty') == {}, 'new owned statistics are not empty')
    _, installed = call('POST', '/api/plugins/install', {'plugin_id': PLUGIN, 'repo_url': peer_origin + '/index'})
    check(installed.get('data', {}).get('success') is True, 'normal installation business failure')
    registered()
    check(peer_counts() == {'GET /index': 1, 'GET /worker': 1}, 'unexpected request at install')
    first, before, rows, window = query('none_fail_and_first_nonzero',
        [operation('success'), operation('fail'), operation('success', DOMAINS[1], 7)])
    check(before == {} and set(rows) == set(DOMAINS), 'initial owned row creation differs')
    warm_worker = first['pid']
    a, b = rows[DOMAINS[0]], rows[DOMAINS[1]]
    values(a, 1, 1, 1, 1); values(b, 1, 0, 7, 0)
    check(a['id'] != b['id'] and note(a) == {} and note(b) == {b['lst_mod_date']: 7},
          'actual new IDs or None/nonzero notes differ')
    timestamp_in_window(a, window); timestamp_in_window(b, window)
    original_ids = {domain: rows[domain]['id'] for domain in DOMAINS}

    recovered, before, rows, window = query('invalid_wire_then_same_worker_zero_recovery',
        [operation('invalid_wire'), operation('success', seconds=0)], warm_worker,
        ok=False, continue_on_error=True)
    a = rows[DOMAINS[0]]
    values(a, 2, 1, 1, 0)
    check(note(a) == {a['lst_mod_date']: 1} and rows[DOMAINS[1]] == before[DOMAINS[1]],
          'invalid frame mutated rows or zero recovery differs')
    timestamp_in_window(a, window)
    check({domain: rows[domain]['id'] for domain in DOMAINS} == original_ids, 'warm IDs changed')

    third, before, rows, window = query('old_caller_denied_then_fail_second_domain',
        [operation('fail', DOMAINS[1])], warm_worker, probe=True)
    b = rows[DOMAINS[1]]
    check(rows[DOMAINS[0]] == before[DOMAINS[0]], 'denied old success incremented or changed first row')
    values(b, 1, 1, 7, 1)
    check(b['id'] == original_ids[DOMAINS[1]] and b['note'] == before[DOMAINS[1]]['note'],
          'fail changed old identity/note')
    timestamp_in_window(b, window)
    before_cold = rows
    first_host = proc.pid
    stop()
    check(read_owned_rows('after_first_normal_stop') == before_cold, 'normal stop changed access rows')
    start()
    check(proc.pid != first_host, 'cold host is not a new process')
    registered()
    check(read_owned_rows('cold_before_first_call') == before_cold, 'cold owned records were not preserved')
    check(peer_counts() == {'GET /index': 1, 'GET /worker': 1}, 'cold startup re-fetched artifacts')
    cold, before, rows, window = query('cold_same_DB_new_worker_success_none', [operation('success')])
    check(cold['pid'] != warm_worker, 'cold worker was not fresh')
    a = rows[DOMAINS[0]]
    values(a, 3, 1, 1, 0)
    check(a['id'] == original_ids[DOMAINS[0]] and a['note'] == before[DOMAINS[0]]['note']
          and rows[DOMAINS[1]] == before[DOMAINS[1]], 'cold None changed old notes/identity or other row')
    timestamp_in_window(a, window)
    stop()
    check(read_owned_rows('after_second_normal_stop') == rows, 'final normal stop changed rows')
    counts = peer_counts()
    check(counts == {'GET /index': 1, 'GET /worker': 1}
          and all(row.get('response_completed') for row in OBS['peer_calls']), 'artifact request totals differ')
    check(len(OBS['queries']) == 4 and
          sum(row['method'] == 'POST' and row['path'] == '/api/plugins/install' for row in OBS['calls']) == 1,
          'finite four calls/one installation differ')
    for path in E.glob('host-*.log'):
        check(password.encode() not in path.read_bytes(), 'owned login secret appeared in private host log')
    OBS['actual_artifact_request_counts'] = counts
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    OBS['error'] = str(error).replace(password, '[owned-secret]')

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
