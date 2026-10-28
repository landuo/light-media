"""Actual pinned TransferInfo and nested FileItem constructor/coercion/defaults.
No storage or service is replaced or run. Non-finite floats are separately
classified when they cannot be encoded as standard JSON for the Go SDK wire.
"""
import copy,json,sys
from pathlib import Path
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
Info=env['ns']['TransferInfo'];cases=[]
vectors=[('defaults',{}),('explicit_nulls',dict(fileitem=None,target_item=None,target_diritem=None,file_count=None,total_size=None,file_list=None,file_list_new=None,fail_list=None,message=None,transfer_type=None,need_notify=None,need_scrape=None)),('bool_words',dict(success='YES',need_notify='off',need_scrape='true')),('bool_numbers',dict(success=1,need_notify=0.0,need_scrape=1.0)),('bad_success_null',dict(success=None)),('bad_success_two',dict(success=2)),('bad_bool_space',dict(need_scrape=' true ')),('bad_bool_object',dict(need_notify={})),('integer_strings',dict(file_count='2.0',total_size='1_024')),('integer_bool',dict(file_count=True,total_size=False)),('integer_float',dict(file_count=2.0,total_size=0.0)),('integer_big',dict(total_size=2**100)),('integer_huge_float',dict(total_size=1e100)),('bad_integer_fraction',dict(file_count=1.5)),('bad_integer_exponent_string',dict(file_count='2e3')),('bad_integer_object',dict(file_count={})),('negative_integers',dict(file_count=-2,total_size=-1)),('lists_any_values',dict(file_list=[None,True,3,{'path':'opaque'}],file_list_new=[],fail_list=['owned'])),('bad_list_dict',dict(file_list={})),('bad_list_text',dict(file_list_new='owned')),('message_text',dict(message='Owned 中文\nmessage',transfer_type='link')),('bad_message_number',dict(message=123)),('bad_type_bool',dict(transfer_type=False)),('empty_items',dict(fileitem={},target_item={},target_diritem={})),('item_defaults',dict(target_item=dict(path='/owned/Target.mkv',type='file',name='Target.mkv'))),('item_coercions',dict(fileitem=dict(path='/owned/Source.mkv',size='12.0',modify_time='42.5',children=[None,{'unknown':'retained child'}],unknown='discarded'),unknown='discarded')),('item_nulls',dict(target_item=dict(path=None,storage=None,size=None,modify_time=None,children=None))),('item_integer_bool_float_time',dict(fileitem=dict(size=True,modify_time=False))),('item_bad_fraction',dict(fileitem=dict(size=2.5))),('item_bad_path_number',dict(target_item=dict(path=123))),('item_bad_storage_bool',dict(target_item=dict(storage=False))),('item_bad_children_dict',dict(fileitem=dict(children={}))),('item_bad_object_list',dict(target_item=[])),('item_float_underscore',dict(fileitem=dict(modify_time='1_024.5'))),('item_float_hex',dict(fileitem=dict(modify_time='0x1p2'))),('item_nonfinite',dict(fileitem=dict(modify_time='Infinity')))]
vectors += [('float_below_i64_max',{'total_size':float(2**63-1024)}),('float_at_i64_max',{'total_size':float(2**63)}),('float_at_i64_min',{'total_size':float(-(2**63))}),('float_below_i64_min',{'total_size':float(-(2**63)-2048)}),('float_at_u64',{'total_size':float(2**64)}),('string_big_zero_decimal',{'total_size':'1267650600228229401496703205376.0'}),('bad_float_underscore_decimal',{'fileitem':{'modify_time':'1._0'}}),('float_zero_underflow',{'fileitem':{'modify_time':'1e-999'}}),('veto',{'success':False,'message':'owned constructor veto','need_scrape':None})]
for name,fields in vectors:
 error=None;wire_error=None;output=None
 try:
  value=Info(**copy.deepcopy(fields));output=value.model_dump(mode='json')
  try:json.dumps(output,allow_nan=False)
  except ValueError:wire_error='nonfinite_standard_json';output=None
 except Exception as e:error=type(e).__name__
 cases.append(dict(name=name,input=fields,output=output,validation_error=error,wire_error=wire_error))
Path(__file__).with_name('transfer-info-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/schemas/transfer.py:TransferInfo','app/schemas/file.py:FileURI/FileItem'],'kind':__doc__,'python':sys.version,'cases':cases},ensure_ascii=False,indent=2)+'\n')
print(len(cases),'actual fixed TransferInfo/FileItem constructor cases')
