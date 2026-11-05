#!/usr/bin/env python3
"""Real cmd/server + installed SDK OCR/RMT acceptance, only owned synthetic inputs."""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,platform,signal,socket,subprocess,threading,time,urllib.error,urllib.request,zlib
from pathlib import Path
p=argparse.ArgumentParser()
for key in ['server','worker','directory','rmt-oracle','large-png','large-oracle']:p.add_argument('--'+key,required=True)
p.add_argument('--timeout-oracle');p.add_argument('--cookie-oracle');p.add_argument('--wire-header-oracle');p.add_argument('--rmt-startup-snapshot',action='store_true');p.add_argument('--worker-update');p.add_argument('--hold',action='store_true');p.add_argument('--listen',default='127.0.0.1');p.add_argument('--public-host')
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
image_cookie_expected='owned-cookie=1'
wire_headers=json.loads(Path(a.wire_header_oracle).read_bytes())['ordinary']if a.wire_header_oracle else None
counts=dict(image=0,ocr=0);ocr_mode='normal';post_observations=[]
class OwnedHTTP(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  path=self.path.split('?',1)[0]
  if path=='/index':body=json.dumps(index).encode()
  elif path=='/worker':body=payload
  elif path=='/worker-update'and update:body=update
  elif path=='/image.png':
   counts['image']+=1;assert self.headers.get('Cookie')==image_cookie_expected and self.headers.get('User-Agent')=='Owned/OCR'
   body=zlib.compress(png);self.send_response(200);self.send_header('Content-Encoding','deflate');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body);return
  else:self.send_error(404);return
  self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_POST(self):
  assert self.path=='/captcha/base64';counts['ocr']+=1
  assert not self.headers.get('Cookie') and not self.headers.get('Authorization')
  if wire_headers:
   for key in ['User-Agent','Accept-Encoding','Connection']:assert self.headers.get(key)==wire_headers[key],(key,self.headers.get(key),wire_headers[key])
   assert self.headers.get('Accept')is None
   assert self.headers.get('Content-Type')=='application/json'
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
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
env=os.environ|dict(OCR_HOST=repo_origin)
for key in ['RMT_MEDIAEXT','RMT_SUBEXT','RMT_AUDIOEXT','HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:env.pop(key,None)
if a.rmt_startup_snapshot:env['RMT_MEDIAEXT']=json.dumps(initial_extensions)
bootstrap=dict(username='owned-ocr',password='owned-ocr-browser-password',settings=dict(plugin_markets=[repo_origin+'/index'],plugin_server_url='',plugin_statistic_share=False,sites=[dict(id='owned-image',name='Owned synthetic image',kind='nexusphp',base_url=repo_origin,cookie='owned-cookie=1',ua='Owned/OCR',enabled=False)]))
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));process=None;log=(root/'server.log').open('wb');observations=[]
def request(method,path,value=None,status=200):
 raw=json.dumps(value).encode()if value is not None else None;req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json'},method=method)
 try:res=client.open(req,timeout=30)
 except urllib.error.HTTPError as error:res=error
 body=res.read();assert res.code==status,(method,path,res.code,status,body[:500]);return json.loads(body)if body else None
def success(method,path,value=None):
 result=request(method,path,value);assert result['data']['success'],result;return result['data']
def start():
 global process
 process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],env=env,stdout=log,stderr=log)
 for _ in range(600):
  try:request('GET','/api/auth/status');break
  except(OSError,AssertionError):assert process.poll()is None;time.sleep(.02)
 else:raise AssertionError('server did not start')
 request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop():
 global process
 if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
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
  current=request('GET','/api/settings')['data'];current['sites'][0]['cookie']=row['cookie'];request('PUT','/api/settings',current)
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
 for instance in [clone,'OwnedHostBusiness']:success('POST','/api/plugins/'+instance+'/uninstall');call('settings.read',dict(key='RMT_MEDIAEXT'),instance,status=404)
 stop()
 bindings={key:hashlib.sha256(Path(value).read_bytes()).hexdigest()for key,value in dict(server=a.server,worker=a.worker,driver=__file__,rmt_oracle=a.rmt_oracle,large_png=a.large_png,large_oracle=a.large_oracle).items()}
 if a.wire_header_oracle:bindings['wire_headers_source_oracle']=hashlib.sha256(Path(a.wire_header_oracle).read_bytes()).hexdigest()
 if a.cookie_oracle:bindings['cookie_source_oracle']=hashlib.sha256(Path(a.cookie_oracle).read_bytes()).hexdigest()
 if a.timeout_oracle:bindings['timeout_source_oracle']=hashlib.sha256(Path(a.timeout_oracle).read_bytes()).hexdigest()
 if a.worker_update:bindings['worker_update']=hashlib.sha256(Path(a.worker_update).read_bytes()).hexdigest()
 (root/'result.json').write_text(json.dumps(dict(status='passed',classification='actual cmd/server + installed out-of-process standard SDK worker on owned SQLite; source expected ordinary OCR/RMT protocol and synthetic PNG; no live site/OCR challenge/provider',bindings=bindings,observations=observations,ocr_post_observations=post_observations),indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
 (root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+':'+str(error),counts=counts,observations=observations,ocr_post_observations=post_observations),indent=2)+'\n');raise
finally:stop();repo.shutdown();repo.server_close();log.close()
