"""Actual fixed media.search caller sorting/page slice with real MediaInfo
models. The MediaChain return is controlled; no provider/plugin engine is mocked
as proof in this caller-only oracle.
"""
from pathlib import Path
import ast,asyncio,json,types
p=Path(__file__).with_name('generate_images_oracle.py');scope={'__file__':str(p)};exec(compile(p.read_text().split("images={'backdrops'")[0],str(p),'exec'),scope)
ns=scope['ns'];root=scope['root'];one=scope['one'];execute=scope['execute'];media=ns['MediaInfo'];ns['schemas'].MediaInfo=media;ns['schemas'].MediaPerson=ns['Any'];ns['MediaSource']=ns['Any']
fn=one('app/api/endpoints/media.py',ast.AsyncFunctionDef,'search');fn.decorator_list=[];execute([fn],'<fixed:search caller source ordering/page slice>')
inputs=[{'source':'douban','douban_id':'2','type':'电影','title':'D2'},{'source':'themoviedb','tmdb_id':120,'type':'电影','title':'T120'},{'source':'owned','media_id':'opaque /?:1','type':'电影','title':'Opaque'},{'source':'douban','douban_id':'3','type':'电影','title':'D3'},{'source':'themoviedb','tmdb_id':121,'type':'电视剧','title':'T121'}]
models=[]
for fields in inputs:item=media();item.from_dict(fields);models.append(item)
cases=[]
async def main():
 for name,order,page,count in [('ordered_page1','douban,themoviedb',1,2),('ordered_page2','douban,themoviedb',2,2),('ordered_page3','douban,themoviedb',3,2),('page_zero','douban,themoviedb',0,2),('negative_page','douban,themoviedb',-1,2),('zero_count','douban,themoviedb',1,0),('negative_count','douban,themoviedb',1,-2),('empty_order','',1,8),('duplicate_order','themoviedb,douban,themoviedb',1,8),('explicit_order','owned,themoviedb',1,8)]:
  ns['settings'].SEARCH_SOURCE=order;calls=[]
  async def search(**kwargs):calls.append(kwargs);return None,models
  ns['MediaChain']=lambda:types.SimpleNamespace(async_search=search)
  output=await ns['search'](title='Owned',type='media',page=page,count=count,source=None)
  cases.append({'name':name,'order':order,'page':page,'count':count,'calls':calls,'output':output})
asyncio.run(main())
Path(__file__).with_name('search-paging-source-oracle.json').write_text(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':__doc__,'input':inputs,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual fixed caller ordering/page cases')
