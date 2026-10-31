#!/usr/bin/env python3
"""PREPARED, NOT EXECUTED: shared module request INFO through existing async site API.
Two ordinary owned hosts, one install/clone, failure then same-worker recovery.
Not the new synchronous browse SDK/host, SitesHelper/native parser, browser or UI.
Only exact 39123 control and fixed owned artifact/setup peer; no proxy rewrite.
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
origin = 'http://127.0.0.1:39123'
FIRST, SECOND = 'OwnedModuleOverride', 'OwnedModuleOverridelog'
NAMES = ['Owned module override', 'Owned module log second']
SITE_ID, SITE_NAME = 'owned-module-log-site', 'Owned module log site'
SITE_COOKIE, SITE_UA = 'owned-module-log-cookie=synthetic', 'OwnedModuleLog/1.0'
METHOD = 'async_refresh_torrents'
RESOURCES = '/api/sites/manage/' + SITE_ID + '/resources'
LOG_INFO = '/api/logs?module=plugins&level=INFO&limit=100'
LOG_ERROR = '/api/logs?module=plugins&level=ERROR&limit=100'
OBS = {'status': 'FAIL', 'kind': 'shared module INFO existing async site resource CLI',
       'calls': [], 'peer_calls': [], 'setup_root_calls': [], 'processes': [], 'queries': [],
       'not_claimed': ['new synchronous browse SDK or host', 'SitesHelper/native source parser',
                       'serial self-reentry', 'browser/UI', 'real tracker/download/media/NAS',
                       'full platform, CI or delivery']}


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
    check(INITIAL_ENV.get('S12_BROWSE_MODULE_LOG_CLI_GRANTED') == '1',
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
          ('POST', '/api/plugins/' + FIRST + '/clone'),
          ('PUT', '/api/plugins/' + FIRST), ('PUT', '/api/plugins/' + SECOND),
          ('POST', RESOURCES), ('GET', LOG_INFO), ('GET', LOG_ERROR)}
ROUTES.update(('GET', '/api/v1/plugin/' + pid + '/snapshot') for pid in (FIRST, SECOND))


def call(method, path, body=None, authenticated=True, expected=200):
    global cookie
    reserve()
    check((method, path) in ROUTES, 'outside fourteen exact owned method/path pairs')
    target = urllib.parse.urlsplit(origin + path)
    check(target.scheme == 'http' and target.netloc == '127.0.0.1:39123'
          and target.hostname == '127.0.0.1' and target.port == 39123
          and target.path == path.split('?', 1)[0]
          and target.query == (path.split('?', 1)[1] if path in (LOG_INFO, LOG_ERROR) else '')
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
            check(self.path in ('/', '/index', '/worker'), 'unexpected owned GET path/native request')
            check(self.headers.get('Host') == urllib.parse.urlsplit(peer_origin).netloc,
                  'owned peer Host differs')
            check(not self.headers.get('Authorization') and not self.headers.get('Proxy-Authorization')
                  and not self.headers.get('X-API-Key'), 'unexpected owned peer credential')
            check(not self.headers.get('Transfer-Encoding')
                  and int(self.headers.get('Content-Length', '0')) == 0, 'unexpected peer request body')
            check(self.headers.get('Cookie', '') == (SITE_COOKIE if self.path == '/' else ''),
                  'owned setup/artifact Cookie boundary differs')
            if self.path == '/':
                status, raw, kind = 404, b'404 page not found\n', 'text/plain; charset=utf-8'
            elif self.path == '/index':
                status, raw, kind = 200, json.dumps(index).encode(), 'application/json'
            else:
                status, raw, kind = 200, worker_bytes, 'application/octet-stream'
            row = {'at': now(), 'method': 'GET', 'path': self.path,
                   'scope': 'setup' if self.path == '/' else 'artifact',
                   'status': status, 'response_bytes': len(raw),
                   'response_sha256': hashlib.sha256(raw).hexdigest()}
            with lock:
                OBS['peer_calls'].append(row)
                if self.path == '/':
                    OBS['setup_root_calls'].append(row)
            self.reply(status, raw, kind)
            with lock:
                row['response_completed'] = True
            if self.path == '/':
                setup_root_seen.set()
        except BaseException as error:
            self.reject('GET ' + type(error).__name__ + ': ' + str(error))

    def do_CONNECT(self):
        self.reject('CONNECT is not an owned fixture operation')

    def do_POST(self):
        self.reject('POST is not an owned fixture operation')

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
    call('POST', '/api/auth/login', {'username': 'owned-module-log', 'password': password}, authenticated=False)
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


def snapshot(pid, ready=False):
    path = '/api/v1/plugin/' + pid + '/snapshot'
    deadline = time.monotonic() + 20
    while True:
        status, result = call('GET', path, expected=None if ready else 200)
        if status == 200:
            check(isinstance(result.get('calls'), dict) and isinstance(result.get('inputs'), dict),
                  'actual process snapshot missing')
            bind_worker(result.get('pid'))
            return result
        check(ready and status == 404 and time.monotonic() < deadline,
              'snapshot registration failed; no transport/authorization-denial retry')
        time.sleep(.05)  # Only exact same-route application 404 registration readiness.


def configure(pid, handler):
    _, response = call('PUT', '/api/plugins/' + pid,
                       {'enabled': True, 'modules': {METHOD: handler}, 'result': False})
    check(response.get('data', {}).get('success') is True, 'owned configuration business failure')


def logs(path):
    _, result = call('GET', path)
    page = result.get('data', {})
    rows = page.get('entries')
    check(isinstance(rows, list) and not page.get('gap') and not page.get('truncated')
          and not page.get('next_cursor'), 'finite persisted log page incomplete')
    check(all(isinstance(row.get('id'), str) and row['id'] and row.get('time') for row in rows),
          'actual persistent log identity absent')
    return rows


def log_delta(before, path, event, expected):
    ids = {row['id'] for row in before}
    deadline = time.monotonic() + 5
    while True:
        rows = logs(path)
        delta = [row for row in rows if row['id'] not in ids and row.get('event') == event]
        check(len(delta) <= expected, 'extra shared dispatcher event')
        if len(delta) == expected:
            # queryLogs reads persistent files in reverse order; not timestamp sorting.
            return list(reversed(delta)), rows
        check(time.monotonic() < deadline, 'persisted shared dispatcher event missing')
        time.sleep(.05)  # Only successful 200 log visibility polling; no rejected retry.


def check_site(result):
    rows = result.get('data', {}).get('sites')
    check(isinstance(rows, list) and len(rows) == 1, 'owned site projection differs')
    conf = rows[0].get('config', {})
    check(conf.get('id') == SITE_ID and conf.get('kind') == 'jiayuan'
          and conf.get('base_url') == peer_origin and conf.get('enabled') is False
          and conf.get('name') == SITE_NAME and rows[0].get('credential_configured') is True,
          'disabled owned site was not preserved')


def operation(label, fail=False):
    before = [snapshot(pid) for pid in (FIRST, SECOND)]
    check(before[0]['pid'] != before[1]['pid'], 'source and clone share a worker')
    before_info, before_error = logs(LOG_INFO), logs(LOG_ERROR)
    peer_before = peer_counts()
    _, response = call('POST', RESOURCES, {'query': '', 'page': 2})
    data = response.get('data', {})
    check(data.get('candidates') == [] and data.get('page') == 2 and data.get('has_more') is False,
          'false result is not an empty page-two business result')
    sites = data.get('sites')
    check(isinstance(sites, list) and len(sites) == 1 and sites[0].get('id') == SITE_ID
          and sites[0].get('status') == 'empty' and sites[0].get('count') == 0,
          'actual site business status differs')
    after = [snapshot(pid) for pid in (FIRST, SECOND)]
    for n in (0, 1):
        check(after[n]['pid'] == before[n]['pid'], 'business call replaced owned worker')
        previous, current = before[n]['calls'], after[n]['calls']
        for method in ('get_state', 'get_module'):
            check(current.get(method, 0) - previous.get(method, 0) == 1,
                  'discovery actual RPC count differs: ' + method)
        check(current.get('init', 0) == previous.get('init', 0), 'business call reinitialized worker')
        expected_execute = (0 if fail else 1) if n == 0 else (1 if fail else 0)
        check(current.get('module_execute', 0) - previous.get('module_execute', 0) == expected_execute,
              'actual module_execute count differs')
        check(current.get('module_fail', 0) - previous.get('module_fail', 0) == (1 if n == 0 and fail else 0),
              'actual failure RPC count differs')
    called = [(0, 'module_fail'), (1, 'module_execute')] if fail else [(0, 'module_execute')]
    for n, handler in called:
        value = after[n]['inputs'][handler]
        kwargs = value.get('kwargs', {})
        site = kwargs.get('site', {})
        check(value.get('args') == [] and set(kwargs) == {'site', 'keyword', 'cat', 'page'}
              and kwargs['keyword'] is None and kwargs['cat'] is None and kwargs['page'] == 1,
              'actual existing UI async argument/page projection differs')
        check(type(site.get('id')) is int and site['id'] > 0 and site.get('name') == SITE_NAME
              and site.get('url') == peer_origin and site.get('domain') == peer_origin
              and site.get('cookie') == SITE_COOKIE and site.get('ua') == SITE_UA
              and site.get('proxy') == 0 and site.get('is_active') is False,
              'actual stored site/private source argument differs')
    messages, all_info = log_delta(before_info, LOG_INFO, 'module_request', 2)
    expected_messages = ['请求插件 ' + name + ' 执行：' + METHOD + ' ...' for name in NAMES]
    check([row['message'] for row in messages] == expected_messages,
          'source INFO execution order/text differs')
    fresh_info = [row for row in all_info if row['id'] not in {v['id'] for v in before_info}]
    check(not any(row.get('event') == 'module_rate_limited' for row in fresh_info),
          'request INFO was confused with rate-limit event')
    errors, _ = log_delta(before_error, LOG_ERROR, 'module_execution_failed', 1 if fail else 0)
    check(peer_counts() == peer_before, 'business call attempted native HTTP or artifact fetch')
    row = {'label': label, 'host_pid': proc.pid, 'response': response,
           'before': before, 'after': after, 'request_logs': messages, 'failure_logs': errors,
           'actual_rpc_count': 2 if fail else 1, 'request_INFO_count': len(messages),
           'peer_counts_before': peer_before, 'peer_counts_after': peer_counts()}
    OBS['queries'].append(row)
    save()
    return row


preflight()
DATA = E / 'private-data'
password = secrets.token_urlsafe(30)
worker_bytes = WORKER.read_bytes()
try:
    completed = subprocess.run([str(WORKER), '--manifest'], capture_output=True, env=ENV, timeout=10)
    check(completed.returncode == 0, 'owned worker manifest failed')
    manifest = json.loads(completed.stdout)
    check(manifest.get('plugin_id') == FIRST and manifest.get('version') == '1.0'
          and {'get_module', 'module_execute', 'module_fail', 'http'}.issubset(manifest.get('methods', [])),
          'actual snapshot-enabled module worker required')
    check(manifest.get('apis') == [{'key': 'snapshot', 'path': '/snapshot', 'methods': ['GET'], 'auth': 'bear'}],
          'actual ldflags-enabled snapshot API differs')
    (E / 'worker-manifest.json').write_bytes(completed.stdout)
    peer = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    peer.daemon_threads = True
    peer_origin = 'http://127.0.0.1:' + str(peer.server_address[1])
    index.update({'schema_version': 1, 'plugin_id': FIRST, 'current_version': '1.0',
                  'releases': [{'version': '1.0', 'manifest': manifest, 'artifacts': [{
                      'platform': 'linux-amd64', 'url': peer_origin + '/worker',
                      'sha256': args.expected_worker_sha, 'executable_sha256': args.expected_worker_sha,
                      'size': len(worker_bytes), 'format': 'executable',
                      'host_contract': 'light-media.sdk-base/v1'}]}]})
    thread = threading.Thread(target=peer.serve_forever, daemon=True)
    thread.start()
    bootstrap = {'username': 'owned-module-log', 'password': password, 'settings': {
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
    _, saved = call('POST', '/api/sites/manage',
                    {'id': SITE_ID, 'kind': 'jiayuan', 'name': SITE_NAME, 'base_url': peer_origin,
                     'cookie': SITE_COOKIE, 'ua': SITE_UA, 'enabled': False, 'proxy': False})
    check_site(saved)
    wait_setup_root()
    _, installed = call('POST', '/api/plugins/install', {'plugin_id': FIRST, 'repo_url': peer_origin + '/index'})
    check(installed.get('data', {}).get('success') is True, 'owned install business failure')
    configure(FIRST, 'module_execute')
    first_snapshot = snapshot(FIRST, ready=True)
    _, cloned = call('POST', '/api/plugins/' + FIRST + '/clone', {'suffix': 'Log', 'name': NAMES[1]})
    check(cloned.get('data', {}).get('success') is True, 'owned clone business failure')
    configure(SECOND, 'module_execute')
    snapshot(SECOND, ready=True)
    artifact_counts = {'GET /index': 1, 'GET /worker': 1}
    check(peer_counts() == artifact_counts, 'unexpected startup artifact/native request')
    first = operation('false_stops_second_RPC_but_logs_second_request')
    configure(FIRST, 'module_fail')
    failure = operation('ordinary_failure_then_second_false_prevents_native', fail=True)
    check([s['pid'] for s in failure['before']] == [s['pid'] for s in first['after']],
          'configuring owned failure replaced warm workers')
    configure(FIRST, 'module_execute')
    recovered = operation('same_worker_recovery_after_real_module_error')
    warm_workers = [s['pid'] for s in recovered['after']]
    check(warm_workers == [s['pid'] for s in first['after']], 'recovery replaced warm workers')
    persisted = {row['id']: row for query in OBS['queries']
                 for row in query['request_logs'] + query['failure_logs']}
    first_host = proc.pid
    stop()
    start()
    check(proc.pid != first_host, 'cold normal host reused process')
    cold_snapshots = [snapshot(pid, ready=True) for pid in (FIRST, SECOND)]
    check(all(row['pid'] not in warm_workers for row in cold_snapshots), 'cold worker was not fresh')
    _, cold_site = call('GET', '/api/sites/manage')
    check_site(cold_site)
    cold_records = {row['id']: row for row in logs(LOG_INFO) + logs(LOG_ERROR)}
    check(all(cold_records.get(key) == value for key, value in persisted.items()),
          'actual persisted INFO/failure records changed or disappeared after cold restart')
    check(peer_counts() == artifact_counts, 'cold startup reacquired artifact or invoked native source')
    cold = operation('cold_normal_host_same_configuration_and_shared_log_order')
    check([s['pid'] for s in cold['before']] == [s['pid'] for s in cold_snapshots],
          'cold business call replaced newly installed workers')
    stop()
    check_setup_roots()
    check(sum(c['method'] == 'POST' and c['path'] == '/api/plugins/install' for c in OBS['calls']) == 1,
          'more than one installation')
    check(sum(c['method'] == 'POST' and c['path'].endswith('/clone') for c in OBS['calls']) == 1,
          'more than one clone')
    check(peer_counts() == artifact_counts, 'unexpected artifact/native request at shutdown')
    OBS['persisted_warm_log_ids'] = list(persisted)
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
