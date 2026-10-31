#!/usr/bin/env python3
"""PREPARED, NOT RUN: S12.material through two ordinary owned CLI hosts.

Only exact 39123 controller requests and fixed same-process synthetic peers.
No source imports, requests/urllib3, generic forwarding, CONNECT, proxy rewrite,
private DB access, downloader, browser, real site or media/NAS operations.
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
setup_root_seen = threading.Event()
index, owned_workers, process_workers = {}, {}, {}
peer_origin = ''
recovery_healthy = False
origin = 'http://127.0.0.1:39123'
API = '/api/v1/plugin/OwnedTorrentMaterial/material'
SITE_COOKIE = 'owned-material-cookie=synthetic'
SITE_UA = 'OwnedMaterial/1.0'
SITE_NAME = 'Owned Material Site'
FAIL_TITLE = 'Owned.S12.Failure 种子下载失败！'
MAX_MATERIAL = 16 << 20
OBS = {
    'status': 'FAIL', 'kind': 'new S12.material two normal CLI hosts and actual installed SDK',
    'calls': [], 'peer_calls': [], 'setup_root_calls': [], 'processes': [], 'queries': [],
    'physical_wire_counters': {'available_from_ordinary_API': False, 'measured': False},
    'not_claimed': ['browser/UI', 'browse/RSS/S14', 'real site/downloader/media/NAS',
                    'arbitrary-size source parity', 'physical wire byte counts',
                    'capability-negative artifact or config-race App gates',
                    'full platform, CI or delivery'],
}


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
    check(INITIAL_ENV.get('S12_NORMAL_CLI_GRANTED') == '1',
          'coordinator execution window required; this file is only prepared')
    check(SERVER.is_relative_to(ROOT) and WORKER.is_relative_to(ROOT),
          'owned new project binaries required')
    check(sha(SERVER) == args.expected_server_sha and sha(WORKER) == args.expected_worker_sha
          and sha(__file__) == args.expected_driver_sha, 'frozen program/driver SHA mismatch')
    check(E.parent == AUDIT and not E.exists(), 'exclusive new owned audit child required')
    for name in ('PLUGIN_MARKET', 'LIGHT_MEDIA_MEDIA_ROOTS'):
        check(not ENV.get(name), 'inherited endpoint/mount environment conflicts; no rewrite')
    for name in ('DEV', 'LIGHT_MEDIA_SAFE_MODE', 'MOVIEPILOT_SAFE_MODE'):
        check(name not in ENV or ENV[name].lower() in ('0', 'false', 'f', 'no', 'n', 'off'),
              'inherited startup mode conflicts; no rewrite')
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
          ('POST', '/api/sites/manage'), ('GET', '/api/sites/manage'),
          ('GET', '/api/notifications'), ('GET', API), ('POST', API)}


def call(method, path, body=None, authenticated=True, expected=200):
    global cookie
    reserve()
    check((method, path) in ROUTES, 'outside nine exact owned method/path pairs')
    target = urllib.parse.urlsplit(origin + path)
    check(target.scheme == 'http' and target.netloc == '127.0.0.1:39123'
          and target.hostname == '127.0.0.1' and target.port == 39123
          and target.path == path and not target.query and not target.fragment
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


def bencode(value):
    # Fixed synthetic fixture construction only, not a migrated parser/oracle.
    if isinstance(value, bytes):
        return str(len(value)).encode() + b':' + value
    if type(value) is int:
        return b'i' + str(value).encode() + b'e'
    if isinstance(value, dict):
        return b'd' + b''.join(bencode(key) + bencode(value[key]) for key in sorted(value)) + b'e'
    raise TypeError('outside finite synthetic bencode values')


def synthetic_materials():
    info = {b'length': 1, b'name': b'owned.mkv', b'piece length': 16384,
            b'pieces': hashlib.sha1(b'\x00').digest()}
    small = bencode({b'info': info})
    padding = MAX_MATERIAL - len(small) - 32
    for _ in range(4):
        large = bencode({b'comment': b'O' * padding, b'info': info})
        if len(large) == MAX_MATERIAL:
            return small, large
        padding += MAX_MATERIAL - len(large)
    raise AssertionError('fixed exact 16 MiB material construction failed')


PEER_GETS = {'/', '/index', '/worker', '/material/small.torrent', '/material/large.torrent',
             '/material/recover.torrent', '/material/descriptor.torrent'}
DESCRIPTOR_PATH = '/descriptor/token?owned=descriptor'


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

    def admit(self, method, cookie_expected):
        expected_host = urllib.parse.urlsplit(peer_origin).netloc
        check(self.headers.get('Host') == expected_host, 'owned peer Host differs')
        check(not self.headers.get('Authorization') and not self.headers.get('Proxy-Authorization')
              and not self.headers.get('X-API-Key'), 'unexpected owned peer credential header')
        check(not self.headers.get('Transfer-Encoding'), 'owned peer chunked request not allowed')
        length = int(self.headers.get('Content-Length', '0'))
        check(length == 0, 'source descriptor params must be query, not request body')
        check(self.headers.get('Cookie', '') == cookie_expected, 'source Cookie boundary differs')
        # SiteUpdated's existing icon consumer uses the server UA while its
        # userdata consumer uses the configured site UA. Neither is material IO.
        if self.path not in ('/', '/index', '/worker'):
            check(self.headers.get('User-Agent') == SITE_UA, 'source User-Agent differs')
        row = {'at': now(), 'method': method, 'path': self.path,
               'cookie_present': bool(self.headers.get('Cookie')), 'request_bytes': length,
               'scope': 'setup' if self.path == '/' else
                        ('artifact' if self.path in ('/index', '/worker') else 'material')}
        with lock:
            OBS['peer_calls'].append(row)
            if self.path == '/':
                OBS['setup_root_calls'].append(row)
        return row

    def do_GET(self):
        try:
            check(self.path in PEER_GETS, 'unexpected owned GET path')
            expected_cookie = SITE_COOKIE if self.path in (
                '/', '/material/small.torrent', '/material/large.torrent', '/material/recover.torrent') else ''
            row = self.admit('GET', expected_cookie)
            if self.path == '/':
                # Saving even a disabled site emits the real SiteUpdated core
                # userdata/icon work. This exact owned setup route is explicit,
                # recorded and 404, never treated as an unknown ignored request.
                raw = b'404 page not found\n'
                with lock:
                    row.update(status=404, response_bytes=len(raw),
                               response_sha256=hashlib.sha256(raw).hexdigest())
                self.reply(404, raw, 'text/plain; charset=utf-8')
                with lock:
                    row['response_completed'] = True
                setup_root_seen.set()
                return
            kind = 'application/x-bittorrent'
            if self.path == '/index':
                raw, kind = json.dumps(index).encode(), 'application/json'
            elif self.path == '/worker':
                raw, kind = worker_bytes, 'application/octet-stream'
            elif self.path == '/material/large.torrent':
                raw = large_bytes
            elif self.path == '/material/recover.torrent':
                with lock:
                    raw = small_bytes if recovery_healthy else b''
            else:
                raw = small_bytes
            with lock:
                row.update(status=200, response_bytes=len(raw), response_sha256=hashlib.sha256(raw).hexdigest())
            self.reply(200, raw, kind)
        except BaseException as error:
            self.reject('GET ' + type(error).__name__ + ': ' + str(error))

    def do_POST(self):
        try:
            check(self.path == DESCRIPTOR_PATH, 'unexpected owned POST path')
            row = self.admit('POST', SITE_COOKIE)
            raw = json.dumps({'success': True, 'data': {'url': '/material/descriptor.torrent'}}).encode()
            with lock:
                row.update(status=200, response_bytes=len(raw), response_sha256=hashlib.sha256(raw).hexdigest())
            self.reply(200, raw)
        except BaseException as error:
            self.reject('POST ' + type(error).__name__ + ': ' + str(error))

    def do_CONNECT(self):
        self.reject('CONNECT is not an owned fixture operation')

    def do_PUT(self):
        self.reject('PUT is not an owned fixture operation')

    def do_DELETE(self):
        self.reject('DELETE is not an owned fixture operation')

    def do_HEAD(self):
        self.reject('HEAD is not an owned fixture operation')


def peer_counts():
    # Material/artifact counts exclude only the separately recorded fixed root
    # setup route. Root records are never cleared or folded into fetch counts.
    with lock:
        result = {}
        for row in OBS['peer_calls']:
            if row['scope'] == 'setup':
                continue
            key = row['method'] + ' ' + row['path']
            result[key] = result.get(key, 0) + 1
        check(not OBS.get('peer_errors'), 'owned peer rejected an unexpected request')
        return result


def wait_setup_root():
    # Wait only on this peer's actual root-response event, not App.wg or any
    # unrelated background/material completion. More root requests may follow;
    # retain every one without inventing an expected total of one or two.
    check(setup_root_seen.wait(timeout=5), 'owned SiteUpdated root response was not observed')
    with lock:
        OBS['setup_root_wait'] = {'at': now(), 'observed_count': len(OBS['setup_root_calls']),
                                  'waited_only_owned_peer_event': True}


def check_setup_roots():
    with lock:
        rows = OBS['setup_root_calls']
        check(rows and all(row['method'] == 'GET' and row['path'] == '/'
                           and row.get('status') == 404 and row.get('response_completed') is True
                           for row in rows), 'owned setup root responses were not exact completed 404s')
        OBS['setup_root_total'] = len(rows)


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
    call('POST', '/api/auth/login', {'username': 'owned-s12', 'password': password}, authenticated=False)
    _, settings = call('GET', '/api/plugins/markets')
    check(settings['data']['plugin_statistic_share'] is False
          and settings['data']['plugin_server_url'] == '', 'owned settings isolation differs')


def registered():
    deadline = time.monotonic() + 20
    while True:
        status, _ = call('GET', API, expected=None)
        if status == 405:
            return
        check(status == 404 and time.monotonic() < deadline,
              'material registration failed; no transport-denial retry')
        time.sleep(.05)  # Existing same-route application 404 -> 405 polling only.


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


def torrent(enclosure, title='Owned.S12.Material'):
    # No mandatory browse/get or private numeric-ID lookup: configured origin
    # is unique. All private fields are synthetic and match the managed site.
    return {'torrent': {'enclosure': enclosure, 'title': title, 'site_name': SITE_NAME,
                        'site_cookie': SITE_COOKIE, 'site_ua': SITE_UA, 'site_proxy': False}}


def query(label, requests, worker=None, ok=True, continue_on_error=False, probe=True):
    body = {'requests': requests, 'continue_on_error': continue_on_error, 'probe_previous': probe}
    before = peer_counts()
    _, result = call('POST', API, body)
    bind_worker(result.get('pid'))
    row = {'label': label, 'host_pid': proc.pid, 'response': result,
           'peer_counts_before': before, 'peer_counts_after': peer_counts()}
    OBS['queries'].append(row)
    save()
    check(result.get('ok') is ok and result.get('completed') == len(requests)
          and result.get('requested') == len(requests), 'SDK operation aggregate differs: ' + label)
    check(len(result.get('results', [])) == len(requests), 'SDK attempt count differs: ' + label)
    if ok:
        check(result.get('succeeded') == len(requests) and result.get('failed') == 0,
              'successful SDK operation count differs: ' + label)
    previous = [item['response'] for item in OBS['queries'][:-1]
                if item['response'].get('pid') == result['pid']]
    expected_invocation = previous[-1]['invocation'] + 1 if previous else 1
    check(result.get('invocation') == expected_invocation, 'worker invocation sequence differs: ' + label)
    if worker is not None:
        check(result['pid'] == worker and result.get('had_previous') is True, 'warm worker replaced: ' + label)
        if probe:
            check(result.get('stale_denied') is True, 'expired prior invocation callback accepted: ' + label)
    else:
        check(result.get('had_previous') is False and 'stale_denied' not in result,
              'fresh worker has prior business invocation: ' + label)
    return result


def expect_value(result, index, value, calls):
    attempt = result['results'][index]
    check(attempt.get('index') == index and attempt.get('ok') is True
          and attempt.get('result') == value and 'error' not in attempt and 'source_error' not in attempt,
          'SDK full material tuple differs')
    check(attempt.get('host_calls') == calls, 'actual SDK callback attempt counts differ')


def byte_value(raw):
    return {'kind': 'bytes', 'length': len(raw), 'sha256': hashlib.sha256(raw).hexdigest(),
            'folder': '', 'files': ['owned.mkv']}


def expect_source_error(result, index):
    attempt = result['results'][index]
    check(attempt.get('index') == index and attempt.get('ok') is False
          and attempt.get('source_error') == 'JSONDecodeError' and bool(attempt.get('error'))
          and 'result' not in attempt and not attempt.get('error_code'),
          'source exception must be typed SDK class, not None, transport error or string matching')
    check(attempt.get('host_calls') == PREPARE, 'descriptor exception did not stop at prepare')


def notices():
    _, result = call('GET', '/api/notifications')
    rows = result.get('data', {}).get('items')
    check(isinstance(rows, list), 'normal notification history is not a list')
    return rows


def failure_notice(rows):
    found = [row for row in rows if row.get('title') == FAIL_TITLE]
    check(len(found) == 1, 'expected exactly one real material failure notification')
    notice = found[0]
    expected = {'title': FAIL_TITLE, 'message': '错误信息：未下载到种子数据\n站点：' + SITE_NAME,
                'mtype': '手动处理', 'channel': 'Web', 'source': 'OwnedMaterialGate',
                'userid': '9007199254740993'}
    check(all(notice.get(key) == value for key, value in expected.items())
          and not notice.get('link') and bool(notice.get('id')) and bool(notice.get('created_at')),
          'source Manual notice/channel/source/large integer/link fields differ')
    return notice


def check_site(result):
    rows = result.get('data', {}).get('sites')
    check(isinstance(rows, list) and len(rows) == 1, 'owned managed site projection differs')
    conf = rows[0].get('config', {})
    check(conf.get('id') == 'owned-material-site' and conf.get('kind') == 'jiayuan'
          and conf.get('base_url') == peer_origin and conf.get('enabled') is False
          and conf.get('name') == SITE_NAME and rows[0].get('credential_configured') is True,
          'disabled configured site was not preserved; do not enable it to hide a source difference')


preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
worker_bytes = WORKER.read_bytes()
small_bytes, large_bytes = synthetic_materials()
PREPARE = {'total': 1, 'prepare': 1, 'read': 0, 'release': 0}
SMALL_CALLS = {'total': 3, 'prepare': 1, 'read': 1, 'release': 1}
LARGE_CALLS = {'total': 18, 'prepare': 1, 'read': 16, 'release': 1}
NONE = {'kind': 'none', 'folder': '', 'files': []}
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest['plugin_id'] == 'OwnedTorrentMaterial' and manifest['version'] == '1.0'
          and manifest['capabilities'] == ['chain.download.download_torrent'], 'owned one-capability manifest differs')
    check(manifest['apis'] == [{'key': 'material', 'path': '/material', 'methods': ['POST'],
                               'auth': 'bear', 'body': 'json', 'body_required': True}]
          and 'http' in manifest['methods'], 'owned ordinary material API differs')
    (E / 'worker-manifest.json').write_bytes(completed.stdout)
    OBS['synthetic_materials'] = {'small': byte_value(small_bytes), 'large': byte_value(large_bytes)}
    peer = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    peer.daemon_threads = True
    peer_origin = 'http://127.0.0.1:' + str(peer.server_address[1])
    index.update({'schema_version': 1, 'plugin_id': 'OwnedTorrentMaterial', 'current_version': '1.0',
                  'releases': [{'version': '1.0', 'manifest': manifest, 'artifacts': [{
                      'platform': 'linux-amd64', 'url': peer_origin + '/worker',
                      'sha256': args.expected_worker_sha, 'executable_sha256': args.expected_worker_sha,
                      'size': len(worker_bytes), 'format': 'executable',
                      'host_contract': 'light-media.sdk-resources/v1'}]}]})
    thread = threading.Thread(target=peer.serve_forever, daemon=True)
    thread.start()
    bootstrap = {'username': 'owned-s12', 'password': password, 'settings': {
        'api_token': secrets.token_urlsafe(32), 'plugin_markets': [], 'plugin_local_repo_paths': [],
        'plugin_auto_reload': False, 'plugin_statistic_share': False, 'plugin_server_url': '',
        'site_data_refresh_hours': 0, 'read_site_messages': False, 'downloaders': [], 'sites': [],
        'directories': [], 'roots': [], 'allowed_roots': [], 'rss_sites': []}}
    boot = subprocess.run([str(SERVER), '--data-dir', str(DATA), '--bootstrap-stdin'],
                          input=json.dumps(bootstrap).encode(), capture_output=True, env=ENV, timeout=30)
    del bootstrap
    OBS['bootstrap_exit'] = boot.returncode
    check(boot.returncode == 0 and b'bootstrap_complete' in boot.stdout,
          'owned bootstrap failed; credentials/output withheld')
    start()
    site = {'id': 'owned-material-site', 'kind': 'jiayuan', 'name': SITE_NAME, 'base_url': peer_origin,
            'cookie': SITE_COOKIE, 'ua': SITE_UA, 'enabled': False, 'proxy': False}
    _, saved = call('POST', '/api/sites/manage', site)
    check_site(saved)
    wait_setup_root()
    _, installed = call('POST', '/api/plugins/install',
                        {'plugin_id': 'OwnedTorrentMaterial', 'repo_url': peer_origin + '/index'})
    check(installed.get('data', {}).get('success') is True, 'owned installation business result failed')
    registered()
    artifact_counts = {'GET /index': 1, 'GET /worker': 1}
    check(peer_counts() == artifact_counts, 'initialization performed material IO or acquired extra artifacts')
    call('POST', API, {'requests': [torrent(peer_origin + '/material/small.torrent')]},
         authenticated=False, expected=401)
    check(peer_counts() == artifact_counts, 'unauthenticated API caused material IO')
    before_notices = notices()
    first = query('source_none', [{'torrent': {'enclosure': None}}], probe=False)
    warm = first['pid']
    expect_value(first, 0, NONE, PREPARE)
    check(first.get('reassembled_bytes') == 0 and first.get('invocation') == 1, 'fresh source None differs')
    magnet = 'magnet:?xt=urn:btih:0123456789012345678901234567890123456789&dn=Owned.S12'
    value = query('exact_magnet_and_stale_scope', [{'torrent': {'enclosure': magnet}}], warm)
    expect_value(value, 0, {'kind': 'string', 'text': magnet, 'folder': '', 'files': []}, PREPARE)
    check(value.get('reassembled_bytes') == 0 and peer_counts() == artifact_counts, 'magnet/None caused network')
    malformed = torrent('[ew==]' + peer_origin + '/descriptor/token')  # base64("{") -> JSONDecodeError.
    failed = query('typed_descriptor_exception', [malformed], warm, ok=False)
    expect_source_error(failed, 0)
    check(failed.get('failed') == 1 and failed.get('succeeded') == 0
          and failed.get('reassembled_bytes') == 0 and peer_counts() == artifact_counts,
          'descriptor exception classification/side effects differ')
    check(notices() == before_notices, 'early None/magnet/descriptor exception fabricated a notice')
    small_request = torrent(peer_origin + '/material/small.torrent')
    small = query('same_worker_recovery_small_full_bytes', [small_request], warm)
    expect_value(small, 0, byte_value(small_bytes), SMALL_CALLS)
    check(small.get('reassembled_bytes') == len(small_bytes), 'small full reconstruction differs')
    warm_cache = peer_counts()
    cached = query('warm_cache_full_byte_return', [small_request], warm)
    expect_value(cached, 0, byte_value(small_bytes), SMALL_CALLS)
    check(peer_counts() == warm_cache, 'warm cache fetched source again')
    ordered = query('typed_failure_then_same_invocation_recovery', [malformed, small_request],
                    warm, ok=False, continue_on_error=True)
    expect_source_error(ordered, 0)
    expect_value(ordered, 1, byte_value(small_bytes), SMALL_CALLS)
    check(ordered.get('failed') == 1 and ordered.get('succeeded') == 1
          and ordered.get('reassembled_bytes') == len(small_bytes) and peer_counts() == warm_cache,
          'ordered typed failure/recovery differs')
    descriptor = {'method': 'post', 'params': {'owned': 'descriptor'}, 'result': 'data.url',
                  'success': 'success', 'cookie': True, 'proxy': False, 'result_base_url': peer_origin}
    encoded = base64.b64encode(json.dumps(descriptor, separators=(',', ':')).encode()).decode()
    indirect = query('descriptor_query_and_second_stage_cookie_drop',
                     [torrent('[' + encoded + ']' + peer_origin + '/descriptor/token')], warm)
    expect_value(indirect, 0, byte_value(small_bytes), SMALL_CALLS)
    failure_request = torrent(peer_origin + '/material/recover.torrent', 'Owned.S12.Failure')
    failure_request.update(channel='Web', source='OwnedMaterialGate', userid=9007199254740993)
    empty = query('helper_empty_is_source_none_with_real_notice', [failure_request], warm)
    expect_value(empty, 0, NONE, PREPARE)
    check(empty.get('failed') == 0 and empty.get('reassembled_bytes') == 0,
          'source None was confused with SDK/transport error')
    persisted_notice = failure_notice(notices())
    with lock:
        recovery_healthy = True
    recovered = query('same_url_and_worker_after_empty_failure', [failure_request], warm)
    expect_value(recovered, 0, byte_value(small_bytes), SMALL_CALLS)
    check(failure_notice(notices()) == persisted_notice, 'successful recovery duplicated/changed failure notice')
    large_request = torrent(peer_origin + '/material/large.torrent')
    full = query('actual_full_16MiB_inside_worker', [large_request], warm)
    expect_value(full, 0, byte_value(large_bytes), LARGE_CALLS)
    check(full.get('reassembled_bytes') == MAX_MATERIAL, 'worker did not reconstruct full 16 MiB')
    before_pair = peer_counts()
    pair = query('two_sequential_full_16MiB_actual_32MiB', [large_request, large_request], warm)
    for i in (0, 1):
        expect_value(pair, i, byte_value(large_bytes), LARGE_CALLS)
    check(pair.get('reassembled_bytes') == 2 * MAX_MATERIAL and pair.get('succeeded') == 2
          and pair.get('failed') == 0 and peer_counts() == before_pair,
          'actual two-full-byte normal Runner operation failed; arithmetic alone is not a PASS')
    expected_peer = dict(artifact_counts, **{
        'GET /material/small.torrent': 1, 'POST ' + DESCRIPTOR_PATH: 1,
        'GET /material/descriptor.torrent': 1, 'GET /material/recover.torrent': 2,
        'GET /material/large.torrent': 1})
    check(peer_counts() == expected_peer, 'warm owned peer request set/count differs')
    first_host = proc.pid
    stop()
    before_cold = peer_counts()
    start()
    check(proc.pid != first_host, 'cold host reused process')
    registered()
    _, cold_site = call('GET', '/api/sites/manage')
    check_site(cold_site)
    check(peer_counts() == before_cold, 'cold startup replayed material work or reacquired artifact')
    cold = query('cold_normal_host_persistent_small_and_large_cache',
                 [small_request, large_request], probe=False)
    check(cold['pid'] != warm, 'cold host did not own a fresh installed worker')
    expect_value(cold, 0, byte_value(small_bytes), SMALL_CALLS)
    expect_value(cold, 1, byte_value(large_bytes), LARGE_CALLS)
    check(cold.get('reassembled_bytes') == len(small_bytes) + MAX_MATERIAL
          and peer_counts() == before_cold, 'cold full-byte cache read fetched source again')
    cold_recovered = query('cold_recovered_material_and_scope', [failure_request], cold['pid'])
    expect_value(cold_recovered, 0, byte_value(small_bytes), SMALL_CALLS)
    check(peer_counts() == before_cold and failure_notice(notices()) == persisted_notice,
          'persistent recovered cache/real notice changed across normal restart')
    stop()
    check_setup_roots()
    check(sum(c['method'] == 'POST' and c['path'] == '/api/plugins/install' for c in OBS['calls']) == 1,
          'more than one installation')
    check(peer_counts() == expected_peer, 'unexpected source/artifact request at shutdown')
    OBS['actual_sequential_reassembled_bytes'] = pair['reassembled_bytes']
    OBS['actual_sdk_attempt_counts_for_pair'] = [row['host_calls'] for row in pair['results']]
    OBS['normal_runner_completed_pair'] = True
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
    OBS['environment_unchanged'] = os.environ.copy() == INITIAL_ENV and ENV == INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):
        OBS['status'] = 'FAIL'
    OBS['finished_at'] = now()
    save()
    (E / 'driver.exit').write_text('0\n' if OBS['status'] == 'PASS' else '1\n')
    print(json.dumps({'status': OBS['status'], 'hosts': [x['pid'] for x in OBS['processes']],
                      'queries': len(OBS['queries']), 'error': OBS.get('error'), 'evidence': str(E)}))
raise SystemExit(0 if OBS['status'] == 'PASS' else 1)
