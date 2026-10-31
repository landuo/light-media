import datetime,hashlib,json,os,subprocess,shutil
from pathlib import Path
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');P=R/'light-media';E=R/'evidence/s10-server01';E.mkdir(exist_ok=False);BIN=R/'runtime/s10-server01';assert not BIN.exists();ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
record={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'RUNNING','kind':'newnormalserver compile only; uses unchanged restoredUI assets, notnewbrowser acceptance','commands':[]}
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def run(label,cmd):
 sample={'phase':label,'scratch_free':shutil.disk_usage(R).free,'tmp_free':shutil.disk_usage('/tmp').free};record.setdefault('resource_samples',[]).append(sample);assert min(sample['scratch_free'],sample['tmp_free'])>=1<<30,'actual free-space floor reached'
 record['commands'].append({'label':label,'argv':cmd})
 with (E/(label+'.stdout')).open('wb') as o,(E/(label+'.stderr')).open('wb') as e:result=subprocess.run(cmd,cwd=P,env=ENV,stdout=o,stderr=e)
 record['commands'][-1]['exit']=result.returncode;(E/(label+'.exit')).write_text(str(result.returncode)+'\n');assert result.returncode==0,label
 return (E/(label+'.stdout')).read_text()
def snapshot(paths):return {str(x):{'sha256':sha(x),'bytes':x.stat().st_size} for x in sorted(paths)}
try:
 raw=run('go-list',['go','list','-mod=readonly','-buildvcs=false','-deps','-json','./cmd/server']);decoder=json.JSONDecoder();offset=0;paths={P/'go.mod',P/'go.sum',Path(__file__).resolve()}
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  pkg,offset=decoder.raw_decode(raw,offset)
  if not pkg.get('Dir'):continue
  for key in ('GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SysoFiles','EmbedFiles'):
   for f in pkg.get(key,[]):
    path=Path(pkg['Dir'])/f
    if path.is_file():paths.add(path.resolve())
 before=snapshot(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before)
 run('build',['go','build','-mod=readonly','-buildvcs=false','-p=1','-o',str(BIN),'./cmd/server'])
 after=snapshot(paths);(E/'inputs-after.json').write_text(json.dumps(after,indent=2)+'\n');assert before==after,'input drift'
 record['program_sha256']=sha(BIN);record['status']='PASS'
except BaseException as err:
 record.update(status='FAIL',error_class=type(err).__name__,error=str(err));raise
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();(E/'result.json').write_text(json.dumps(record,indent=2)+'\n');print(json.dumps(record))
