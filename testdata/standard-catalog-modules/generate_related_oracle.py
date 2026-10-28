"""Pinned detail-card endpoints/wrappers/async dispatcher/MediaPerson and MediaInfo.
Registry and native provider returns are controlled. No remote service/runtime.
"""
import ast,asyncio,copy,hashlib,json,subprocess,types
from pathlib import Path
from pydantic import TypeAdapter, BaseModel, Field
base=Path(__file__).with_name('generate_images_oracle.py');env={'__file__':str(base)}
exec(compile(base.read_text().split("images={'backdrops'")[0],str(base),'exec'),env)
ns=env['ns'];root=env['root'];one=env['one'];execute=env['execute'];fixed='8a83efeacd3e26b82be495e17d50146e31c96a60'
paths=['app/chain/__init__.py','app/chain/tmdb.py','app/chain/douban.py','app/chain/bangumi.py','app/api/endpoints/tmdb.py','app/api/endpoints/douban.py','app/api/endpoints/bangumi.py','app/schemas/context.py']
sources={p:subprocess.check_output(['git','show',fixed+':'+p],cwd=root)for p in paths}
for p,b in sources.items():assert (root/p).read_bytes()==b,p
ns.update(BaseModel=BaseModel,Field=Field)
execute([one('app/schemas/context.py',ast.ClassDef,'MediaPerson')],'<fixed MediaPerson>');ns['schemas'].MediaPerson=ns['MediaPerson']
for source,klass in [('tmdb','TmdbChain'),('douban','DoubanChain'),('bangumi','BangumiChain')]:
 owner=one('app/chain/'+source+'.py',ast.ClassDef,klass)
 methods=[copy.deepcopy(n)for n in owner.body if isinstance(n,ast.AsyncFunctionDef)and n.name in ['async_movie_credits','async_tv_credits','async_movie_recommend','async_tv_recommend','async_bangumi_credits','async_bangumi_recommend']]
 execute([ast.ClassDef(name=klass+'Owned',bases=[ast.Name(id='ChainBase',ctx=ast.Load())],keywords=[],body=methods,decorator_list=[])],'<fixed related wrapper>')
 for part in ['credits','recommend']:
  fn=one('app/api/endpoints/'+source+'.py',ast.AsyncFunctionDef,source+'_'+part);fn.decorator_list=[];execute([fn],'<fixed related endpoint>')
def media(title):
 item=ns['MediaInfo']();item.from_dict({'source':'themoviedb','tmdb_id':120,'type':'电影','title':title,'year':'2026','media_id':'120'});return item
cases=[]
async def main():
 recipes=[('tmdb','movie','credits',label,value,fail)for label,value,fail in [('none',None,False),('list',[{'id':122,'name':'plugin actor'}],False),('empty_list',[],False),('false',False,False),('dict',{},False),('invalid',9,False),('throw',None,True)]]
 recipes += [('tmdb','tv','recommend',label,value,False)for label,value in [('none',None),('false',False),('list',[{'source':'themoviedb','tmdb_id':123,'type':'电影','title':'plugin recommended','year':'2026'}]),('invalid',9)]]
 recipes += [('douban','tv','credits','false',False,False),('douban','movie','credits','none',None,False),('douban','movie','recommend','none',None,False),('bangumi','tv','credits','list',[{'id':i+200,'name':'plugin '+str(i)}for i in range(25)],False),('bangumi','tv','recommend','none',None,False)]
 for source,kind,part,label,value,fail in recipes:
  calls=[];method='async_'+source+'_'+(kind+'_'if source!='bangumi'else'')+part
  async def plugin(*args,**kw):
   calls.append({'kind':'source','method':method,'kwargs':kw})
   if fail:raise ValueError('owned related failure')
   if part=='recommend'and isinstance(value,list):
    out=[]
    for v in value:item=ns['MediaInfo']();item.from_dict(v);out.append(item)
    return out
   return copy.deepcopy(value)
  async def clone(*args,**kw):calls.append({'kind':'clone','method':method,'kwargs':kw});return None
  async def native(*args,**kw):
   calls.append({'kind':'native','method':method,'kwargs':kw})
   return [ns['MediaPerson'](id=120,name='native actor',source=source)]if part=='credits'else [media('native recommended')]
  core=types.SimpleNamespace(get_priority=lambda:0,get_name=lambda:source);setattr(core,method,native)
  klass={'tmdb':'TmdbChain','douban':'DoubanChain','bangumi':'BangumiChain'}[source];chain=ns[klass+'Owned']();chain.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('source','Source'):{method:plugin},('clone','Clone'):{method:clone}});chain.modulemanager=types.SimpleNamespace(get_running_modules=lambda m:[core]);chain.messagehelper=types.SimpleNamespace(put=lambda **kw:None);chain.eventmanager=types.SimpleNamespace(send_event=lambda *a:None);ns[klass]=lambda:chain
  args={source+'id': '120'if source=='douban'else 120}
  if source!='bangumi':args['type_name']='电影'if kind=='movie'else'电视剧'
  try:
   result=await ns[source+'_'+part](**args)
   if part=='credits':result=[x.model_dump(mode='json')for x in TypeAdapter(list[ns['MediaPerson']]).validate_python(result)]
   status=200
  except Exception:result=None;status=500
  cases.append({'source':'themoviedb'if source=='tmdb'else source,'kind':kind,'part':'recommendations'if part=='recommend'else part,'name':label,'method':method,'plugin':value,'fail':fail,'status':status,'output':result,'calls':calls})
asyncio.run(main())
Path(__file__).with_name('related-source-oracle.json').write_text(json.dumps({'fixed_backend':fixed,'boundary':__doc__,'source_sha256':{p:hashlib.sha256(b).hexdigest()for p,b in sources.items()},'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed related pipeline cases')
