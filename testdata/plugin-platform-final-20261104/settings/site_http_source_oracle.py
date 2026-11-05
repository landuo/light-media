#!/usr/bin/env python3
"""Independent fixed RequestUtils wire observation; no target implementation used."""
import ast
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
SOURCE = subprocess.check_output([
    "git", "-C", "/Users/accidia/projects/MoviePilot", "show", COMMIT + ":app/utils/http.py"
])
tree = ast.parse(SOURCE)
nodes = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in {"_url_decode_if_latin", "cookie_parse"}]
helper = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "RequestUtils")
helper.body = [n for n in helper.body if isinstance(n, ast.FunctionDef) and n.name in {"__init__", "request", "get_res", "post_res"}]
selected = ast.unparse(ast.fix_missing_locations(ast.Module(body=nodes + [helper], type_ignores=[])))
PROGRAM = r'''
import sys,os,types,json,threading,http.server,urllib.parse,importlib.metadata
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
r=json.loads(sys.stdin.readline());settings=types.SimpleNamespace(USER_AGENT='Owned-global')
logger=types.SimpleNamespace(debug=lambda *a:None);get_caller=lambda:None
_REQUESTS_RETRY_IDEMPOTENT_METHODS=set()
exec(r['source'])
trace=[]
class Peer(http.server.BaseHTTPRequestHandler):
 def serve(self):
  raw=self.rfile.read(int(self.headers.get('Content-Length','0')))
  trace.append(dict(method=self.command,path=self.path,body=raw.decode('utf8'),headers={k.lower():v for k,v in self.headers.items() if k.lower() not in ['host','content-length']}))
  code=int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get('status',['200'])[0])
  if self.path.startswith('/redirect'):
   self.send_response(code);self.send_header('Location','/final');self.send_header('Set-Cookie','owned=redirect; Path=/');self.send_header('Content-Length','0');self.end_headers();return
  body=b'owned-response';self.send_response(code);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_GET(self):self.serve()
 def do_POST(self):self.serve()
 def log_message(self,*args):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start()
origin='http://127.0.0.1:'+str(peer.server_port);cases=[]
try:
 for name,options,kwargs,path in [
  ('ordered-repeated-form',dict(cookies='owned=initial; other=a=b',ua='OwnedPlugin/1',referer=origin+'/from'),dict(data=[('second','x y'),('choice[]','1'),('choice[]','2'),('first','中文')]),'/form'),
  ('json-default-header',{},dict(json={'z':1,'a':'中'}),'/json'),
  ('json-explicit-headers',dict(headers={'Content-Type':'application/json','X-Owned':'yes'}),dict(json={'z':1,'a':'中'}),'/json'),
  ('raw-body-non2xx',{},dict(data=b'owned=%2B&x=raw'),'/raw?status=503'),
  ('post-302',dict(cookies='owned=initial'),dict(data=[('key','one')]),'/redirect?status=302'),
  ('post-307',dict(cookies='owned=initial'),dict(data=[('key','one')]),'/redirect?status=307'),
 ]:
  trace.clear();response=RequestUtils(**options).post_res(origin+path,**kwargs)
  result=None if response is None else dict(status=response.status_code,body=response.content.decode('utf8'))
  if response is not None:response.close()
  rows=json.loads(json.dumps(trace))
  for row in rows:
   for key,val in row['headers'].items():row['headers'][key]=val.replace(origin,'<owned-origin>')
  cases.append(dict(name=name,response=result,trace=rows))
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['hash'],requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),cases=cases)))
'''
IMAGE = "sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881"
command = shlex.join([
    "docker", "run", "--rm", "-i", "--name", "light-media-site-http-independent-source-20261104",
    "--network", "none", "--read-only", "--no-healthcheck", "--user", "1000:1001",
    "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "384m",
    "--entrypoint", "/usr/local/bin/python", IMAGE, "-I", "-B", "-S", "-c",
    'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-site-http-source","exec"))',
])
payload = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(dict(source=selected, commit=COMMIT, hash=hashlib.sha256(SOURCE).hexdigest())) + "\n").encode()
result = subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, timeout=60)
(OUT / "site-http-independent-source.stderr").write_bytes(result.stderr)
if result.returncode:
    raise SystemExit(result.returncode)
observed = json.loads(result.stdout)
observed.update(dict(classification="Fixed source AST + actual paired requests/urllib3 in disposable readonly network-none container; only owned loopback HTTP. No target code or business plugin executes.", dependency_image=IMAGE, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), selected_source_sha256=hashlib.sha256(selected.encode()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "site-http-independent-source-oracle.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(dict(cases=len(observed['cases']), requests=observed['requests'], urllib3=observed['urllib3'])))
