#!/usr/bin/env python3
"""Narrow independent re-execution of existing source inputs; no target oracle."""
from pathlib import Path
import base64,hashlib,json,runpy,sys,tempfile,platform
ROOT=Path(__file__).resolve().parent.parent; E=ROOT/'evidence'
sys.dont_write_bytecode=True
D=ROOT.parent/'toolchain/python-oracle/ruamel-0.19.1';sys.path.insert(0,str(D))
import ruamel.yaml
assert ruamel.yaml.__version__=='0.19.1'
driver=E/'categoryeditor-source-helper-replay.py'; n=runpy.run_path(str(driver),run_name='narrow_source_reuse')
assert n['digest'](n['SOURCE'])==n['SHA'];assert n['digest'](n['PLUGIN_SOURCE'])==n['PLUGIN_SHA']
provenance=json.loads(n['PROVENANCE'].read_text()); assert {x:n['digest'](D/x) for x in provenance['python_sources']}==provenance['python_sources']
primary=json.loads((E/'categoryeditor-source-helper-inputs.json').read_text()); extra=json.loads((E/'categoryeditor-review-source-extra-inputs.json').read_text())
selected={'blank','parser_failure','scalar_true','scalar_integer','scalar_string','nonempty_sequence','movie_scalar','section_scalar','rule_scalar','unicode_decimal_range','unicode_superscript_range','yaml11_boolean_condition','duplicate_true_and_one','html_escaped_payload_under_64k','truthy_empty_string_list_string_condition','condition_bool','condition_integer'}
wire_path=E/'categoryeditor-independent-wire-edge-inputs-20261029.json';wire=json.loads(wire_path.read_text());selected.update(x['id'] for x in wire['cases'])
fixtures=Path(tempfile.mkdtemp(prefix='categoryeditor-independent-narrow-source-',dir=E))
paths=[wire_path,Path(__file__),driver,n['SOURCE'],n['PLUGIN_SOURCE'],n['PROVENANCE'],E/'categoryeditor-source-helper-inputs.json',E/'categoryeditor-review-source-extra-inputs.json']
out={'scope':'controlled unmodified historical helper + uploaded current plugin replay; explicit dependency adapters; not current NAS or target validation','current_nas_helper_verified':False,'source_bindings':{str(p.relative_to(ROOT)):n['digest'](p) for p in paths},'yaml_version':ruamel.yaml.__version__,'yaml_python_sources':provenance['python_sources'],'environment':{'python':sys.version,'platform':platform.platform()},'fixtures':str(fixtures.relative_to(ROOT)),'cases':[]}
for case in primary['cases']+extra['cases']+wire['cases']:
 if case['id'] not in selected:continue
 data=dict(primary);data['media']=case.get('media',primary['media']); row=n['replay'](case,fixtures/case['id'],data);out['cases'].append(row)
 print(json.dumps({'id':case['id'],'apply':row['warm']['plugin_with_retained_archived_helper']['call'],'match':row['warm']['lookups']['movie_match']},ensure_ascii=False))
p=E/'categoryeditor-independent-narrow-source-20261029.json';p.write_text(json.dumps(out,ensure_ascii=True,indent=2)+'\n');print('recorded',len(out['cases']),n['digest'](p))
