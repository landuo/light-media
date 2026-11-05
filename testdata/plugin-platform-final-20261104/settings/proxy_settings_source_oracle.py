#!/usr/bin/env python3
"""Exact fixed core.config proxy properties, controlled nonsecret inputs only."""
import ast
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

OUT = Path(__file__).resolve().parent
COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
source = subprocess.check_output(["git", "-C", "/Users/accidia/projects/MoviePilot", "show", COMMIT + ":app/core/config.py"])
tree = ast.parse(source)
settings = next(n for n in tree.body if isinstance(n, ast.ClassDef) and any(isinstance(x, ast.FunctionDef) and x.name == "PROXY_SERVER" for x in n.body))
properties = [n for n in settings.body if isinstance(n, ast.FunctionDef) and n.name in {"PROXY", "PROXY_SERVER", "_get_env_proxy"}]
shadow = ast.ClassDef(name="FixedProxySettings", bases=[], keywords=[], body=properties, decorator_list=[])
selected = ast.unparse(ast.fix_missing_locations(ast.Module(body=[shadow], type_ignores=[])))
PROGRAM = r'''
import sys,os,json,types,platform
from typing import *
from urllib.parse import urlparse
r=json.loads(sys.stdin.readline());logger=types.SimpleNamespace(error=lambda *a:None);exec(r['source']);rows=[]
for name,setting,environment in [
 ('unset',None,{}),('empty','',{}),('whitespace','  ',{}),
 ('explicit-over-environment',' http://owned:synthetic@Proxy.Example:3128 ',{'HTTP_PROXY':'http://env-http.invalid:80','HTTPS_PROXY':'http://env-https.invalid:81'}),
 ('explicit-encoded-auth','http://owned%40name:synthetic%3Avalue@Proxy.Example:3128',{}),
 ('explicit-ipv6','http://[::1]:3128',{}),
 ('explicit-no-scheme','127.0.0.1:3128',{}),
 ('http-upper',None,{'HTTP_PROXY':' http://upper-http.invalid:80 '}),
 ('https-lower',None,{'https_proxy':' http://lower-https.invalid:81 '}),
 ('uppercase-nonempty-wins',None,{'HTTP_PROXY':'http://upper.invalid:80','http_proxy':'http://lower.invalid:81'}),
 ('empty-uppercase-falls-through',None,{'HTTP_PROXY':'','http_proxy':'http://lower.invalid:81'}),
 ('whitespace-uppercase-falls-through',None,{'HTTPS_PROXY':'  ','https_proxy':' http://lower.invalid:81 '}),
 ('split-schemes',None,{'HTTP_PROXY':'http://http.invalid:80','https_proxy':'http://https.invalid:81'}),
 ('all-only',None,{'ALL_PROXY':'socks5h://all.invalid:1080'}),
 ('no-proxy-does-not-change-property',None,{'HTTP_PROXY':'http://http.invalid:80','NO_PROXY':'*'}),
 ('cgi-does-not-change-property',None,{'HTTP_PROXY':'http://http.invalid:80','REQUEST_METHOD':'GET'}),
 ('host-specific-does-not-change-property',None,{'http://owned.invalid_proxy':'http://specific.invalid:80'}),
]:
 os.environ.clear();os.environ.update(environment);s=FixedProxySettings();s.PROXY_HOST=setting
 rows.append(dict(name=name,proxy_host=setting,environment=environment,proxy=s.PROXY,proxy_server=s.PROXY_SERVER))
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['hash'],python=platform.python_version(),cases=rows)))
'''
IMAGE = "sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881"
command = shlex.join(["docker", "run", "--rm", "-i", "--name", "light-media-proxy-settings-independent-source-20261105", "--network", "none", "--read-only", "--no-healthcheck", "--user", "1000:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "256m", "--entrypoint", "/usr/local/bin/python", IMAGE, "-I", "-B", "-S", "-c", 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-proxy-settings-source","exec"))'])
payload = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(dict(source=selected, commit=COMMIT, hash=hashlib.sha256(source).hexdigest())) + "\n").encode()
result = subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, timeout=60)
(OUT / "proxy-settings-independent-source.stderr").write_bytes(result.stderr)
if result.returncode:
    raise SystemExit(result.returncode)
observed = json.loads(result.stdout)
observed.update(dict(classification="Exact fixed PROXY/PROXY_SERVER/_get_env_proxy property AST, controlled synthetic inputs and process-local environment in disposable readonly network-none Python3.12 container. Source getters do not perform requests environment merging; that is independently bound existing transport evidence.", dependency_image=IMAGE, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), selected_source_sha256=hashlib.sha256(selected.encode()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "proxy-settings-independent-source-oracle.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(dict(cases=len(observed['cases']), python=observed['python'])))
