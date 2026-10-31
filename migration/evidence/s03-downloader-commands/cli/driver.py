#!/usr/bin/env python3
"""PREPARED ONLY. S03 ordinary installed SDK through two normal hosts and
strict owned fixed HTTP protocol fixtures. Never a real daemon/download.
No Go/source imports, forwarding, CONNECT, proxy edits, or mutation retry.
"""
import argparse
import base64
import datetime
import email.parser
import email.policy
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
PLUGIN = 'OwnedDownloaderCommands'
API = '/api/v1/plugin/' + PLUGIN + '/commands'
HASH = '1111111111111111111111111111111111111111'
FAIL_HASH = '2222222222222222222222222222222222222222'
MAGNET = 'magnet:?xt=urn:btih:' + HASH
FAIL_MAGNET = 'magnet:?xt=urn:btih:' + FAIL_HASH
LARGE = 16 << 20
QB_DEFAULTS = {'autoTMM': ['False'], 'sequentialDownload': ['False'], 'firstLastPiecePrio': ['False']}
TR_VALUE = {'id': 101, 'hashString': HASH, 'name': 'Owned S03 synthetic result'}
CAPABILITIES = ['downloader.' + kind + '.' + operation
                for kind in ('qbittorrent', 'transmission')
                for operation in ('add', 'start', 'recheck', 'delete_keep_files')]
OBS = {'status': 'FAIL', 'kind': 'S03 normal host / installed SDK / fixed owned HTTP protocol',
       'calls': [], 'peer_calls': [], 'processes': [], 'queries': [],
       'physical_plugin_wire_counters': {'measured': False, 'available_from_ordinary_API': False},
       'measured_layers': ['worker logical SDK callback counters', 'owned endpoint HTTP requests and entity-body bytes'],
       'not_claimed': ['real daemon/download/NAS', 'filesystem or data deletion',
                       'complete Python Torrent object', 'browser/UI', 'full S03/platform/delivery']}



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
    check(INITIAL_ENV.get('S03_COMMAND_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER.is_relative_to(ROOT / 'runtime') and WORKER.is_relative_to(ROOT / 'runtime'),
          'owned coordinator-frozen binaries required')
    check(args.expected_server_sha != 'b999c2d36100289983bab3e6955e23b327b7b7733f6821227cd43c7e77eb7bca',
          'old S02 server cannot supply new capabilities')
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
    check(E.parent == ROOT / 'test-instances' and E.name.startswith('s03-command-')
          and not E.exists(), 'exclusive new S03 test-instance required')
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
        pass  # Never log request headers, credential values or raw bodies.

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
        self.connection.settimeout(20)  # Only this accepted owned server connection.
        raw = self.rfile.read(int(length))
        check(len(raw) == int(length), 'incomplete owned request body')
        return raw

    def emit(self, row, status, raw, kind='application/json'):
        row.update(at=now(), status=status, response_bytes=len(raw))
        with lock:
            OBS['peer_calls'].append(row)
        self.reply(status, raw, kind)
        with lock:
            row['response_completed'] = True

    def do_GET(self):
        try:
            check(self.path in ('/index', '/worker', '/api/v2/app/webapiVersion'), 'GET route not whitelisted')
            artifact = self.path in ('/index', '/worker')
            raw = self.common(0, None if artifact else 'Bearer ' + qb_key)
            check(raw == b'', 'GET body forbidden')
            if self.path == '/index':
                result, kind = json.dumps(index).encode(), 'application/json'
            elif self.path == '/worker':
                result, kind = worker_bytes, 'application/octet-stream'
            else:
                result, kind = b'2.11.0', 'text/plain'
            self.emit({'method': 'GET', 'path': self.path, 'request_body_bytes': 0}, 200, result, kind)
        except BaseException as error:
            self.reject('GET rejected: ' + type(error).__name__)

    def do_POST(self):
        try:
            check(self.path in ('/api/v2/torrents/add', '/api/v2/torrents/start',
                               '/api/v2/torrents/recheck', '/api/v2/torrents/delete', '/transmission/rpc'),
                  'POST route not whitelisted')
            if self.path == '/transmission/rpc':
                self.transmission()
            else:
                self.qbittorrent()
        except BaseException as error:
            self.reject('POST rejected: ' + type(error).__name__)

    def qbittorrent(self):
        raw = self.common(LARGE + (256 << 10), 'Bearer ' + qb_key)
        row = {'method': 'POST', 'path': self.path, 'request_body_bytes': len(raw)}
        content_type = self.headers.get('Content-Type', '')
        material = None
        if content_type.startswith('multipart/form-data;'):
            check(self.path == '/api/v2/torrents/add', 'multipart only on fixed qB add')
            parsed = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                b'Content-Type: ' + content_type.encode('ascii') + b'\r\nMIME-Version: 1.0\r\n\r\n' + raw)
            check(parsed.is_multipart() and not parsed.defects, 'invalid owned multipart')
            form = {}
            for part in parsed.iter_parts():
                check(not part.is_multipart() and not part.defects, 'nested/invalid multipart')
                name, filename = part.get_param('name', header='content-disposition'), part.get_filename()
                value = part.get_payload(decode=True)
                if filename is not None:
                    check(name == 'torrent__0' and filename == 'torrent__0' and material is None,
                          'one exact synthetic material part required')
                    check(part.get('Content-Type') is None, 'source bytes part Content-Type differs')
                    material = value
                else:
                    check(name not in form, 'duplicate ordinary qB form field')
                    form[name] = [value.decode('utf-8')]
            check(material is not None and len(material) == LARGE and hashlib.sha256(material).hexdigest() == large_sha,
                  'actual daemon-received 16 MiB material differs')
            row['material'] = {'length': len(material), 'sha256': hashlib.sha256(material).hexdigest(),
                               'kind': 'synthetic_i_mod_251_not_real_torrent'}
        else:
            check(content_type == 'application/x-www-form-urlencoded', 'qB form encoding differs')
            form = urllib.parse.parse_qs(raw.decode('ascii'), keep_blank_values=True, strict_parsing=True)
        if self.path == '/api/v2/torrents/add':
            if material is not None:
                check(form == QB_DEFAULTS, 'byte-add fixed form differs')
                row['selected_operation'] = 'qb_add_bytes'
                self.emit(row, 200, b'Ok.', 'text/plain')
            else:
                check(set(form) == set(QB_DEFAULTS) | {'urls'} and all(form[k] == v for k, v in QB_DEFAULTS.items()),
                      'string-add fixed form differs')
                check(form['urls'] in ([MAGNET], [FAIL_MAGNET]), 'non-owned add content refused without fetching')
                failed = form['urls'] == [FAIL_MAGNET]
                row['selected_operation'] = 'qb_add_owned_unknown500' if failed else 'qb_add_magnet'
                # Always fixed for this synthetic marker. No mutation queue,
                # upstream fetch, task creation, or successful replay fallback.
                self.emit(row, 500 if failed else 200, b'owned fixed error' if failed else b'Ok.', 'text/plain')
        else:
            wanted = {'hashes': [HASH]}
            if self.path.endswith('/delete'):
                wanted['deleteFiles'] = ['False']
                row['delete_files'] = False
            check(form == wanted, 'only exact owned IDs and deleteFiles=False permitted')
            row['selected_operation'] = 'qb_' + self.path.rsplit('/', 1)[1]
            self.emit(row, 200, b'Ok.', 'text/plain')

    def transmission(self):
        expected_auth = 'Basic ' + base64.b64encode((tr_user + ':' + tr_password).encode()).decode()
        raw = self.common(128 << 10, expected_auth)
        check(self.headers.get('Content-Type') == 'application/json'
              and not self.headers.get('X-Transmission-Session-Id'), 'fixed TR request headers differ')
        body = json.loads(raw)
        check(type(body) is dict and set(body) == {'method', 'arguments'} and type(body['arguments']) is dict,
              'legacy fixed envelope required; no generic or nested arbitrary RPC')
        method, arguments = body['method'], body['arguments']
        check(method in ('session-get', 'torrent-add', 'torrent-start', 'torrent-verify', 'torrent-remove'),
              'TR method not whitelisted')
        row = {'method': 'POST', 'path': self.path, 'rpc_method': method, 'request_body_bytes': len(raw)}
        if method == 'session-get':
            check(arguments == {}, 'session-get arguments differ')
            result = {'rpc-version': 17, 'rpc-version-minimum': 1, 'version': '4.0.6'}
        elif method == 'torrent-add':
            check(arguments == {'filename': MAGNET, 'paused': False}, 'only fixed synthetic TR add accepted')
            result = {'torrent-added': TR_VALUE}
            row['selected_operation'] = 'tr_add'
        else:
            wanted = {'ids': [101]}
            if method == 'torrent-remove':
                wanted['delete-local-data'] = False
                check(arguments.get('delete-local-data') is False, 'delete-local-data must be JSON false')
                row['delete_local_data'] = False
            check(arguments == wanted, 'only exact owned TR ID and delete-local-data=False accepted')
            result = {}
            row['selected_operation'] = {'torrent-start': 'tr_start', 'torrent-verify': 'tr_recheck',
                                         'torrent-remove': 'tr_delete'}[method]
        self.emit(row, 200, json.dumps({'result': 'success', 'arguments': result}).encode())

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


def command(kind, operation, content=None):
    item = {'kind': kind, 'op': operation,
            'downloader_id': 'owned-command-qb' if kind == 'qbittorrent' else 'owned-command-tr'}
    if operation == 'add':
        item['args'] = {'content': content}
    else:
        item['ids'] = [HASH] if kind == 'qbittorrent' else [101]
    return item


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
        # expected_delta above excludes the prior successful recheck. That
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


def qb_success(attempt):
    check(attempt.get('ok') is True and attempt.get('result') == {'success': True, 'ids': []}
          and attempt.get('source_truthy') is True and attempt.get('result_kind') == 'qbittorrent_source_tuple',
          'qB source tuple projection differs')


def control_success(attempt):
    check(attempt.get('ok') is True and attempt.get('result') is True
          and attempt.get('result_kind') == 'source_bool', 'source control return differs')



preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
qb_key, tr_user, tr_password = secrets.token_urlsafe(24), 'owned-s03-tr', secrets.token_urlsafe(24)
worker_bytes = WORKER.read_bytes()
# Pure synthetic formula independent of the worker; never written as torrent.
large_sha = hashlib.sha256((bytes(range(251)) * ((LARGE+250)//251))[:LARGE]).hexdigest()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == PLUGIN and manifest.get('version') == '1.0'
          and manifest.get('capabilities') == CAPABILITIES, 'worker eight-cap manifest differs')
    check(manifest.get('apis') == [{'key': 'commands', 'path': '/commands', 'methods': ['POST'],
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
    bootstrap = {'username': 'owned-s03', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'sites': [], 'rss_sites': [],
        'directories': [], 'allowed_roots': [], 'roots': [],
        'downloaders': [{'id': 'owned-command-qb', 'kind': 'qb', 'name': 'Owned synthetic qB commands',
                         'base_url': peer_origin, 'enabled': True, 'api_key': qb_key},
                        {'id': 'owned-command-tr', 'kind': 'transmission', 'name': 'Owned synthetic TR commands',
                         'base_url': peer_origin + '/transmission/rpc', 'enabled': True,
                         'username': tr_user, 'password': tr_password}]}}
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
    operations = [command(kind, operation, MAGNET) for kind in ('qbittorrent', 'transmission')
                  for operation in ('add', 'start', 'recheck', 'delete_keep_files')]
    first = query('actual_eight_fixed_methods', operations,
                  {k: 1 for k in ('qb_add_magnet', 'qb_start', 'qb_recheck', 'qb_delete',
                                   'tr_add', 'tr_start', 'tr_recheck', 'tr_delete')})
    warm_worker = first['pid']
    check(first['succeeded'] == 8 and first['failed'] == 0, 'eight SDK completions missing')
    for i, attempt in enumerate(first['results']):
        callbacks(attempt)
        if i == 0:
            qb_success(attempt)
        elif i == 4:
            check(attempt.get('ok') is True and attempt.get('result') == TR_VALUE
                  and attempt.get('result_kind') == 'transmission_native_json_not_python_torrent',
                  'finite native TR JSON fields differ')
        else:
            control_success(attempt)
    recovery = query('owned500_unknown_then_same_worker_recovery',
                     [command('qbittorrent', 'add', FAIL_MAGNET), command('qbittorrent', 'recheck')],
                     {'qb_add_owned_unknown500': 1, 'qb_recheck': 1}, warm_worker,
                     ok=False, continue_on_error=True)
    failed = recovery['results'][0]
    check(failed.get('ok') is False and failed.get('error_code') == 'host_operation_failed'
          and 'result' not in failed and recovery['succeeded'] == 1 and recovery['failed'] == 1,
          'post-dispatch failure wrongly converted to source False/success')
    callbacks(failed, failed=1)
    control_success(recovery['results'][1]); callbacks(recovery['results'][1])
    OBS['owned500_outcome'] = {'effect': 'unknown_by_product_not_replayed',
                              'fixture': 'one fixed 500 response; no actual daemon mutation'}
    large = {'kind': 'qbittorrent', 'op': 'add', 'downloader_id': 'owned-command-qb',
             'args': {}, 'synthetic_content': {'length': LARGE}}
    paged = query('16MiB_actual_upload_and_previous_callback_denial', [large],
                  {'qb_add_bytes': 1}, warm_worker, probe=True)
    attempt = paged['results'][0]; qb_success(attempt)
    counts = callbacks(attempt, total=18, begin=1, append=16, submit=1, invoke=0)
    check(counts.get('append_payload_bytes') == LARGE
          and attempt.get('generated') == {'kind': 'deterministic_bytes_i_mod_251', 'length': LARGE, 'sha256': large_sha},
          'worker full byte generation/callback measurement differs')
    byte_rows = [r for r in OBS['peer_calls'] if r.get('selected_operation') == 'qb_add_bytes']
    check(len(byte_rows) == 1 and byte_rows[0]['material']['sha256'] == large_sha,
          'independent actual endpoint byte digest missing')
    before_cold = peer_counts(); first_host = proc.pid
    stop(); start()
    check(proc.pid != first_host, 'cold host is not a new process')
    registered()
    check(peer_counts() == before_cold, 'cold startup unexpectedly re-fetched or contacted a downloader')
    cold = query('cold_same_DB_new_worker_recheck', [command('qbittorrent', 'recheck')], {'qb_recheck': 1})
    check(cold['pid'] != warm_worker and cold['succeeded'] == 1 and cold['failed'] == 0,
          'cold worker was not fresh/successful')
    control_success(cold['results'][0]); callbacks(cold['results'][0])
    stop()
    counts = peer_counts()
    expected = {'GET /index': 1, 'GET /worker': 1, 'GET /api/v2/app/webapiVersion': 4,
                'POST /api/v2/torrents/add': 3, 'POST /api/v2/torrents/start': 1,
                'POST /api/v2/torrents/recheck': 3, 'POST /api/v2/torrents/delete': 1,
                'POST /transmission/rpc': 8}
    check(counts == expected and all(row.get('response_completed') for row in OBS['peer_calls']),
          'finite HTTP request totals/incomplete responses differ')
    check(sum(row['method'] == 'POST' and row['path'] == '/api/plugins/install' for row in OBS['calls']) == 1,
          'more than one installation')
    for path in E.glob('host-*.log'):
        data = path.read_bytes()
        check(not any(secret.encode() in data for secret in (password, qb_key, tr_password)),
              'synthetic credential appeared in host log; private evidence requires review')
    OBS['actual_endpoint_request_counts'] = counts
    OBS['status'] = 'PASS'
except BaseException as error:
    OBS['error_class'] = type(error).__name__
    # Validation messages are fixed and never echo wire values; sanitize even
    # synthetic credentials defensively if an external exception contains one.
    text = str(error)
    for secret in (password, qb_key, tr_password):
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
