"""Owned real production process/HTTP/SDK/SQLite lifecycle witness; not browser UI."""
from pathlib import Path
import hashlib
import http.cookiejar
import http.server
import json
import os
import secrets
import signal
import sqlite3
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

OUT=Path(os.environ['OWNED_RESET_OUT']).resolve()
OUT.mkdir()
O=OUT
SERVER=Path(os.environ['OWNED_RESET_SERVER']).resolve()
WORKERS=[Path(os.environ[k]).resolve() for k in ('OWNED_RESET_MUTATOR','OWNED_RESET_OBSERVER')]
sha=lambda b:hashlib.sha256(b).hexdigest()
binaries={str(p):sha(p.read_bytes()) for p in [SERVER]+WORKERS}
runtime_parent = O / 'private-runtime'
runtime_parent.mkdir()
temp = tempfile.TemporaryDirectory(prefix='owned-uninstall-', dir=runtime_parent)
root = Path(temp.name)
runtime = root / 'app'
home = root / 'home'
home.mkdir()
username, password, token = 'owned-row17', secrets.token_hex(24), secrets.token_hex(32)
env = {'PATH': os.environ['PATH'], 'HOME': str(home), 'TMPDIR': str(root), 'TZ': 'UTC', 'LANG': 'C.UTF-8'}
packages={}
for worker in WORKERS:
 payload=worker.read_bytes();manifest=json.loads(subprocess.check_output([str(worker),'--manifest'],env=env));packages[manifest['plugin_id']]={'manifest':manifest,'payload':payload}
class Repository(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  parts=self.path.strip('/').split('/')
  if len(parts)!=2 or parts[0] not in packages or parts[1] not in ('index','worker'):
   self.send_error(404);return
  package=packages[parts[0]];payload=package['payload'];manifest=package['manifest'];digest=sha(payload)
  body=payload if parts[1]=='worker' else json.dumps({'schema_version':1,'plugin_id':manifest['plugin_id'],'current_version':manifest['version'],'releases':[{'version':manifest['version'],'manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','url':repository_url+'/'+parts[0]+'/worker','sha256':digest,'executable_sha256':digest,'size':len(payload),'host_contract':'light-media.sdk-base/v1'}]}]}).encode()
  self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*args):pass
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
        if parsed.scheme != 'http' or parsed.netloc != expected.netloc or parsed.path not in tuple('/'+pid+'/'+kind for pid in packages for kind in ('worker','index')):
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
def config(instance,case,enabled=True):
 value={'enabled':enabled,'trace_path':str(root/'trace.jsonl'),'label':instance,'mode':'error','payload':'null','name_case':case,'name_value':'ordinary source name','seed_data':True}
 success('PUT','/api/plugins/'+instance,value)
 return value

def traces():
 p=root/'trace.jsonl'
 return [json.loads(line) for line in p.read_text().splitlines()] if p.exists() else []

try:
 start()
 controls=call('GET','/api/plugins/markets')['data']
 assert controls['plugin_statistic_share'] is False and controls['plugin_server_url']==''
 source,observer,clone='OwnedResetMutator','OwnedResetObserver','OwnedResetMutatortwin'
 for pid in (source,observer):success('POST','/api/plugins/install',{'plugin_id':pid,'repo_url':repository_url+'/'+pid+'/index'})
 config(observer,'observer')
 expected=config(source,'missing')
 success('POST','/api/plugins/'+source+'/clone',{'suffix':'Twin'})
 config(clone,'string',False)
 before=snapshot(source);other=snapshot(observer);twin=snapshot(clone)
 assert before['config']==expected and before['data']=='retained-data'
 (root/'trace.jsonl').write_text('')
 call('POST','/api/plugins/'+source+'/reset',{},expected=400)
 after=snapshot(source)
 assert after==before and snapshot(observer)==other and snapshot(clone)==twin
 assert [r['method'] for r in traces()]==['reset_first','reset_second']
 observations.append({'case':'missing-name','mutation_callback_sequence':True,'same_worker_config_KV':True,'observer_clone_unchanged':True})
 stop();start()
 cold=snapshot(source)
 assert cold['pid']!=before['pid'] and cold['config']==expected and cold['data']=='retained-data'
 (root/'trace.jsonl').write_text('')
 call('POST','/api/plugins/'+source+'/reset',{},expected=400)
 assert snapshot(source)==cold
 observations.append({'case':'missing-name-cold','same_persisted_config_KV':True,'fresh_worker':True,'failure_preserved':True})
 # The named source path resumes ordinary error reporting/continuation; a real
 # second-phase SQL failure must still preserve committed config deletion.
 config(source,'string')
 with sqlite3.connect(runtime/'light-media.db',timeout=10) as db:
  db.execute("CREATE TRIGGER owned_reset_data_fail BEFORE DELETE ON plugin_values WHEN OLD.instance_id='OwnedResetMutator' AND OLD.kind='data' BEGIN SELECT RAISE(ABORT,'owned reset second phase failure'); END")
 call('POST','/api/plugins/'+source+'/reset',{},expected=500)
 success('POST','/api/plugins/'+source+'/reload',{})
 partial=snapshot(source)
 assert partial['config'] is None and partial['data']=='retained-data'
 with sqlite3.connect(runtime/'light-media.db',timeout=10) as db:db.execute('DROP TRIGGER owned_reset_data_fail')
 observations.append({'case':'named-sql-second-phase-failure','config_committed_deleted':True,'KV_retained':True})
 stop();start()
 partial_cold=snapshot(source)
 assert partial_cold['config'] is None and partial_cold['data']=='retained-data'
 config(source,'string')
 old=snapshot(source);clone_before=snapshot(clone)
 success('POST','/api/plugins/'+source+'/reset',{})
 clear=snapshot(source)
 assert clear['pid']!=old['pid'] and clear['config'] is None and clear['data'] is None
 assert snapshot(clone)==clone_before
 observations.append({'case':'named-success','reset_only_route_target':True,'fresh_worker':True,'config_KV_deleted':True})
 stop();start()
 clear_cold=snapshot(source)
 assert clear_cold['config'] is None and clear_cold['data'] is None
 assert snapshot(clone)['config']['enabled'] is False and snapshot(clone)['data']=='retained-data'
 observations.append({'case':'success-cold','config_KV_absence_retained':True,'clone_retained':True})
 stop();server_log.flush()
 assert rejected==[],rejected
 assert not any(secret.encode() in (O/'server.log').read_bytes() for secret in (password,token))
 result={'scope':__doc__,'binaries':binaries,'driver_sha256':sha(Path(__file__).read_bytes()),'requests':requests,'observations':observations,'lifecycle':lifecycle,'network':{'rejected':rejected,'forwarded':forwarded,'stats':False,'server_url':''},'owned_services_stopped':True,'secret_log_scan':'absent'}
 (O/'result.json').write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'observations':len(observations),'http_requests':len(requests),'lifecycle':lifecycle}))
finally:
 stop();proxy.shutdown();proxy.server_close();proxy_thread.join(timeout=10);repository.shutdown();repository.server_close();thread.join(timeout=10);server_log.close()
