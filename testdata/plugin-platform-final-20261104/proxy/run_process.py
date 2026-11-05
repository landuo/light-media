#!/usr/bin/env python3
"""Actual isolated Linux cmd/server + compiled workers and owned proxy/target."""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,pathlib,platform,signal,socket,subprocess,threading,time,urllib.error,urllib.parse,urllib.request
p=argparse.ArgumentParser()
for key in ['server','worker','worker-v2','directory']:p.add_argument('--'+key,required=True)
p.add_argument('--hold',action='store_true');a=p.parse_args();root=pathlib.Path(a.directory).resolve();root.mkdir(mode=0o700,parents=True,exist_ok=False)
observations=[];wire=[];lock=threading.Lock();process=None;serverlog=None
# Synthetic proxy authentication is kept only in this owned test service.
proxy_events=[]
class Target(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  body=self.rfile.read(int(self.headers.get('Content-Length',0)));content=b'owned-native:'+body
  with lock:wire.append(dict(body=body.decode(),method=self.command,private_proxy_header_absent=self.headers.get('Proxy-Authorization') is None))
  self.send_response(200);self.send_header('Content-Length',str(len(content)));self.end_headers();self.wfile.write(content)
 def log_message(self,*_):pass
target=http.server.ThreadingHTTPServer(('127.0.0.1',0),Target);threading.Thread(target=target.serve_forever,daemon=True).start()
class Proxy(http.server.BaseHTTPRequestHandler):
 def do_GET(self):self.forward()
 def do_POST(self):self.forward()
 def forward(self):
  if self.headers.get('Proxy-Authorization')!='Basic '+base64.b64encode(b'owned-proxy-user:owned-proxy-password').decode():self.send_error(407);return
  body=self.rfile.read(int(self.headers.get('Content-Length',0)))
  parsed=urllib.parse.urlsplit(self.path)
  # The proxy forwards only the owned imaginary native host, or the owned repo.
  if parsed.hostname=='owned-native.invalid':address='http://127.0.0.1:'+str(target.server_port)+parsed.path
  elif parsed.hostname=='127.0.0.1':address=self.path
  else:self.send_error(403);return
  request=urllib.request.Request(address,body if self.command=='POST' else None,method=self.command)
  client=urllib.request.build_opener(urllib.request.ProxyHandler({}))
  try:response=client.open(request,timeout=10)
  except urllib.error.HTTPError as error:response=error
  content=response.read()
  if parsed.hostname=='owned-native.invalid':
   with lock:proxy_events.append(dict(method=self.command,body=body.decode(),authentication_verified=True))
  self.send_response(response.code);self.send_header('Content-Length',str(len(content)));self.end_headers();self.wfile.write(content)
 def log_message(self,*_):pass
proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),Proxy);threading.Thread(target=proxy.serve_forever,daemon=True).start()
proxy_uri='http://owned-proxy-user:owned-proxy-password@127.0.0.1:'+str(proxy.server_port)
native_url='http://owned-native.invalid/owned-native'
workers={v:pathlib.Path(path).resolve() for v,path in [('1.0',a.worker),('2.0',a.worker_v2)]};payloads={v:path.read_bytes() for v,path in workers.items()};manifests={v:json.loads(subprocess.check_output([str(path),'--manifest'])) for v,path in workers.items()}
index=dict(schema_version=1,plugin_id='OwnedHostProxy',current_version='1.0',releases=[])
class Repository(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  version=self.path.rsplit('/',1)[-1]
  if self.path=='/index':data=json.dumps(index).encode()
  elif self.path.startswith('/worker/') and version in payloads:data=payloads[version]
  else:self.send_error(404);return
  self.send_response(200);self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
 def log_message(self,*_):pass
repo=http.server.ThreadingHTTPServer(('127.0.0.1',0),Repository);threading.Thread(target=repo.serve_forever,daemon=True).start();repo_url='http://127.0.0.1:'+str(repo.server_port)
for v,data in payloads.items():
 sha=hashlib.sha256(data).hexdigest();index['releases'].append(dict(version=v,manifest=manifests[v],artifacts=[dict(platform=platform.system().lower()+'-'+{'x86_64':'amd64','arm64':'arm64','aarch64':'arm64'}[platform.machine()],url=repo_url+'/worker/'+v,format='executable',sha256=sha,executable_sha256=sha,size=len(data),host_contract='light-media.sdk-resources/v1')]))
with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
origin='http://127.0.0.1:'+str(port);data=root/'data';env=dict(os.environ)
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:env.pop(key,None)
bootstrap=dict(username='owned-proxy',password='owned-proxy-browser-password',settings=dict(plugin_markets=[repo_url+'/index'],proxy_host=proxy_uri,media_recognize_share=False,plugin_server_url=''))
env['NO_PROXY']='*'
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));serverlog=(root/'server.log').open('wb')
def request(method,path,value=None,status=200):
 body=json.dumps(value,ensure_ascii=False).encode() if value is not None else None;req=urllib.request.Request(origin+path,body,{'Content-Type':'application/json','Origin':origin},method=method)
 try:r=client.open(req,timeout=25)
 except urllib.error.HTTPError as error:r=error
 raw=r.read()
 if status is None:assert r.code>=400,(method,path,r.code,raw[:300])
 else:assert r.code==status,(method,path,r.code,status,raw[:300])
 return json.loads(raw) if raw else None

def success(method,path,value=None):result=request(method,path,value);assert result['data']['success'],result;return result['data']
def start():
 global process
 process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen','127.0.0.1:'+str(port)],env=env,stdout=serverlog,stderr=serverlog)
 for _ in range(600):
  try:request('GET','/api/auth/status');break
  except (OSError,AssertionError):assert process.poll() is None;time.sleep(.02)
 else:raise AssertionError('owned server startup timeout')
 request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop():
 global process
 if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None

def snapshot(instance='OwnedHostProxy'):return request('GET','/api/v1/plugin/'+instance+'/snapshot')
def run(value,instance='OwnedHostProxy',status=200):return request('POST','/api/v1/plugin/'+instance+'/run',value,status)
def configure(text,instance='OwnedHostProxy'):
 config=dict(url=native_url,body=text,run_once=True)
 success('PUT','/api/plugins/'+instance,config);value=snapshot(instance)
 assert value['config']['run_once'] is False and value['calls'][-1]['body']=='owned-native:'+text
 return value

def safe_records(value):
 raw=json.dumps(value)
 for secret in ['owned-proxy-user','owned-proxy-password',proxy_uri]:assert secret not in raw,'private proxy escaped into public result'

try:
 start();success('POST','/api/plugins/install',dict(plugin_id='OwnedHostProxy',repo_url=repo_url+'/index'));initial=snapshot();assert initial['calls'] is None
 value=run(dict(url=native_url,body='private proxy CLI'));assert value['status']==200 and value['body']=='owned-native:private proxy CLI' and value['configured_proxy'] and proxy_events[-1]['authentication_verified'];safe_records(value)
 public=request('GET','/api/settings');safe_records(public);assert public['data']['proxy_endpoint'].startswith('http://127.0.0.1:')
 request('GET','/api/network.proxy.read',status=404)
 before=len(wire);run(dict(url='not-a-valid-target',body='must not send'),status=None);assert len(wire)==before
 observations.append(dict(case='private-snapshot-authenticated-native-post-and-public-boundary',proxy_authenticated=True,no_proxy_does_not_remove_explicit_host_proxy=True,native_result=value,public_proxy_endpoint_redacted=True))
 source=configure('CLI source');success('POST','/api/plugins/OwnedHostProxy/clone',dict(suffix='copy'));clone=snapshot('OwnedHostProxycopy');assert clone['calls'] is None and clone['pid']!=source['pid'];clone=configure('CLI clone','OwnedHostProxycopy');assert snapshot()['config']==source['config'];safe_records(snapshot());safe_records(clone);observations.append(dict(case='source-clone-config-data-isolation-and-failure-recovery',source=source,clone=clone,native_failure_no_request=True))
 index['current_version']='2.0';success('POST','/api/plugins/install',dict(plugin_id='OwnedHostProxy',repo_url=repo_url+'/index',release_version='2.0',force=True));updated=snapshot();assert updated['pid']!=source['pid'] and updated['version']=='2.0' and updated['config']==source['config'] and updated['calls']==source['calls'];assert snapshot('OwnedHostProxycopy')==clone
 before={i:snapshot(i) for i in ['OwnedHostProxy','OwnedHostProxycopy']};wire_before=len(wire);stop();start();after={i:snapshot(i) for i in before};assert len(wire)==wire_before
 for i in before:assert after[i]['pid']!=before[i]['pid'] and after[i]['calls']==before[i]['calls'] and after[i]['config']==before[i]['config']
 observations.append(dict(case='actual-update-and-cold-no-replay',before=before,after=after,wire_count_unchanged=True))
 if a.hold:
  (root/'browser-state.json').write_text(json.dumps(dict(origin=origin,username=bootstrap['username'],password=bootstrap['password'],source=after['OwnedHostProxy']['config'],clone=after['OwnedHostProxycopy']['config']),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=origin,port=port)),flush=True)
  while not(root/'.browser-done').exists():assert process.poll() is None;time.sleep(.2)
  browser_before={i:snapshot(i) for i in before};browser_wire=len(wire)
  for i,value in browser_before.items():assert value['config']['run_once'] is False;safe_records(value)
  stop();start();browser_cold={i:snapshot(i) for i in before}
  for i in before:assert browser_cold[i]['pid']!=browser_before[i]['pid'] and browser_cold[i]['calls']==browser_before[i]['calls'] and browser_cold[i]['config']==browser_before[i]['config']
  assert len(wire)==browser_wire;observations.append(dict(case='browser-writes-cold-no-replay',before=browser_before,after=browser_cold,wire_unchanged=True))
 for i in ['OwnedHostProxycopy','OwnedHostProxy']:success('POST','/api/plugins/'+i+'/uninstall');request('GET','/api/v1/plugin/'+i+'/snapshot',status=404)
 stop();safe_records(observations)
 (root/'result.json').write_text(json.dumps(dict(status='passed',classification='actual '+platform.system()+' cmd/server and separately compiled standard SDK; native Go HTTP POST through owned authenticated proxy using private host snapshot; no messenger/business service or existing data modified',source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',bindings={key:hashlib.sha256(pathlib.Path(value).read_bytes()).hexdigest() for key,value in dict(server=a.server,worker=a.worker,worker_v2=a.worker_v2,driver=__file__).items()},observations=observations,wire=wire,proxy_events=proxy_events,uninstalled_api404=True,stopped=True),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations),wire=len(wire))),flush=True)
except BaseException as error:
 (root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+': '+str(error),observations=observations,wire=wire),ensure_ascii=False,indent=2)+'\n');raise
finally:
 stop();target.shutdown();proxy.shutdown();repo.shutdown();target.server_close();proxy.server_close();repo.server_close();serverlog.close()
