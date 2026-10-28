"""Pinned three-task manual preview loop, callback and fallback rows.
The main/subtitle/audio task set is supplied at the collection boundary; the
exact do_transfer loop/summary, dispatch, FileManager and native preview branch
execute. Collection's own list_files override wiring is audited separately.
"""
import ast,copy,json,tempfile
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-preview-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('cases=[]')[0],str(p),'exec'),env)
ns,run,do,Handler=env['ns'],env['run'],env['do'],env['Handler']
loop=copy.deepcopy(next(n for n in do.body if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='transfer_tasks'))
summary=copy.deepcopy(next(n for n in do.body if isinstance(n,ast.If)and isinstance(n.test,ast.Name)and n.test.id=='preview'and any(isinstance(x,ast.Return)for x in n.body)))
def assignment(name):
 return copy.deepcopy(next(n for n in do.body if (isinstance(n,ast.Assign)and any(isinstance(t,ast.Name)and t.id==name for t in n.targets))or(isinstance(n,ast.AnnAssign)and isinstance(n.target,ast.Name)and n.target.id==name)))
function=ast.FunctionDef(name='run_batch',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self'),ast.arg(arg='transfer_tasks')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=[assignment('all_success'),assignment('err_msgs'),assignment('preview_items'),copy.deepcopy(env['callback']),loop,assignment('error_msg'),summary],decorator_list=[])
run([ast.ClassDef(name='PreviewBatch',bases=[],keywords=[],body=[function],decorator_list=[])],'<fixed:manual per-task preview loop and summary>')
ns['List']=list;ns['global_vars']=SimpleNamespace(is_system_stopped=False);ns['continue_callback']=None
rows=[]
for name in ['native','raw-false-subtitle','typed-veto-subtitle']:
 with tempfile.TemporaryDirectory(prefix='owned-related-preview-')as td:
  root=Path(td);source=root/'source';library=root/'library';source.mkdir();library.mkdir();trace=[]
  names=['Owned.2024.mkv','Owned.2024.zh.srt','Owned.2024.flac'];items=[];tasks=[]
  for filename in names:
   path=source/filename;path.write_bytes(b'owned preview source')
   item=ns['FileItem'](path=str(path),name=filename,extension=path.suffix[1:],type='file',storage='local',size=path.stat().st_size);items.append(item)
   meta=ns['MetaBase']();meta.fields={'name':'Owned'}
   for key,value in dict(begin_season=None,begin_episode=None,end_episode=None,part=None,org_string=filename,apply_words=[],resource_team=None,customization=None).items():setattr(meta,key,value)
   media=ns['MediaInfo']();media.type=SimpleNamespace(value='电影');media.title_year='Owned (2024)'
   tasks.append(SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,target_directory=None,target_storage='local',target_path=library,transfer_type='link',episodes_info=None,scrape=False,library_type_folder=False,library_category_folder=None,preview=True))
  targets={items[0].path:library/'Owned (2024)'/'Owned (2024).mkv',items[1].path:library/'Owned (2024)'/'Owned (2024).default.chi.zh-cn.srt',items[2].path:library/'Owned (2024)'/'Owned (2024).flac'}
  for target in targets.values():target.parent.mkdir(exist_ok=True);target.write_bytes(b'preexisting target')
  def plugin(**kw):
   trace.append({'source':kw['fileitem'].path,'kind':'plugin','preview':kw['preview']})
   if kw['fileitem'].extension=='srt':
    if name=='raw-false-subtitle':return False
    if name=='typed-veto-subtitle':return ns['TransferInfo'](success=False,message='owned subtitle veto')
   return None
  class Native(Handler):
   def transfer_media(self,**kw):
    item=kw['fileitem'];target=targets[item.path];trace.append({'source':item.path,'kind':'native_preview'})
    ns.update(result=ns['TransferInfo'](),target_storage='local',folder_path=target.parent,new_file=target,fileitem=item,need_scrape=False,transfer_type='link')
    return super().transfer_media(**kw)
  ns['TransHandler']=Native
  core=ns['FileManagerModule']();core.get_priority=lambda:0;core.get_name=lambda:'文件整理';core._FileManagerModule__get_storage_oper=lambda storage:object()
  chain=ns['TransferChain']();chain.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('Owned','Owned'):{'transfer':plugin}});chain.modulemanager=SimpleNamespace(get_running_modules=lambda name:[core]);chain.messagehelper=SimpleNamespace(put=lambda**kw:None);chain.eventmanager=SimpleNamespace(send_event=lambda*a:None)
  def handle(task,callback):ns.update(task=task,callback=callback);return ns['consume'](chain)
  batch=ns['PreviewBatch']();batch._PreviewBatch__handle_transfer=handle;batch._PreviewBatch__start_job_execution=lambda task:None;batch._PreviewBatch__finish_job_execution=lambda task:None
  batch.jobview=SimpleNamespace(fail_task=lambda task:None,finish_task=lambda task:None,try_remove_job=lambda task:None)
  ns.update(preview=True,source_oper=None,target_oper=None)
  result=batch.run_batch(tasks)
  assert len(trace)>=3 and result[1]['summary']['total']==3,(name,result,trace)
  assert result[1]['summary']['failed']==(0 if name=='native'else 1),(name,result,trace)
  rows.append(json.loads(json.dumps({'name':name,'result':result,'trace':trace,'unchanged':[p.read_text()for p in targets.values()]},ensure_ascii=False).replace(td,'{root}')))
Path(__file__).with_name('transfer-related-preview-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/transfer.py:3538-3667','app/chain/transfer.py:3687-3697','app/modules/filemanager/transhandler.py:389-419'],'kind':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed related preview batch/fallback cases')
