#!/usr/bin/env python3
"""Coordinator-only metadata freeze after reviewed actual server build; no service run."""
import argparse,datetime,hashlib,json,shlex,sys
from pathlib import Path
ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE=Path(__file__).resolve().parent;D=HERE.parent
BASE='5f829f8d6dba7572e48b8bf3d261c99e8f2447da'
P=argparse.ArgumentParser(description=__doc__)
P.add_argument('--expected-server-result-sha',required=True)
args=P.parse_args()
def row(path):
    p=Path(path);raw=p.read_bytes()
    return {'path':str(p),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}
E=ROOT/'evidence/market-display-body-server01';result_path=E/'result.json'
assert row(result_path)['sha256']==args.expected_server_result_sha
result=json.loads(result_path.read_text());assert result['status']=='PASS' and result['base']==BASE
assert result['package']=='./cmd/server'
before=json.loads((E/'inputs-before.json').read_text());after=json.loads((E/'inputs-after.json').read_text())
assert before==after
paths={Path(p) for p in before}
for path,expected in before.items():
    current=row(path);assert current['bytes']==expected['bytes'] and current['sha256']==expected['sha256'],path
program=ROOT/'runtime/market-display-body-server01'
assert result['program_path']==str(program) and row(program)['sha256']==result['program_sha256']
clean=D/'gates01/clean-view/head-proof.json';candidate=D/'candidate01/manifest.json'
assert json.loads(clean.read_text())['actual_head']==BASE
paths.update({program,result_path,E/'inputs-before.json',E/'inputs-after.json',E/'virtual-compile-inputs.json',
              E/'generated-source-map.json',HERE/'normal-cli-driver.py',HERE/'cli-preparation-seal.json',Path(__file__),
              clean,candidate,D/'app01/manifest.json',D/'app01/overlay/internal/app/testdata/market-display-body-source.json',
              ROOT/'evidence/market-display-body-source01/stdout.json',
              ROOT/'audits/market-display-body-independent/app-final01/seal.json',
              ROOT/'audits/market-display-body-independent/source-final01/seal.json',
              ROOT/'audits/market-display-body-independent/vet-final01/seal.json',
              ROOT.parent/'plugins-cloud-20261028/toolchain/go/src/net/http/internal/testcert/testcert.go',Path(sys.executable).resolve()})
binding={'status':'COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION','at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
         'base':BASE,'server_sha256':row(program)['sha256'],'server_path':str(program),
         'driver_sha256':row(HERE/'normal-cli-driver.py')['sha256'],
         'candidate_manifest_sha256':row(candidate)['sha256'],'clean_view_proof':row(clean),
         'inputs':[row(p) for p in sorted(paths)],
         'worker_not_required':True,'fixed_owned_only':True,'normal_hosts':2,'upstream_requests':15,
         'not_claimed':['original getter/network execution','real/public repository','new worker/UI execution','full parent acceptance','remote delivery']}
binding_path=HERE/'execution-bindings.json'
with binding_path.open('x') as stream:stream.write(json.dumps(binding,indent=2)+'\n')
argv=['python3',str(HERE/'normal-cli-driver.py'),'--server',str(program),'--output',str(ROOT/'test-instances/market-display-body-cli01'),
      '--expected-server-sha',binding['server_sha256'],'--expected-driver-sha',binding['driver_sha256'],'--expected-bindings-sha',row(binding_path)['sha256']]
with (HERE/'execution-command.json').open('x') as stream:stream.write(json.dumps({'status':'PREPARED_NOT_EXECUTED','argv':argv,'grant_marker':'MARKET_DISPLAY_BODY_CLI_GRANTED=1'},indent=2)+'\n')
with (HERE/'run-cli.sh').open('x') as stream:
    stream.write('#!/usr/bin/env bash\nset -euo pipefail\n[[ "$#" == 0 ]]\nexport MARKET_DISPLAY_BODY_CLI_GRANTED=1\nexec '+shlex.join(argv)+'\n')
print(json.dumps({'binding':row(binding_path),'command':row(HERE/'execution-command.json'),'run_cli':row(HERE/'run-cli.sh'),'program':row(program)}),flush=True)
