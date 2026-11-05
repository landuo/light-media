#!/usr/bin/env python3
"""Real server/installed SDK native recognition with owned TLS provider replay.

The child server trusts a newly generated CA only for this run. The product's
fixed provider origins, TLS checks and existing configured proxy remain active.
Python/openssl are external acceptance tools, never product dependencies.
"""
import argparse
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

p=argparse.ArgumentParser()
for key in ['server','worker','directory']:p.add_argument('--'+key,required=True)
p.add_argument('--source-cases',action='append',default=[])
p.add_argument('--hold',action='store_true')
p.add_argument('--listen',default='127.0.0.1')
p.add_argument('--settings-startup',action='store_true')
a=p.parse_args();directory=Path(a.directory).resolve();directory.mkdir(mode=0o700,parents=True,exist_ok=False)
source_fixture=dict(cases=[]);source_bindings={}
for path in a.source_cases:
    raw=Path(path).read_bytes();source_bindings[Path(path).name]=hashlib.sha256(raw).hexdigest()
    source_fixture['cases'].extend(json.loads(raw)['cases'])
provider_hosts={'api.themoviedb.org','api.bgm.tv','graphql.anilist.co','movie.douban.com','frodo.douban.com','api.douban.com','api.trace.moe','trace.moe','raw.githubusercontent.com'}
assert all(reply['host'] in provider_hosts for case in source_fixture['cases'] for reply in case['replies'])
certificate=directory/'owned-root.pem';private_key=directory/'owned-key.pem'
with (directory/'certificate-generation.log').open('wb') as log:
    subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(private_key),'-out',str(certificate),'-subj','/CN=Owned recognition acceptance','-addext','basicConstraints=critical,CA:TRUE','-addext','subjectAltName='+','.join('DNS:'+host for host in sorted(provider_hosts))],stdout=log,stderr=log,check=True)
private_key.chmod(0o600)
tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(certificate,private_key)
lock=threading.Lock();trace=[];revision=1;fail_detail=False;active_replies=[]
def emit(handler,code,value):
    raw=json.dumps(value).encode();handler.send_response(code);handler.send_header('Content-Type','application/json');handler.send_header('Content-Length',str(len(raw)));handler.end_headers();handler.wfile.write(raw)
class Provider(http.server.BaseHTTPRequestHandler):
    def do_GET(self):self.dispatch()
    def do_POST(self):self.dispatch()
    def dispatch(self):
        path=urllib.parse.urlsplit(self.path);query=urllib.parse.parse_qs(path.query,keep_blank_values=True)
        host=self.headers.get('Host','').split(':')[0]
        raw=self.rfile.read(int(self.headers.get('Content-Length','0')))
        if active_replies:
            reply=active_replies.pop(0)
            assert (host,self.command,path.path)==(reply['host'],reply.get('method','GET'),reply['path']),(host,self.command,path.path,reply['host'],reply.get('method','GET'),reply['path'])
            for key,value in reply.get('query',{}).items():assert query.get(key)==value,(key,query.get(key),value)
            for key,value in reply.get('json',{}).items():assert json.loads(raw)[key]==value
            with lock:trace.append(dict(operation='source-replay',host=host,method=self.command,path=path.path))
            emit(self,reply.get('status',200),reply['body']);return
        assert host=='api.themoviedb.org' and query.get('language')==['en-US'],(host,path.path,query.get('language'))
        if path.path.startswith('/3/search/'):
            name=query['query'][0]
            with lock:trace.append(dict(operation='search',name=name))
            if name=='Owned Network':
                self.close_connection=True
                self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
            rows=[] if name=='Owned Missing' else [dict(id=123,media_type='movie',title='Owned Native',original_title='Owned Native',release_date='2024-01-01')]
            emit(self,200,dict(results=rows));return
        if path.path=='/3/movie/123':
            with lock:trace.append(dict(operation='detail',revision=revision))
            if fail_detail:emit(self,404,{});return
            emit(self,200,dict(id=123,title='Owned detail v'+str(revision),original_title='Owned Native',release_date='2024-01-01'));return
        raise AssertionError(('unexpected native provider request',host,self.command,path.path))
    def log_message(self,*_):pass
worker=Path(a.worker).resolve();payload=worker.read_bytes();manifest=json.loads(subprocess.check_output([str(worker),'--manifest']))
assert manifest['plugin_id']=='OwnedHostServices'
platform=manifest['platform'] if 'platform' in manifest else None
import platform as machine_platform
platform=machine_platform.system().lower()+'-'+{'arm64':'arm64','aarch64':'arm64','x86_64':'amd64'}[machine_platform.machine()]
digest=hashlib.sha256(payload).hexdigest()
index=dict(schema_version=1,plugin_id='OwnedHostServices',current_version=manifest['version'],releases=[dict(version=manifest['version'],manifest=manifest,artifacts=[dict(platform=platform,format='executable',sha256=digest,executable_sha256=digest,size=len(payload),host_contract='light-media.sdk-resources/v1')])])
class Repository(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path=urllib.parse.urlsplit(self.path).path
        if path=='/index':raw=json.dumps(index).encode()
        elif path=='/worker':raw=payload
        else:self.send_error(404);return
        self.send_response(200);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
    def log_message(self,*_):pass
repo=http.server.ThreadingHTTPServer(('127.0.0.1',0),Repository);threading.Thread(target=repo.serve_forever,daemon=True).start()
repo_origin='http://127.0.0.1:'+str(repo.server_port);index['releases'][0]['artifacts'][0]['url']=repo_origin+'/worker'
class Proxy(Repository):
    def do_CONNECT(self):
        host,port=self.path.rsplit(':',1);assert host in provider_hosts and port=='443',self.path
        self.send_response(200,'Connection established');self.end_headers()
        connection=tls.wrap_socket(self.connection,server_side=True)
        try:Provider(connection,self.client_address,self.server)
        finally:connection.close()
        self.close_connection=True
    def do_GET(self):
        target=urllib.parse.urlsplit(self.path)
        assert target.scheme=='http' and target.netloc==urllib.parse.urlsplit(repo_origin).netloc
        super().do_GET()
proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),Proxy);threading.Thread(target=proxy.serve_forever,daemon=True).start()
with socket.socket() as sock:sock.bind((a.listen,0));port=sock.getsockname()[1]
origin='http://'+a.listen+':'+str(port);data=directory/'data'
env=os.environ|dict(SSL_CERT_FILE=str(certificate),TMDB_LOCALE='en-US',META_CACHE_EXPIRE='48',BIG_MEMORY_MODE='true',RECOGNIZE_SOURCE='themoviedb')
bootstrap=dict(username='owned-native',password='owned-native-browser-password',settings=dict(plugin_markets=[repo_origin+'/index'],plugin_server_url='',proxy_host='http://127.0.0.1:'+str(proxy.server_port),tmdb=dict(api_key='owned-native-synthetic-key'),media_recognize_share=False))
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
process=None;log=(directory/'server.log').open('wb');observations=[]
def request(method,path,value=None,status=200):
    body=json.dumps(value).encode() if value is not None else None
    req=urllib.request.Request(origin+path,body,{'Content-Type':'application/json'},method=method)
    try:response=client.open(req,timeout=30)
    except urllib.error.HTTPError as error:response=error
    raw=response.read();assert response.code==status,(method,path,response.code,status,raw[:1000]);return json.loads(raw) if raw else None
def start():
    global process
    process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],env=env,stdout=log,stderr=log)
    for _ in range(600):
        try:request('GET','/api/auth/status');break
        except (OSError,AssertionError):assert process.poll() is None,'server exited';time.sleep(.02)
    else:raise AssertionError('server did not start')
    request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop():
    global process
    if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
def resource(method,value,instance='OwnedHostServices',status=200):return request('POST','/api/v1/plugin/'+instance+'/run',dict(method=method,input=value),status=status)
def run(value,instance='OwnedHostServices'):return resource('media.recognize',value,instance)
def recognize(name='Owned Native',instance='OwnedHostServices'):return run(dict(meta=dict(en_name=name,type='未知')),instance)
def assert_info(value,expected_title,hit):assert value['title']==expected_title and value['recognize_cache_hit']==hit,value
def counts(operation):return sum(item['operation']==operation for item in trace)
def subset(actual,expected):
    if isinstance(expected,dict):
        assert isinstance(actual,dict),(actual,expected)
        for key,value in expected.items():assert key in actual,key;subset(actual[key],value)
    else:assert actual==expected,(actual,expected)
try:
    start();result=request('POST','/api/plugins/install',dict(plugin_id='OwnedHostServices',repo_url=repo_origin+'/index'));assert result['data']['success'],result
    if a.settings_startup:
        startup_values=dict(BIG_MEMORY_MODE=True,META_CACHE_EXPIRE=48,TMDB_LOCALE='en-US',RECOGNIZE_SOURCE='themoviedb')
        for key,value in startup_values.items():
            observed=resource('settings.read',dict(key=key));assert observed['schema_version']==1 and observed['key']==key and observed['value']==value,observed
            resource('settings.write',dict(key=key,value=None),status=502)
            again=resource('settings.read',dict(key=key));assert again['value']==value,again
        observations.append(dict(phase='startup-ordinary-settings',values=startup_values,worker_writes_rejected=True))
    first=recognize();assert_info(first,'Owned detail v1',False);assert counts('search')==1 and counts('detail')==1
    revision=2;second=recognize();assert_info(second,'Owned detail v1',True);assert counts('search')==1 and counts('detail')==1
    fresh=run(dict(meta=dict(en_name='Owned Native',type='未知'),cache=False));assert_info(fresh,'Owned detail v2',False);assert counts('search')==2 and counts('detail')==2
    assert recognize('Owned Missing') is None and recognize('Owned Missing') is None;assert counts('search')==3
    assert recognize('Owned Network') is None and recognize('Owned Network') is None;assert counts('search')==11
    result=request('POST','/api/plugins/OwnedHostServices/clone',dict(suffix='copy'));assert result['data']['success'],result
    assert_info(recognize(instance='OwnedHostServicescopy'),'Owned detail v2',True);assert counts('search')==11 and counts('detail')==2
    before=request('GET','/api/v1/plugin/OwnedHostServices/snapshot');stop();start();revision=3
    after=request('GET','/api/v1/plugin/OwnedHostServices/snapshot');assert before['pid']!=after['pid']
    third=recognize();assert_info(third,'Owned detail v3',True);assert counts('search')==11 and counts('detail')==3
    assert recognize('Owned Missing') is None;assert counts('search')==12
    stop();fail_detail=True;start();assert recognize() is None;assert counts('search')==12 and counts('detail')==4;fail_detail=False
    recovered=run(dict(meta=dict(en_name='Owned Native',type='未知'),cache=False));assert_info(recovered,'Owned detail v3',False);assert counts('search')==13 and counts('detail')==5
    for case in source_fixture['cases']:
        active_replies[:]=case['replies'];value=run(case['request']);assert not active_replies,(case['case'],active_replies)
        subset(value,case['expected']);observations.append(dict(phase='source-provider-native',case=case['case'],result=value))
    display=recognize();assert_info(display,'Owned detail v3',True)
    observations.append(dict(phase='native-tmdb-cache',first=first,second=second,fresh=fresh,after_restart=third,recovered=recovered,pid_before=before['pid'],pid_after=after['pid'],provider_trace=trace,negative_memory_only=True,network_none_retried=True,provider_memory_only=True,clone_host_cache_shared=True))
    if a.hold:
        (directory/'browser-state.json').write_text(json.dumps(dict(origin=origin,ready=True,plugin='OwnedHostServices',last_title=display['title'],last_cache_hit=display['recognize_cache_hit']),indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=origin)),flush=True)
        while not (directory/'.browser-done').exists():assert process.poll() is None;time.sleep(.2)
    for instance in ['OwnedHostServicescopy','OwnedHostServices']:
        result=request('POST','/api/plugins/'+instance+'/uninstall');assert result['data']['success'],result;request('GET','/api/v1/plugin/'+instance+'/snapshot',status=404)
    stop()
    binding={key:hashlib.sha256(Path(value).resolve().read_bytes()).hexdigest() for key,value in dict(server=a.server,worker=a.worker,driver=__file__).items()}
    if a.source_cases:binding['source_cases']=source_bindings
    (directory/'result.json').write_text(json.dumps(dict(status='passed',classification='actual cmd/server HTTP API and installed standard SDK worker; fixed-origin HTTPS provider requests through owned configured CONNECT proxy and trusted owned CA; synthetic protocol responses, not live TMDB or other provider',binding=binding,environment=dict(TMDB_LOCALE='en-US',META_CACHE_EXPIRE='48',BIG_MEMORY_MODE=True),observations=observations),indent=2)+'\n')
    print(json.dumps(dict(status='passed',observations=len(observations))),flush=True)
except BaseException as error:
    (directory/'failure.json').write_text(json.dumps(dict(status='failed',driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),error=type(error).__name__+': '+str(error),trace=trace,remaining_source_replies=active_replies,completed_observations=observations),indent=2)+'\n')
    raise
finally:
    stop();proxy.shutdown();proxy.server_close();repo.shutdown();repo.server_close();log.close()
