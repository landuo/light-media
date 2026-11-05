#!/usr/bin/env python3
"""Supplement direct Attribute inventory with the actual response-header alias."""
import gzip
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
snapshot = ROOT / "testdata/plugin-resource-full-audit-20261029/installed-source-ast.json.gz"
expected = next(r for r in json.loads(gzip.decompress(snapshot.read_bytes()))["files"] if r["file"] == "mpserverstatus/__init__.py")
PROGRAM = r'''
import ast,hashlib,json
from pathlib import Path
p=Path('/app/app/plugins/mpserverstatus/__init__.py');raw=p.read_bytes();tree=ast.parse(raw.decode('utf-8-sig'));parents={id(c):n for n in ast.walk(tree)for c in ast.iter_child_nodes(n)}
function=next(n for n in ast.walk(tree)if isinstance(n,ast.FunctionDef)and n.name=='_build_http_probe')
rows=[]
for n in ast.walk(function):
 if not isinstance(n,ast.Call):continue
 if isinstance(n.func,ast.Attribute)and isinstance(n.func.value,ast.Name)and n.func.value.id=='headers'and n.func.attr=='get'and n.args and isinstance(n.args[0],ast.Constant):
  value=n.args[0].value
  rows.append(dict(line=n.lineno,kind='header_alias_read',name=value if value in {'Server','Date','Content-Type'}else'<other-header-name>'))
 if isinstance(n.func,ast.Name)and n.func.id=='getattr'and len(n.args)>1 and isinstance(n.args[1],ast.Constant):
  value=n.args[1].value
  rows.append(dict(line=n.lineno,kind='dynamic_metadata_read',name=value if value in {'headers','content','text','status_code','raw','version'}else'<other-attribute>'))
print(json.dumps(dict(file='mpserverstatus/__init__.py',sha256=hashlib.sha256(raw).hexdigest(),function_line=function.lineno,function_end_line=function.end_lineno,observations=rows)))
'''
def ssh(command, payload=None):
    return subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, check=True, timeout=60).stdout
inspect = "docker inspect --format '{{json .State}}' d1fd69248ee9"
before = json.loads(ssh(inspect))
wrapper = 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-site-http-dynamic-ast","exec"))'
observed = json.loads(ssh("docker exec -i d1fd69248ee9 python -B -c " + shlex.quote(wrapper), ("exec(" + repr(PROGRAM) + ")\n").encode()))
after = json.loads(ssh(inspect))
keys = ["Pid", "StartedAt", "Running", "Restarting"]
observed.update(dict(status="passed" if observed['sha256'] == expected['sha256'] else "source-drift", expected_sha256=expected['sha256'], classification="Read-only stdlib AST supplement: header alias/getattr metadata reads of the exact bound original source; no plugin imports/runtime/settings/DB access or arbitrary source literals exported.", original_container_unchanged=all(before[k] == after[k] for k in keys), original_state_before={k:before[k] for k in keys}, original_state_after={k:after[k] for k in keys}, driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest(), scope="MPServerStatus uses its own configured server URL with RequestUtils timeout; not a request using host configured-site credentials. Normal Go HTTP exposes these headers/protocol too; no new business implementation accepted here."))
(OUT / "site-http-dynamic-metadata-source-supplement.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(dict(status=observed['status'],observations=observed['observations'],original_container_unchanged=observed['original_container_unchanged'])))
