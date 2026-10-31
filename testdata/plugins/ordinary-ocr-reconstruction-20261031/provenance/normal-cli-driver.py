#!/usr/bin/env python3
"""New S15 owned normal CLI gate, prepared only until a separate execution grant.
Stdlib controller, exact39123, five synthetic OCR calls; no requests import,
source runtime, Go build, browser, real OCR/site, proxy rewrite or forwarding.
"""
import argparse,datetime,hashlib,http.client,http.cookies,http.server,json,os
import secrets,shutil,signal,socket,subprocess,threading,time,urllib.parse
from pathlib import Path

P=argparse.ArgumentParser()
for name in ('server','worker','output','expected-server-sha','expected-worker-sha','expected-driver-sha'):
 P.add_argument('--'+name,required=True)
args=P.parse_args();AUDIT=Path(__file__).resolve().parent;ROOT=AUDIT.parents[1]
SERVER=Path(args.server).resolve();WORKER=Path(args.worker).resolve();E=Path(args.output).resolve()
INITIAL_ENV=os.environ.copy();ENV=INITIAL_ENV.copy();peer=thread=proc=log=None;cookie=None;origin='http://127.0.0.1:39123';peer_origin=None
lock=threading.Lock();mode='healthy';index={};owned_workers={};process_workers={}
OBS={'status':'FAIL','kind':'new normal CLI two-host/installed SDK; synthetic owned OCR HTTP only','calls':[],'peer_calls':[],'ocr_calls':[],'processes':[],'queries':[],'not_claimed':['source requests HTTP runtime','real recognition quality/site credentials','browser/S14','business AutoSignIn','whole platform/CI/delivery']}

def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def check(ok,message):
 if not ok:raise AssertionError(message)
def save():
 (E/'result.json').write_text(json.dumps(OBS,ensure_ascii=True,indent=2)+'\n')
def reserve():
 row={'at':now(),'scratch_free':shutil.disk_usage(E).free,'tmp_free':shutil.disk_usage('/tmp').free}
 with (E/'resources.jsonl').open('a') as output:output.write(json.dumps(row)+'\n')
 check(row['scratch_free']>=1<<30 and row['tmp_free']>=3*(1<<29),'actual free-space reserve reached')
def preflight():
 check(INITIAL_ENV.get('S15_NORMAL_CLI_GRANTED')=='1','coordinator execution window required')
 check(SERVER.is_relative_to(ROOT) and WORKER.is_relative_to(ROOT),'owned new project binaries required')
 check(sha(SERVER)==args.expected_server_sha and sha(WORKER)==args.expected_worker_sha and sha(__file__)==args.expected_driver_sha,'frozen program/driver SHA mismatch')
 check(E.parent==AUDIT and not E.exists(),'exclusive new owned audit child required')
 for name in ('PLUGIN_MARKET','LIGHT_MEDIA_MEDIA_ROOTS'):
  check(not ENV.get(name),'inherited endpoint/mount environment conflicts; no rewrite')
 for name in ('DEV','LIGHT_MEDIA_SAFE_MODE','MOVIEPILOT_SAFE_MODE'):
  check(name not in ENV or ENV[name].lower() in ('0','false','f','no','n','off'),'inherited startup mode conflicts; no rewrite')
 E.mkdir(mode=0o700,exist_ok=False);reserve()

def exact_owned_listener():
 # Same known-child descriptor/inode proof already accepted in new runtime02.
 check(proc is not None and proc.poll() is None,'held owned process stopped')
 sockets=set()
 for fd in (Path('/proc')/str(proc.pid)/'fd').iterdir():
  try:target=os.readlink(fd)
  except FileNotFoundError:continue
  if target.startswith('socket:[') and target.endswith(']'):sockets.add(target[8:-1])
 for line in (Path('/proc')/str(proc.pid)/'net/tcp').read_text().splitlines()[1:]:
  fields=line.split()
  if fields[1]=='0100007F:%04X'%39123 and fields[3]=='0A' and fields[9] in sockets:return fields[9]
 return None

ROUTES={('GET','/healthz'),('POST','/api/auth/login'),('GET','/api/plugins/markets'),('POST','/api/plugins/install'),('GET','/api/v1/plugin/OwnedOCR/query'),('POST','/api/v1/plugin/OwnedOCR/query')}
def call(method,path,body=None,authenticated=True,expected=200):
 global cookie
 reserve();check((method,path) in ROUTES,'outside six exact owned method/path pairs')
 target=urllib.parse.urlsplit(origin+path)
 check(target.scheme=='http' and target.netloc=='127.0.0.1:39123' and target.hostname=='127.0.0.1' and target.port==39123 and target.path==path and not target.query and not target.fragment and target.username is None and target.password is None,'exact owned controller origin required')
 check(exact_owned_listener() is not None,'exact listener not held by owned host; no request')
 headers={'Content-Type':'application/json'}
 if authenticated and path not in ('/healthz','/api/auth/login'):
  check(cookie is not None,'owned login cookie missing');headers['Cookie']=cookie
 row={'at':now(),'host_pid':proc.pid,'method':method,'path':path,'authenticated':authenticated};OBS['calls'].append(row)
 connection=http.client.HTTPConnection('127.0.0.1',39123,timeout=20)
 try:
  connection.request(method,path,body=None if body is None else json.dumps(body).encode(),headers=headers)
  response=connection.getresponse();raw=response.read(2*1024*1024+1);row['status']=response.status
  check(len(raw)<=2*1024*1024,'owned API response exceeds finite bound')
  if path=='/api/auth/login':
   row['private_headers_body_not_retained']=True
   if response.status==200:
    jar=http.cookies.SimpleCookie()
    for value in response.headers.get_all('Set-Cookie',[]):jar.load(value)
    check(jar,'owned session cookie missing');cookie='; '.join(v.key+'='+v.value for v in jar.values())
  else:
   name='response-%03d.body'%len(OBS['calls']);(E/name).write_bytes(raw);row.update(raw_file=name,sha256=hashlib.sha256(raw).hexdigest())
  check(response.status not in (301,302,303,307,308),'redirect refused; no alternate request')
  if expected is not None:check(response.status==expected,'unexpected owned API HTTP '+str(response.status)+' '+path)
  return response.status,json.loads(raw)
 except BaseException as error:
  row['transport_or_validation_error_class']=type(error).__name__;raise
 finally:connection.close()

class Peer(http.server.BaseHTTPRequestHandler):
 protocol_version='HTTP/1.1'
 def log_message(self,*args):pass
 def reply(self,status,raw,kind='application/json'):
  self.send_response(status);self.send_header('Content-Type',kind);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  with lock:OBS['peer_calls'].append({'method':'GET','path':self.path if self.path in ('/index','/worker') else 'outside-owned-routes'})
  if self.path=='/index':return self.reply(200,json.dumps(index).encode())
  if self.path=='/worker':return self.reply(200,worker_bytes,'application/octet-stream')
  with lock:OBS.setdefault('peer_errors',[]).append('unexpected owned GET')
  self.reply(502,b'{}')
 def do_POST(self):
  if self.path!='/captcha/base64':
   with lock:OBS.setdefault('peer_errors',[]).append('unexpected owned POST')
   self.reply(502,b'{}');return
  size=int(self.headers.get('Content-Length','0'));check(0<=size<=65536,'owned OCR body bound')
  raw=self.rfile.read(size)
  with lock:
   selected=mode;row={'method':'POST','path':self.path,'mode':selected,'body_sha256':hashlib.sha256(raw).hexdigest(),'body':raw.decode('ascii','replace'),'cookie_present':bool(self.headers.get('Cookie')),'authorization_present':bool(self.headers.get('Authorization')),'accept_present':'Accept' in self.headers};OBS['ocr_calls'].append(row)
   if raw!=b'{"base64_img": "QQ=="}' or row['cookie_present'] or row['authorization_present'] or row['accept_present']:OBS.setdefault('peer_errors',[]).append('source OCR payload/header boundary differs')
  if selected=='disconnect':
   self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
  self.reply(200,b'broken-json' if selected=='malformed' else b'{"result":"OWNED_OCR_NORMAL_OK"}')

def identity(pid):
 try:raw=Path('/proc/'+str(pid)+'/stat').read_text()
 except FileNotFoundError:return None
 fields=raw[raw.rfind(')')+2:].split();return {'ppid':int(fields[1]),'starttime':fields[19]}
def bind_worker(pid):
 check(type(pid) is int and pid>0 and pid not in (os.getpid(),proc.pid),'invalid returned worker PID')
 before=identity(pid);check(before is not None and before['ppid']==proc.pid,'worker is not direct child of held host')
 link=Path('/proc/'+str(pid)+'/exe');path=Path(os.readlink(link)).resolve()
 check(path.is_relative_to(DATA) and sha(link)==args.expected_worker_sha,'worker not owned installed executable')
 check(identity(pid)==before,'worker identity changed during binding')
 row=dict(before,pid=pid,executable=str(path),executable_sha256=args.expected_worker_sha,host_pid=proc.pid)
 owned_workers[(pid,before['starttime'])]=row;process_workers[(pid,before['starttime'])]=row
 OBS.setdefault('worker_identities',[]).append(row)
def wait_workers(rows):
 deadline=time.monotonic()+5
 for key,row in rows.items():
  while True:
   current=identity(row['pid'])
   if current is None or current['starttime']!=row['starttime']:break
   check(time.monotonic()<deadline,'owned worker remained after graceful host stop');time.sleep(.02)
def start():
 global proc,log,cookie,process_workers
 reserve();cookie=None;process_workers={};check(len(OBS['processes'])<2,'only two sequential normal hosts')
 logfile=E/('host-%d.log'%(len(OBS['processes'])+1));log=logfile.open('xb')
 proc=subprocess.Popen([str(SERVER),'--listen','127.0.0.1:39123','--data-dir',str(DATA)],env=ENV,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
 row={'pid':proc.pid,'identity':identity(proc.pid),'normal_not_safe':True,'same_private_data':str(DATA)};OBS['processes'].append(row);save();deadline=time.monotonic()+30
 while True:
  check(proc.poll() is None and time.monotonic()<deadline,'held host startup failed/timed out')
  if b'light-media listening at 127.0.0.1:39123' in logfile.read_bytes():
   inode=exact_owned_listener()
   if inode is not None:row['owned_listener_inode']=inode;break
  time.sleep(.05) # metadata/log readiness only, never a network retry.
 _,health=call('GET','/healthz',authenticated=False);check(health.get('data',{}).get('status')=='ok','owned DB health failed')
 call('POST','/api/auth/login',{'username':'owned-s15','password':password},authenticated=False)
 _,settings=call('GET','/api/plugins/markets');check(settings['data']['plugin_statistic_share'] is False and settings['data']['plugin_server_url']=='','owned settings isolation differs')
def registered():
 deadline=time.monotonic()+20
 while True:
  status,_=call('GET','/api/v1/plugin/OwnedOCR/query',expected=None)
  if status==405:return
  check(status==404 and time.monotonic()<deadline,'query registration failed; no transport-denial retry')
  time.sleep(.05) # only existing same-route application404->405 state polling.
def stop():
 global proc,log
 if proc is None:return
 current=proc;row=OBS['processes'][-1]
 if current.poll() is None:current.send_signal(signal.SIGTERM)
 try:code=current.wait(timeout=30)
 except subprocess.TimeoutExpired:
  row['forced_stop']=True;os.killpg(current.pid,signal.SIGKILL);code=current.wait(timeout=5)
 row['exit']=code;proc=None
 if log is not None:log.close();log=None
 check(code==0 and not row.get('forced_stop'),'normal host did not stop naturally')
 wait_workers(process_workers)
def query(label,selected):
 global mode
 with lock:before=len(OBS['ocr_calls']);mode=selected
 _,result=call('POST','/api/v1/plugin/OwnedOCR/query',{'image_b64':'QQ'})
 with lock:after=len(OBS['ocr_calls'])
 check(after==before+1,'query did not make one owned OCR call')
 check(result.get('stale_denied') is True,'expired host callback was accepted');bind_worker(result.get('pid'))
 if selected=='malformed':check(result.get('error_class')=='JSONDecodeError' and 'value' not in result,'typed source JSON failure differs')
 else:
  value=result.get('value',{});points=[] if selected=='disconnect' else list(map(ord,'OWNED_OCR_NORMAL_OK'))
  check(value.get('kind')=='string' and value.get('points',[])==points and not result.get('error_class'),'source success/network-empty result differs')
 OBS['queries'].append({'label':label,'host_pid':proc.pid,'response':result,'owned_ocr_calls':after});save();return result['pid']

preflight();DATA=E/'private-data';password=secrets.token_urlsafe(30);worker_bytes=WORKER.read_bytes()
try:
 completed=subprocess.run([str(WORKER),'--manifest'],capture_output=True,timeout=10)
 check(completed.returncode==0,'owned worker manifest command failed');manifest=json.loads(completed.stdout)
 check(manifest['plugin_id']=='OwnedOCR' and manifest['version']=='1.0' and manifest['capabilities']==['ocr.get_captcha_text'],'owned worker identity/capability differs')
 check(manifest['apis']==[{'key':'query','path':'/query','methods':['POST'],'auth':'bear','body':'json','body_required':True}] and 'http' in manifest['methods'],'owned ordinary API differs')
 (E/'worker-manifest.json').write_bytes(completed.stdout)
 peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);peer.daemon_threads=True;peer_origin='http://127.0.0.1:'+str(peer.server_address[1])
 index.update({'schema_version':1,'plugin_id':'OwnedOCR','current_version':'1.0','releases':[{'version':'1.0','manifest':manifest,'artifacts':[{'platform':'linux-amd64','url':peer_origin+'/worker','sha256':args.expected_worker_sha,'executable_sha256':args.expected_worker_sha,'size':len(worker_bytes),'format':'executable','host_contract':'light-media.sdk-resources/v1'}]}]})
 thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start()
 ENV['OCR_HOST']=peer_origin # The sole child override; never proxy/TLS/HOME/market modes.
 bootstrap={'username':'owned-s15','password':password,'settings':{'api_token':secrets.token_urlsafe(32),'plugin_markets':[],'plugin_local_repo_paths':[],'plugin_auto_reload':False,'plugin_statistic_share':False,'plugin_server_url':'','site_data_refresh_hours':0,'read_site_messages':False,'downloaders':[],'sites':[],'directories':[],'roots':[],'allowed_roots':[],'rss_sites':[]}}
 boot=subprocess.run([str(SERVER),'--data-dir',str(DATA),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),capture_output=True,env=ENV,timeout=30);del bootstrap
 OBS['bootstrap_exit']=boot.returncode;check(boot.returncode==0 and b'bootstrap_complete' in boot.stdout,'owned bootstrap failed; credentials/output withheld')
 start();_,installed=call('POST','/api/plugins/install',{'plugin_id':'OwnedOCR','repo_url':peer_origin+'/index'})
 check(installed.get('data',{}).get('success') is True,'owned standard installation business result failed');registered()
 check(len(OBS['ocr_calls'])==0,'installation/initialization replayed OCR')
 call('POST','/api/v1/plugin/OwnedOCR/query',{'image_b64':'QQ'},authenticated=False,expected=401)
 check(len(OBS['ocr_calls'])==0,'unauthenticated query reached OCR')
 warm=query('warm_success','healthy')
 check(query('typed_json_failure','malformed')==warm,'typed error restarted worker')
 check(query('network_failure_empty','disconnect')==warm,'network failure restarted worker')
 check(query('warm_recovery','healthy')==warm,'warm recovery restarted worker')
 first=proc.pid;stop();start();check(proc.pid!=first,'cold host reused process');registered()
 check(len(OBS['ocr_calls'])==4,'cold initialization replayed OCR')
 cold=query('cold_success','healthy');check(cold!=warm,'cold worker reused process');stop()
 check(len(OBS['ocr_calls'])==5,'five-call gate changed')
 check(sum(c['method']=='POST' and c['path']=='/api/plugins/install' for c in OBS['calls'])==1,'more than one installation')
 check(OBS['peer_calls']==[{'method':'GET','path':'/index'},{'method':'GET','path':'/worker'}],'cold artifact reacquired or unexpected peer request')
 check(not OBS.get('peer_errors'),'owned OCR peer rejected source payload')
 OBS['status']='PASS'
except BaseException as error:
 OBS['error_class']=type(error).__name__;OBS['error']=str(error).replace(password,'[owned-password]')
finally:
 try:stop()
 except BaseException as error:OBS['status']='FAIL';OBS['cleanup_error']=type(error).__name__+': '+str(error)
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
 OBS['input_drift']=[label for label,path,digest in [('server',SERVER,args.expected_server_sha),('worker',WORKER,args.expected_worker_sha),('driver',Path(__file__),args.expected_driver_sha)] if not path.is_file() or sha(path)!=digest]
 OBS['environment_unchanged_except_owned_OCR_HOST']=os.environ.copy()==INITIAL_ENV and ENV==dict(INITIAL_ENV,OCR_HOST=peer_origin)
 if OBS['input_drift'] or not OBS['environment_unchanged_except_owned_OCR_HOST'] or OBS.get('peer_errors'):OBS['status']='FAIL'
 OBS['finished_at']=now();save();(E/'driver.exit').write_text('0\n' if OBS['status']=='PASS' else '1\n');print(json.dumps({'status':OBS['status'],'hosts':[x['pid'] for x in OBS['processes']],'OCR_calls':len(OBS['ocr_calls']),'error':OBS.get('error'),'evidence':str(E)}))
raise SystemExit(0 if OBS['status']=='PASS' else 1)
