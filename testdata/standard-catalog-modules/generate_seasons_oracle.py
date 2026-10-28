"""Actual pinned season wrapper, endpoint response model and native system
module. Provider responses and ordinary plugin callbacks are owned controls.
"""
from pathlib import Path
import ast,asyncio,copy,json,types
from pydantic import BaseModel,Field,TypeAdapter
p=Path(__file__).with_name('generate_images_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split("images={'backdrops'")[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute'];ns.update(BaseModel=BaseModel,Field=Field)
execute([one('app/schemas/tmdb.py',ast.ClassDef,'TmdbSeason')],'<fixed:season DTO>');ns['schemas'].TmdbSeason=ns['TmdbSeason']
for path,source,target,bases in [('app/chain/tmdb.py','TmdbChain','SeasonChain',[ast.Name(id='ChainBase',ctx=ast.Load())]),('app/modules/themoviedb/__init__.py','TheMovieDbModule','NativeSeasons',[])]:
 owner=one(path,ast.ClassDef,source);execute([ast.ClassDef(name=target,bases=bases,keywords=[],body=[n for n in owner.body if isinstance(n,ast.AsyncFunctionDef)and n.name=='async_tmdb_seasons'],decorator_list=[])],'<fixed:'+target+'>')
fn=one('app/api/endpoints/tmdb.py',ast.AsyncFunctionDef,'tmdb_seasons');fn.decorator_list=[];execute([fn],'<fixed season endpoint>')
season={'season_number':'2','name':'Plugin Season Two','episode_count':'8','extra':'discarded'}
native={'season_number':1,'name':'Native Season One','episode_count':3}
cases=[]
async def main():
 for name,plugin,provider,fail in [('null',None,[native],False),('false',False,[native],False),('list_empty',[],[native],False),('list',[season],[native],False),('tuple',(season,),[native],False),('dict',{},[native],False),('invalid',9,[native],False),('bad_list',[False],[native],False),('native_dto_failure',None,[{'season_number':1,'name':123}],False),('list_native_dto_failure',[season],[{'season_number':1,'name':123}],False),('native_special_and_missing',None,[{}, {'season_number':None},{'season_number':0,'name':'Specials','overview':''},native],False),('throw',None,[native],True)]:
  calls=[];errors=[];encoded=copy.deepcopy({'__plugin_module_tuple':list(plugin)}if isinstance(plugin,tuple)else plugin)
  async def callback(*a,**kw):
   calls.append({'kind':'source','kwargs':kw})
   if fail:raise RuntimeError('owned season failure')
   return plugin
  async def clone(*a,**kw):calls.append({'kind':'clone','kwargs':kw});return None
  async def obtain(tmdbid,mtype):calls.append({'kind':'native','kwargs':{'tmdbid':tmdbid}});return {'seasons':copy.deepcopy(provider)}
  core=ns['NativeSeasons']();core.tmdb=types.SimpleNamespace(async_get_info=obtain);core.get_priority=lambda:0;core.get_name=lambda:'TheMovieDb'
  chain=ns['SeasonChain']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('source','Source'):{'async_tmdb_seasons':callback},('clone','Clone'):{'async_tmdb_seasons':clone}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None);ns['TmdbChain']=lambda:chain
  try:out=await ns['tmdb_seasons'](tmdbid=120);out=TypeAdapter(list[ns['TmdbSeason']]).validate_python(out);output=[item.model_dump(mode='json')for item in out];status=200
  except Exception:output=None;status=500
  cases.append({'name':name,'plugin':encoded,'native':provider,'fail':fail,'calls':calls,'status':status,'error_count':len(errors),'output':output})
asyncio.run(main())
import hashlib,subprocess
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';paths=['app/chain/__init__.py','app/chain/tmdb.py','app/modules/themoviedb/__init__.py','app/api/endpoints/tmdb.py','app/schemas/tmdb.py'];hashes={}
for p in paths:
 raw=subprocess.check_output(['git','show',fixed+':'+p],cwd=root);assert raw==(root/p).read_bytes();hashes[p]=hashlib.sha256(raw).hexdigest()
Path(__file__).with_name('seasons-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'boundary':__doc__,'source_sha256':hashes,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed seasons cases')
