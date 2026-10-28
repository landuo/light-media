"""Actual fixed API->Chain->module calls, including endpoint supplied flags.
No target code or target-owned expectations are used for the resulting kwargs.
"""
import ast,copy,hashlib,json,runpy,asyncio
from pathlib import Path
from typing import *
from types import SimpleNamespace
state=runpy.run_path(str(Path(__file__).with_name('generate_oracle.py')))
root=state['ROOT'];ns=state['ns'];Boundary=state['Boundary'];cases=[];hashes={}
ns.update(Depends=lambda fn:None,verify_token=lambda:None,Query=lambda default=None,**kw:default,PageParam=int,CountParam=int,Awaitable=Awaitable,TMDbException=type('TMDbException',(Exception,),{}),HTTPException=RuntimeError,MediaSource=Any)
select={'recommend':['tmdb_movies','tmdb_tvs','tmdb_trending','bangumi_calendar','douban_showing','douban_movies','douban_tvs','douban_movie_top250','douban_tv_weekly_chinese','douban_tv_weekly_global','douban_tv_animation','douban_movie_hot','douban_tv_hot'],'discover':['tmdb_movies','tmdb_tvs','bangumi','douban_movies','douban_tvs'],'anilist':['anilist_trending','anilist_popular_this_season','anilist_discover']}
async def main():
 for file,names in select.items():
  p=root/f'app/api/endpoints/{file}.py';text=p.read_text();hashes[str(p.relative_to(root))]=hashlib.sha256(text.encode()).hexdigest();tree=ast.parse(text)
  nodes=[copy.deepcopy(n) for n in tree.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and (n.name in names or n.name in ['_require_tmdb_result','_serialize_medias'])]
  for n in nodes:n.decorator_list=[]
  exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<fixed:'+file+'>','exec'),ns)
  for name in names:
   Boundary.calls=[];Boundary.returned=[]
   kwargs={'page':2}
   if name=='anilist_discover':kwargs.update(media_format='TV',season_year=2026)
   await ns[name](**kwargs)
   cases.append({'endpoint':file+'.'+name,'input':kwargs,'calls':copy.deepcopy(Boundary.calls)})
   if file=='recommend' and name=='tmdb_tvs':
    Boundary.calls=[];await ns[name](page=2,with_original_language='zh|en|ja|ko');cases.append({'endpoint':'recommend.tmdb_tvs.source-ui','input':{'page':2,'with_original_language':'zh|en|ja|ko'},'calls':copy.deepcopy(Boundary.calls)})
asyncio.run(main())
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed API functions and actual fixed Chain wrappers; module boundary returns controlled empty list','sources':hashes,'cases':cases}
Path(__file__).with_name('endpoint-calls-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual fixed catalog API -> module callsites')
