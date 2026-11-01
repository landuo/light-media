"""One-package static vet only; no source, product or test execution."""
import datetime,hashlib,json,os,shutil,subprocess
from pathlib import Path
ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE=ROOT/'target-drafts/market-display-body01/vet01'
ACTIVE=ROOT/'release-light-media'
E=ROOT/'evidence/market-display-body-artifacts-vet01'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def snapshot(rows):
    result={}
    for r in rows:
        p=Path(r['path']);actual={'sha256':sha(p),'bytes':p.stat().st_size}
        assert actual=={k:r[k] for k in ('sha256','bytes')},'input drift '+str(p)
        result[str(p)]=actual
    return result
binding=json.loads((HERE/'bindings.json').read_text())
assert json.loads((ROOT/'evidence/market-display-body-artifacts-normal01/result.json').read_text())['status']=='PASS'
assert shutil.which('go')==binding['go_executable']
assert os.environ.get('GOPROXY')=='off' and os.environ.get('GOSUMDB')=='off' and os.environ.get('GOTOOLCHAIN')=='local'
before=snapshot(binding['inputs'])
assert shutil.disk_usage(ROOT).free>=1<<30 and shutil.disk_usage('/tmp').free>=1<<30
E.mkdir(mode=0o700)
record={'status':'RUNNING','kind':'static_go_vet_only','started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':binding['command'],'source_or_tests_run':False,'input_count':len(before),'binding_sha256':sha(HERE/'bindings.json'),'proxy_CA_environment_unchanged':True}
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
save();(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n')
try:
    with (E/'vet.stdout').open('wb') as out,(E/'vet.stderr').open('wb') as err:
        result=subprocess.run(binding['command'],cwd=ACTIVE,stdout=out,stderr=err)
    (E/'vet.exit').write_text(str(result.returncode)+'\n');record['exit']=result.returncode
    after=snapshot(binding['inputs']);assert after==before
    (E/'inputs-after.json').write_text(json.dumps(after,indent=2)+'\n')
    record['input_drift']=[];record['stdout_sha256']=sha(E/'vet.stdout');record['stderr_sha256']=sha(E/'vet.stderr')
    assert result.returncode==0,'go vet failed'
    record['status']='PASS'
except BaseException as e:
    record.update(status='FAIL',error_class=type(e).__name__,error=str(e));raise
finally:
    record['finished']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print(json.dumps(record))
