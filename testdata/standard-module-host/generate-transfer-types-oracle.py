"""Fixed StorageChain dispatcher, endpoint and DTO; controlled provider returns.

The target native provider exposes only its existing Go operations. The original
LocalStorage dictionary is recorded separately so this adaptation stays visible.
"""
import ast, copy, hashlib, json, subprocess
from pathlib import Path
from types import SimpleNamespace as NS
from typing import Optional, Any
from pydantic import BaseModel, Field

FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60'
ROOT=Path('/Users/accidia/projects/MoviePilot')
base=Path(__file__).with_name('generate-metadata-module-oracle.py')
env={'__file__':str(base)}
exec(compile(base.read_text().split('cases=[]')[0],str(base),'exec'),env)
paths=['app/chain/__init__.py','app/chain/storage.py','app/api/endpoints/storage.py','app/schemas/file.py','app/modules/filemanager/storages/local.py']
sources={p:subprocess.check_output(['git','show',FIXED+':'+p],cwd=ROOT) for p in paths}
assert (ROOT/paths[0]).read_bytes()==sources[paths[0]]
owner=next(n for n in ast.parse(sources[paths[1]]).body if isinstance(n,ast.ClassDef) and n.name=='StorageChain')
method=next(copy.deepcopy(n) for n in owner.body if isinstance(n,ast.FunctionDef) and n.name=='support_transtype')
method.returns=None
scope={};exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),paths[1],'exec'),scope)
env['ns']['MediaChain'].support_transtype=scope['support_transtype']
dto=next(copy.deepcopy(n) for n in ast.parse(sources[paths[3]]).body if isinstance(n,ast.ClassDef) and n.name=='StorageTransType')
scope={'BaseModel':BaseModel,'Field':Field,'Optional':Optional}
exec(compile(ast.fix_missing_locations(ast.Module(body=[dto],type_ignores=[])),paths[3],'exec'),scope)
endpoint=next(copy.deepcopy(n) for n in ast.parse(sources[paths[2]]).body if isinstance(n,ast.AsyncFunctionDef) and n.name=='transtype')
endpoint=ast.FunctionDef(name=endpoint.name,args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='name')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=endpoint.body,decorator_list=[])
native={'link':'硬链接','copy':'复制','auto':'自动'}
rows=[]
for label,value,fail in [('native',None,False),('override',{'copy':'插件复制','link':'插件硬链接'},False),('empty_object',{},False),('false',False,False),('empty_list',[],False),('invalid_list',['bad'],False),('empty_tuple',(),False),('invalid_tuple',('bad',),False),('invalid_scalar',9,False),('throw',None,True)]:
 calls=[];messages=[]
 def plugin(*args,**kwargs):
  calls.append(dict(kind='source',kwargs=kwargs))
  if fail:raise RuntimeError('owned transfer type failure')
  return copy.deepcopy(value)
 def clone(*args,**kwargs):calls.append(dict(kind='clone',kwargs=kwargs));return None
 class Core:
  def get_priority(self):return 0
  def get_name(self):return '文件整理'
  def support_transtype(self,storage:str):calls.append(dict(kind='native',kwargs={'storage':storage}));return copy.deepcopy(native)
 obj=env['ns']['MediaChain']();obj.pluginmanager=NS(get_plugin_modules=lambda:{('source','Source'):{'support_transtype':plugin},('clone','Clone'):{'support_transtype':clone}});obj.modulemanager=NS(get_running_modules=lambda name:[Core()]);obj.messagehelper=NS(put=lambda **kw:messages.append(kw));obj.eventmanager=NS(send_event=lambda *a:None)
 endpointenv={'StorageChain':lambda:obj,'schemas':NS(StorageTransType=scope['StorageTransType'])}
 exec(compile(ast.fix_missing_locations(ast.Module(body=[endpoint],type_ignores=[])),paths[2],'exec'),endpointenv)
 try:result=endpointenv['transtype']('local').model_dump();status=200
 except Exception:result=None;status=500
 rows.append(dict(name=label,plugin=env['clean'](value),fail=fail,output=result,status=status,calls=calls,error_count=len(messages)))
# Extract the fixed LocalStorage capability dictionary.
owner=next(n for n in ast.parse(sources[paths[4]]).body if isinstance(n,ast.ClassDef))
original=ast.literal_eval(next(n.value for n in owner.body if isinstance(n,ast.Assign) and any(isinstance(v,ast.Name) and v.id=='transtype' for v in n.targets)))
out={'fixed_backend':FIXED,'source_sha256':{p:hashlib.sha256(v).hexdigest() for p,v in sources.items()},'boundary':__doc__,'native_adaptation':{'original':original,'target':native},'cases':rows}
Path(__file__).with_name('transfer-types-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed transfer-type cases')
