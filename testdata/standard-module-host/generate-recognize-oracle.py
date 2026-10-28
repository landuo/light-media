"""Pinned ChainBase methods. Only provider outputs/registries are owned boundaries."""
import ast,asyncio,contextlib,copy,hashlib,inspect,json,pathlib,traceback,types
from enum import Enum
from typing import *
root=pathlib.Path('/Users/accidia/projects/MoviePilot');chain_path=root/'app/chain/__init__.py';raw=chain_path.read_text();tree=ast.parse(raw);owner=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='ChainBase')
names=['__is_valid_empty','__handle_plugin_error','__handle_system_error','__handle_rate_limit_error','__execute_plugin_modules','__execute_system_modules','run_module','_resolve_media_source_params','_can_use_media_recognize_share','recognize_media','__async_execute_plugin_modules','__async_execute_system_modules','async_run_module','async_recognize_media']
nodes=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names];assert len(nodes)==len(names)
class MediaType(Enum):MOVIE='电影';TV='电视剧'
class RateLimitExceededException(Exception):pass
object_path=root/'app/utils/object.py';object_tree=ast.parse(object_path.read_text());check=next(n for n in ast.walk(object_tree) if isinstance(n,ast.FunctionDef) and n.name=='check_signature');check.decorator_list=[]
object_ns=dict(inspect=inspect,get_type_hints=get_type_hints,FunctionType=types.FunctionType);exec(compile(ast.fix_missing_locations(ast.Module(body=[check],type_ignores=[])),'fixed/object.py','exec'),object_ns)
media_path=root/'app/utils/media.py';media_tree=ast.parse(media_path.read_text());normal=next(n for n in media_tree.body if isinstance(n,ast.FunctionDef) and n.name=='normalize_media_source');alias=next(n for n in media_tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='MEDIA_SOURCE_ALIASES' for t in n.targets))
media_ns=dict(Optional=Optional);exec(compile(ast.Module(body=[alias,normal],type_ignores=[]),'fixed/media.py','exec'),media_ns)
logger=types.SimpleNamespace(info=lambda *a:None,debug=lambda *a:None,error=lambda *a:None)
reports=[]
async def run_in_threadpool(fn,*args,**kwargs):return fn(*args,**kwargs)
class AsyncFresh:
 async def __aenter__(self):return self
 async def __aexit__(self,*args):pass
async def async_report(**kwargs):reports.append('report')
ns=dict(inspect=inspect,run_in_threadpool=run_in_threadpool,async_fresh=lambda *a:AsyncFresh(),logger=logger,traceback=traceback,RateLimitExceededException=RateLimitExceededException,normalize_media_source=media_ns['normalize_media_source'],fresh=lambda *a:contextlib.nullcontext(),MediaType=MediaType,ObjectUtils=types.SimpleNamespace(check_signature=object_ns['check_signature']),MoviePilotServerHelper=types.SimpleNamespace(report_recognize_share=lambda **kw:reports.append('report'),async_report_recognize_share=async_report),EventType=types.SimpleNamespace(SystemError='system.error'),settings=types.SimpleNamespace(MEDIA_RECOGNIZE_SHARE=False))
module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[]);exec(compile(ast.fix_missing_locations(module),str(chain_path),'exec'),ns);Chain=ns['ChainBase']
model_fields={'source':'themoviedb','tmdb_id':900043,'type':'电影','title':'owned override recognition','year':'2026','recognize_cache_hit':True,'tmdb_info':{'id':900043,'title':'owned TMDB projection','release_date':'2026-01-01','poster_path':'/owned.jpg','overview':'owned provider'},'original_title':'owned original','overview':'owned override overview'}
class Model(types.SimpleNamespace):pass
class Core:
 def get_priority(self):return 0
 def get_name(self):return 'owned core'
 def recognize_media(self,meta=None,mtype=None,tmdbid=None,episode_group=None,cache=True,**kwargs):
  observations.append(dict(kind='core',method='recognize_media',args=[],kwargs=dict(meta=meta,mtype=mtype,tmdbid=tmdbid,episode_group=episode_group,cache=cache,**kwargs)))
  return Model(**dict(model_fields,title='owned core recognition'))
 async def async_recognize_media(self,meta=None,mtype=None,tmdbid=None,episode_group=None,cache=True,**kwargs):
  observations.append(dict(kind='core',method='async_recognize_media',args=[],kwargs=dict(meta=meta,mtype=mtype,tmdbid=tmdbid,episode_group=episode_group,cache=cache,**kwargs)))
  return Model(**dict(model_fields,title='owned core recognition'))
def clean(v):
 if isinstance(v,Enum):return v.value
 if isinstance(v,types.SimpleNamespace):return {k:clean(x) for k,x in vars(v).items()}
 if isinstance(v,dict):return {k:clean(x) for k,x in v.items()}
 if isinstance(v,(list,tuple)):return [clean(x) for x in v]
 return v
cases=[]
for label,value in [('null',None),('false',False),('zero',0),('empty_dict',{}),('model',Model(**model_fields)),('model_without_provider',Model(**{k:v for k,v in model_fields.items() if k!='tmdb_info'})),('empty_list',[]),('list_model',[Model(**model_fields)]),('all_none_tuple',(None,None)),('failure','owned_raise')]:
 observations=[];errors=[];reports=[]
 def plugin(*args,**kwargs):
  observations.append(dict(kind='plugin',method='recognize_media',args=args,kwargs=kwargs))
  if label=='failure':raise RuntimeError('owned failure')
  return copy.deepcopy(value)
 c=Chain();c.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','owned plugin'):{'recognize_media':plugin}});c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[Core()]);c.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw['role']));c.eventmanager=types.SimpleNamespace(send_event=lambda *args:None)
 try:result=c.recognize_media(mtype=MediaType.MOVIE,tmdbid=900043);error=None
 except Exception as exc:result=None;error=type(exc).__name__
 cases.append(dict(name=label,plugin_result=clean(value) if label!='failure' else None,observations=clean(observations),returned=clean(result),error=error,error_roles=errors))
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',source_sha256={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [chain_path,object_path,media_path]},kind='actual pinned ChainBase ID normalization/dispatcher/recognition methods and ObjectUtils signature check; synthetic plugin/core provider model outputs only',cases=cases)
pathlib.Path(__file__).with_name('recognize-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print('recognize source cases',len(cases))

async_cases=[]
for sample in cases:
 label=sample['name'];value={'null':None,'false':False,'zero':0,'empty_dict':{},'model':Model(**model_fields),'model_without_provider':Model(**{k:v for k,v in model_fields.items() if k!='tmdb_info'}),'empty_list':[],'list_model':[Model(**model_fields)],'all_none_tuple':(None,None),'failure':'owned_raise'}[label]
 observations=[];errors=[];reports=[]
 async def plugin(*args,**kwargs):
  observations.append(dict(kind='plugin',method='async_recognize_media',args=args,kwargs=kwargs))
  if label=='failure':raise RuntimeError('owned failure')
  return copy.deepcopy(value)
 c=Chain();c.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','owned plugin'):{'async_recognize_media':plugin}});c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[Core()]);c.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw['role']));c.eventmanager=types.SimpleNamespace(send_event=lambda *args:None)
 try:result=asyncio.run(c.async_recognize_media(mtype=MediaType.MOVIE,source='themoviedb',mediaid='900043',tmdbid=900043,cache=False));error=None
 except Exception as exc:result=None;error=type(exc).__name__
 async_cases.append(dict(name=label,plugin_result=sample['plugin_result'],observations=clean(observations),returned=clean(result),error=error,error_roles=errors))
# Actual async_add prefix through its first recognition request. MetaInfo parsing
# is an owned boundary here; the source prefix itself sets year/type/season.
subscribe_path=root/'app/chain/subscribe.py';subscribe_tree=ast.parse(subscribe_path.read_text());add=next(n for n in ast.walk(subscribe_tree) if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_add');prefix=[]
for node in add.body:
 prefix.append(copy.deepcopy(node))
 if isinstance(node,ast.If) and isinstance(node.test,ast.Call) and isinstance(node.test.func,ast.Name) and node.test.func.id=='any':break
else:raise AssertionError('fixed async_add recognition prefix not located')
add=copy.deepcopy(add);add.body=prefix;add.decorator_list=[]
media_tree=ast.parse(media_path.read_text());resolve=next(n for n in media_tree.body if isinstance(n,ast.FunctionDef) and n.name=='resolve_media_identity');id_fields=next(n for n in media_tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='MEDIA_SOURCE_ID_FIELDS' for t in n.targets));resolve_ns=dict(Optional=Optional,Union=Union,normalize_media_source=media_ns['normalize_media_source']);exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),id_fields,resolve],type_ignores=[])),'fixed/media.py','exec'),resolve_ns)
caller=[]
async def capture(**kwargs):caller.append(kwargs);return None
caller_ns=dict(MetaInfo=lambda title:Model(org_string=title),MediaType=MediaType,logger=logger,resolve_media_identity=resolve_ns['resolve_media_identity'])
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),add],type_ignores=[])),'fixed/subscribe.py','exec'),caller_ns)
asyncio.run(caller_ns['async_add'](types.SimpleNamespace(async_recognize_media=capture),title='Owned async source',year='2026',mtype=MediaType.TV,tmdbid=900043,season=2));assert len(caller)==1
async_out=dict(out,cases=async_cases,caller_prefix={'source':'app/chain/subscribe.py:1076-1133','meta_parser_boundary':'owned MetaInfo initial org_string only; source statements add year/type/season; existing native full MetaInfo separately validated','recognize_method':'async_recognize_media','kwargs':clean(caller[0])});async_out['source_sha256']['app/chain/subscribe.py']=hashlib.sha256(subscribe_path.read_bytes()).hexdigest();async_out['kind']='actual fixed async ChainBase dispatcher/recognition plus async_add prefix; owned model/provider/MetaInfo parser outputs, no old runtime/service'
pathlib.Path(__file__).with_name('async-recognize-source-oracle.json').write_text(json.dumps(async_out,ensure_ascii=False,indent=2)+'\n');print('async recognize source cases',len(async_cases),'actual async_add prefix',len(caller))
# Source common build_subscribe_meta + _subscribe_recognize_kwargs and actual
# synchronous search recognition call. Only MetaInfo parser/provider are owned.
helpers=[copy.deepcopy(n) for n in subscribe_tree.body if isinstance(n,ast.FunctionDef) and n.name in ['build_subscribe_meta','_subscribe_recognize_kwargs']];assert len(helpers)==2
sync_ns=dict(MetaInfo=lambda title:Model(org_string=title),MediaType=MediaType,resolve_media_identity=resolve_ns['resolve_media_identity'])
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*helpers],type_ignores=[])),'fixed/subscribe.py','exec'),sync_ns)
record=dict(name='Owned sync source S03',year=None,season=2,type='电视剧',tmdbid=900043,doubanid='owned-legacy-id',bangumiid=None,anilistid=None,media_source='themoviedb',media_id='900043',episode_group='owned-episode-group',best_version=False,username=None)
observations=[]
def capture_sync(*args,**kwargs):
 observations.append(dict(kind='plugin',method='recognize_media',args=args,kwargs=kwargs));return Model(**dict(model_fields,type='电视剧'))
c=Chain();c.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','owned plugin'):{'recognize_media':capture_sync}});c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[])
sync_ns.update(self=c,subscribe=Model(**record));sync_ns['meta']=sync_ns['build_subscribe_meta'](sync_ns['subscribe'])
request=next(n for n in ast.walk(subscribe_tree) if isinstance(n,ast.AnnAssign) and n.lineno==1378);exec(compile(ast.fix_missing_locations(ast.Module(body=[copy.deepcopy(request)],type_ignores=[])),'fixed/subscribe.py','exec'),sync_ns);assert len(observations)==1
sync_prefix=dict(source=['app/chain/subscribe.py:54-92','app/chain/subscribe.py:1378-1384'],record=record,kwargs=clean(observations[0]['kwargs']),method='recognize_media',meta_parser_boundary='owned MetaInfo initial org_string only; source build_subscribe_meta assignments + exact recognition call and ChainBase normalize are executed')
for path in ['recognize-source-oracle.json','async-recognize-source-oracle.json']:
 p=pathlib.Path(__file__).with_name(path);d=json.loads(p.read_text());d['sync_subscription_caller']=sync_prefix;p.write_text(json.dumps(d,ensure_ascii=False,indent=2)+'\n')
print('actual synchronous subscription caller',len(observations))
