#!/usr/bin/env python3
"""Owned Linux product image -> installed SDK -> actual native Chromium proof.
Python is an external acceptance driver, absent from the application image.
"""
import argparse, hashlib, http.cookiejar, http.server, json, os, platform, secrets, signal, socket, subprocess, threading, time, urllib.error, urllib.request
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--proxy-source-oracle');p.add_argument('--image',required=True);p.add_argument('--worker',required=True);p.add_argument('--worker-update',required=True);p.add_argument('--directory',required=True);p.add_argument('--hold',action='store_true');p.add_argument('--listen',default='127.0.0.1');p.add_argument('--public-host',default='127.0.0.1')
a=p.parse_args();root=Path(a.directory).resolve();root.mkdir(mode=0o700,parents=True,exist_ok=False)
worker=Path(a.worker).read_bytes();second=Path(a.worker_update).read_bytes()
manifest=json.loads(subprocess.check_output([a.worker,'--manifest']));new_manifest=json.loads(subprocess.check_output([a.worker_update,'--manifest']))
assert manifest['plugin_id']==new_manifest['plugin_id']=='OwnedHostBrowser' and manifest['version']!=new_manifest['version']
image_id=subprocess.check_output(['docker','image','inspect','--format','{{.Id}}',a.image],text=True).strip()
name='light-media-browser-proof-'+secrets.token_hex(5);password=secrets.token_urlsafe(24)
counts={'page':0,'data':0,'pending':0,'external':0};mode='normal';fs_mode='normal';fs_calls=[];fs_payloads=[];observations=[]
class Peer(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  path=self.path.split('?',1)[0]
  if path in ['/index','/worker','/worker-update']:
   body=json.dumps(index).encode()if path=='/index'else worker if path=='/worker'else second
  elif path=='/data':
   counts['data']+=1;assert self.headers.get('Cookie')=='owned-browser=one' and self.headers.get('User-Agent')=='OwnedBrowser/Linux';time.sleep(.08);body=json.dumps({'marker':'actual-native-js-'+mode}).encode()
  elif path=='/pending':
   counts['pending']+=1;time.sleep(4);body=b'owned'
  elif path=='/page':
   counts['page']+=1;assert self.headers.get('Cookie')=='owned-browser=one' and self.headers.get('User-Agent')=='OwnedBrowser/Linux'
   script="fetch('/pending')"if mode=='timeout'else"fetch('/data').then(r=>r.json()).then(x=>document.body.dataset.marker=x.marker)"
   body=('<!DOCTYPE html><html><body><script>'+script+'</script></body></html>').encode()
  else:self.send_error(404);return
  self.send_response(200);self.send_header('Content-Type','text/html'if path=='/page'else'application/json');self.send_header('Content-Length',str(len(body)));self.end_headers()
  try:self.wfile.write(body)
  except(BrokenPipeError,ConnectionResetError):pass
 def do_POST(self):
  assert self.path=='/fs/v1';value=json.loads(self.rfile.read(int(self.headers['Content-Length'])));fs_calls.append(value['cmd']);fs_payloads.append(value);assert value['cmd']in['request.get','sessions.create','sessions.destroy']
  body=json.dumps({'status':'ok'}if value['cmd']!='request.get'else{'status':'error'}if fs_mode=='fallback'else{'status':'ok','solution':{'cookies':[]}if fs_mode=='missing_response'else{'response':'<html>owned-fs-response</html>'}}).encode();self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);peer_origin='http://127.0.0.1:'+str(peer.server_port);threading.Thread(target=peer.serve_forever,daemon=True).start()
def release(payload,m,suffix):
 digest=hashlib.sha256(payload).hexdigest();return dict(version=m['version'],manifest=m,artifacts=[dict(platform='linux-amd64',url=peer_origin+suffix,format='executable',sha256=digest,executable_sha256=digest,size=len(payload),host_contract='light-media.sdk-resources/v1')])
index=dict(schema_version=1,plugin_id=manifest['plugin_id'],current_version=manifest['version'],releases=[release(worker,manifest,'/worker'),release(second,new_manifest,'/worker-update')])
with socket.socket()as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
origin='http://127.0.0.1:'+str(port);data=root/'data';bootstrap=dict(username='owned-browser',password=password,settings=dict(plugin_markets=[peer_origin+'/index'],plugin_statistic_share=False,sites=[dict(id='owned-browser-site',name='Owned browser page',kind='nexusphp',base_url=peer_origin,cookie='owned-browser=one',ua='OwnedBrowser/Linux',enabled=False)]))
def docker_base(container):return ['docker','run','--rm','--name',container,'--network','host','--env','HTTP_PROXY=http://127.0.0.1:1','--env','HTTPS_PROXY=http://127.0.0.1:1','--env','NO_PROXY=127.0.0.1,localhost','--tmpfs','/tmp:rw,nosuid,nodev,size=256m','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','256','--memory','1g','--volume',str(root)+':'+str(root)]

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
base=docker_base(name+'-bootstrap')+['-i',a.image,'--data-dir',str(data),'--bootstrap-stdin'];subprocess.run(base,input=json.dumps(bootstrap).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
integrity('after-bootstrap')
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));process=None;log=(root/'server.log').open('wb');emulation='cloakbrowser'
def request(method,path,value=None,status=200):
 if method!='GET':integrity('before '+method+' '+path)
 raw=json.dumps(value).encode()if value is not None else None;req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json'},method=method)
 try:res=client.open(req,timeout=900)
 except urllib.error.HTTPError as error:res=error
 body=res.read()
 if method!='GET':integrity('after '+method+' '+path)
 assert res.code==status,(method,path,res.code,status,body[:500]);return json.loads(body)if body else None
def success(method,path,value=None):
 result=request(method,path,value);assert result['data']['success'],result;return result['data']
def start():
 global process
 integrity('before-start')
 process=subprocess.Popen(docker_base(name)+['--env','BROWSER_EMULATION='+emulation,'--env','FLARESOLVERR_URL='+peer_origin+'/fs',a.image,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],stdout=log,stderr=log)
 for _ in range(5000):
  try:request('GET','/api/auth/status');break
  except(OSError,AssertionError):assert process.poll()is None;time.sleep(.02)
 else:raise AssertionError('server did not start')
 integrity('after-start')
 mounts=json.loads(subprocess.check_output(['docker','inspect','--format','{{json .Mounts}}',name],text=True));assert any(m.get('Source')==str(root)and m.get('Destination')==str(root)for m in mounts),mounts
 print(json.dumps(dict(actual_container=name,actual_bind_mounts=mounts,data_dir_argument=str(data))),flush=True)
 request('POST','/api/auth/login',dict(username=bootstrap['username'],password=password))
def stop(check_integrity=True):
 global process
 if process is not None:
  if check_integrity:integrity('before-stop')
  subprocess.run(['docker','stop','--time','20',name],check=True,stdout=subprocess.PIPE);assert process.wait(timeout=60)==0;process=None
  if check_integrity:integrity('after-stop')
def call(value=None,instance='OwnedHostBrowser',status=200):return request('POST','/api/v1/plugin/'+instance+'/run',value or{},status)
def snapshot(instance='OwnedHostBrowser'):return request('GET','/api/v1/plugin/'+instance+'/snapshot')
def clean_profiles():
 entries=list((data/'work'/'browser').glob('*'));assert not entries,entries
 listing=subprocess.check_output(['docker','top',name,'-eo','pid,ppid,stat,comm'],text=True).splitlines();assert not any('chrome'in x or'Xvfb'in x for x in listing),listing
 return dict(temp_profiles=0,remaining_native_browser_processes=0,actual_process_table=listing)
try:
 start();success('POST','/api/plugins/install',dict(plugin_id='OwnedHostBrowser',repo_url=peer_origin+'/index'))
 success('PUT','/api/plugins/OwnedHostBrowser',dict(id='owned-browser-site',path='/page',headless=False,timeout_seconds=3))
 source_pid=snapshot()['pid'];got=call();assert got['success']and'data-marker="actual-native-js-normal"'in got['result']['html'];assert got['result']['html'].startswith('<!DOCTYPE html>')
 observations.append(dict(phase='actual-headed-native-js',source_pid=source_pid,mode='source-default-headed-Xvfb',rendered_marker=True,networkidle_after_fetch=True,cleanup=clean_profiles()))
 mode='timeout';got=call(dict(id='owned-browser-site',path='/page',headless=True,timeout_seconds=1));assert not got['success']and got['result']['html']is None
 observations.append(dict(phase='actual-native-networkidle-failure',result=None,cleanup=clean_profiles()));mode='restored'
 got=call();assert got['success']and'actual-native-js-restored'in got['result']['html'];clean_profiles()
 call(dict(id='owned-browser-site',path='https://other.invalid/'),status=502)
 success('POST','/api/plugins/OwnedHostBrowser/clone',dict(suffix='Twin'));clone='OwnedHostBrowsertwin';clone_pid=snapshot(clone)['pid'];assert clone_pid!=source_pid
 success('PUT','/api/plugins/'+clone,dict(id='owned-browser-site',path='/page',headless=True,timeout_seconds=3));assert call(instance=clone)['success'];clean_profiles()
 success('POST','/api/plugins/install',dict(plugin_id='OwnedHostBrowser',repo_url=peer_origin+'/index',release_version=new_manifest['version'],force=True));updated=snapshot();assert updated['pid']!=source_pid and updated['version']==new_manifest['version']and snapshot(clone)['pid']==clone_pid
 before=counts.copy();stop();start();cold=snapshot();assert cold['pid']!=updated['pid']and counts==before;assert call()['success'];clean_profiles()
 observations.append(dict(phase='actual-clone-update-cold',source_pid=source_pid,clone_pid=clone_pid,updated_pid=updated['pid'],cold_pid=cold['pid'],source_update_keeps_clone_pid=True,cold_no_render_replay=True,config_and_last_persisted=True))
 emulation='flaresolverr';stop();start();before=counts.copy();got=call();assert got['success']and got['result']['html']=='<html>owned-fs-response</html>'and counts==before
 fs_mode='missing_response';got=call();assert not got['success']and got['result']['html']is None and counts==before
 fs_mode='fallback';got=call();assert got['success']and'actual-native-js-restored'in got['result']['html'];clean_profiles();observations.append(dict(phase='actual-FS-protocol-fallback',classification='owned FS HTTP protocol responses; actual native fallback, not live challenge provider',commands=fs_calls.copy(),truthy_missing_response_preserves_none=True))
 if a.proxy_source_oracle:
  source=json.loads(Path(a.proxy_source_oracle).read_bytes());proxy_cases=[]
  fs_mode='normal'
  for label in ['http-upper','explicit-encoded-auth','explicit-ipv6']:
   row=next(r for r in source['cases']if r['name']==label)
   settings=request('GET','/api/settings')['data'];settings['sites'][0]['proxy']=True;settings['proxy_host']=row['proxy_host']or'';settings['clear_proxy_host']=not bool(row['proxy_host']);request('PUT','/api/settings',settings)
   before=len(fs_payloads);got=call();assert got['success']and got['result']['html']=='<html>owned-fs-response</html>'
   actual=fs_payloads[before:];expected=row['proxy_server']
   if expected and(expected.get('username')or expected.get('password')):
    assert [x['cmd']for x in actual]==['sessions.create','request.get','sessions.destroy'],actual
    want=dict(url=expected['server']);want.update({k:expected[k]for k in ['username','password']if expected.get(k)})
    assert actual[0]['proxy']==want,(actual,want)
    assert actual[0]['session']==actual[1]['session']==actual[2]['session']
   else:
    assert len(actual)==1 and actual[0]['cmd']=='request.get'
    assert actual[0].get('proxy')==({'url':expected['server']}if expected else None),(actual,expected)
   proxy_cases.append(dict(case=label,actual_FS_payloads=actual))
   if label=='explicit-ipv6':
    fs_mode='fallback';before_counts=counts.copy();got=call();assert not got['success']and got['result']['html']is None and counts==before_counts;clean_profiles();fs_mode='normal'
    proxy_cases[-1]['native_invalid_source_server_safe_none_no_target_requests']=True
  settings=request('GET','/api/settings')['data'];settings['sites'][0]['proxy']=False;settings['clear_proxy_host']=True;request('PUT','/api/settings',settings)
  observations.append(dict(phase='actual-source-PROXY_SERVER',source_getter_oracle_sha256=hashlib.sha256(Path(a.proxy_source_oracle).read_bytes()).hexdigest(),runtime_env_proxy='owned unreachable loopback fixture; NO_PROXY protects the owned FS control endpoint',cases=proxy_cases))
 emulation='cloakbrowser';stop();start();success('PUT','/api/plugins/OwnedHostBrowser',dict(id='owned-browser-site',path='/page',headless=True,timeout_seconds=3));assert call()['success']
 if a.hold:
  state=dict(origin='http://'+a.public_host+':'+str(port),local_origin=origin,username='owned-browser',password=password,plugin='OwnedHostBrowser',clone=clone,container=name)
  state_file=root/'browser-state.json';state_file.write_text(json.dumps(state));state_file.chmod(0o600);print(json.dumps(dict(status='browser_ready',origin=origin,container=name)),flush=True)
  deadline=time.monotonic()+1800
  while not(root/'.browser-done').exists():assert process.poll()is None and time.monotonic()<deadline;time.sleep(.3)
 uninstalled=[]
 for instance in[clone,'OwnedHostBrowser']:
  ack=success('POST','/api/plugins/'+instance+'/uninstall');gone=request('GET','/api/v1/plugin/'+instance+'/snapshot',status=404)
  uninstalled.append(dict(instance=instance,uninstall_response=ack,subsequent_custom_api_status=404,subsequent_custom_api_response=gone))
 cleanup=clean_profiles();assert not any('plugin-host-'in line for line in cleanup['actual_process_table']),cleanup
 cleanup.update(instances_uninstalled=True,API404=True,uninstalled=uninstalled,plugin_workers=0)
 stop()
 remaining=subprocess.check_output(['docker','ps','--all','--filter','name=^/'+name+'$','--format','{{.Names}}'],text=True).strip();assert not remaining,remaining
 cleanup.update(container_absent=True,container_name=name,data_directory_retained_for_evidence=True)
 runtime=subprocess.check_output(['docker','run','--rm','--entrypoint','sh',a.image,'-c','set -eu; test ! -e /usr/local/bin/python; ! command -v python; ! command -v python3; ! command -v node; sha256sum /usr/local/bin/light-media; /usr/local/lib/light-media/cloakbrowser/chrome --version; id'],text=True)
 result=dict(status='passed',classification='actual Linux product Dockerfile image, cmd/server -> installed standard SDK -> native pinned CloakBrowser; owned HTML/FS responses; external Python driver not product runtime',image_id=image_id,driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),worker_sha256=hashlib.sha256(worker).hexdigest(),update_worker_sha256=hashlib.sha256(second).hexdigest(),observations=observations,parent_tool_integrity=integrity_observations,protected_tool_sha256=integrity_files,http_counts=counts,runtime=runtime,cleanup=cleanup)
 (root/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
 print(json.dumps(dict(failure=type(error).__name__+':'+str(error),parent_tool_integrity=integrity_observations)),flush=True)
 if root.is_dir():(root/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+':'+str(error),observations=observations,counts=counts),indent=2));raise
finally:
 stop(check_integrity=False);peer.shutdown();peer.server_close();log.close()
