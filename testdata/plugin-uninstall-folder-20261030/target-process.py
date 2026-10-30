"""Owned real production process/HTTP/SDK/SQLite lifecycle witness; not browser UI."""
from pathlib import Path
import hashlib
import http.cookiejar
import http.server
import json
import os
import secrets
import signal
import sys
import sqlite3
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

OUT = Path(os.environ['OWNED_FOLDER_EVIDENCE']).resolve()
SERVER, WORKER = Path(os.environ['OWNED_FOLDER_SERVER']), Path(os.environ['OWNED_FOLDER_WORKER'])
binding = json.loads(Path(os.environ['OWNED_FOLDER_BINDING']).read_text())
sha = lambda b: hashlib.sha256(b).hexdigest()
for path in (SERVER, WORKER):
    assert sha(path.read_bytes()) == binding['binaries'][str(path)]
case = json.loads(Path(os.environ['OWNED_FOLDER_ORACLE']).read_text())['cases'][int(sys.argv[1])]
O = Path(os.environ['OWNED_FOLDER_OUT']).resolve()
assert O.is_relative_to(OUT.resolve())
O.mkdir()
runtime_parent = O / 'private-runtime'
runtime_parent.mkdir()
temp = tempfile.TemporaryDirectory(prefix='owned-uninstall-', dir=runtime_parent)
root = Path(temp.name)
runtime = root / 'app'
home = root / 'home'
home.mkdir()
username, password, token = 'owned-row17', secrets.token_hex(24), secrets.token_hex(32)
env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'TMPDIR': str(root), 'TZ': 'UTC', 'LANG': 'C.UTF-8'}
payload = WORKER.read_bytes()
manifest = json.loads(subprocess.check_output([str(WORKER), '--manifest'], env=env))
assert manifest['plugin_id'] == 'OwnedBaseFixture'
class Repository(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/worker':
            body = payload
        elif self.path == '/index':
            digest = sha(payload)
            body = json.dumps({'schema_version': 1, 'plugin_id': manifest['plugin_id'], 'current_version': manifest['version'],
                               'releases': [{'version': manifest['version'], 'manifest': manifest,
                                             'artifacts': [{'platform': 'linux-amd64', 'format': 'executable',
                                                            'url': repository_url + '/worker', 'sha256': digest,
                                                            'executable_sha256': digest, 'size': len(payload),
                                                            'host_contract': 'light-media.sdk-base/v1'}]}]}).encode()
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args): pass
repository = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Repository)
repository_url = 'http://127.0.0.1:' + str(repository.server_port)
thread = threading.Thread(target=repository.serve_forever, daemon=True)
thread.start()
# QA-only egress boundary: no direct non-loopback target is authorized.
# Go net/http deliberately bypasses proxy for literal loopback; the only
# permitted proxy forward is our exact repository origin, never arbitrary local.
rejected, forwarded = [], []
class OwnedOnlyProxy(http.server.BaseHTTPRequestHandler):
    def do_CONNECT(self):
        rejected.append({'method':'CONNECT', 'authority':self.path})
        self.send_error(403, 'owned-only QA egress')
    def do_GET(self):
        parsed = urllib.parse.urlsplit(self.path)
        expected = urllib.parse.urlsplit(repository_url)
        if parsed.scheme != 'http' or parsed.netloc != expected.netloc or parsed.path not in ('/worker','/index'):
            rejected.append({'method':'GET', 'scheme':parsed.scheme, 'authority':parsed.netloc})
            self.send_error(403, 'owned-only QA egress')
            return
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(self.path, timeout=10) as response:
            body = response.read()
            self.send_response(response.status)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        forwarded.append({'method':'GET','path':parsed.path})
    def log_message(self,*args): pass
proxy = http.server.ThreadingHTTPServer(('127.0.0.1',0),OwnedOnlyProxy)
proxy_thread=threading.Thread(target=proxy.serve_forever,daemon=True)
proxy_thread.start()
proxy_url='http://127.0.0.1:'+str(proxy.server_port)
env.update({'HTTP_PROXY':proxy_url,'HTTPS_PROXY':proxy_url,'ALL_PROXY':proxy_url,
            'http_proxy':proxy_url,'https_proxy':proxy_url,'all_proxy':proxy_url,
            'NO_PROXY':'127.0.0.1,localhost','no_proxy':'127.0.0.1,localhost'})
bootstrap = {'username': username, 'password': password,
             'settings': {'api_token': token, 'plugin_markets': [],
                          'plugin_statistic_share': False, 'plugin_server_url': ''}}
boot = subprocess.run([str(SERVER), '--data-dir', str(runtime), '--bootstrap-stdin'],
                      input=json.dumps(bootstrap).encode(), capture_output=True, env=env, timeout=30)
assert boot.returncode == 0 and b'bootstrap_complete' in boot.stdout, 'owned bootstrap failed; payload withheld'
listener = socket.socket()
listener.bind(('127.0.0.1', 0))
port = listener.getsockname()[1]
listener.close()
origin = 'http://127.0.0.1:' + str(port)
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
process = None
server_log = (O / 'server.log').open('wb')
observations, requests, lifecycle = [], [], []
def call(method, path, body=None, expected=200, record=True):
    raw = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(origin + path, data=raw, method=method,
                                     headers={'Content-Type': 'application/json', 'X-API-KEY': token})
    try:
        with client.open(request, timeout=30) as response:
            code, value = response.status, response.read()
    except urllib.error.HTTPError as error:
        code, value = error.code, error.read()
    assert code == expected, (method, path, code, expected)
    if record:
        requests.append({'method': method, 'path': path, 'status': code})
    return json.loads(value) if value else None
def success(method, path, value=None):
    result = call(method, path, value)
    assert result.get('data', result).get('success') is True, (method, path, 'management failed')
    return result
def start():
    global process
    process = subprocess.Popen([str(SERVER), '--listen', '127.0.0.1:' + str(port), '--data-dir', str(runtime)],
                               env=env, stdout=server_log, stderr=server_log)
    deadline = time.monotonic() + 20
    while True:
        try:
            call('GET', '/api/auth/status', record=False)
            break
        except (OSError, urllib.error.URLError):
            assert time.monotonic() < deadline and process.poll() is None, 'owned server unavailable'
            time.sleep(.05)
    call('POST', '/api/auth/login', {'username': username, 'password': password}, record=False)
    lifecycle.append({'start_pid': process.pid})
def stop():
    global process
    if process is None: return
    pid = process.pid
    process.send_signal(signal.SIGTERM)
    try:
        code = process.wait(timeout=20)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise AssertionError('owned server graceful stop timed out')
    process = None
    lifecycle.append({'stop_pid': pid, 'exit': code})
    assert code == 0
def snapshot(instance, target=None):
    query = '' if target is None else '?target=' + urllib.parse.quote(target)
    return call('GET', '/api/v1/plugin/' + instance + '/snapshot' + query)
def retained_file(snapshot_value, name, value):
    parent = Path(snapshot_value['path']).resolve()
    assert parent.is_relative_to(runtime.resolve()) and not parent.is_symlink()
    f = parent / name
    f.write_text(value)
    return f
def safe_value(value):
    return json.loads(json.dumps(value).replace(str(root), '<owned-runtime>'))

try:
    start()
    controls=call('GET','/api/plugins/markets')['data']
    assert controls['plugin_statistic_share'] is False and controls['plugin_server_url']==''
    source, clone = 'OwnedBaseFixture', 'OwnedBaseFixturetwin'
    success('POST', '/api/plugins/install', {'plugin_id': source, 'repo_url': repository_url + '/index'})
    success('PUT', '/api/plugins/' + source, {'marker': 'ordinary-retained', 'owned_plan': {'key': 'shared', 'value': 'source-data'}})
    before = snapshot(source)
    source_file = retained_file(before, 'source-retained.txt', 'owned source file')
    success('POST', '/api/plugins/' + source + '/clone', {'suffix': 'Twin'})
    success('PUT', '/api/plugins/' + clone, {'marker': 'clone-private', 'owned_plan': {'key': 'shared', 'value': 'clone-data'}})
    clone_before = snapshot(clone)
    clone_file = retained_file(clone_before, 'clone-retained.txt', 'owned clone file')
    source_code = runtime / 'plugin-packages' / source
    clone_code = runtime / 'plugin-packages' / clone
    assert source_code.is_dir() and clone_code.is_dir()
    assert case['input']['source']==source and case['input']['clone']==clone
    folders=case['input']['folders']
    saved=call('PUT','/api/plugins/organization',{'folders':folders})['data']
    assert saved['folders']==folders
    # Own DB only: create/drop a specific failure trigger. No account, settings,
    # sealed data or key material is read; ordinary public GET observes state.
    with sqlite3.connect(runtime/'light-media.db',timeout=10) as db:
        db.execute("CREATE TRIGGER owned_folder_fail BEFORE UPDATE ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned folder write failure'); END")
    success('DELETE','/api/plugins/'+source)
    warm=call('GET','/api/plugins/organization')['data']
    assert warm['folders']==case['warm_after_failure']
    call('GET','/api/v1/plugin/'+source+'/snapshot',expected=404)
    retained=snapshot(clone,source)
    assert retained['config']['marker']=='ordinary-retained' and retained['value']=='source-data'
    assert snapshot(clone)['init_count']==clone_before['init_count']
    with sqlite3.connect(runtime/'light-media.db',timeout=10) as db:
        db.execute('DROP TRIGGER owned_folder_fail')
    success('DELETE','/api/plugins/'+source)
    retry=call('GET','/api/plugins/organization')['data']
    assert retry['folders']==case['warm_after_retry']
    assert snapshot(clone)['init_count']==clone_before['init_count']
    observations.append({'operation':case['name'],
      'input':{'source':source,'clone':clone,'folders':folders},'first_success':True,
      'public_after_failure':warm,'retry_success':True,'public_after_retry':retry,
      'source_stopped':True,'source_config_KV_retained':True,'clone_init_count_unchanged':True,
      'public_read_keeps_source_warm_semantics':True,'no_sealed_SQL_or_secret_values_read':True})
    assert source_file.read_text()=='owned source file' and clone_file.read_text()=='owned clone file'
    assert source_code.is_dir() and clone_code.is_dir()
    # A real server+worker cold start, not a reset of an in-process map.
    stop()
    start()
    cold=call('GET','/api/plugins/organization')['data']
    assert cold['folders']==case['cold']
    cold_clone=snapshot(clone)
    retained=snapshot(clone,source)
    assert retained['config']['marker']=='ordinary-retained' and retained['value']=='source-data'
    assert cold_clone['config']['marker']=='clone-private' and cold_clone['value']=='clone-data'
    assert source_file.read_text()=='owned source file' and clone_file.read_text()=='owned clone file'
    observations.append({'operation':'actual server/worker cold recovery','folders':cold['folders'],'ordinary_config_KV_files_code_retained':True,'clone_config_KV_files_code_retained':True})
    stop()
    server_log.flush()
    log_bytes = (O / 'server.log').read_bytes()
    assert not any(secret.encode() in log_bytes for secret in (password, token))
    assert rejected == [], ('unexpected attempted external request',rejected)
    result = {'network':{'bootstrap_plugin_statistic_share':False,'bootstrap_plugin_server_url':'','public_settings_readback':True,'owned_only_proxy':True,'rejected':rejected,'forwarded':forwarded}, 'scope': __doc__, 'target_revision': binding['revision'], 'binaries': binding['binaries'],
              'input_binding_sha256': sha(Path(os.environ['OWNED_FOLDER_BINDING']).read_bytes()),
              'driver_sha256': sha(Path(__file__).read_bytes()), 'requests': requests,
              'observations': observations, 'lifecycle': lifecycle, 'all_owned_services_stopped': True,
              'secret_value_scan': 'password/token absent from retained server log; no runtime files included'}
    (O / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'observations': len(observations), 'http_requests': len(requests), 'lifecycle': lifecycle}))
finally:
    stop()
    proxy.shutdown()
    proxy.server_close()
    proxy_thread.join(timeout=10)
    assert not proxy_thread.is_alive()
    repository.shutdown()
    repository.server_close()
    thread.join(timeout=10)
    server_log.close()
