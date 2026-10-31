#!/usr/bin/env python3
"""Serial targeted Go gate: exact compile-input snapshot, one reusable test program."""
import argparse,datetime,hashlib,json,os,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--label',required=True);p.add_argument('--package',required=True);p.add_argument('--run',required=True);p.add_argument('--race',action='store_true');a=p.parse_args()
ROOT=Path(__file__).resolve().parent;REPO=ROOT/'light-media';E=ROOT/'evidence'/a.label;E.mkdir(exist_ok=False,mode=0o700)
BIN=ROOT/'runtime'/(a.label+'.test')
ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
assert not BIN.exists()
record={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'package':a.package,'pattern':a.run,'race':a.race,'status':'FAIL','commands':[],'environment_proxy_unchanged':True}
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
def run(label,cmd):
 record['commands'].append({'label':label,'argv':cmd});save()
 with (E/(label+'.stdout')).open('wb') as out,(E/(label+'.stderr')).open('wb') as err:
  x=subprocess.run(cmd,cwd=REPO,env=ENV,stdout=out,stderr=err)
 (E/(label+'.exit')).write_text(str(x.returncode)+'\n')
 record['commands'][-1]['exit']=x.returncode;save()
 if x.returncode:raise RuntimeError(label+' exited '+str(x.returncode))
 return (E/(label+'.stdout')).read_bytes()
def freeze(paths):return {str(path):{'sha256':sha(path),'bytes':path.stat().st_size} for path in sorted(paths)}
try:
 raw=run('go-list',['go','list','-mod=readonly','-buildvcs=false','-deps','-test','-json',a.package]).decode()
 decoder=json.JSONDecoder();offset=0;packages=[]
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  value,offset=decoder.raw_decode(raw,offset);packages.append(value)
 paths={REPO/'go.mod',REPO/'go.sum',Path(__file__).resolve()}
 for package in packages:
  if not package.get('Dir'):continue
  directory=Path(package['Dir'])
  for key in ('GoFiles','CgoFiles','TestGoFiles','XTestGoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles','TestEmbedFiles','XTestEmbedFiles'):
   for value in package.get(key,[]):
    path=directory/value
    if path.is_file():paths.add(path.resolve())
 before=freeze(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before)
 generated={};cache=Path(ENV['GOCACHE']).resolve()
 for path in paths:
  if path.is_relative_to(cache):
   dest=E/'generated-source'/(before[str(path)]['sha256']+'.go');dest.parent.mkdir(exist_ok=True);dest.write_bytes(path.read_bytes());generated[str(path)]=str(dest.relative_to(E))
 (E/'generated-source-map.json').write_text(json.dumps(generated,indent=2)+'\n');save()
 command=['go','test','-mod=readonly','-buildvcs=false','-p=1','-c','-o',str(BIN)]
 if a.race:command+=['-race']
 command+=[a.package];run('compile',command);record['program_sha256']=sha(BIN);save()
 compiled=freeze(paths);assert compiled==before,'compile inputs drifted';(E/'inputs-after-compile.json').write_text(json.dumps(compiled,indent=2)+'\n')
 run('tests',['go','tool','test2json','-t','-p',a.package,str(BIN),'-test.v','-test.run',a.run,'-test.count=1','-test.timeout=90s'])
 after=freeze(paths);assert after==before,'test inputs drifted';assert sha(BIN)==record['program_sha256'],'program drifted'
 (E/'inputs-after-test.json').write_text(json.dumps(after,indent=2)+'\n')
 events=[json.loads(x) for x in (E/'tests.stdout').read_text().splitlines() if x.startswith('{')]
 record['root_passes']=[x['Test'] for x in events if x.get('Action')=='pass' and 'Test' in x and '/' not in x['Test']]
 assert record['root_passes'],'zero executed test roots';record['status']='PASS'
except BaseException as err:
 record.update(error_class=type(err).__name__,error=str(err));raise
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
 print(json.dumps({k:v for k,v in record.items() if k!='commands'}),flush=True)
