#!/usr/bin/env python3
"""Fixed complete OCR helper + actual paired requests transport; owned loopback."""
import ast,hashlib,json,pathlib,shlex,subprocess
out=pathlib.Path(__file__).resolve().parent;commit='8a83efeacd3e26b82be495e17d50146e31c96a60';repo='/Users/accidia/projects/MoviePilot'
http=subprocess.check_output(['git','-C',repo,'show',commit+':app/utils/http.py']);ocr=subprocess.check_output(['git','-C',repo,'show',commit+':app/helper/ocr.py']);tree=ast.parse(http)
functions=[n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name in {'_url_decode_if_latin','cookie_parse'}];cls=next(n for n in tree.body if isinstance(n,ast.ClassDef)and n.name=='RequestUtils');cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef)and n.name in {'__init__','request','get_res','post_res'}];selected=ast.unparse(ast.fix_missing_locations(ast.Module(body=[*functions,cls],type_ignores=[])));ocrclass=next(n for n in ast.parse(ocr).body if isinstance(n,ast.ClassDef)and n.name=='OcrHelper');ocrselected=ast.unparse(ast.fix_missing_locations(ast.Module(body=[ocrclass],type_ignores=[])))
program=r'''import sys,os,json,types,threading,http.server,urllib.parse,base64,typing,importlib.metadata
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
r=json.loads(sys.stdin.readline());settings=types.SimpleNamespace(USER_AGENT='Owned-global',OCR_HOST='');get_caller=lambda:None;logger=types.SimpleNamespace(debug=lambda*x:None);_REQUESTS_RETRY_IDEMPOTENT_METHODS={'GET','HEAD','OPTIONS'}
exec(r['http']);trace=[];image_redirects=0;post_redirects=0
class Peer(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  step=int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('step',['0'])[0]);trace.append(dict(method='GET',step=step,cookie=self.headers.get('Cookie')))
  if step<image_redirects:self.send_response(307);self.send_header('Location','/image?step='+str(step+1));self.send_header('Set-Cookie','owned=redirect; Path=/');self.send_header('Content-Length','0');self.end_headers();return
  body=b'owned-image';self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])));step=int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('step',['0'])[0]);trace.append(dict(method='POST',step=step,cookie=self.headers.get('Cookie'),body=body))
  if step<post_redirects:self.send_response(307);self.send_header('Location','/captcha/base64?step='+str(step+1));self.send_header('Set-Cookie','owned=redirect; Path=/');self.send_header('Content-Length','0');self.end_headers();return
  raw=json.dumps({'result':'OWNED'}).encode();self.send_response(200);self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
 def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start();origin='http://127.0.0.1:'+str(peer.server_port);settings.OCR_HOST=origin;rows=[]
try:
 for name,count in [('image',1),('image',30),('image',31),('post',30),('post',31)]:
  image_redirects=count if name=='image'else 0;post_redirects=count if name=='post'else 0;trace.clear();exec(r['ocr']);arguments={'image_b64':'ZmFsbGJhY2s='}
  if name=='image':arguments.update(image_url=origin+'/image',cookie='owned=initial; other=yes',ua='Owned/OCR')
  result=OcrHelper().get_captcha_text(**arguments);rows.append(dict(kind=name,redirects=count,result=result,trace=list(trace)))
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_hashes=r['hashes'],requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),classification='actual fixed complete OcrHelper + fixed RequestUtils AST + paired dependencies in disposable no-network read-only container; owned loopback only',cases=rows)))
'''
image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881';name='light-media-ocr-redirect-source-20261104'
command=shlex.join(['docker','run','--rm','-i','--name',name,'--network','none','--read-only','--no-healthcheck','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--pids-limit','64','--memory','384m','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c','import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-ocr-redirect-source","exec"))'])
payload=('exec('+repr(program)+')\n'+json.dumps(dict(commit=commit,hashes={'app/utils/http.py':hashlib.sha256(http).hexdigest(),'app/helper/ocr.py':hashlib.sha256(ocr).hexdigest()},http=selected,ocr=ocrselected))+'\n').encode()
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',command],input=payload,capture_output=True,timeout=60);(out/'ocr-redirect-source-stderr.log').write_bytes(r.stderr);assert r.returncode==0,r.stderr
j=json.loads(r.stdout);j['driver_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest();j['program_sha256']=hashlib.sha256(program.encode()).hexdigest();j['dependency_image']=image;(out/'ocr-redirect-source-oracle.json').write_text(json.dumps(j,indent=2)+'\n');print(json.dumps([dict(kind=row['kind'],redirects=row['redirects'],result=row['result'],requests=len(row['trace']),second_cookie=row['trace'][1]['cookie'])for row in j['cases']]))
