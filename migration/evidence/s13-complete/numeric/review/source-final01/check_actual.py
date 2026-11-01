"""Read-only JSON/hash/AST review. Does not execute source, collector or target."""
from pathlib import Path
import ast
import hashlib
import json
from collections import Counter

ROOT=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
PREP=ROOT/'source-collectors/s13-tr-numeric01'
ACTUAL=ROOT/'evidence/s13-tr-numeric-source01'
OUT=ROOT/'audits/s13-tr-numeric-independent/source-final01'

def record(path):
    body=path.read_bytes()
    return {'path':str(path),'bytes':len(body),'sha256':hashlib.sha256(body).hexdigest()}

def load(path):return json.loads(path.read_bytes())
def checked(row):
    actual=record(Path(row['path']))
    assert all(actual[key]==row[key] for key in ('bytes','sha256')),row['path']
    return actual

manifest=load(PREP/'input-bindings.json')
preparation=load(PREP/'preparation-seal.json')
preexec=ROOT/'audits/s13-tr-numeric-coordinator-preexec01'
raw_record=record(ACTUAL/'stdout.json')
assert raw_record['sha256']=='1235d5889dd7c2c87bda487c628fc78567387deb98dd552c2384b88bafada29d'
data=load(ACTUAL/'stdout.json')
result=load(ACTUAL/'result.json')
receipt=load(ACTUAL/'tool-receipt.json')
assert (ACTUAL/'exit').read_text().strip()=='0' and not (ACTUAL/'stderr.log').read_bytes()
assert result['exit']==receipt['actual_exit_code']==0
assert receipt['actual_tool_chunk']=='cc88c0'
assert result['stdout_sha256']==receipt['stdout_sha256']==raw_record['sha256']
assert result['case_count']==receipt['case_count']==data['case_count']==14
assert data['source_commit']==manifest['source_commit']=='8a83efeacd3e26b82be495e17d50146e31c96a60'
assert data['input_manifest_sha256']==record(PREP/'input-bindings.json')['sha256']
assert data['input_drift']==result['input_drift']==[]
assert data['forbidden_imports_absent'] is result['forbidden_imports_absent'] is True
assert data['python']['version']==result['python_version']
assert data['python']['version'].startswith('3.12.14 ')
assert data['python']['executable']==manifest['python_invocation']==result['argv'][0]
assert result['argv'][1:]==['-I','-S','-B',str(PREP/'capture_tr_numeric.py')]
for key in ('isolated','no_site','dont_write_bytecode'):assert data['python'][key]==1
inputs=[checked(row) for row in manifest['inputs']]
assert len(inputs)==113
for path in manifest['absent_cache_paths']:assert not Path(path).exists(),path
prepared=[checked(row) for row in preparation['files']]
preexec_files=[checked(row) for row in load(preexec/'preexec-seal.json')['files']]
assert record(PREP/'preparation-seal.json')['sha256'].startswith('a41a9d49')
assert data['modules_before']==data['modules_after']
assert len(data['modules_before'])==46
by_path={r['path']:r for r in inputs}
for table in (data['modules_before'],data['modules_after']):
    for name,row in table.items():
        assert row['path'] in by_path
        assert row['sha256']==by_path[row['path']]['sha256']
stdlib=load(PREP/'stdlib-bindings.json')
assert stdlib['python']==data['python']['version']

bound=load(PREP/'source-nodes.json')['source_class']
source=Path(bound['path']).read_bytes()
assert len(source)==bound['bytes'] and hashlib.sha256(source).hexdigest()==bound['sha256']
tree=ast.parse(source,filename=bound['path'])
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==bound['class'])
assert not cls.bases and not cls.keywords and not cls.decorator_list
assert hashlib.sha256(ast.dump(cls,include_attributes=True).encode()).hexdigest()==bound['complete_class_ast_sha256']
actual_nodes=[];text_parts=[]
for expected in bound['nodes']:
    node=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name==expected['name'])
    segment=b''.join(source.splitlines(keepends=True)[node.lineno-1:node.end_lineno])
    assert not node.decorator_list and not any(isinstance(n,(ast.Import,ast.ImportFrom)) for n in ast.walk(node))
    actual={'name':node.name,'first_line':node.lineno,'last_line':node.end_lineno,'source_bytes_sha256':hashlib.sha256(segment).hexdigest(),'ast_sha256':hashlib.sha256(ast.dump(node,include_attributes=True).encode()).hexdigest(),'decorators':[]}
    assert actual==expected
    actual_nodes.append(actual)
    text_parts.append(f'### {bound["path"]} {bound["class"]}.{node.name} {node.lineno}-{node.end_lineno}\n'.encode()+segment+b'\n')
assert b''.join(text_parts)==(PREP/'selected-source.txt').read_bytes()
assert actual_nodes==data['source_nodes']
# Check the claimed old recorder/extractor identity without executing either.
delta=load(PREP/'driver-delta.json')
checked(delta['baseline']);checked(delta['new_collector'])
a=ast.parse(Path(delta['baseline']['path']).read_bytes());b=ast.parse((PREP/'capture_tr_numeric.py').read_bytes())
old={n.name:n for n in a.body if isinstance(n,(ast.FunctionDef,ast.ClassDef))};new={n.name:n for n in b.body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
intact=[]
for name in delta['intact_original_capture_functions']:
    assert ast.dump(old[name],include_attributes=False)==ast.dump(new[name],include_attributes=False),name
    intact.append(name)

cases=load(PREP/'owned-cases.json')['cases']
assert len(cases)==14 and len({c['id'] for c in cases})==14
observations=[];counts=Counter();total_calls=0
for planned,actual in zip(cases,data['cases'],strict=True):
    assert actual['input']==planned and actual['id']==planned['id']
    assert actual['arguments_after_source']==planned['args']
    assert planned['method']=='change_torrent' and planned['client_present'] is True
    assert planned['args']['hash_string']=='owned-numeric'
    for key,value in planned['args'].items():
        if key!='hash_string':assert value is None or type(value) is str and len(value)<=64
    calls=actual['recording_client_calls'];trace=actual['trace'];outcome=actual['outcome']
    assert calls==len(planned['steps'])
    total_calls+=calls;counts[outcome['kind']]+=1
    if calls:
        assert calls==1 and len(trace)==2
        assert trace[0]['event']=='recording_client_call' and trace[0]['method']=='change_torrent' and trace[0]['ordinal']==1
        assert trace[1]=={'event':'recording_client_return','method':'change_torrent','value':None}
        assert trace[0]['kwargs']['ids']=='owned-numeric'
        assert set(trace[0]['kwargs'])==set(trace[0]['python_types'])
        # This validates actual serialized type claims, not numeric conversions.
        for key,value in trace[0]['kwargs'].items():assert type(value).__name__==trace[0]['python_types'][key]
        assert outcome=={'kind':'return','value':True,'python_type':'bool','python_truthy':True}
    else:
        assert trace==[] and outcome['kind']=='uncaught_exception' and outcome['class']=='ValueError'
        assert type(outcome['message']) is str and outcome['message']
    observations.append({key:actual[key] for key in ['id','outcome','trace','recording_client_calls','arguments_after_source']})
assert total_calls==result['recording_calls']==receipt['recording_calls']==3
assert counts=={'return':3,'uncaught_exception':11}
report={'status':'FINITE_ORIGINAL_NUMERIC14_ACTUAL_ACCEPTED','reviewer_source_or_target_execution':False,'raw':raw_record,'result':record(ACTUAL/'result.json'),'receipt':record(ACTUAL/'tool-receipt.json'),'preparation_seal':record(PREP/'preparation-seal.json'),'preexec_seal':record(preexec/'preexec-seal.json'),'inputs_current':inputs,'prepared_current':prepared,'preexec_current':preexec_files,'actual_python':data['python'],'modules_before_after_equal':True,'module_file_count':46,'expected_missing_cache_count':len(manifest['absent_cache_paths']),'complete_source_nodes':actual_nodes,'recorder_extractor_AST_unchanged':intact,'case_count':14,'recording_calls':3,'outcome_counts':dict(counts),'observations':observations,'version_erratum':{'path':str(ROOT/'audits/s13-tr-numeric-scope01/REVIEW.md'),'line':12,'incorrect_current_runtime':'3.12.13','actual_current_runtime':'3.12.14','historical_serverstatus_line22_not_reaudited':True},'limits':['recording client and logger, no daemon/RPC acceptance','no global syscall audit; forbidden imports checked, not a full native-effects trace','11 errors before outbound call, no local partial kwargs observation','only complete change_torrent called; retained set_torrent_tag not invoked','no rerun of prior S13 operations/source23; no full S13 acceptance']}
(OUT/'review.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':report['status'],'inputs':len(inputs),'modules':46,'cases':14,'recording_calls':3,'outcomes':dict(counts),'python':data['python']['version'],'raw':raw_record['sha256']},ensure_ascii=False))
