"""Replay the pinned ordinary DownloadChain existence decision and DTO schema.
Controlled media-server lookup and provider result; no light-media code is used.
"""
import ast,copy,json,subprocess,hashlib
from pathlib import Path
from types import SimpleNamespace as NS
from typing import Optional,Dict,Union,List
from enum import Enum
from pydantic import BaseModel,Field
FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60'
def read(path):return subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',FIXED+':'+path],text=True)
texts={p:read(p)for p in ['app/chain/download.py','app/chain/__init__.py','app/schemas/mediaserver.py','app/schemas/types.py']}
ns=dict(Optional=Optional,Dict=Dict,Union=Union,List=List,Enum=Enum,BaseModel=BaseModel,Field=Field)
for path,names in [('app/schemas/types.py',['MediaType']),('app/schemas/mediaserver.py',['ExistMediaInfo','NotExistMediaInfo'])]:
 nodes=[copy.deepcopy(n)for n in ast.parse(texts[path]).body if isinstance(n,ast.ClassDef)and n.name in names]
 exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),path,'exec'),ns)
methods=[]
for path,name in [('app/chain/download.py','get_no_exists_info'),('app/chain/__init__.py','media_exists')]:
 n=next(n for n in ast.walk(ast.parse(texts[path]))if isinstance(n,ast.FunctionDef)and n.name==name);n=copy.deepcopy(n);n.returns=None
 for a in n.args.args:a.annotation=None
 methods.append(n)
cls=ast.ClassDef(name='DownloadChain',bases=[],keywords=[],body=methods,decorator_list=[])
ns.update(resolve_media_identity=lambda **k:('themoviedb',900043),build_media_key=lambda source,id:id,MediaServerOper=lambda:NS(get_item_id=lambda **kw:None),logger=NS(info=lambda *a:None,debug=lambda *a:None,error=lambda *a:None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'<fixed ordinary existence>','exec'),ns)
cases=[]
for name,kind,result,totals,season in [
 ('movie-none','movie',None,{},1),('movie-false','movie',False,{},1),('movie-present','movie',{'type':'电影'}, {},1),
 ('tv-none','tv',None,{},1),('tv-false','tv',False,{},1),('tv-partial','tv',{'seasons':{'1':[1,3]}},{},1),('tv-complete','tv',{'seasons':{'1':[1,2,3]}},{},1),
 ('tv-empty-season','tv',{'seasons':{'1':[]}},{},1),('tv-other-season','tv',{'seasons':{'2':[1,2,3]}},{},1),('tv-custom-total','tv',{'seasons':{'1':[1,3]}},{1:5},1),
 ('tv-numeric-equality','tv',{'seasons':{'1':[True,2.0,'3']}},{},1),('tv-null-seasons','tv',{'seasons':None},{},1),('tv-season-zero','tv',{'seasons':{'0':[1,3]}},{},0),
]:
 mt=ns['MediaType'].MOVIE if kind=='movie'else ns['MediaType'].TV
 exists=ns['ExistMediaInfo'](**result)if isinstance(result,dict)else result
 chain=ns['DownloadChain']();calls=[]
 def run(method,**kwargs):
  calls.append(dict(method=method,itemid=kwargs['itemid'],server=kwargs['server'],same_media=kwargs['mediainfo']is media));return exists
 chain.run_module=run
 media=NS(type=mt,tmdb_id=900043,title='Owned existence',title_year='Owned existence (2024)',seasons={season:[1,2,3]},season=season)
 meta=NS(sea='S%02d'%season,season_list=[season])
 try:
  satisfied,missing=chain.get_no_exists_info(meta,media,totals=totals)
  missing={str(k):{str(s):v.model_dump(mode='json')for s,v in seasons.items()}for k,seasons in missing.items()}
  error=None
 except Exception as e:satisfied,missing,error=None,None,type(e).__name__
 cases.append(dict(name=name,kind=kind,result=result,totals=totals,season=season,satisfied=satisfied,missing=missing,error=error,calls=calls))
out={'fixed_backend':FIXED,'source':{p:hashlib.sha256(s.encode()).hexdigest()for p,s in texts.items()},'boundary':__doc__,'cases':cases}
Path(__file__).with_name('media-exists-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed existence cases')
