"""可携带独立只读产品测试；全部替换仅为新测试与自有 worker。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

p=argparse.ArgumentParser()
p.add_argument('--repo',required=True)
p.add_argument('--output',required=True)
p.add_argument('--race',action='store_true')
a=p.parse_args()
root=Path(a.repo).resolve();fixtures=Path(__file__).resolve().parent;out=Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='owned-s05-independent-') as temp:
    temp=Path(temp)
    tests={'Replace':{str(root/'internal/app/independent_s05_audit_test.go'):str(fixtures/'independent_semantics_test.go'),str(root/'internal/app/independent_s05_close_audit_test.go'):str(fixtures/'close_upgrade_test.go')}}
    worker={'Replace':{str(root/'cmd/owned-shared-host-fixture/main.go'):str(fixtures/'worker_main.go')}}
    test_path=temp/'tests.json';worker_path=temp/'worker.json'
    test_path.write_text(json.dumps(tests));worker_path.write_text(json.dumps(worker))
    command=['go','test']+(['-race'] if a.race else [])+['-overlay='+str(test_path),'./internal/app','-run','^TestAuditS05','-count=1','-v']
    env=dict(os.environ);env['S05_AUDIT_FIXTURES']=str(fixtures);env['S05_AUDIT_WORKER_OVERLAY']=str(worker_path);env['OWNED_SHARED_HOST_RACE']='1' if a.race else '0'
    (out/'command.json').write_text(json.dumps({'command':command,'cwd':str(root),'race_worker':a.race,'test_overlay':tests,'worker_overlay':worker},indent=2)+'\n')
    with (out/'run.log').open('w') as f: rc=subprocess.run(command,cwd=root,env=env,stdout=f,stderr=subprocess.STDOUT).returncode
    (out/'run.exit').write_text(str(rc)+'\n')
    text=(out/'run.log').read_text();cases=re.findall(r'^--- (PASS|FAIL): (TestAudit\S+) \(([^)]+)\)',text,re.M)
    result={'exit':rc,'tests':[{'status':status,'name':name,'duration':duration} for status,name,duration in cases],'log_sha256':hashlib.sha256((out/'run.log').read_bytes()).hexdigest(),'fixture_files':[{'path':f.name,'bytes':f.stat().st_size,'sha256':hashlib.sha256(f.read_bytes()).hexdigest()} for f in sorted(fixtures.iterdir()) if f.is_file()]}
    (out/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'exit':rc,'cases':cases,'log_sha256':result['log_sha256']}))
    raise SystemExit(rc)
