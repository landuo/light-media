"""Local real UI/App/SDK gate with an explicitly synthetic qB protocol peer."""
import hashlib,http.cookiejar,http.server,json,os,pathlib,signal,subprocess,sys,threading,time,urllib.request,urllib.error
ROOT=pathlib.Path(__file__).resolve().parents[2];WORK=ROOT/'work/plugin-host-downloader-20261031/browser';WORK.mkdir(exist_ok=True);APP='http://127.0.0.1:39381';REPO='http://127.0.0.1:39383';PEER='http://127.0.0.1:39382';ID='HostDownloaderTest';calls=[];present=True;state='stoppedUP'
binary=ROOT/'work/plugin-host-downloader-20261031/plugin-host-downloader-fixture';worker=binary.read_bytes();manifest=json.loads(subprocess.check_output([str(binary),'--manifest']));digest=hashlib.sha256(worker).hexdigest()
class Repo(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if self.path=='/index':body=json.dumps({'schema_version':1,'plugin_id':ID,'current_version':'1.0','releases':[{'version':'1.0','manifest':manifest,'artifacts':[{'platform':'darwin-arm64','format':'executable','sha256':digest,'executable_sha256':digest,'size':len(worker),'host_contract':'light-media.sdk-resources/v1','url':REPO+'/worker'}]}]}).encode()
  elif self.path=='/worker':body=worker
  else:self.send_error(404);return
  self.send_response(200);self.end_headers();self.wfile.write(body)
class Peer(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):self.serve()
 def do_POST(self):self.serve()
 def serve(self):
  global present,state
  path=self.path.split('?',1)[0];calls.append({'method':self.command,'path':path,'failure':(WORK/'peer-fail').exists()});(WORK/'peer-calls.json').write_text(json.dumps(calls,indent=2)+'\n');self.rfile.read(int(self.headers.get('Content-Length','0')))
  if (WORK/'peer-fail').exists():self.send_error(503);return
  if path.endswith('/info'):body=json.dumps([{'hash':'a'*40,'name':'owned-ui.bin','state':state,'tags':'alpha, light-media','save_path':str(WORK),'progress':1,'amount_left':0,'size':1024}] if present else []).encode()
  elif path.endswith('/webapiVersion'):body=b'2.11.4'
  elif path.endswith('/add'):present=True;body=b'Ok.'
  elif path.endswith('/delete'):present=False;body=b''
  elif path.endswith('/stop') or path.endswith('/pause'):state='stoppedUP';body=b''
  elif path.endswith('/start') or path.endswith('/resume'):state='uploading';body=b''
  elif path.endswith('/recheck'):state='stoppedUP';body=b''
  else:self.send_error(404);return
  self.send_response(200);self.end_headers();self.wfile.write(body)
repo=http.server.ThreadingHTTPServer(('127.0.0.1',39383),Repo);peer=http.server.ThreadingHTTPServer(('127.0.0.1',39382),Peer)
for server in [repo,peer]:threading.Thread(target=server.serve_forever,daemon=True).start()
env=dict(os.environ,MP_SERVER_HOST='',PLUGIN_MARKET='');process=subprocess.Popen([str(ROOT/'work/plugin-host-downloader-20261031/light-media-server-mac'),'--listen','127.0.0.1:39381','--data-dir',str(WORK/'app')],stdout=open(WORK/'server.log','ab',buffering=0),stderr=subprocess.STDOUT,env=env)
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def req(method,path,value=None):
 r=urllib.request.Request(APP+path,data=None if value is None else json.dumps(value).encode(),method=method,headers={'Content-Type':'application/json'});out=json.load(client.open(r));out=out.get('data',out) if isinstance(out,dict) else out;return out
try:
 for _ in range(100):
  try:req('GET','/api/auth/status');break
  except Exception:time.sleep(.1)
 try:req('POST','/api/auth/login',{'username':'acceptance','password':'owned-browser-acceptance-password'})
 except urllib.error.HTTPError:req('POST','/api/auth/setup',{'username':'acceptance','password':'owned-browser-acceptance-password'})
 settings=req('GET','/api/settings');settings['downloaders']=[{'id':'owned-qb','name':'Owned protocol UI peer','kind':'qbittorrent','enabled':True,'base_url':PEER}];settings['default_downloader']='owned-qb';req('PUT','/api/settings',settings);req('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'1.0','force':True})
 print(json.dumps({'ready':APP,'classification':'real browser+App+SDK, synthetic qB protocol peer','worker_sha256':digest}),flush=True)
 event=threading.Event()
 signal.signal(signal.SIGTERM,lambda *_:event.set());signal.signal(signal.SIGINT,lambda *_:event.set());event.wait()
finally:
 process.send_signal(signal.SIGTERM);process.wait(timeout=10)
 for server in [repo,peer]:server.shutdown()
