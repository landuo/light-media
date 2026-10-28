"""Pinned download_single event construction/consumption, not rewritten expected values.
The excerpt stops before material I/O and the directory host boundary. Media
supplement, callback registry and meta/provider payloads are controlled inputs.
"""
import ast,copy,dataclasses,hashlib,json,pathlib,sys,types
from typing import Any,Optional,Set
from pydantic import BaseModel,Field
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
paths=['app/chain/download.py','app/schemas/event.py','app/core/context.py']
raw={p:(root/p).read_text()for p in paths}
ns=dict(BaseModel=BaseModel,Field=Field,Any=Any,Optional=Optional,Set=Set,dataclass=dataclasses.dataclass,MessageChannel=Any)
def execute(nodes,name):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),name,'exec'),ns)
execute([n for n in ast.parse(raw[paths[1]]).body if isinstance(n,ast.ClassDef)and n.name in ['BaseEventData','ChainEventData','ResourceDownloadEventData']],paths[1])
ns['ResourceDownloadEventData'].model_rebuild(_types_namespace=ns)
execute([n for n in ast.parse(raw[paths[2]]).body if isinstance(n,ast.ClassDef)and n.name=='Context'],paths[2])
fn=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw[paths[0]]))if isinstance(n,ast.FunctionDef)and n.name=='download_single'))
boundary=next(i for i,n in enumerate(fn.body)if isinstance(n,ast.If)and ast.unparse(n.test)=='save_path is not None')
select_start=next(i for i,n in enumerate(fn.body)if isinstance(n,ast.Assign)and ast.unparse(n.targets[0])=='download_episodes')
select_end=next(i for i,n in enumerate(fn.body)if isinstance(n,ast.Assign)and ast.unparse(n.targets[0])=='_folder_name')
fn.body=fn.body[:boundary]+fn.body[select_start:select_end]+[ast.Return(value=ast.Dict(keys=[ast.Constant('save_path'),ast.Constant('context')],values=[ast.Name('save_path',ast.Load()),ast.Name('context',ast.Load())]))]
execute([fn],paths[0])
broadcast=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw[paths[0]]))if isinstance(n,ast.Expr)and isinstance(n.value,ast.Call)and ast.unparse(n.value.func)=='self.eventmanager.send_event'and len(n.value.args)>0 and ast.unparse(n.value.args[0])=='EventType.DownloadAdded'))
emit=ast.parse('def emit(self,context,_hash,username,_downloader,episodes,_meta,source):\n pass').body[0];emit.body=[broadcast];execute([emit],paths[0])
ns['EventType']=types.SimpleNamespace(DownloadAdded='download.added')
ns['MediaChain']=lambda:types.SimpleNamespace(supplement_tmdb_info=lambda media,meta:media)
ns['ChainEventType']=types.SimpleNamespace(ResourceDownload='resource.download')
ns['logger']=types.SimpleNamespace(debug=lambda*a:None)
ns['StringUtils']=types.SimpleNamespace(format_ep=lambda e:'controlled unused formatting boundary')
def clean(v):
 if isinstance(v,BaseModel):return {k:clean(x)for k,x in v.__dict__.items()}
 if isinstance(v,types.SimpleNamespace)or dataclasses.is_dataclass(v):return {k:clean(x)for k,x in vars(v).items()}
 if isinstance(v,set):return sorted(v)
 if isinstance(v,list):return [clean(x)for x in v]
 if isinstance(v,dict):return {k:clean(x)for k,x in v.items()}
 return v
cases=[]
samples=[('normal',{},None),('cancel',{'cancel':True,'reason':'owned veto'},None),('cancel_mutation_before_error',{'cancel':True,'reason':'owned error veto'},None),('path',{'options':{'save_path':'/owned-downloads/changed'}},None),('none_path',{'options':{'save_path':None}},None),('ignored_outputs',{'downloader':'unselected','episodes':{99}},None),('selected',{},set([4,2])),('empty_selection',{},set()),('empty_options',{'options':{}},None)]
for name,patch,selected in samples:
 meta=types.SimpleNamespace(episode_list=[1,2],org_string='Owned.S01E01-E02',extra_private={'marker':'meta'})
 media=types.SimpleNamespace(category='外语电视剧',tmdb_id=123,tmdb_info={'marker':'private media'})
 torrent=types.SimpleNamespace(site_downloader='Site downloader',title='Owned torrent',site_cookie='synthetic-cookie',enclosure='https://owned.invalid/download?id=1',extra_private={'marker':'torrent'})
 context=ns['Context'](meta_info=meta,media_info=media,torrent_info=torrent)
 events=[]
 def send(kind,data):
  events.append({'type':kind,'input':clean(data)})
  for k,v in patch.items():setattr(data,k,copy.deepcopy(v))
  return types.SimpleNamespace(event_data=data)
 ns['eventmanager']=types.SimpleNamespace(send_event=send)
 result=ns['download_single'](types.SimpleNamespace(),context,episodes=selected,source='Manual',downloader=None,save_path='/owned-downloads',return_detail=True)
 cancelled=isinstance(result,tuple)
 if not cancelled:
  collector=types.SimpleNamespace(send_event=lambda kind,data:events.append({'type':kind,'input':clean(data)}))
  ns['emit'](types.SimpleNamespace(eventmanager=collector),context,'owned-accepted-hash',None,'Actual downloader',selected,meta,'Manual')
 cases.append(dict(name=name,patch=clean(patch),fail=name.endswith('before_error'),selected=clean(selected),context=clean(ns['Context'](meta_info=meta,media_info=media,torrent_info=torrent)),events=events,cancelled=cancelled,result=clean(result)if not cancelled else list(result)))
pathlib.Path(__file__).with_name('resource-download-oracle.json').write_text(json.dumps(dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',python=sys.version,classification=__doc__,source_sha256={p:hashlib.sha256(s.encode()).hexdigest()for p,s in raw.items()},cases=cases),ensure_ascii=False,indent=2)+'\n')
print('fixed resource download event cases',len(cases))
