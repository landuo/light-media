#!/usr/bin/env python3
"""Read only previously bound installed source AST; no settings or plugin import."""
import gzip
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
snapshot = ROOT / "testdata/plugin-resource-full-audit-20261029/installed-source-ast.json.gz"
expected = json.loads(gzip.decompress(snapshot.read_bytes()))["files"]
PROGRAM = r'''
import ast,hashlib,json,sys
from pathlib import Path
expected=json.loads(sys.stdin.readline());root=Path('/app/app/plugins');rows=[]
def name(n):
 if isinstance(n,ast.Name):return n.id
 if isinstance(n,ast.Attribute):return name(n.value)+'.'+n.attr
 if isinstance(n,ast.Call):return name(n.func)+'()'
 if isinstance(n,ast.Subscript):return name(n.value)+'[]'
 return '<'+type(n).__name__+'>'
for item in expected:
 p=root/item['file'];raw=p.read_bytes();actual=hashlib.sha256(raw).hexdigest()
 row=dict(file=item['file'],expected_sha256=item['sha256'],sha256=actual,matches=actual==item['sha256'],attributes=[],http_assignments=[])
 if not row['matches']:rows.append(row);continue
 tree=ast.parse(raw.decode('utf-8-sig'));parents={id(c):n for n in ast.walk(tree)for c in ast.iter_child_nodes(n)}
 def owner(n):
  ns=[]
  while id(n)in parents:
   n=parents[id(n)]
   if isinstance(n,(ast.ClassDef,ast.FunctionDef,ast.AsyncFunctionDef)):ns.append(n.name)
  return '.'.join(reversed(ns))
 for n in ast.walk(tree):
  if isinstance(n,ast.Attribute)and n.attr in {'headers','url','encoding'}:
   parent=parents.get(id(n));entry=dict(line=n.lineno,attribute=n.attr,expression=name(n),context=type(n.ctx).__name__,owner=owner(n),parent_type=type(parent).__name__)
   call=parents.get(id(parent))if isinstance(parent,ast.Attribute)else None
   if isinstance(call,ast.Call)and call.args:
    arg=call.args[0]
    if isinstance(arg,ast.Constant)and isinstance(arg.value,str)and arg.value.lower()in {'content-type','content-length','content-disposition','location','etag','last-modified','retry-after'}:entry['header_name']=arg.value
    else:entry['header_name']='not-a-whitelisted-static-http-metadata-header'
   if isinstance(parent,ast.Assign)and n.attr=='encoding':
    value=parent.value
    entry['assigned_encoding']=value.value if isinstance(value,ast.Constant)and isinstance(value.value,str)and value.value.lower()in {'utf-8','utf8','latin1','gbk','gb18030'}else '<dynamic-or-non-encoding-literal>'
   row['attributes'].append(entry)
  if isinstance(n,ast.Assign)and isinstance(n.value,ast.Call):
   callee=name(n.value.func)
   if any(term in callee for term in ['RequestUtils','requests.','session.','self._session.','httpx.']):row['http_assignments'].append(dict(line=n.lineno,targets=[name(t)for t in n.targets],callee=callee,owner=owner(n)))
 rows.append(row)
print(json.dumps(dict(classification='Read-only stdlib AST of exact previously bound77 installed plugin files; no imports, settings, DB, credential or runtime accesses; arbitrary literals omitted.',files=rows)))
'''
def ssh(command, payload=None):
    return subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, check=True, timeout=60).stdout
inspect = "docker inspect --format '{{json .State}}' d1fd69248ee9"
before = json.loads(ssh(inspect))
wrapper = 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-site-http-metadata-ast","exec"))'
request = ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(expected) + "\n").encode()
raw = ssh("docker exec -i d1fd69248ee9 python -B -c " + shlex.quote(wrapper), request)
after = json.loads(ssh(inspect))
observed = json.loads(raw)
state_keys = ["Pid", "StartedAt", "Running", "Restarting"]
observed.update(dict(status="passed" if all(x["matches"] for x in observed["files"]) else "source-drift", original_container_unchanged=all(before[k] == after[k] for k in state_keys), original_state_before={k:before[k] for k in state_keys}, original_state_after={k:after[k] for k in state_keys}, bound_source_snapshot_sha256=hashlib.sha256(snapshot.read_bytes()).hexdigest(), driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "site-http-installed-metadata-source-inventory.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(dict(status=observed['status'],files=len(observed['files']),attributes=sum(len(x['attributes']) for x in observed['files']),original_container_unchanged=observed['original_container_unchanged'])))
