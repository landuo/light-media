"""Isolated real Linux server + SDK worker + owned qBittorrent daemon gate."""
import base64,hashlib,http.cookiejar,http.server,json,os,pathlib,signal,subprocess,sys,threading,time,urllib.request,urllib.error
ROOT=pathlib.Path(sys.argv[1]).resolve()
assert ROOT.name.startswith('.light-media-plugin-downloader-20261031-')
APP='http://127.0.0.1:39281';QB='http://127.0.0.1:39292';REPO='http://127.0.0.1:39283';ID='HostDownloaderTest'
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

session=''
def tr(method,arguments=None):
 global session
 for attempt in range(3):
  payload=json.dumps({'method':method,'arguments':arguments or {}}).encode()
  req=urllib.request.Request(QB+'/transmission/rpc',data=payload,headers={'Content-Type':'application/json','X-Transmission-Session-Id':session})
  try:res=urllib.request.urlopen(req,timeout=10)
  except urllib.error.HTTPError as e:res=e
  if res.status==409:session=res.headers['X-Transmission-Session-Id'];continue
  result=json.loads(res.read());assert res.status==200 and result['result']=='success',result;return result['arguments']
 raise AssertionError('isolated TR session unavailable')
summary={'kind':'real_e2e','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','status':'failed','server_sha256':hashlib.sha256((ROOT/'light-media-server-linux').read_bytes()).hexdigest(),'worker_sha256':{v:d for v,(_,_,d) in versions.items()},'events':trace,'real_dependencies':['Linux production light-media server','installed standard Go SDK worker','isolated Transmission daemon'],'synthetic_inputs':['owned no-tracker torrent and exact bytes']}
try:
 start();manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 settings=manage('GET','/api/settings');settings['downloaders']=[{'id':'owned-tr','kind':'transmission','base_url':QB,'name':'Owned Transmission acceptance','enabled':True}];settings['default_downloader']='owned-tr';manage('PUT','/api/settings',settings)
 manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'1.0','force':True})
 source=api('snapshot')['pid']
 payload=(b'isolated Transmission acceptance\n'*2200)[:65536]
 file=ROOT/'tr-downloads'/'owned-tr.bin';file.write_bytes(payload);os.chmod(file,0o644)
 info={b'length':len(payload),b'name':b'owned-tr.bin',b'piece length':16384,b'pieces':b''.join(hashlib.sha1(payload[i:i+16384]).digest() for i in range(0,len(payload),16384)),b'private':1}
 torrent=bencode({b'info':info});hash=hashlib.sha1(bencode(info)).hexdigest();summary['torrent_hash']=hash;summary['file_sha256']=hashlib.sha256(payload).hexdigest();summary['file_size']=len(payload)
 tr('torrent-remove',{'ids':[hash],'delete-local-data':False})
 result=api('add',{'downloader_id':'owned-tr','torrent':base64.b64encode(torrent).decode(),'save_path':'/downloads','paused':True,'tags':['alpha']});assert result['accepted'],result
 def tasks():return tr('torrent-get',{'ids':[hash],'fields':['hashString','name','status','percentDone','leftUntilDone','labels','uploadLimit','uploadLimited','downloadLimit','downloadLimited']})['torrents']
 wait(tasks,'TR task not added')
 def control(action,**extra):
  result=api('control',dict(downloader_id='owned-tr',hashes=[hash],action=action,**extra));assert result['accepted'] and result['outcome']=='confirmed',result
 def completed(instance=ID):return api('completed',{'downloader_id':'owned-tr'},instance)['tasks']
 control('recheck');wait(lambda:tasks()[0]['percentDone']==1 and tasks()[0]['status']==0,'TR paused verification not complete',30)
 assert completed()==[],'TR stopped-completed was wrongly enumerated; source accepts status5/6 only'
 assert api('list',{'downloader_id':'owned-tr'})['tasks'][0]['hash']==hash
 control('set_limits',upload_limit_kb=32,download_limit_kb=0)
 assert tasks()[0]['uploadLimit']==32 and tasks()[0]['uploadLimited'] is True and tasks()[0]['downloadLimit']==0 and tasks()[0]['downloadLimited'] is False
 control('start');wait(lambda:tasks()[0]['status']==6,'TR task did not start seeding')
 assert completed()[0]['hash']==hash
 control('set_tags',tags=['alpha','alpha'],original_tags=[' existing ','alpha']);assert sorted(tasks()[0]['labels'])==['alpha','existing']
 assert sorted(api('tags',{'downloader_id':'owned-tr','hash':hash})['tags'])==['alpha','existing']
 empty=api('control',{'downloader_id':'owned-tr','hashes':[hash],'action':'set_tags','tags':[]});assert empty['accepted'] is False and sorted(tasks()[0]['labels'])==['alpha','existing']
 api('control',{'downloader_id':'owned-tr','hashes':[hash],'action':'set_category','category':'unsupported'},status=422)
 api('find_by_tag',{'downloader_id':'owned-tr','tags':['alpha']},status=422)
 api('delete_tags',{'downloader_id':'owned-tr','tags':['alpha']},status=422)
 control('pause');wait(lambda:tasks()[0]['status']==0,'TR task did not pause');assert completed()==[]
 control('start');wait(lambda:tasks()[0]['status']==6,'TR task did not resume')
 config={'downloader_id':'owned-tr','operation':'completed','payload_json':'{}','run_once':True};manage('PUT','/api/plugins/'+ID+'/config',{'config':config});saved=api('snapshot');assert saved['config']['run_once'] is False and saved['observation']['result']['tasks'][0]['hash']==hash
 manage('POST','/api/plugins/'+ID+'/clone',{'suffix':'Twin'});clone=ID+'twin';cloned=api('snapshot',instance=clone);assert cloned['pid']!=source and completed(clone)[0]['hash']==hash
 summary['pids']={'source':source,'clone':cloned['pid']}
 current='2.0';manage('POST','/api/plugins/install',{'plugin_id':ID,'repo_url':REPO+'/index','release_version':'2.0','force':True});updated=api('snapshot');assert updated['version']=='2.0' and api('snapshot',instance=clone)['version']=='1.0';summary['pids']['updated']=updated['pid']
 stop();start();manage('POST','/api/auth/login',{'username':'acceptance','password':'owned-downloader-acceptance-password'})
 cold=api('snapshot');cold_clone=api('snapshot',instance=clone);assert cold['pid']!=updated['pid'] and cold_clone['pid']!=cloned['pid'] and cold['observation']==saved['observation'] and cold['config']['run_once'] is False
 assert completed()[0]['hash']==hash and completed(clone)[0]['hash']==hash;summary['pids']['cold_source']=cold['pid'];summary['pids']['cold_clone']=cold_clone['pid']
 tr('torrent-set',{'ids':[hash],'trackerAdd':[REPO+'/announce?passkey=OWNED_TRACKER_PRIVATE']});tr('torrent-reannounce',{'ids':[hash]})
 def tracker_failure():
  rows=tr('torrent-get',{'ids':[hash],'fields':['trackerStats']})['torrents']
  return next((row for row in rows[0]['trackerStats'] if 'OWNED_TRACKER_PRIVATE' in row.get('lastAnnounceResult','')),None)
 tracker=wait(tracker_failure,'owned TR local tracker did not return failure',30)
 safe=api('trackers',{'downloader_id':'owned-tr','hash':hash,'error_messages':[tracker['lastAnnounceResult'],tracker['lastAnnounceResult'].strip()]})
 row=safe['tracker_stats'][0];assert row['message']['matches'][0] and row['message']['stripped_matches'][1] and len(row['endpoint']['comparison_key'])==64
 assert 'OWNED_TRACKER_PRIVATE' not in json.dumps(safe) and 'passkey=' not in json.dumps(safe)
 summary['real_tracker_private_match_and_redaction']=True
 control('remove_task');wait(lambda:not tasks(),'TR task not removed');assert file.read_bytes()==payload,'TR task-only removal changed owned payload';assert completed()==[]

 materials['/material-tr.torrent']=torrent
 added=api('add',{'downloader_id':'owned-tr','url':REPO+'/material-tr.torrent?passkey=OWNED_URL_PRIVATE','cookie':'owned=OWNED_HTTP_COOKIE','save_path':'/downloads','paused':True,'tags':['url-owned']});assert added['accepted'] and added['hash']==hash
 wait(tasks,'TR URL material task not created',30);assert len(material_fetches)==1 and material_fetches[0]['cookie_matches']
 empty=api('add',{'downloader_id':'owned-tr','url':'','cookie':'owned=OWNED_HTTP_COOKIE'},status=422);assert empty['success'] is False and len(material_fetches)==1 and len(tasks())==1 and tasks()[0]['hashString']==hash
 file_uri=api('add',{'downloader_id':'owned-tr','url':'file:///owned/forbidden.torrent'});assert file_uri['accepted'] is False and len(material_fetches)==1
 control('remove_task');wait(lambda:not tasks(),'TR URL task not removed');assert file.read_bytes()==payload
 summary['real_url_material_fetches']=material_fetches

 dl_info=dict(info);dl_info[b'name']=b'owned-tr-active-downloading.bin';dl_hash=hashlib.sha1(bencode(dl_info)).hexdigest();dl_torrent=bencode({b'info':dl_info})
 active=api('add',{'downloader_id':'owned-tr','torrent':base64.b64encode(dl_torrent).decode(),'save_path':'/downloads','paused':False,'tags':['active-owned']});assert active['accepted']
 def active_downloading():return api('downloading',{'downloader_id':'owned-tr','tags':['active-owned']})['tasks']
 wait(lambda:any(row['hash']==dl_hash for row in active_downloading()),'TR actual status3/4 task not enumerated',20)
 raw_dl=tr('torrent-get',{'fields':['hashString','status']})['torrents'];assert {row['hash'] for row in api('downloading',{'downloader_id':'owned-tr'})['tasks']}=={row['hashString'] for row in raw_dl if row['status'] in [3,4]}
 removed=api('control',{'downloader_id':'owned-tr','hashes':[dl_hash],'action':'remove_task'});assert removed['accepted']
 summary['real_downloading_filter']=True


 manage('DELETE','/api/plugins/'+ID);request('GET','/api/plugins/'+ID+'/api/snapshot',status=404);manage('DELETE','/api/plugins/'+clone);request('GET','/api/plugins/'+clone+'/api/snapshot',status=404)
 summary['transmission_version']=tr('session-get')['version'];summary['remaining_daemon_tasks']=len(tr('torrent-get',{'fields':['hashString']})['torrents']);assert summary['remaining_daemon_tasks']==0;summary['status']='passed'
except Exception as e:summary['failure']=str(e);raise
finally:
 stop();repo.shutdown();repo.server_close();(ROOT/'result-tr.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'status':summary['status'],'events':len(trace),'failure':summary.get('failure')},ensure_ascii=False))
