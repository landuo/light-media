"""Incremental actual Linux/App/SDK/qB+TR owned-file deletion acceptance.
Only the explicitly owned temporary directory and daemon endpoints are used.
"""
import base64,hashlib,http.cookiejar,http.server,json,os,pathlib,signal,sqlite3,subprocess,sys,threading,time,urllib.request,urllib.error
ROOT=pathlib.Path(sys.argv[1]).resolve();KIND=sys.argv[2]
assert ROOT.name.startswith('.light-media-plugin-downloader-20261031-') and KIND in ('qb','tr')
APP='http://127.0.0.1:39281';REPO='http://127.0.0.1:39283';ID='HostDownloaderTest';container='lm-host-delete-server-'+KIND+'-5tBuDW69'
DATA=ROOT/('app-delete-'+KIND);DOWNLOADS=ROOT/('downloads' if KIND=='qb' else 'tr-downloads');DATA.mkdir(exist_ok=True)
binary=ROOT/'plugin-host-downloader-fixture-linux';worker=binary.read_bytes();manifest=json.loads(subprocess.check_output([str(binary),'--manifest']));digest=hashlib.sha256(worker).hexdigest()
trace=[];process=None
class Repo(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if self.path=='/index':body=json.dumps({'schema_version':1,'plugin_id':ID,'current_version':'1.0','releases':[{'version':'1.0','manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','sha256':digest,'executable_sha256':digest,'size':len(worker),'host_contract':'light-media.sdk-resources/v1','url':REPO+'/worker'}]}]}).encode()
  elif self.path=='/worker':body=worker
  else:self.send_error(404);return
  self.send_response(200);self.end_headers();self.wfile.write(body)
repo=http.server.ThreadingHTTPServer(('127.0.0.1',39283),Repo);threading.Thread(target=repo.serve_forever,daemon=True).start()
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def req(method,path,value=None,status=200):
 request=urllib.request.Request(APP+path,data=None if value is None else json.dumps(value).encode(),method=method,headers={'Content-Type':'application/json'})
 try:response=client.open(request,timeout=30)
 except urllib.error.HTTPError as e:response=e
 body=response.read();assert response.status==status,(path,response.status,body[:160]);trace.append({'method':method,'path':path,'status':response.status});return json.loads(body) if body else None
def manage(method,path,value=None):
 out=req(method,path,value);out=out.get('data',out) if isinstance(out,dict) else out;assert not isinstance(out,dict) or out.get('success',True);return out
def api(op,value):return req('POST','/api/plugins/'+ID+'/api/'+op,value)
def wait(check,description):
 deadline=time.monotonic()+30
 while True:
  value=check()
  if value:return value
  assert time.monotonic()<deadline,description;time.sleep(.15)
def start():
 global process
 command=['docker','run','--rm','--name',container,'--network','host','--user','1000:1001','--entrypoint',str(ROOT/'light-media-server-linux'),'-e','MP_SERVER_HOST=','-e','PLUGIN_MARKET=','-v',str(ROOT)+':'+str(ROOT),'-v',str(DOWNLOADS)+':/downloads','qbittorrentofficial/qbittorrent-nox:latest','--listen','127.0.0.1:39281','--data-dir',str(DATA)]
 process=subprocess.Popen(command,stdout=open(ROOT/('server-delete-'+KIND+'.log'),'ab',buffering=0),stderr=subprocess.STDOUT)
 def live():
  try:return urllib.request.urlopen(APP+'/api/auth/status',timeout=1).status==200
  except Exception:return False
 wait(live,'isolated container server unavailable')
def stop():
 global process
 if process is not None:
  subprocess.run(['docker','stop','-t','5',container],stdout=subprocess.DEVNULL,check=True);process.wait(timeout=10);process=None
def bencode(v):
 if isinstance(v,bytes):return str(len(v)).encode()+b':'+v
 if isinstance(v,int):return b'i'+str(v).encode()+b'e'
 if isinstance(v,dict):return b'd'+b''.join(bencode(k)+bencode(v[k]) for k in sorted(v))+b'e'
 raise TypeError()
def add(name,tags,variant=None):
 payload=(('owned deletion '+KIND+' '+name+'\n').encode()*4096)[:65536];path=DOWNLOADS/name;path.write_bytes(payload);os.chmod(path,0o644)
 info={b'length':len(payload),b'name':name.encode(),b'piece length':16384,b'pieces':b''.join(hashlib.sha1(payload[i:i+16384]).digest() for i in range(0,len(payload),16384)),b'private':1};
 if variant is not None:info[b'source']=variant.encode()
 hash=hashlib.sha1(bencode(info)).hexdigest()
 out=api('add',{'downloader_id':'owned-'+KIND,'torrent':base64.b64encode(bencode({b'info':info})).decode(),'save_path':'/downloads','paused':True,'tags':tags});assert out['accepted'],out
 out=api('control',{'downloader_id':'owned-'+KIND,'hashes':[hash],'action':'recheck'});assert out['accepted']
 if KIND=='tr':
  time.sleep(.5);out=api('control',{'downloader_id':'owned-'+KIND,'hashes':[hash],'action':'start'});assert out['accepted']
 wait(lambda:next((row for row in api('completed',{'downloader_id':'owned-'+KIND})['tasks'] if row['hash']==hash),None),'completed owned task not verified')
 return hash,path,hashlib.sha256(payload).hexdigest()
def delete(hash,status=200):return req('POST','/api/plugins/'+ID+'/api/delete_files',{'downloader_id':'owned-'+KIND,'hashes':[hash]},status)
summary={'kind':'real_e2e_increment','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','status':'failed','downloader':KIND,'server_sha256':hashlib.sha256((ROOT/'light-media-server-linux').read_bytes()).hexdigest(),'worker_sha256':digest,'events':trace,'synthetic_inputs':['self-created no-tracker private torrent','owned payload files','own temporary DB trigger']}
try:
 start()
 try:manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 except AssertionError:manage('POST','/api/auth/setup',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 settings=manage('GET','/api/settings');settings['downloaders']=[{'id':'owned-'+KIND,'kind':'qbittorrent' if KIND=='qb' else 'transmission','base_url':'http://127.0.0.1:'+('39282' if KIND=='qb' else '39292'),'enabled':True,'name':'Owned '+KIND}];settings['default_downloader']='owned-'+KIND;settings['allowed_roots']=['/downloads'];manage('PUT','/api/settings',settings)
 manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'1.0','force':True})
 settings['downloaders'][0]['enabled']=False;manage('PUT','/api/settings',settings)
 disabled_calls={'completed':{},'downloading':{},'list':{},'tags':{},'trackers':{'hash':'a'*40,'error_messages':[]},'add':{'magnet':'magnet:?xt=urn:btih:'+'a'*40},'control':{'hashes':['a'*40],'action':'start'},'find_by_tag':{'tags':['owned']},'delete_tags':{'tags':['owned']},'delete_files':{'hashes':['a'*40]}}
 for op,value in disabled_calls.items():req('POST','/api/plugins/'+ID+'/api/'+op,dict(downloader_id='owned-'+KIND,**value),422)
 summary['all_new_disabled_public_capabilities_rejected']=len(disabled_calls)
 settings['downloaders'][0]['enabled']=True;manage('PUT','/api/settings',settings)
 foreign,path,digest_file=add('owned-delete-foreign-'+KIND+'.bin',[])
 delete(foreign,422);assert path.exists() and hashlib.sha256(path.read_bytes()).hexdigest()==digest_file
 api('control',{'downloader_id':'owned-'+KIND,'hashes':[foreign],'action':'set_tags','tags':['light-media']})
 settings['allowed_roots']=[];manage('PUT','/api/settings',settings);delete(foreign,422);assert path.exists()
 settings['allowed_roots']=['/downloads'];manage('PUT','/api/settings',settings)
 target=path.with_suffix('.real');path.rename(target);path.symlink_to(target.name);delete(foreign,422);assert target.exists() and path.is_symlink();path.unlink();target.rename(path)
 link=path.with_suffix('.hardlink');os.link(path,link);delete(foreign,422);assert path.exists() and link.exists();link.unlink()
 shared,shared_path,_=add(path.name,['unrelated-owned-test-task'],'shared-task-variant');assert shared!=foreign and shared_path==path
 delete(foreign,422);assert path.exists() and len(api('list',{'downloader_id':'owned-'+KIND})['tasks'])==2
 remove=api('control',{'downloader_id':'owned-'+KIND,'hashes':[shared],'action':'remove_task'});assert remove['accepted'] and path.exists()
 summary['shared_task_and_hardlink_protected']=True
 out=delete(foreign);assert out['daemon_applied'] and out['bookkeeping_completed'] and out['outcome']=='confirmed',out;wait(lambda:not path.exists(),'acknowledged daemon did not remove owned file')
 summary['owned_completed_deleted']=out;summary['rejected_foreign_root_and_symlink_preserved']=True
 fault,fault_path,_=add('owned-delete-db-fault-'+KIND+'.bin',['light-media'])
 db=sqlite3.connect(DATA/'light-media.db');payload=json.dumps({'id':'owned-delete-fault','hash':fault,'downloader_id':'owned-'+KIND,'state':'accepted'})
 db.execute("INSERT INTO records(kind,id,payload,updated_at) VALUES('native_download','owned-delete-fault',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload",(payload,'2026-10-31T00:00:00Z'));db.execute("CREATE TRIGGER owned_delete_failure BEFORE UPDATE ON records WHEN OLD.id='owned-delete-fault' BEGIN SELECT RAISE(ABORT,'owned synthetic bookkeeping failure'); END");db.commit();db.close()
 fault_receipt=delete(fault);assert fault_receipt['daemon_applied'] and fault_receipt['accepted'] and fault_receipt['outcome']=='confirmed' and fault_receipt['bookkeeping_completed'] is False and fault_receipt['failure_code']=='DOWNLOAD_BOOKKEEPING_FAILED',fault_receipt
 wait(lambda:not fault_path.exists(),'daemon actual deletion with local DB failure missing')
 db=sqlite3.connect(DATA/'light-media.db');state=json.loads(db.execute("SELECT payload FROM records WHERE kind='native_download' AND id='owned-delete-fault'").fetchone()[0])['state'];assert state=='accepted';db.execute('DROP TRIGGER owned_delete_failure');db.commit();db.close();summary['daemon_success_local_failure']=fault_receipt
 stop();start();manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'});assert api('list',{'downloader_id':'owned-'+KIND})['tasks']==[] and not path.exists() and not fault_path.exists();summary['cold_no_delete_or_add_replay']=True
 manage('DELETE','/api/plugins/'+ID);req('POST','/api/plugins/'+ID+'/api/delete_files',{'downloader_id':'owned-'+KIND,'hashes':[fault]},404)
 summary['status']='passed'
except Exception as e:summary['failure']=str(e);raise
finally:
 stop();repo.shutdown();(ROOT/('result-delete-'+KIND+'.json')).write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps({'status':summary['status'],'events':len(trace),'failure':summary.get('failure')}))
