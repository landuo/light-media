"""Run fixed ordinary catalog Chain wrappers, with controlled module boundary.
No original app/config/provider/network is imported. No target implementation is
used to produce expected module names, kwargs or falsey/list transformations.
"""
import ast, asyncio, copy, hashlib, json
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Optional, List, Dict, Tuple
ROOT=Path('/Users/accidia/projects/MoviePilot')
class Schema(SimpleNamespace):
 def __getattr__(self,k): return Any
class Media:
 def __init__(self,n): self.n=n
 def to_dict(self): return {'owned':self.n}
class Boundary:
 calls=[]; returned=None
 def run_module(self,module_method,*args,**kwargs):
  Boundary.calls.append({'method':module_method,'args':list(args),'kwargs':copy.deepcopy(kwargs)})
  return Boundary.returned
 async def async_run_module(self,module_method,*args,**kwargs): return self.run_module(module_method,*args,**kwargs)
ns=dict(Any=Any,Optional=Optional,List=List,Dict=Dict,Tuple=Tuple,MediaInfo=Media,MetaBase=dict,CommingMessage=dict,WebhookEventInfo=dict,MediaType=SimpleNamespace(MOVIE='电影',TV='电视剧'),schemas=Schema(),ChainBase=Boundary)
sources={};classes={}
select={
 'app/chain/__init__.py':('FixedBase',['search_medias','async_search_medias','search_persons','async_search_persons','tmdb_info','async_tmdb_info','bangumi_info','async_bangumi_info']),
 'app/chain/tmdb.py':('TmdbChain',['tmdb_discover','async_tmdb_discover','tmdb_trending','async_tmdb_trending','tmdb_seasons','async_tmdb_seasons','tmdb_episodes','async_tmdb_episodes','person_detail','async_person_detail','person_credits','async_person_credits']),
 'app/chain/bangumi.py':('BangumiChain',['calendar','async_calendar','discover','async_discover','bangumi_info','async_bangumi_info']),
 'app/chain/douban.py':('DoubanChain',['douban_discover','async_douban_discover','movie_showing','async_movie_showing','movie_top250','async_movie_top250','tv_weekly_chinese','async_tv_weekly_chinese','tv_weekly_global','async_tv_weekly_global','tv_animation','async_tv_animation','movie_hot','async_movie_hot','tv_hot','async_tv_hot']),
 'app/chain/anilist.py':('AniListChain',['info','async_info','trending','async_trending','popular_this_season','async_popular_this_season','discover','async_discover']),
 'app/chain/recommend.py':('RecommendChain',['async_tmdb_movies','async_tmdb_tvs','async_tmdb_trending','async_bangumi_calendar','async_douban_movie_showing','async_douban_movies','async_douban_tvs','async_douban_movie_top250','async_douban_tv_weekly_chinese','async_douban_tv_weekly_global','async_douban_tv_animation','async_douban_movie_hot','async_douban_tv_hot'])}
for file,(name,names) in select.items():
 text=(ROOT/file).read_text();sources[file]=hashlib.sha256(text.encode()).hexdigest();tree=ast.parse(text);klass=next(n for n in tree.body if isinstance(n,ast.ClassDef));functions=[copy.deepcopy(n) for n in klass.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names]
 if {n.name for n in functions}!=set(names): raise RuntimeError((file,set(names)-{n.name for n in functions}))
 for f in functions:f.decorator_list=[]
 compiled=ast.ClassDef(name=name,bases=[ast.Name(id='ChainBase',ctx=ast.Load())],keywords=[],body=functions,decorator_list=[])
 exec(compile(ast.fix_missing_locations(ast.Module(body=[compiled],type_ignores=[])),'<fixed:'+file+'>','exec'),ns);classes[name]=ns[name]
cases=[]
async def run(name,method,kwargs,returned,label=None):
 Boundary.calls=[];Boundary.returned=returned
 result=getattr(classes[name](),method)(**kwargs)
 if hasattr(result,'__await__'): result=await result
 def encode(x):
  if isinstance(x,Media):return x.to_dict()
  if isinstance(x,list):return [encode(v) for v in x]
  return x
 cases.append({'name':label or name+'.'+method,'class':name,'wrapper':method,'input':kwargs,'returned':encode(returned),'calls':Boundary.calls,'output':encode(result)})
async def main():
 for prefix in ['', 'async_']:
  for method,kwargs in [('search_medias',{'meta':{'name':'Owned','type':'电视剧','begin_season':2,'year':'2026'},'source':None}),('search_persons',{'name':'Owned','source':None}),('tmdb_info',{'tmdbid':110,'mtype':'电视剧','season':2})]:
   await run('FixedBase',prefix+method,kwargs,False)
  for method,kwargs in [('tmdb_seasons',{'tmdbid':110}),('tmdb_episodes',{'tmdbid':110,'season':2,'episode_group':None}),('person_detail',{'person_id':120}),('person_credits',{'person_id':120,'page':3}),('tmdb_trending',{'page':2})]:
   await run('TmdbChain',prefix+method,kwargs,[Media(1)])
  await run('TmdbChain',prefix+'tmdb_discover',{'mtype':'电影','sort_by':'popularity.desc','with_genres':'','with_original_language':'','with_keywords':'','with_watch_providers':'','vote_average':0.0,'vote_count':0,'release_date':'','page':2},[])
  await run('BangumiChain',prefix+'discover',{'type':2,'cat':None,'sort':'rank','year':None,'limit':30,'offset':30},[])
  for method in ['movie_showing','movie_top250','tv_weekly_chinese','tv_weekly_global','tv_animation','movie_hot','tv_hot']:
   await run('DoubanChain',prefix+method,{'page':2,'count':30},[])
  await run('DoubanChain',prefix+'douban_discover',{'mtype':'电视剧','sort':'R','tags':'','page':2,'count':30},[])
  for method in ['trending','popular_this_season']:
   await run('AniListChain',prefix+method,{'page':2,'count':20},False)
  await run('AniListChain',prefix+'discover',{'page':2,'count':20,'media_format':'TV','season':None},[])
  await run('AniListChain',prefix+'info',{'anilist_id':200},None)
 for method in select['app/chain/recommend.py'][1]:
  kwargs={'page':2}
  if method=='async_bangumi_calendar':kwargs['count']=2
  await run('RecommendChain',method,kwargs,[Media(i) for i in range(1,7)])
  await run('RecommendChain',method,kwargs,False,method+'-falsey')
asyncio.run(main())
result={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed ordinary Chain wrapper AST; module transport/provider return controlled, no old host/network','sources':sources,'cases':cases}
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed catalog wrapper scenarios')
