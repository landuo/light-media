"""Fixed full module dispatcher and actual Recommend/Discover callers.
Plugin and native provider outputs are controlled, never target-generated.
"""
import runpy,ast,copy,asyncio,json,pathlib,types,hashlib
from enum import Enum
state=runpy.run_path(str(pathlib.Path(__file__).with_name('generate_person_dispatch.py')));ns=state['ns'];root=state['root']
class MediaType(Enum):MOVIE='电影';TV='电视剧'
class Media:
 def __init__(self,title):self.title=title
 def to_dict(self):return {'source':'themoviedb','tmdb_id':120,'type':'电影','title':self.title}
ns.update(MediaType=MediaType,MediaInfo=Media,Media=Media)
owner=next(n for n in ast.parse((root/'app/modules/themoviedb/__init__.py').read_text()).body if isinstance(n,ast.ClassDef) and n.name=='TheMovieDbModule');fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_tmdb_discover'))
# Provider control replaces only this core callable body, preserving its exact
# signature used by fixed ObjectUtils.check_signature and call binding.
fn.body=ast.parse('''observations.append({'kind':'core','kwargs':{'page':page,'raise_exception':raise_exception}})
if native_failure: raise RuntimeError('owned native provider failure')
return [Media('core')]
''').body
klass=ast.ClassDef(name='Core',bases=[],keywords=[],body=[fn],decorator_list=[]);exec(compile(ast.fix_missing_locations(ast.Module(body=[klass],type_ignores=[])),'<controlled core with fixed signature>','exec'),ns)
owner=next(n for n in ast.parse((root/'app/chain/recommend.py').read_text()).body if isinstance(n,ast.ClassDef) and n.name=='RecommendChain');fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_tmdb_movies'));fn.decorator_list=[];exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='RecommendChain',bases=[],keywords=[],body=[fn],decorator_list=[])],type_ignores=[])),'<fixed:recommend wrapper>','exec'),ns)
cases=[]
def clean(v):
 if isinstance(v,Media):return v.to_dict()
 if isinstance(v,MediaType):return v.value
 if isinstance(v,dict):return {k:clean(x) for k,x in v.items()}
 if isinstance(v,(tuple,list)):return [clean(x) for x in v]
 return v
async def main():
 for label,value,native_failure,raise_flag in [('null',None,False,False),('false',False,False,False),('zero',0,False,False),('empty_string','',False,False),('empty_dict',{},False,False),('truthy_dict',{'title':'bad'},False,False),('empty_list',[],False,False),('list',[Media('plugin')],False,False),('native_fail_with_list',[Media('plugin')],True,False),('native_fail_none',None,True,False),('raise_native',[Media('plugin')],True,True),('raise_plugin','plugin_failure',False,True)]:
  ns['observations']=[];ns['native_failure']=native_failure
  async def plugin(*args,**kwargs):
   ns['observations'].append({'kind':'plugin','args':list(args),'kwargs':kwargs})
   if label=='raise_plugin':raise RuntimeError('owned plugin failure')
   return copy.deepcopy(value)
  core=ns['Core']();core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDb'
  chain=ns['ChainBase']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'async_tmdb_discover':plugin}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:None);chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None)
  ns['TmdbChain']=lambda:chain
  try:returned=await ns['RecommendChain']().async_tmdb_movies(page=2,raise_exception=raise_flag);state='success'
  except Exception:returned=None;state='error'
  cases.append({'name':label,'plugin':clean(value) if label!='raise_plugin' else None,'native_failure':native_failure,'raise_exception':raise_flag,'calls':clean(ns['observations']),'state':state,'output':clean(returned)})
asyncio.run(main())
paths=['app/chain/__init__.py','app/chain/recommend.py','app/modules/themoviedb/__init__.py','app/utils/object.py']
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed full dispatcher and Recommend wrapper; provider callback body controlled with its fixed signature','sources':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths},'cases':cases}
pathlib.Path(__file__).with_name('feed-dispatch-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed full feed dispatcher/caller scenarios')
