#!/usr/bin/env python3
"""Serial targeted Go gate: exact compile-input snapshot, one reusable test program."""
import argparse,datetime,hashlib,json,os,subprocess,shutil
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--root',default=str(Path(__file__).resolve().parent));p.add_argument('--label',required=True);p.add_argument('--package',required=True);p.add_argument('--run',required=True);p.add_argument('--race',action='store_true');p.add_argument('--fixture');p.add_argument('--fixture-env');a=p.parse_args()
ROOT=Path(a.root).resolve();REPO=ROOT/'release-light-media';E=ROOT/'evidence'/a.label;E.mkdir(exist_ok=False,mode=0o700)
BIN=ROOT/'runtime'/(a.label+'.test')
ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
for key,name in [('OWNED_TORRENT_MATERIAL_WORKER','s12-release-fixture01.worker'),('OWNED_TORRENT_MATERIAL_DENIED_WORKER','s12-release-fixture01.denied.worker')]:ENV[key]=str(ROOT/'runtime'/name)
assert not BIN.exists();assert bool(a.fixture)==bool(a.fixture_env)
FIXTURE=ROOT/'runtime'/(a.label+'.worker')
if a.fixture:assert not FIXTURE.exists()
record={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'driver_pid':os.getpid(),'package':a.package,'pattern':a.run,'race':a.race,'status':'RUNNING','commands':[],'environment_proxy_unchanged':True}
def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
def run(label,cmd):
 resource={'phase':label,'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scratch_free':shutil.disk_usage(ROOT).free,'tmp_free':shutil.disk_usage('/tmp').free};record.setdefault('resource_samples',[]).append(resource)
 assert resource['scratch_free']>=1<<30 and resource['tmp_free']>=1<<30,'actual free-space floor reached'
 record['commands'].append({'label':label,'argv':cmd});save()
 with (E/(label+'.stdout')).open('wb') as out,(E/(label+'.stderr')).open('wb') as err:
  x=subprocess.run(cmd,cwd=REPO/a.package.removeprefix('./') if label=='tests' else REPO,env=ENV,stdout=out,stderr=err)
 (E/(label+'.exit')).write_text(str(x.returncode)+'\n')
 record['commands'][-1]['exit']=x.returncode;save()
 if x.returncode:raise RuntimeError(label+' exited '+str(x.returncode))
 return (E/(label+'.stdout')).read_bytes()
def freeze(paths):return {str(path):{'sha256':sha(path),'bytes':path.stat().st_size} for path in sorted(paths)}
try:
 raw=run('go-list',['go','list','-mod=readonly','-buildvcs=false','-deps','-test','-json']+(['-race'] if a.race else [])+[a.package]+([a.fixture] if a.fixture else [])).decode()
 decoder=json.JSONDecoder();offset=0;packages=[]
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  value,offset=decoder.raw_decode(raw,offset);packages.append(value)
 paths={REPO/'go.mod',REPO/'go.sum',Path(__file__).resolve(),ROOT/'runtime/s12-release-fixture01.worker',ROOT/'runtime/s12-release-fixture01.denied.worker'}
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
 if a.fixture:
  run('compile-worker',['go','build','-mod=readonly','-buildvcs=false','-p=1','-o',str(FIXTURE)]+(['-race'] if a.race else [])+[a.fixture])
  record['fixture_sha256']=sha(FIXTURE);record['fixture_env']=a.fixture_env;ENV[a.fixture_env]=str(FIXTURE);save()
 command=['go','test','-mod=readonly','-buildvcs=false','-p=1','-c','-o',str(BIN)]
 if a.race:command+=['-race']
 command+=[a.package];run('compile',command);record['program_sha256']=sha(BIN);save()
 compiled=freeze(paths);assert compiled==before,'compile inputs drifted';(E/'inputs-after-compile.json').write_text(json.dumps(compiled,indent=2)+'\n')
 run('tests',['go','tool','test2json','-t','-p',a.package,str(BIN),'-test.v','-test.run',a.run,'-test.count=1','-test.timeout=90s'])
 after=freeze(paths);assert not a.fixture or sha(FIXTURE)==record['fixture_sha256'];assert after==before,'test inputs drifted';assert sha(BIN)==record['program_sha256'],'program drifted'
 (E/'inputs-after-test.json').write_text(json.dumps(after,indent=2)+'\n')
 events=[json.loads(x) for x in (E/'tests.stdout').read_text().splitlines() if x.startswith('{')]
 record['root_passes']=[x['Test'] for x in events if x.get('Action')=='pass' and 'Test' in x and '/' not in x['Test']]
 assert sorted(record['root_passes'])==['TestSharedTorrentMaterialCacheColdAndFailureHistory', 'TestSharedTorrentMaterialCurrentGuardAndCancellation'],'wrong exact S12 actual App roots'
 assert not [x for x in events if x.get('Action') in ('skip','fail')],'unexpected fail/skip'
 record['status']='PASS'
except BaseException as err:
 record.update(status='FAIL',error_class=type(err).__name__,error=str(err));raise
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
 print(json.dumps({k:v for k,v in record.items() if k!='commands'}),flush=True)
