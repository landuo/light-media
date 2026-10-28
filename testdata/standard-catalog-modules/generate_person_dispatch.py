"""Actual fixed outer module dispatcher + person system module + response DTO.
Provider returns/exceptions are controlled; source control flow is executed.
"""
import ast,copy,json,pathlib,types,hashlib,inspect,traceback,asyncio
from typing import *
from pydantic import BaseModel,Field
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
logger=types.SimpleNamespace(info=lambda *a:None,debug=lambda *a:None,error=lambda *a:None)
class RateLimitExceededException(Exception):pass
ns=dict(Any=Any,Optional=Optional,List=List,Dict=Dict,Union=Union,BaseModel=BaseModel,Field=Field,inspect=inspect,traceback=traceback,logger=logger,RateLimitExceededException=RateLimitExceededException,EventType=types.SimpleNamespace(SystemError='system.error'))
paths=['app/chain/__init__.py','app/chain/tmdb.py','app/utils/object.py','app/modules/themoviedb/__init__.py','app/schemas/context.py']
raw=(root/paths[2]).read_text();f=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='check_signature'));f.decorator_list=[];objectns=dict(inspect=inspect,get_type_hints=get_type_hints,FunctionType=types.FunctionType);exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),'<fixed:ObjectUtils>','exec'),objectns);ns['ObjectUtils']=types.SimpleNamespace(check_signature=objectns['check_signature'])
tree=ast.parse((root/paths[4]).read_text());nodes=[n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='MediaPerson'];exec(compile(ast.Module(body=nodes,type_ignores=[]),'<fixed:personDTO>','exec'),ns);ns['schemas']=types.SimpleNamespace(MediaPerson=ns['MediaPerson'])
async def run_in_threadpool(fn,*args,**kwargs):return fn(*args,**kwargs)
ns['run_in_threadpool']=run_in_threadpool
owner=next(n for n in ast.parse((root/paths[0]).read_text()).body if isinstance(n,ast.ClassDef) and n.name=='ChainBase');names=['__is_valid_empty','__handle_plugin_error','__handle_system_error','__handle_rate_limit_error','__async_execute_plugin_modules','__async_execute_system_modules','async_run_module'];nodes=[copy.deepcopy(n) for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names]
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),'<fixed:outerdispatcher>','exec'),ns)
owner=next(n for n in ast.parse((root/paths[1]).read_text()).body if isinstance(n,ast.ClassDef));f=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_person_detail'));exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='TmdbChain',bases=[ast.Name(id='ChainBase',ctx=ast.Load())],keywords=[],body=[f],decorator_list=[])],type_ignores=[])),'<fixed:personwrapper>','exec'),ns)
owner=next(n for n in ast.parse((root/paths[3]).read_text()).body if isinstance(n,ast.ClassDef) and n.name=='TheMovieDbModule');f=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_tmdb_person_detail'));exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='TheMovieDb',bases=[],keywords=[],body=[f],decorator_list=[])],type_ignores=[])),'<fixed:systemperson>','exec'),ns)
cases=[]
async def main():
 for label,value,provider in [('null',None,{'id':120,'name':'Owned provider'}),('false',False,{'id':120,'name':'Owned provider'}),('zero',0,{'id':120,'name':'Owned provider'}),('integer',9,{'id':9,'name':'wrong called'}),('empty_string','',{}),('empty_dict',{},{}),('empty_list',[],{'id':120,'name':'Owned provider'}),('list',[{'id':121,'name':'Owned list'}],{'id':120,'name':'Owned provider'}),('model',{'id':122,'name':'Owned plugin'},{}),('provider_empty',None,{})]:
  calls=[]
  async def plugin(*args,**kwargs):calls.append({'kind':'plugin','args':list(args),'kwargs':kwargs});return copy.deepcopy(value)
  async def get(**kwargs):calls.append({'kind':'provider','kwargs':kwargs});return copy.deepcopy(provider)
  core=ns['TheMovieDb']();core.tmdb=types.SimpleNamespace(async_get_person_detail=get);core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDB'
  chain=ns['TmdbChain']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'async_tmdb_person_detail':plugin}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:None);chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None)
  returned=await chain.async_person_detail(person_id=120)
  try:out=ns['MediaPerson'].model_validate(returned).model_dump(mode='json');status=200
  except Exception:out=None;status=500
  cases.append({'name':label,'plugin':value,'provider':provider,'calls':calls,'http_status':status,'output':out})
asyncio.run(main())
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual outer async_run_module, exact wrapper and system method, actual Pydantic DTO; provider controlled','sources':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths},'cases':cases}
pathlib.Path(__file__).with_name('person-dispatch-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual person full dispatcher/system/DTO scenarios')
