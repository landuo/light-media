#!/usr/bin/env python3
"""Prepared only: actual ordinary CLI, two host processes, owned read-only qB HTTP.
No Go build, source Python, downloader daemon, mutations, mock SDK, or TLS probe.
Execute only after coordinator grants the reviewed, SHA-bound production window.
"""
import argparse, datetime, hashlib, http.client, http.cookies, http.server, io, json, os
import platform, secrets, signal, subprocess, threading, time, zipfile
import urllib.parse
from pathlib import Path

parser=argparse.ArgumentParser()
for name in ('server','worker','evidence','expected-server-sha','expected-worker-sha'):
    parser.add_argument('--'+name,required=True)
args=parser.parse_args()
E=Path(args.evidence).resolve()
assert E.parent==Path(__file__).resolve().parent and not E.exists(), 'new owned audit child required'
E.mkdir(exist_ok=False,mode=0o700)
DATA=E/'private-data';DATA.mkdir(mode=0o700)
SERVER=Path(args.server).resolve();WORKER=Path(args.worker).resolve()
ENV=os.environ.copy();INITIAL_ENV=os.environ.copy()
ID='OwnedQBittorrentCompleted';QUERY='/api/v1/plugin/'+ID+'/query';CONFIG='/api/plugins/'+ID
selection={'downloader_id':'owned-qb','ids_json':'null','tags_json':'"owned"'}
password=secrets.token_urlsafe(28);qb_password=secrets.token_urlsafe(28)
OBS={'status':'FAIL','kind':'ordinary CLI two-process cold query; ownedHTTPprotocol only','passes':[],'calls':[],'query_responses':[],'peer_calls':[],'processes':[],'checks':{},'not_claimed':['real qB daemon/source/module/business consumer acceptance','browser','full raw-fields CLI gate','any downloader mutations or real downloads']}
peer=None;peer_thread=None;proc=None;log=None;origin='http://127.0.0.1:39123';cookie=None;manifest=None
state={'mode':'success','qb_calls':0,'info_calls':0,'login_calls':0,'artifact_calls':0,'index_calls':0}
lock=threading.Lock();owned_workers={};current_workers={};inputs={}
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def write(name,value):(E/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n')
def check(ok,message):
    if not ok:raise AssertionError(message)
def snapshot():
    with lock:return dict(state)
def reserve():
    import shutil
    row={'at':now(),'scratch_free':shutil.disk_usage(E).free,'tmp_free':shutil.disk_usage('/tmp').free}
    with (E/'resources.jsonl').open('a') as out:out.write(json.dumps(row)+'\n')
    check(row['scratch_free']>=1<<30 and row['tmp_free']>=3*(1<<29),'resource reserve reached')
def startup_false(name):
    if name not in ENV:return
    value=ENV[name].lower()
    check(value in ('0','false','f','no','n','off'), 'inherited '+name+' conflicts with ordinary isolated run; no environment rewrite allowed')
def preflight():
    check(os.environ.get('S01_CLI_EXECUTION_GRANTED'),'separate coordinator production grant required')
    check(platform.system()=='Linux' and platform.machine() in ('x86_64','amd64'),'requires frozen linux-amd64 inputs')
    check(sha(SERVER)==args.expected_server_sha and sha(WORKER)==args.expected_worker_sha,'binary SHA mismatch')
    check(not [v for v in ENV.get('PLUGIN_MARKET','').split(',') if v.strip()],'inherited PLUGIN_MARKET conflicts with owned-only; do not clear it')
    startup_false('DEV')
    startup_false('LIGHT_MEDIA_SAFE_MODE' if 'LIGHT_MEDIA_SAFE_MODE' in ENV else 'MOVIEPILOT_SAFE_MODE')
    OBS['environment']={'inherited_environment_copy':True,'overrides':[],'all_inherited_variables_equal':ENV==INITIAL_ENV,'inherited_variable_count':len(INITIAL_ENV),'proxy_guard_environment_not_rewritten':True}
    check(ENV==INITIAL_ENV,'environment changed');reserve()

# Root-authorized one-purpose fixed local testing controller. No proxy
# credential access, environment rewrite, redirects or alternate destinations.
API_ROUTES={('GET','/healthz'),('POST','/api/auth/login'),
            ('POST','/api/plugins/install'),('PUT',CONFIG),('GET',CONFIG),
            ('POST',QUERY)}
def req(method,path,body=None,auth=True):
    global cookie
    reserve();check((method,path) in API_ROUTES,'outside fixed owned API allowlist')
    target=origin+path;url=urllib.parse.urlsplit(target)
    check(url.scheme=='http' and url.hostname=='127.0.0.1' and url.port==39123
          and url.netloc=='127.0.0.1:39123' and url.path==path and not url.query
          and not url.fragment and url.username is None and url.password is None,
          'exact http://127.0.0.1:39123 required')
    check(proc is not None and proc.poll() is None,'held owned host must be running')
    headers={'Content-Type':'application/json'}
    if auth and path!='/api/auth/login':
        check(cookie is not None,'owned authenticated cookie required');headers['Cookie']=cookie
    row={'at':now(),'method':method,'path':path,'host_pid':proc.pid};OBS['calls'].append(row)
    conn=http.client.HTTPConnection('127.0.0.1',39123,timeout=15)
    try:
        conn.request(method,path,body=None if body is None else json.dumps(body).encode(),headers=headers)
        response=conn.getresponse();code=response.status;raw=response.read(2*1024*1024+1)
        check(len(raw)<=2*1024*1024,'owned API response bound exceeded');row['status']=code
        if path=='/api/auth/login':
            row['credentials_headers_body_not_persisted']=True
            if code==200:
                jar=http.cookies.SimpleCookie()
                for value in response.headers.get_all('Set-Cookie',[]):jar.load(value)
                check(jar,'owned login cookie absent');cookie='; '.join(v.key+'='+v.value for v in jar.values())
        else:
            name='api-response-%03d.body'%len(OBS['calls']);(E/name).write_bytes(raw)
            row.update(raw_file=name,raw_sha256=hashlib.sha256(raw).hexdigest(),bytes=len(raw))
        check(code not in (301,302,303,307,308),'redirect refused; no alternate request')
        try:value=json.loads(raw)
        except ValueError:value=raw.decode(errors='replace')
        if path==QUERY:
            name='query-response-%03d.body'%(len(OBS['query_responses'])+1);(E/name).write_bytes(raw)
            OBS['query_responses'].append({'status':code,'value':value,'raw_file':name,'raw_sha256':hashlib.sha256(raw).hexdigest(),'peer_after':snapshot()});write('query-responses.json',OBS['query_responses'])
        return code,value
    except BaseException as exc:
        row['transport_or_validation_error_class']=type(exc).__name__
        raise # No retry/fallback, including connection refusal or policy denial.
    finally:conn.close()

def ok(method,path,body=None):
    code,value=req(method,path,body);check(code==200,'unexpected HTTP '+str(code)+' for '+path)
    return value

def safe_summary(value):
    check(isinstance(value,dict) and value.get('success') is True,'query not successful')
    wanted={'instance_id':ID,'downloader_id':'owned-qb','count':1,'tasks':[{'name':'Owned completed item','status':'stoppedUP'}]}
    check(value.get('summary')==wanted,'safe query summary differs')
    check(set(value)=={'success','summary'},'unexpected raw material in HTTP result')
    return value['summary']

class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def send(self,status,value,kind='application/json',headers=None):
        raw=value if isinstance(value,bytes) else json.dumps(value).encode()
        self.send_response(status);self.send_header('Content-Type',kind);self.send_header('Content-Length',str(len(raw)))
        for key,value in (headers or {}).items():self.send_header(key,value)
        self.end_headers();self.wfile.write(raw)
    def record(self,method,path):
        with lock:OBS['peer_calls'].append({'at':now(),'method':method,'path':path})
    def do_GET(self):
        path=urllib.parse.urlsplit(self.path).path;self.record('GET',path)
        if path=='/artifact/index':
            with lock:state['index_calls']+=1
            return self.send(200,index)
        if path=='/artifact/worker.zip':
            with lock:state['artifact_calls']+=1
            return self.send(200,archive,'application/zip')
        with lock:OBS.setdefault('peer_errors',[]).append('unexpected GET '+path)
        self.send(404,{'error':'outside owned fixture routes'})
    def do_POST(self):
        path=urllib.parse.urlsplit(self.path).path;self.record('POST',path)
        try:
            size=int(self.headers.get('Content-Length','0'));check(0<=size<131072,'owned request body bound')
            form=urllib.parse.parse_qs(self.rfile.read(size).decode(),keep_blank_values=True)
            check(path in ('/api/v2/auth/login','/api/v2/torrents/info'),'unexpected write/mutation route '+path)
            with lock:state['qb_calls']+=1
            if path=='/api/v2/auth/login':
                check(form=={'username':['owned-qb-user'],'password':[qb_password]},'unexpected owned login form')
                with lock:state['login_calls']+=1
                return self.send(200,b'Ok.','text/plain',{'Set-Cookie':'SID=owned-s01-session; Path=/'})
            check(form=={'filter':['completed']},'source unknown-hash finite form changed')
            check('SID=owned-s01-session' in self.headers.get('Cookie',''),'native SID session missing')
            with lock:state['info_calls']+=1;mode=state['mode']
            if mode=='failure503':return self.send(503,{'error':'owned transient fixture failure'})
            check(mode=='success','unexpected fixture mode')
            return self.send(200,[{'hash':'a'*40,'name':'Owned completed item','state':'stoppedUP','progress':1,'amount_left':0,'tags':'owned','private_field':'must-not-enter-safe-summary'},{'hash':'b'*40,'name':'Other tag completed item','state':'stoppedUP','progress':1,'amount_left':0,'tags':'other'}])
        except Exception as exc:
            with lock:OBS.setdefault('peer_errors',[]).append(type(exc).__name__+': '+str(exc))
            self.send(400,{'error':'owned fixture rejected request'})

def identity(pid):
    try:
        # Only a known owned host or its direct descendants are inspected.
        value=Path('/proc/'+str(pid)+'/stat').read_text();fields=value[value.rfind(')')+2:].split()
        return fields[19]  # Linux starttime field22, immune to PID reuse.
    except FileNotFoundError:return None

def process_stat(pid):
    value=Path('/proc/'+str(pid)+'/stat').read_text();fields=value[value.rfind(')')+2:].split()
    check(len(fields)>19,'owned process stat shape unavailable')
    return {'ppid':int(fields[1]),'starttime':fields[19]}

def observe_worker():
    # PID comes only from the existing bear-authenticated owned fixture API.
    # No /proc enumeration or unsupported task/children file is consulted.
    before=snapshot();value=ok('POST',QUERY,{'observe_only':True})
    check(isinstance(value,dict) and set(value)=={'observe_only','pid','instance_id'} and value['observe_only'] is True and value['instance_id']==ID,'invalid owned observation envelope')
    pid=value['pid'];check(type(pid) is int and pid>0 and pid not in (proc.pid,os.getpid()),'observation PID is not a distinct positive worker integer')
    first=process_stat(pid);check(first['ppid']==proc.pid,'observed PID parent is not the current owned host')
    exe_link=Path('/proc/'+str(pid)+'/exe');raw=os.readlink(exe_link);executable=Path(raw).resolve()
    executable.relative_to(DATA)
    digest=sha(exe_link);check(digest==args.expected_worker_sha,'actual owned executable SHA differs')
    second=process_stat(pid);check(first==second and second['ppid']==proc.pid,'observed process identity changed during binding')
    row={'pid':pid,'starttime':first['starttime'],'ppid':first['ppid'],'exe_readlink':raw,'executable_sha256':digest,'instance_id':ID,'host_pid':proc.pid}
    owned_workers[(pid,first['starttime'])]=row;current_workers[(pid,first['starttime'])]=row
    OBS.setdefault('worker_observations',[]).append(row);write('worker-observations.json',OBS['worker_observations'])
    quiet_check('observe_only_'+str(len(OBS['worker_observations']))+'_no_qb',before)

def collect_workers():
    # Cleanup checks only identities already supplied and bound by observe_only.
    # No discovery, child enumeration, fallback or matching arbitrary process.
    if proc is None:return
    for (pid,start),row in current_workers.items():
        current=process_stat(pid)
        check(current['starttime']==start and current['ppid']==proc.pid,'known owned worker identity changed before host stop')

def start():
    global proc,log,cookie,current_workers
    reserve();current_workers={};cookie=None
    check(len(OBS['processes'])<2,'only two sequential owned normal hosts')
    addr='127.0.0.1:39123'
    log_path=E/('server-'+str(len(OBS['processes'])+1)+'.log');log=log_path.open('xb')
    proc=subprocess.Popen([str(SERVER),'--listen',addr,'--data-dir',str(DATA)],env=ENV,stdout=log,stderr=log,start_new_session=True)
    OBS['processes'].append({'pid':proc.pid,'starttime':identity(proc.pid),'ordinary':True,'safe_mode':False,'same_private_datadir':True,'listen':addr})
    # Wait only on the held host's own startup log. A connection itself is
    # attempted once; rejection never starts an alternate network path.
    deadline=time.monotonic()+30
    while True:
        check(proc.poll() is None,'ordinary server exited before listener announcement')
        if b'light-media listening at '+addr.encode() in log_path.read_bytes():break
        check(time.monotonic()<deadline,'held host did not announce exact listener');time.sleep(.05)
    time.sleep(.1) # same bounded listener-announcement settle as accepted S07
    check(req('GET','/healthz',auth=False)[0]==200,'exact owned health request failed')
    ok('POST','/api/auth/login',{'username':'owned-s01','password':password})
    collect_workers()

def stop():
    global proc,log
    if proc is None:return
    current=proc;row=OBS['processes'][-1];collection_error=None
    try:collect_workers()
    except BaseException as exc:
        collection_error=type(exc).__name__+': '+str(exc)
        row['worker_collection_failure']=collection_error;OBS['status']='FAIL'
    row['observed_workers']=list(current_workers.values())
    try:
        current.send_signal(signal.SIGTERM)
        try:rc=current.wait(timeout=25)
        except subprocess.TimeoutExpired:
            current.kill();current.wait(timeout=10);row['forced_kill']=True;raise AssertionError('normal server did not stop gracefully')
        check(rc==0,'normal server Wait nonzero')
    finally:
        row['wait_exit']=current.returncode
        try:row['workers_still_same_identity']=[pid for pid,start in current_workers if identity(pid)==start]
        finally:log.close();proc=None
    check(not row['workers_still_same_identity'],'owned worker not naturally reaped')
    check(collection_error is None,'owned worker collection failed before cleanup: '+str(collection_error))

def quiet_check(label,before):
    # Readiness/config/auth only, never get_page (that would issue a query).
    check(snapshot()['qb_calls']==before['qb_calls'],label+' unexpectedly contacted qB')
    OBS['checks'][label]={'before':before,'after':snapshot()}

def termination_requested(signum,frame):
    raise KeyboardInterrupt('owned driver termination signal '+str(signum))
signal.signal(signal.SIGTERM,termination_requested)

try:
    preflight();inputs={str(p):sha(p) for p in (SERVER,WORKER,Path(__file__))};OBS['inputs']=inputs;write('inputs.json',inputs)
    manifest_run=subprocess.run([str(WORKER),'--manifest'],env=ENV,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=10)
    check(manifest_run.returncode==0,'real SDK worker manifest failed');manifest=json.loads(manifest_run.stdout)
    check(manifest.get('plugin_id')==ID and manifest.get('events',[])==[],'wrong or observing worker')
    check(manifest.get('capabilities')==['plugin.base.get_config','plugin.base.get_instance_metadata','downloader.qbittorrent.completed.query'],'unexpected SDK capabilities')
    check(manifest.get('apis')==[{'key':'query','path':'/query','methods':['POST'],'auth':'bear','body':'json','body_required':True}],'fixture ordinary API changed')
    write('worker-manifest.json',manifest)
    buffer=io.BytesIO()
    with zipfile.ZipFile(buffer,'w',zipfile.ZIP_DEFLATED) as z:
        entry=zipfile.ZipInfo(manifest['entry']);entry.external_attr=0o100700<<16;entry.compress_type=zipfile.ZIP_DEFLATED;z.writestr(entry,WORKER.read_bytes())
    archive=buffer.getvalue();archive_sha=hashlib.sha256(archive).hexdigest()
    peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);peer_url='http://127.0.0.1:'+str(peer.server_port)
    index={'schema_version':1,'plugin_id':ID,'current_version':manifest['version'],'releases':[{'version':manifest['version'],'manifest':manifest,'artifacts':[{'platform':'linux-amd64','url':peer_url+'/artifact/worker.zip','sha256':archive_sha,'executable_sha256':args.expected_worker_sha,'size':len(archive),'format':'zip','host_contract':'light-media.sdk-resources/v1'}]}]}
    OBS['artifact']={'zip_sha256':archive_sha,'bytes':len(archive),'executable_sha256':args.expected_worker_sha}
    peer_thread=threading.Thread(target=peer.serve_forever,daemon=True);peer_thread.start()
    boot={'username':'owned-s01','password':password,'settings':{'plugin_markets':[],'plugin_local_repo_paths':[],'plugin_auto_reload':False,'plugin_statistic_share':False,'plugin_server_url':'','site_data_refresh_hours':0,'read_site_messages':False,'downloaders':[{'id':'owned-qb','kind':'qb','name':'Owned qB protocol fixture','base_url':peer_url,'username':'owned-qb-user','password':qb_password,'enabled':True}],'default_downloader':'owned-qb','sites':[],'directories':[],'roots':[],'allowed_roots':[],'rss_sites':[]}}
    run=subprocess.run([str(SERVER),'--data-dir',str(DATA),'--bootstrap-stdin'],input=json.dumps(boot).encode(),env=ENV,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=25)
    (E/'bootstrap.log').write_bytes(run.stdout+run.stderr);OBS['bootstrap_exit']=run.returncode
    check(run.returncode==0 and b'bootstrap_complete' in run.stdout,'ordinary bootstrap failed')
    before=snapshot();start();host1=proc.pid;quiet_check('host1_startup_no_qb',before)
    installed=ok('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':peer_url+'/artifact/index'})
    check(installed.get('data',{}).get('success') is True,'ordinary install business failure')
    saved=ok('PUT',CONFIG,selection);check(saved.get('data',{}).get('success') is True,'ordinary config save failed')
    config1=ok('GET',CONFIG);check(config1.get('data')==selection,'warm persisted config readback differs')
    observe_worker();check(current_workers,'actual owned SDK process not observed');quiet_check('install_config_no_qb',before)
    for invalid in ({'observe_only':'true'},{'observe_only':None},{'observe_only':True,'action':'other'}):
        before_invalid=snapshot();check(req('POST',QUERY,invalid)[0]!=200,'invalid observation options accepted');quiet_check('invalid_observe_'+str(len(OBS['query_responses']))+'_no_qb',before_invalid)
    before=snapshot();check(req('POST',QUERY,{},auth=False)[0]==401,'anonymous query did not reject401');quiet_check('warm_401_no_qb',before)
    first=safe_summary(ok('POST',QUERY,{}));OBS['passes'].append('ordinary CLI/installed SDK successful unknown-hash completed query, safe summary')
    with lock:state['mode']='failure503'
    before=snapshot();code,value=req('POST',QUERY,{});check(code!=200,'owned503 became successful/empty summary')
    check(snapshot()['info_calls']==before['info_calls']+1,'503 query retried or not dispatched exactly once')
    with lock:state['mode']='success'
    check(safe_summary(ok('POST',QUERY,{}))==first,'recovery changed successful summary')
    OBS['passes'].append('one owned503 is visible failure, then normal recovery; no full semantics matrix rerun')
    warm_workers=set(current_workers);stop();check(OBS['processes'][-1]['wait_exit']==0,'first host Wait0 missing')
    before=snapshot();start();check(proc.pid!=host1,'not a distinct second OS host')
    config2=ok('GET',CONFIG);check(config2.get('data')==config1.get('data')==selection,'cold persisted plugin configuration changed')
    observe_worker();check(current_workers and not warm_workers.intersection(current_workers),'cold SDK worker not new')
    quiet_check('host2_cold_config_before_query_no_qb',before)
    OBS['checks']['cold_same_config']=True
    check(req('POST',QUERY,{},auth=False)[0]==401,'cold anonymous query did not reject401');quiet_check('cold_401_no_qb',before)
    check(safe_summary(ok('POST',QUERY,{}))==first,'cold ordinary query changed summary')
    stop();OBS['passes'].append('same private DataDir, two distinct OS hosts/new SDK worker, cold zero active qB before query, persisted config,401 fence,both Wait0 and natural worker reaping')
    check(snapshot()['index_calls']==1 and snapshot()['artifact_calls']==1,'cold query reacquired artifact/index')
    check(not OBS.get('peer_errors'),'unexpected owned peer request')
    OBS['status']='PASS'
except BaseException as exc:
    OBS['failure']=(type(exc).__name__+': '+str(exc)).replace(password,'[owned-password]').replace(qb_password,'[owned-qb-password]')
finally:
    try:stop()
    except BaseException as exc:OBS['cleanup_failure']=type(exc).__name__+': '+str(exc);OBS['status']='FAIL'
    # Failure cleanup is limited to exact observe_only PIDs whose PPid/exe SHA
    # were already bound to this owned host. Never enumerate any process tree.
    for (pid,start),row in owned_workers.items():
        if identity(pid)==start:
            OBS.setdefault('forced_worker_cleanup',[]).append(row);OBS['status']='FAIL'
            try:os.kill(pid,signal.SIGTERM)
            except ProcessLookupError:pass
    if peer is not None:peer.shutdown();peer.server_close()
    if peer_thread is not None:peer_thread.join(timeout=3)
    OBS['final_peer_counts']=snapshot();OBS['input_drift']=[p for p,h in inputs.items() if not Path(p).is_file() or sha(p)!=h]
    OBS['environment_unchanged']=ENV==INITIAL_ENV and os.environ.copy()==INITIAL_ENV
    if OBS['input_drift'] or not OBS['environment_unchanged'] or OBS.get('peer_errors'):OBS['status']='FAIL'
    OBS['finished_at']=now();write('observations.json',OBS);(E/'driver.exit').write_text('0\n' if OBS['status']=='PASS' else '1\n')
    print(json.dumps({'status':OBS['status'],'failure':OBS.get('failure'),'evidence':str(E),'passes':OBS['passes']},ensure_ascii=False,indent=2))
raise SystemExit(0 if OBS['status']=='PASS' else 1)
