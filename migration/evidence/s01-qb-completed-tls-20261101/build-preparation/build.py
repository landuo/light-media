"""Coordinator-only two normal program builds; no tests/services/HTTP/source imports."""
import datetime,hashlib,json,os,shutil,subprocess
from pathlib import Path
B=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');R=B/'release-light-media'
G=Path(__file__).resolve().parent;E=B/'evidence/s01-qb-query-tls-programs01'
BASE='bd4ec4513e6ff9edc6eb4e0a357febf7d4ec30e0'
PROGRAMS=[('worker','./cmd/owned-qbittorrent-completed-fixture',B/'runtime/s01-qb-query-tls-worker01'),('server','./cmd/server',B/'runtime/s01-qb-query-tls-server01')]
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def freeze(paths):return {str(p):{'sha256':sha(p),'bytes':p.stat().st_size} for p in sorted(paths)}
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()==BASE
assert not E.exists() and all(not x[2].exists() for x in PROGRAMS)
E.mkdir(mode=0o700,exist_ok=False);ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
record={'status':'RUNNING','started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'base':BASE,'kind':'compile-only normal worker and server; no program executed','commands':[],'programs':{},'proxy_environment_unchanged':True,'tests_executed':False,'vet':'NOT_RUN (go build only)'}
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
def run(label,argv):
 sample={'phase':label,'scratch_free':shutil.disk_usage(B).free,'tmp_free':shutil.disk_usage('/tmp').free};record.setdefault('resource_samples',[]).append(sample)
 assert min(sample['scratch_free'],sample['tmp_free'])>=1<<30,'actual free-space reserve reached'
 record['commands'].append({'label':label,'argv':argv});save()
 with (E/(label+'.stdout')).open('xb') as out,(E/(label+'.stderr')).open('xb') as err:
  x=subprocess.run(argv,cwd=R,env=ENV,stdout=out,stderr=err)
 record['commands'][-1]['exit']=x.returncode;(E/(label+'.exit')).write_text(str(x.returncode)+'\n');save()
 assert x.returncode==0,label+' failed'
 return (E/(label+'.stdout')).read_text()
try:
 binding=json.loads((G/'binding.json').read_text());overlay=json.loads((G/'overlay.json').read_text())['Replace']
 assert binding['base']==BASE and set(overlay)=={x['virtual_path'] for x in binding['replacements']}
 rows=binding['inputs']
 for x in rows:
  assert Path(x['path']).is_file() and Path(x['path']).stat().st_size==x['bytes'] and sha(x['path'])==x['sha256'],'input drift '+x['path']
 for x in binding['replacements']:
  assert Path(x['virtual_path']).is_file()==x['virtual_present_before']
  assert overlay[x['virtual_path']]==(x['backing']['path'] if x['backing'] else '')
  if x['backing'] is None:assert x['removed_preimage']['path']==x['virtual_path']
 flags=['-overlay='+str(G/'overlay.json'),'-mod=readonly','-buildvcs=false']
 raw=run('go-list',['go','list',*flags,'-deps','-json']+[x[1] for x in PROGRAMS]);dec=json.JSONDecoder();offset=0;declarations=[]
 paths={Path(x['path']) for x in rows}|{Path(__file__).resolve(),G/'binding.json',G/'overlay.json',R/'go.mod',R/'go.sum'}
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  package,offset=dec.raw_decode(raw,offset)
  if not package.get('Dir'):continue
  for kind in ('GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles'):
   for name in package.get(kind,[]):
    virtual=str((Path(package['Dir'])/name).resolve());backing=overlay.get(virtual,virtual)
    assert backing,'hidden source was declared '+virtual
    p=Path(backing);assert p.is_file(),'missing declaration '+virtual
    paths.add(p);declarations.append({'package':package['ImportPath'],'kind':kind,'virtual_path':virtual,'backing_path':backing})
 (E/'virtual-compile-inputs.json').write_text(json.dumps(declarations,indent=2)+'\n')
 before=freeze(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before)
 generated={};cache=Path(ENV['GOCACHE']).resolve()
 for p in paths:
  if p.is_relative_to(cache):
   dest=E/'generated-source'/(before[str(p)]['sha256']+'.source');dest.parent.mkdir(exist_ok=True);dest.write_bytes(p.read_bytes());generated[str(p)]=str(dest.relative_to(E))
 (E/'generated-source-map.json').write_text(json.dumps(generated,indent=2)+'\n');save()
 for label,pkg,program in PROGRAMS:
  run('build-'+label,['go','build',*flags,'-p=1','-o',str(program),pkg])
  after=freeze(paths);(E/('inputs-after-'+label+'.json')).write_text(json.dumps(after,indent=2)+'\n');assert after==before,'compile input drift after '+label
  record['programs'][label]={'path':str(program),'sha256':sha(program),'bytes':program.stat().st_size,'package':pkg};save()
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()==BASE
 record.update(status='PASS',input_drift=[])
except BaseException as exc:
 record.update(status='FAIL',error_class=type(exc).__name__,error=str(exc));raise
finally:
 record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print(json.dumps(record),flush=True)
