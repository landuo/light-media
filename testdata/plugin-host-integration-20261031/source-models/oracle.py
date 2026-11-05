#!/usr/bin/env python3
"""Execute fixed MediaInfo setters. Provider values and title-parser output are owned boundaries.
The source MetaInfo parser is independently covered by the existing parser corpus.
No source installation/service is imported, executed, or modified.
"""
import ast,copy,dataclasses,hashlib,json,pathlib,re,subprocess,types
from enum import Enum
from typing import *
ROOT=pathlib.Path(__file__).resolve().parents[3]
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
text=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/core/context.py'],text=True)
class MediaType(Enum):MOVIE='电影';TV='电视剧';UNKNOWN='未知'
# Explicit fixture boundary: no inferred parser results masquerade as source logic.
META={'Owned':{'name':'Owned','begin_season':None},'Owned S02':{'name':'Owned','begin_season':2},'Owned S00':{'name':'Owned','begin_season':0},'原片名':{'name':'原片名','begin_season':None}}
def metainfo(title):return types.SimpleNamespace(**META.get(title,{'name':title,'begin_season':None}))
ns=dict(dataclass=dataclasses.dataclass,field=dataclasses.field,re=re,MediaType=MediaType,MetaInfo=metainfo,MetaBase=object,List=List,Dict=Dict,Any=Any,Tuple=Tuple,Optional=Optional,Set=Set,settings=types.SimpleNamespace(),StringUtils=object)
tree=ast.parse(text)
node=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='MediaInfo')
constants=[n for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id.startswith(('BANGUMI_','ANILIST_')) for t in n.targets)]
exec(compile(ast.fix_missing_locations(ast.Module(body=[*constants,node],type_ignores=[])),'fixed/app/core/context.py','exec'),ns)
base={'id':'901','title':'Owned','subtype':'movie','year':'2026','rating':{'value':7.5},'pic':{'large':'https://img1.doubanio.com/owned.jpg'},'pubdate':['2026-09-01(中国大陆)'],'intro':'owned intro','genres':['Drama'],'countries':['中国大陆'],'durations':['96分钟'],'aka':['Other(港译名)','Alias(台湾)'],'actors':[{'name':'Owned Actor'}],'directors':[{'name':'Owned Director'}]}
specs=[('complete',{}),('raw_media_type_ignored',{'media_type':'电影','subtype':'tv'}),('invalid_year',{'year':2026}),('tv_season',{'title':'Owned S02','subtype':'tv','episodes_count':3}),('specials',{'title':'Owned S00','subtype':'tv','episodes_count':2}),('tv_without_season',{'subtype':'tv','episodes_count':2}),('no_type',{'subtype':None}),('target_type',{'subtype':None,'target_type':'tv'}),('uri_type',{'subtype':None,'uri':'douban://douban.com/tv/901'}),('type_name',{'subtype':None,'type_name':'电视剧'}),('plain_type',{'subtype':None,'type':'tv'}),('no_rating',{'rating':None}),('rating_string',{'rating':{'value':'0'}}),('extra_year',{'year':None,'extra':{'year':'1999'}}),('intro_year',{'year':None,'intro':'premiere 2001'}),('extra_overview',{'intro':'','card_subtitle':'','extra':{'info':[['year','2008'],['origin','China']]}}),('cover_resize',{'pic':None,'cover_url':'https://img1.doubanio.com/x?imageView2/0/q/80/w/9999/h/120/format/webp'}),('nested_cover',{'pic':None,'cover':{'large':{'url':'https://img1.doubanio.com/nested.jpg'}}}),('release_date',{'release_date':'2020-02-03'}),('no_public_date',{'pubdate':[]}),('residual_fields',{'imdb_id':'tt00901','number_of_episodes':99,'languages':['中文'],'vote_count':10}),('unknown_fields',{'owned_unknown':3}),('zero_episodes',{'subtype':'tv','episodes_count':0}),('negative_episodes',{'subtype':'tv','episodes_count':-1})]
specs.extend([('string_episodes',{'subtype':'tv','episodes_count':'3'}),('integral_float_episodes',{'subtype':'tv','episodes_count':3.0}),('fractional_episodes',{'subtype':'tv','episodes_count':3.5}),('bool_episodes',{'subtype':'tv','episodes_count':True})])
specs.extend([('unicode_duration',{'durations':['٩٦分钟']}),('padded_duration',{'durations':['0096分钟']}),('unicode_overview_year',{'year':None,'intro':'上映年份２０２６'}),('unicode_pubdate',{'pubdate':['２０２６-０９-０１(中国大陆)']}),('unicode_cover_resize',{'pic':None,'cover_url':'https://img1.doubanio.com/x?imageView2/０/q/８０/w/９９９９/h/１２０/format/webp'})])
cases=[]
for name,patch in specs:
 info=copy.deepcopy(base);info.update(patch)
 row=dict(name=name,source='douban',input=info)
 try:row['expected']=ns['MediaInfo'](douban_info=copy.deepcopy(info)).to_dict()
 except Exception as exc:row['error']=type(exc).__name__
 cases.append(row)
out=dict(commit=COMMIT,source='app/core/context.py:604-744',source_sha256=hashlib.sha256(text.encode()).hexdigest(),kind='fixed source AST with synthetic provider + explicit parser boundary',parser_boundary=META,cases=cases)
pathlib.Path(__file__).with_name('douban-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(cases=len(cases),errors=sum('error'in row for row in cases))))
base_b={'id':902,'name':'Owned original','name_cn':'Owned','platform':'TV','date':'2024-02-03','rating':{'score':7.25},'images':{'large':'https://lain.bgm.tv/owned.jpg'},'summary':'owned summary','total_episodes':3,'tags':[{'name':'Adventure'},{'name':''}],'infobox':[{'key':'别名','value':[{'v':'Other'},'Alias']},{'key':'动画制作','value':'Owned Studio'},{'key':'导演','value':[{'v':'Owned Director'}]}],'actors':[{'id':9,'name':'Owned Actor'}]}
specs_b=[('complete',{}),('unicode_casefold_not_movie',{'platform':'MOVİE'}),('movie',{'platform':' MOVIE '}),('chinese_movie',{'platform':'剧场版'}),('unknown_platform',{'platform':None}),('season2',{'name_cn':'Owned S02'}),('specials',{'name_cn':'Owned S00'}),('name_fallback',{'name_cn':''}),('air_date',{'date':None,'air_date':'2001-02-03'}),('date_missing',{'date':None}),('fallback_image',{'images':None,'image':'https://lain.bgm.tv/fallback.jpg'}),('no_rating',{'rating':None}),('rating_zero',{'rating':{'score':0}}),('eps_fallback',{'total_episodes':0,'eps':2}),('no_episodes',{'total_episodes':None}),('negative_episodes',{'total_episodes':-1}),('first_alias',{'infobox':[{'key':'别名','value':'Only'},{'key':'别名','value':'Ignored'}]}),('nested_infobox',{'infobox':[{'key':'制作','value':[{'v':'A'},None,'B',{}]},{'key':'导演','value':'C'}]}),('empty_actors',{'actors':None})]
specs_b.extend([('string_episodes',{'total_episodes':'3'}),('integral_float_episodes',{'total_episodes':3.0}),('fractional_episodes',{'total_episodes':3.5}),('bool_episodes',{'total_episodes':True})])
cases_b=[]
for name,patch in specs_b:
 info=copy.deepcopy(base_b);info.update(patch);row=dict(name=name,source='bangumi',input=info)
 try:row['expected']=ns['MediaInfo'](bangumi_info=copy.deepcopy(info)).to_dict()
 except Exception as exc:row['error']=type(exc).__name__
 cases_b.append(row)
out_b=dict(out,source='app/core/context.py:746-858',cases=cases_b)
pathlib.Path(__file__).with_name('bangumi-oracle.json').write_text(json.dumps(out_b,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(bangumi_cases=len(cases_b),errors=sum('error'in row for row in cases_b))))
# Execute the same full source MediaInfo setter for a count above the removed
# arbitrary Go limit; retain a compact observation rather than a huge fixture.
large=dict(base,subtype='tv',episodes_count=100001)
model=ns['MediaInfo'](douban_info=large).to_dict()
episodes=next(iter(model['seasons'].values()))
bounds={'commit':COMMIT,'source_sha256':hashlib.sha256(text.encode()).hexdigest(),'count':100001,'length':len(episodes),'first':episodes[0],'last':episodes[-1],'json_bytes':len(json.dumps(episodes,separators=(',',':')).encode())}
pathlib.Path(__file__).with_name('episode-array-source.json').write_text(json.dumps(bounds,indent=2)+'\n')
