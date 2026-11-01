#!/usr/bin/env python3
"""Prepared only: exact39123 normal hosts, installed SDK, fixed owned qB HTTPS.
No real daemon, upstream, proxy/CONNECT fixture, environment rewrite or source execution.
"""
import argparse, datetime, hashlib, http.client, http.cookies, http.server, io
import json, os, re, secrets, shutil, signal, ssl, subprocess, threading, time, zipfile
import urllib.parse
from pathlib import Path
P=argparse.ArgumentParser(description=__doc__)
for name in ('server','worker','output','expected-server-sha','expected-worker-sha','expected-driver-sha','expected-bindings-sha'):
    P.add_argument('--'+name,required=True)
args=P.parse_args();AUDIT=Path(__file__).resolve().parent;ROOT=AUDIT.parents[1]
BINDINGS=AUDIT/'execution-bindings.json'
SERVER,WORKER,E=(Path(p).resolve() for p in (args.server,args.worker,args.output))
BASE='bd4ec4513e6ff9edc6eb4e0a357febf7d4ec30e0'
CANDIDATE=ROOT/'target-drafts/s01-qb-query-tls01/candidate02.json'
CANDIDATE_SHA='22eaadf630d91021e030dc4cd0bcc75cadc1533472e37bdd589e21b8505b2c28'
TESTCERT=ROOT.parent/'plugins-cloud-20261028/toolchain/go/src/net/http/internal/testcert/testcert.go'
INITIAL_ENV=os.environ.copy();ENV=INITIAL_ENV.copy()
peer=thread=proc=log=cookie=None
lock=threading.Lock();index={};owned_workers={};process_workers={}
origin='http://127.0.0.1:39123';peer_origin='';PLUGIN='OwnedQBittorrentCompleted';ID=PLUGIN
API='/api/v1/plugin/'+PLUGIN+'/query';CONFIG='/api/plugins/'+PLUGIN
selection={'downloader_id':'owned-qb','ids_json':'null','tags_json':'"owned"'}
state={'mode':'success','login_calls':0,'info_calls':0,'index_calls':0,'artifact_calls':0}
OBS={'status':'FAIL','kind':'S01 qB private TLS ordinary CLI with actual installed standard worker',
     'calls':[],'peer_calls':[],'processes':[],'queries':[],
     'not_claimed':['real qB daemon','source whole application','browser/UI','all task or downloading query',
                    'new Transmission validation','new raw-field matrix','whole61','push/CI']}
ROUTES={('GET','/healthz'),('POST','/api/auth/login'),('GET','/api/plugins/markets'),
        ('POST','/api/plugins/install'),('PUT',CONFIG),('GET',CONFIG),('POST',API)}
password=secrets.token_urlsafe(28);api_token=secrets.token_urlsafe(28);qb_password=secrets.token_urlsafe(28)
DATA=E/'private-data'

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
    check(INITIAL_ENV.get('S01_QB_TLS_CLI_GRANTED') == '1', 'coordinator execution window required')
    check(SERVER == ROOT / 'runtime/s01-qb-query-tls-server01'
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
    check(WORKER == ROOT / 'runtime/s01-qb-query-tls-worker01', 'exact newly bound normal owned worker required')
    clean = binding.get('clean_view_proof', {})
    check(type(clean.get('path')) is str and type(clean.get('sha256')) is str
          and rows.get(clean['path']) == clean['sha256'], 'reviewed clean-view proof not bound')
    for item in binding['inputs']:
        check(sha(item['path']) == item['sha256'], 'frozen input changed: ' + item['path'])
    check(E.parent == ROOT / 'test-instances' and E.name == 's01-qb-query-tls-cli01'
          and not E.exists(), 'exclusive new S01 TLS test-instance required')
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
    if path == API and authenticated:
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
            check(not any(v.encode() in raw for v in (password, api_token, qb_password)), 'private auth value in ordinary response')
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

def safe_summary(value):
    check(isinstance(value,dict) and value.get('success') is True,'query not successful')
    wanted={'instance_id':ID,'downloader_id':'owned-qb','count':1,'tasks':[{'name':'Owned completed item','status':'stoppedUP'}]}
    check(value.get('summary')==wanted,'safe query summary differs')
    check(set(value)=={'success','summary'},'unexpected raw material in HTTP result')
    return value['summary']

def counts():
    with lock:
        check(not OBS.get('peer_errors'),'owned peer recorded an unexpected request')
        return dict(state)


def quiet(label,before):
    after=counts()
    check(after['login_calls']==before['login_calls'] and after['info_calls']==before['info_calls'],
          label+' unexpectedly queried qB')
    OBS.setdefault('quiet_checks',[]).append({'label':label,'before':before,'after':after})


def start():
    global proc,log,cookie,process_workers
    reserve();cookie=None;process_workers={}
    check(len(OBS['processes'])<2,'only two sequential normal hosts')
    logfile=E/('host-%d.log'%(len(OBS['processes'])+1));log=logfile.open('xb')
    proc=subprocess.Popen([str(SERVER),'--listen','127.0.0.1:39123','--data-dir',str(DATA)],
                          env=ENV,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
    row={'pid':proc.pid,'identity':identity(proc.pid),'normal_not_safe':True,'same_private_data':str(DATA)}
    OBS['processes'].append(row)
    check(row['identity'] is not None and row['identity']['ppid']==os.getpid(),'held host parent mismatch')
    held_host();check(sha('/proc/'+str(proc.pid)+'/exe')==args.expected_server_sha,'host executable SHA mismatch')
    row['executable_sha256']=args.expected_server_sha;save()
    deadline=time.monotonic()+30
    while True:
        check(proc.poll() is None and time.monotonic()<deadline,'owned host startup failed/timed out')
        if b'light-media listening at 127.0.0.1:39123' in logfile.read_bytes():
            inode=exact_owned_listener()
            if inode is not None:row['owned_listener_inode']=inode;break
        time.sleep(.05)  # Metadata readiness only, never a failed-network retry.
    _,health=call('GET','/healthz',authenticated=False)
    check(health.get('data',{}).get('status')=='ok','owned DB health failed')
    call('POST','/api/auth/login',{'username':'owned-s01','password':password},authenticated=False)
    _,settings=call('GET','/api/plugins/markets')
    check(settings['data']['plugin_statistic_share'] is False and settings['data']['plugin_server_url']=='',
          'inherited owned public-center isolation differs')


def observe_worker(label):
    before=counts();_,value=call('POST',API,{'observe_only':True})
    check(isinstance(value,dict) and set(value)=={'observe_only','pid','instance_id'}
          and value['observe_only'] is True and value['instance_id']==PLUGIN,'invalid actual worker observation')
    bind_worker(value['pid']);quiet(label+'_observe_no_qb',before)
    OBS.setdefault('worker_observations',[]).append({'label':label,**value})
    return value['pid']


def business(label,expected=200):
    before=counts();code,value=call('POST',API,{},expected=expected);after=counts()
    check(after['login_calls']==before['login_calls']+1 and after['info_calls']==before['info_calls']+1,
          'business query changed exact login/info count or replayed')
    summary=safe_summary(value) if expected==200 else None
    OBS['queries'].append({'label':label,'status':code,'summary':summary,'call_index':len(OBS['calls']),
                           'peer_before':before,'peer_after':after})
    return summary


class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def send(self,status,value,kind='application/json',headers=None):
        raw=value if isinstance(value,bytes) else json.dumps(value).encode()
        self.send_response(status);self.send_header('Content-Type',kind);self.send_header('Content-Length',str(len(raw)))
        for key,value in (headers or {}).items():self.send_header(key,value)
        self.end_headers();self.wfile.write(raw)
    def fixed(self,method,allowed):
        parsed=urllib.parse.urlsplit(self.path)
        check(parsed.scheme=='' and parsed.netloc=='' and parsed.query=='' and parsed.fragment==''
              and parsed.path==self.path and self.path in allowed,'outside exact owned peer route')
        check(self.headers.get('Host')=='127.0.0.1:'+str(self.server.server_address[1]),'unexpected owned Host')
        check(not self.headers.get('Proxy-Authorization'),'proxy credentials forbidden at owned peer')
        with lock:OBS['peer_calls'].append({'at':now(),'method':method,'path':self.path})
    def error(self,exc):
        with lock:OBS.setdefault('peer_errors',[]).append(type(exc).__name__)
        self.send(400,{'error':'owned fixed peer rejected request'})
    def do_GET(self):
        try:
            self.fixed('GET',{'/artifact/index','/artifact/worker.zip'})
            if self.path=='/artifact/index':
                with lock:state['index_calls']+=1
                return self.send(200,index)
            with lock:state['artifact_calls']+=1
            self.send(200,archive,'application/zip')
        except BaseException as exc:self.error(exc)
    def do_POST(self):
        try:
            self.fixed('POST',{'/api/v2/auth/login','/api/v2/torrents/info'})
            size=int(self.headers.get('Content-Length','0'));check(0<=size<131072,'owned body exceeds fixed bound')
            form=urllib.parse.parse_qs(self.rfile.read(size).decode(),keep_blank_values=True)
            if self.path=='/api/v2/auth/login':
                check(form=={'username':['owned-qb-user'],'password':[qb_password]},'owned login form differs')
                with lock:state['login_calls']+=1
                return self.send(200,b'Ok.','text/plain',{'Set-Cookie':'SID=owned-s01-session; Path=/'})
            check(form=={'filter':['completed']},'source completed form differs')
            check('SID=owned-s01-session' in self.headers.get('Cookie',''),'owned SID missing')
            with lock:state['info_calls']+=1;mode=state['mode']
            if mode=='failure503':return self.send(503,{'error':'owned transient failure'})
            check(mode=='success','unknown owned phase')
            return self.send(200,[{'hash':'a'*40,'name':'Owned completed item','state':'stoppedUP','progress':1,'amount_left':0,'tags':'owned','private_field':'must-not-enter-safe-summary'},
                                 {'hash':'b'*40,'name':'Other tag completed item','state':'stoppedUP','progress':1,'amount_left':0,'tags':'other'}])
        except BaseException as exc:self.error(exc)
    def do_CONNECT(self):
        with lock:OBS.setdefault('peer_errors',[]).append('forbidden CONNECT')
        self.send(405,{'error':'CONNECT not implemented'})


try:
    preflight();DATA.mkdir(mode=0o700,exist_ok=False)
    worker_bytes=WORKER.read_bytes()
    completed=subprocess.run([str(WORKER),'--manifest'],env=ENV,capture_output=True,timeout=10)
    check(completed.returncode==0,'actual worker manifest failed')
    manifest=json.loads(completed.stdout)
    check(manifest.get('plugin_id')==PLUGIN and manifest.get('events',[])==[],'unexpected worker identity/events')
    check(manifest.get('capabilities')==['plugin.base.get_config','plugin.base.get_instance_metadata','downloader.qbittorrent.completed.query'],'worker capabilities changed')
    check(manifest.get('apis')==[{'key':'query','path':'/query','methods':['POST'],'auth':'bear','body':'json','body_required':True}],'fixed worker API differs')
    (E/'worker-manifest.json').write_bytes(completed.stdout)
    buffer=io.BytesIO()
    with zipfile.ZipFile(buffer,'w',zipfile.ZIP_DEFLATED) as z:
        entry=zipfile.ZipInfo(manifest['entry']);entry.external_attr=0o100700<<16;entry.compress_type=zipfile.ZIP_DEFLATED
        z.writestr(entry,worker_bytes)
    archive=buffer.getvalue();archive_sha=hashlib.sha256(archive).hexdigest()
    peer=make_tls_peer();peer_origin='https://127.0.0.1:'+str(peer.server_address[1]);OBS['owned_peer_origin']=peer_origin
    index.update({'schema_version':1,'plugin_id':PLUGIN,'current_version':manifest['version'],'releases':[{'version':manifest['version'],'manifest':manifest,'artifacts':[{
        'platform':'linux-amd64','url':peer_origin+'/artifact/worker.zip','sha256':archive_sha,'executable_sha256':args.expected_worker_sha,
        'size':len(archive),'format':'zip','host_contract':'light-media.sdk-resources/v1'}]}]})
    OBS['artifact']={'zip_sha256':archive_sha,'bytes':len(archive),'worker_sha256':args.expected_worker_sha}
    thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start()
    bootstrap={'username':'owned-s01','password':password,'settings':{
        'api_token':api_token,'plugin_markets':[],'plugin_local_repo_paths':[],'plugin_auto_reload':False,
        'plugin_statistic_share':False,'plugin_server_url':'','site_data_refresh_hours':0,'read_site_messages':False,
        'downloaders':[{'id':'owned-qb','kind':'qb','name':'Owned qB HTTPS protocol fixture','base_url':peer_origin,
                        'username':'owned-qb-user','password':qb_password,'enabled':True}],
        'default_downloader':'owned-qb','sites':[],'rss_sites':[],'directories':[],'roots':[],'allowed_roots':[]}}
    boot=subprocess.run([str(SERVER),'--data-dir',str(DATA),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),
                        capture_output=True,env=ENV,timeout=30)
    del bootstrap;OBS['bootstrap_exit']=boot.returncode
    check(boot.returncode==0 and b'bootstrap_complete' in boot.stdout,'owned bootstrap failed; private output withheld')
    before=counts();start();first_host=proc.pid;quiet('startup',before)
    _,installed=call('POST','/api/plugins/install',{'plugin_id':PLUGIN,'repo_url':peer_origin+'/artifact/index'})
    check(installed.get('data',{}).get('success') is True,'ordinary install business failure')
    _,saved=call('PUT',CONFIG,selection);check(saved.get('data',{}).get('success') is True,'ordinary config save failed')
    _,config1=call('GET',CONFIG);check(config1.get('data')==selection,'warm config readback differs')
    first_worker=observe_worker('installed');quiet('install_config',before)
    before=counts();call('POST',API,{},authenticated=False,expected=401);quiet('warm_401',before)
    first=business('success')
    with lock:state['mode']='failure503'
    business('failure503',expected=502)
    check(observe_worker('after-failure')==first_worker,'failure replaced the persistent worker')
    with lock:state['mode']='success'
    check(business('recovery')==first,'recovered source summary changed')
    check(observe_worker('recovered')==first_worker,'recovery replaced persistent worker')
    stop();before=counts();start();check(proc.pid!=first_host,'second host was not a different process')
    _,config2=call('GET',CONFIG);check(config2.get('data')==config1.get('data')==selection,'same DB cold config differs')
    check(observe_worker('cold')!=first_worker,'cold worker process not replaced')
    quiet('cold_startup',before)
    before=counts();call('POST',API,{},authenticated=False,expected=401);quiet('cold_401',before)
    check(business('cold')==first,'cold completed summary changed');stop()
    total=counts();check(total['index_calls']==1 and total['artifact_calls']==1 and total['login_calls']==4 and total['info_calls']==4,
                         'cold refetch, replay or exact peer totals differ')
    check(len(OBS['queries'])==4 and len(OBS['processes'])==2 and all(x.get('exit')==0 for x in OBS['processes']), 'normal lifecycle incomplete')
    for path in E.glob('host-*.log'):
        check(not any(v.encode() in path.read_bytes() for v in (password,api_token,qb_password)), 'owned secret in host log')
    OBS['actual_endpoint_request_counts']=peer_counts();OBS['final_peer_counts']=total;OBS['status']='PASS'
except BaseException as error:
    OBS['error_class']=type(error).__name__;text=str(error)
    for secret in (password,api_token,qb_password):text=text.replace(secret,'[owned-secret]')
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
    if BINDINGS.is_file():
        for item in json.loads(BINDINGS.read_text())['inputs']:
            if not Path(item['path']).is_file() or sha(item['path'])!=item['sha256']:OBS['input_drift'].append(item['path'])
    OBS['environment_unchanged']=os.environ.copy()==INITIAL_ENV and ENV==INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):OBS['status']='FAIL'
    OBS['finished_at']=now()
    if E.is_dir():
        try:reserve()
        except BaseException as error:OBS['status']='FAIL';OBS['terminal_reserve_error']=type(error).__name__
        save();(E/'driver.exit').write_text('0\n' if OBS['status']=='PASS' else '1\n')
    print(json.dumps({'status':OBS['status'],'hosts':[x['pid'] for x in OBS['processes']],
                      'queries':len(OBS['queries']),'error':OBS.get('error'),'evidence':str(E)}))
raise SystemExit(0 if OBS['status']=='PASS' else 1)
