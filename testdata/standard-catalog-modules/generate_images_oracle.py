"""Actual fixed detail caller/outer dispatcher/MediaInfo/image system module.
Only provider response, registries and installed test-plugin callbacks are owned
controls; MediaInfo identity/mutation, source control flow and image choice run.
"""
import ast,asyncio,copy,hashlib,inspect,json,re,sys,traceback,types,unicodedata
from dataclasses import dataclass,field
from datetime import datetime
from enum import Enum
from pathlib import Path
from typing import *
root=Path('/Users/accidia/projects/MoviePilot')
paths=['app/core/context.py','app/chain/__init__.py','app/api/endpoints/media.py','app/modules/themoviedb/__init__.py','app/modules/themoviedb/tmdbapi.py','app/schemas/types.py','app/utils/media.py','app/utils/object.py']
logger=types.SimpleNamespace(**{name:lambda *a,**kw:None for name in ['info','debug','warning','warn','error']})
class RateLimitExceededException(Exception):pass
ns=dict(Any=Any,Optional=Optional,List=List,Dict=Dict,Tuple=Tuple,Set=Set,Union=Union,Enum=Enum,dataclass=dataclass,field=field,datetime=datetime,MetaBase=Any,MetaInfo=Any,logger=logger,inspect=inspect,traceback=traceback,re=re,RateLimitExceededException=RateLimitExceededException,settings=types.SimpleNamespace(RECOGNIZE_SOURCE='themoviedb',TMDB_LOCALE='zh',TMDB_IMAGE_URL=lambda value:'https://image.tmdb.org/t/p/original'+value),EventType=types.SimpleNamespace(SystemError='system.error'))
def one(path,kind,name):return copy.deepcopy(next(n for n in ast.parse((root/path).read_text()).body if isinstance(n,kind) and n.name==name))
def execute(nodes,label):exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),label,'exec'),ns)
execute([one(paths[5],ast.ClassDef,'MediaType')],'<fixed:MediaType>')
execute([one(paths[0],ast.ClassDef,'MediaInfo')],'<fixed:MediaInfo>')
media=ns['MediaInfo'];mtype=ns['MediaType']
raw=(root/paths[7]).read_text();fn=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='check_signature'));fn.decorator_list=[]
objns={'inspect':inspect,'get_type_hints':get_type_hints,'FunctionType':types.FunctionType};exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<fixed:ObjectUtils>','exec'),objns);ns['ObjectUtils']=types.SimpleNamespace(check_signature=objns['check_signature'])
async def run_in_threadpool(fn,*args,**kwargs):return fn(*args,**kwargs)
ns['run_in_threadpool']=run_in_threadpool
owner=one(paths[1],ast.ClassDef,'ChainBase');wanted=['__is_valid_empty','__handle_plugin_error','__handle_system_error','__handle_rate_limit_error','__async_execute_plugin_modules','__async_execute_system_modules','async_run_module','async_obtain_images']
execute([ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in wanted],decorator_list=[])],'<fixed:image outer dispatcher/wrapper>')
owner=one(paths[3],ast.ClassDef,'TheMovieDbModule');wanted=['_validate_obtain_images_params','_pick_best_tmdb_image','_process_tmdb_images','async_obtain_images']
execute([ast.ClassDef(name='NativeImages',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in wanted],decorator_list=[])],'<fixed:TMDB image module>')
utils=ast.parse((root/paths[6]).read_text());execute([n for n in utils.body if isinstance(n,ast.Assign) or isinstance(n,ast.FunctionDef) and n.name in ['normalize_media_source','parse_media_key']],'<fixed:media key parse>')
ns.update(Depends=lambda f:None,verify_token=lambda:None,schemas=types.SimpleNamespace(TokenPayload=Any,MediaInfo=lambda:{}))
fn=one(paths[2],ast.AsyncFunctionDef,'detail');fn.decorator_list=[];execute([fn],'<fixed:detail caller ignores image return>')
images={'backdrops':[{'file_path':'/backdrop.jpg','vote_average':8,'vote_count':4}],'logos':[{'file_path':'/logo-first.svg','vote_average':9,'vote_count':1},{'file_path':'/logo-best.svg','vote_average':9,'vote_count':3}],'posters':[{'file_path':'/poster-first.jpg','vote_average':8,'vote_count':2},{'file_path':'/poster-tie.jpg','vote_average':8,'vote_count':2}]}
recipes=[('native',[]),('mutate_none',['none']),('mutate_false',['false']),('mutate_other_model',['other']),('mutate_raise',['raise']),('mutate_rate',['rate']),('mutate_list',['list']),('mutate_dict',['dict']),('two_live_plugins',['none','second']),('already_complete',[]),('without_tmdb_id',[]),('highest_image_missing_path',[])]
cases=[]
def clean(value):
 if isinstance(value,Enum):return value.value
 if isinstance(value,dict):return {k:clean(v) for k,v in value.items()}
 if isinstance(value,(list,tuple)):return [clean(v) for v in value]
 if isinstance(value,media):return {'returned_media':value.to_dict()}
 return value
async def main():
 for name,handlers in recipes:
  model=media();model.from_dict({'source':'themoviedb','tmdb_id':120,'type':'电影','title':'Owned source','year':'2026','original_language':'ja','tmdb_info':{'owned_private':1}})
  if name=='already_complete':model.poster_path='old-poster';model.logo_path='old-logo';model.backdrop_path='old-backdrop'
  if name=='without_tmdb_id':model.tmdb_id=None
  native_images=copy.deepcopy(images)
  if name=='highest_image_missing_path':native_images['posters']=[{'file_path':None,'vote_average':10,'vote_count':9},{'file_path':'/must-not-skip.jpg','vote_average':9,'vote_count':9}]
  before=clean(copy.deepcopy(vars(model)));calls=[];errors=[];plugin_groups={}
  def make(mode,index):
   async def plugin(*args,**kwargs):
    item=kwargs['mediainfo'];calls.append({'kind':'plugin','mode':mode,'before':clean(copy.deepcopy(vars(item)))})
    if mode=='second':item.backdrop_path='plugin-backdrop';return None
    item.poster_path='plugin-poster'
    if mode=='none':return None
    if mode=='false':return False
    if mode=='dict':return {}
    if mode=='list':return ['ignored-list-item']
    if mode=='other':other=media();other.poster_path='different-return-object';return other
    if mode=='raise':raise RuntimeError('owned image handler failure')
    if mode=='rate':raise RateLimitExceededException('owned image rate limit')
   return plugin
  for index,mode in enumerate(handlers):plugin_groups[('Owned'+str(index),'Owned'+str(index))]={'async_obtain_images':make(mode,index)}
  async def provider(tmdbid,original_language=None):calls.append({'kind':'provider','tmdbid':tmdbid,'original_language':original_language});return copy.deepcopy(native_images)
  core=ns['NativeImages']();core.tmdb=types.SimpleNamespace(async_get_movie_images=provider,async_get_tv_images=provider);core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDb'
  chain=ns['ChainBase']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:plugin_groups);chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(clean(kw)));chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None)
  async def recognize(**kwargs):calls.append({'kind':'recognize','kwargs':clean(kwargs)});return model
  chain.async_recognize_media=recognize;ns['MediaChain']=lambda:chain
  result=await ns['detail'](mediaid='tmdb:120',type_name='电影')
  cases.append({'name':name,'handlers':handlers,'input':before,'images':native_images,'calls':calls,'output':clean(result),'live_after':clean(copy.deepcopy(vars(model))),'error_count':len(errors)})
asyncio.run(main())
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','runtime':{'python':sys.version,'unicode':unicodedata.unidata_version},'kind':'actual fixed detail caller/async outer dispatcher/MediaInfo model/image system methods; provider/registries/ordinary plugin callbacks controlled','sources':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths},'cases':cases}
Path(__file__).with_name('images-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual image mutation/caller/system cases')
