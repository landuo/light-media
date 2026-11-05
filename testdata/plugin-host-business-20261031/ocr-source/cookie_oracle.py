#!/usr/bin/env python3
"""Fixed RequestUtils cookie/UA behavior through actual installed requests HTTP."""
import ast,hashlib,json,pathlib,shlex,subprocess
OUT=pathlib.Path(__file__).resolve().parent;COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/utils/http.py']);tree=ast.parse(raw)
functions=[n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name in {'_url_decode_if_latin','cookie_parse'}]
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef)and n.name=='RequestUtils');methods=[n for n in cls.body if isinstance(n,ast.FunctionDef)and n.name in {'__init__','request','get_res'}]
request_class=ast.ClassDef(name='RequestUtils',bases=[],keywords=[],body=methods,decorator_list=[])
selected=ast.unparse(ast.fix_missing_locations(ast.Module(body=[*functions,request_class],type_ignores=[])))
cases=[dict(case=n,cookie=c,ua=u)for n,c,u in [('simple','owned=1','Owned/OCR'),('duplicates_ascii_percent','dup=first;dup=last;value=%20A%2BB;empty=;flag;equal=a=b','Owned/OCR'),('invalid_escape_still_decodes_valid','bad=%ZZ%20;plus=a+b','Owned/OCR'),('nonascii_percent_preserved','latin=%E9;utf8=%E4%B8%AD;mix=%20%E9','Owned/OCR'),('raw_latin1','latin=é','Owned/OCR'),('raw_unicode_raises','word=中文%20','Owned/OCR'),('percent_crlf_network_none','bad=%0A','Owned/OCR'),('no_cookie','', 'Owned/OCR'),('empty_name','=value','Owned/OCR'),('ua_latin1','owned=1','Owned/é'),('ua_nonlatin_raises','owned=1','Owned/中')]]
program=r'''import sys,json,typing,types,threading,http.server,importlib.metadata,os,base64
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
r=json.loads(sys.stdin.readline());logger=types.SimpleNamespace(error=lambda*x:None,debug=lambda*x:None);settings=types.SimpleNamespace(USER_AGENT='OwnedHost/1');get_caller=lambda:None
_REQUESTS_RETRY_IDEMPOTENT_METHODS={'GET','HEAD','OPTIONS'}
exec(r['selected']);trace=[]
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):trace.append(dict(cookie=self.headers.get('Cookie'),ua=self.headers.get('User-Agent'),cookie_bytes_b64=base64.b64encode(self.headers.get('Cookie','').encode('latin-1')).decode(),ua_bytes_b64=base64.b64encode(self.headers.get('User-Agent','').encode('latin-1')).decode()));self.send_response(200);self.end_headers();self.wfile.write(b'owned')
 def log_message(self,*args):pass
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=server.serve_forever,daemon=True).start();out=[]
try:
 for row in r['cases']:
  trace.clear();result=dict(row,parsed=cookie_parse(row['cookie']))
  try:
   response=RequestUtils(cookies=row['cookie'],ua=row['ua']).get_res('http://127.0.0.1:'+str(server.server_port)+'/image')
   result['response_present']=response is not None
  except Exception as exc:result['error']=type(exc).__name__
  result['trace']=list(trace);out.append(result)
finally:server.shutdown();server.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['sha256'],requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),classification='fixed RequestUtils/cookie parser AST + actual installed requests/urllib3 over owned loopback HTTP; no app imports/config/DB/old service calls',cases=out),ensure_ascii=False))
'''
def docker(*args,data=None,check=True):return subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',shlex.join(['docker',*args])],input=data,capture_output=True,check=check,timeout=60)
def snapshot():return docker('inspect','--format','{{.State.Pid}} {{.State.StartedAt}} {{.RestartCount}}','d1fd69248ee9').stdout.decode().strip()
before=snapshot();cid=None;name='light-media-owned-ocr-cookie-source-20261101'
try:
 cid=docker('create','-i','--name',name,'--network','none','--read-only','--no-healthcheck','--cpus','1','--memory','384m','--pids-limit','64','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--entrypoint','/usr/local/bin/python','sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881','-I','-B','-S','-c','import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-ocr-cookie","exec"))').stdout.decode().strip()
 run=docker('start','-a','-i',cid,data=('exec('+repr(program)+')\n'+json.dumps(dict(commit=COMMIT,sha256=hashlib.sha256(raw).hexdigest(),selected=selected,cases=cases))+'\n').encode(),check=False)
 (OUT/'cookie-source-stderr.log').write_bytes(run.stderr);assert run.returncode==0,run.stderr.decode();value=json.loads(run.stdout)
 (OUT/'cookie-oracle.json').write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n');print(json.dumps(dict(cases=len(value['cases']),requests=value['requests'],urllib3=value['urllib3'],errors=sum('error'in x for x in value['cases']))))
finally:
 if cid:docker('rm','-f','-v',cid)
 after=snapshot();absent=not docker('ps','-a','--filter','name=^/'+name+'$','--format','{{.ID}}').stdout.strip();(OUT/'cookie-cleanup.json').write_text(json.dumps(dict(original_before=before,original_after=after,original_unchanged=before==after,owned_container_absent=absent,driver_sha256=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()),indent=2)+'\n');assert before==after and absent
