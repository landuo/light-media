"""Static/readback only: existing build programs, manifests and prepared command."""
from pathlib import Path
import json,hashlib,shlex,collections
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');A=R/'release-light-media';C=R/'target-drafts/s13-tr-numeric01/cli02';O=R/'audits/s13-tr-numeric-independent/cli-preexec02'
def load(p):return json.loads(p.read_bytes())
def rec(p):b=p.read_bytes();return dict(path=str(p),bytes=len(b),sha256=hashlib.sha256(b).hexdigest())
def check(p,row):
 r=rec(p);assert r['bytes']==row['bytes'] and r['sha256']==row['sha256'],str(p);return r
mapping=load(R/'target-drafts/s13-tr-numeric01/native02/gates02/overlay.json')['Replace'];reports=[]
for family,package in [('fixture','./cmd/owned-tr-final-fixture'),('server','./cmd/server')]:
 label='s13-tr-numeric-'+family+'02';E=R/'evidence'/label;r=load(E/'result.json')
 assert r['status']=='PASS' and r['base']=='808148abfad0b7fe230c426387a3b7b49eeedb18' and r['package']==package and r['overlay']==mapping and r['input_drift']==[]
 before=load(E/'inputs-before.json');assert before==load(E/'inputs-after.json') and len(before)==r['input_count']
 for p,v in before.items():check(Path(p),v)
 generated=load(E/'generated-source-map.json')
 for p,v in generated.items():check(E/v,before[p])
 prog=Path(r['program_path']);assert prog==R/'runtime'/label and rec(prog)['sha256']==r['program_sha256']
 for phase in ['go-list','build']:
  assert (E/(phase+'.exit')).read_text().strip()=='0' and not (E/(phase+'.stderr')).read_bytes()
 assert len(r['commands'])==2 and all(c['exit']==0 for c in r['commands']) and r['commands'][1]['argv'][:2]==['go','build'] and '-race' not in r['commands'][1]['argv']
 raw=(E/'go-list.stdout').read_text();dec=json.JSONDecoder();off=0;packages=[]
 while off<len(raw):
  while off<len(raw) and raw[off].isspace():off+=1
  if off==len(raw):break
  x,off=dec.raw_decode(raw,off);packages.append(x)
 declarations=[]
 for x in packages:
  if not x.get('Dir'):continue
  for kind in ('GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles'):
   for n in x.get(kind,[]):
    virtual=str((Path(x['Dir'])/n).resolve());backing=mapping.get(virtual,virtual);assert backing and backing in before
    declarations.append(dict(package=x.get('ImportPath'),kind=kind,virtual_path=virtual,backing_path=backing))
 assert declarations==load(E/'virtual-compile-inputs.json')
 if family=='fixture':required=['cmd/owned-tr-final-fixture/main.go']
 else:required=['internal/plugins/downloadercommands/tr_numeric.go','internal/plugins/downloadercommands/tr_final.go','internal/app/plugin_shared_transmission_final.go','internal/plugins/iyuuautoseed/native/shared_transmission_final.go','internal/plugins/shared_transmission_final.go','cmd/server/main.go']
 for rel in required:assert any(d['virtual_path']==str(A/rel) for d in declarations),rel
 for name,prior in r['prior_gates'].items():
  suffix='01' if name=='fixture-normal' else '02';p=R/'evidence'/('s13-tr-numeric-'+name+suffix)/'result.json';assert rec(p)['sha256']==prior['result_sha256']
 reports.append(dict(label=label,result=rec(E/'result.json'),program=rec(prog),started=r['started'],ended=r['ended'],input_count=len(before),both_maps_and_current_equal=True,go_list_packages=len(packages),compile_declarations=len(declarations),generated_copies=len(generated),prior_gates=r['prior_gates'],files=[rec(E/n) for n in ['inputs-before.json','inputs-after.json','virtual-compile-inputs.json','generated-source-map.json','go-list.stdout','go-list.exit','go-list.stderr','build.stdout','build.exit','build.stderr']]))
assert reports[0]['ended']<=reports[1]['started']
binding=load(C/'execution-bindings.json');assert binding['status']=='COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION' and binding['base']=='808148abfad0b7fe230c426387a3b7b49eeedb18'
assert binding['driver_sha256']==rec(C/'normal-cli-driver.py')['sha256']=='8a849c5d7698e29ff3b6c325052f012a8f239092c08a04c1df33ea4e32408b14'
assert binding['candidate_sha256']==rec(R/'target-drafts/s13-tr-numeric01/native02/preparation-seal.json')['sha256']
assert binding['server_sha256']==reports[1]['program']['sha256'] and binding['worker_sha256']==reports[0]['program']['sha256']
by_path={x['path']:x for x in binding['inputs']};assert len(by_path)==len(binding['inputs'])
for p,v in by_path.items():check(Path(p),v)
for r in reports:
 E=Path(r['result']['path']).parent
 for p,v in load(E/'inputs-before.json').items():assert all(by_path[p][k]==v[k] for k in ('bytes','sha256'))
 assert r['program']['path'] in by_path and r['result']['path'] in by_path
cmd=load(C/'execution-command.json');assert cmd['status']=='PREPARED_NOT_EXECUTED' and cmd['grant_marker']=='S13_TR_NUMERIC_CLI_GRANTED=1'
want=['python3',str(C/'normal-cli-driver.py'),'--server',reports[1]['program']['path'],'--worker',reports[0]['program']['path'],'--output',str(R/'test-instances/s13-tr-numeric-normal02'),'--expected-server-sha',binding['server_sha256'],'--expected-worker-sha',binding['worker_sha256'],'--expected-driver-sha',binding['driver_sha256'],'--expected-bindings-sha',rec(C/'execution-bindings.json')['sha256']]
assert cmd['argv']==want
shell='#!/usr/bin/env bash\nset -euo pipefail\n[[ "$#" == 0 ]]\nexport S13_TR_NUMERIC_CLI_GRANTED=1\nexec '+shlex.join(want)+'\n';assert (C/'run-cli.sh').read_text()==shell
assert not (R/'test-instances/s13-tr-numeric-normal02').exists()
out=dict(status='READY_EXACT_CLI02_ACTUAL_PROGRAMS',reports=reports,binding=rec(C/'execution-bindings.json'),binding_inputs=len(by_path),command=rec(C/'execution-command.json'),runner=rec(C/'run-cli.sh'),driver=rec(C/'normal-cli-driver.py'),preparation=rec(R/'audits/s13-tr-numeric-independent/cli-preparation02/preparation-seal.json'),reviewer_execution='read/hash only; no programs run',actual_CLI_run='not yet')
(O/'readback.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({'status':out['status'],'inputs':out['binding_inputs'],'programs':[x['program'] for x in reports]}))
