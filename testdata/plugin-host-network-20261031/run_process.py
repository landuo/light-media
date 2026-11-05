#!/usr/bin/env python3
"""Owned real Linux server/SDK network acceptance; peers are synthetic protocols.

No public upstream request, production configuration or global trust changes.
"""
import argparse
import base64
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import platform
import secrets
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
for key in ['server','worker','worker-v2','directory','source-oracle','vendor-oracle']:p.add_argument('--'+key,required=True)
p.add_argument('--listen',default='127.0.0.1');p.add_argument('--hold',action='store_true');p.add_argument('--timeout-cases',action='store_true')
p.add_argument('--vendor-edge-oracle')
a=p.parse_args();directory=Path(a.directory).resolve();directory.mkdir(mode=0o700,parents=True,exist_ok=False)
sources=json.loads(Path(a.source_oracle).read_bytes());vendor=json.loads(Path(a.vendor_oracle).read_bytes())
if a.vendor_edge_oracle:
    edge_oracle=json.loads(Path(a.vendor_edge_oracle).read_bytes())
    for case in edge_oracle['cases']:
        if case['name']=='single-whitespace':
            case=dict(case,content_b64=edge_oracle['go_header_ows_adaptation']['content_b64'])
        vendor['cases'].append(case)
cases={case['name']:case for case in sources['cases']};codecs={case['name']:case for case in vendor['cases']}
hosts={'raw.githubusercontent.com','owned-text.test','github.unrelated.test'}
certificate=directory/'owned-ca.pem';key=directory/'owned-key.pem'
with (directory/'certificate-generation.log').open('wb') as log:
    subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(key),'-out',str(certificate),'-subj','/CN=Owned network acceptance','-addext','basicConstraints=critical,CA:TRUE','-addext','subjectAltName='+','.join('DNS:'+host for host in sorted(hosts))],stdout=log,stderr=log,check=True)
key.chmod(0o600);tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(certificate,key)
token=secrets.token_hex(24);proxy_password=secrets.token_hex(18)
proxy_auth='Basic '+base64.b64encode(('owned-proxy:'+proxy_password).encode()).decode()
trace=[];observations=[];errors=[];lock=threading.Lock()
def emit(handler,status,body,headers=None):
    handler.send_response(status)
    for name,value in (headers or {}).items():handler.send_header(name,value)
    handler.send_header('Content-Length',str(len(body)));handler.end_headers();handler.wfile.write(body)
class Provider(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        try:self.dispatch()
        except BaseException as error:
            errors.append(type(error).__name__+': '+str(error));self.close_connection=True
    def dispatch(self):
        path=urllib.parse.urlsplit(self.path).path;host=self.headers['Host'].split(':')[0]
        assert host in hosts,host
        authenticated=self.headers.get('Authorization')=='Bearer '+token
        assert self.headers.get('Proxy-Authorization') is None,'proxy credential escaped tunnel'
        with lock:trace.append(dict(host=host,path=path,authenticated=authenticated,has_auth=bool(self.headers.get('Authorization')),cookie=self.headers.get('Cookie',''),accept_encoding=self.headers.get('Accept-Encoding',''),user_agent=self.headers.get('User-Agent',''),accept=self.headers.get('Accept',''),content_type=self.headers.get('Content-Type',''),connection=self.headers.get('Connection','')))
        if path.startswith('/source/'):
            case=cases[path.rsplit('/',1)[1]]
            if case['response'] is None:
                self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
            return emit(self,case['response']['status_code'],base64.b64decode(case['response']['content_b64']))
        if path.startswith('/codec/'):
            case=codecs[path.rsplit('/',1)[1]];return emit(self,200,base64.b64decode(case['wire_b64']),{'Content-Encoding':case['encoding']})
        if path=='/same':return emit(self,302,b'',{'Location':'/same-end','Set-Cookie':'owned=same; Path=/'})
        if path=='/leave':return emit(self,302,b'',{'Location':'https://owned-text.test/back'})
        if path=='/back':return emit(self,302,b'',{'Location':'https://raw.githubusercontent.com/returned'})
        if path=='/loop':return emit(self,302,b'',{'Location':'/loop'})
        if path=='/truncated':
            self.send_response(200);self.send_header('Content-Length','300');self.end_headers();self.wfile.write(b'short');self.close_connection=True;return
        if path=='/progress':
            self.send_response(200);self.send_header('Content-Length','5');self.end_headers()
            for i in range(5):
                self.wfile.write(b'x');self.wfile.flush()
                if i<4:time.sleep(4)
            return
        if path=='/idle':time.sleep(17);return
        if path=='/large':return emit(self,200,b'x'*(13<<20))
        if path=='/missing':return emit(self,404,b'owned missing')
        return emit(self,200,('owned '+path).encode())
    def log_message(self,*_):pass
workers={version:Path(path).resolve() for version,path in [('1.0',a.worker),('2.0',a.worker_v2)]}
payloads={version:path.read_bytes() for version,path in workers.items()}
manifests={version:json.loads(subprocess.check_output([str(path),'--manifest'])) for version,path in workers.items()}
platform_name=platform.system().lower()+'-'+{'x86_64':'amd64','aarch64':'arm64','arm64':'arm64'}[platform.machine()]
index=dict(schema_version=1,plugin_id='OwnedHostNetwork',current_version='1.0',releases=[])
class Repository(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path=urllib.parse.urlsplit(self.path).path
        if path=='/index':return emit(self,200,json.dumps(index).encode())
        if path in ['/worker/1.0','/worker/2.0']:return emit(self,200,payloads[path.rsplit('/',1)[1]])
        self.send_error(404)
    def log_message(self,*_):pass
repo=http.server.ThreadingHTTPServer(('127.0.0.1',0),Repository);threading.Thread(target=repo.serve_forever,daemon=True).start()
repo_origin='http://127.0.0.1:'+str(repo.server_port)
for version,payload in payloads.items():
    digest=hashlib.sha256(payload).hexdigest()
    index['releases'].append(dict(version=version,manifest=manifests[version],artifacts=[dict(platform=platform_name,url=repo_origin+'/worker/'+version,format='executable',sha256=digest,executable_sha256=digest,size=len(payload),host_contract='light-media.sdk-resources/v1')]))
class Proxy(Repository):
    def do_CONNECT(self):
        try:
            host,port=self.path.rsplit(':',1);assert host in hosts and port=='443',self.path
            assert self.headers.get('Proxy-Authorization')==proxy_auth,'missing proxy authentication'
            self.send_response(200,'Connection established');self.end_headers()
            connection=tls.wrap_socket(self.connection,server_side=True)
            try:Provider(connection,self.client_address,self.server)
            finally:connection.close()
        except BaseException as error:errors.append(type(error).__name__+': '+str(error))
        self.close_connection=True
    def do_GET(self):
        target=urllib.parse.urlsplit(self.path)
        if target.hostname in hosts:
            # Forward-proxy authentication belongs to this peer, not origin.
            assert self.headers.get('Proxy-Authorization')==proxy_auth
            del self.headers['Proxy-Authorization'];return Provider.dispatch(self)
        assert target.netloc==urllib.parse.urlsplit(repo_origin).netloc,target.netloc
        super().do_GET()
proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),Proxy);threading.Thread(target=proxy.serve_forever,daemon=True).start()
with socket.socket() as sock:sock.bind((a.listen,0));port=sock.getsockname()[1]
origin='http://'+a.listen+':'+str(port);data=directory/'data'
env=os.environ|dict(SSL_CERT_FILE=str(certificate))
for name in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:env.pop(name,None)
bootstrap=dict(username='owned-network',password='owned-network-browser-password',settings=dict(plugin_markets=[repo_origin+'/index'],plugin_server_url='',proxy_host='http://owned-proxy:'+proxy_password+'@127.0.0.1:'+str(proxy.server_port),github_token=token,repo_github_token='owned/project:owned-unused-repo-token',github_proxy='https://unused-mirror.invalid',media_recognize_share=False))
subprocess.run([a.server,'--data-dir',str(data),'--bootstrap-stdin'],input=json.dumps(bootstrap).encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
process=None;log=(directory/'server.log').open('wb')
def request(method,path,value=None,status=200):
    raw=json.dumps(value).encode() if value is not None else None
    req=urllib.request.Request(origin+path,raw,{'Content-Type':'application/json'},method=method)
    try:response=client.open(req,timeout=35)
    except urllib.error.HTTPError as error:response=error
    raw=response.read();assert response.code==status,(method,path,response.code,status,raw[:700]);return json.loads(raw) if raw else None
def start():
    global process
    process=subprocess.Popen([a.server,'--data-dir',str(data),'--listen',a.listen+':'+str(port)],env=env,stdout=log,stderr=log)
    for _ in range(600):
        try:request('GET','/api/auth/status');break
        except (OSError,AssertionError):assert process.poll() is None;time.sleep(.02)
    else:raise AssertionError('server startup timeout')
    request('POST','/api/auth/login',dict(username=bootstrap['username'],password=bootstrap['password']))
def stop():
    global process
    if process is not None:process.send_signal(signal.SIGTERM);assert process.wait(timeout=20)==0;process=None
def run(url=None,github=False,instance='OwnedHostNetwork',status=200):
    value={} if url is None else dict(url=url,github_headers=github)
    value=request('POST','/api/v1/plugin/'+instance+'/run',value,status)
    return value.get('result') if status==200 else value
def response(value,status,content):
    assert value['schema_version']==1 and value['response']['status_code']==status,value
    assert base64.b64decode(value['response']['content'])==content,value
def configure(instance,url,github=False):
    value=request('PUT','/api/plugins/'+instance,dict(url=url,github_headers=github));assert value['data']['success'],value
try:
    start();value=request('POST','/api/plugins/install',dict(plugin_id='OwnedHostNetwork',repo_url=repo_origin+'/index'));assert value['data']['success'],value
    for name,case in cases.items():
        before=len(trace);observed=run('https://owned-text.test/source/'+name)
        if case['response'] is None:assert observed['response'] is None,observed
        else:response(observed,case['response']['status_code'],base64.b64decode(case['response']['content_b64']))
        assert len(trace)==before+1,(name,'unexpected retry',trace[before:]);observations.append(dict(case='source-'+name,result=observed))
    for name,case in codecs.items():
        observed=run('https://owned-text.test/codec/'+name)
        if case['exception']:assert observed['response'] is None,observed
        else:response(observed,200,base64.b64decode(case['content_b64']))
        observations.append(dict(case='vendor-'+name,result=observed))
    for url,selected,expected in [('https://raw.githubusercontent.com/owned/project/text',True,True),('https://raw.githubusercontent.com/no-headers',False,False),('http://raw.githubusercontent.com/plain',True,False),('https://github.unrelated.test/text',True,False)]:
        value=run(url,selected);assert value['response']['status_code']==200,value;assert trace[-1]['authenticated']==expected and trace[-1]['has_auth']==expected,trace[-1]
        assert trace[-1]['accept_encoding']=='gzip, deflate, br' and trace[-1]['connection']=='keep-alive'
        if expected:assert trace[-1]['accept']=='*/*' and trace[-1]['content_type']==''
        else:assert trace[-1]['user_agent']=='python-urllib3/2.7.0' and trace[-1]['accept']=='' and trace[-1]['content_type']=='application/x-www-form-urlencoded; charset=UTF-8'
    start_trace=len(trace);response(run('https://raw.githubusercontent.com/same',True),200,b'owned /same-end')
    assert len(trace)==start_trace+2 and all(row['authenticated'] for row in trace[start_trace:]) and trace[-1]['cookie']=='owned=same'
    response(run('https://raw.githubusercontent.com/leave',True),200,b'owned /returned');assert [row['has_auth'] for row in trace[-3:]]==[True,False,False]
    assert run('https://owned-text.test/loop')['response'] is None
    assert run('https://owned-text.test/truncated')['response'] is None
    run('https://owned-text.test/large',status=502)
    before=len(trace);run('file:///tmp/owned',status=502);run('https://name:secret@owned-text.test/',status=502);assert len(trace)==before
    observations.append(dict(case='headers-redirects-limits',auth_origin_bound=True,repo_token_unused=True,mirror_unused=True,proxy_auth_confined=True))
    if a.timeout_cases:
        started=time.monotonic();response(run('https://owned-text.test/progress'),200,b'xxxxx');progress=time.monotonic()-started;assert progress>15
        started=time.monotonic();assert run('https://owned-text.test/idle')['response'] is None;idle=time.monotonic()-started;assert 14<idle<17
        observations.append(dict(case='source-scalar-inactivity-timeout',source_oracle='timeout-oracle.json',progress_seconds=progress,idle_seconds=idle,source_scalar_seconds=15,progress_result='xxxxx',idle_result=None))
    configure('OwnedHostNetwork','https://owned-text.test/source-config');response(run(),200,b'owned /source-config')
    value=request('POST','/api/plugins/OwnedHostNetwork/clone',dict(suffix='copy'));assert value['data']['success'],value
    configure('OwnedHostNetworkcopy','https://owned-text.test/clone-config');response(run(instance='OwnedHostNetworkcopy'),200,b'owned /clone-config')
    before=request('GET','/api/v1/plugin/OwnedHostNetwork/snapshot');clone_before=request('GET','/api/v1/plugin/OwnedHostNetworkcopy/snapshot')
    index['current_version']='2.0'
    value=request('POST','/api/plugins/install',dict(plugin_id='OwnedHostNetwork',repo_url=repo_origin+'/index',release_version='2.0',force=True));assert value['data']['success'],value
    after=request('GET','/api/v1/plugin/OwnedHostNetwork/snapshot');assert after['version']=='2.0' and before['pid']!=after['pid'] and before['config']==after['config'] and before['last']==after['last']
    clone_after=request('GET','/api/v1/plugin/OwnedHostNetworkcopy/snapshot');assert clone_before==clone_after
    stop();start();cold=request('GET','/api/v1/plugin/OwnedHostNetwork/snapshot');assert cold['pid']!=after['pid'] and cold['config']==after['config'] and cold['last']==after['last']
    response(run(),200,b'owned /source-config');response(run(instance='OwnedHostNetworkcopy'),200,b'owned /clone-config')
    observations.append(dict(case='upgrade-clone-cold',before=before,after=after,cold=cold,clone=clone_after))
    if a.hold:
        (directory/'browser-state.json').write_text(json.dumps(dict(origin=origin,ready=True,plugin='OwnedHostNetwork',source= cold['config'],clone=clone_after['config']),indent=2)+'\n');print(json.dumps(dict(status='browser_ready',origin=origin)),flush=True)
        while not (directory/'.browser-done').exists():assert process.poll() is None;time.sleep(.2)
    for instance in ['OwnedHostNetworkcopy','OwnedHostNetwork']:
        value=request('POST','/api/plugins/'+instance+'/uninstall');assert value['data']['success'],value;request('GET','/api/v1/plugin/'+instance+'/snapshot',status=404)
    stop();assert not errors,errors
    binding={name:hashlib.sha256(Path(path).read_bytes()).hexdigest() for name,path in dict(server=a.server,worker=a.worker,worker_v2=a.worker_v2,source_oracle=a.source_oracle,vendor_oracle=a.vendor_oracle,driver=__file__).items()}
    if a.vendor_edge_oracle:binding['vendor_edge_oracle']=hashlib.sha256(Path(a.vendor_edge_oracle).read_bytes()).hexdigest()
    (directory/'result.json').write_text(json.dumps(dict(status='passed',classification='real Linux cmd/server and installed Go SDK lifecycle; actual TLS/HTTP against owned synthetic protocol origins, not live GitHub',binding=binding,observations=observations,trace=trace),indent=2)+'\n');print(json.dumps(dict(status='passed',observations=len(observations),http_events=len(trace))),flush=True)
except BaseException as error:
    (directory/'failure.json').write_text(json.dumps(dict(status='failed',error=type(error).__name__+': '+str(error),peer_errors=errors,observations=observations,trace=trace),indent=2)+'\n');raise
finally:
    stop();proxy.shutdown();proxy.server_close();repo.shutdown();repo.server_close();log.close()
