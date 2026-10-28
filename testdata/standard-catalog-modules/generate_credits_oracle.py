"""Actual pinned person-credits wrapper/caller + module engine. Controlled
registry callbacks return actual MediaInfo models; provider paging runs as a
second actual TmdbApi method against controlled movie/TV responses.
"""
from pathlib import Path
import ast,asyncio,copy,hashlib,json,types
p=Path(__file__).with_name('generate_images_oracle.py');code=p.read_text();prefix=code[:code.index("images={'backdrops'")];nsx={'__file__':str(p)};exec(compile(prefix,str(p),'exec'),nsx)
ns=nsx['ns'];root=nsx['root'];execute=nsx['execute'];one=nsx['one'];media=ns['MediaInfo']
owner=one('app/chain/tmdb.py',ast.ClassDef,'TmdbChain');fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_person_credits'));execute([ast.ClassDef(name='CreditsChain',bases=[ast.Name(id='ChainBase',ctx=ast.Load())],keywords=[],body=[fn],decorator_list=[])],'<fixed:person credits wrapper>')
fn=one('app/api/endpoints/tmdb.py',ast.AsyncFunctionDef,'tmdb_person_credits');fn.decorator_list=[];execute([fn],'<fixed:person credits endpoint>')
fields=lambda id,title:{'source':'themoviedb','tmdb_id':id,'type':'电影','title':title,'year':'2026','media_id':str(id)}
def model(value):
 item=media();item.from_dict(value);return item
cases=[]
async def main():
 for label,plugin in [('null',None),('false',False),('dict',{}),('empty_list',[]),('list',[fields(121,'plugin')]),('single_model',fields(122,'not iterable')),('integer',9)]:
  calls=[]
  value=[model(v) for v in plugin] if isinstance(plugin,list) else model(plugin) if label=='single_model' else plugin
  async def callback(*args,**kwargs):calls.append({'kind':'plugin','kwargs':kwargs});return value
  async def core_callback(person_id,page=1):calls.append({'kind':'core','kwargs':{'person_id':person_id,'page':page}});return [model(fields(120,'core'))]
  core=types.SimpleNamespace(async_tmdb_person_credits=core_callback,get_priority=lambda:0,get_name=lambda:'TheMovieDb')
  chain=ns['CreditsChain']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'async_tmdb_person_credits':callback}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:None);chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None);ns['TmdbChain']=lambda:chain
  try:out=await ns['tmdb_person_credits'](person_id=120,page=2);status=200
  except Exception as error:out=None;status=500
  cases.append({'name':label,'plugin':plugin,'calls':calls,'status':status,'output':out})
asyncio.run(main())
# Actual API helper date sort, cast-only merge, duplicate preservation and slice.
owner=one('app/modules/themoviedb/tmdbapi.py',ast.ClassDef,'TmdbApi');fn=copy.deepcopy(next(n for n in owner.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_get_person_credits'));execute([ast.ClassDef(name='ProviderCredits',bases=[],keywords=[],body=[fn],decorator_list=[])],'<fixed:person credit native provider algorithm>')
provider_cases=[]
async def provider_main():
 movies=[{'id':i,'title':'Movie'+str(i),'release_date':str(2000+i)+'-01-01'} for i in range(1,31)]
 tvs=[{'id':i,'name':'TV'+str(i),'first_air_date':str(1990+i)+'-01-01'} for i in range(1,31)]
 for page in [1,2,3,4]:
  async def movie(person_id):return {'cast':copy.deepcopy(movies),'crew':[{'id':999,'title':'must-not-enter-cast','release_date':'2099-01-01'}]}
  async def tv(person_id):return {'cast':copy.deepcopy(tvs)}
  obj=ns['ProviderCredits']();obj.person=types.SimpleNamespace(async_movie_credits=movie,async_tv_credits=tv)
  out=await obj.async_get_person_credits(person_id=120,page=page)
  provider_cases.append({'page':page,'movies':movies,'tvs':tvs,'output':out})
asyncio.run(provider_main())
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed credits caller/wrapper/module dispatcher/MediaInfo plus source provider merge/date/paging; registry/provider boundaries controlled','cases':cases,'provider_cases':provider_cases}
Path(__file__).with_name('credits-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'credits pipeline and',len(provider_cases),'provider page cases')
