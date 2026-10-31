"""Finite b4+S01 candidate gates. Never invokes a daemon or source runtime."""
from pathlib import Path
import datetime, hashlib, json, os, re, shutil, subprocess, sys
E=Path(__file__).resolve().parent
MODE='normal'
PLAN=json.loads((E/'normal-resume-plan.json').read_text());assert PLAN['state']=='frozen'
REPO=Path(PLAN['worktree']);RUN=E/PLAN['run_directory'];RUN.mkdir(exist_ok=False)
ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',GOFLAGS='-mod=readonly -buildvcs=false -p=1',GOPROXY='off',GOSUMDB='off',CGO_ENABLED='1',GOTMPDIR=PLAN['gotmpdir'],TZ='UTC',LANG='C.UTF-8')
if MODE=='race':ENV['GORACE']='halt_on_error=1 exitcode=66'
Path(ENV['GOTMPDIR']).mkdir(exist_ok=False)
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(path):
 h=hashlib.sha256()
 with Path(path).open('rb') as f:
  for b in iter(lambda:f.read(1<<20),b''):h.update(b)
 return h.hexdigest()
def write(name,obj):(RUN/name).write_text(json.dumps(obj,indent=2)+'\n')
def guard():
 if shutil.disk_usage(E).free<1<<30 or shutil.disk_usage('/tmp').free<3*(1<<29):raise RuntimeError('real reserved free space reached')
def command(name,argv,cwd=REPO):
 guard();write(name+'.command.json',{'argv':argv,'cwd':str(cwd),'at':now()})
 with (RUN/(name+'.log')).open('xb') as out:r=subprocess.run(argv,cwd=cwd,env=ENV,stdout=out,stderr=subprocess.STDOUT)
 (RUN/(name+'.exit')).write_text(str(r.returncode)+'\n');print(name,'exit',r.returncode,flush=True)
 if r.returncode:raise subprocess.CalledProcessError(r.returncode,argv)
def bind(phase):
 command(phase+'-list',['go','list',*(['-race'] if MODE=='race' else []),'-deps','-test','-json',*PLAN['source_packages']])
 stream=(RUN/(phase+'-list.log')).read_text();decoder=json.JSONDecoder();paths={REPO/'go.mod',REPO/'go.sum',E/'normal-resume-plan.json',E/'run-resume.py',E/'monitor-resume.py',E/'launch-resume.sh',Path(PLAN['candidate_manifest'])};count=0
 while stream.strip():
  stream=stream.lstrip();pkg,n=decoder.raw_decode(stream);stream=stream[n:];count+=1
  if pkg.get('Error') or pkg.get('DepsErrors'):raise RuntimeError('Go list declared package errors')
  for key in ['GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles','TestGoFiles','XTestGoFiles','TestEmbedFiles','XTestEmbedFiles']:
   for filename in pkg.get(key,[]):
    path=(Path(pkg['Dir'])/filename).resolve()
    if not path.is_file():raise RuntimeError('missing declared input '+str(path))
    paths.add(path)
  for module in (pkg.get('Module',{}),pkg.get('Module',{}).get('Replace',{})):
   if module.get('GoMod'):paths.add(Path(module['GoMod']).resolve())
 paths.update(Path(n).resolve() if Path(n).is_absolute() else REPO/n for n in PLAN['runtime_inputs'])
 td=Path(subprocess.check_output(['go','env','GOTOOLDIR'],env=ENV,text=True).strip());paths.add(Path(shutil.which('go',path=ENV['PATH'])).resolve());paths.update(td/n for n in ['compile','asm','link','cgo'])
 files={str(p):sha(p) for p in sorted(paths)};generated={}
 for p,digest in files.items():
  if '/cache/go-build/' in p:
   dest=RUN/'generated-inputs'/digest;dest.parent.mkdir(exist_ok=True)
   if not dest.exists():shutil.copyfile(p,dest)
   assert sha(dest)==digest;generated[p]={'sha256':digest,'copy':str(dest),'bytes':Path(p).stat().st_size}
 write(phase+'-generated-inputs.json',generated)
 result={'at':now(),'files':files,'package_count':count,'go_env':json.loads(subprocess.check_output(['go','env','-json','GOOS','GOARCH','GOVERSION','CGO_ENABLED','GOFLAGS'],env=ENV)),'boundary':'Actual Go-listed compile/test/embed closure plus pinned finite runtime source/oracle/runner/tools; not full OS/CGO closure.'};write(phase+'-inputs.json',result);return result
before=run_before=after=None;binaries={};exitcode=1
try:
 assert Path(shutil.which('go',path=ENV['PATH'])).resolve()==Path(PLAN['expected_go']).resolve(),'source pinned existing env.sh first'
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=REPO,text=True).strip()==PLAN['base_commit'],'candidate base changed'
 for filename,digest in PLAN['frozen_runner_inputs'].items():assert sha(filename)==digest,'runner drift '+filename
 candidate=json.loads(Path(PLAN['candidate_manifest']).read_text())
 for filename,digest in candidate['target_files'].items():assert sha(REPO/filename)==digest,'candidate drift '+filename
 write('candidate-readback.json',{'target_files':candidate['target_files'],'base_commit':PLAN['base_commit']})
 write('environment-binding.json',{'inherits_environment':True,'proxy_and_TLS_unchanged':True,'all_values_sha256':{k:hashlib.sha256(v.encode()).hexdigest() for k,v in sorted(ENV.items())},'overrides':{k:ENV[k] for k in ['GOMAXPROCS','GOFLAGS','GOPROXY','GOSUMDB','CGO_ENABLED','GOTMPDIR','TZ','LANG']}})
 # Reuse only exact successful original programs. Before any new Go-list,
 # normal01 original source closure was separately rehashed in saved proof;
 # repeat that check here against the actual current files as execution input.
 old_before=json.loads(Path(PLAN['original_before_inputs']).read_text())
 for p,digest in old_before['files'].items():
  assert Path(p).is_file() and sha(p)==digest,'original completed compilation input drift '+p
 proof=json.loads(Path(PLAN['reuse_proof']).read_text())
 for name,row in proof['binaries'].items():
  path=Path(PLAN['reuse_binaries'][name]);assert str(path)==row['path']
  assert sha(path)==row['sha256'] and path.stat().st_size==row['bytes'],'reused binary drift '+name
  assert Path(row['build_exit_file']).read_text().strip()=='0','original build not complete '+name
  binaries[name]={'sha256':row['sha256'],'bytes':row['bytes'],'path':str(path),'reused_completed_compile':True}
 for f in PLAN['fixtures']:ENV[f['env']]=PLAN['reuse_binaries'][f['binary']]
 write('actual-reuse-readback.json',{'original_input_count':len(old_before['files']),'input_drift':[],'all7build_exit0':True,'binary_sha_bytes_equal':True,'program_paths_reused_without_copy':True,'new_compile_count':0})
 before=bind('before')
 write('binary-hashes.json',binaries);write('fixture-environment.json',{f['env']:ENV[f['env']] for f in PLAN['fixtures']})
 run_before=bind('run-before');assert before['files']==run_before['files'],'input drift before tests'
 roots=[]
 for p in PLAN['test_packages']:
  name='test-'+p['label'];command(name,[PLAN['reuse_binaries'][p['binary']],'-test.run','^('+'|'.join(p['roots'])+')$','-test.v','-test.count=1','-test.timeout=300s'],REPO/p['package'].removeprefix('./'))
  raw=(RUN/(name+'.log')).read_text();actual=re.findall(r'^--- (PASS|FAIL|SKIP): ([^ /]+) ',raw,re.M)
  assert sorted(n for s,n in actual)==sorted(p['roots']) and all(s=='PASS' for s,n in actual),'exact roots mismatch'
  assert not re.search(r'^\s*--- SKIP:',raw,re.M) and 'WARNING: DATA RACE' not in raw,'skip/race report'
  roots.extend({'package':p['package'],'name':n,'status':s} for s,n in actual)
 write('actual-roots.json',roots);assert len(roots)==PLAN['expected_roots']
 after=bind('after');assert before['files']==run_before['files']==after['files'],'input drift after tests';exitcode=0
except Exception as exc:
 write('failure.json',{'type':type(exc).__name__,'message':str(exc)});print(type(exc).__name__,str(exc),flush=True)
finally:
 frozen=run_before or before;drift=[]
 if frozen:drift=[p for p,h in frozen['files'].items() if not Path(p).is_file() or sha(p)!=h]
 binary_drift=[n for n,v in binaries.items() if not Path(v['path']).is_file() or sha(v['path'])!=v['sha256']]
 if drift or binary_drift:exitcode=1
 result={'at':now(),'exit':exitcode,'mode':MODE,'input_count':len(frozen['files']) if frozen else 0,'before_run_before_after_equal':bool(before and run_before and after and before['files']==run_before['files']==after['files']),'terminal_drift':drift,'binary_drift':binary_drift,'scope':PLAN['scope']};write('result.json',result);(RUN/'runner.exit').write_text(str(exitcode)+'\n');print(json.dumps(result),flush=True)
sys.exit(exitcode)
