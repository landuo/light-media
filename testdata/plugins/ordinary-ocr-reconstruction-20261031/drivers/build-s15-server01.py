import datetime,hashlib,json,os,subprocess
from pathlib import Path
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');P=R/'light-media';E=R/'evidence/s15-server01';E.mkdir(exist_ok=False);BIN=R/'runtime/s15-server01';assert not BIN.exists();ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
record={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'FAIL','kind':'newnormalserver compile only; uses unchanged restoredUI assets, notnewbrowser acceptance','commands':[]}
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def run(label,cmd):
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
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();(E/'result.json').write_text(json.dumps(record,indent=2)+'\n');print(json.dumps(record))
