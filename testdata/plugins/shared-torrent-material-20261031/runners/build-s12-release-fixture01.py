import datetime,hashlib,json,os,subprocess,shutil
from pathlib import Path
R=Path(__file__).resolve().parents[1];P=R/'release-light-media';E=R/'evidence/s12-release-fixture01';E.mkdir(exist_ok=False);BIN=R/'runtime/s12-release-fixture01.worker';DENIED=R/'runtime/s12-release-fixture01.denied.worker';assert not BIN.exists() and not DENIED.exists();ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1');record={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'RUNNING','kind':'normal+denied ownedfixture compile and --manifest only; notactualHostcall','driver_pid':os.getpid(),'commands':[]}
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
def run(label,cmd):
 sample={'phase':label,'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scratch_free':shutil.disk_usage(R).free,'tmp_free':shutil.disk_usage('/tmp').free};record.setdefault('resource_samples',[]).append(sample);assert min(sample['scratch_free'],sample['tmp_free'])>=1<<30
 record['commands'].append({'label':label,'argv':cmd});save()
 with (E/(label+'.stdout')).open('wb') as o,(E/(label+'.stderr')).open('wb') as e:x=subprocess.run(cmd,cwd=P,env=ENV,stdout=o,stderr=e)
 record['commands'][-1]['exit']=x.returncode;(E/(label+'.exit')).write_text(str(x.returncode)+'\n');save();assert x.returncode==0,label
 return (E/(label+'.stdout')).read_text()
def snap(paths):return {str(x):{'sha256':sha(x),'bytes':x.stat().st_size} for x in sorted(paths)}
try:
 package='./cmd/owned-torrent-material-fixture';raw=run('go-list',['go','list','-mod=readonly','-buildvcs=false','-deps','-json',package]);decoder=json.JSONDecoder();offset=0;paths={P/'go.mod',P/'go.sum',Path(__file__).resolve()}
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  pkg,offset=decoder.raw_decode(raw,offset)
  if not pkg.get('Dir'):continue
  for key in ('GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SysoFiles','EmbedFiles'):
   for f in pkg.get(key,[]):
    p=Path(pkg['Dir'])/f
    if p.is_file():paths.add(p.resolve())
 before=snap(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before);generated={};cache=Path(ENV['GOCACHE']).resolve()
 for p in paths:
  if p.is_relative_to(cache):
   q=E/'generated-source'/(before[str(p)]['sha256']+'.go');q.parent.mkdir(exist_ok=True);q.write_bytes(p.read_bytes());generated[str(p)]=str(q.relative_to(E))
 (E/'generated-source-map.json').write_text(json.dumps(generated,indent=2)+'\n')
 for label,program,flags in [('normal',BIN,[]),('denied',DENIED,['-ldflags','-X main.fixtureDenied=true'])]:
  run('build-'+label,['go','build','-mod=readonly','-buildvcs=false','-p=1','-o',str(program)]+flags+[package]);record[label+'_program_sha256']=sha(program);assert snap(paths)==before
  manifest=json.loads(run('manifest-'+label,[str(program),'--manifest']));assert manifest['plugin_id']==('OwnedTorrentMaterial' if label=='normal' else 'OwnedTorrentMaterialDenied');assert manifest['capabilities']==(['chain.download.download_torrent'] if label=='normal' else []);record[label+'_manifest']=manifest;assert sha(program)==record[label+'_program_sha256']
 after=snap(paths);assert before==after;(E/'inputs-after.json').write_text(json.dumps(after,indent=2)+'\n');record['status']='PASS'
except BaseException as error:record.update(status='FAIL',error_class=type(error).__name__,error=str(error));raise
finally:record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print(json.dumps(record))
