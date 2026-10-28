"""Pinned existence caller plus actual None/list/terminal system dispatcher.
Native inventory and plugin values are owned dependencies; native errors run
the fixed system error handler before the caller consumes the retained list.
"""
import copy,json
from pathlib import Path
from types import SimpleNamespace as NS
p=Path(__file__).with_name('generate-media-exists-oracle.py');scope={'__file__':str(p)}
exec(compile(p.read_text().split('cases=[]')[0],str(p),'exec'),scope)
ns=scope['ns']
p=Path(__file__).with_name('generate-metadata-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('cases=[]')[0],str(p),'exec'),env)
rows=[]
for kind,result in [('movie',[]),('movie',[False]),('tv',[]),('tv',[False]),('tv',False)]:
 for native_error in [False,True]:
  calls=[];errors=[];events=[]
  def plugin(**kw): calls.append('plugin');return copy.deepcopy(result)
  class FileManagerModule:
   def get_priority(self):return 0
   def get_name(self):return '文件整理'
   def media_exists(self,mediainfo,itemid=None,server=None):
    calls.append('native')
    if native_error:raise RuntimeError('owned inventory unavailable')
    return ns['ExistMediaInfo'](seasons={1:[1,2,3]})
  dispatcher=env['ns']['MediaChain']();dispatcher.pluginmanager=NS(get_plugin_modules=lambda:{('Owned','Owned'):{'media_exists':plugin}});dispatcher.modulemanager=NS(get_running_modules=lambda name:[FileManagerModule()]);dispatcher.messagehelper=NS(put=lambda **kw:errors.append(kw));dispatcher.eventmanager=NS(send_event=lambda *args:events.append(args))
  chain=ns['DownloadChain']();chain.run_module=dispatcher.run_module
  mt=ns['MediaType'].MOVIE if kind=='movie'else ns['MediaType'].TV
  media=NS(type=mt,tmdb_id=900043,title='Owned',title_year='Owned (2024)',seasons={1:[1,2,3]},season=1)
  try:
   satisfied,missing=chain.get_no_exists_info(NS(sea='S01',season_list=[1]),media)
   missing={str(k):{str(s):v.model_dump(mode='json')for s,v in seasons.items()}for k,seasons in missing.items()};error=None
  except Exception as e:satisfied,missing,error=None,None,type(e).__name__
  rows.append(dict(name=kind+'-'+json.dumps(result)+'-'+str(native_error),kind=kind,plugin=result,native_error=native_error,calls=calls,messages=errors,system_errors=len(events),satisfied=satisfied,missing=missing,error=error))
Path(__file__).with_name('media-exists-dispatch-source-oracle.json').write_text(json.dumps({'fixed_backend':scope['FIXED'],'source':['app/chain/__init__.py:298-470,1606-1621','app/chain/download.py:1586-1725'],'boundary':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n');print(len(rows),'fixed native existence dispatch cases')
