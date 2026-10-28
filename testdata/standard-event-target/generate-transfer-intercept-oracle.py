"""Pinned __transfer_file body; counted storage/command protocol boundaries."""
import ast,copy,hashlib,json,pathlib,sys,types
from typing import Any,Optional,List,Tuple
from pydantic import BaseModel,Field
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
paths=['app/schemas/file.py','app/schemas/event.py','app/schemas/transfer.py','app/modules/filemanager/transhandler.py']
raw={p:(root/p).read_text()for p in paths}
ns=dict(BaseModel=BaseModel,Field=Field,Any=Any,Optional=Optional,List=List,Tuple=Tuple,Path=pathlib.Path,MetaBase=Any,MediaInfo=Any,StorageBase=Any)
def execute(nodes,name):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),name,'exec'),ns)
for path,names in [(paths[0],['FileURI','FileItem']),(paths[1],['BaseEventData','ChainEventData','TransferInterceptEventData']),(paths[2],['TransferInfo'])]:
 execute([n for n in ast.parse(raw[path]).body if isinstance(n,ast.ClassDef)and n.name in names],path)
for name in ['FileURI','FileItem','TransferInterceptEventData','TransferInfo']:ns[name].model_rebuild(_types_namespace=ns)
function=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw[paths[3]]))if isinstance(n,ast.FunctionDef)and n.name=='__transfer_file'))
execute([function],paths[3]);ns['logger']=types.SimpleNamespace(info=lambda*a:None,debug=lambda*a:None,warn=lambda*a:None)
ns['ChainEventType']=types.SimpleNamespace(TransferIntercept='transfer.intercept')
cases=[]
for name,patch,fail,over in [('normal',{},False,False),('cancel',dict(cancel=True,reason='owned cancellation'),False,False),('cancel_failure',dict(cancel=True,reason='mutation survives exception'),True,False),('false_cancel',dict(cancel=False,reason='ignored'),False,False),('ignored_path',dict(target_path='/outside/ignored.mkv',transfer_type='move'),False,False),('truthy_cancel',dict(cancel='cancel',reason='truthy'),False,False),('overwrite_flag',{},False,True),('empty_reason',dict(cancel=True,reason=''),False,False)]:
 calls=[];events=[]
 item=ns['FileItem'](storage='local',type='file',path='/owned-source/Old.mkv',name='Old.mkv',basename='Old',extension='mkv',size=10,modify_time=1712345678.1234567)
 target=ns['FileItem'](storage='local',path='/owned-target/New.mkv',name='New.mkv',basename='New',extension='mkv',size=10)
 def send(kind,data):
  events.append(dict(type=kind,input=data.model_dump(mode='json')))
  for key,value in patch.items():setattr(data,key,copy.deepcopy(value))
  # Existing EventManager's safe invoke retains writes before an exception.
  return types.SimpleNamespace(event_data=data)
 def command(**kw):
  calls.append(dict(type='command',target=str(kw['target_file']),transfer_type=kw['transfer_type']));return target,''
 def update(**kw):
  calls.append(dict(type='update',file_count=kw['file_count'],total_size=kw['total_size']))
 ns['eventmanager']=types.SimpleNamespace(send_event=send)
 owner=types.SimpleNamespace(__transfer_command=command,__update_result=update)
 result=ns['TransferInfo']()
 value,error=ns['__transfer_file'](owner,item,dict(title='meta'),dict(title='media'),None,types.SimpleNamespace(get_item=lambda p:None), 'local',pathlib.Path('/owned-target/New.mkv'),'link',result,over)
 cases.append(dict(name=name,patch=patch,fail=fail,over=over,events=events,cancelled=value is None,reason=error,calls=calls))
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',python=sys.version,kind='actual fixed file-transfer caller/Pydantic; storage and command are counted protocol boundaries, not real filesystem writes',source_sha256={p:hashlib.sha256(s.encode()).hexdigest()for p,s in raw.items()},cases=cases)
pathlib.Path(__file__).with_name('transfer-intercept-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print('fixed file interception cases',len(cases))
