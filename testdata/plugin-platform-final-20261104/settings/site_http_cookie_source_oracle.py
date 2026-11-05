#!/usr/bin/env python3
"""Source response-cookie extraction and next-RequestUtils forwarding, independently."""
import ast
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

OUT = Path(__file__).resolve().parent
COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
source = subprocess.check_output(["git", "-C", "/Users/accidia/projects/MoviePilot", "show", COMMIT + ":app/utils/http.py"])
tree = ast.parse(source)
functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in {"_url_decode_if_latin", "cookie_parse"}]
helper = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "RequestUtils")
helper.body = [n for n in helper.body if isinstance(n, ast.FunctionDef) and n.name in {"__init__", "request", "get_res", "post_res"}]
selected = ast.unparse(ast.fix_missing_locations(ast.Module(body=functions + [helper], type_ignores=[])))
PROGRAM = r'''
import sys,os,types,json,threading,http.server,importlib.metadata
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
r=json.loads(sys.stdin.readline());settings=types.SimpleNamespace(USER_AGENT='Owned-global');logger=types.SimpleNamespace(debug=lambda *a:None);get_caller=lambda:None
_REQUESTS_RETRY_IDEMPOTENT_METHODS=set();exec(r['source'])
headers=[];forwarded=[]
class Peer(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200)
  for value in headers:self.send_header('Set-Cookie',value)
  self.send_header('Content-Length','0');self.end_headers()
 def do_POST(self):
  self.rfile.read(int(self.headers.get('Content-Length','0')));forwarded.append(self.headers.get('Cookie'))
  self.send_response(200);self.send_header('Content-Length','0');self.end_headers()
 def log_message(self,*args):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start();origin='http://127.0.0.1:'+str(peer.server_port);rows=[]
try:
 for name,values in [
  ('same-name-different-path',['owned=root; Path=/','owned=deep; Path=/deep']),
  ('foreign-domain',['owned=foreign; Domain=other.invalid; Path=/']),
  ('max-age-zero',['owned=gone; Max-Age=0; Path=/']),
  ('expires-past',['owned=old; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/']),
  ('expires-future',['owned=future; Expires=Tue, 01 Jan 2036 00:00:00 GMT; Path=/']),
  ('empty-value',['owned=; Path=/']),
  ('same-response-set-then-delete',['owned=first; Path=/','owned=deleted; Max-Age=0; Path=/']),
  ('secure-cookie-on-http',['owned=secure; Secure; Path=/']),
 ]:
  headers=values;response=RequestUtils(cookies='owned=config').get_res(origin+'/response')
  cookie_dict=response.cookies.get_dict();cookie_rows=[dict(name=c.name,value=c.value,domain=c.domain,path=c.path,secure=c.secure)for c in response.cookies];response.close()
  forwarded.clear();following=RequestUtils(cookies=cookie_dict).post_res(origin+'/next',data={'owned':'next'});following.close()
  rows.append(dict(name=name,set_cookie=values,response_cookie_dict=cookie_dict,response_cookie_entries=cookie_rows,next_request_cookie=forwarded[0]))
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['hash'],requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),cases=rows)))
'''
IMAGE = "sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881"
command = shlex.join(["docker", "run", "--rm", "-i", "--name", "light-media-site-cookie-independent-source-20261104", "--network", "none", "--read-only", "--no-healthcheck", "--user", "1000:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "384m", "--entrypoint", "/usr/local/bin/python", IMAGE, "-I", "-B", "-S", "-c", 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-site-cookie-source","exec"))'])
payload = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(dict(source=selected, commit=COMMIT, hash=hashlib.sha256(source).hexdigest())) + "\n").encode()
result = subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, timeout=60)
(OUT / "site-http-cookie-independent-source.stderr").write_bytes(result.stderr)
if result.returncode:
    raise SystemExit(result.returncode)
observed = json.loads(result.stdout)
observed.update(dict(classification="Fixed source RequestUtils response.cookies.get_dict then fresh RequestUtils(cookies=dict), actual paired requests/urllib3; owned loopback only, readonly network-none disposable container; no target code.", dependency_image=IMAGE, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), selected_source_sha256=hashlib.sha256(selected.encode()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "site-http-cookie-independent-source-oracle.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(dict(cases=len(observed['cases']), requests=observed['requests'], urllib3=observed['urllib3'])))
