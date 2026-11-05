#!/usr/bin/env python3
"""Exact RequestUtils/get_caller source with real plugin-named frame filenames."""
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
functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in {"_url_decode_if_latin", "cookie_parse", "get_caller"}]
helper = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "RequestUtils")
helper.body = [n for n in helper.body if isinstance(n, ast.FunctionDef) and n.name in {"__init__", "request", "get_res", "post_res"}]
selected = ast.unparse(ast.fix_missing_locations(ast.Module(body=functions + [helper], type_ignores=[])))
PROGRAM = r'''
import sys,os,types,json,threading,http.server,importlib.metadata
from pathlib import Path
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
r=json.loads(sys.stdin.readline());settings=types.SimpleNamespace(USER_AGENT='OwnedHost/1 (Linux; owned)');logger=types.SimpleNamespace(debug=lambda *a:None)
_REQUESTS_RETRY_IDEMPOTENT_METHODS=set();exec(compile(r['source'],'/app/app/utils/http.py','exec'));observed=[]
class Peer(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  observed.append(self.headers.get('User-Agent'));self.send_response(200);self.send_header('Content-Length','0');self.end_headers()
 def log_message(self,*args):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start();origin='http://127.0.0.1:'+str(peer.server_port);rows=[]
try:
 for module,mode in [('owned_source','global'),('owned_source_clone','global'),('owned_source','browser'),('owned_source','explicit')]:
  namespace=dict(globals());namespace['options']={'headers':{'User-Agent':settings.USER_AGENT}}if mode=='explicit'else {'ua':settings.USER_AGENT if mode=='global'else'OwnedBrowser/1'}
  plugin='def inner():\n return RequestUtils(**options).get_res(origin)\ndef outer():\n return inner()\n'
  exec(compile(plugin,'/app/app/plugins/'+module+'/__init__.py','exec'),namespace);response=namespace['outer']();response.close();rows.append(dict(module=module,mode=mode,wire_user_agent=observed[-1]))
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['hash'],controlled_global_user_agent=settings.USER_AGENT,requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),cases=rows)))
'''
IMAGE = "sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881"
command = shlex.join(["docker", "run", "--rm", "-i", "--name", "light-media-site-ua-independent-source-20261104", "--network", "none", "--read-only", "--no-healthcheck", "--user", "1000:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "384m", "--entrypoint", "/usr/local/bin/python", IMAGE, "-I", "-B", "-S", "-c", 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-site-ua-source","exec"))'])
payload = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(dict(source=selected, commit=COMMIT, hash=hashlib.sha256(source).hexdigest())) + "\n").encode()
result = subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, timeout=60)
(OUT / "site-http-useragent-independent-source.stderr").write_bytes(result.stderr)
if result.returncode:
    raise SystemExit(result.returncode)
observed = json.loads(result.stdout)
observed.update(dict(classification="Fixed RequestUtils+get_caller AST with actual paired requests, controlled nonsecret global USER_AGENT, real plugin-named frame filenames for source/clone. No target implementation or business plugin executes.", dependency_image=IMAGE, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), selected_source_sha256=hashlib.sha256(selected.encode()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "site-http-useragent-independent-source-oracle.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(observed['cases']))
