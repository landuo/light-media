"""Actual fixed TMDB source search branching/provider filters/MediaInfo
constructor. Provider transport and the already parsed MetaBase are controls.
"""
from pathlib import Path
import ast,asyncio,copy,json,types,cn2an
p=Path(__file__).with_name('generate_images_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split("images={'backdrops'")[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute'];ns['cn2an']=cn2an
for path,name,target,methods in [('app/modules/themoviedb/__init__.py','TheMovieDbModule','SearchModule',['async_search_medias','_build_search_medias_result']),('app/modules/themoviedb/tmdbapi.py','TmdbApi','SearchProvider',['async_search_multiis','async_search_movies','async_search_tvs'])]:
 owner=one(path,ast.ClassDef,name);execute([ast.ClassDef(name=target,bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in methods],decorator_list=[])],'<fixed:'+target+'>')
def clean(v):
 if isinstance(v,ns['Enum']):return v.value
 if isinstance(v,dict):return {k:clean(v) for k,v in v.items()}
 if isinstance(v,(tuple,list)):return [clean(x) for x in v]
 return v
movies=[{'id':120,'title':'Owned Movie','original_title':'Original movie','media_type':'movie','release_date':'2026-01-01','genre_ids':[18],'poster_path':'/owned.jpg','vote_average':8.24},{'id':122,'title':'Unrelated','release_date':'2027-01-01'}]
tv=[{'id':121,'name':'Owned TV','original_name':'Original TV','media_type':'tv','first_air_date':'2026-06-01','genre_ids':[16]}]
multi=copy.deepcopy(movies[:1]+tv+[{'id':123,'name':'Person','media_type':'person'}])
cases=[]
async def main():
 for name,kind,year,season,source,search_source in [('multi','未知',None,None,None,'themoviedb'),('mixed_year','未知','2026',None,None,'themoviedb'),('movie','电影','2026',None,'themoviedb','douban'),('tv_season_zero','电视剧',None,0,None,'themoviedb'),('tv_season_two','电视剧',None,2,None,'themoviedb'),('source_skip','电影',None,None,'douban','themoviedb'),('setting_skip','电影',None,None,None,'douban'),('empty_name','未知',None,None,None,'themoviedb')]:
  calls=[]
  async def fetch(which,**kwargs):calls.append({'kind':which,'kwargs':kwargs});return copy.deepcopy({'movie':movies,'tv':tv,'multi':multi}[which])
  provider=ns['SearchProvider']();provider.search=types.SimpleNamespace(async_movies=lambda **kw:fetch('movie',**kw),async_tv_shows=lambda **kw:fetch('tv',**kw),async_multi=lambda **kw:fetch('multi',**kw))
  meta=types.SimpleNamespace(name='' if name=='empty_name' else 'Owned',type=ns['MediaType'](kind),year=year,begin_season=season)
  obj=ns['SearchModule']();obj.tmdb=provider;ns['settings'].SEARCH_SOURCE=search_source
  result=await obj.async_search_medias(meta,source)
  cases.append({'name':name,'meta':{'name':meta.name,'type':kind,'year':year,'begin_season':season},'source':source,'search_source':search_source,'calls':calls,'rows':None if result is None else [clean(v.to_dict()) for v in result],'live':None if result is None else [clean(vars(v)) for v in result]})
asyncio.run(main())
Path(__file__).with_name('search-source-oracle.json').write_text(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':__doc__,'cn2an':cn2an.__version__,'movies':movies,'tv':tv,'multi':multi,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual source search/provider/model cases')
