#!/usr/bin/env python3
import gzip,hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[3];OUT=pathlib.Path(__file__).resolve().parent
snapshot=ROOT/'testdata/plugin-resource-full-audit-20261029/installed-source-ast.json.gz'
rows=json.loads(gzip.decompress(snapshot.read_bytes()))['files']
def ssh(cmd,data=None):return subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',cmd],input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True).stdout
inspect="docker inspect --format '{{json .State}}' d1fd69248ee9"
before=json.loads(ssh(inspect));program=(OUT/'read_installed_ast.py').read_text()
request=json.dumps([{'file':r['file'],'plugin':r['plugin'],'sha256':r['sha256']}for r in rows])+'\n'
# Code is a single literal line; following stdin contains only bound path/hash metadata.
wrapper='import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-source-ast","exec"))'
import shlex
raw=ssh('docker exec -i d1fd69248ee9 python -B -c '+shlex.quote(wrapper),('exec('+repr(program)+')\n'+request).encode())
after=json.loads(ssh(inspect));values=json.loads(raw)
result={'classification':values['classification'],'bound_source_snapshot_sha256':hashlib.sha256(snapshot.read_bytes()).hexdigest(),'files':values['files'],'status':'passed'if all(r['matches']for r in values['files'])else'source-drift','original_container_unchanged':all(before.get(k)==after.get(k)for k in ['Pid','StartedAt','Restarting','Running']),'original_container_state_before':{k:before.get(k)for k in ['Pid','StartedAt','Restarting','Running']},'original_container_state_after':{k:after.get(k)for k in ['Pid','StartedAt','Restarting','Running']},'collector_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'probe_sha256':hashlib.sha256((OUT/'read_installed_ast.py').read_bytes()).hexdigest()}
(OUT/'installed-residual-ast.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':result['status'],'files':len(result['files']),'mismatch':[r['file']for r in result['files']if not r['matches']],'original_container_unchanged':result['original_container_unchanged'],'settings_attributes':sum(len(r.get('settings_attributes',[]))for r in result['files']),'dynamic_calls':sum(len(r.get('dynamic_calls',[]))for r in result['files'])}))
