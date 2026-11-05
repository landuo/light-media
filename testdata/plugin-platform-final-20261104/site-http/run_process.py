#!/usr/bin/env python3
"""Actual isolated Linux cmd/server + compiled workers, owned site HTTP only."""
import argparse,base64,hashlib,http.cookiejar,http.server,json,os,pathlib,platform,signal,socket,subprocess,threading,time,urllib.error,urllib.parse,urllib.request
p=argparse.ArgumentParser()
for key in ['server','worker','worker-v2','directory']:p.add_argument('--'+key,required=True)
p.add_argument('--hold',action='store_true');a=p.parse_args();root=pathlib.Path(a.directory).resolve();root.mkdir(mode=0o700,parents=True,exist_ok=False)
observations=[];wire=[];lock=threading.Lock();process=None;serverlog=None
# Only synthetic acceptance credentials enter this freshly initialized instance.
owned_cookie='owned=site-private';owned_ua='OwnedSiteHTTP/Linux';owned_key='owned-site-api-private';owned_token='owned-site-token-private'
class Site(http.server.BaseHTTPRequestHandler):
 def do_GET(self):self.dispatch()
 def do_POST(self):self.dispatch()
 def dispatch(self):
  body=self.rfile.read(int(self.headers.get('Content-Length',0)));route=urllib.parse.urlsplit(self.path).path
  with lock:wire.append(dict(method=self.command,path=self.path,body_sha256=hashlib.sha256(body).hexdigest(),body=body.decode(errors='replace'),has_configured_cookie=self.headers.get('Cookie')==owned_cookie,has_configured_ua=self.headers.get('User-Agent')==owned_ua,api_key_correct=self.headers.get('Api-Token')==owned_key,token_correct=self.headers.get('Authorization')==owned_token))
  if route=='/drop':self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
  if route=='/set-cookie':self.send_response(200);self.send_header('Set-Cookie','owned_next=private-response-cookie; Path=/');content=b'owned-cookie-issued'
  elif route=='/cookie-echo':
   assert self.headers.get('Cookie')=='owned_next=private-response-cookie';self.send_response(200);content=b'owned-private-cookie-reused'
  elif route=='/agent':self.send_response(200);content=self.headers.get('User-Agent','').encode()
  elif route=='/redirect':self.send_response(307);self.send_header('Location','/echo');self.send_header('Set-Cookie','owned_redirect=redirect; Path=/');content=b''
  elif route=='/status':self.send_response(503);content=b'owned-http-status503'
  else:self.send_response(200);content=b'owned-response:'+body
  self.send_header('Content-Type','text/plain; charset=utf-8');self.send_header('Content-Length',str(len(content)));self.end_headers();self.wfile.write(content)
 def log_message(self,*_):pass
site=http.server.ThreadingHTTPServer(('127.0.0.1',0),Site);threading.Thread(target=site.serve_forever,daemon=True).start();site_url='http://127.0.0.1:'+str(site.server_port)
workers={v:pathlib.Path(path).resolve() for v,path in [('1.0',a.worker),('2.0',a.worker_v2)]};payloads={v:path.read_bytes() for v,path in workers.items()};manifests={v:json.loads(subprocess.check_output([str(path),'--manifest'])) for v,path in workers.items()}
index=dict(schema_version=1,plugin_id='OwnedHostSiteHTTP',current_version='1.0',releases=[])
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
bootstrap=dict(username='owned-site-http',password='owned-site-http-browser-password',settings=dict(plugin_markets=[repo_url+'/index'],sites=[dict(id='owned-site',name='Owned site HTTP acceptance',kind='nexusphp',base_url=site_url,cookie=owned_cookie,ua=owned_ua,api_key=owned_key,token=owned_token,enabled=False)],media_recognize_share=False,plugin_server_url=''))
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

def snapshot(instance='OwnedHostSiteHTTP'):return request('GET','/api/v1/plugin/'+instance+'/snapshot')
def run(value,instance='OwnedHostSiteHTTP',status=200):return request('POST','/api/v1/plugin/'+instance+'/run',value,status)
def configure(text,instance='OwnedHostSiteHTTP'):
 config=dict(request_json=json.dumps(dict(id='owned-site',path='/echo',method='POST',form=[dict(name='message',value=text)])),run_once=True)
 success('PUT','/api/plugins/'+instance,config);value=snapshot(instance);assert value['config']['run_once'] is False;assert base64.b64decode(value['calls'][-1]['response']['content'])==('owned-response:message='+urllib.parse.quote_plus(text)).encode();return value

def safe_records(value):
 raw=json.dumps(value)
 for secret in [owned_cookie,owned_key,owned_token,'private-response-cookie']:assert secret not in raw,'private credential appeared outside worker memory'

try:
 start();success('POST','/api/plugins/install',dict(plugin_id='OwnedHostSiteHTTP',repo_url=repo_url+'/index'));initial=snapshot();assert initial['calls'] is None
 request_frame=dict(id='owned-site',path='/echo',method='POST',form=[dict(name='choice[]',value='one'),dict(name='choice[]',value='two')],credentials=[dict(credential='api_key',header='api-token'),dict(credential='token',header='Authorization')])
 value=run(request_frame);assert value['response']['status_code']==200 and value['request_state']=='completed';assert wire[-1]['api_key_correct'] and wire[-1]['token_correct'] and wire[-1]['has_configured_cookie'] and wire[-1]['has_configured_ua'];safe_records(value)
 actual=request('POST','/api/v1/plugin/OwnedHostSiteHTTP/verify',dict(id='owned-site',operation='credentials'));expected=hashlib.sha256(('\0'.join([owned_cookie,owned_ua,owned_key,owned_token])).encode()).hexdigest();assert actual['digest']==expected;safe_records(actual)
 actual=request('POST','/api/v1/plugin/OwnedHostSiteHTTP/verify',dict(id='owned-site',operation='cookie_flow'));assert base64.b64decode(actual['response']['content'])==b'owned-private-cookie-reused';safe_records(actual)
 actual=request('POST','/api/v1/plugin/OwnedHostSiteHTTP/verify',dict(id='owned-site',operation='user_agent'));assert actual['write_denied'];ua=base64.b64decode(actual['response']['response']['content']).decode();assert ua==actual['read']+' Plugin/ownedhostsitehttp';observations.append(dict(case='actual-private-credentials-cookie-flow-host-agent',credentials_verified=True,cookie_flow_verified=True,user_agent=actual['read'],host_agent_suffix_verified=True,ua_write_denied=True))
 count=len(wire);run(dict(id='owned-site',path='/must-not-call',method='DELETE'),status=None);assert len(wire)==count
 response=run(dict(id='owned-site',path='/status'));assert response['response']['status_code']==503
 response=run(dict(id='owned-site',path='/drop',method='POST',body=base64.b64encode(b'owned-no-retry').decode()));assert response['response'] is None and response['request_state']=='unknown';assert sum(row['path']=='/drop' for row in wire)==1
 source=configure('CLI source');success('POST','/api/plugins/OwnedHostSiteHTTP/clone',dict(suffix='copy'));clone=snapshot('OwnedHostSiteHTTPcopy');assert clone['calls'] is None and clone['pid']!=source['pid'];clone=configure('CLI clone','OwnedHostSiteHTTPcopy');assert snapshot()['config']==source['config'];safe_records(snapshot());safe_records(clone);observations.append(dict(case='source-clone-config-data-isolation-and-failure-recovery',source=source,clone=clone,non2xx_preserved=True,post_drop_unknown_no_retry=True))
 index['current_version']='2.0';success('POST','/api/plugins/install',dict(plugin_id='OwnedHostSiteHTTP',repo_url=repo_url+'/index',release_version='2.0',force=True));updated=snapshot();assert updated['pid']!=source['pid'] and updated['version']=='2.0' and updated['config']==source['config'] and updated['calls']==source['calls'];assert snapshot('OwnedHostSiteHTTPcopy')==clone
 before={i:snapshot(i) for i in ['OwnedHostSiteHTTP','OwnedHostSiteHTTPcopy']};wire_before=len(wire);stop();start();after={i:snapshot(i) for i in before};assert len(wire)==wire_before
 for i in before:assert after[i]['pid']!=before[i]['pid'] and after[i]['calls']==before[i]['calls'] and after[i]['config']==before[i]['config']
 observations.append(dict(case='actual-update-and-cold-no-replay',before=before,after=after,wire_count_unchanged=True))
 if a.hold:
  (root/'browser-state.json').write_text(json.dumps(dict(origin=origin,username=bootstrap['username'],password=bootstrap['password'],source=after['OwnedHostSiteHTTP']['config'],clone=after['OwnedHostSiteHTTPcopy']['config']),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=origin,port=port)),flush=True)
  while not(root/'.browser-done').exists():assert process.poll() is None;time.sleep(.2)
  browser_before={i:snapshot(i) for i in before};browser_wire=len(wire)
  for i,value in browser_before.items():assert value['config']['run_once'] is False;safe_records(value)
  stop();start();browser_cold={i:snapshot(i) for i in before}
  for i in before:assert browser_cold[i]['pid']!=browser_before[i]['pid'] and browser_cold[i]['calls']==browser_before[i]['calls'] and browser_cold[i]['config']==browser_before[i]['config']
  assert len(wire)==browser_wire;observations.append(dict(case='browser-writes-cold-no-replay',before=browser_before,after=browser_cold,wire_unchanged=True))
 for i in ['OwnedHostSiteHTTPcopy','OwnedHostSiteHTTP']:success('POST','/api/plugins/'+i+'/uninstall');request('GET','/api/v1/plugin/'+i+'/snapshot',status=404)
 stop();safe_records(observations)
 (root/'result.json').write_text(json.dumps(dict(status='passed',classification='actual '+platform.system()+' cmd/server and separately compiled standard SDK; owned synthetic HTTP/cookies only, no real site/signin service or existing data modified',source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',bindings={key:hashlib.sha256(pathlib.Path(value).read_bytes()).hexdigest() for key,value in dict(server=a.server,worker=a.worker,worker_v2=a.worker_v2,driver=__file__).items()},observations=observations,wire=wire,uninstalled_api404=True,stopped=True),ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations),wire=len(wire))),flush=True)
except BaseException as error:
 (root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+': '+str(error),observations=observations,wire=wire),ensure_ascii=False,indent=2)+'\n');raise
finally:
 stop();site.shutdown();repo.shutdown();site.server_close();repo.server_close();serverlog.close()
