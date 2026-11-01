import datetime,hashlib,json,os,subprocess,shutil
from pathlib import Path
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');P=R/'release-light-media';C=R/'target-drafts/s03-qb-cookie01/app01/clean-view01';E=R/'evidence/s03-qb-cookie-server01';BIN=R/'runtime/s03-qb-cookie-server01';W=R/'runtime/s03-qb-cookie-app01.worker'
def sha(p):return hashlib.sha256(Path(p).read_bytes()).hexdigest()
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=P,text=True).strip()=='60206809a9c46b9f11d8fb67590a889e24919df6'
assert sha(R/'target-drafts/s03-qb-cookie01/candidate07.json')=='891130b2e513f7fd74cf5be2bf8707aebe398f8559750c09e37f973bbc7eebbd'
assert sha(R/'evidence/s03-qb-cookie-app01/result.json')=='24b836328ae5aa9c5396b886b9fb82fb195f5c22fdaa6cf2881f60429f642e22'
app=json.loads((R/'evidence/s03-qb-cookie-app01/result.json').read_text());assert app['status']=='PASS' and sha(W)==app['fixture_sha256']
assert not BIN.exists();E.mkdir(exist_ok=False);ENV=os.environ.copy();ENV.update(GOMAXPROCS='2',CGO_ENABLED='1')
record={'started':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'RUNNING','kind':'normal cmd/server compile only; no service/test/browser execution','base':'60206809a9c46b9f11d8fb67590a889e24919df6','commands':[],'proxy_environment_unchanged':True,'worker_reuse':{'path':str(W),'sha256':sha(W)},'overlay':str(C/'overlay.json')}
def save():(E/'result.json').write_text(json.dumps(record,indent=2)+'\n')
def run(label,cmd):
 sample={'phase':label,'scratch_free':shutil.disk_usage(R).free,'tmp_free':shutil.disk_usage('/tmp').free};record.setdefault('resource_samples',[]).append(sample);assert min(sample['scratch_free'],sample['tmp_free'])>=1<<30,'actual free-space floor reached'
 record['commands'].append({'label':label,'argv':cmd});save()
 with (E/(label+'.stdout')).open('xb') as out,(E/(label+'.stderr')).open('xb') as err:x=subprocess.run(cmd,cwd=P,env=ENV,stdout=out,stderr=err)
 record['commands'][-1]['exit']=x.returncode;(E/(label+'.exit')).write_text(str(x.returncode)+'\n');save();assert x.returncode==0,label
 return (E/(label+'.stdout')).read_text()
def snapshot(paths):return {str(p):{'sha256':sha(p),'bytes':p.stat().st_size} for p in sorted(paths)}
def app_unchanged():
 prior=json.loads((R/'evidence/s03-qb-cookie-app01/inputs-after-test.json').read_text());assert snapshot({Path(p) for p in prior})==prior,'App source/fixture compile inputs drifted';assert sha(W)==record['worker_reuse']['sha256'];return len(prior)
try:
 overlay=json.loads((C/'overlay.json').read_text())['Replace'];binding=json.loads((C/'overlay-inputs.json').read_text());rows=[binding['active_original'],binding['source_oracle'],binding['source_original']]+binding['additional_bound_files'];assert set(overlay)=={x['virtual_path'] for x in binding['replacements']}
 for x in binding['replacements']:
  assert Path(x['virtual_path']).exists()==x['virtual_present_before']
  if x['backing'] is None:assert overlay[x['virtual_path']]=='';rows.append(x['removed_preimage'])
  else:assert overlay[x['virtual_path']]==x['backing']['path'];rows.append(x['backing'])
 for x in rows:assert Path(x['path']).stat().st_size==x['bytes'] and sha(x['path'])==x['sha256'],'preimage drift '+x['path']
 record['preflight_rows']=len(rows);record['app_reuse_inputs_verified']=app_unchanged();save()
 raw=run('go-list',['go','list','-overlay='+str(C/'overlay.json'),'-mod=readonly','-buildvcs=false','-deps','-json','./cmd/server']);decoder=json.JSONDecoder();offset=0;declarations=[];paths={P/'go.mod',P/'go.sum',Path(__file__).resolve(),C/'overlay.json',C/'overlay-inputs.json',R/'evidence/s03-qb-cookie-app01/result.json',R/'evidence/s03-qb-cookie-app01/inputs-after-test.json',R/'evidence/s03-qb-cookie-app01/tool-receipt.json'};paths.update(Path(x['path']) for x in rows)
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  pkg,offset=decoder.raw_decode(raw,offset)
  if not pkg.get('Dir'):continue
  for kind in ('GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles'):
   for name in pkg.get(kind,[]):
    virtual=str((Path(pkg['Dir'])/name).resolve());backing=overlay.get(virtual,virtual);assert backing,'hidden file declared '+virtual;path=Path(backing);assert path.is_file(),'missing compile input '+virtual;paths.add(path);declarations.append({'package':pkg.get('ImportPath'),'kind':kind,'virtual_path':virtual,'backing_path':backing})
 (E/'virtual-compile-inputs.json').write_text(json.dumps(declarations,indent=2)+'\n');before=snapshot(paths);(E/'inputs-before.json').write_text(json.dumps(before,indent=2)+'\n');record['input_count']=len(before);save()
 run('build',['go','build','-overlay='+str(C/'overlay.json'),'-mod=readonly','-buildvcs=false','-p=1','-o',str(BIN),'./cmd/server'])
 after=snapshot(paths);assert after==before,'server compile inputs drifted';(E/'inputs-after.json').write_text(json.dumps(after,indent=2)+'\n');record['app_reuse_inputs_verified_after']=app_unchanged();record.update(program_path=str(BIN),program_sha256=sha(BIN),status='PASS',input_drift=[])
except BaseException as err:record.update(status='FAIL',error_class=type(err).__name__,error=str(err));raise
finally:record['ended']=datetime.datetime.now(datetime.timezone.utc).isoformat();save();print(json.dumps(record),flush=True)
