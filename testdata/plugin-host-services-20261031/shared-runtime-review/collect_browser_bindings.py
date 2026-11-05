#!/usr/bin/env python3
"""Read only the 77 hash-bound installed source files; never import plugins."""
import gzip
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
SNAPSHOT = ROOT / 'testdata/plugin-resource-full-audit-20261029/installed-source-ast.json.gz'
rows = json.loads(gzip.decompress(SNAPSHOT.read_bytes()))['files']
PROGRAM = '''import ast,hashlib,json,sys
from pathlib import Path
rows=json.loads(sys.stdin.readline());out=[]
def name(n):
 if isinstance(n,ast.Name):return n.id
 if isinstance(n,ast.Attribute):return name(n.value)+'.'+n.attr
 if isinstance(n,ast.Call):return name(n.func)+'()'
 return type(n).__name__
for row in rows:
 raw=(Path('/app/app/plugins')/row['file']).read_bytes();sha=hashlib.sha256(raw).hexdigest()
 item={'file':row['file'],'sha256':sha,'expected_sha256':row['sha256'],'matches':sha==row['sha256'],'browser_imports':[],'calls':[]}
 if item['matches']:
  tree=ast.parse(raw.decode('utf-8-sig'))
  for n in ast.walk(tree):
   if isinstance(n,ast.ImportFrom) and n.module=='app.helper.browser':
    item['browser_imports'].extend({'name':a.name,'alias':a.asname,'line':n.lineno}for a in n.names)
   if isinstance(n,ast.Call):
    callee=name(n.func)
    if 'PlaywrightHelper' in callee or callee.endswith('.action'):
     item['calls'].append({'line':n.lineno,'callee':callee,'keyword_names':[k.arg for k in n.keywords]})
 out.append(item)
print(json.dumps(out,ensure_ascii=False))
'''

def ssh(command, data=None):
    return subprocess.run(['ssh', '-o', 'BatchMode=yes', 'admin@192.168.8.129', command], input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True).stdout

inspect = "docker inspect --format '{{json .State}}' d1fd69248ee9"
before = json.loads(ssh(inspect))
wrapper = 'import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-browser-ast","exec"))'
payload = 'exec(' + repr(PROGRAM) + ')\n' + json.dumps([{'file': r['file'], 'sha256': r['sha256']} for r in rows]) + '\n'
values = json.loads(ssh('docker exec -i d1fd69248ee9 python -B -c ' + shlex.quote(wrapper), payload.encode()))
after = json.loads(ssh(inspect))
result = {
    'classification': 'read-only source AST, no application imports or plugin execution; no settings/credentials read',
    'source_snapshot_sha256': hashlib.sha256(SNAPSHOT.read_bytes()).hexdigest(),
    'collector_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
    'file_count': len(values), 'all_hashes_match': all(r['matches'] for r in values),
    'original_container_unchanged': all(before.get(k) == after.get(k) for k in ['Pid', 'StartedAt', 'Restarting', 'Running']),
    'files': values,
}
(OUT / 'browser-installed-bindings.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({k: v for k, v in result.items() if k != 'files'}))
print(json.dumps([r for r in values if r['browser_imports'] or r['calls']], ensure_ascii=False))
