#!/usr/bin/env python3
"""Actual fixed-source POST retry behavior with and without idempotency header."""
import ast
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

OUT = Path(__file__).resolve().parent
COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
raw = subprocess.check_output(["git", "-C", "/Users/accidia/projects/MoviePilot", "show", COMMIT + ":app/utils/http.py"])
tree = ast.parse(raw)
functions = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in {"_url_decode_if_latin", "cookie_parse"}]
helper = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "RequestUtils")
helper.body = [n for n in helper.body if isinstance(n, ast.FunctionDef) and n.name in {"__init__", "request", "post_res"}]
selected = ast.unparse(ast.fix_missing_locations(ast.Module(body=functions + [helper], type_ignores=[])))
PROGRAM = r'''
import sys,os,json,types,threading,http.server,socket,importlib.metadata
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import requests
from requests import Session,Response
from urllib.parse import quote,unquote
from typing import *
for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(key,None)
r=json.loads(sys.stdin.readline());settings=types.SimpleNamespace(USER_AGENT='Owned-global');logger=types.SimpleNamespace(debug=lambda *a:None);get_caller=lambda:None;_REQUESTS_RETRY_IDEMPOTENT_METHODS=set();exec(r['source']);again=0
class Peer(http.server.BaseHTTPRequestHandler):
 protocol_version='HTTP/1.1'
 def do_POST(self):
  global again
  self.rfile.read(int(self.headers.get('Content-Length','0')))
  if self.path=='/begin':
   self.send_response(307);self.send_header('Location','/again');self.send_header('Content-Length','0');self.end_headers();return
  again+=1
  if again==1:
   self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
  self.send_response(200);self.send_header('Content-Length','0');self.end_headers()
 def log_message(self,*args):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start();origin='http://127.0.0.1:'+str(peer.server_port);rows=[]
try:
 for with_key in [False,True]:
  again=0;options={'headers':{'Idempotency-Key':'owned-private-binding'}}if with_key else{}
  response=RequestUtils(**options).post_res(origin+'/begin',data=b'owned=write')
  rows.append(dict(idempotency_key=with_key,again_body_receipts=again,response_present=response is not None))
  if response is not None:response.close()
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['hash'],requests=importlib.metadata.version('requests'),urllib3=importlib.metadata.version('urllib3'),cases=rows)))
'''
IMAGE = "sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881"
command = shlex.join(["docker", "run", "--rm", "-i", "--name", "light-media-post-retry-independent-source-20261105", "--network", "none", "--read-only", "--no-healthcheck", "--user", "1000:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "384m", "--entrypoint", "/usr/local/bin/python", IMAGE, "-I", "-B", "-S", "-c", 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-post-retry-source","exec"))'])
payload = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(dict(source=selected, commit=COMMIT, hash=hashlib.sha256(raw).hexdigest())) + "\n").encode()
result = subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, timeout=60)
(OUT / "site-http-post-retry-independent-source.stderr").write_bytes(result.stderr)
if result.returncode:
    raise SystemExit(result.returncode)
observed = json.loads(result.stdout)
observed.update(dict(classification="Actual fixed RequestUtils/paired requests POST307 connection reuse then response EOF, with and without Idempotency-Key; only owned loopback, no target code.", dependency_image=IMAGE, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), selected_source_sha256=hashlib.sha256(selected.encode()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "site-http-post-retry-independent-source-oracle.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(observed['cases']))
