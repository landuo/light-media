"""Pinned ChainBase sync/async recognition request normalization. The outer
module return is None and unrelated external shared-recognition is disabled.
"""
from pathlib import Path
import ast,asyncio,contextlib,json,types
p=Path(__file__).with_name('generate_images_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split("images={'backdrops'")[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute']
@contextlib.contextmanager
def fresh(value):yield
@contextlib.asynccontextmanager
async def async_fresh(value):yield
ns.update(fresh=fresh,async_fresh=async_fresh)
owner=one('app/chain/__init__.py',ast.ClassDef,'ChainBase');wanted=['_resolve_media_source_params','recognize_media','async_recognize_media'];execute([ast.ClassDef(name='Requests',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in wanted],decorator_list=[])],'<fixed:request normalization wrappers>')
recipes=[('native_id',{}, {'tmdbid':120}),('explicit_type',{'type':'电视剧'},{'tmdbid':120,'mtype':'电影'}),('meta_ids',{'type':'电视剧','tmdbid':120,'doubanid':'130','media_id':'original-id'},{}),('meta_source_priority',{'type':'电影','media_source':'douban','doubanid':'130'},{'tmdbid':120}),('explicit_source',{'type':'电视剧','media_source':'douban','media_id':'130'},{'source':'themoviedb','mediaid':'120'}),('unknown_prefix',{'type':'电影','tmdbid':120},{'source':' Owned ','mediaid':'opaque /?:id'}),('zero_fallback',{'tmdbid':121,'media_id':'122'},{'tmdbid':0,'mediaid':0}),('unicode_id',{}, {'source':'TMDB','mediaid':'１２３'}),('huge_id',{}, {'source':'tmdb','mediaid':'9'*80}),('super_digit_invalid',{}, {'source':'tmdb','mediaid':'²'}),('negative_no_source',{}, {'tmdbid':-1}),('float_source_id',{}, {'source':'tmdb','mediaid':120.0}),('episode_group',{'type':'电视剧','episode_group':'meta-group'},{'episode_group':'','cache':False}),('none_source_type',{'type':'电影'},{}),('bool_tmdb',{}, {'tmdbid':True})]
def clean(v):
 if isinstance(v,ns['Enum']):return v.value
 if isinstance(v,types.SimpleNamespace):return {k:clean(v) for k,v in vars(v).items()}
 if isinstance(v,dict):return {k:clean(v) for k,v in v.items()}
 return v
cases=[]
async def main():
 for method in ['recognize_media','async_recognize_media']:
  for name,meta,explicit in recipes:
   obj=ns['Requests']();calls=[];obj._can_use_media_recognize_share=lambda *a:False
   def record(method,**kwargs):calls.append({'method':method,'kwargs':clean(kwargs)});return None
   async def arecord(method,**kwargs):return record(method,**kwargs)
   obj.run_module=record;obj.async_run_module=arecord
   actual_meta=types.SimpleNamespace(**meta) if meta else None
   if actual_meta and hasattr(actual_meta,'type'):actual_meta.type=ns['MediaType'](actual_meta.type)
   kwargs=dict(explicit)
   if kwargs.get('mtype'):kwargs['mtype']=ns['MediaType'](kwargs['mtype'])
   try:
    if method.startswith('async'):await getattr(obj,method)(meta=actual_meta,**kwargs)
    else:getattr(obj,method)(meta=actual_meta,**kwargs)
    state='ok'
   except Exception as error:state=type(error).__name__
   cases.append({'name':name,'method':method,'meta':meta or None,'explicit':explicit,'calls':calls,'state':state})
asyncio.run(main())
Path(__file__).with_name('recognition-kwargs-source-oracle.json').write_text(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':__doc__,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual sync/async request normalization cases')
