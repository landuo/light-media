"""Pinned storage selection -> transfer dispatch -> actual native naming.
Runs the exact __handle_transfer statements, dispatcher and FileManager wrapper,
and the exact get_rename_path with real Pydantic events/Jinja2. Native copying
and the plugin callback are owned physical boundaries; no old service runs.
"""
import ast,copy,json,os,tempfile,typing
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
ns,run=env['ns'],env['run'];parent,pos=env['parent'],env['pos']
start=next(i for i,n in enumerate(parent.body[:pos])if isinstance(n,ast.Assign)and any(isinstance(t,ast.Name)and t.id=='source_oper'for t in n.targets))
fn=ast.FunctionDef(name='consume_storage',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=copy.deepcopy(parent.body[start:pos+4]),decorator_list=[]);run([fn],'<fixed:storage-selection-to-transfer>')
p=Path(__file__).parents[1]/'standard-event-target/generate-transfer-rename-oracle.py';rename={'__file__':str(p)}
exec(compile(p.read_text().split("template='")[0],str(p),'exec'),rename)
rn=rename['ns'];rn['StorageBase']=typing.Any;rn['Callable']=typing.Callable
rename['execute']([n for n in ast.parse(rename['raw']['app/schemas/event.py']).body if isinstance(n,ast.ClassDef)and n.name=='StorageOperSelectionEventData'],'<fixed:storage event model>')
rn['StorageOperSelectionEventData'].model_rebuild(_types_namespace=rn)
ns['StorageOperSelectionEventData']=rn['StorageOperSelectionEventData'];ns['ChainEventType']=SimpleNamespace(StorageOperSelection='storage.operation')
Info,FileItem,Chain=ns['TransferInfo'],ns['FileItem'],ns['TransferChain'];rows=[]
for name,value,ext in [(n+suffix,v,ext)for suffix,ext in [('', 'mkv'),('_subtitle','srt'),('_audio','flac')]for n,v in [('model_true_effect','model'),('false',False),('none',None)]]:
 with tempfile.TemporaryDirectory(prefix='owned-naming-order-')as td:
  folder=Path(td);source=folder/f'Source.{ext}';source.write_bytes(b'owned naming source');target=folder/'Native'/f'Owned.{ext}';plugin_target=folder/'Plugin'/f'Returned.{ext}';trace=[];native_count=[0]
  item=FileItem(storage='local',type='file',path=str(source),name=source.name,extension=ext,size=source.stat().st_size)
  meta=ns['MetaBase']();meta.fields={'name':'Owned'};media=ns['MediaInfo']();media.type=SimpleNamespace(value='电影');media.title_year='Owned (2024)'
  def send(kind,data):trace.append(kind);return SimpleNamespace(event_data=data)
  ns['eventmanager']=SimpleNamespace(send_event=send);rn['eventmanager']=ns['eventmanager']
  def plugin(**kw):
   trace.append('plugin')
   if value=='model':
    plugin_target.parent.mkdir();os.link(source,plugin_target)
    return Info(success=True,target_item=FileItem(storage='local',type='file',path=str(plugin_target),name=plugin_target.name))
   return value
  class Native:
   def get_dest_path(self,**kw):return kw['target_path']
   def transfer_media(self,**kw):
    native_count[0]+=1
    target_path=rn['get_rename_path']('Native/{{ title }}.'+ext,{'title':'Owned'},path=folder,source_path=str(source),source_item=rn['FileItem'].model_validate(item.model_dump()))
    target_path.parent.mkdir();os.link(source,target_path)
    return Info(success=True,target_item=FileItem(storage='local',type='file',path=str(target_path),name=target_path.name))
  ns['TransHandler']=Native
  core=ns['FileManagerModule']();core.get_priority=lambda:0;core.get_name=lambda:'文件整理';core._FileManagerModule__get_storage_oper=lambda storage:object()
  chain=Chain();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'transfer':plugin}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda **kw:None);chain.eventmanager=SimpleNamespace(send_event=lambda*a:None)
  ns['task']=SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,target_directory=None,target_storage='local',target_path=folder,transfer_type='link',episodes_info=None,scrape=False,library_type_folder=False,library_category_folder=None,preview=False)
  ns['callback']=lambda task,info:(info.success,info.message)
  result=ns['consume_storage'](chain)
  rows.append({'name':name,'trace':trace,'native_calls':native_count[0],'result':result,'native_target_exists':target.exists(),'plugin_target_exists':plugin_target.exists()})
Path(__file__).with_name('transfer-naming-order-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/transfer.py:2004-2064','app/chain/__init__.py:1425-1477','app/modules/filemanager/__init__.py:415-541','app/modules/filemanager/transhandler.py:1221-1290'],'kind':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print(len(rows),'actual fixed storage/dispatch/native-name order cases')
