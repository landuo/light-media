"""Pinned batch_download ResourceSelection construction and not-None consumption.
Stops before TorrentHelper.sort_torrents; sorting/material/history remain host
boundaries, not fake successful downloads. The actual Context/Pydantic classes
are used, with explicitly controlled MetaInfo/MediaInfo/TorrentInfo values.
"""
import ast,copy,dataclasses,hashlib,json,pathlib,sys,types
from typing import Any,Optional,List
from pydantic import BaseModel,Field
root=pathlib.Path('/Users/accidia/projects/MoviePilot');paths=['app/chain/download.py','app/schemas/event.py','app/core/context.py'];raw={p:(root/p).read_text()for p in paths}
ns=dict(BaseModel=BaseModel,Field=Field,Any=Any,Optional=Optional,List=List,dataclass=dataclasses.dataclass)
def execute(nodes,name):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),name,'exec'),ns)
execute([n for n in ast.parse(raw[paths[1]]).body if isinstance(n,ast.ClassDef)and n.name in ['BaseEventData','ChainEventData','ResourceSelectionEventData']],paths[1]);ns['ResourceSelectionEventData'].model_rebuild(_types_namespace=ns)
execute([n for n in ast.parse(raw[paths[2]]).body if isinstance(n,ast.ClassDef)and n.name=='Context'],paths[2])
source=next(n for n in ast.walk(ast.parse(raw[paths[0]]))if isinstance(n,ast.FunctionDef)and n.name=='batch_download')
start=next(i for i,n in enumerate(source.body)if isinstance(n,ast.Assign)and ast.unparse(n.targets[0])=='event_data')
end=next(i for i,n in enumerate(source.body)if isinstance(n,ast.Assign)and ast.unparse(n.targets[0])=='contexts')
fn=ast.parse('def select(self,contexts,downloader,source):\n pass').body[0];fn.body=copy.deepcopy(source.body[start:end])+[ast.Return(value=ast.Name('contexts',ast.Load()))];execute([fn],paths[0])
ns['ChainEventType']=types.SimpleNamespace(ResourceSelection='resource.selection');ns['logger']=types.SimpleNamespace(debug=lambda*a:None)
def clean(v):
 if isinstance(v,BaseModel):return {k:clean(x)for k,x in vars(v).items()}
 if isinstance(v,types.SimpleNamespace)or dataclasses.is_dataclass(v):return {k:clean(x)for k,x in vars(v).items()}
 if isinstance(v,list):return [clean(x)for x in v]
 if isinstance(v,dict):return {k:clean(x)for k,x in v.items()}
 return v
def context(n):return ns['Context'](meta_info=types.SimpleNamespace(org_string='Owned '+str(n),episode_list=[n]),media_info=types.SimpleNamespace(tmdb_id=123,tmdb_info={'private':n}),torrent_info=types.SimpleNamespace(site=1,title='Owned '+str(n),enclosure='https://owned.invalid/download?id='+str(n)),resource_source='search')
cases=[]
for name,patch,fail in [('unchanged',{},False),('false_updated',{'updated':False,'updated_contexts':[]},False),('true_none',{'updated':True,'updated_contexts':None},False),('true_empty',{'updated':True,'updated_contexts':[]},False),('reverse',{'updated':True,'updated_contexts':[context(2),context(1)]},False),('replace',{'updated':True,'updated_contexts':[context(3)]},False),('ignored_input',{'contexts':[]},False),('mutate_before_error',{'updated':True,'updated_contexts':[]},True),('invalid_scalar',{'updated':True,'updated_contexts':42},False)]:
 events=[];contexts=[context(1),context(2)]
 def send(kind,data):
  events.append({'type':kind,'input':clean(data)})
  for k,v in patch.items():setattr(data,k,copy.deepcopy(v))
  return types.SimpleNamespace(event_data=data)
 ns['eventmanager']=types.SimpleNamespace(send_event=send)
 error=None;result=None
 try:result=ns['select'](types.SimpleNamespace(),contexts,None,'Subscribe|owned')
 except Exception as e:error=type(e).__name__
 cases.append(dict(name=name,patch=clean(patch),fail=fail,input=clean(contexts),events=events,result=clean(result),error=error))
pathlib.Path(__file__).with_name('resource-selection-oracle.json').write_text(json.dumps(dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',python=sys.version,classification=__doc__,source_sha256={p:hashlib.sha256(s.encode()).hexdigest()for p,s in raw.items()},cases=cases),ensure_ascii=False,indent=2)+'\n');print('fixed resource selection cases',len(cases))
