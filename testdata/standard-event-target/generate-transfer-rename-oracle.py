"""Actual fixed get_rename_path + Pydantic event models and Jinja2 3.1.6."""
import ast,copy,hashlib,json,pathlib,types,sys
from typing import Optional,Dict,Any
from pydantic import BaseModel,Field
from jinja2 import Template
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
paths=['app/schemas/file.py','app/schemas/event.py','app/modules/filemanager/transhandler.py']
raw={p:(root/p).read_text()for p in paths}
ns=dict(BaseModel=BaseModel,Field=Field,Optional=Optional,Dict=Dict,Any=Any,Path=pathlib.Path,Template=Template)
def execute(nodes,name):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),name,'exec'),ns)
execute([n for n in ast.parse(raw[paths[0]]).body if isinstance(n,ast.ClassDef)and n.name in ['FileURI','FileItem']],paths[0])
execute([n for n in ast.parse(raw[paths[1]]).body if isinstance(n,ast.ClassDef)and n.name in ['BaseEventData','ChainEventData','TransferRenameBuildEventData','TransferRenameEventData']],paths[1])
for name in ['FileURI','FileItem','TransferRenameBuildEventData','TransferRenameEventData']:ns[name].model_rebuild(_types_namespace=ns)
method=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw[paths[2]]))if isinstance(n,ast.FunctionDef)and n.name=='get_rename_path'));method.decorator_list=[]
execute([method],paths[2]);ns['logger']=types.SimpleNamespace(debug=lambda *a:None)
ns['ChainEventType']=types.SimpleNamespace(TransferRenameBuild='transfer.rename.build',TransferRename='transfer.rename')
template='{{ title }} ({{ year }})/{{ title }}.mkv';variables=dict(title='Owned',year=2026)
cases=[]
scenarios=[('unchanged',{}, {},False,False),('build_context',{'rename_dict':dict(title='Changed',year=2027)}, {},False,False),('template_input_ignored',{'template_string':'ignored'}, {},False,False),('updated_false',{},dict(updated=False,updated_str='ignored'),False,False),('updated_empty',{},dict(updated=True,updated_str=''),False,False),('updated_true',{},dict(updated=True,updated_str='Changed.mkv'),False,False),('absolute_updated',{},dict(updated=True,updated_str='/owned-outside.mkv'),False,False),('build_error_after_mutation',{'rename_dict':dict(title='Before failure',year=2026)},{},True,False),('rename_error_after_mutation',{},dict(updated=True,updated_str='Before failure.mkv'),False,True)]
for name,build_patch,rename_patch,build_failure,rename_failure in scenarios:
 events=[];errors=[]
 def send(kind,data):
  events.append(dict(type=kind,input=data.model_dump(mode='json')))
  for key,value in (build_patch if kind=='transfer.rename.build'else rename_patch).items():setattr(data,key,copy.deepcopy(value))
  # The already-audited EventManager safe-invoke boundary retains mutation.
  if (build_failure if kind=='transfer.rename.build'else rename_failure):errors.append(kind)
  return types.SimpleNamespace(event_data=data)
 ns['eventmanager']=types.SimpleNamespace(send_event=send)
 result=ns['get_rename_path'](template,copy.deepcopy(variables),path=pathlib.Path('/owned-target'),source_path='/owned-source/old.mkv',source_item=None)
 cases.append(dict(name=name,template=template,variables=variables,base='/owned-target',source='/owned-source/old.mkv',patches={'transfer.rename.build':build_patch,'transfer.rename':rename_patch},fail={'transfer.rename.build':build_failure,'transfer.rename':rename_failure},events=events,errors=errors,result=str(result)))
ns['eventmanager']=types.SimpleNamespace(send_event=lambda kind,data:types.SimpleNamespace(event_data=data))
for text,base in [('',None),('sub/../Owned.mkv',None),('/absolute.mkv','/base')]:
 cases.append(dict(name='path-'+str(len(cases)),template=text,variables={},base=base,source=None,patches={},fail={},result=str(ns['get_rename_path'](text,{},path=None if base is None else pathlib.Path(base)))))
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',python=sys.version,kind='actual fixed caller/Pydantic models/Jinja2; callback registry is counted mutation-safe boundary, not full core execution',source_sha256={p:hashlib.sha256(s.encode()).hexdigest()for p,s in raw.items()},cases=cases)
pathlib.Path(__file__).with_name('transfer-rename-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print('fixed rename path cases',len(cases))
