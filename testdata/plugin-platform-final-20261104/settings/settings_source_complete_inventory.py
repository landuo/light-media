#!/usr/bin/env python3
"""Complete settings-identifier/alias/static and dynamic AST pass over bound77."""
import gzip
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
snapshot = ROOT / "testdata/plugin-resource-full-audit-20261029/installed-source-ast.json.gz"
expected = json.loads(gzip.decompress(snapshot.read_bytes()))["files"]
PROGRAM = r'''
import ast,hashlib,json,sys
from pathlib import Path
expected=json.loads(sys.stdin.readline());results=[]
def expression(n):
 if isinstance(n,ast.Name):return n.id
 if isinstance(n,ast.Attribute):return expression(n.value)+'.'+n.attr
 return '<'+type(n).__name__+'>'
for entry in expected:
 raw=(Path('/app/app/plugins')/entry['file']).read_bytes();h=hashlib.sha256(raw).hexdigest();row=dict(file=entry['file'],expected_sha256=entry['sha256'],sha256=h,matches=h==entry['sha256'],aliases=[],static_reads=[],dynamic_reads=[],namespace_operations=[])
 if not row['matches']:results.append(row);continue
 tree=ast.parse(raw.decode('utf-8-sig'));parents={id(c):n for n in ast.walk(tree)for c in ast.iter_child_nodes(n)};aliases=set();module_aliases=set()
 for n in ast.walk(tree):
  if isinstance(n,ast.ImportFrom)and n.module=='app.core.config':
   aliases.update(a.asname or a.name for a in n.names if a.name=='settings')
  if isinstance(n,ast.ImportFrom)and n.module=='app.core':module_aliases.update(a.asname or a.name for a in n.names if a.name=='config')
  if isinstance(n,ast.Import):module_aliases.update(a.asname or a.name for a in n.names if a.name=='app.core.config')
 def issettings(n):return isinstance(n,ast.Name)and n.id in aliases or isinstance(n,ast.Attribute)and n.attr=='settings'and expression(n.value)in module_aliases
 for _ in range(4):
  prior=set(aliases)
  for n in ast.walk(tree):
   if isinstance(n,ast.Assign)and issettings(n.value):aliases.update(t.id for t in n.targets if isinstance(t,ast.Name))
  if prior==aliases:break
 def owner(n):
  names=[]
  while id(n)in parents:
   n=parents[id(n)]
   if isinstance(n,(ast.ClassDef,ast.FunctionDef,ast.AsyncFunctionDef)):names.append(n.name)
  return '.'.join(reversed(names))
 for n in ast.walk(tree):
  if isinstance(n,ast.Attribute)and issettings(n.value):
   row['static_reads'].append(dict(line=n.lineno,key=n.attr,owner=owner(n),context=type(n.ctx).__name__))
   if n.attr in {'__dict__','dict','model_dump','model_fields'}:row['namespace_operations'].append(dict(line=n.lineno,operation=n.attr,owner=owner(n)))
  if isinstance(n,ast.Call)and isinstance(n.func,ast.Name)and n.func.id in {'getattr','hasattr','setattr','vars','dict'}and n.args and issettings(n.args[0]):
   key=n.args[1].value if len(n.args)>1 and isinstance(n.args[1],ast.Constant)and isinstance(n.args[1].value,str)else None
   row['dynamic_reads'].append(dict(line=n.lineno,operation=n.func.id,key=key,owner=owner(n)))
  if isinstance(n,ast.Subscript)and issettings(n.value):row['namespace_operations'].append(dict(line=n.lineno,operation='subscript',owner=owner(n)))
 row['aliases']=sorted(aliases);row['module_aliases']=sorted(module_aliases);results.append(row)
print(json.dumps(dict(classification='Read-only complete source AST pass: imported settings aliases, simple alias propagation, direct fields, getattr/hasattr/setattr, namespace/subscript access. No plugin imports/runtime/config/DB accesses; field names only.',files=results)))
'''
def ssh(command, payload=None):
    return subprocess.run(["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129", command], input=payload, capture_output=True, check=True, timeout=60).stdout
inspect = "docker inspect --format '{{json .State}}' d1fd69248ee9"
before = json.loads(ssh(inspect))
wrapper = 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-settings-complete-ast","exec"))'
raw = ssh("docker exec -i d1fd69248ee9 python -B -c " + shlex.quote(wrapper), ("exec(" + repr(PROGRAM) + ")\n" + json.dumps(expected) + "\n").encode())
after = json.loads(ssh(inspect))
observed = json.loads(raw)
keys = ["Pid", "StartedAt", "Running", "Restarting"]
observed.update(dict(status="passed" if all(r['matches'] for r in observed['files']) else "source-drift", original_container_unchanged=all(before[k] == after[k] for k in keys), original_state_before={k:before[k] for k in keys}, original_state_after={k:after[k] for k in keys}, bound_source_snapshot_sha256=hashlib.sha256(snapshot.read_bytes()).hexdigest(), driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), program_sha256=hashlib.sha256(PROGRAM.encode()).hexdigest()))
(OUT / "settings-source-complete-inventory.json").write_text(json.dumps(observed, ensure_ascii=False, indent=2) + "\n")
static = [x for f in observed['files'] for x in f['static_reads']]
dynamic = [dict(file=f['file'], **x) for f in observed['files'] for x in f['dynamic_reads']]
print(json.dumps(dict(status=observed['status'],files=len(observed['files']),static=len(static),keys=sorted({x['key'] for x in static}),dynamic=dynamic,namespace_operations=[dict(file=f['file'],**x)for f in observed['files']for x in f['namespace_operations']],original_container_unchanged=observed['original_container_unchanged'])))
