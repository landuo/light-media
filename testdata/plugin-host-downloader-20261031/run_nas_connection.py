"""Real isolated Linux App + standard SDK + auth-enabled qB/TR connection gate.
All passwords are randomly generated in memory, never returned in evidence.
"""
import hashlib,http.cookiejar,http.server,json,os,pathlib,secrets,signal,socket,subprocess,sys,threading,time,urllib.request,urllib.error,urllib.parse,base64
ROOT=pathlib.Path(sys.argv[1]).resolve();assert ROOT.name=='.light-media-plugin-downloader-20261031-5tBuDW69';APP='http://127.0.0.1:39281';REPO='http://127.0.0.1:39283';QB='http://127.0.0.1:39282';TR='http://127.0.0.1:39292/transmission/rpc';ID='HostDownloaderTest';QBNAME='lm-host-downloader-20261031-5tBuDW69';TRNAME='lm-host-tr-20261031-5tBuDW69';QBIMAGE='sha256:8479bc5b568aa65e28b7de384f49c300e07e52856fdd85cfcfcc6dddb48736e4';TRIMAGE='lscr.io/linuxserver/transmission:4.0.6'
qb_password=secrets.token_urlsafe(24);tr_password=secrets.token_urlsafe(24);app_password=secrets.token_urlsafe(24);wrong_password=secrets.token_urlsafe(24);trace=[];containers=[];process=None;repo=None;tr_session=''
worker=(ROOT/'plugin-host-downloader-fixture-linux').read_bytes();manifest=json.loads(subprocess.check_output([str(ROOT/'plugin-host-downloader-fixture-linux'),'--manifest']));digest=hashlib.sha256(worker).hexdigest();jar=http.cookiejar.CookieJar();app_client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar));qb_client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
summary={'kind':'real_Linux_App_SDK_auth_enabled_daemons','status':'failed','server_sha256':hashlib.sha256((ROOT/'light-media-server-linux').read_bytes()).hexdigest(),'worker_sha256':digest,'source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','events':trace,'random_credentials_in_evidence':False,'results':{},'container_environment':[]}
def wait(check,description):
 end=time.monotonic()+40
 while True:
  try:value=check()
  except (OSError,urllib.error.URLError):value=False
  if value:return value
  assert time.monotonic()<end,description;time.sleep(.2)
def app(method,path,value=None,status=200):
 req=urllib.request.Request(APP+path,data=None if value is None else json.dumps(value).encode(),method=method,headers={'Content-Type':'application/json'})
 try:response=app_client.open(req,timeout=30)
 except urllib.error.HTTPError as e:response=e
 body=response.read();assert response.status==status,(path,response.status);out=json.loads(body) if body else None;trace.append({'method':method,'path':path,'status':response.status});return out
def manage(method,path,value=None):
 out=app(method,path,value);out=out.get('data',out) if isinstance(out,dict) else out;assert not isinstance(out,dict) or out.get('success',True),(path,'business rejection');return out
def qb(path,form=None):
 req=urllib.request.Request(QB+'/api/v2/'+path,data=None if form is None else urllib.parse.urlencode(form).encode());return qb_client.open(req,timeout=10).read()
def tr(method,args=None):
 global tr_session
 headers={'Content-Type':'application/json','Authorization':'Basic '+base64.b64encode(('owned:'+tr_password).encode()).decode()}
 if tr_session:headers['X-Transmission-Session-Id']=tr_session
 req=urllib.request.Request(TR,data=json.dumps({'method':method,'arguments':args or {}}).encode(),headers=headers)
 try:response=urllib.request.urlopen(req,timeout=10)
 except urllib.error.HTTPError as e:
  assert e.code==409;tr_session=e.headers['X-Transmission-Session-Id'];return tr(method,args)
 out=json.load(response);assert out['result']=='success';return out['arguments']
class Repo(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if self.path=='/index':body=json.dumps({'schema_version':1,'plugin_id':ID,'current_version':'1.0','releases':[{'version':'1.0','manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','sha256':digest,'executable_sha256':digest,'size':len(worker),'host_contract':'light-media.sdk-resources/v1','url':REPO+'/worker'}]}]}).encode()
  elif self.path=='/worker':body=worker
  else:self.send_error(404);return
  self.send_response(200);self.end_headers();self.wfile.write(body)
try:
 for name in [QBNAME,TRNAME]:assert subprocess.run(['docker','inspect',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode!=0,'owned name already exists'
 for part in ['qb-config/qBittorrent/config','tr-config','downloads','tr-downloads']:(ROOT/part).mkdir(parents=True,exist_ok=True)
 (ROOT/'qb-config/qBittorrent/config/qBittorrent.conf').write_text('[LegalNotice]\nAccepted=true\n[Preferences]\nWebUI\\Address=127.0.0.1\nWebUI\\Port=39282\nWebUI\\LocalHostAuth=false\nConnection\\PortRangeMin=39285\nConnection\\UPnP=false\nBittorrent\\DHT=false\nBittorrent\\PeX=false\nBittorrent\\LSD=false\n')
 (ROOT/'tr-config/settings.json').write_text(json.dumps({'rpc-port':39292,'rpc-bind-address':'127.0.0.1','rpc-authentication-required':True,'rpc-username':'owned','rpc-password':tr_password,'rpc-whitelist':'127.0.0.1','rpc-whitelist-enabled':True,'rpc-host-whitelist':'localhost,127.0.0.1','download-dir':'/downloads','peer-port':39295,'port-forwarding-enabled':False,'dht-enabled':False,'pex-enabled':False,'lpd-enabled':False}))
 subprocess.run(['docker','run','-d','--name',QBNAME,'--network','host','-e','QBT_LEGAL_NOTICE=confirm','-e','QBT_WEBUI_PORT=39282','-v',str(ROOT/'qb-config')+':/config','-v',str(ROOT/'downloads')+':/downloads',QBIMAGE],check=True,stdout=subprocess.DEVNULL);containers.append(QBNAME)
 subprocess.run(['docker','run','-d','--name',TRNAME,'--network','host','--env-file','/dev/stdin','-e','PUID=1000','-e','PGID=1001','-v',str(ROOT/'tr-config')+':/config','-v',str(ROOT/'tr-downloads')+':/downloads',TRIMAGE],input=('USER=owned\nPASS='+tr_password+'\n').encode(),check=True,stdout=subprocess.DEVNULL);containers.append(TRNAME)
 wait(lambda:qb('app/version'),'owned qB startup');wait(lambda:tr('session-get'),'owned TR auth startup')
 prefs=json.loads(qb('app/preferences'));assert 'bypass_local_auth' in prefs and 'web_ui_username' in prefs
 qb('app/setPreferences',{'json':json.dumps({'web_ui_username':'owned','web_ui_password':qb_password,'bypass_local_auth':False,'dht':False,'pex':False,'lsd':False,'upnp':False,'listen_port':39285})})
 assert qb('auth/login',{'username':'owned','password':qb_password}).startswith(b'Ok')
 for url,body in [(QB+'/api/v2/app/version',None),(TR,json.dumps({'method':'session-get','arguments':{}}).encode())]:
  try:urllib.request.urlopen(urllib.request.Request(url,data=body),timeout=5);raise AssertionError('owned auth-required daemon unexpectedly permitted anonymous access')
  except urllib.error.HTTPError as error:assert error.code in (401,403)
 summary['auth_required_independently_verified']=True;summary['daemon_versions']={'qb':qb('app/version').decode(),'qb_webapi':qb('app/webapiVersion').decode(),'tr':tr('session-get')['version']}
 for name in containers:
  row=json.loads(subprocess.check_output(['docker','inspect',name]))[0];summary['container_environment'].append({'name':name,'image_id':row['Image'],'mounts':[{'source':m['Source'],'target':m['Destination']} for m in row['Mounts']]})
 repo=http.server.ThreadingHTTPServer(('127.0.0.1',39283),Repo);threading.Thread(target=repo.serve_forever,daemon=True).start();env=dict(os.environ,MP_SERVER_HOST='',PLUGIN_MARKET='');process=subprocess.Popen([str(ROOT/'light-media-server-linux'),'--listen','127.0.0.1:39281','--data-dir',str(ROOT/'app-connection')],stdout=open(ROOT/'server-connection.log','ab',buffering=0),stderr=subprocess.STDOUT,env=env)
 wait(lambda:urllib.request.urlopen(APP+'/api/auth/status',timeout=1).status==200,'owned App startup');manage('POST','/api/auth/setup',{'username':'owned','password':app_password});settings=manage('GET','/api/settings')
 sock=socket.socket();sock.bind(('127.0.0.1',0));unreachable='http://127.0.0.1:'+str(sock.getsockname()[1]);sock.close()
 settings['downloaders']=[{'id':'qb-good','kind':'qbittorrent','base_url':QB,'username':'owned','password':qb_password,'enabled':True},{'id':'qb-denied','kind':'qbittorrent','base_url':QB,'username':'owned','password':wrong_password,'enabled':True},{'id':'tr-good','kind':'transmission','base_url':TR,'username':'owned','password':tr_password,'enabled':True},{'id':'tr-denied','kind':'transmission','base_url':TR,'username':'owned','password':wrong_password,'enabled':True},{'id':'unreachable','kind':'qbittorrent','base_url':unreachable,'enabled':True},{'id':'disabled','kind':'qbittorrent','base_url':QB,'username':'owned','password':qb_password,'enabled':False}];settings['default_downloader']='qb-good';manage('PUT','/api/settings',settings);manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'1.0','force':True})
 for id,inactive in [('qb-good',False),('qb-denied',True),('tr-good',False),('tr-denied',True),('unreachable',True)]:
  out=app('POST','/api/plugins/'+ID+'/api/connection',{'downloader_id':id});assert out['schema_version']==1 and out['downloader_id']==id and out['inactive'] is inactive,out;encoded=json.dumps(out);assert all(secret not in encoded for secret in [qb_password,tr_password,app_password,wrong_password]);assert set(out)=={'schema_version','downloader_id','kind','inactive'};summary['results'][id]=out
 denied=app('POST','/api/plugins/'+ID+'/api/connection',{'downloader_id':'disabled'},422);assert denied['success'] is False;summary['disabled_rejected']=True
 assert json.loads(qb('torrents/info'))==[] and tr('torrent-get',{'fields':['hashString']})['torrents']==[];summary['no_tasks_created']=True
 config={'downloader_id':'qb-good','operation':'connection','payload_json':'{}','run_once':True};manage('PUT','/api/plugins/'+ID+'/config',{'config':config});snapshot=app('GET','/api/plugins/'+ID+'/api/snapshot');assert snapshot['config']['run_once'] is False and snapshot['observation']['result']['inactive'] is False
 manage('DELETE','/api/plugins/'+ID);app('POST','/api/plugins/'+ID+'/api/connection',{'downloader_id':'qb-good'},404);summary['status']='passed'
except Exception as error:summary['failure_type']=type(error).__name__;raise
finally:
 if process is not None:process.send_signal(signal.SIGTERM);process.wait(timeout=10)
 if repo is not None:repo.shutdown()
 for name in containers:subprocess.run(['docker','rm','-f',name],check=True,stdout=subprocess.DEVNULL)
 summary['owned_containers_removed']=containers;(ROOT/'result-connection.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps({'status':summary['status'],'events':len(trace),'failure_type':summary.get('failure_type')}),flush=True)
