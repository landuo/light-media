#!/usr/bin/env python3
"""Real cmd/server + installed SDK OCR/RMT acceptance, only owned synthetic inputs."""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,platform,secrets,signal,socket,subprocess,threading,time,urllib.error,urllib.parse,urllib.request,zlib
from pathlib import Path
p=argparse.ArgumentParser()
for key in ['server','worker','directory','rmt-oracle','large-png','large-oracle']:p.add_argument('--'+key,required=True)
p.add_argument('--runtime-image');p.add_argument('--redirect-source-oracle');p.add_argument('--timeout-oracle');p.add_argument('--cookie-oracle');p.add_argument('--wire-header-oracle');p.add_argument('--rmt-startup-snapshot',action='store_true');p.add_argument('--worker-update');p.add_argument('--hold',action='store_true');p.add_argument('--listen',default='127.0.0.1');p.add_argument('--public-host')
a=p.parse_args();root=Path(a.directory).resolve();root.mkdir(mode=0o700,parents=True,exist_ok=False);data=root/'data'
worker=Path(a.worker).resolve();payload=worker.read_bytes();manifest=json.loads(subprocess.check_output([str(worker),'--manifest']));assert manifest['plugin_id']=='OwnedHostBusiness'
platform_key={'Linux':'linux','Darwin':'darwin'}[platform.system()]+'-'+{'x86_64':'amd64','arm64':'arm64','aarch64':'arm64'}[platform.machine()]
def release(body,m):return dict(version=m['version'],manifest=m,artifacts=[dict(platform=platform_key,format='executable',sha256=hashlib.sha256(body).hexdigest(),executable_sha256=hashlib.sha256(body).hexdigest(),size=len(body),host_contract='light-media.sdk-resources/v1')])
index=dict(schema_version=1,plugin_id=manifest['plugin_id'],current_version=manifest['version'],releases=[release(payload,manifest)])
update=None
if a.worker_update:
 update=Path(a.worker_update).read_bytes();update_manifest=json.loads(subprocess.check_output([a.worker_update,'--manifest']));assert update_manifest['version']!=manifest['version'];index['releases'].append(release(update,update_manifest))
png=Path(a.large_png).read_bytes();large=json.loads(Path(a.large_oracle).read_bytes());assert hashlib.sha256(png).hexdigest()==large['png_sha256'] and len(png)>128*1024
rmt=json.loads(Path(a.rmt_oracle).read_bytes());default=next(row['expected']for row in rmt['cases']if row['case']=='default');mixed=next(row['expected']for row in rmt['cases']if row['case']=='unsupported_native_member')
initial_extensions=rmt['live_settings']['initial']if a.rmt_startup_snapshot else default
mixed=rmt['live_settings']['after_api']if a.rmt_startup_snapshot else mixed
cold_extensions=rmt['live_settings']['cold']if a.rmt_startup_snapshot else mixed
image_cookie_expected='owned-cookie=1';configured_image_cookie=image_cookie_expected
wire_headers=json.loads(Path(a.wire_header_oracle).read_bytes())['ordinary']if a.wire_header_oracle else None
counts=dict(image=0,ocr=0);ocr_mode='normal';post_observations=[]
redirect_kind=None;redirect_count=0;redirect_trace=[]
class OwnedHTTP(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  path=self.path.split('?',1)[0]
  step=int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('step',['0'])[0])
  if path=='/index':body=json.dumps(index).encode()
  elif path=='/worker':body=payload
  elif path=='/worker-update'and update:body=update
  elif path=='/image.png':
   counts['image']+=1
   if redirect_kind:
    redirect_trace.append(dict(method='GET',step=step,cookie=self.headers.get('Cookie')))
    if redirect_kind=='image'and step<redirect_count:
     self.send_response(307);self.send_header('Location','/image.png?step='+str(step+1));self.send_header('Set-Cookie','owned=redirect; Path=/');self.send_header('Content-Length','0');self.end_headers();return
    body=b'owned-image';self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
   assert self.headers.get('Cookie')==image_cookie_expected and self.headers.get('User-Agent')=='Owned/OCR'
   body=zlib.compress(png);self.send_response(200);self.send_header('Content-Encoding','deflate');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
  else:self.send_error(404);return
  self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_POST(self):
  assert self.path.split('?',1)[0]=='/captcha/base64';counts['ocr']+=1
  step=int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('step',['0'])[0])
  if not redirect_kind:assert not self.headers.get('Cookie') and not self.headers.get('Authorization')
  if wire_headers:
   for key in ['User-Agent','Accept-Encoding','Connection']:assert self.headers.get(key)==wire_headers[key],(key,self.headers.get(key),wire_headers[key])
   assert self.headers.get('Accept')is None
   assert self.headers.get('Content-Type')=='application/json'
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  if redirect_kind:
   redirect_trace.append(dict(method='POST',step=step,cookie=self.headers.get('Cookie'),body=body))
   if redirect_kind=='post'and step<redirect_count:
    self.send_response(307);self.send_header('Location','/captcha/base64?step='+str(step+1));self.send_header('Set-Cookie','owned=redirect; Path=/');self.send_header('Content-Length','0');self.end_headers();return
   raw=b'{"result":"OWNED"}';self.send_response(200);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw);return
  value=body['base64_img'];post_observations.append(dict(base64_bytes=len(value),base64_sha256=hashlib.sha256(value.encode()).hexdigest(),cookie_absent=True,authorization_absent=True))
  if ocr_mode=='failed':self.send_error(503);return
  result='OWNED_LARGE'if hashlib.sha256(value.encode()).hexdigest()==large['post']['base64_sha256']else'ABC123'
  raw=(b'{owned malformed'if ocr_mode=='malformed'else json.dumps(dict(result=result)).encode())
  self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers()
  if ocr_mode=='progress':
   parts=[raw[i:i+3]for i in range(0,len(raw),3)]
   for index,part in enumerate(parts):
    if index:time.sleep(3.5)
    self.wfile.write(part);self.wfile.flush()
  else:self.wfile.write(raw)
 def log_message(self,*_):pass
repo=http.server.ThreadingHTTPServer(('127.0.0.1',0),OwnedHTTP);repo_origin='http://127.0.0.1:'+str(repo.server_port);threading.Thread(target=repo.serve_forever,daemon=True).start()
index['releases'][0]['artifacts'][0]['url']=repo_origin+'/worker'
if update:index['releases'][1]['artifacts'][0]['url']=repo_origin+'/worker-update'
with socket.socket()as sock:sock.bind((a.listen,0));port=sock.getsockname()[1]
origin='http://127.0.0.1:'+str(port);public_origin='http://'+(a.public_host or'127.0.0.1')+':'+str(port)
owned_container='light-media-ocr-proof-'+secrets.token_hex(5)
runtime_image_id=subprocess.check_output(['docker','image','inspect','--format','{{.Id}}',a.runtime_image],text=True).strip()if a.runtime_image else None
env=os.environ|dict(OCR_HOST=repo_origin,OWNED_OCR_CONTAINER=owned_container)
if runtime_image_id:env['OWNED_OCR_IMAGE']=runtime_image_id
for key in ['RMT_MEDIAEXT','RMT_SUBEXT','RMT_AUDIOEXT','HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:env.pop(key,None)
if a.rmt_startup_snapshot:env['RMT_MEDIAEXT']=json.dumps(initial_extensions)
bootstrap=dict(username='owned-ocr',password='owned-ocr-browser-password',settings=dict(plugin_markets=[repo_origin+'/index'],plugin_server_url='',plugin_statistic_share=False,sites=[dict(id='owned-image',name='Owned synthetic image',kind='nexusphp',base_url=repo_origin,cookie='owned-cookie=1',ua='Owned/OCR',enabled=False)]))

# Observe the parent/tool boundary before and after each mutating API and cold
# lifecycle step. Evidence is streamed outside the mounted tree before asserting.
integrity_observations=[]
integrity_files={str(Path(__file__).resolve()):hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
for key,value in vars(a).items():
 if isinstance(value,str) and Path(value).is_file():integrity_files[str(Path(value).resolve())]=hashlib.sha256(Path(value).read_bytes()).hexdigest()
sentinel=root.parent/'.owned-sentinel'
integrity_files[str(sentinel)]=hashlib.sha256(sentinel.read_bytes()).hexdigest()
def integrity(phase):
 missing=[];changed=[]
 for name,expected in integrity_files.items():
  file=Path(name)
  if not file.is_file():missing.append(name)
  elif hashlib.sha256(file.read_bytes()).hexdigest()!=expected:changed.append(name)
 record=dict(phase=phase,parent=str(root.parent),parent_present=root.parent.is_dir(),data_dir=str(data),data_dir_present=data.is_dir(),files_checked=len(integrity_files),missing=missing,changed=changed)
 integrity_observations.append(record);print(json.dumps(dict(integrity=record)),flush=True)
 assert record['parent_present'] and not missing and not changed,record
integrity('before-bootstrap')
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
integrity('after-bootstrap')
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));process=None;log=(root/'server.log').open('wb');observations=[]
def request(method,path,value=None,status=200):
 if method!='GET':integrity('before '+method+' '+path)
 raw=json.dumps(value).encode()if value is not None else None;req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json'},method=method)
 try:res=client.open(req,timeout=30)
 except urllib.error.HTTPError as error:res=error
 body=res.read()
 if method!='GET':integrity('after '+method+' '+path)
 assert res.code==status,(method,path,res.code,status,body[:500]);return json.loads(body)if body else None
def success(method,path,value=None):
 result=request(method,path,value);assert result['data']['success'],result;return result['data']
def start():
 global process
 integrity('before-start')
 process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],env=env,stdout=log,stderr=log)
 for _ in range(5000):
  try:request('GET','/api/auth/status');break
  except(OSError,AssertionError):assert process.poll()is None;time.sleep(.02)
 else:raise AssertionError('server did not start')
 integrity('after-start')
 if a.runtime_image:
  mounts=json.loads(subprocess.check_output(['docker','inspect','--format','{{json .Mounts}}',owned_container],text=True));assert any(m.get('Source')==str(root.parent)and m.get('Destination')==str(root.parent)for m in mounts),mounts
  print(json.dumps(dict(actual_container=owned_container,actual_bind_mounts=mounts,data_dir_argument=str(data))),flush=True)
 request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop(check_integrity=True):
 global process
 if process is not None:
  if check_integrity:integrity('before-stop')
  process.send_signal(signal.SIGTERM);assert process.wait(timeout=60)==0;process=None
  if check_integrity:integrity('after-stop')
def call(method,value,instance='OwnedHostBusiness',status=200):return request('POST','/api/v1/plugin/'+instance+'/run',dict(method=method,input=value),status)
def ocr(value,instance='OwnedHostBusiness',status=200):return call('ocr.captcha.text',value,instance,status)
def snapshot(instance='OwnedHostBusiness'):return call('settings.read',dict(key='RMT_MEDIAEXT'),instance)
try:
 start();success('POST','/api/plugins/install',dict(plugin_id=manifest['plugin_id'],repo_url=repo_origin+'/index'))
 assert call('settings.read',dict(key='RMT_MEDIAEXT'))['value']['value']==initial_extensions
 settings=request('GET','/api/settings')['data'];settings['rmt_mediaext']=mixed;request('PUT','/api/settings',settings)
 assert call('settings.read',dict(key='RMT_MEDIAEXT'))['value']['value']==mixed
 call('settings.write',dict(key='RMT_MEDIAEXT',value=['.forged']),status=502)
 assert call('settings.read',dict(key='RMT_MEDIAEXT'))['value']['value']==mixed
 assert ocr(dict(image_base64=' Y W\nJjZA '))['value']['result']=='ABC123'
 assert ocr(dict(image_base64=base64.b64encode(png).decode()))['value']['result']==large['expected_result']
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png',image_base64='fallback'))['value']['result']==large['expected_result'];assert counts['image']==1
 if a.cookie_oracle:
  cookie_source=json.loads(Path(a.cookie_oracle).read_bytes());row=next(row for row in cookie_source['cases']if row['case']=='duplicates_ascii_percent')
  current=request('GET','/api/settings')['data'];current['sites'][0]['cookie']=row['cookie'];configured_image_cookie=row['cookie'];request('PUT','/api/settings',current)
  image_cookie_expected=base64.b64decode(row['trace'][0]['cookie_bytes_b64']).decode('latin-1')
  assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
  observations.append(dict(phase='actual-cookie-parse-source-wire',source_oracle_sha256=hashlib.sha256(Path(a.cookie_oracle).read_bytes()).hexdigest(),duplicate_overwrite_ascii_percent_and_order=True,header_matches_actual_source_http=True))
 before=counts.copy();ocr(dict(site_id='owned-image',image_url='http://127.0.0.1:1/foreign'),status=502);assert counts==before
 if a.timeout_oracle:
  timeout_source=json.loads(Path(a.timeout_oracle).read_bytes());ocr_mode='progress';began=time.monotonic()
  assert ocr(dict(image_base64='YWJj'))['value']['result']=='ABC123'
  elapsed=time.monotonic()-began;assert elapsed>20,elapsed
  observations.append(dict(phase='actual-sdk-20s-read-inactivity-not-total',elapsed_seconds=elapsed,source_timeout_oracle_sha256=hashlib.sha256(Path(a.timeout_oracle).read_bytes()).hexdigest(),result='ABC123'))
 ocr_mode='failed';assert ocr(dict(image_base64='YWJj'))['value']['result']==''
 ocr_mode='malformed';ocr(dict(image_base64='YWJj'),status=502)
 ocr_mode='normal';assert ocr(dict(image_base64='YWJj'))['value']['result']=='ABC123'
 if a.redirect_source_oracle:
  source=json.loads(Path(a.redirect_source_oracle).read_bytes());assert source['source_commit']=='8a83efeacd3e26b82be495e17d50146e31c96a60'and len(source['cases'])==5
  settings_before=request('GET','/api/settings')['data'];saved_cookie=configured_image_cookie;settings_before['sites'][0]['cookie']='owned=initial; other=yes';request('PUT','/api/settings',settings_before)
  actual_cases=[]
  for source_case in source['cases']:
   redirect_kind=source_case['kind'];redirect_count=source_case['redirects'];redirect_trace.clear();value=dict(image_base64='ZmFsbGJhY2s=')
   if redirect_kind=='image':value.update(site_id='owned-image',image_url=repo_origin+'/image.png')
   got=ocr(value)['value']['result'];assert got==source_case['result']and redirect_trace==source_case['trace'],(source_case,got,redirect_trace)
   actual_cases.append(dict(kind=redirect_kind,redirects=redirect_count,result=got,actual_trace=list(redirect_trace)))
  redirect_kind=None;settings_before['sites'][0]['cookie']=saved_cookie;request('PUT','/api/settings',settings_before)
  observations.append(dict(phase='actual-product-full-fixed-OCR-redirect-source',source_sha256=hashlib.sha256(Path(a.redirect_source_oracle).read_bytes()).hexdigest(),cases=actual_cases))
 success('PUT','/api/plugins/OwnedHostBusiness',dict(marker='OCR init ran',method='ocr.captcha.text',input=json.dumps(dict(image_base64='YWJj')),run_once=True))
 page=request('GET','/api/plugins/OwnedHostBusiness/page')['data'];assert 'ABC123'in json.dumps(page),page
 success('POST','/api/plugins/OwnedHostBusiness/clone',dict(suffix='Twin'));clone='OwnedHostBusinesstwin'
 source_pid=snapshot()['pid'];clone_pid=snapshot(clone)['pid'];assert source_pid!=clone_pid
 assert call('settings.read',dict(key='RMT_MEDIAEXT'),clone)['value']['value']==mixed
 assert ocr(dict(image_base64='YWJj'),clone)['value']['result']=='ABC123'
 if update:
  success('POST','/api/plugins/install',dict(plugin_id=manifest['plugin_id'],repo_url=repo_origin+'/index',release_version=update_manifest['version'],force=True))
  updated_pid=snapshot()['pid'];assert updated_pid!=source_pid and snapshot(clone)['pid']==clone_pid
  assert ocr(dict(image_base64='YWJj'))['value']['result']=='ABC123'
 else:updated_pid=source_pid
 if a.rmt_startup_snapshot:env['RMT_MEDIAEXT']=json.dumps(cold_extensions)
 stop();start();cold_pid=snapshot()['pid'];assert cold_pid!=updated_pid
 assert call('settings.read',dict(key='RMT_MEDIAEXT'))['value']['value']==cold_extensions
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
 observations.append(dict(phase='real-ocr-rmt',default_extensions=default,observed_initial_extensions=initial_extensions,raw_mixed_extensions=mixed,observed_cold_extensions=cold_extensions,startup_environment_snapshot=a.rmt_startup_snapshot,readonly_write_rejected=True,source_pid=source_pid,clone_pid=clone_pid,updated_pid=updated_pid,cold_pid=cold_pid,image_cookie_ua_retained=True,ocr_receives_no_site_credentials=True,large_png_bytes=len(png),large_base64_bytes=large['post']['base64_length'],compressed_image_decoded=True,source_falsey_and_exception_paths=True,configuration_init_run_once_calls_sdk=True,counts=counts.copy()))
 success('PUT','/api/plugins/OwnedHostBusiness',dict(marker='OCR browser',method='ocr.captcha.text',input=json.dumps(dict(image_base64='YWJj')),run_once=False))
 if a.hold:
  (root/'browser-state.json').write_text(json.dumps(dict(origin=public_origin,local_origin=origin,username=bootstrap['username'],password=bootstrap['password'],plugin='OwnedHostBusiness',clone=clone,method='ocr.captcha.text',input=json.dumps(dict(image_base64='YWJj')),source_pid=cold_pid),indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=public_origin)),flush=True)
  while not(root/'.browser-done').exists():assert process.poll()is None;time.sleep(.2)
 uninstalled=[]
 for instance in [clone,'OwnedHostBusiness']:
  ack=success('POST','/api/plugins/'+instance+'/uninstall');gone=call('settings.read',dict(key='RMT_MEDIAEXT'),instance,status=404)
  uninstalled.append(dict(instance=instance,uninstall_response=ack,subsequent_custom_api_status=404,subsequent_custom_api_response=gone))
 cleanup=dict(uninstalled=uninstalled)
 if a.runtime_image:
  actual_image=subprocess.check_output(['docker','inspect','--format','{{.Image}}',owned_container],text=True).strip();assert actual_image==runtime_image_id
  native_sha=subprocess.check_output(['docker','exec',owned_container,'sha256sum','/usr/local/bin/light-media'],text=True).split()[0]
  processes=subprocess.check_output(['docker','top',owned_container,'-eo','pid,ppid,stat,comm'],text=True).splitlines()
  assert not any('plugin-host-'in line or'chrome'in line or'Xvfb'in line for line in processes),processes
  profiles=list((data/'work'/'browser').glob('*'));assert not profiles,profiles
  cleanup.update(image_id=actual_image,server_sha256=native_sha,processes_after_uninstall=processes,plugin_worker_browser_display_processes=0,temp_profiles=0)
 stop()
 if a.runtime_image:
  remaining=subprocess.check_output(['docker','ps','--all','--filter','name=^/'+owned_container+'$','--format','{{.Names}}'],text=True).strip();assert not remaining,remaining
  cleanup.update(container_name=owned_container,container_absent=True,data_directory_retained_for_evidence=True)
 bindings={key:hashlib.sha256(Path(value).read_bytes()).hexdigest()for key,value in dict(server=a.server,worker=a.worker,driver=__file__,rmt_oracle=a.rmt_oracle,large_png=a.large_png,large_oracle=a.large_oracle).items()}
 if a.redirect_source_oracle:bindings['full_redirect_source_oracle']=hashlib.sha256(Path(a.redirect_source_oracle).read_bytes()).hexdigest()
 if a.wire_header_oracle:bindings['wire_headers_source_oracle']=hashlib.sha256(Path(a.wire_header_oracle).read_bytes()).hexdigest()
 if a.cookie_oracle:bindings['cookie_source_oracle']=hashlib.sha256(Path(a.cookie_oracle).read_bytes()).hexdigest()
 if a.timeout_oracle:bindings['timeout_source_oracle']=hashlib.sha256(Path(a.timeout_oracle).read_bytes()).hexdigest()
 if a.worker_update:bindings['worker_update']=hashlib.sha256(Path(a.worker_update).read_bytes()).hexdigest()
 (root/'result.json').write_text(json.dumps(dict(status='passed',classification='actual cmd/server + installed out-of-process standard SDK worker on owned SQLite; source expected ordinary OCR/RMT protocol and synthetic PNG; no live site/OCR challenge/provider',bindings=bindings,observations=observations,parent_tool_integrity=integrity_observations,protected_tool_sha256=integrity_files,ocr_post_observations=post_observations,cleanup=cleanup),indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
 print(json.dumps(dict(failure=type(error).__name__+':'+str(error),parent_tool_integrity=integrity_observations)),flush=True)
 if root.is_dir():(root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+':'+str(error),counts=counts,observations=observations,ocr_post_observations=post_observations),indent=2)+'\n');raise
finally:stop(check_integrity=False);repo.shutdown();repo.server_close();log.close()
