import collections,gzip,hashlib,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];OUT=ROOT/'testdata/plugin-platform-final-20261104/final-independent-review.json'
BACK='8a83efeacd3e26b82be495e17d50146e31c96a60';FRONT='2829e3b08214348f05ff2f5beae644882eccad58'
SOURCES={'B':('/Users/accidia/projects/MoviePilot',BACK),'F':('/Users/accidia/projects/MoviePilot-Frontend',FRONT)}
def digest(data):return hashlib.sha256(data).hexdigest()
def hashfile(path):return digest(path.read_bytes())
def load(path):
 p=ROOT/path;data=p.read_bytes();return json.loads(gzip.decompress(data)if p.suffix=='.gz'else data)
old=load('testdata/plugin-module-no-official-auth-20261026/final-evidence-50-20261029/work/plugin-no-official-auth-20261026/final50-audit.json.gz')
old_errors=[];checked_old=[]
for row in old['manifests']:
 p=ROOT/row['path'];got=hashfile(p)if p.exists()else'absent'
 if got!=row['sha256']:old_errors.append({'path':row['path'],'expected':row['sha256'],'actual':got})
for row in old['artifacts']:
 p=ROOT/row['path'];stored=p.read_bytes()if p.exists()else None
 got=digest(stored)if stored is not None else'absent'
 if got!=row['stored_sha256']:old_errors.append({'path':row['path'],'kind':'stored','expected':row['stored_sha256'],'actual':got})
 if stored is not None:
  raw=gzip.decompress(stored)if p.suffix=='.gz'else stored
  if digest(raw)!=row['raw_sha256']:old_errors.append({'path':row['path'],'kind':'raw','expected':row['raw_sha256'],'actual':digest(raw)})
 checked_old.append({'path':row['path'],'stored_sha256':got,'raw_sha256':row['raw_sha256']})
contents={}
def source(tree,path):
 key=(tree,path)
 if key not in contents:
  repo,sha=SOURCES[tree];data=subprocess.check_output(['git','-C',repo,'show',sha+':'+path]);contents[key]=data
 return contents[key]
source_errors=[];positions=[]
for row in old['source_locations']:
 data=source(row['tree'],row['path']);valid=all(0<int(n)<=len(data.splitlines())for n in re.findall(r'\d+',row['location']))
 if digest(data)!=row['sha256']or not valid:source_errors.append(row)
 positions.append(row)
allfiles=subprocess.check_output(['rg','--files'],cwd=ROOT,text=True).splitlines();byname=collections.defaultdict(list)
for path in allfiles:byname[pathlib.Path(path).name].append(path)
def resolve(text):
 text=text.split(':')[0].strip('` ')
 if (ROOT/text).exists():return [text]
 if (ROOT/'docs'/text).exists():return ['docs/'+text]
 if '*'in text:return [str(p.relative_to(ROOT))for prefix in ['','internal/app/','internal/','web/src/components/','web/src/features/']for p in ROOT.glob(prefix+text)]
 values=[]
 for prefix in ['internal/app/','internal/','web/src/components/','web/src/features/','web/src/','cmd/']:
  if(ROOT/(prefix+text)).exists():values.append(prefix+text)
 if not values:values=byname.get(pathlib.Path(text).name,[])
 if not values:values=byname.get(pathlib.Path(text).name+'.gz',[])
 return sorted(set(values))
rows=[];matrix=ROOT/'docs/plugin-module-parity-matrix.md'
for n,line in enumerate(matrix.read_text().splitlines(),1):
 if not re.match(r'^\| (?:S\d\d|\d\d) ·',line):continue
 c=[x.strip()for x in line.split('|')[1:-1]];identity=c[0].split(' ')[0]
 row={'id':identity,'matrix_line':n,'name':c[0],'state':c[-1],'source_column':c[1],'target_column':c[2],'necessary_adaptation_column':c[3],'evidence_column':c[4]}
 explicit=[]
 for tree,path,location in re.findall(r'\b([BF])/([^`<>;；\s:]+):(\d+(?:[-–,]\d+)*)',c[1]):
  try:data=source(tree,path)
  except subprocess.CalledProcessError:source_errors.append({'id':identity,'missing_source':path});continue
  valid=all(0<int(x)<=len(data.splitlines())for x in re.findall(r'\d+',location));explicit.append({'tree':tree,'path':path,'location':location,'sha256':digest(data),'valid':valid})
  if not valid:source_errors.append({'id':identity,'invalid_source':path+':'+location})
 row['direct_fixed_source_positions']=explicit
 if not explicit:row['source_inheritance']='composite index or abbreviated supplemental source; bound primary acceptance manifest/source oracle must provide exact positions'
 tokens=[x for x in re.findall(r'`([^`]+)`',c[2])if re.search(r'\.(go|vue|ts)$|/|\.go\*|\*\.go',x)]
 row['target_resolutions']={x:resolve(x)for x in tokens}
 refs=re.findall(r'\]\(([^)]+)\)',c[4])+[x for x in re.findall(r'`([^`]+)`',c[4])if re.search(r'\.(?:json|jsonl|log|py|gz)$|/$',x)]
 row['evidence_resolutions']={x:resolve(x)for x in refs}
 if identity.isdigit():row['legacy_source_artifact_audit_covered']=int(identity)in range(1,51)
 rows.append(row)
supplemental_ids={r['id']for r in rows if r['id'].startswith('S')}
expected_ids={f'{i:02d}'for i in range(1,51)}|{f'S{i:02d}'for i in range(1,len(supplemental_ids)+1)}
assert {r['id']for r in rows}==expected_ids and len(rows)==len(expected_ids)
assert {r['id']for r in rows if r['state']=='按用户要求排除'}=={'22','44','45','46'}
accepted_paths=sorted(p.relative_to(ROOT).as_posix()for folder in ['testdata/plugin-host-integration-20261031','testdata/plugin-platform-final-20261104'] for p in (ROOT/folder).glob('accepted-*.json'))+['testdata/plugin-host-local-files-20261031/evidence/acceptance.json']
for optional in ['testdata/plugin-platform-final-20261104/message/acceptance.json','testdata/plugin-platform-final-20261104/browser-ocr/acceptance.json']:
 if(ROOT/optional).exists() and optional not in accepted_paths:accepted_paths.append(optional)
accepted_results=[];new_errors=[]
def scan_hashes(value,where,rows):
 if isinstance(value,dict):
  record_path=value.get('path');record_hash=value.get('sha256')
  if isinstance(record_path,str)and re.fullmatch('[0-9a-f]{64}',str(record_hash))and record_path.startswith(('testdata/','docs/','internal/','cmd/','web/','go.')):
   p=ROOT/record_path;actual=hashfile(p)if p.exists()else'absent';rows.append({'path':record_path,'expected':record_hash,'actual':actual,'match':actual==record_hash,'location':where+'/path+sha256'})
  for k,v in value.items():
   if isinstance(v,str)and re.fullmatch('[0-9a-f]{64}',v)and (k.startswith(('testdata/','docs/','internal/','cmd/','web/','go.'))):
    p=ROOT/k;actual=hashfile(p)if p.exists()else'absent';rows.append({'path':k,'expected':v,'actual':actual,'match':actual==v,'location':where+'/'+k})
   elif isinstance(v,dict)and re.fullmatch('[0-9a-f]{64}',str(v.get('sha256','')))and k.startswith(('testdata/','docs/','internal/','cmd/','web/')):
    p=ROOT/k;actual=hashfile(p)if p.exists()else'absent';rows.append({'path':k,'expected':v['sha256'],'actual':actual,'match':actual==v['sha256'],'location':where+'/'+k})
   else:scan_hashes(v,where+'/'+k,rows)
 elif isinstance(value,list):
  for i,item in enumerate(value):scan_hashes(item,where+'/'+str(i),rows)
for path in accepted_paths:
 j=load(path);checks=[];scan_hashes(j,'',checks)
 errors=[r for r in checks if not r['match']];new_errors.extend({'manifest':path,**r}for r in errors)
 accepted_results.append({'path':path,'sha256':hashfile(ROOT/path),'rows':j.get('matrix_rows',j.get('accepted',j.get('accepted_features',j.get('features',j.get('matrix'))))),'hash_checks':checks,'errors':errors,'runtime_classification':j.get('classification',j.get('verification',j.get('review',j.get('coverage'))))})
lineage_resolutions=[]
lineage_path='testdata/plugin-platform-final-20261104/s02-evidence-lineage-supplement.json'
if(ROOT/lineage_path).exists():
 supplement=load(lineage_path)
 for p,h in supplement['artifacts'].items():assert hashfile(ROOT/p)==h,(lineage_path,p)
 amended=load(supplement['review_amendment']['path'])
 for p,h in amended['reviewed_files'].items():assert hashfile(ROOT/p)==h,(lineage_path,p)
 for error in new_errors:
  if error['manifest']==supplement['historical_acceptance']['path'] and error['path']==supplement['review_amendment']['path'] and error['actual']==supplement['review_amendment']['sha256'] and error['expected']==supplement['historical_acceptance']['stale_review_sha256']:
   error['resolved_via_lineage_supplement']=lineage_path
 lineage_resolutions.append({'path':lineage_path,'sha256':hashfile(ROOT/lineage_path),'classification':supplement['classification'],'independent_reviewed_file_count':len(amended['reviewed_files']),'current_review_sha256':supplement['review_amendment']['sha256'],'all_artifact_hashes_valid':True,'no_claim_historical_rerun':True})
unresolved_new_errors=[e for e in new_errors if not e.get('resolved_via_lineage_supplement')]
nested_paths=['testdata/plugin-host-downloader-20261031/manifest.json','testdata/plugin-host-downloader-20261031/postguard-connection-increment/manifest.json','testdata/plugin-host-services-20261031/evidence-manifest.json','testdata/plugin-host-services-20261031/root-models-independent-review.json','testdata/plugin-host-services-20261031/local-files-independent-review.json']
for optional in ['testdata/plugin-platform-final-20261104/message/acceptance.json','testdata/plugin-platform-final-20261104/browser-ocr/acceptance.json']:
 if(ROOT/optional).exists() and optional not in nested_paths:nested_paths.append(optional)
# Include every current primary manifest in full declared-group verification.
# This catches relative evidence_sha256/fixture files and Dockerfile/scripts too.
for primary in accepted_paths:
 if primary not in nested_paths:nested_paths.append(primary)
authoritative_targets={}
for p in accepted_paths:
 j=load(p)
 for group in ['target_files','target_sha256']:
  for key,value in j.get(group,{}).items():
   h=value if isinstance(value,str)else value.get('sha256')
   if h:authoritative_targets[key]=h
nested_checks=[];nested_errors=[]
for path in nested_paths:
 j=load(path);records=[]
 for group in ['target_files','target_sha256','targets','reviewed_files','fixture_files','evidence_files','evidence_sha256','additional_evidence','evidence','files','artifacts']:
  for key,value in j.get(group,{}).items():
   expected=value if isinstance(value,str)else value.get('sha256')if isinstance(value,dict)else None
   if not re.fullmatch('[0-9a-f]{64}',str(expected)):continue
   candidates=([ROOT/key,(ROOT/path).parent/key,(ROOT/path).parent.parent/key] if key.startswith(('testdata/','docs/','internal/','cmd/','web/')) else [(ROOT/path).parent/key,(ROOT/path).parent.parent/key,ROOT/key])
   file=next((p for p in candidates if p.is_file()),None)
   actual=hashfile(file)if file else'absent';match=actual==expected
   superseded=not match and key in authoritative_targets and authoritative_targets[key]==actual
   record={'group':group,'key':key,'resolved':str(file.relative_to(ROOT))if file else None,'expected':expected,'actual':actual,'match':match,'superseded_by_current_primary_target':superseded}
   records.append(record)
   if not match and not superseded:nested_errors.append({'manifest':path,**record})
 nested_checks.append({'manifest':path,'sha256':hashfile(ROOT/path),'records':records})
counts=dict(collections.Counter(r['state']for r in rows));in_scope=[r for r in rows if r['state']!='按用户要求排除']
requirements=[{'id':'final.current_compilation_binding','status':'pending','requirement':'Stable final cmd/server and every acceptance worker build inputs captured; all relevant central routing/runtime/protocol current changes covered. Prior binaries remain incremental evidence only.'},{'id':'final.current_platform_sdk','status':'pending','requirement':'Current ordinary SDK/host platform regression passes with correct freshly compiled fixture environment; any failure disposition reviewed from raw logs.'},{'id':'final.current_linux_host','status':'pending','requirement':'Actual final Linux host+installed Go worker installation/config-init/API/data/call/failure/recovery/stop/reload/update/cold/clone/reset/uninstall; route absence and side effects inspected, not HTTP200.'},{'id':'final.current_browser','status':'pending','requirement':'Actual browser plugin UI config save failure/recovery, page call, source/clone isolation and reload against stated final or explicitly linked binary; runtime cold restart proven separately.'},{'id':'final.s15','status':'pending','requirement':'Ordinary OCR fixed source+actual SDK/Linux+browser+independent evidence; no site/AI business migration.'},{'id':'final.s16','status':'pending','requirement':'Ordinary JS renderer actual Chromium execution, product Docker/runtime provision, normal/failure/cleanup and source FlareSolverr path; AI browser classes/actions absent.'},{'id':'final.s17','status':'pending','requirement':'Ordinary incoming parser/input/history/command/user.message chain fixed source comparison, actual SDK/Linux/browser and independently reviewed AI split; no Agent/permission placeholder.'},{'id':'final.frontend','status':'pending','requirement':'Current frontend tests/build and plugin scope checks pass; source-bound UI evidence retained.'},{'id':'final.cleanup','status':'pending','requirement':'Only owned test processes/binaries/temp roots/tunnels cleaned, production/original unchanged.'}]
current_review_paths=['testdata/plugin-platform-final-20261104/settings/independent-review.json','testdata/plugin-platform-final-20261104/message-independent-review.json','testdata/plugin-platform-final-20261104/browser-ocr-independent-review.json','testdata/plugin-platform-final-20261104/settings/concurrent-cancel-fence-review.json','testdata/plugin-platform-final-20261104/settings/settings-complete-scope-review.json','testdata/plugin-platform-final-20261104/settings/s19-independent-review.json','testdata/plugin-platform-final-20261104/settings/s18-independent-review.json']
current_reviews={p:hashfile(ROOT/p)for p in current_review_paths if(ROOT/p).exists()}
behavior_rows={r['id']:digest(json.dumps({k:r[k]for k in ['source_column','target_column','necessary_adaptation_column','evidence_column']},ensure_ascii=False,sort_keys=True).encode())for r in rows if r['id']!='50'and r['state']!='按用户要求排除'}
report={'status':'pending_final_gates','reviewer':'/root/settings_scope, independent of implementation authors; read-only audit; no new acceptance count assigned','authoritative_matrix':'docs/plugin-module-parity-matrix.md','matrix_sha256':hashfile(matrix),'behavior_row_bindings_excluding_closing_metadata50':behavior_rows,'current_review_bindings':current_reviews,'source_commits':{'backend':BACK,'frontend':FRONT},'counts':{'rows':len(rows),'excluded':4,'in_scope':len(in_scope),'states':counts},'rows':rows,'legacy_baseline_audit':{'scope':'Original50 indices only; excludes22/44/45/46 and does not accept S01–S17 or current50 completion','source':'testdata/plugin-module-no-official-auth-20261026/final-evidence-50-20261029/work/plugin-no-official-auth-20261026/final50-audit.json.gz','checked_manifests':len(old['manifests']),'checked_artifact_records':len(old['artifacts']),'checked_fixed_source_locations':len(old['source_locations']),'errors':old_errors,'source_errors':source_errors,'actual_artifact_hashes':checked_old},'supplemental_acceptance_audit':accepted_results,'nested_supplemental_manifest_checks':nested_checks,'nested_supplemental_hash_errors':nested_errors,'lineage_resolutions':lineage_resolutions,'supplemental_unresolved_hash_errors':unresolved_new_errors,'supplemental_hash_errors':new_errors,'unverified_in_scope':[r['id']for r in in_scope if r['state']!='验收通过'],'unexplained_differences':[],'blocking_findings':([{'id':'accepted.hash_mismatch','status':'pending_coordinator_resolution','detail':'Previously accepted immutable evidence has a changed input. Require explicit historical-to-current lineage with source impact, newly compiled input binding and actual affected regression/runtime evidence; do not rewrite an old acceptance hash as if rerun.','mismatches':unresolved_new_errors}]if unresolved_new_errors else[]) + ([{'id':'nested.hash_mismatch','status':'pending_coordinator_resolution','details':nested_errors}]if nested_errors else[]),'required_final_gates':requirements,'evidence_classification_rules':['source AST/vendor replay is separate from actual Go host/SDK','owned protocol peer is synthetic external dependency while cmd/server/SQLite/files/PIDs/browser are real execution','browser reload is not server restart; source/clone worker versions/binaries remain explicitly bound','prior published main platform is historical delivery; uncommitted host additions are not main delivered'],'delivery':{'branch':subprocess.check_output(['git','branch','--show-current'],cwd=ROOT,text=True).strip(),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'current_changes_committed':False,'current_changes_pushed':False,'current_changes_deployed':False}}
# Group inventory is a view over the unique matrix, not a second acceptance ledger.
group_target_audit=[]
legacy_group_binding=load('testdata/plugin-platform-final-20261104/stable-platform/binding.json')
r3_context=load('testdata/plugin-platform-final-20261104/browser-ocr/product-r3-source-inputs.json')
def collect_groups(value):
 if isinstance(value,dict):
  if re.fullmatch('P[0-9]{2}',str(value.get('id',''))):
   targets=[]
   for path in value.get('target_paths',[]):
    p=ROOT/path;actual=hashfile(p)if p.is_file()else'absent'
    targets.append({'path':path,'sha256':actual,'stable_platform_baseline_same':legacy_group_binding['target_files'].get(path)==actual,'r3_full_context_same':r3_context['files'].get(path)==actual})
   group_target_audit.append({'id':value['id'],'matrix_indices':value.get('existing_matrix_indices',[]),'target_paths':targets,'classification':'Current content binding only; runtime proof belongs to source/target/evidence per unique matrix leaf and current final compilation/delta gates. Prior context equality does not alone prove behavior.'})
  for child in value.values():collect_groups(child)
 elif isinstance(value,list):
  for child in value:collect_groups(child)
collect_groups(load('migration/ledger.json'))
if any(r['id']=='S18'for r in rows):
 report['required_final_gates'].append({'id':'final.s18','status':'pending','requirement':'Fixed source SiteOper raw credential/read and ordinary RequestUtils request/post_res semantics, private trusted-worker typed credentials, actual configured-site HTTP SDK/Linux/browser+clone/cold/failure and credential non-leakage; no business signin workflow or crypto transformation framework.'})
if any(r['id']=='S19'for r in rows):
 report['required_final_gates'].append({'id':'final.s19','status':'pending','requirement':'Original config.PROXY getter and actual filtered proxy/CGI/environment snapshot exposed only to trusted private current SDK; normal Go POST through owned proxy, fresh/revoked/clone/config/env/cold guards and actual Linux/browser proof; no notification business or whole-host setting dump.'})
report['group_target_paths_audit']=group_target_audit
report['group_coverage_rule']='P01-P13 inherit only accepted in-scope matrix leaves; group scenarios and facets must link this final source/target/runtime evidence seal. Workflows, AI, official authentication are excluded by user scope. Publication/main/commit are separate and have not occurred.'
report['whole_review_input_binding_rule']='All referenced archived manifests/artifacts, supplemental manifests and independent reviews are individually SHA256 bound. Matrix behavior columns except closing row50 are bound to avoid a self-reference when the coordinator updates only final status/header. Later current-source native/routing changes require explicit newly compiled and actually executed delta proof; previous binary bindings are never silently rewritten.'
OUT.parent.mkdir(parents=True,exist_ok=True);OUT.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'counts':report['counts'],'legacy_artifact_errors':len(old_errors),'source_errors':len(source_errors),'accepted_manifests':len(accepted_results),'nested_checks':sum(len(x['records'])for x in nested_checks),'nested_errors':nested_errors,'unresolved_hash_errors':unresolved_new_errors,'lineage_resolutions':len(lineage_resolutions),'pending':report['unverified_in_scope']},ensure_ascii=False))
