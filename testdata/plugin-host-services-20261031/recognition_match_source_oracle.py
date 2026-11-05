"""Execute pinned TMDB matcher and module bodies with finite owned responses.

No provider or MoviePilot service is contacted. The Chinese conversion uses
the actual pinned zhconv-rs 0.4.1 wheel in the coordinator's isolated venv.
"""
import ast
import copy
from enum import Enum
import hashlib
import json
from pathlib import Path
import re
import subprocess
import traceback
from types import SimpleNamespace
from typing import List,Optional

from zhconv_rs import zhconv

SHA="8a83efeacd3e26b82be495e17d50146e31c96a60"
REPO="/Users/accidia/projects/MoviePilot"
sources={}
def read(path):
    raw=subprocess.check_output(["git","-C",REPO,"show",SHA+":"+path]);sources[path]=hashlib.sha256(raw).hexdigest();return ast.parse(raw)
class MediaType(Enum):
    UNKNOWN="未知"
    MOVIE="电影"
    TV="电视剧"
env=dict(re=re,traceback=traceback,List=List,Optional=Optional,MediaType=MediaType,MetaBase=SimpleNamespace,MediaInfo=SimpleNamespace,TMDbException=Exception,
         logger=SimpleNamespace(info=lambda *a:None,debug=lambda *a:None,error=lambda *a:None,warn=lambda *a:None),settings=SimpleNamespace(RECOGNIZE_SOURCE="themoviedb"),zhconv_convert=zhconv)
def extract(tree,name,methods):
    cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==name)
    nodes=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in methods]
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name=name,bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),name,"exec"),env)
    return env[name]
env['Union']=__import__('typing').Union
extract(read("app/utils/string.py"),"StringUtils",{"clear"})
api_type=extract(read("app/modules/themoviedb/tmdbapi.py"),"TmdbApi",{"get_info","__get_names","__get_content_rating","__update_tmdbinfo_extra_title","__update_tmdbinfo_cn_title","__compare_names","_validate_match_params","_generate_year_range","_log_match_debug","_set_media_type","_sort_multi_results","_convert_media_type","_match_multi_title","_match_multi_names","match","__search_movie_by_name","__search_tv_by_name","__search_tv_by_season","__get_tv_seasons","match_multi"})
module_type=extract(read("app/modules/themoviedb/__init__.py"),"TheMovieDbModule",{"_get_info_by_tmdbid","_disambiguate_by_meta","_validate_recognize_params","_prepare_search_names","_search_by_name","recognize_media"})

def movie(id,title,year="2024",**extra):return dict(id=id,title=title,original_title=title,release_date=year+"-01-01",**extra)
def tv(id,title,year="2024",**extra):return dict(id=id,name=title,original_name=title,first_air_date=year+"-01-01",**extra)
def sample(case,meta,responses,details=None,groups=None):return dict(case=case,meta=meta,responses=responses,details=details or {},groups=groups or [])
base=dict(en_name="Owned Match",type="未知",year=None,begin_season=None)
samples=[
    sample("unknown_multi_movie_precedence",base,{"multi:Owned Match:":[dict(tv(2,"Owned Match","2026"),media_type="tv"),dict(movie(1,"Owned Match","2020"),media_type="movie")]},{"movie:1":movie(1,"Owned Match","2020")}),
    sample("exact_not_substring",base,{"multi:Owned Match:":[dict(movie(3,"Owned Match Extra"),media_type="movie")]},{"movie:3":movie(3,"Owned Match Extra")}),
    sample("title_pass_before_alias",base,{"multi:Owned Match:":[dict(movie(4,"Other","2026"),media_type="movie"),dict(movie(5,"Owned Match","2020"),media_type="movie")]},{"movie:4":movie(4,"Other","2026",names=["Owned Match"]),"movie:5":movie(5,"Owned Match","2020")}),
    sample("movie_year_plus_one",dict(base,type="电影",year="2024"),{"movie:Owned Match:2025":[movie(6,"Owned Match","2025")]},{"movie:6":movie(6,"Owned Match","2025")}),
    sample("movie_year_minus_one",dict(base,type="电影",year="2024"),{"movie:Owned Match:2023":[movie(7,"Owned Match","2023")]},{"movie:7":movie(7,"Owned Match","2023")}),
    sample("movie_then_tv",dict(base,type="电影",year="2024"),{"tv:Owned Match:2024":[tv(8,"Owned Match")]},{"tv:8":tv(8,"Owned Match")}),
    sample("tv_drop_year",dict(base,type="电视剧",year="2024"),{"tv:Owned Match:":[tv(9,"Owned Match","2010")]},{"tv:9":tv(9,"Owned Match","2010")}),
    sample("season_air_year",dict(base,type="电视剧",year="2024",begin_season=2),{"tv:Owned Match:":[tv(10,"Owned Match","2010")]},{"tv:10":tv(10,"Owned Match","2010",seasons=[dict(season_number=2,air_date="2024-03-01",episode_count=2)])}),
    sample("group_season_air_year",dict(base,type="电视剧",year="2024",begin_season=2),{"tv:Owned Match:":[tv(11,"Owned Match","2010")]},{"tv:11":tv(11,"Owned Match","2010")},[dict(order=2,episodes=[dict(air_date="2024-05-01",episode_number=1)])]),
    sample("alias_lookup",base,{"multi:Owned Match:":[dict(movie(12,"Other"),media_type="movie")]},{"movie:12":movie(12,"Other",names=["Owned Match"])}),
    sample("punctuation_and_upper",dict(base,en_name="Owned - STRASSE"),{"multi:Owned - STRASSE:":[dict(movie(13,"owned straße"),media_type="movie")]},{"movie:13":movie(13,"owned straße")}),
    sample("traditional_simplified_name_order",dict(base,cn_name="變形金剛",en_name="Transformers"),{"multi:变形金刚:":[dict(movie(14,"变形金刚"),media_type="movie")]},{"movie:14":movie(14,"变形金刚")}),
    sample("cn_zh_en_last_fallback",dict(base,cn_name="變形金剛",en_name="Transformers"),{"multi:Transformers:":[dict(movie(15,"Transformers"),media_type="movie")]},{"movie:15":movie(15,"Transformers")}),
    sample("typed_all_miss_multi_last",dict(base,type="电影",year="2024"),{"multi:Owned Match:":[dict(tv(16,"Owned Match","2000"),media_type="tv")]},{"tv:16":tv(16,"Owned Match","2000")}),
    sample("network_failure_none",base,{"multi:Owned Match:":None}),
    sample("empty_results_negative",base,{}),
]
results=[]
for data in samples:
    trace=[];api=api_type()
    meta_data=dict(cn_name=None,en_name=None,year=None,begin_season=None,type="未知");meta_data.update(data['meta']);meta=SimpleNamespace(**meta_data)
    meta.type=MediaType(meta.type);meta.name=meta.cn_name or meta.en_name
    class Search:
        total_results=0
        def fetch(self,kind,name,year=None):
            trace.append(dict(operation="search",kind=kind,name=name,year=year))
            rows=copy.deepcopy(data['responses'].get(kind+":"+name+":"+(year or ""),[]))
            if rows is None:raise RuntimeError("owned network failure")
            self.total_results=len(rows);return rows
        def movies(self,term,year=None):return self.fetch("movie",term,year)
        def tv_shows(self,term,release_year=None):return self.fetch("tv",term,release_year)
        def multi(self,term):return self.fetch("multi",term)
    api.search=Search()
    def detail(mtype,tmdbid):
        kind="movie" if mtype==MediaType.MOVIE else "tv";trace.append(dict(operation="detail",kind=kind,id=tmdbid))
        row=copy.deepcopy(data['details'].get(kind+":"+str(tmdbid)))
        if row is not None:row['media_type']=mtype
        return row
    api.get_info=detail
    def get_groups(group):trace.append(dict(operation="groups",group=group));return copy.deepcopy(data['groups'])
    api.get_tv_group_seasons=get_groups
    module=module_type();module.tmdb=api
    cache=[];module.cache=SimpleNamespace(get=lambda _: {},update=lambda m,info:cache.append("none" if info is None else "negative" if not info else "positive"))
    module._build_media_info_result=lambda info,*_:SimpleNamespace(info=info)
    result=module.recognize_media(meta=meta,cache=False,episode_group="owned-group" if data['groups'] else None)
    results.append(dict(**data,names=module._prepare_search_names(meta),trace=trace,cache_updates=cache,selected_id=result.info['id'] if result else None,selected_type=result.info['media_type'].value if result else None))
unknown=[]
for case,tv_found,movie_found in [('unknown_ambiguous',True,True),('unknown_tv_only',True,False),('unknown_movie_only',False,True),('unknown_neither',False,False)]:
    api=api_type();api.tmdb=SimpleNamespace(language='en-US');trace=[]
    def get_tv(_):trace.append('tv');return tv(123,'Owned TV') if tv_found else None
    def get_movie(_):trace.append('movie');return movie(123,'Owned Movie') if movie_found else None
    api._TmdbApi__get_tv_detail=get_tv;api._TmdbApi__get_movie_detail=get_movie
    module=module_type();module.tmdb=api
    value=module._get_info_by_tmdbid(123,MediaType.UNKNOWN,SimpleNamespace(cn_name='Owned TV',en_name=None,year='2024',type=MediaType.TV))
    unknown.append(dict(case=case,tv=tv_found,movie=movie_found,trace=trace,selected_type=value['media_type'].value if value else None))
print(json.dumps(dict(source_commit=SHA,classification="unchanged pinned AST bodies with finite provider/cache fixtures; actual zhconv-rs0.4.1 wheel; no real provider",sources=sources,cases=results,unknown_cases=unknown),ensure_ascii=False,indent=2))
