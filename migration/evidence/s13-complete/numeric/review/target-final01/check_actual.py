"""Read/hash existing gate evidence only. Does not execute source or Go."""
from pathlib import Path
import hashlib,json,sys,collections
R=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305');A=R/'release-light-media';G=R/'target-drafts/s13-tr-numeric01/gates01';OUT=R/'audits/s13-tr-numeric-independent/target-final01'
mode=sys.argv[1];assert mode in ('normal','race')
def rec(p):b=p.read_bytes();return {'path':str(p),'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()}
def load(p):return json.loads(p.read_bytes())
def check(p,v):
 a=rec(p);assert a['bytes']==v['bytes'] and a['sha256']==v['sha256'],str(p)
 return a
mapping=load(G/'overlay.json')['Replace'];gate=load(G/'seal.json');reports=[]
source=load(R/'evidence/s13-tr-numeric-source01/stdout.json');case_ids=[c['id'] for c in source['cases']]
for family,package in [('leaf','./internal/plugins/downloadercommands'),('sdk','./internal/plugins')]:
 label=f's13-tr-numeric-{family}-{mode}01';E=R/'evidence'/label;b=load(G/f'overlay-inputs-{family}-{mode}.json');result=load(E/'result.json');receipt=load(E/'coordinator-tool-receipt.json')
 assert result['status']=='PASS' and result['race']==(mode=='race') and result['package']==package
 assert result['overlay']==mapping and result['pattern']=='^('+'|'.join(b['roots'])+')$'
 assert receipt['tool_exit']==0 and receipt['result_sha256']==rec(E/'result.json')['sha256']
 for phase in ['go-list','compile','tests']:
  assert (E/(phase+'.exit')).read_text().strip()=='0'
  assert not (E/(phase+'.stderr')).read_bytes(),label+' '+phase
 assert len(result['commands'])==3 and all(c['exit']==0 for c in result['commands'])
 assert result['commands'][1]['argv'][0:2]==['go','test'] and '-c' in result['commands'][1]['argv'] and '-vet=off' in result['commands'][1]['argv']
 assert ('-race' in result['commands'][1]['argv'])==(mode=='race')
 assert ('-race' in result['commands'][0]['argv'])==(mode=='race')
 assert result['commands'][2]['argv'][0:3]==['go','tool','test2json']
 before=load(E/'inputs-before.json');compiled=load(E/'inputs-after-compile.json');after=load(E/'inputs-after-test.json')
 assert before==compiled==after and len(before)==result['input_count']
 generated=load(E/'generated-source-map.json');current=[];recovered=[]
 for path,value in before.items():
  p=Path(path)
  if p.is_file():current.append(check(p,value))
  else:
   assert path in generated,path
   check(E/generated[path],value);recovered.append(path)
 for path,copy in generated.items():check(E/copy,before[path])
 program=R/'runtime'/(label+'.test');assert rec(program)['sha256']==result['program_sha256']
 raw=(E/'go-list.stdout').read_text();decoder=json.JSONDecoder();offset=0;packages=[]
 while offset<len(raw):
  while offset<len(raw) and raw[offset].isspace():offset+=1
  if offset==len(raw):break
  value,offset=decoder.raw_decode(raw,offset);packages.append(value)
 declarations=[]
 for p in packages:
  if not p.get('Dir'):continue
  for kind in ['GoFiles','CgoFiles','TestGoFiles','XTestGoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles','TestEmbedFiles','XTestEmbedFiles']:
   for name in p.get(kind,[]):
    virtual=str((Path(p['Dir'])/name).resolve());backing=mapping.get(virtual,virtual);assert backing in before,backing
    declarations.append({'package':p.get('ImportPath'),'kind':kind,'virtual_path':virtual,'backing_path':backing})
 assert declarations==load(E/'virtual-compile-inputs.json')
 required=['internal/plugins/downloadercommands/tr_final.go','internal/plugins/downloadercommands/tr_numeric.go','internal/plugins/downloadercommands/tr_numeric_test.go'] if family=='leaf' else ['internal/plugins/shared_transmission_numeric_test.go']
 required.append('internal/plugins/downloadercommands/testdata/source-tr-numeric.json')
 for rel in required:
  virtual=str(A/rel);assert any(d['virtual_path']==virtual and d['backing_path']==mapping[virtual] for d in declarations),rel
 for row in [b['active_original'],b['source_oracle'],b['source_original'],*b['additional_bound_files']]:assert before[row['path']]=={k:row[k] for k in ['bytes','sha256']}
 for row in b['replacements']:
  backing=row['backing'];assert before[backing['path']]=={k:backing[k] for k in ['bytes','sha256']}
 events=[json.loads(line) for line in (E/'tests.stdout').read_text().splitlines()]
 assert not any(x.get('Action') in ['fail','skip'] for x in events)
 roots=[x['Test'] for x in events if x.get('Action')=='pass' and 'Test' in x and '/' not in x['Test']]
 assert roots==result['root_passes'] and sorted(roots)==sorted(b['roots'])
 numeric_root='TestTRNumericLeafActualSourceObservations' if family=='leaf' else 'TestSharedTRNumericOriginalStringsAndSourceClassification'
 actual_ids=[x['Test'].split('/',1)[1] for x in events if x.get('Action')=='pass' and x.get('Test','').startswith(numeric_root+'/')]
 assert sorted(actual_ids)==sorted(case_ids)
 assert len(generated)==1
 generated_text='\n'.join((E/path).read_text() for path in generated.values())
 for root in roots:assert root in generated_text
 reports.append({'label':label,'package':package,'race':mode=='race','started':result['started'],'ended':result['ended'],'program':rec(program),'receipt':rec(E/'coordinator-tool-receipt.json'),'tool_receipt_contents':receipt,'result':rec(E/'result.json'),'input_count':len(before),'three_snapshots_equal':True,'current_input_count':len(current),'missing_cache_recovered':recovered,'go_list_package_count':len(packages),'declaration_count':len(declarations),'generated_source_count':len(generated),'generated_files':[rec(E/x) for x in generated.values()],'root_passes':roots,'numeric_original_case_passes':actual_ids,'event_counts':dict(collections.Counter(x['Action'] for x in events)),'evidence_files':[rec(E/name) for name in ['inputs-before.json','inputs-after-compile.json','inputs-after-test.json','go-list.stdout','go-list.stderr','go-list.exit','compile.stdout','compile.stderr','compile.exit','tests.stdout','tests.stderr','tests.exit','virtual-compile-inputs.json','generated-source-map.json']],'vet_not_run':True})
assert reports[0]['ended']<=reports[1]['started']
report={'status':'FINITE_NUMERIC_'+mode.upper()+'_ACTUAL_ACCEPTED','mode':mode,'gate_preexec':rec(R/'audits/s13-tr-numeric-independent/gates-preexec01/preexec-seal.json'),'candidate_manifest':rec(R/'target-drafts/s13-tr-numeric01/candidate-manifest.json'),'cases_original14_and_retained23':True,'reports':reports,'source_or_Go_rerun_by_reviewer':False,'HTTP_App_daemon_or_fullS13_accepted':False}
(OUT/(mode+'-readback.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'status':report['status'],'gates':[{'label':x['label'],'inputs':x['input_count'],'current':x['current_input_count'],'roots':len(x['root_passes']),'numeric14':len(x['numeric_original_case_passes']),'program_sha256':x['program']['sha256']} for x in reports]}))
