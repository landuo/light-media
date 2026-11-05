#!/usr/bin/env python3
"""Fixed source AniList model/module/API replay with owned provider boundaries."""
import ast,copy,hashlib,json,pathlib,subprocess,types,typing
from urllib.parse import urlsplit

ROOT=pathlib.Path(__file__).resolve().parents[3]
OUT=pathlib.Path(__file__).resolve().parent
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):return subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True)
# Reuse only the independently declared fixed MediaInfo AST/parser boundary;
# stop before its cases or any output writes. It imports no application code.
shared=ROOT/'testdata/plugin-host-integration-20261031/source-models/oracle.py'
prefix=shared.read_text().split("base={'id'",1)[0]
setup={'__file__':str(shared)};exec(compile(prefix,str(shared),'exec'),setup)
ns=setup['ns'];MediaInfo=ns['MediaInfo'];MediaType=setup['MediaType']
base={'id':151,'idMal':26,'title':{'chinese':'Owned','native':'Original','romaji':'Romaji','english':'English'},'format':'TV','status':'FINISHED','description':' <b>Hello</b><br>World &amp; all ','startDate':{'year':2026,'month':10,'day':31},'endDate':{'year':2026,'month':11,'day':1},'seasonYear':2026,'episodes':3,'duration':24,'countryOfOrigin':'JP','coverImage':{'extraLarge':'https://s4.anilist.co/cover.jpg','large':'fallback'},'bannerImage':'https://s4.anilist.co/banner.jpg','genres':['Drama'],'synonyms':['Alias','Alias'],'averageScore':86.5,'popularity':17,'isAdult':False,'studios':{'nodes':[{'name':'Studio'}]},'actors':[{'id':9,'name':'Actor'}],'directors':[{'id':10,'name':'Director'}],'externalLinks':[{'site':'AniDB','url':'https://anidb.net/anime/456'}]}
specs=[('complete',{}),('movie',{'format':'MOVIE'}),('lowercase_movie',{'format':'movie'}),('unknown_format',{'format':None}),('title_native',{'title':{'native':'Owned','english':'English'}}),('chinese_synonym',{'title':{'chinese':'かな','native':'Native'},'synonyms':['首个中文','最后中文']}),('chinese_with_kana',{'title':{'chinese':'中文かな','native':'Native'},'synonyms':['中文标题']}),('season2',{'title':{'chinese':'Owned S02'}}),('specials',{'title':{'chinese':'Owned S00'}}),('zero_score',{'averageScore':0}),('missing_score',{'averageScore':None}),('invalid_score',{'averageScore':'bad'}),('string_score',{'averageScore':'86.5'}),('year_only',{'startDate':{'year':2026}}),('year_month',{'startDate':{'year':2026,'month':3}}),('month_missing_day',{'startDate':{'year':2026,'day':3}}),('season_year_fallback',{'startDate':{},'seasonYear':2025}),('null_start_date',{'startDate':None}),('no_date',{'startDate':{},'seasonYear':None}),('no_episodes',{'episodes':None}),('zero_episodes',{'episodes':0}),('negative_episodes',{'episodes':-1}),('boolean_episodes',{'episodes':True}),('float_episodes',{'episodes':3.0}),('string_episodes',{'episodes':'3'}),('null_studios',{'studios':None}),('null_cover',{'coverImage':None}),('empty_people',{'actors':None,'directors':None}),('non_japanese',{'countryOfOrigin':'CN'}),('unicode_anidb',{'externalLinks':[{'site':'ANIDB','url':'owned/١٢٣'}]}),('null_external_link',{'externalLinks':[None]}),('html_entities',{'description':'<i>Owned</i><br/>A<br />B &lt; C'})]
specs.extend([('false_title',{'title':False}),('zero_cover',{'coverImage':0}),('empty_list_title',{'title':[]})])
model=[]
for label,patch in specs:
 info=copy.deepcopy(base);info.update(patch);row={'case':label,'input':info}
 try:row['expected']=MediaInfo(anilist_info=copy.deepcopy(info)).to_dict()
 except BaseException as exc:row['error']=type(exc).__name__
 model.append(row)

module_source=source('app/modules/anilist/__init__.py');api_source=source('app/modules/anilist/anilist.py')
module_cls=next(n for n in ast.parse(module_source).body if isinstance(n,ast.ClassDef) and n.name=='AniListModule')
module_methods={'_source_enabled','_media_type','_matches_meta','_enrich_people','recognize_media','_match_by_meta'}
module_nodes=[copy.deepcopy(n) for n in module_cls.body if isinstance(n,ast.FunctionDef) and n.name in module_methods]
api_cls=next(n for n in ast.parse(api_source).body if isinstance(n,ast.ClassDef) and n.name=='AniListApi')
api_methods={'_extract_response','_invoke','_disable_proxy','_build_translation_map','_translation_map','_inject_chinese','detail','search','_page_medias'}
api_nodes=[copy.deepcopy(n) for n in api_cls.body if isinstance(n,ast.Assign) or isinstance(n,ast.FunctionDef) and n.name in api_methods]
for n in api_nodes:
 if isinstance(n,ast.FunctionDef):n.decorator_list=[d for d in n.decorator_list if isinstance(d,ast.Name) and d.id in {'staticmethod','classmethod'}]
env=dict(vars(typing),MediaInfo=MediaInfo,MediaType=MediaType,MetaBase=object,settings=types.SimpleNamespace(RECOGNIZE_SOURCE='anilist'),logger=types.SimpleNamespace(info=lambda *x:None,warning=lambda *x:None,error=lambda *x:None))
for clsname,nodes in [('AniListApi',api_nodes),('AniListModule',module_nodes)]:exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name=clsname,bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),COMMIT+':'+clsname,'exec'),env)

people={'characters':{'edges':[{'node':{'name':{'full':'Character','native':'角色'}},'voiceActors':[{'id':9,'name':{'full':'Actor','native':'声优'},'image':{'large':'https://s4.anilist.co/actor.jpg'},'siteUrl':'https://anilist.co/staff/9'},{'id':99,'name':{'full':'Ignored'}}]}]},'staff':{'edges':[{'role':'Chief Director','node':{'id':10,'name':{'full':'Director'},'image':{'large':'https://s4.anilist.co/director.jpg'},'siteUrl':'https://anilist.co/staff/10'}},{'role':'director','node':{'name':{'full':'Ignored'}}}]}}
full=copy.deepcopy(base);full.update(people)
translations=[{'id':151,'title':'Owned','synonyms':['Alias','Translated']}]
cases=[];native=[]
def run(label,request,rows=None,detail=None,patch=None,proxy_status=200,proxy_errors=False,translation_value=translations,shared_api=None,fresh=True):
 info=copy.deepcopy(full if detail is None else detail)
 if patch:info.update(patch)
 values=copy.deepcopy(rows if rows is not None else [info])
 replies=[];trace=[]
 class Response:
  def __init__(self,status,body):self.status_code=status;self.body=body
  def json(self):return copy.deepcopy(self.body)
 class Request:
  def post_res(self,url,json):
   u=urlsplit(url);is_detail='id'in json['variables'];result={'data':{'Media':info}} if is_detail else {'data':{'Page':{'media':values}}}
   status=proxy_status if u.hostname=='trace.moe' else 200
   if proxy_errors and u.hostname=='trace.moe':result={'errors':[{'message':'owned GraphQL error'}]}
   replies.append({'host':u.hostname,'method':'POST','path':u.path or '/','status':status,'body':copy.deepcopy(result),'request_body':copy.deepcopy(json)})
   trace.append({'host':u.hostname,'operation':'detail' if is_detail else 'search','variables':copy.deepcopy(json['variables'])})
   return Response(status,result)
  def get_json(self,url):
   u=urlsplit(url);replies.append({'host':u.hostname,'method':'GET','path':u.path,'status':200,'body':copy.deepcopy(translation_value)})
   trace.append({'host':u.hostname,'operation':'translations'})
   return copy.deepcopy(translation_value)
 api=shared_api or env['AniListApi']()
 if shared_api is None:api._proxy_available=True;api._translations=None
 api._request=Request()
 module=env['AniListModule']();module.anilist_api=api
 meta=request.get('meta');kwargs=dict(request)
 if meta is not None:
  kind=meta.get('type');kind=MediaType(kind) if kind else None
  kwargs['meta']=types.SimpleNamespace(name=meta.get('en_name') or meta.get('name'),year=meta.get('year'),type=kind,begin_season=meta.get('begin_season'))
 row={'case':label,'request':request,'trace':trace}
 try:
  result=module.recognize_media(**kwargs);row['expected']=result.to_dict() if result else None
 except BaseException as exc:row['error']=type(exc).__name__
 row['replies']=replies;cases.append(row)
 if request.get('source')=='anilist' and 'error'not in row:
  native.append({'case':'anilist_'+label,'request':dict(request,cache=False),'replies':replies,'expected':row['expected'],'fresh_process':fresh})
 return api
run('id',{'source':'anilist','anilistid':151})
run('id_meta_override',{'source':'anilist','anilistid':151,'meta':{'en_name':'Owned','type':'电视剧','year':'1990','begin_season':0}},patch={'title':{'chinese':'Owned S02'}})
run('search',{'source':'anilist','meta':{'en_name':'Owned','type':'电视剧','year':'2026'}})
wrong=copy.deepcopy(full);wrong['format']='MOVIE'
year=copy.deepcopy(full);year['startDate']['year']=2025
run('ordered_filter',{'source':'anilist','meta':{'en_name':'Owned','type':'电视剧','year':'2026'}},rows=[wrong,year,full])
run('no_match',{'source':'anilist','meta':{'en_name':'Owned','type':'电视剧','year':'1990'}},rows=[full])
run('lowercase_format_type_difference',{'source':'anilist','meta':{'en_name':'Owned','type':'电视剧','year':'2026'}},patch={'format':'movie'})
run('proxy_http_fallback',{'source':'anilist','anilistid':151},proxy_status=503)
run('proxy_graphql_fallback',{'source':'anilist','anilistid':151},proxy_errors=True)
run('translation_empty',{'source':'anilist','anilistid':151},translation_value=[])
run('nested_translation',{'source':'anilist','anilistid':151},patch={'synonyms':['Alias','中文标题'],'title':{'chinese':'かな','native':'Original'}},translation_value=[{'id':151,'title':'かな','synonyms':['中文标题','中文标题']}])
run('null_people_error',{'source':'anilist','anilistid':151},patch={'characters':None})
run('missing_people',{'source':'anilist','anilistid':151},patch={'characters':{},'staff':{}})
run('source_disabled',{'source':'themoviedb','meta':{'en_name':'Owned','type':'电视剧'}})
run('id_ignores_source_guard',{'source':'themoviedb','anilistid':151})
run('no_meta',{'source':'anilist'})
run('source_inferred_from_id',{'source':'anilist','anilistid':151})
native[-1]['request'].pop('source')
run('explicit_anilist_over_other_ids',{'source':'anilist','anilistid':151})
native[-1]['request']['tmdbid']=999
# The original API is reused here: cache=False does not reset its proxy flag or
# loaded translation dictionary. Request boundaries alone are replaced.
client=run('lifetime_proxy_first',{'source':'anilist','anilistid':151},proxy_status=503)
second=copy.deepcopy(full);second['id']=152;second['title']['chinese']='Second'
run('lifetime_proxy_second',{'source':'anilist','anilistid':152},detail=second,shared_api=client,fresh=False)
client=run('lifetime_empty_translation_first',{'source':'anilist','anilistid':151},translation_value=None)
run('lifetime_empty_translation_second',{'source':'anilist','anilistid':152},detail=second,shared_api=client,fresh=False)
manifest={'source_commit':COMMIT,'classification':'fixed source AST model/module/API replay; owned HTTP bodies and explicit source parser boundary; no application imports or real provider network','source_hashes':{p:hashlib.sha256(source(p).encode()).hexdigest() for p in ['app/core/context.py','app/modules/anilist/__init__.py','app/modules/anilist/anilist.py']},'parser_boundary':setup['META'],'model_cases':model,'recognition_cases':cases,'native_cases':native,'production_writes':0,'network_calls':0}
(OUT/'oracle.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n');(OUT/'native-cases.json').write_text(json.dumps({'source_commit':COMMIT,'initial_condition':'fresh_process=true resets AniList client; consecutive false retains source proxy/translation state; cache=false only bypasses recognition/provider value cache','cases':native},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'models':len(model),'recognition':len(cases),'native':len(native),'model_errors':sum('error'in r for r in model),'recognition_errors':sum('error'in r for r in cases)}))
