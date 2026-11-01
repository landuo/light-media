#!/usr/bin/env python3
"""Serial targeted Go gate: exact compile-input snapshot, one reusable test program."""
import argparse,datetime,hashlib,json,os,subprocess,shutil
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--root',default=str(Path(__file__).resolve().parent));p.add_argument('--label',required=True);p.add_argument('--package',required=True);p.add_argument('--run',required=True);p.add_argument('--race',action='store_true');p.add_argument('--fixture');p.add_argument('--fixture-env');p.add_argument('--expect',action='append',required=True);p.add_argument('--overlay');p.add_argument('--extra-input',action='append',default=[]);p.add_argument('--binding',required=True);a=p.parse_args()
ROOT=Path(a.root).resolve();REPO=ROOT/'release-light-media';E=ROOT/'evidence'/a.label;E.mkdir(exist_ok=False,mode=0o700)
BIN=ROOT/'runtime'/(a.label+'.test')
ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
OVERLAY={}
if a.overlay:
 OVERLAY={str(Path(k).resolve()):(str(Path(v).resolve()) if v else '') for k,v in json.loads(Path(a.overlay).read_text())['Replace'].items()}
GOFLAGS=['-overlay='+str(Path(a.overlay).resolve())] if a.overlay else []
assert not BIN.exists();assert bool(a.fixture)==bool(a.fixture_env)
FIXTURE=ROOT/'runtime'/(a.label+'.worker')
if a.fixture:assert not FIXTURE.exists()
record={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'driver_pid':os.getpid(),'package':a.package,'pattern':a.run,'race':a.race,'status':'RUNNING','vet':'NOT_RUN: virtual-only package directory; prior automatic vet chdir failure retained','commands':[],'environment_proxy_unchanged':True,'overlay':OVERLAY,'extra_inputs':a.extra_input}
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
 binding=json.loads(Path(a.binding).read_text())
 rows=[binding['active_original'],binding['source_oracle'],binding['source_original']]+binding.get('additional_bound_files',[])
 assert set(OVERLAY)=={r['virtual_path'] for r in binding['replacements']},'overlay keys changed'
 for r in binding['replacements']:
  assert Path(r['virtual_path']).exists()==r['virtual_present_before'],'virtual path presence drift'
  if r['backing'] is None:
   assert OVERLAY[r['virtual_path']]=='','hidden source overlay changed'
   assert r['removed_preimage']['path']==r['virtual_path'] and r['virtual_present_before'],'hidden source preimage mismatch'
   rows.append(r['removed_preimage'])
  else:
   assert OVERLAY[r['virtual_path']]==r['backing']['path'],'overlay backing changed'
   rows.append(r['backing'])
 for row in rows:
  path=Path(row['path']);assert path.stat().st_size==row['bytes'] and sha(path)==row['sha256'],'preflight byte drift '+str(path)
 record['preflight_verified_files']=len(rows);record['preflight_verified_replacements']=len(binding['replacements']);save()
 raw=run('go-list',['go','list',*GOFLAGS,'-mod=readonly','-buildvcs=false','-deps','-test','-json']+(['-race'] if a.race else [])+[a.package]+([a.fixture] if a.fixture else [])).decode()
 decoder=json.JSONDecoder();offset=0;packages=[]
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  value,offset=decoder.raw_decode(raw,offset);packages.append(value)
 paths={REPO/'go.mod',REPO/'go.sum',Path(__file__).resolve(),Path(a.binding).resolve()}
 paths.update(Path(row['path']).resolve() for row in rows)
 declarations=[]
 for package in packages:
  if not package.get('Dir'):continue
  directory=Path(package['Dir'])
  for key in ('GoFiles','CgoFiles','TestGoFiles','XTestGoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles','TestEmbedFiles','XTestEmbedFiles'):
   for value in package.get(key,[]):
    path=directory/value
    backing=OVERLAY.get(str(path.resolve()),str(path.resolve()))
    assert backing,'Go declared a source explicitly hidden by the immutable clean-base overlay: '+str(path)
    effective=Path(backing)
    assert effective.is_file(),'missing declared compile input '+str(path)+' -> '+str(effective)
    paths.add(effective)
    declarations.append({'package':package.get('ImportPath'),'kind':key,'virtual_path':str(path.resolve()),'backing_path':str(effective)})
 paths.update(Path(x).resolve() for x in a.extra_input)
 if a.overlay:paths.add(Path(a.overlay).resolve());paths.update(Path(x) for x in OVERLAY.values() if x)
 (E/'virtual-compile-inputs.json').write_text(json.dumps(declarations,indent=2)+'\n')
 before=freeze(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before)
 generated={};cache=Path(ENV['GOCACHE']).resolve()
 for path in paths:
  if path.is_relative_to(cache):
   dest=E/'generated-source'/(before[str(path)]['sha256']+'.go');dest.parent.mkdir(exist_ok=True);dest.write_bytes(path.read_bytes());generated[str(path)]=str(dest.relative_to(E))
 (E/'generated-source-map.json').write_text(json.dumps(generated,indent=2)+'\n');save()
 if a.fixture:
  run('compile-worker',['go','build',*GOFLAGS,'-mod=readonly','-buildvcs=false','-p=1','-o',str(FIXTURE)]+(['-race'] if a.race else [])+[a.fixture])
  record['fixture_sha256']=sha(FIXTURE);record['fixture_env']=a.fixture_env;ENV[a.fixture_env]=str(FIXTURE);save()
 command=['go','test',*GOFLAGS,'-vet=off','-mod=readonly','-buildvcs=false','-p=1','-c','-o',str(BIN)]
 if a.race:command+=['-race']
 command+=[a.package];run('compile',command);record['program_sha256']=sha(BIN);save()
 compiled=freeze(paths);assert compiled==before,'compile inputs drifted';(E/'inputs-after-compile.json').write_text(json.dumps(compiled,indent=2)+'\n')
 run('tests',['go','tool','test2json','-t','-p',a.package,str(BIN),'-test.v','-test.run',a.run,'-test.count=1','-test.timeout=120s'])
 after=freeze(paths);assert not a.fixture or sha(FIXTURE)==record['fixture_sha256'];assert after==before,'test inputs drifted';assert sha(BIN)==record['program_sha256'],'program drifted'
 (E/'inputs-after-test.json').write_text(json.dumps(after,indent=2)+'\n')
 events=[json.loads(x) for x in (E/'tests.stdout').read_text().splitlines() if x.startswith('{')]
 record['root_passes']=[x['Test'] for x in events if x.get('Action')=='pass' and 'Test' in x and '/' not in x['Test']]
 assert sorted(record['root_passes'])==sorted(a.expect),'wrong exact selected roots'
 assert not [x for x in events if x.get('Action') in ('skip','fail')],'unexpected fail/skip'
 record['status']='PASS'
except BaseException as err:
 record.update(status='FAIL',error_class=type(err).__name__,error=str(err));raise
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save()
 print(json.dumps({k:v for k,v in record.items() if k!='commands'}),flush=True)
