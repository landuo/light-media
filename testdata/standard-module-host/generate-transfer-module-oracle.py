"""Pinned ChainBase.transfer/dispatcher/FileManagerModule.transfer and task result consumption.
Only TransHandler/storage providers and installed plugin callbacks are controlled;
source FileManager validation, model defaults, argument forwarding and callback
branches run unchanged. Effects use real disposable local files.
"""
import ast,copy,json,os,tempfile
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-browser-storage-oracle.py');env={'__file__':str(p)};exec(compile(p.read_text().split('base={')[0],str(p),'exec'),env)
ns,one,run,FileItem=env['ns'],env['one'],env['run'],env['FileItem']
for path,name in [('app/schemas/system.py','TransferDirectoryConf'),('app/schemas/tmdb.py','TmdbEpisode'),('app/schemas/transfer.py','TransferInfo')]:
 run([one(path,ast.ClassDef,name)],'<fixed:'+name+'>');ns[name].model_rebuild(_types_namespace=ns)
ns['Callable']=object
owner=one('app/chain/__init__.py',ast.ClassDef,'ChainBase');transfer=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='transfer')
run([ast.ClassDef(name='TransferChain',bases=[ast.Name(id='MediaChain',ctx=ast.Load())],keywords=[],body=[transfer],decorator_list=[])],'<fixed:transfer wrapper>')
owner=one('app/modules/filemanager/__init__.py',ast.ClassDef,'FileManagerModule');run([ast.ClassDef(name='FileManagerModule',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='transfer'],decorator_list=[])],'<fixed:FileManager transfer>')
# Actual final task statements, after storage selection: call, falsey check,
# callback and direct return. Scheduling/locks are not simulated here.
owner=one('app/chain/transfer.py',ast.ClassDef,'TransferChain');method=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.lineno<=2042<=n.end_lineno)
parent=None;pos=None
for node in ast.walk(method):
 if not isinstance(node,(ast.Try,ast.With,ast.FunctionDef)):continue
 for i,n in enumerate(node.body):
  if isinstance(n,ast.AnnAssign)and isinstance(n.target,ast.Name)and n.target.id=='transferinfo'and isinstance(n.value,ast.Call)and isinstance(n.value.func,ast.Attribute)and n.value.func.attr=='transfer':parent=node;pos=i;break
 if parent is not None:break
assert parent is not None
nodes=copy.deepcopy(parent.body[pos:pos+4]);fn=ast.FunctionDef(name='consume',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=nodes,decorator_list=[]);run([fn],'<fixed:handle_task result statements>')
Info=ns['TransferInfo'];Chain=ns['TransferChain'];cases=[]
for name,kind,value,effect,throws in [('none','raw',None,False,False),('false','raw',False,False,False),('empty_list','raw',[],False,False),('list','raw',['plugin'],False,False),('empty_dict','raw',{},False,False),('dict_success','raw',{'success':True},False,False),('model_false','model',{'success':False,'message':'owned veto'},False,False),('model_true_without_effect','model',{'success':True},False,False),('model_true_effect','model',{'success':True},True,False),('model_false_effect','model',{'success':False,'message':'owned veto after effect'},True,False),('throw','raw',None,False,True),('throw_after_effect','raw',None,True,True)]:
 with tempfile.TemporaryDirectory(prefix='owned-transfer-source-')as td:
  folder=Path(td);source=folder/'Source.mkv';target=folder/'Owned.mkv';source.write_bytes(b'owned transfer content');trace=[];errors=[];callbacks=[]
  item=FileItem(storage='local',type='file',path=str(source),name=source.name,size=source.stat().st_size)
  meta=ns['MetaBase']();meta.fields={'name':'Owned'};media=ns['MediaInfo']();media.type=SimpleNamespace(value='电影');media.title_year='Owned (2024)'
  def plugin(**kw):
   trace.append({'role':'plugin','keys':sorted(kw),'transfer_type':kw['transfer_type'],'preview':kw['preview']})
   if effect:os.link(source,target)
   if throws:raise RuntimeError('owned callback failure')
   return Info(**copy.deepcopy(value))if kind=='model'else copy.deepcopy(value)
  class Handler:
   def get_dest_path(self,**kw):return kw['target_path']
   def transfer_media(self,**kw):
    trace.append({'role':'native_handler','keys':sorted(kw)})
    if target.exists():return Info(success=False,fileitem=item,message='owned existing target')
    os.link(source,target)
    return Info(success=True,fileitem=item,target_item=FileItem(storage='local',type='file',path=str(target),name=target.name,size=target.stat().st_size),target_diritem=FileItem(storage='local',type='dir',path=str(folder)+'/',name=folder.name),transfer_type='link',file_count=1,file_list=[str(source)],file_list_new=[str(target)],total_size=source.stat().st_size)
  ns['TransHandler']=Handler
  core=ns['FileManagerModule']();core.get_priority=lambda:0;core.get_name=lambda:'文件整理';core._FileManagerModule__get_storage_oper=lambda storage:object()
  chain=Chain();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'transfer':plugin}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=SimpleNamespace(send_event=lambda*a:None)
  ns['task']=SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,target_directory=None,target_storage='local',target_path=folder,transfer_type='link',episodes_info=None,scrape=False,library_type_folder=False,library_category_folder=None,preview=False);ns['source_oper']=None;ns['target_oper']=None
  def callback(task,result):callbacks.append(type(result).__name__);return result.success,result.message
  ns['callback']=callback
  error=None
  try:result=ns['consume'](chain)
  except Exception as e:result=None;error=type(e).__name__
  result_raw={'__plugin_transfer_info':copy.deepcopy(value)}if kind=='model'else value
  cases.append({'name':name,'kind':kind,'plugin':result_raw,'effect':effect,'throws':throws,'result':result,'exception':error,'callbacks':callbacks,'target_exists':target.exists(),'same_inode':target.exists()and target.stat().st_ino==source.stat().st_ino,'native_calls':sum(x['role']=='native_handler'for x in trace),'trace':trace,'error_titles':[x.get('title')for x in errors]})
Path(__file__).with_name('transfer-module-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/__init__.py:1425-1477','app/modules/filemanager/__init__.py:415-541','app/chain/transfer.py:2042-2064','app/schemas/transfer.py:TransferInfo'],'kind':__doc__,'cases':cases},ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual pinned transfer call/dispatch/caller cases')
