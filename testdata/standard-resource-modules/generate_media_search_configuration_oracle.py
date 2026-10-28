"""Actual ordinary Settings normalization and MinIntervalLimiter reservations."""
import ast
import copy
import hashlib
import json
import pathlib
import pydantic
import threading
import time
import types
from typing import Any, Type, Tuple, Union, get_origin, get_args

here=pathlib.Path(__file__).resolve().parent
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
source=(root/'app/core/config.py').read_text();tree=ast.parse(source)
config=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='ConfigModel')
names=['MAX_SEARCH_NAME_LIMIT','SEARCH_MULTIPLE_NAME','SEARCH_KEYWORD_MIN_INTERVAL']
fields=[copy.deepcopy(n) for n in config.body if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id in names]
settings=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='Settings')
methods=[copy.deepcopy(n) for n in settings.body if isinstance(n,ast.FunctionDef) and n.name in ['generic_type_converter','generic_type_validator','update_env_config']]
assert len(fields)==3 and len(methods)==3
diagnostics=[]
ns=dict(BaseModel=pydantic.BaseModel,model_validator=pydantic.model_validator,copy=copy,json=json,Any=Any,Type=Type,Tuple=Tuple,Union=Union,get_origin=get_origin,get_args=get_args,os=types.SimpleNamespace(environ={key:'owned-present' for key in names}),logger=types.SimpleNamespace(error=lambda *a:diagnostics.append('ERROR'),warning=lambda *a:diagnostics.append('WARN'),info=lambda *a:None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Settings',bases=[ast.Name(id='BaseModel',ctx=ast.Load())],keywords=[],body=fields+methods,decorator_list=[])],type_ignores=[])),'fixed/media-settings.py','exec'),ns)
cases=[]
for name,values in [('MAX_SEARCH_NAME_LIMIT',['0','-2',' ２ ','1_0','bad','\x1c2\x1f']),('SEARCH_MULTIPLE_NAME',['YES','t',' off ','0','bad']),('SEARCH_KEYWORD_MIN_INTERVAL',['2','0','-1','-0.0','nan','inf','-inf','+-inf',' ２.５ ','1_0.5','0x1p2','bad','1e1000'])]:
 for raw in values:
  diagnostics.clear();value=ns['Settings'](**{name:raw})
  cases.append(dict(field=name,raw=raw,max_names=str(value.MAX_SEARCH_NAME_LIMIT),multiple=value.SEARCH_MULTIPLE_NAME,interval=str(value.SEARCH_KEYWORD_MIN_INTERVAL),normalized='WARN' in diagnostics,diagnostics=diagnostics.copy()))
limit_source=(root/'app/utils/limit.py').read_text();node=copy.deepcopy(next(n for n in ast.parse(limit_source).body if isinstance(n,ast.ClassDef) and n.name=='MinIntervalLimiter'))
clock=[100.0]
lns=dict(threading=threading,time=types.SimpleNamespace(monotonic=lambda:clock[0],sleep=time.sleep))
exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'fixed/search-keyword-limiter.py','exec'),lns)
reservations=[]
for interval in [2.0,0.0,-1.0,float('nan'),float('inf'),1e100,9e9]:
 limiter=lns['MinIntervalLimiter'](interval);waits=[];error=None
 for tick in [100.0,100.0,102.0]:
  clock[0]=tick
  try:
   wait=limiter.reserve();waits.append(str(wait))
   if wait==float('inf') or wait>9223372036.854776:
    time.sleep(wait) # Actual CPython overflow path; never sleeps a finite delay here.
  except OverflowError as exc:
   error=type(exc).__name__;break
 reservations.append(dict(interval=str(interval),ticks=[100.0,100.0,102.0],waits=waits,error=error))
assert reservations[0]['waits']==['0.0','2.0','2.0']
assert reservations[-1]['waits'][0]=='0.0' and reservations[-1]['error']=='OverflowError'
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',source_sha256={'app/core/config.py':hashlib.sha256(source.encode()).hexdigest(),'app/utils/limit.py':hashlib.sha256(limit_source.encode()).hexdigest()},kind='actual selected ordinary Settings converters/validator under dev Pydantic, fake env-present mapping prevents file writes; actual MinIntervalLimiter.reserve and CPython overflow sleep, controlled monotonic clock',pydantic=pydantic.__version__,cases=cases,reservations=reservations)
(here/'media-search-configuration-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print('actual source media settings',len(cases),'reservation cases',len(reservations))
