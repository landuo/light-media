"""Actual installed AST + pinned requests against disposable loopback TLS only.

Run with .network-reference-venv/bin/python. No original application import,
user configuration, credential lookup, notification or external probe occurs.
"""
import ast
import gzip
import hashlib
import http.server
import importlib.metadata
import json
import os
import pathlib
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import types
import typing
import zlib
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from urllib.parse import urlparse

import requests as actual_requests
import brotli
import zstandard

ROOT=pathlib.Path(__file__).resolve().parent
source=json.loads((ROOT/'installed-source.json').read_text())
for name,version in source['installed_http_distributions'].items():
    if version is not None:assert importlib.metadata.version(name)==version,(name,version)

STATUS='Active connections: 12\nserver accepts handled requests\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n'
records=[]
records_lock=threading.Lock()
class Server(http.server.ThreadingHTTPServer):daemon_threads=True
class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args):pass
    def version_string(self):return 'synthetic-nginx'
    def do_GET(self):
        assert self.headers.get('Authorization') is None
        assert self.headers.get('Cookie') is None
        with records_lock:records.append({'path':self.path,'headers':dict(self.headers)})
        if self.path=='/slow':time.sleep(.15)
        if self.path=='/redirect':
            self.send_response(302);self.send_header('Location','/status');self.send_header('Content-Length','0');self.end_headers();return
        body=STATUS.encode();content_type='text/plain'
        if self.path=='/latin1':body+=b'\xe9\x80'
        elif self.path=='/utf8':body+='标签'.encode();content_type='text/plain; charset=utf-8'
        elif self.path=='/invalid-utf8':body+=b'\xff\xff\xe2\x82';content_type='text/plain; charset=utf-8'
        elif self.path=='/json-default':body+='标签'.encode();content_type='application/json'
        elif self.path=='/apparent':body+='标签'.encode();content_type=None
        encoding=None
        if self.path=='/gzip':body=gzip.compress(body,mtime=0);encoding='gzip'
        elif self.path=='/deflate':body=zlib.compress(body);encoding='deflate'
        elif self.path=='/deflate-raw':encoder=zlib.compressobj(wbits=-zlib.MAX_WBITS);body=encoder.compress(body)+encoder.flush();encoding='deflate'
        elif self.path=='/br':body=brotli.compress(body);encoding='br'
        elif self.path=='/zstd':body=zstandard.ZstdCompressor().compress(body);encoding='zstd'
        self.send_response(503 if self.path=='/503' else 200)
        if content_type:self.send_header('Content-Type',content_type)
        if encoding:self.send_header('Content-Encoding',encoding)
        self.send_header('Content-Length',str(len(body)+(10 if self.path=='/truncated' else 0)))
        self.end_headers()
        try:
            if self.path=='/trickle':
                for offset in range(0,len(body),12):
                    time.sleep(.03);self.wfile.write(body[offset:offset+12]);self.wfile.flush()
            else:self.wfile.write(body);self.wfile.flush()
        except OSError:pass
        if self.path=='/truncated':self.close_connection=True

class Logger:
    def __getattr__(self,key):return lambda *a,**kw:None

def build_classes(origin):
    state=types.SimpleNamespace(mode='',calls=[],constructors=[])
    def send(method,target,**kwargs):
        assert target.startswith(origin+'/') and urlparse(target).hostname=='127.0.0.1'
        state.calls.append({'method':method,'path':urlparse(target).path,**kwargs})
        if state.mode=='unexpected':raise ValueError('synthetic unexpected request error')
        if state.mode=='request-exception':raise actual_requests.exceptions.ConnectionError('synthetic request exception')
        if state.mode=='signature' and len(state.calls)==1:raise TypeError('synthetic legacy request signature')
        return actual_requests.request(method,target,**kwargs)
    namespace={'requests':types.SimpleNamespace(request=send,exceptions=actual_requests.exceptions),'Session':actual_requests.Session,'Response':actual_requests.Response,'Optional':typing.Optional,'Union':typing.Union,'Any':typing.Any,'logger':Logger(),'settings':types.SimpleNamespace(USER_AGENT='synthetic-unused-agent'),'get_caller':lambda:None,'cookie_parse':lambda _:(_ for _ in ()).throw(AssertionError('no cookies permitted')),'_REQUESTS_RETRY_IDEMPOTENT_METHODS':('GET','HEAD','OPTIONS')}
    methods=[ast.parse(text).body[0] for text in source['http_methods'].values()]
    klass=ast.ClassDef(name='RequestUtils',bases=[],keywords=[],body=methods,decorator_list=[])
    exec(compile(ast.fix_missing_locations(ast.Module(body=[klass],type_ignores=[])),'<actual-installed-RequestUtils>','exec'),namespace)
    request_class=namespace['RequestUtils']
    def factory(*args,**kwargs):state.constructors.append(kwargs.copy());return request_class(*args,**kwargs)
    ns={'_PluginBase':object,'List':typing.List,'Tuple':typing.Tuple,'Dict':typing.Dict,'Any':typing.Any,'Optional':typing.Optional,'re':__import__('re'),'socket':socket,'ssl':ssl,'time':time,'datetime':datetime,'timezone':timezone,'parsedate_to_datetime':parsedate_to_datetime,'urlparse':urlparse,'logger':Logger(),'RequestUtils':factory}
    exec(compile(ast.parse(source['class_source']),'<actual-installed-MPServerStatus>','exec'),ns)
    return ns['MPServerStatus'],request_class,state

with tempfile.TemporaryDirectory(prefix='status-network-fixture-') as directory:
    directory=pathlib.Path(directory)
    # Child-process environment only. An empty owned netrc prevents requests
    # from consulting any real account file for loopback fixture requests.
    (directory/'empty-netrc').write_text('')
    os.environ['NETRC']=str(directory/'empty-netrc')
    os.environ['NO_PROXY']='127.0.0.1,localhost'
    certificate,key=directory/'cert.pem',directory/'key.pem'
    subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1','-keyout',str(key),'-out',str(certificate)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    server=Server(('127.0.0.1',0),Handler)
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(certificate,key);server.socket=context.wrap_socket(server.socket,server_side=True)
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    origin='https://127.0.0.1:'+str(server.server_port)
    Plugin,RequestUtils,state=build_classes(origin)
    cases=[]
    try:
        for path in ['/status','/latin1','/utf8','/invalid-utf8','/json-default','/apparent','/gzip','/deflate','/deflate-raw','/br','/zstd','/redirect','/503','/truncated','/slow','/trickle']:
            state.calls=[];state.constructors=[];state.mode=''
            with records_lock:records.clear()
            p=Plugin();p.init_plugin({'enable':True});p._server_base=origin+path
            if path=='/slow':p._status_timeout=.03
            if path=='/trickle':p._status_timeout=.1
            response,seconds,error=p._request_server_status()
            result={'none':response is None,'request_error':error,'request_seconds_nonnegative':seconds>=0}
            if response is not None:result.update(status=response.status_code,bool=bool(response),text=response.text,encoding=response.encoding,apparent_encoding=response.apparent_encoding,content_hex=response.content.hex(),protocol=response.raw.version,final_path=urlparse(response.url).path,content_encoding=response.headers.get('Content-Encoding'),content_length_header=response.headers.get('Content-Length'))
            if path=='/trickle':result['elapsed_exceeded_read_timeout']=seconds>p._status_timeout
            if path=='/status':
                dns=p._get_dns_info();tls=p._get_tls_info()
                result['dns']={k:v for k,v in dns.items() if k not in ('seconds','port')}
                result['tls']={k:v for k,v in tls.items() if k!='seconds'}
                result['tls_failed_verification']='CERTIFICATE_VERIFY_FAILED' in tls['error']
                result['tls_failure_cached']=p._get_tls_info() is tls
                result['http_metrics']=p._build_metrics(p._parse_status_text(response.text),seconds)['active_connections']
            with records_lock:wire=[{'path':x['path'],'headers':{k:('<fixture-host>' if k.lower()=='host' else v) for k,v in x['headers'].items()}} for x in records]
            cases.append({'path':path,'constructors':state.constructors[:],'request_calls':state.calls[:],'wire':wire,'result':result})
        for mode in ['request-exception','unexpected','signature']:
            state.calls=[];state.constructors=[];state.mode=mode
            p=Plugin();p.init_plugin({'enable':True});p._server_base=origin+'/status'
            response,seconds,error=p._request_server_status()
            cases.append({'mode':mode,'constructors':state.constructors[:],'request_calls':state.calls[:],'result':{'none':response is None,'status':response.status_code if response is not None else None,'request_error':error}})
        # A normal requests call with verification kept enabled rejects the
        # same disposable certificate. No process/global verifier is changed.
        strict_failed=False
        try:actual_requests.get(origin+'/status',timeout=1)
        except actual_requests.exceptions.SSLError:strict_failed=True
        assert strict_failed
        assert cases[0]['result']['tls_failed_verification'] and cases[0]['result']['http_metrics']==12
        for case in cases:
            for call in case['request_calls']:assert call['verify'] is False and call['cookies'] is None and call['proxies'] is None
        output={'provenance':{'plugin_source_sha256':source['source_sha256'],'http_source_sha256':source['http_source_sha256'],'distributions':source['installed_http_distributions'],'runtime':'actual installed AST plus matching pinned dependency versions on local disposable self-signed loopback server','python':__import__('sys').version,'openssl':ssl.OPENSSL_VERSION,'strict_http_rejects_same_certificate':strict_failed,'network_scope':'127.0.0.1 only; generated key destroyed; no original app imports/user configuration/credentials/notifications; netrc is owned empty fixture'},'cases':cases}
        raw=json.dumps(output,ensure_ascii=False,indent=2)+'\n';(ROOT.parent/'server-status-http-original-reference.json').write_text(raw)
        print(json.dumps({'cases':len(cases),'tls_strategy_difference_proved':True,'sha256':hashlib.sha256(raw.encode()).hexdigest()}))
    finally:server.shutdown();server.server_close();thread.join()
