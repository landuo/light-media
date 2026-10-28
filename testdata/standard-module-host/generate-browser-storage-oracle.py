"""Pinned StorageChain, ChainBase dispatcher, FileManager list and storage/list caller.
Only module registries, test plugin values and LocalStorage.list responses are
owned. Actual source FileItem/Pydantic, sort and None/list/tuple effects run.
"""
import ast,copy,fnmatch,math,re,json,typing
from pathlib import Path
from types import SimpleNamespace
from pydantic import BaseModel,Field,TypeAdapter
p=Path(__file__).with_name('generate-metadata-module-oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split('cases=[]')[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['ROOT'];ns.update(BaseModel=BaseModel,Field=Field,re=re,math=math,fnmatch=fnmatch,Optional=typing.Optional,List=typing.List,Dict=typing.Dict,Tuple=typing.Tuple,Union=typing.Union,Any=typing.Any,User=typing.Any,Depends=lambda f:None,get_current_active_manage_user=lambda:None)
def one(path,kind,name):return copy.deepcopy(next(n for n in ast.parse((root/path).read_text()).body if isinstance(n,kind)and n.name==name))
def run(nodes,label):exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+nodes,type_ignores=[])),label,'exec'),ns)
run([one('app/schemas/file.py',ast.ClassDef,'FileURI'),one('app/schemas/file.py',ast.ClassDef,'FileItem')],'<fixed:FileItem>');FileItem=ns['FileItem'];ns['FileURI'].model_rebuild(_types_namespace=ns);FileItem.model_rebuild(_types_namespace=ns);ns['schemas']=SimpleNamespace(FileItem=FileItem)
for path,name,base,methods in [('app/chain/storage.py','StorageChain','MediaChain',['list_files']),('app/modules/filemanager/__init__.py','FileManagerModule',None,['list_files'])]:
 owner=one(path,ast.ClassDef,name);run([ast.ClassDef(name=name,bases=[ast.Name(id=base,ctx=ast.Load())]if base else[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in methods],decorator_list=[])],'<fixed:'+name+'>')
owner=one('app/utils/string.py',ast.ClassDef,'StringUtils');natural=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='natural_sort_key'));natural.decorator_list=[];run([natural],'<fixed:natural key>');ns['StringUtils']=SimpleNamespace(natural_sort_key=ns['natural_sort_key'])
endpoint=one('app/api/endpoints/storage.py',ast.FunctionDef,'list_files');endpoint.decorator_list=[];run([endpoint],'<fixed:list endpoint>');call=ns['list_files'];Chain=ns['StorageChain']
base={'storage':'local','type':'dir','path':'/owned/','name':'owned','basename':'owned','modify_time':42};native=[{'storage':'local','type':'file','path':'/owned/File10.mkv','name':'File10.mkv','size':3,'modify_time':10}];plugin=[{'storage':'local','type':'file','path':'/owned/File2.mkv','name':'File2.mkv','size':'12.0','modify_time':'2.0'}];cases=[]
for name,value,fail,core_fail in [('none',None,False,False),('false',False,False,False),('empty_list',[],False,False),('list',plugin,False,False),('tuple',tuple(plugin),False,False),('empty_tuple',(),False,False),('dict',{},False,False),('plugin_throw',None,True,False),('core_throw',None,False,True)]:
 trace=[];errors=[];original=copy.deepcopy(value)
 def models(v):
  if isinstance(v,list):return [FileItem.model_validate(x)for x in v]
  if isinstance(v,tuple):return tuple(FileItem.model_validate(x)for x in v)
  return v
 def callback(fileitem,recursion=False):
  trace.append({'role':'plugin','kwargs':{'fileitem':fileitem.model_dump(mode='json'),'recursion':recursion}})
  if fail:raise RuntimeError('owned plugin failure')
  return models(copy.deepcopy(original))
 core=ns['FileManagerModule']();core._support_storages=['local'];core.get_priority=lambda:0;core.get_name=lambda:'文件整理'
 def obtain(item):
  trace.append({'role':'core','item':item.model_dump(mode='json')})
  if core_fail:raise RuntimeError('owned native listing failure')
  return models(copy.deepcopy(native))
 core._FileManagerModule__get_storage_oper=lambda storage:SimpleNamespace(list=obtain)
 chain=Chain();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'list_files':callback}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=SimpleNamespace(send_event=lambda*a:None);ns['StorageChain']=lambda:chain
 error=None
 try:result=call(FileItem.model_validate(base),sort='name',keyword=None);result=TypeAdapter(list[FileItem]).validate_python(result);out=[x.model_dump(mode='json')for x in result];status=200
 except Exception as e:out=None;status=500;error=type(e).__name__+': '+str(e)
 def encode(v):return {'__plugin_module_tuple':list(v)}if isinstance(v,tuple)else v
 cases.append({'name':name,'plugin':encode(original),'native':native,'fail':fail,'core_fail':core_fail,'calls':trace,'status':status,'output':out,'errors':len(errors),'exception':error})
Path(__file__).with_name('browser-storage-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/storage.py:45','app/chain/__init__.py:470','app/modules/filemanager/__init__.py:220','app/api/endpoints/storage.py:90','app/utils/string.py:1000'],'kind':__doc__,'cases':cases,'natural_keys':{s:ns['natural_sort_key'](s)for s in ['File2.mkv','File10.mkv','file02.mkv','İ2','Σ1Σ','１２.mkv','١٠.mkv']}},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual source browser storage cases')
