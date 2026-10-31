#!/usr/bin/env python3
"""PREPARED ONLY: S02 local backup through two normal hosts and installed SDK.
Exact owned 39123 controller, fixed index/worker peer, no forwarding or CONNECT.
No real backup/downloader/source imports, filesystem escape/permission probes,
Go compilation, proxy rewrite, business transfer, or explicit file deletion.
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
             'expected-worker-sha', 'expected-driver-sha'):
    P.add_argument('--' + name, required=True)
args = P.parse_args()
AUDIT = Path(__file__).resolve().parent
ROOT = AUDIT.parents[1]
SERVER, WORKER, E = (Path(p).resolve() for p in (args.server, args.worker, args.output))
INITIAL_ENV = os.environ.copy()
ENV = INITIAL_ENV.copy()
peer = thread = proc = log = cookie = None
lock = threading.Lock()
index, owned_workers, process_workers = {}, {}, {}
peer_origin = ''
origin = 'http://127.0.0.1:39123'
PLUGIN = 'OwnedLocalTorrentBackup'
API = '/api/v1/plugin/' + PLUGIN + '/backup'
PREPARE = {'total': 1, 'prepare': 1, 'read': 0, 'release': 0}
OBS = {'status': 'FAIL', 'kind': 'S02 local backup two normal CLI hosts/installed SDK',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [], 'backup_checks': [],
       'physical_wire_counters': {'available_from_ordinary_API': False, 'measured': False},
       'not_claimed': ['filesystem permission/inode/symlink/path-escape probes',
                       'real downloader/backup/media/NAS', 'whole TorrentTransfer business',
                       'age-based deletion boundary', 'browser/UI', 'full platform/delivery']}

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
    check(INITIAL_ENV.get('S02_LOCAL_BACKUP_CLI_GRANTED') == '1',
          'coordinator execution window required; prepared driver only')
    check(SERVER.is_relative_to(ROOT / 'runtime')
          and WORKER.is_relative_to(ROOT / 'runtime'), 'owned coordinator-frozen binaries required')
    check(sha(WORKER) == args.expected_worker_sha
          and sha(SERVER) == args.expected_server_sha
          and sha(__file__) == args.expected_driver_sha, 'program/driver SHA mismatch')
    check(E.parent == ROOT / 'test-instances' and E.name.startswith('s02-')
          and not E.exists(), 'exclusive new S02 test-instance required')
    for name in ('PLUGIN_MARKET', 'LIGHT_MEDIA_MEDIA_ROOTS'):
        check(not ENV.get(name), 'inherited endpoint/mount conflicts; no rewrite')
    for name in ('DEV', 'LIGHT_MEDIA_SAFE_MODE', 'MOVIEPILOT_SAFE_MODE'):
        check(name not in ENV or ENV[name].lower() in ('0', 'false', 'f', 'no', 'n', 'off'),
              'inherited startup mode conflicts; no rewrite')
    bindings = json.loads((AUDIT / 'input-bindings.json').read_text())
    for item in bindings['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    E.parent.mkdir(mode=0o700, exist_ok=True)
    E.mkdir(mode=0o700, exist_ok=False)
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

    def log_message(self, *args):
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

    def do_GET(self):
        try:
            check(self.path in ('/index', '/worker'), 'not one of two fixed artifact routes')
            check(self.headers.get('Host') == urllib.parse.urlsplit(peer_origin).netloc,
                  'owned artifact Host differs')
            check(not any(self.headers.get(name) for name in
                          ('Authorization', 'Proxy-Authorization', 'X-API-Key', 'Cookie')),
                  'unexpected artifact credential')
            check(not self.headers.get('Transfer-Encoding')
                  and int(self.headers.get('Content-Length', '0')) == 0,
                  'artifact request has a body')
            raw = json.dumps(index).encode() if self.path == '/index' else worker_bytes
            kind = 'application/json' if self.path == '/index' else 'application/octet-stream'
            row = {'at': now(), 'method': 'GET', 'path': self.path, 'status': 200,
                   'response_bytes': len(raw), 'response_sha256': hashlib.sha256(raw).hexdigest()}
            with lock:
                OBS['peer_calls'].append(row)
            self.reply(200, raw, kind)
            with lock:
                row['response_completed'] = True
        except BaseException as error:
            self.reject('GET ' + type(error).__name__ + ': ' + str(error))

    def do_CONNECT(self):
        self.reject('CONNECT forbidden')

    def do_POST(self):
        self.reject('POST forbidden; no downloader protocol fixture')

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
    call('POST', '/api/auth/login', {'username': 'owned-s02', 'password': password}, authenticated=False)
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
              'backup API registration failed; no rejected/transport retry')
        time.sleep(.05)  # Only same owned-route application 404 -> 405 readiness.


def material_input(downloader, directory, hash_value):
    return {'downloader_id': downloader, 'directory': str(directory), 'hash': hash_value}


def file_record(path):
    # Only explicitly created owned positive fixture files; no filesystem escape
    # probe, inode inspection, permissions manipulation or broad tree scan.
    data = path.read_bytes()
    st = path.stat()
    return {'path': str(path.relative_to(E)), 'size': len(data),
            'sha256': hashlib.sha256(data).hexdigest(), 'mtime_ns': st.st_mtime_ns}


def check_backups(label):
    records = [file_record(path) for path in backup_paths]
    check(records == initial_backups, 'owned backup bytes/mtime changed: ' + label)
    OBS['backup_checks'].append({'label': label, 'files': records})


def check_temp(label):
    row = file_record(TEMP)
    check(row['sha256'] == hashlib.sha256(rebuilt).hexdigest() and row['size'] == len(rebuilt),
          'retained rebuilt material bytes differ: ' + label)
    entries = sorted(path.name for path in TEMP.parent.iterdir())
    check(entries == [HASH + '.torrent'], 'same-hash calls accumulated temporary material')
    OBS.setdefault('temporary_checks', []).append({'label': label, 'file': row, 'entries': entries})
    return row


def query(label, requests, worker=None, ok=True, continue_on_error=False):
    before = peer_counts()
    _, result = call('POST', API, {'requests': requests, 'continue_on_error': continue_on_error,
                                  'probe_previous': False})
    bind_worker(result.get('pid'))
    OBS['queries'].append({'label': label, 'host_pid': proc.pid, 'response': result,
                           'peer_counts_before': before, 'peer_counts_after': peer_counts()})
    save()
    check(result.get('ok') is ok and result.get('completed') == len(requests)
          and result.get('requested') == len(requests) and len(result.get('results', [])) == len(requests),
          'actual SDK aggregate differs: ' + label)
    previous = [row['response'] for row in OBS['queries'][:-1]
                if row['response'].get('pid') == result['pid']]
    check(result.get('invocation') == (previous[-1]['invocation'] + 1 if previous else 1),
          'worker invocation sequence differs: ' + label)
    if worker is None:
        check(result.get('had_previous') is False, 'fresh worker has prior business invocation')
    else:
        check(result['pid'] == worker and result.get('had_previous') is True,
              'warm worker replaced after skip/error/recovery')
    check(type(result.get('init_count')) is int and result['init_count'] >= 1,
          'actual worker init receipt missing')
    if previous:
        check(result['init_count'] == previous[-1]['init_count'],
              'material operation reinitialized the warm worker')
    check(peer_counts() == before, 'material operation contacted downloader or artifact peer')
    check_backups(label)
    return result


def expect_bytes(result, position, value, temporary):
    attempt = result['results'][position]
    expected = {'skipped': False, 'temporary': temporary, 'length': len(value),
                'sha256': hashlib.sha256(value).hexdigest()}
    if len(value) <= 4096:
        expected['small_base64'] = base64.b64encode(value).decode()
    pages = (len(value) + (1 << 20) - 1) // (1 << 20)
    counts = {'total': 2 + pages, 'prepare': 1, 'read': pages, 'release': 1}
    check(attempt.get('index') == position and attempt.get('ok') is True
          and attempt.get('result') == expected and not attempt.get('error'),
          'worker did not return/hash full actual SDK material')
    check(attempt.get('host_calls') == counts, 'actual SDK page/callback attempts differ')


def expect_skip(result):
    attempt = result['results'][0]
    check(attempt.get('ok') is True and attempt.get('result') == {'skipped': True, 'temporary': False}
          and attempt.get('host_calls') == PREPARE and result.get('succeeded') == 1
          and result.get('failed') == 0 and result.get('reassembled_bytes') == 0,
          'missing resume must be source skip, not bytes or transport error')


preflight()
DATA, BACKUP, LIBRARY = E / 'private-data', E / 'backup', E / 'library-backup'
HASH = '1111111111111111111111111111111111111111'
TEMP = DATA / 'temp/plugin-torrent-backup' / (HASH + '.torrent')
password = secrets.token_urlsafe(30)
worker_bytes = WORKER.read_bytes()
backup_paths, initial_backups = [], []
try:
    materials_raw = (AUDIT / 'owned-materials.json').read_bytes()
    materials = json.loads(materials_raw)
    original = base64.b64decode(materials['restore']['torrent_b64'])
    resume = base64.b64decode(materials['restore']['resume_b64'])
    rebuilt = base64.b64decode(materials['restore']['expected_b64'])
    missing = base64.b64decode(materials['missing_resume']['torrent_b64'])
    # Positive TR branch reads raw bytes unchanged, without parsing a torrent.
    # This finite synthetic paging vector is not a new source parser observation.
    large = (b'Owned S02 raw material\x00' * 50000)[:(1 << 20) + 3]
    check(len(large) == (1 << 20) + 3, 'owned two-page vector has wrong length')
    BACKUP.mkdir(mode=0o700)
    LIBRARY.mkdir(mode=0o700)
    for path, data in [(BACKUP / (HASH + '.torrent'), original),
                       (BACKUP / (HASH + '.fastresume'), resume),
                       (BACKUP / 'OwnedMissingResume.torrent', missing),
                       (LIBRARY / 'OwnedPages.torrent', large)]:
        with path.open('xb') as target:
            target.write(data)
        backup_paths.append(path)
    initial_backups = [file_record(path) for path in backup_paths]
    OBS['initial_backups'] = initial_backups
    OBS['owned_material_input_sha256'] = hashlib.sha256(materials_raw).hexdigest()
    OBS['temp_age_policy'] = {'configured_override': False, 'source_default_days': 3,
                             'age_deletion_boundary_claimed': False}
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == ['downloader.backup.material'], 'worker manifest differs')
    check(manifest.get('apis') == [{'key': 'backup', 'path': '/backup', 'methods': ['POST'],
                                   'auth': 'bear', 'body': 'json', 'body_required': True}],
          'worker backup API differs')
    (E / 'worker-manifest.json').write_bytes(completed.stdout)
    peer = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    peer.daemon_threads = True
    peer_origin = 'http://127.0.0.1:' + str(peer.server_address[1])
    index.update({'schema_version': 1, 'plugin_id': PLUGIN, 'current_version': '1.0',
                  'releases': [{'version': '1.0', 'manifest': manifest, 'artifacts': [{
                      'platform': 'linux-amd64', 'url': peer_origin + '/worker',
                      'sha256': args.expected_worker_sha, 'executable_sha256': args.expected_worker_sha,
                      'size': len(worker_bytes), 'format': 'executable',
                      'host_contract': 'light-media.sdk-resources/v1'}]}]})
    thread = threading.Thread(target=peer.serve_forever, daemon=True)
    thread.start()
    bootstrap = {'username': 'owned-s02', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [str(BACKUP)],
        'roots': [{'id': 'owned-library-backup', 'name': 'Owned backup library',
                   'path': str(LIBRARY), 'kind': 'mixed'}],
        'downloaders': [{'id': 'owned-qb', 'kind': 'qb', 'name': 'Owned qB selector',
                         'base_url': peer_origin, 'enabled': False},
                        {'id': 'owned-tr', 'kind': 'transmission', 'name': 'Owned TR selector',
                         'base_url': peer_origin, 'enabled': False}]}}
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
    artifact_counts = {'GET /index': 1, 'GET /worker': 1}
    check(peer_counts() == artifact_counts, 'unexpected artifact/downloader calls at install')
    small_request = material_input('owned-qb', BACKUP, HASH)
    large_request = material_input('owned-tr', LIBRARY, 'OwnedPages')
    first = query('source_rebuilt_bytes_actual_worker', [small_request])
    expect_bytes(first, 0, rebuilt, True)
    warm_worker = first['pid']
    check(first.get('succeeded') == 1 and first.get('failed') == 0
          and first.get('reassembled_bytes') == len(rebuilt), 'rebuilt aggregate differs')
    skip = query('missing_resume_source_skip', [material_input('owned-qb', BACKUP, 'OwnedMissingResume')], warm_worker)
    expect_skip(skip)
    paged = query('TR_two_actual_pages', [large_request], warm_worker)
    expect_bytes(paged, 0, large, False)
    check(paged.get('reassembled_bytes') == len(large), 'large SDK bytes not fully assembled')
    recovery = query('parameter_error_then_same_worker_recovery',
                     [material_input('', BACKUP, HASH), small_request], warm_worker,
                     ok=False, continue_on_error=True)
    failure = recovery['results'][0]
    check(failure.get('ok') is False and failure.get('error_code') == 'host_operation_failed'
          and bool(failure.get('error')) and 'result' not in failure
          and failure.get('host_calls') == PREPARE, 'parameter failure not actually observed')
    expect_bytes(recovery, 1, rebuilt, True)
    check(recovery.get('failed') == 1 and recovery.get('succeeded') == 1
          and recovery.get('reassembled_bytes') == len(rebuilt), 'same-invocation recovery differs')
    warm_temp = check_temp('before_first_Close')
    first_host = proc.pid
    stop()
    check_backups('after_first_Close')
    check(check_temp('after_first_Close') == warm_temp, 'Close changed fresh retained temp material')
    start()
    check(proc.pid != first_host, 'cold host is not a new process')
    registered()
    check(check_temp('cold_before_material') == warm_temp, 'cold startup changed fresh retained material')
    check(peer_counts() == artifact_counts, 'cold startup re-fetched artifact or queried downloader')
    cold = query('cold_normal_same_data_rebuilt_and_paged', [small_request, large_request])
    check(cold['pid'] != warm_worker, 'cold host did not load a fresh worker')
    expect_bytes(cold, 0, rebuilt, True)
    expect_bytes(cold, 1, large, False)
    check(cold.get('succeeded') == 2 and cold.get('failed') == 0
          and cold.get('reassembled_bytes') == len(rebuilt) + len(large), 'cold byte aggregate differs')
    stop()
    check_backups('terminal_two_hosts')
    check_temp('terminal_retained_not_deleted')
    check(peer_counts() == artifact_counts and all(row.get('response_completed') for row in OBS['peer_calls']),
          'unexpected/incomplete fixed artifact calls')
    check(sum(row['method'] == 'POST' and row['path'] == '/api/plugins/install'
              for row in OBS['calls']) == 1, 'more than one installation')
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    OBS['error'] = str(error).replace(password, '[owned-password]')
finally:
    try:
        stop()
    except BaseException as error:
        OBS['status'] = 'FAIL'
        OBS['cleanup_error'] = type(error).__name__ + ': ' + str(error)
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
        ('driver', Path(__file__), args.expected_driver_sha)) if not path.is_file() or sha(path) != digest]
    for item in json.loads((AUDIT / 'input-bindings.json').read_text())['inputs']:
        if not Path(item['path']).is_file() or sha(item['path']) != item['sha256']:
            OBS['input_drift'].append(item['path'])
    OBS['environment_unchanged'] = os.environ.copy() == INITIAL_ENV and ENV == INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):
        OBS['status'] = 'FAIL'
    try:
        reserve()  # Terminal space reserve is a gate too.
    except BaseException as error:
        OBS['status'] = 'FAIL'
        OBS['terminal_reserve_error'] = type(error).__name__ + ': ' + str(error)
    OBS['finished_at'] = now()
    save()
    (E / 'driver.exit').write_text('0\n' if OBS['status'] == 'PASS' else '1\n')
    print(json.dumps({'status': OBS['status'], 'hosts': [x['pid'] for x in OBS['processes']],
                      'queries': len(OBS['queries']), 'error': OBS.get('error'), 'evidence': str(E)}))
raise SystemExit(0 if OBS['status'] == 'PASS' else 1)
