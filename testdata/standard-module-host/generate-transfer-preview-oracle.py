"""Pinned transfer dispatcher, FileManager, native file-preview branch and preview callback.
Storage selection and rename rendering are controlled boundaries; the real
TransHandler preview helpers/branch and real nested _preview_callback execute.
All preexisting targets and source files are disposable local test files.
"""
import ast,copy,json,tempfile
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
ns,one,run=env['ns'],env['one'],env['run'];Info,Chain,FileItem=ns['TransferInfo'],ns['TransferChain'],ns['FileItem']
owner=one('app/modules/filemanager/transhandler.py',ast.ClassDef,'TransHandler')
media=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='transfer_media')
branch=next(n for n in ast.walk(media)if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preview'and n.lineno==389)
preview=ast.FunctionDef(name='transfer_media',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self')],kwonlyargs=[],kw_defaults=[],defaults=[],kwarg=ast.arg(arg='kw')),body=copy.deepcopy(branch.body),decorator_list=[])
run([ast.ClassDef(name='TransHandler',bases=[],keywords=[],body=[copy.deepcopy(n)for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in ['__build_preview_item','__update_result']]+[preview],decorator_list=[])],'<fixed:native file preview>')
Handler=ns['TransHandler'];Handler.get_dest_path=lambda self,**kw:kw['target_path']
owner=one('app/chain/transfer.py',ast.ClassDef,'TransferChain')
do=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='do_transfer')
callback=copy.deepcopy(next(n for n in ast.walk(do)if isinstance(n,ast.FunctionDef)and n.name=='_preview_callback'));run([callback],'<fixed:preview callback>')
cases=[]
for name,value,kind,throws in [('none',None,'raw',False),('false',False,'raw',False),('empty_list',[],'raw',False),('list',['plugin'],'raw',False),('empty_dict',{},'raw',False),('dict_success',{'success':True},'raw',False),('model_false',{'success':False,'message':'owned preview veto'},'model',False),('model_default',{},'model',False),('model_target',{'success':True,'target_item':{'storage':'local','type':'file','path':'{root}/custom/Preview.mkv','name':'Preview.mkv'}},'model',False),('throw',None,'raw',True)]:
 with tempfile.TemporaryDirectory(prefix='owned-preview-source-')as td:
  folder=Path(td);source=folder/'Source.mkv';source.write_bytes(b'owned preview content');target=folder/'Owned (2024)'/'Source.mkv';target.parent.mkdir();target.write_bytes(b'preexisting target');trace=[];errors=[]
  item=FileItem(storage='local',type='file',path=str(source),name=source.name,size=source.stat().st_size)
  meta=ns['MetaBase']();meta.fields={'name':'Owned'}
  for k,v in dict(begin_season=None,begin_episode=None,end_episode=None,part=None,org_string=source.name,apply_words=[],resource_team=None,customization=None).items():setattr(meta,k,v)
  media=ns['MediaInfo']();media.type=SimpleNamespace(value='电影');media.title_year='Owned (2024)'
  configured=json.loads(json.dumps(value).replace('{root}',td))
  def plugin(**kw):
   trace.append('plugin');assert kw['preview']is True
   if throws:raise RuntimeError('owned preview failure')
   return Info(**copy.deepcopy(configured))if kind=='model'else copy.deepcopy(configured)
  # This provider executes the exact preview branch, with naming/storage at
  # controlled boundaries. Any native get_folder/get_item would be absent.
  class Native(Handler):
   def transfer_media(self,**kw):
    trace.append('native_preview');ns.update(result=Info(),target_storage='local',folder_path=target.parent,new_file=target,fileitem=item,need_scrape=False,transfer_type='link')
    return super().transfer_media(**kw)
  ns['TransHandler']=Native
  core=ns['FileManagerModule']();core.get_priority=lambda:0;core.get_name=lambda:'文件整理';core._FileManagerModule__get_storage_oper=lambda storage:object()
  chain=Chain();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'transfer':plugin}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda **kw:errors.append(kw));chain.eventmanager=SimpleNamespace(send_event=lambda*a:None)
  ns['task']=SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,target_directory=None,target_storage='local',target_path=folder,transfer_type='link',episodes_info=None,scrape=False,library_type_folder=False,library_category_folder=None,preview=True)
  ns.update(source_oper=None,target_oper=None,preview_items=[],callback=ns['_preview_callback'])
  error=None
  try:result=ns['consume'](chain)
  except Exception as e:result=None;error=type(e).__name__
  rows=ns['preview_items'];raw={'__plugin_transfer_info':value}if kind=='model'else value
  encoded=json.dumps({'name':name,'plugin':raw,'throws':throws,'result':result,'exception':error,'preview_items':rows,'trace':trace,'source_bytes':source.read_text(),'target_bytes':target.read_text(),'errors':len(errors)},ensure_ascii=False).replace(td,'{root}')
  cases.append(json.loads(encoded))
Path(__file__).with_name('transfer-preview-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/transfer.py:2004-2064','app/chain/transfer.py:3536-3559','app/modules/filemanager/transhandler.py:83-137','app/modules/filemanager/transhandler.py:389-419'],'kind':__doc__,'cases':cases},ensure_ascii=False,indent=2)+'\n')
print(len(cases),'fixed preview dispatcher/caller/native branch cases')
