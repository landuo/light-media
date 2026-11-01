#!/usr/bin/env python3
"""Prepared only: two exact39123 normal hosts, real A HTTPS acquisition.
No public repository/upstream forwarding/CONNECT fixture or proxy rewrite.
B is not exercised and its prior timeout remains separate.
"""
import argparse, datetime, hashlib, http.client, http.cookies, http.server
import json, os, re, secrets, shutil, signal, ssl, subprocess, threading, time
import urllib.parse
from pathlib import Path
P = argparse.ArgumentParser(description=__doc__)
for name in ('server','worker','output','expected-server-sha','expected-worker-sha','expected-driver-sha','expected-bindings-sha'):
    P.add_argument('--'+name, required=True)
args=P.parse_args(); AUDIT=Path(__file__).resolve().parent; ROOT=AUDIT.parents[1]
BINDINGS=AUDIT/'execution-bindings.json'
SERVER,WORKER,E=(Path(p).resolve() for p in (args.server,args.worker,args.output))
BASE='90d214fc5ca861d327e71327488aa77ee7011e2b'
CANDIDATE=ROOT/'target-drafts/platform-requestutils-a-only01/candidate01.json'
CANDIDATE_SHA='90f3158450af416597c216d9dfe5da0d87b8abfde1d31705c67719cc8b17de8c'
TESTCERT=ROOT.parent/'plugins-cloud-20261028/toolchain/go/src/net/http/internal/testcert/testcert.go'
INITIAL_ENV=os.environ.copy(); ENV=INITIAL_ENV.copy()
peer=thread=proc=log=cookie=None
lock=threading.Lock(); index={}; owned_workers={}; process_workers={}
peer_origin=''; origin='http://127.0.0.1:39123'; PLUGIN='OwnedStandardFixture'
API='/api/v1/plugin/'+PLUGIN+'/echo'
MARKET='/api/plugins/market?details=true'
FORCE_MARKET=MARKET+'&force=true'
HISTORY='/api/plugins/history/'+PLUGIN+'?force=true'
phase='warm-startup'; phase_offset=0
PHASES={'warm-startup':['/index'],'market-force':['/index'],
        'install-failure':['/index','/worker'],'install-recovery':['/index','/worker'],
        'compatibility':['/index'],'same-version-force':['/index','/worker'],
        'history':['/index'],'release':['/index'],'cold-startup':['/index'],'idle':[]}
OBS={'status':'FAIL','kind':'A private TLS actual ordinary normal-server install/recovery/cold',
     'calls':[],'peer_calls':[],'processes':[],'queries':[],
     'not_claimed':['B success','real/public repository or telemetry','UI/browser','distinct-version update',
                    'all parent completion','whole source host/network audit','push/CI']}
ROUTES={('GET','/healthz'),('POST','/api/auth/login'),('GET','/api/plugins/markets'),
        ('POST','/api/plugins/install'),('GET',API),('PUT','/api/plugins/'+PLUGIN),
        ('GET',MARKET),('GET',FORCE_MARKET),('GET',HISTORY)}

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
    check(INITIAL_ENV.get('PLATFORM_A_TLS_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER == ROOT / 'runtime/platform-requestutils-a-only-server01'
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
    check(rows.get(str(TESTCERT)) == sha(TESTCERT), 'public test certificate source not bound')
    check(WORKER == ROOT / 'runtime/s10-integrated-normal01.worker5'
          and args.expected_worker_sha == '60403e493ba75c73f24ec7786058d2b93cc2ec7b3cd521680a84c15b0c240808',
          'exact historical normal owned worker required')
    clean = binding.get('clean_view_proof', {})
    check(type(clean.get('path')) is str and type(clean.get('sha256')) is str
          and rows.get(clean['path']) == clean['sha256'], 'reviewed clean-view proof not bound')
    for item in binding['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    check(E.parent == ROOT / 'test-instances' and E.name == 'platform-requestutils-a-only01'
          and not E.exists(), 'exclusive new A test-instance required')
    for name in ('PLUGIN_MARKET', 'LIGHT_MEDIA_MEDIA_ROOTS'):
        check(not ENV.get(name), 'inherited endpoint/mount conflicts; no rewrite')
    for name in ('DEV', 'LIGHT_MEDIA_SAFE_MODE', 'MOVIEPILOT_SAFE_MODE'):
        check(name not in ENV or ENV[name].lower() in ('0', 'false', 'f', 'no', 'n', 'off'),
              'inherited startup mode conflicts; no rewrite')
    no_proxy = ENV.get('no_proxy') or ENV.get('NO_PROXY') or ''
    check('127.0.0.1' in [x.strip() for x in no_proxy.split(',')], 'existing exact NO_PROXY missing; no alternate route')
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


def call(method, path, body=None, authenticated=True, expected=200):
    global cookie
    reserve()
    check((method, path) in ROUTES, 'outside exact owned method/path/query pairs')
    target = urllib.parse.urlsplit(origin + path)
    check(target.scheme == 'http' and target.netloc == '127.0.0.1:39123'
          and target.hostname == '127.0.0.1' and target.port == 39123
          and target.path == path.split('?', 1)[0]
          and not target.fragment
          and target.username is None and target.password is None,
          'exact owned controller origin required')
    check(exact_owned_listener() is not None, 'exact listener not held by owned host; no request')
    headers = {'Content-Type': 'application/json'}
    if authenticated and path not in ('/healthz', '/api/auth/login'):
        check(cookie is not None, 'owned login cookie missing')
        headers['Cookie'] = cookie
    if path == API:
        headers['X-API-KEY'] = api_token
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
            check(not any(v.encode() in raw for v in (password, api_token)), 'private auth value in ordinary response')
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
    call('POST', '/api/auth/login', {'username': 'owned-a', 'password': password}, authenticated=False)
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



def set_phase(name):
    global phase, phase_offset
    finish_phase()
    with lock:
        check(name in PHASES, 'unapproved owned phase')
        phase, phase_offset=name,0


def finish_phase():
    deadline=time.monotonic()+10
    while True:
        with lock:
            check(not OBS.get('peer_errors'), 'fixed peer rejected request')
            completed=phase_offset==len(PHASES[phase]) and all(
                r.get('response_completed') for r in OBS['peer_calls'] if r['phase']==phase)
        if completed:return
        check(time.monotonic()<deadline, 'owned peer phase did not finish')
        time.sleep(.01)  # Owned peer completion only, not HTTP retry or alternate route.


class Peer(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*unused):pass
    def reply(self,status,raw,kind='application/json'):
        self.send_response(status); self.send_header('Content-Type',kind)
        self.send_header('Content-Length',str(len(raw))); self.send_header('Connection','close')
        self.end_headers(); self.wfile.write(raw); self.close_connection=True
    def reject(self,reason):
        with lock:OBS.setdefault('peer_errors',[]).append(reason)
        self.reply(403,b'{}')
    def do_GET(self):
        global phase_offset
        try:
            target=urllib.parse.urlsplit(self.path)
            query=urllib.parse.parse_qs(target.query,keep_blank_values=True)
            check(not target.scheme and not target.netloc and not target.fragment
                  and target.path in ('/index','/worker'), 'only fixed origin GET admitted')
            check(not query or (target.path=='/index' and set(query)=={'_refresh'}
                  and len(query['_refresh'])==1 and query['_refresh'][0].isdigit()),'unknown query')
            check(self.headers.get('Host')==urllib.parse.urlsplit(peer_origin).netloc,
                  'fixed owned TLS Host required')
            check(not any(self.headers.get(k) for k in ('Authorization','Proxy-Authorization','Cookie','X-API-Key')),
                  'credential reached artifact peer')
            check(not self.headers.get('Transfer-Encoding') and self.headers.get('Content-Length','0')=='0','GET entity rejected')
            self.connection.settimeout(20)
            with lock:
                name=phase; expected=PHASES[name]
                check(phase_offset<len(expected) and expected[phase_offset]==target.path,'unexpected request/replay')
                phase_offset+=1
                row={'method':'GET','path':target.path,'phase':name,'ordinal':phase_offset,'query_keys':list(query)}
                OBS['peer_calls'].append(row)
            status=503 if target.path=='/worker' and name=='install-failure' else 200
            raw=(b'owned-artifact-failure-private-marker' if status==503 else
                 worker_bytes if target.path=='/worker' else json.dumps(index).encode())
            row.update(status=status,response_bytes=len(raw),response_sha256=hashlib.sha256(raw).hexdigest())
            self.reply(status,raw,'application/octet-stream' if target.path=='/worker' else 'application/json')
            with lock:row['response_completed']=True
        except BaseException as error:self.reject('GET: '+type(error).__name__)
    def do_CONNECT(self):self.reject('CONNECT forbidden')
    def do_POST(self):self.reject('POST forbidden')
    def do_PUT(self):self.reject('PUT forbidden')
    def do_DELETE(self):self.reject('DELETE forbidden')
    def do_HEAD(self):self.reject('HEAD forbidden')


def make_tls_peer():
    # Public Go test-only localhost certificate/key, exactly the httptest
    # fixture material. Never a real certificate or account key. No key export.
    text=TESTCERT.read_text()
    certs=re.findall(r'var LocalhostCert = \[\]byte\(`(.*?)`\)',text,re.S)
    keys=re.findall(r'var LocalhostKey = \[\]byte\(testingKey\(`(.*?)`\)\)',text,re.S)
    check(len(certs)==1 and len(keys)==1,'fixed public Go testcert declarations changed')
    cert=certs[0].encode()+b'\n'
    # The public encoded key is PKCS8. Give OpenSSL its correct PEM envelope;
    # only the TESTING label changes, not the public fixture DER/key bytes.
    key=keys[0].replace('RSA TESTING KEY','PRIVATE KEY').encode()+b'\n'
    directory=DATA/'owned-test-tls'; directory.mkdir(parents=True,mode=0o700,exist_ok=False)
    for name,data in [('certificate.pem',cert),('key.pem',key)]:
        fd=os.open(directory/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'wb') as f:f.write(data)
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(directory/'certificate.pem'),str(directory/'key.pem'))
    server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer)  # AF_INET only; no fallback.
    try:server.socket=context.wrap_socket(server.socket,server_side=True)
    except BaseException:server.server_close();raise
    server.daemon_threads=True
    OBS['public_testcert_source_sha256']=sha(TESTCERT)
    OBS['private_test_key_not_for_evidence_export']=True
    return server


def success(result):
    check(result.get('data',{}).get('success') is True,'ordinary business result not success')


def install(name,force=False,ok=True):
    set_phase(name)
    _,result=call('POST','/api/plugins/install',{'plugin_id':PLUGIN,'repo_url':peer_origin+'/index','force':force})
    check(result.get('data',{}).get('success') is ok,'ordinary install outcome differs')
    check('owned-artifact-failure-private-marker' not in json.dumps(result),'artifact body leaked')
    finish_phase()
    OBS['queries'].append({'label':name,'response':result,'peer_counts':peer_counts()})
    save()


def snapshot(name,old_pid=None):
    deadline=time.monotonic()+10
    while True:
        status,result=call('GET',API,expected=None)
        if status==200:break
        check(status==404 and time.monotonic()<deadline,'installed API not registered; no denial retry')
        time.sleep(.05)
    bind_worker(result.get('nonce'))
    check(result.get('version')=='1.0' and result.get('stale_denied') is True,'actual standard worker state differs')
    if old_pid is not None:check(result['nonce']!=old_pid,'actual worker was not replaced')
    OBS.setdefault('worker_snapshots',[]).append({'label':name,'response':result})
    save();return result


preflight()
DATA=E/'private-data'
password=secrets.token_urlsafe(30); api_token=secrets.token_urlsafe(32)
worker_bytes=WORKER.read_bytes()
try:
    completed=subprocess.run([str(WORKER),'--manifest'],capture_output=True,env=ENV,timeout=10)
    check(completed.returncode==0,'owned worker manifest failed')
    manifest=json.loads(completed.stdout)
    check(manifest.get('plugin_id')==PLUGIN and manifest.get('version')=='1.0'
          and manifest.get('runtime')=='go-executable'
          and manifest.get('apis')==[{'key':'echo','path':'/echo','methods':['GET'],'auth':'apikey'}],
          'bound standard worker manifest differs')
    (E/'worker-manifest.json').write_bytes(completed.stdout)
    peer=make_tls_peer(); peer_origin='https://127.0.0.1:'+str(peer.server_address[1]); OBS['owned_peer_origin']=peer_origin
    RELEASE='/api/plugins/releases/'+PLUGIN+'?repo_url='+urllib.parse.quote(peer_origin+'/index',safe='')
    ROUTES.add(('GET',RELEASE))
    index.update({'schema_version':1,'plugin_id':PLUGIN,'current_version':'1.0','history':{'1.0':'owned actual release'},
                  'releases':[{'version':'1.0','manifest':manifest,'artifacts':[{
                    'platform':'linux-amd64','url':peer_origin+'/worker','sha256':args.expected_worker_sha,
                    'executable_sha256':args.expected_worker_sha,'size':len(worker_bytes),'format':'executable',
                    'host_contract':'light-media.sdk-base/v1'}]}]})
    thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start()
    bootstrap={'username':'owned-a','password':password,'settings':{
        'api_token':api_token,'plugin_markets':[peer_origin+'/index'],'plugin_local_repo_paths':[],
        'plugin_auto_reload':False,'plugin_statistic_share':False,'plugin_server_url':'',
        'site_data_refresh_hours':0,'read_site_messages':False,'sites':[],'rss_sites':[],
        'directories':[],'allowed_roots':[],'roots':[],'downloaders':[]}}
    boot=subprocess.run([str(SERVER),'--data-dir',str(DATA),'--bootstrap-stdin'],
                        input=json.dumps(bootstrap).encode(),capture_output=True,env=ENV,timeout=30)
    del bootstrap
    OBS['bootstrap_exit']=boot.returncode
    check(boot.returncode==0 and b'bootstrap_complete' in boot.stdout,'owned bootstrap failed; output withheld')
    start();finish_phase()
    # This ordinary read joins any initial source read to completion, then
    # force is a separate real request; no synthetic clock or source skip.
    _,first_market=call('GET',MARKET)
    check(any(x.get('plugin_id')==PLUGIN for x in first_market.get('data',{}).get('items',[])), 'actual market card missing')
    check(peer_counts()=={'GET /index':1},'warm startup metadata count differs')
    set_phase('market-force');_,fresh=call('GET',FORCE_MARKET);finish_phase()
    check(any(x.get('plugin_id')==PLUGIN for x in fresh.get('data',{}).get('items',[])), 'fresh actual card missing')
    install('install-failure',ok=False)
    _,absent=call('GET',API,expected=404)
    check(peer_counts()=={'GET /index':3,'GET /worker':1},'failed install replayed requests')
    install('install-recovery');first=snapshot('installed')
    _,saved=call('PUT','/api/plugins/'+PLUGIN,{'enabled':False,'key':'owned-a-persisted'});success(saved)
    install('compatibility');compatible=snapshot('compatible',first['nonce'])
    install('same-version-force',force=True);forced=snapshot('forced',compatible['nonce'])
    check(forced.get('config')=={'enabled':False,'key':'owned-a-persisted'}
          and forced.get('value')=='owned-a-persisted','same-version force lost config/KV')
    set_phase('history');_,history=call('GET',HISTORY);finish_phase()
    h=history.get('data',{});check(h.get('current_version')=='1.0' and h.get('latest_version')=='1.0'
          and h.get('history',{}).get('1.0')=='owned actual release','history differs')
    set_phase('release');_,release=call('GET',RELEASE);finish_phase()
    r=release.get('data',{});check(r.get('current_version')=='1.0' and len(r.get('items',[]))==1,'release differs')
    check(peer_counts()=={'GET /index':8,'GET /worker':3},'warm chain totals differ')
    set_phase('cold-startup');old_host=proc.pid;stop();start();finish_phase()
    check(proc.pid!=old_host,'cold host PID unchanged')
    # Source normal startup uses the saved HTTPS repository; ordinary market
    # read waits for that existing fetch and must not add another request.
    call('GET',MARKET)
    cold=snapshot('cold',forced['nonce'])
    check(cold.get('config')==forced.get('config') and cold.get('value')==forced.get('value'), 'same DB cold config/KV changed')
    check(peer_counts()=={'GET /index':9,'GET /worker':3},'cold startup refetched artifact or totals differ')
    set_phase('idle');stop()
    check(len(OBS['processes'])==2 and all(x.get('exit')==0 for x in OBS['processes']), 'normal hosts did not naturally stop')
    for path in E.glob('host-*.log'):
        raw=path.read_bytes();check(not any(v.encode() in raw for v in (password,api_token)), 'owned auth secret in log')
    OBS['actual_endpoint_request_counts']=peer_counts();OBS['status']='PASS'
except BaseException as error:
    OBS['error_class']=type(error).__name__;text=str(error)
    for secret in (password,api_token):text=text.replace(secret,'[owned-secret]')
    OBS['error']=text
finally:
    try:stop()
    except BaseException as error:OBS['status']='FAIL';OBS['cleanup_error']=type(error).__name__
    for (pid,starttime),row in owned_workers.items():
        current=identity(pid)
        if current is not None and current['starttime']==starttime:
            OBS['status']='FAIL';OBS.setdefault('forced_worker_cleanup',[]).append(row)
            try:os.kill(pid,signal.SIGTERM)
            except ProcessLookupError:pass
    if peer is not None:
        if thread is not None and thread.is_alive():peer.shutdown()
        peer.server_close()
    if thread is not None:thread.join(timeout=3)
    OBS['input_drift']=[label for label,path,digest in (
        ('server',SERVER,args.expected_server_sha),('worker',WORKER,args.expected_worker_sha),
        ('driver',Path(__file__),args.expected_driver_sha),('bindings',BINDINGS,args.expected_bindings_sha))
        if not path.is_file() or sha(path)!=digest]
    for item in json.loads(BINDINGS.read_text())['inputs']:
        if not Path(item['path']).is_file() or sha(item['path'])!=item['sha256']:OBS['input_drift'].append(item['path'])
    OBS['environment_unchanged']=os.environ.copy()==INITIAL_ENV and ENV==INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):OBS['status']='FAIL'
    try:reserve()
    except BaseException as error:OBS['status']='FAIL';OBS['terminal_reserve_error']=type(error).__name__
    OBS['finished_at']=now();save()
    (E/'driver.exit').write_text('0\n' if OBS['status']=='PASS' else '1\n')
    print(json.dumps({'status':OBS['status'],'hosts':[x['pid'] for x in OBS['processes']],
                      'queries':len(OBS['queries']),'error':OBS.get('error'),'evidence':str(E)}))
raise SystemExit(0 if OBS['status']=='PASS' else 1)
