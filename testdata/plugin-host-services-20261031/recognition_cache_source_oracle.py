"""Pinned TmdbCache/FileBackend AST + actual cachetools7.1.4, owned files.

Only clock/auth-free configuration and CacheProxy plumbing are supplied. Both
cache eviction/expiry and pickle persistence run the pinned source algorithm.
"""
import ast
import cachetools
from collections import OrderedDict
import copy
from enum import Enum
import hashlib
import json
from math import ceil
from pathlib import Path
import pickle
import shutil
import subprocess
import tempfile
from threading import RLock
import traceback
from types import SimpleNamespace
from typing import Any,Dict,Optional

SHA="8a83efeacd3e26b82be495e17d50146e31c96a60";REPO="/Users/accidia/projects/MoviePilot";sources={};clock=[1000.0,10.0]
class MediaType(Enum):UNKNOWN="未知";MOVIE="电影";TV="电视剧"
class MemoryTLRUCache(cachetools.TLRUCache):
    def __init__(self,*args,**kwargs):super().__init__(*args,timer=lambda:clock[1],**kwargs)
env=dict(Any=Any,Optional=Optional,Dict=Dict,Path=Path,tempfile=tempfile,shutil=shutil,MemoryTLRUCache=MemoryTLRUCache,DEFAULT_CACHE_REGION="default",pickle=pickle,traceback=traceback,ceil=ceil,time=lambda:clock[0],lock=RLock(),PERSISTENCE_VERSION=1,PERSISTENCE_REGION="recognize",PERSISTENCE_KEY="tmdb",MediaType=MediaType,MetaBase=SimpleNamespace,logger=SimpleNamespace(error=lambda *a:None))
def source(path):
    raw=subprocess.check_output(["git","-C",REPO,"show",SHA+":"+path]);sources[path]=hashlib.sha256(raw).hexdigest();return ast.parse(raw)
def extract(tree,name,methods=None):
    cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==name)
    body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and (methods is None or n.name in methods)]
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name=name,bases=[] if name!="_MemoryTLRUCache" else [ast.Name(id="MemoryTLRUCache",ctx=ast.Load())],keywords=[],body=body,decorator_list=[])],type_ignores=[])),name,"exec"),env);return env[name]
tree=source("app/core/cache.py");memory_type=extract(tree,"_MemoryTLRUCache");file_type=extract(tree,"FileBackend",{"__init__","get","set","delete"})
class TTLCache:
    def __init__(self,region,maxsize,ttl):self.cache=memory_type(maxsize,ttl)
    def get(self,key):return self.cache.get(key)
    def set(self,key,value,ttl=None):self.cache.set(key,value,self.cache.ttl if ttl is None else ttl)
    def items(self):return self.cache.items()
    def is_redis(self):return False
env.update(TTLCache=TTLCache,FileCache=lambda base,ttl:file_type(base))
cache_type=extract(source("app/modules/themoviedb/tmdb_cache.py"),"TmdbCache",{"__init__","_restore","_set","__get_key","get","update","save"})
def meta(name,**extra):return dict(name=name,type="未知",year=None,begin_season=None,tmdbid=None,**extra)
def info(id=123,title="Owned",kind="电影"):return dict(id=id,title=title,name=title,media_type=kind,release_date="2024-01-01",first_air_date="2024-01-01",poster_path=None,backdrop_path=None)
def key(name):return "[未知][zh]"+name+"-None-None"
def wire(value):
    if isinstance(value,Enum):return value.value
    if isinstance(value,dict):return {k:wire(v) for k,v in value.items()}
    if isinstance(value,list):return [wire(v) for v in value]
    return value
def convert_info(value):
    if value is None:return None
    value=copy.deepcopy(value)
    if value.get('media_type'):value['media_type']=MediaType(value['media_type'])
    return value
def convert_meta(value):
    value=copy.deepcopy(value);value['type']=MediaType(value['type']);return SimpleNamespace(**value)
positive=info()
scenarios=[
    dict(case="positive_negative_none",operations=[dict(action="update",meta=meta("Positive"),info=positive),dict(action="update",meta=meta("Negative"),info={}),dict(action="update",meta=meta("Network"),info=None),dict(action="get",meta=meta("Positive")),dict(action="get",meta=meta("Negative")),dict(action="get",meta=meta("Network")),dict(action="save")]),
    dict(case="wall_forward_keeps_live_but_not_persisted",operations=[dict(action="update",meta=meta("Owned"),info=positive),dict(action="clock",wall=100000,mono=11),dict(action="get",meta=meta("Owned")),dict(action="save")]),
    dict(case="monotonic_expiry_with_wall_backwards",operations=[dict(action="update",meta=meta("Owned"),info=positive),dict(action="clock",wall=1,mono=4000),dict(action="get",meta=meta("Owned")),dict(action="save")]),
    dict(case="restore_ceil_monotonic",seed=dict(version=1,items={key("Owned"):dict(value=positive,expires_at=1000.2)}),operations=[dict(action="clock",wall=5000,mono=10.5),dict(action="get",meta=meta("Owned")),dict(action="clock",wall=1,mono=11.0),dict(action="get",meta=meta("Owned")),dict(action="save")]),
    dict(case="restore_invalid_expired_delete",seed=dict(version=1,items={"invalid":123,"expired":dict(value=positive,expires_at=999),"bad-value":dict(value=[],expires_at=2000),"bad-time":dict(value=positive,expires_at="2000")}),operations=[dict(action="save")]),
    dict(case="legacy_plain_restore",seed={key("Legacy"):positive},operations=[dict(action="get",meta=meta("Legacy")),dict(action="save")]),
    dict(case="negative_ttl_removes",ttl=-3600,operations=[dict(action="update",meta=meta("Owned"),info=positive),dict(action="get",meta=meta("Owned")),dict(action="save")]),
    dict(case="capacity_lru",operations=[dict(action="bulk",start=0,count=256),dict(action="get",meta=meta("Owned0")),dict(action="update",meta=meta("Owned256"),info=positive),dict(action="get",meta=meta("Owned1")),dict(action="save")]),
    dict(case="big_to_small_restore",seed=dict(version=1,items={key("Owned"+str(i)):dict(value=info(i+1),expires_at=4600+i%7/10) for i in range(300)}),operations=[dict(action="get",meta=meta("Owned0")),dict(action="get",meta=meta("Owned299")),dict(action="save")]),
    dict(case="negative_not_restored",operations=[dict(action="update",meta=meta("Negative"),info={}),dict(action="update",meta=meta("Positive"),info=positive),dict(action="save"),dict(action="restart"),dict(action="get",meta=meta("Negative")),dict(action="get",meta=meta("Positive"))]),
]
results=[]
with tempfile.TemporaryDirectory(prefix="source-tmdb-cache-") as folder:
    for scenario in scenarios:
        clock[:]=[1000.0,10.0];directory=Path(folder)/scenario['case'];directory.mkdir();settings=SimpleNamespace(CONF=SimpleNamespace(tmdb=scenario.get('maxsize',256),meta=scenario.get('ttl',3600)),TMDB_LOCALE="zh",CACHE_PATH=directory/"cache",TEMP_PATH=directory/"temp");env['settings']=settings
        file=file_type(settings.CACHE_PATH)
        if 'seed' in scenario:
            seed=copy.deepcopy(scenario['seed'])
            def convert(value):
                if isinstance(value,dict):
                    for k,v in value.items():
                        if k in ['type','media_type'] and v in ['电影','电视剧','未知']:value[k]=MediaType(v)
                        else:convert(v)
            convert(seed);file.set("tmdb",pickle.dumps(seed),region="recognize")
        cache=cache_type();observations=[]
        for operation in scenario['operations']:
            action=operation['action']
            if action=='clock':clock[:]=[operation['wall'],operation['mono']]
            elif action=='update':cache.update(convert_meta(operation['meta']),convert_info(operation['info']))
            elif action=='get':observations.append(dict(action='get',meta=operation['meta'],value=wire(cache.get(convert_meta(operation['meta'])))))
            elif action=='bulk':
                for i in range(operation['start'],operation['start']+operation['count']):cache.update(convert_meta(meta('Owned'+str(i))),convert_info(info(i+1)))
            elif action=='save':cache.save()
            elif action=='restart':cache=cache_type()
        cache.save();payload=file.get('tmdb',region='recognize');saved=pickle.loads(payload) if payload else None
        results.append(dict(**scenario,observations=observations,persisted=wire(saved),persisted_order=list(saved['items']) if saved else [],seed_order=list(scenario.get('seed',{}).get('items',scenario.get('seed',{})))))
print(json.dumps(dict(source_commit=SHA,cachetools_version=cachetools.__version__,classification="unchanged pinned TmdbCache/FileBackend/_MemoryTLRUCache AST; actual cachetools; synthetic independent wall/monotonic clocks; owned real pickle files",sources=sources,cases=results),ensure_ascii=False,indent=2))
