"""Validate actual fixed catalog DTO classes using source's allowed Pydantic.
This is source-model replay, not a target assertion or an HTTP response claim.
"""
import ast,json,hashlib
from pathlib import Path
from typing import Optional,Union,List,Dict,Any
from pydantic import BaseModel,Field
root=Path('/Users/accidia/projects/MoviePilot');ns=dict(BaseModel=BaseModel,Field=Field,Optional=Optional,Union=Union,List=List,Dict=Dict,Any=Any);sources={}
for relative,names in [('app/schemas/context.py',['MediaPerson']),('app/schemas/tmdb.py',['TmdbSeason','TmdbEpisode'])]:
 p=root/relative;text=p.read_text();sources[relative]=hashlib.sha256(text.encode()).hexdigest();tree=ast.parse(text);nodes=[x for x in tree.body if isinstance(x,ast.ClassDef) and x.name in names];exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<fixed:'+relative+'>','exec'),ns)
inputs=[None,False,0,'',[],{}, {'id':120,'name':'Owned Person','also_known_as':['One'],'source':'themoviedb','profile_path':'https://owned.invalid/person.jpg'}, {'id':'120','name':'Owned','popularity':'0.0'}, {'id':True,'type':True,'gender':False}, {'id':12.0}, {'id':12.5}, {'id':10**400}, {'also_known_as':[1,{'name':'owned'}]}, {'name':123}, {'images':[]}, {'unknown':'ignored'}, {'episode_number':'4','season_number':2,'crew':[{'id':1}]}, {'episode_number':None,'season_number':None}]
cases=[]
for name in ['MediaPerson','TmdbSeason','TmdbEpisode']:
 for value in inputs:
  try:out=ns[name].model_validate(value).model_dump(mode='json');case={'model':name,'input':value,'state':'valid','output':out}
  except Exception as err:case={'model':name,'input':value,'state':'invalid','error_types':[x['type'] for x in err.errors()]}
  cases.append(case)
result={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'actual fixed Pydantic DTO classes, source-model validation only; no old application, no target implementation','pydantic':__import__('pydantic').__version__,'sources':sources,'cases':cases}
Path(__file__).with_name('model-source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed catalog DTO validations')
