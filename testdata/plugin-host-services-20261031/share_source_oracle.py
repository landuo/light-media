"""Pinned pure ordinary recognition-share fields, without network/app imports."""
import ast
from enum import Enum
import hashlib
import json
import subprocess
from types import SimpleNamespace
from typing import Any, Dict, List, Optional, Tuple

SHA="8a83efeacd3e26b82be495e17d50146e31c96a60"
REPO="/Users/accidia/projects/MoviePilot"
sources={}
def read(path):
    raw=subprocess.check_output(["git","-C",REPO,"show",SHA+":"+path])
    sources[path]=hashlib.sha256(raw).hexdigest()
    return ast.parse(raw)
env=dict(Enum=Enum,Any=Any,Dict=Dict,List=List,Optional=Optional,Tuple=Tuple,MetaBase=SimpleNamespace,MediaInfo=SimpleNamespace)
types=read("app/schemas/types.py")
selected=[n for n in types.body if isinstance(n,ast.ClassDef) and n.name=="MediaType" or isinstance(n,ast.FunctionDef) and n.name=="media_type_to_agent"]
exec(compile(ast.fix_missing_locations(ast.Module(body=selected,type_ignores=[])),"fixed-types","exec"),env)
media=read("app/utils/media.py")
selected=[n for n in media.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id in {"MEDIA_SOURCE_ALIASES","MEDIA_SOURCE_PREFIXES","MEDIA_SOURCE_ID_FIELDS"} for t in n.targets) or isinstance(n,ast.FunctionDef) and n.name in {"normalize_media_source","parse_media_key","resolve_media_identity"}]
exec(compile(ast.fix_missing_locations(ast.Module(body=selected,type_ignores=[])),"fixed-media","exec"),env)
server=read("app/helper/server.py")
cls=next(n for n in server.body if isinstance(n,ast.ClassDef) and n.name=="MoviePilotServerHelper")
methods={"_normalize_media_type","_extract_keyword","_extract_media_type","_extract_year","_extract_season","_build_recognize_query_params","_build_recognize_report_payload","to_recognize_params","_parse_response_item"}
cls=ast.ClassDef(name=cls.name,bases=[],keywords=[],decorator_list=[],body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in methods])
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),"fixed-server-helper","exec"),env)
helper=env["MoviePilotServerHelper"]
MT=env["MediaType"]
def meta(value):
    fields=dict(original_name=None,name=None,type=None,year=None,begin_season=None,begin_episode=None)
    fields.update(value)
    if fields["type"] in {"电影","电视剧"}: fields["type"]=MT.MOVIE if fields["type"]=="电影" else MT.TV
    return SimpleNamespace(**fields)
queries=[]
for value,mtype in [(dict(original_name=" Owned.Raw.Title ",name="Owned",type="电影",year="2025"),None),
                    (dict(name="Owned TV",begin_season=0,begin_episode=1,year=2024),None),
                    (dict(name="Owned Unknown"),None),
                    (dict(name="",original_name=""),None)]:
    result=helper._build_recognize_query_params(meta(value),mtype)
    queries.append(dict(meta=value,mtype=mtype,result=result))
reports=[]
for value,info in [(dict(original_name=" Original.Title ",name="Owned",type="电视剧",begin_season=0,year="2024"),dict(type="电视剧",title="Recognized",year="2025",season=2,source="themoviedb",media_id="123",tmdb_id=123)),
                   (dict(name="Owned",type="电影"),dict(type="电影",title="",year="2026",tmdb_id=123)),
                   (dict(name="Owned",type="电影"),dict(type="电影",source="douban",tmdb_id=123))]:
    fields=dict(type=None,title=None,year=None,season=None,source=None,media_source=None,media_id=None,mediaid=None,tmdb_id=None,douban_id=None,bangumi_id=None,anilist_id=None)
    fields.update(info)
    if fields["type"] in {"电影","电视剧"}: fields["type"]=MT.MOVIE if fields["type"]=="电影" else MT.TV
    result=helper._build_recognize_report_payload(meta(value),SimpleNamespace(**fields))
    reports.append(dict(meta=value,info=info,result=result))
print(json.dumps(dict(source_commit=SHA,classification="isolated pinned source method replay, no external sharing",sources=sources,queries=queries,reports=reports),ensure_ascii=False,indent=2))
