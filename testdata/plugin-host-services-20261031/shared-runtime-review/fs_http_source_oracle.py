#!/usr/bin/env python3
"""Pinned RequestUtils.post_json with actual NAS-paired HTTP dependencies.

Runs in the coordinator's disposable dependency image with --network none.
Only an owned loopback HTTP server is used. Not a product Python dependency.
"""
import hashlib
import json
from pathlib import Path
import subprocess

OUT=Path(__file__).resolve().parent
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/utils/http.py']).decode()
program='source='+repr(raw)+'\n'+r'''
import ast,json,http.server,threading,types,typing,requests,urllib3,hashlib,gzip,brotli,time,urllib.parse
cls=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef) and n.name=='RequestUtils')
cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ['__init__','request','post_json']]
ns={'requests':requests,'settings':types.SimpleNamespace(USER_AGENT='owned-UA'),'get_caller':lambda:None,'cookie_parse':lambda v:v,'logger':types.SimpleNamespace(debug=lambda *a,**k:None),'_REQUESTS_RETRY_IDEMPOTENT_METHODS':('GET','HEAD','OPTIONS'),'Response':requests.Response,'Session':requests.Session,'Union':typing.Union,'Optional':typing.Optional,'Any':typing.Any}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'fixed:http.py','exec'),ns)
traces=[]
class Peer(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  request=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  traces.append({'case':self.path,'headers':dict(self.headers),'json':request})
  parsed=urllib.parse.urlsplit(self.path)
  if parsed.path.startswith('/redirect-'):
   count=int(parsed.path.rsplit('-',1)[1]);step=int(urllib.parse.parse_qs(parsed.query).get('step',['0'])[0])
   if step<count:
    self.send_response(307);self.send_header('Location',parsed.path+'?step='+str(step+1));self.send_header('Content-Length','0')
    if step==0:self.send_header('Set-Cookie','owned_redirect=one; Path=/')
    self.end_headers();return
  body=json.dumps({'status':'ok','solution':{'response':'owned'}}).encode();status=200;encoding=''
  if self.path=='/gzip':body=gzip.compress(body);encoding='gzip'
  if self.path=='/br':body=brotli.compress(body);encoding='br'
  if self.path=='/500':status=500
  if self.path=='/invalid':body=b'owned invalid JSON'
  self.send_response(status)
  if encoding:self.send_header('Content-Encoding',encoding)
  self.send_header('Content-Length',str(len(body)));self.end_headers()
  if self.path=='/progress':
   try:
    for offset in range(0,len(body),10):self.wfile.write(body[offset:offset+10]);self.wfile.flush();time.sleep(.05)
   except (BrokenPipeError,ConnectionResetError):pass
  else:self.wfile.write(body)
 def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start()
rows=[];redirects=[]
try:
 for name in ['plain','gzip','br','500','invalid','progress']:
  start=time.monotonic();result=ns['RequestUtils'](content_type='application/json',timeout=.15 if name=='progress' else 3).post_json('http://127.0.0.1:'+str(peer.server_port)+'/'+name,json={'cmd':'request.get','url':'https://owned.invalid/','maxTimeout':3000})
  rows.append({'case':name,'result':result,'elapsed_seconds':round(time.monotonic()-start,3)})
 for count in [30,31]:
  before=len(traces);result=ns['RequestUtils'](content_type='application/json',timeout=3).post_json('http://127.0.0.1:'+str(peer.server_port)+'/redirect-'+str(count),json={'cmd':'request.get','url':'https://owned.invalid/','maxTimeout':3000})
  redirects.append({'redirect_count':count,'result':result,'actual_http_requests':len(traces)-before,'second_request_cookie':traces[before+1]['headers'].get('Cookie')})
finally:peer.shutdown();peer.server_close()
for trace in traces:trace['headers'].pop('Host',None)
print(json.dumps({'source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(source.encode()).hexdigest(),'classification':'fixed RequestUtils AST with actual vendor HTTP in disposable network-none image; owned loopback only','requests':requests.__version__,'urllib3':urllib3.__version__,'traces':traces,'cases':rows,'redirects':redirects}))
'''
(OUT/'fs-http-source-driver.py').write_text(program)
image='33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129','docker','run','--rm','-i','--network','none','--entrypoint','python',image,'-B','-'],input=program,text=True,capture_output=True)
(OUT/'fs-http-source-stderr.log').write_text(r.stderr)
if r.returncode:raise SystemExit(r.stderr)
result=json.loads(r.stdout)
result['capture_driver_sha256']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
result['executed_driver_sha256']=hashlib.sha256(program.encode()).hexdigest()
result['dependency_image']=image
(OUT/'fs-http-source-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'cases':len(result['cases']),'requests':result['requests'],'urllib3':result['urllib3'],'headers':result['traces'][0]['headers']}))
