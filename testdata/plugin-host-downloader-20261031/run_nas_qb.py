"""Isolated real Linux server + SDK worker + owned qBittorrent daemon gate."""
import base64,hashlib,http.cookiejar,http.server,json,os,pathlib,signal,subprocess,sys,threading,time,urllib.request,urllib.error
ROOT=pathlib.Path(sys.argv[1]).resolve()
assert ROOT.name.startswith('.light-media-plugin-downloader-20261031-')
APP='http://127.0.0.1:39281';QB='http://127.0.0.1:39282';REPO='http://127.0.0.1:39283';ID='HostDownloaderTest'
trace=[];current='1.0';server_process=None;materials={};material_fetches=[]
versions={}
for version,file in [('1.0','plugin-host-downloader-fixture-linux'),('2.0','plugin-host-downloader-fixture-v2-linux')]:
 raw=(ROOT/file).read_bytes();manifest=json.loads(subprocess.check_output([str(ROOT/file),'--manifest']))
 digest=hashlib.sha256(raw).hexdigest();versions[version]=(raw,manifest,digest)
class Repo(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if self.path.split('?',1)[0] in materials:
   key=self.path.split('?',1)[0];ok=self.headers.get('Cookie')=='owned=OWNED_HTTP_COOKIE';material_fetches.append({'material':key,'cookie_matches':ok})
   if not ok:self.send_error(403);return
   body=materials[key]
  elif self.path.startswith('/announce?'):body=bencode({b'failure reason':b' rejected OWNED_TRACKER_PRIVATE '})
  elif self.path=='/index':
   releases=[]
   for version,(raw,manifest,digest) in versions.items():
    releases.append({'version':version,'manifest':manifest,'artifacts':[{'platform':'linux-amd64','format':'executable','sha256':digest,'executable_sha256':digest,'size':len(raw),'host_contract':'light-media.sdk-resources/v1','url':REPO+'/'+version}]})
   body=json.dumps({'schema_version':1,'plugin_id':ID,'current_version':current,'releases':releases}).encode()
  elif self.path[1:] in versions:body=versions[self.path[1:]][0]
  else:self.send_error(404);return
  self.send_response(200);self.end_headers();self.wfile.write(body)
repo=http.server.ThreadingHTTPServer(('127.0.0.1',39283),Repo);threading.Thread(target=repo.serve_forever,daemon=True).start()
jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def request(method,path,value=None,status=200):
 data=None if value is None else json.dumps(value).encode()
 req=urllib.request.Request(APP+path,data=data,method=method,headers={'Content-Type':'application/json'})
 try:response=client.open(req,timeout=60)
 except urllib.error.HTTPError as e:response=e
 body=response.read();assert response.status==status,(method,path,response.status,body[:250])
 out=json.loads(body) if body else None
 trace.append({'method':method,'path':path,'status':response.status})
 return out

def manage(method,path,value=None):
 out=request(method,path,value)
 if isinstance(out,dict) and 'data' in out:out=out['data']
 assert not isinstance(out,dict) or out.get('success',True),('business failure',path,out)
 return out

def qb(method,path,form=None):
 import urllib.parse
 req=urllib.request.Request(QB+'/api/v2/'+path,data=None if form is None else urllib.parse.urlencode(form).encode(),method=method)
 return urllib.request.urlopen(req,timeout=10).read()
def wait(predicate,description,seconds=20):
 deadline=time.monotonic()+seconds
 while True:
  result=predicate()
  if result:return result
  assert time.monotonic()<deadline,description
  time.sleep(.15)
def start():
 global server_process
 log=open(ROOT/'server.log','ab',buffering=0)
 env=dict(os.environ,MP_SERVER_HOST='',PLUGIN_MARKET='')
 server_process=subprocess.Popen([str(ROOT/'light-media-server-linux'),'--listen','127.0.0.1:39281','--data-dir',str(ROOT/'app')],stdout=log,stderr=log,env=env)
 def live():
  try:return urllib.request.urlopen(APP+'/api/auth/status',timeout=1).status==200
  except Exception:return False
 wait(live,'server unavailable')
def stop():
 global server_process
 if server_process is not None:
  server_process.send_signal(signal.SIGTERM);server_process.wait(timeout=10);server_process=None

def api(op,value=None,instance=ID,status=200):return request('GET' if op=='snapshot' else 'POST','/api/plugins/'+instance+'/api/'+op,value,status)
def bencode(value):
 if isinstance(value,bytes):return str(len(value)).encode()+b':'+value
 if isinstance(value,int):return b'i'+str(value).encode()+b'e'
 if isinstance(value,dict):return b'd'+b''.join(bencode(k)+bencode(value[k]) for k in sorted(value))+b'e'
 raise TypeError()
summary={'kind':'real_e2e','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','real_dependencies':['Linux production light-media server executable','separately installed Go SDK fixture worker','isolated qBittorrent daemon'],'synthetic_inputs':['owned 65536-byte file and matching no-tracker private torrent'],'status':'failed','server_sha256':hashlib.sha256((ROOT/'light-media-server-linux').read_bytes()).hexdigest(),'worker_sha256':{v:d for v,(_,_,d) in versions.items()},'events':trace}
try:
 start()
 
 if (ROOT/'app'/'light-media.db').stat().st_size > 0:
  try:manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
  except AssertionError:manage('POST','/api/auth/setup',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 else:manage('POST','/api/auth/setup',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 settings=manage('GET','/api/settings')
 settings['downloaders']=[{'id':'owned-qb','kind':'qbittorrent','base_url':QB,'name':'Owned qB acceptance','enabled':True}]
 settings['default_downloader']='owned-qb'
 manage('PUT','/api/settings',settings)
 manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'1.0','force':True})
 source=wait(lambda:api('snapshot').get('pid'),'source fixture not running')
 payload=b'light-media isolated acceptance\n'*2200;payload=payload[:65536]
 file=ROOT/'downloads'/'owned-download.bin';file.write_bytes(payload);os.chmod(file,0o644)
 info={b'length':len(payload),b'name':b'owned-download.bin',b'piece length':16384,b'pieces':b''.join(hashlib.sha1(payload[i:i+16384]).digest() for i in range(0,len(payload),16384)),b'private':1}
 torrent=bencode({b'info':info});hash=hashlib.sha1(bencode(info)).hexdigest();summary['torrent_hash']=hash
 qb('POST','app/setPreferences',{'json':json.dumps({'dht':False,'pex':False,'lsd':False,'upnp':False,'listen_port':39285,'random_port':False})})
 qb('POST','torrents/delete',{'hashes':hash,'deleteFiles':'false'})
 summary['file_sha256']=hashlib.sha256(payload).hexdigest();summary['file_size']=len(payload)
 result=api('add',{'downloader_id':'owned-qb','torrent':base64.b64encode(torrent).decode(),'save_path':'/downloads','paused':True,'tags':['alpha','beta','lookup-owned'],'upload_limit_bytes':32768,'download_limit_bytes':65536})
 assert result['accepted'] and result['outcome']=='confirmed',result
 def tasks():return json.loads(qb('GET','torrents/info?hashes='+hash))
 wait(lambda:tasks(),'task not added')
 assert tasks()[0]['up_limit']==32768 and tasks()[0]['dl_limit']==65536
 assert api('list',{'downloader_id':'owned-qb'})['tasks'][0]['hash']==hash
 def control(action,**extra):
  result=api('control',dict(downloader_id='owned-qb',hashes=[hash],action=action,**extra));assert result['accepted'] and result['outcome']=='confirmed',result
 control('recheck')
 def completed(instance=ID):
  result=api('completed',{'downloader_id':'owned-qb','tags':['alpha']},instance)
  return result['tasks']
 wait(completed,'verified complete task not enumerated',30)
 lookup_started=time.monotonic();lookup=api('find_by_tag',{'downloader_id':'owned-qb','tags':['lookup-owned']});lookup_elapsed=time.monotonic()-lookup_started
 assert lookup['hash']==hash and lookup_elapsed>=3 and 'lookup-owned' not in tasks()[0]['tags'] and 'lookup-owned' not in api('tags',{'downloader_id':'owned-qb'})['tags']
 summary['find_by_tag_elapsed_seconds']=lookup_elapsed
 control('start');wait(lambda:tasks()[0]['state'] not in ['stoppedUP','pausedUP'],'task did not start')
 control('pause');wait(lambda:tasks()[0]['state'] in ['stoppedUP','pausedUP'],'task did not pause')
 control('set_tags',tags=['gamma']);assert 'gamma' in api('tags',{'downloader_id':'owned-qb'})['tags']
 control('remove_tags',tags=['beta']);assert 'beta' not in tasks()[0]['tags']
 api('control',{'downloader_id':'owned-qb','hashes':[hash],'action':'set_category','category':'definitely-missing-owned-category'},status=422)
 if 'owned-category' not in json.loads(qb('GET','torrents/categories')):qb('POST','torrents/createCategory',{'category':'owned-category','savePath':'/downloads'})
 control('set_category',category='owned-category');assert tasks()[0]['category']=='owned-category'
 control('reannounce')
 api('control',{'downloader_id':'owned-qb','hashes':[],'action':'start'},status=422)
 config={'downloader_id':'owned-qb','operation':'completed','payload_json':'{"tags":["alpha"]}','run_once':True}
 manage('PUT','/api/plugins/'+ID+'/config',{'config':config})
 saved=api('snapshot');assert saved['config']['run_once'] is False and saved['observation']['result']['tasks'][0]['hash']==hash,saved
 manage('POST','/api/plugins/'+ID+'/clone',{'suffix':'Twin'})
 clone=ID+'twin';cloned=api('snapshot',instance=clone);assert cloned['pid']!=source and completed(clone)[0]['hash']==hash
 summary['pids']={'source':source,'clone':cloned['pid']}
 manage('POST','/api/plugins/'+ID+'/reload',{})
 reloaded=api('snapshot');assert reloaded['pid']!=source and reloaded['observation']==saved['observation']
 summary['pids']['reload']=reloaded['pid']
 current='2.0';manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'2.0','force':True})
 updated=api('snapshot');assert updated['version']=='2.0' and api('snapshot',instance=clone)['version']=='1.0'
 summary['pids']['updated']=updated['pid']
 stop();start();manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 cold=api('snapshot');cold_clone=api('snapshot',instance=clone);assert cold['pid']!=updated['pid'] and cold_clone['pid']!=cloned['pid']
 assert cold['observation']==saved['observation'] and cold['config']['run_once'] is False
 assert completed()[0]['hash']==hash and completed(clone)[0]['hash']==hash
 summary['pids']['cold_source']=cold['pid'];summary['pids']['cold_clone']=cold_clone['pid']
 control('delete_tags',tags=['gamma']);assert 'gamma' not in api('tags',{'downloader_id':'owned-qb'})['tags']
 qb('POST','torrents/createTags',{'tags':'unused-global-owned'})
 deleted=api('delete_tags',{'downloader_id':'owned-qb','tags':['unused-global-owned']});assert deleted['accepted'] and 'unused-global-owned' not in api('tags',{'downloader_id':'owned-qb'})['tags']
 
 export=ROOT/'backup-export';export.mkdir(exist_ok=True)
 backups={}
 for extension in ['.torrent','.fastresume']:
  candidates=wait(lambda:list((ROOT/'qb-config').rglob(hash+extension)),'owned qB backup not flushed: '+extension,30)
  assert len(candidates)==1
  material=candidates[0].read_bytes();(export/(hash+extension)).write_bytes(material)
  backups[extension]={'sha256':hashlib.sha256(material).hexdigest(),'size':len(material),'path':str(export/(hash+extension))}
 summary['owned_backup_export']=backups
 control('start');qb('POST','torrents/addTrackers',{'hash':hash,'urls':REPO+'/announce?passkey=OWNED_TRACKER_PRIVATE'});control('reannounce')
 def tracker_failure():
  rows=json.loads(qb('GET','torrents/trackers?hash='+hash))
  return next((row for row in rows if row.get('url','').startswith(REPO+'/announce') and 'OWNED_TRACKER_PRIVATE' in row.get('msg','')),None)
 tracker=wait(tracker_failure,'owned local tracker did not return failure',30)
 safe=api('trackers',{'downloader_id':'owned-qb','hash':hash,'error_messages':[tracker['msg'],tracker['msg'].strip()]})
 row=next(row for row in safe['trackers'] if row['endpoint']['display']==REPO)
 assert row['message']['matches'][0] and row['message']['stripped_matches'][1] and len(row['endpoint']['comparison_key'])==64
 assert 'OWNED_TRACKER_PRIVATE' not in json.dumps(safe) and 'passkey=' not in json.dumps(safe)
 summary['real_tracker_private_match_and_redaction']=True
 control('remove_task');wait(lambda:not tasks(),'task not removed')
 assert file.read_bytes()==payload,'task removal changed or deleted data'
 assert completed()==[],'removed task still enumerated'
 assert api('list',{'downloader_id':'owned-qb'})['tasks']==[]

 materials['/material-a.torrent']=torrent
 info_b=dict(info);info_b[b'name']=b'owned-download-url-b.bin';hash_b=hashlib.sha1(bencode(info_b)).hexdigest();materials['/material-b.torrent']=bencode({b'info':info_b});(ROOT/'downloads'/'owned-download-url-b.bin').write_bytes(payload)
 urls=REPO+'/material-a.torrent?passkey=OWNED_URL_PRIVATE\n'+REPO+'/material-b.torrent?passkey=OWNED_URL_PRIVATE'
 added=api('add',{'downloader_id':'owned-qb','url':urls,'cookie':'owned=OWNED_HTTP_COOKIE','save_path':'/downloads','paused':True,'tags':['url-owned']});assert added['accepted']
 def url_tasks():return json.loads(qb('GET','torrents/info'))
 wait(lambda:len(url_tasks())==2,'qB multi-URL material tasks not created',30)
 assert {row['hash'] for row in url_tasks()}=={hash,hash_b} and len(material_fetches)==2 and all(row['cookie_matches'] for row in material_fetches)
 empty=api('add',{'downloader_id':'owned-qb','url':'','cookie':'owned=OWNED_HTTP_COOKIE'});assert empty['accepted'] is False and len(material_fetches)==2
 removed=api('control',{'downloader_id':'owned-qb','hashes':[hash,hash_b],'action':'remove_task'});assert removed['accepted'];wait(lambda:not url_tasks(),'qB URL tasks not removed')
 assert file.read_bytes()==payload and (ROOT/'downloads'/'owned-download-url-b.bin').read_bytes()==payload
 summary['real_url_material_fetches']=material_fetches

 dl_info=dict(info);dl_info[b'name']=b'owned-active-downloading.bin';dl_hash=hashlib.sha1(bencode(dl_info)).hexdigest();dl_torrent=bencode({b'info':dl_info})
 active=api('add',{'downloader_id':'owned-qb','torrent':base64.b64encode(dl_torrent).decode(),'save_path':'/downloads','paused':False,'tags':['active-owned']});assert active['accepted']
 def active_downloading():return api('downloading',{'downloader_id':'owned-qb','tags':['active-owned']})['tasks']
 wait(lambda:any(row['hash']==dl_hash for row in active_downloading()),'qB actual incomplete task not enumerated',20)
 raw_dl=json.loads(qb('POST','torrents/info',{'filter':'downloading'}));assert {row['hash'] for row in api('downloading',{'downloader_id':'owned-qb'})['tasks']}=={row['hash'] for row in raw_dl}
 removed=api('control',{'downloader_id':'owned-qb','hashes':[dl_hash],'action':'remove_task'});assert removed['accepted']
 summary['real_downloading_filter']=True
 missing_started=time.monotonic();missing=api('find_by_tag',{'downloader_id':'owned-qb','tags':['owned-no-such-tag']});missing_elapsed=time.monotonic()-missing_started
 assert missing['hash'] is None and missing_elapsed>=27;summary['missing_tag_elapsed_seconds']=missing_elapsed


 manage('DELETE','/api/plugins/'+ID)
 request('GET','/api/plugins/'+ID+'/api/snapshot',status=404)
 manage('DELETE','/api/plugins/'+clone)
 request('GET','/api/plugins/'+clone+'/api/snapshot',status=404)
 summary['qB_version']=qb('GET','app/version').decode();summary['qB_webapi_version']=qb('GET','app/webapiVersion').decode()
 summary['remaining_daemon_tasks']=len(json.loads(qb('GET','torrents/info')))
 assert summary['remaining_daemon_tasks']==0
 summary['status']='passed'
except Exception as e:
 summary['failure']=str(e);raise
finally:
 stop();repo.shutdown();repo.server_close()
 (ROOT/'result.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
 print(json.dumps({'status':summary['status'],'events':len(trace),'failure':summary.get('failure')},ensure_ascii=False))
