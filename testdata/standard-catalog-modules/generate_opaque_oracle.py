"""Fixed media.detail and MediaChain name fallback methods. Registry/provider
responses and the previously parsed MetaBase input are controlled boundaries.
The prefix parsing, event decisions, mutation and call order run source AST.
"""
from pathlib import Path
import ast,asyncio,copy,hashlib,json,types
from pydantic import BaseModel,Field
p=Path(__file__).with_name('generate_images_oracle.py');prefix=p.read_text().split("images={'backdrops'")[0];scope={'__file__':str(p)};exec(compile(prefix,str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute'];media=ns['MediaInfo'];typ=ns['MediaType']
ns.update(ChainEventData=BaseModel,Field=Field,ChainEventType=types.SimpleNamespace(MediaRecognizeConvert='media.recognize.convert',NameRecognize='name.recognize'),deepcopy=copy.deepcopy)
execute([one('app/schemas/event.py',ast.ClassDef,'MediaRecognizeConvertEventData')],'<fixed:convert event DTO>')
def model(value):
 if not isinstance(value,dict):return value
 item=media();item.from_dict(value);return item
def clean(value):return scope['clean'](value) if 'clean' in scope else normalize(value)
def normalize(value):
 if isinstance(value,ns['Enum']):return value.value
 if isinstance(value,dict):return {k:normalize(v) for k,v in value.items()}
 if isinstance(value,(tuple,list)):return [normalize(v) for v in value]
 return value
base={'source':'themoviedb','tmdb_id':120,'media_id':'120','type':'电影','title':'Owned opaque','year':'2026','poster_path':'already-poster','backdrop_path':'already-backdrop','logo_path':'already-logo'}
recipes=[
 ('known_direct','tmdb:120',{'themoviedb:120':base},None,''),
 ('opaque_direct','Owned:abc/?:x',{'owned:abc/?:x':base},None,''),
 ('converted','Owned:abc',{'themoviedb:120':base},{'convert_type':'themoviedb','media_dict':{'id':120}},''),
 ('converted_zero','Owned:abc',{'themoviedb:0':base},{'convert_type':'themoviedb','media_dict':{'id':0}},''),
 ('converted_false_id','Owned:abc',{'themoviedb:False':base},{'convert_type':'themoviedb','media_dict':{'id':False}},''),
 ('known_false_no_convert','douban:120',{}, {'convert_type':'themoviedb','media_dict':{'id':120}},'Fallback Film'),
 ('unknown_name_fallback','Owned:abc',{}, {'convert_type':'themoviedb','media_dict':{}},'Fallback Film'),
 ('missing_prefix_name','legacy-key',{},None,'Fallback Film'),
 ('null_id_name','Owned:abc',{}, {'convert_type':'themoviedb','media_dict':{'id':None}},'Fallback Film'),
 ('empty_convert_type','Owned:abc',{}, {'convert_type':'','media_dict':{'id':120}},''),
 ('missing_id','Owned:abc',{}, {'convert_type':'themoviedb','media_dict':{'title':'unused'}},''),
 ('trim_alias','  TMDB  : 120 ',{'themoviedb:120':base},None,''),
]
cases=[]
async def main():
 for name,key,responses,mutation,title in recipes:
  calls=[]
  async def recognize(**kwargs):
   calls.append({'kind':'recognize','kwargs':normalize(kwargs)});return model(responses.get(str(kwargs['source'])+':'+str(kwargs['mediaid'])))
  async def by_name(meta,obtain_images=False):calls.append({'kind':'name','title':meta.title,'year':meta.year,'type':meta.type.value,'obtain_images':obtain_images});return model(base)
  async def images(item):calls.append({'kind':'images','tmdb_id':item.tmdb_id})
  async def send(event_type,data):
   calls.append({'kind':'event','type':event_type,'input':data.model_dump()})
   if mutation is None:return None
   for k,v in mutation.items():setattr(data,k,v)
   return types.SimpleNamespace(event_data=data)
  ns['eventmanager']=types.SimpleNamespace(async_send_event=send)
  ns['MediaChain']=lambda:types.SimpleNamespace(async_recognize_media=recognize,async_recognize_by_meta=by_name,async_obtain_images=images)
  ns['MetaInfo']=lambda title:types.SimpleNamespace(title=title,year=None,type=typ.UNKNOWN)
  out=await ns['detail'](mediaid=key,type_name='电影',title=title or None,year='2026')
  cases.append({'name':name,'key':key,'responses':responses,'mutation':mutation,'title':title,'year':'2026','calls':calls,'output':normalize(out)})
asyncio.run(main())
# Actual MetaBase fields/name setter plus actual async ordering and helper.
owner=one('app/core/meta/metabase.py',ast.ClassDef,'MetaBase')
body=[n for n in owner.body if isinstance(n,ast.AnnAssign) or isinstance(n,ast.FunctionDef) and n.name in ['__init__','name']]
ns.update(StringUtils=types.SimpleNamespace(is_all_chinese=lambda text:all(c==' ' or '\u4e00'<=c<='\u9fff' for c in text)),Self=object)
execute([ast.ClassDef(name='MetaBase',bases=[],keywords=[],body=body,decorator_list=[])],'<fixed:MetaBase input/name property>')
owner=one('app/chain/media.py',ast.ClassDef,'MediaChain');wanted=['async_select_recognize_source','_async_recognize_with_fallback_by_meta','async_recognize_help','_parse_recognize_event_number']
execute([ast.ClassDef(name='NameChain',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in wanted],decorator_list=[])],'<fixed:name fallback ordering/helper>')
name_cases=[]
async def names():
 for label,event in [('new_name',{'name':'Other.Film/other','year':'2026/2025'}),('same_name',{'name':'Original Film','year':'2020'}),('unknown',{'name':'Unknown'}),('no_name',{'year':'2026'}),('zero_season',{'name':'TV.Zero','year':'bad','season':0,'episode':'0'}),('negative',{'name':'TV.Negative','season':'-1','episode':False}),('numeric_year',{'name':'Other Film','year':2026}),('unicode_numbers',{'name':'TV.Unicode','year':'２０２６','season':'０'}),('plugin_first_name',{'name':'Other.Film','year':'2026'}),('plugin_first_unknown',{'name':'Unknown'}),('plugin_first_same',{'name':'Original Film','year':'2020'})]:
  meta=ns['MetaBase']('Original Film 2020');meta.title='Original Film 2020';meta.name='Original Film';meta.year='2020';meta.type=typ.MOVIE
  calls=[]
  async def recognize(**kwargs):
   item=kwargs['meta'];calls.append({'kind':'recognize','name':item.name,'title':item.title,'year':item.year,'type':item.type.value,'season':item.begin_season,'episode':item.begin_episode});return None if len(calls)==1 else model(base)
  async def send(kind,data):calls.append({'kind':'event','type':kind,'input':copy.deepcopy(data)});return types.SimpleNamespace(event_data=copy.deepcopy(event))
  ns['eventmanager']=types.SimpleNamespace(check=lambda kind:True,async_send_event=send)
  ns['settings'].RECOGNIZE_PLUGIN_FIRST=label.startswith('plugin_first')
  chain=ns['NameChain']();chain.async_recognize_media=recognize
  out=await chain._async_recognize_with_fallback_by_meta(meta)
  name_cases.append({'name':label,'plugin_first':ns['settings'].RECOGNIZE_PLUGIN_FIRST,'mutation':event,'calls':calls,'recognized':bool(out)})
asyncio.run(names())
dto=one('app/schemas/context.py',ast.ClassDef,'MediaInfo');dto.name='MediaResponse';ns.update(BaseModel=BaseModel);execute([dto],'<fixed:media detail response model>')
response_cases=[]
for label,value in [('default',{}),('typed',base),('invalid_title',{'title':123}),('bool_ids',{'tmdb_id':True,'season':False}),('coerce',{'tmdb_id':'120','vote_average':'8.2','adult':'true'}),('invalid_genre',{'genres':[3]}),('null_lists',{'names':None,'actors':None}),('required_null',{'release_dates':None}),('extra_private',{'tmdb_info':{'private':True},'logo_path':'extra-logo'})]:
 try:result=ns['MediaResponse'].model_validate(value).model_dump(mode='json');status=200
 except Exception:result=None;status=500
 response_cases.append({'name':label,'input':value,'status':status,'output':result})
out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','runtime':__import__('sys').version,'kind':__doc__,'cases':cases,'name_cases':name_cases,'response_cases':response_cases,'sources':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in ['app/api/endpoints/media.py','app/utils/media.py','app/schemas/event.py','app/schemas/context.py','app/chain/media.py','app/core/meta/metabase.py']}}
Path(__file__).with_name('opaque-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'detail and',len(name_cases),'actual name fallback cases')
