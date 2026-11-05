#!/usr/bin/env python3
"""Fixed provider module algorithms and MediaInfo setters; owned HTTP/parser boundaries."""
import ast,copy,datetime,hashlib,importlib.util,json,pathlib,subprocess,types
from typing import *
p=pathlib.Path(__file__).with_name('oracle.py');spec=importlib.util.spec_from_file_location('model_oracle',p);base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)
COMMIT=base.COMMIT
sources={}
def source(path):
 text=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True);sources[path]=hashlib.sha256(text.encode()).hexdigest();return ast.parse(text)
def source_class(path,name,methods,ns):
 node=next(n for n in source(path).body if isinstance(n,ast.ClassDef) and n.name==name)
 node.bases=[];node.keywords=[];node.decorator_list=[];node.body=[n for n in node.body if isinstance(n,(ast.Assign,ast.AnnAssign)) and (not isinstance(n,ast.AnnAssign) or not isinstance(n.annotation,ast.Name) or n.annotation.id not in ['BangumiApi','DoubanApi','DoubanScraper']) or isinstance(n,ast.FunctionDef) and n.name in methods]
 for n in node.body:
  if isinstance(n,ast.FunctionDef):n.decorator_list=[x for x in n.decorator_list if isinstance(x,ast.Name) and x.id in ['staticmethod','classmethod']]
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),node],type_ignores=[])),path,'exec'),ns)
 return ns[name]
ns=dict(base.ns,datetime=datetime.datetime,logger=types.SimpleNamespace(info=lambda *a:None,error=lambda *a:None,warn=lambda *a:None),settings=types.SimpleNamespace(RECOGNIZE_SOURCE='bangumi'))
API=source_class('app/modules/bangumi/bangumi.py','BangumiApi',{'search','detail','credits'},ns)
Module=source_class('app/modules/bangumi/__init__.py','BangumiModule',{'recognize_media','_matches_meta','_match_by_meta','bangumi_info'},ns)
root=copy.deepcopy(base.base_b)
chars=[{'id':80,'name':'Owned Character','actors':[{'id':90,'name':'First Actor'},{'id':91,'name':'Ignored Actor'}]},{'id':81,'name':'No Cast','actors':[]}]
specs=[('id',{'bangumiid':902},[],{902:root},chars),('meta_season_override',{'bangumiid':902,'meta':{'name':'Owned','begin_season':5}},[],{902:root},chars),('search_first_match',{'meta':{'name':'Owned','year':'2024','type':'电视剧'}},[{'id':901},{'id':902}],{901:dict(root,id=901,date='2023-01-01'),902:root},chars),('search_movie_filter',{'meta':{'name':'Owned','type':'电影'}},[{'id':902},{'id':903}],{902:root,903:dict(root,id=903,platform='Movie')},chars),('search_unknown_type',{'meta':{'name':'Owned','type':'未知'}},[{'id':902}],{902:root},[]),('search_no_date_accept',{'meta':{'name':'Owned','year':'1990'}},[{'id':902}],{902:dict(root,date=None)},[]),('skip_id_and_missing',{'meta':{'name':'Owned'}},[{}, {'id':901},{'id':902}],{902:root},[]),('first_ten_limit',{'meta':{'name':'Owned','year':'2024'}},[{'id':i} for i in range(910,921)],{i:dict(root,id=i,date='2023-01-01' if i<920 else '2024-01-01') for i in range(910,921)},[]),('id_missing',{'bangumiid':904},[],{},[]),('search_empty',{'meta':{'name':'Owned'}},[],{},[])]
specs.extend([('numeric_year_mismatch',{'meta':{'name':'Owned','year':1990}},[{'id':902}],{902:root},[]),('numeric_year_match',{'meta':{'name':'Owned','year':2024}},[{'id':902}],{902:root},[]),('bad_actor',{'bangumiid':902},[],{902:root},[{'id':1,'actors':[1]}]),('no_meta',{},[],{},[])])
cases=[];native=[]
specs.extend([('wrong_detail_shape',{'bangumiid':902},[],{902:[{'id':902}]},[]),('wrong_characters_shape',{'bangumiid':902},[],{902:root},{'error':'owned'}),('missing_character_id_skips_bad_cast',{'bangumiid':902},[],{902:root},[{'actors':{'bad':'owned'}}])])
for label,request,search,details,characters in specs:
 trace=[];replies=[]
 def invoke(self,url,key=None,**kwargs):
  if url.startswith('search/subject/'):
   result={'list':search};trace.append({'op':'search','name':url[len('search/subject/'):]})
  elif url.endswith('/characters'):
   result=characters;trace.append({'op':'credits','id':int(url.split('/')[2])})
  else:
   identity=int(url.split('/')[2]);result=details.get(identity);trace.append({'op':'detail','id':identity})
  replies.append({'host':'api.bgm.tv','method':'GET','path':'/'+url,'status':200 if result is not None else 404,'body':copy.deepcopy(result) if result is not None else {}})
  return copy.deepcopy(result)
 API._BangumiApi__invoke=invoke
 module=Module();module.bangumiapi=API.__new__(API)
 rawmeta=request.get('meta');meta=None
 if rawmeta is not None:
  values={'name':'','cn_name':None,'en_name':None,'year':None,'type':None,'begin_season':None};values.update(rawmeta)
  if values['type']:values['type']=base.MediaType(values['type'])
  meta=types.SimpleNamespace(**values)
 try:
  result=module.recognize_media(meta=meta,bangumiid=request.get('bangumiid'),source='bangumi');expected=result.to_dict() if result else None;error=None
 except Exception as exc:expected=None;error=type(exc).__name__
 row={'case':label,'request':request,'search':search,'details':details,'characters':characters,'trace':trace,'expected':expected,'error':error};cases.append(row)
 sdk={'source':'bangumi','cache':False}
 if 'bangumiid'in request:sdk['bangumiid']=request['bangumiid']
 if rawmeta is not None:sdk['meta']=rawmeta
 native.append({'case':'bangumi_'+label,'request':sdk,'replies':replies,'expected':expected,'error':error})
out={'commit':COMMIT,'source_sha256':sources,'kind':'fixed module/API/context AST; HTTP and title parser are explicit owned boundaries','cases':cases}
pathlib.Path(__file__).with_name('bangumi-recognition-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
pathlib.Path(__file__).with_name('bangumi-native-cases.json').write_text(json.dumps({'cases':native},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'bangumi_recognition_cases':len(cases),'http_calls':sum(len(r['replies']) for r in native)}))
from zhconv_rs import zhconv
ns=dict(base.ns,datetime=datetime.datetime,logger=types.SimpleNamespace(info=lambda *a:None,error=lambda *a:None,warn=lambda *a:None),settings=types.SimpleNamespace(RECOGNIZE_SOURCE='douban'),zhconv_convert=zhconv,APIRateLimitException=RuntimeError)
DAPI=source_class('app/modules/douban/apiv2.py','DoubanApi',{'search','movie_detail','tv_detail','movie_celebrities','tv_celebrities'},ns)
DModule=source_class('app/modules/douban/__init__.py','DoubanModule',{'_recognize_media_core','_prepare_search_names','douban_info','match_doubaninfo','_process_search_results'},ns)
root_d=copy.deepcopy(base.base)
tv_d=dict(root_d,id='902',subtype='tv',episodes_count=3)
people={'directors':[{'name':'API Director'}],'actors':[{'name':'API Actor'}]}
def item(identity='901',title='Owned',year='2026',kind='电影'):
 return {'type_name':kind,'target':{'id':identity,'title':title,'year':year}}
dspecs=[('id_movie',{'doubanid':'901','mtype':'电影'}, {},{'movie/901':root_d},people),('id_tv',{'doubanid':'902','mtype':'电视剧'}, {},{'tv/902':tv_d},people),('id_auto_fallback',{'doubanid':'902'}, {},{'tv/902':tv_d},people),('id_no_people',{'doubanid':'901','mtype':'电影'}, {},{'movie/901':root_d},None),('name_movie',{'meta':{'name':'Owned','en_name':'Owned','type':'电影','year':'2026'}},{'Owned 2026':{'items':[item()]}},{'movie/901':root_d},people),('tv_default_season',{'meta':{'name':'Owned','en_name':'Owned','type':'电视剧','year':'2026'}},{'Owned 2026':{'items':[item('902',kind='电视剧')]}},{'tv/902':tv_d},people),('tv_season2',{'meta':{'name':'Owned','en_name':'Owned','type':'电视剧','begin_season':2}},{'Owned':{'items':[item('902','Owned S02',kind='电视剧')]}},{'tv/902':dict(tv_d,title='Owned S02')},people),('year_strict',{'meta':{'name':'Owned','en_name':'Owned','type':'电影','year':'2026'}},{'Owned 2026':{'items':[item(year=2026)]}},{'movie/901':root_d},people),('wrong_type_skip',{'meta':{'name':'Owned','en_name':'Owned','type':'电影'}},{'Owned':{'items':[item(kind='书籍'),item('902',kind='电视剧'),item()]}},{'movie/901':root_d},people),('no_name',{'meta':{}},{},{},[]),('not_found',{'doubanid':'999'}, {},{},people),('names_order',{'meta':{'name':'龍','cn_name':'龍','en_name':'Owned','type':'电影'}},{'龍':{'items':[]},'龙':{'items':[]},'Owned':{'items':[item()]}},{'movie/901':root_d},people)]
dspecs.extend([('numeric_year',{'meta':{'name':'Owned','en_name':'Owned','type':'电影','year':2026}},{'Owned 2026':{'items':[item(year=2026)]}},{'movie/901':root_d},people),('query_python_strip',{'meta':{'name':'\x1cOwned\x1d','en_name':'\x1cOwned\x1d','type':'电影'}},{'Owned':{'items':[]}},{},[])])
dcases=[];dnative=[]
dspecs.extend([('wrong_detail_shape',{'doubanid':'901','mtype':'电影'},{},{'movie/901':[{'id':'901'}]},people),('wrong_people_shape',{'doubanid':'901','mtype':'电影'},{},{'movie/901':root_d},[{'id':'owned'}])])
for label,request,searches,details,celebrities in dspecs:
 trace=[];replies=[]
 def dinvoke(self,url,**kwargs):
  if url=='/search/weixin':
   result=searches.get(kwargs['q']);trace.append({'op':'search','query':kwargs['q']});query={'q':[kwargs['q']],'start':['0'],'count':['20']}
  elif url.endswith('/celebrities'):
   result=celebrities;trace.append({'op':'credits','path':url});query=None
  else:
   result=details.get(url.lstrip('/'));trace.append({'op':'detail','path':url});query=None
  reply={'host':'frodo.douban.com','method':'GET','path':'/api/v2'+url,'status':200 if result is not None else 404,'body':copy.deepcopy(result) if result is not None else {}}
  if query:reply['query']=query
  replies.append(reply);return copy.deepcopy(result)
 DAPI._DoubanApi__invoke_search=dinvoke
 module=DModule();module.doubanapi=DAPI.__new__(DAPI)
 rawmeta=request.get('meta');meta=None
 if rawmeta is not None:
  values={'name':'','cn_name':None,'en_name':None,'year':None,'type':None,'begin_season':None};values.update(rawmeta)
  if values['type']:values['type']=base.MediaType(values['type'])
  meta=types.SimpleNamespace(**values)
 kind=base.MediaType(request['mtype']) if request.get('mtype') else (meta.type if meta else None)
 try:
  result=module._recognize_media_core(meta=meta,mtype=kind,doubanid=request.get('doubanid'),douban_info_func=module.douban_info,match_doubaninfo_func=module.match_doubaninfo,source='douban');expected=result.to_dict() if result else None;error=None
 except Exception as exc:expected=None;error=type(exc).__name__
 dcases.append({'case':label,'request':request,'search':searches,'details':details,'people':celebrities,'trace':trace,'expected':expected,'error':error})
 sdk={'source':'douban','cache':False};sdk.update(request)
 dnative.append({'case':'douban_'+label,'request':sdk,'replies':replies,'expected':expected,'error':error})
dout={'commit':COMMIT,'source_sha256':sources,'kind':'fixed module/API/context AST; decorators removed here, rate-limit/retry independently tested; HTTP and title parser explicit owned boundaries','cases':dcases}
pathlib.Path(__file__).with_name('douban-recognition-oracle.json').write_text(json.dumps(dout,ensure_ascii=False,indent=2)+'\n')
pathlib.Path(__file__).with_name('douban-native-cases.json').write_text(json.dumps({'cases':dnative},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'douban_recognition_cases':len(dcases),'http_calls':sum(len(r['replies']) for r in dnative)}))
