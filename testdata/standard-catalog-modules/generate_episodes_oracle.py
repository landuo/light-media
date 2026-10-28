"""Actual pinned episode wrapper, endpoint response model and native system
module. Provider responses and ordinary plugin callbacks are owned controls.
"""
from pathlib import Path
import ast,asyncio,copy,json,types
from pydantic import BaseModel,Field,TypeAdapter
p=Path(__file__).with_name('generate_images_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split("images={'backdrops'")[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute'];ns.update(BaseModel=BaseModel,Field=Field)
execute([one('app/schemas/tmdb.py',ast.ClassDef,'TmdbEpisode')],'<fixed:episode DTO>');ns['schemas'].TmdbEpisode=ns['TmdbEpisode']
for path,source,target,bases,methods in [('app/chain/tmdb.py','TmdbChain','EpisodeChain',[ast.Name(id='ChainBase',ctx=ast.Load())],['async_tmdb_episodes']),('app/modules/themoviedb/__init__.py','TheMovieDbModule','NativeEpisodes',[],['async_tmdb_episodes']),('app/modules/themoviedb/tmdbapi.py','TmdbApi','ProviderGroups',[],['async_get_tv_group_seasons','async_get_tv_group_detail'])]:
 owner=one(path,ast.ClassDef,source);execute([ast.ClassDef(name=target,bases=bases,keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in methods],decorator_list=[])],'<fixed:'+target+'>')
fn=one('app/api/endpoints/tmdb.py',ast.AsyncFunctionDef,'tmdb_season_episodes');fn.decorator_list=[];execute([fn],'<fixed:episode endpoint>')
episode={'episode_number':'2','season_number':0,'name':'Owned plugin episode','crew':[{'id':'opaque'}],'unknown_id':120}
native={'episode_number':1,'season_number':0,'name':'Owned native episode'}
cases=[]
async def main():
 for name,plugin,provider in [('null',None,[native]),('false',False,[native]),('zero',0,[native]),('dict',{},[native]),('list_empty',[],[native]),('list',[episode],[native]),('tuple',(episode,),[native]),('tuple_empty',(),[native]),('bad_list',[False],[native]),('native_dto_failure',None,[{'name':123}]),('list_native_dto_failure',[episode],[{'name':123}])]:
  calls=[];errors=[]
  encoded=copy.deepcopy({'__plugin_module_tuple':list(plugin)} if isinstance(plugin,tuple) else plugin)
  async def callback(*a,**kw):calls.append({'kind':'plugin','kwargs':kw});return plugin
  async def obtain(tmdbid,season):calls.append({'kind':'provider','kwargs':{'tmdbid':tmdbid,'season':season}});return {'episodes':copy.deepcopy(provider)}
  core=ns['NativeEpisodes']();core.tmdb=types.SimpleNamespace(async_get_tv_season_detail=obtain);core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDb'
  chain=ns['EpisodeChain']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'async_tmdb_episodes':callback}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None);ns['TmdbChain']=lambda:chain
  try:out=await ns['tmdb_season_episodes'](tmdbid=120,season=0,episode_group=None);out=TypeAdapter(list[ns['TmdbEpisode']]).validate_python(out);output=[item.model_dump(mode='json') for item in out];status=200
  except Exception:output=None;status=500
  cases.append({'name':name,'plugin':encoded,'native':provider,'calls':calls,'status':status,'error_count':len(errors),'output':output})
asyncio.run(main())
group_cases=[]
async def groups():
 raw=[{'order':0,'episodes':[{'episode_number':15,'name':'First'},{'episode_number':3,'name':'Second'}]},{'order':1,'episodes':[]}]
 for season in [0,1,2]:
  async def fetch(group):return copy.deepcopy(raw)
  obj=ns['ProviderGroups']();obj.tv=types.SimpleNamespace(async_group_episodes=fetch)
  group_cases.append({'season':season,'input':raw,'output':await obj.async_get_tv_group_detail('owned-group',season)})
asyncio.run(groups())
dto=[]
for value in [{},{'episode_number':True,'runtime':'12.0','vote_average':False},{'episode_number':'1e2'},{'episode_number':'1_2'},{'episode_number':'١٢'},{'episode_number':12.0},{'episode_number':12.5},{'runtime':'1.00000000000000001'},{'crew':None,'guest_stars':[3]},None]:
 try:output=ns['TmdbEpisode'].model_validate(value).model_dump(mode='json');status=200
 except Exception:output=None;status=500
 dto.append({'input':value,'output':output,'status':status})
Path(__file__).with_name('episodes-source-oracle.json').write_text(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':__doc__,'cases':cases,'group_cases':group_cases,'dto_cases':dto},ensure_ascii=False,indent=2)+'\n');print(len(cases),'pipeline,',len(group_cases),'group,',len(dto),'DTO actual source cases')
