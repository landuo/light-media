#!/usr/bin/env python3
"""Real cmd/server + installed SDK OCR/RMT acceptance, only owned synthetic inputs."""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,platform,signal,socket,subprocess,threading,time,urllib.error,urllib.parse,urllib.request,zlib
from pathlib import Path
p=argparse.ArgumentParser()
for key in ['server','worker','directory','rmt-oracle','large-png','large-oracle']:p.add_argument('--'+key,required=True)
p.add_argument('--mutate-restore-bootstrap',action='store_true');p.add_argument('--redirect-source-oracle');p.add_argument('--timeout-oracle');p.add_argument('--cookie-oracle');p.add_argument('--wire-header-oracle');p.add_argument('--rmt-startup-snapshot',action='store_true');p.add_argument('--worker-update');p.add_argument('--hold',action='store_true');p.add_argument('--listen',default='127.0.0.1');p.add_argument('--public-host')
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
   assert self.headers.get('Cookie')==image_cookie_expected and self.headers.get('User-Agent')=='Owned/OCR',(self.headers.get('Cookie'),image_cookie_expected,self.headers.get('User-Agent'))
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
 for _ in range(5000):
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
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
 observations.append(dict(phase='bootstrap-cookie',wire_cookie=image_cookie_expected))
 cookie_source=json.loads(Path(a.cookie_oracle).read_bytes());row=next(row for row in cookie_source['cases']if row['case']=='duplicates_ascii_percent')
 current=request('GET','/api/settings')['data'];assert 'cookie'not in current['sites'][0]
 configured_image_cookie=row['cookie'];current['sites'][0]['cookie']=configured_image_cookie;request('PUT','/api/settings',current)
 image_cookie_expected=base64.b64decode(row['trace'][0]['cookie_bytes_b64']).decode('latin-1')
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
 observations.append(dict(phase='source-duplicate-cookie',wire_cookie=image_cookie_expected))
 saved_cookie=bootstrap['settings']['sites'][0]['cookie']if a.mutate_restore_bootstrap else configured_image_cookie
 current=request('GET','/api/settings')['data'];assert 'cookie'not in current['sites'][0]
 current['sites'][0]['cookie']='owned=initial; other=yes';request('PUT','/api/settings',current)
 source=json.loads(Path(a.redirect_source_oracle).read_bytes());case=next(c for c in source['cases']if c['kind']=='image'and c['redirects']==1)
 redirect_kind='image';redirect_count=1;redirect_trace.clear()
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png',image_base64='ZmFsbGJhY2s='))['value']['result']==case['result']
 assert redirect_trace==case['trace'],(redirect_trace,case['trace'])
 redirect_kind=None;current['sites'][0]['cookie']=saved_cookie;request('PUT','/api/settings',current)
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
 observations.append(dict(phase='configured-cookie-restored',wire_cookie=image_cookie_expected))
 stop();start()
 assert ocr(dict(site_id='owned-image',image_url=repo_origin+'/image.png'))['value']['result']==large['expected_result']
 observations.append(dict(phase='cold-recovered-cookie',wire_cookie=image_cookie_expected))
 success('POST','/api/plugins/OwnedHostBusiness/uninstall');call('settings.read',dict(key='RMT_MEDIAEXT'),status=404);stop()
 result=dict(status='passed',classification='actual product container/server -> standard SDK -> actual owned HTTP; source-derived cookie/redirect contract, test-only synthetic PNG and service',observations=observations,driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),worker_sha256=hashlib.sha256(payload).hexdigest(),mutation=a.mutate_restore_bootstrap)
 (root/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
 (root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+':'+str(error),observations=observations,mutation=a.mutate_restore_bootstrap),indent=2)+'\n');raise
finally:stop();repo.shutdown();repo.server_close();log.close()
