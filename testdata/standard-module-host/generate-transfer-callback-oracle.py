"""Pinned per-task history-before-event callback prefix, ordinary branches only.
Real Pydantic models and exact add_success/add_fail execute against a controlled
history store. Notification/batch code after this prefix is not claimed here.
"""
import ast,copy,json
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-target-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('rows=[]')[0],str(p),'exec'),env)
ns,one,run=env['ns'],env['one'],env['run'];Info,FileItem=ns['TransferInfo'],ns['FileItem']
owner=one('app/db/transferhistory_oper.py',ast.ClassDef,'TransferHistoryOper')
run([ast.ClassDef(name='CallbackHistory',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in ['add_success','add_fail']],decorator_list=[])],'<fixed:add_success/add_fail>')
branch=env['branch'];consume=copy.deepcopy(env['consume']);consume.name='consume_failure';consume.body=copy.deepcopy(branch.body[:3])+[ast.Return(value=ast.Name(id='history',ctx=ast.Load()))]
run([ast.ClassDef(name='FailureConsumer',bases=[],keywords=[],body=[consume],decorator_list=[])],'<fixed:failure history/event prefix>')
ns['EventType']=SimpleNamespace(**{key:value for key,value in [('TransferComplete','transfer.complete'),('TransferFailed','transfer.failed'),('SubtitleTransferComplete','transfer.subtitle.complete'),('SubtitleTransferFailed','transfer.subtitle.failed'),('AudioTransferComplete','transfer.audio.complete'),('AudioTransferFailed','transfer.audio.failed')]})
rows=[]
for kind,ext in [('main','mkv'),('subtitle','srt'),('audio','flac')]:
 for success,null_target in [(True,False),(True,True),(False,True)]:
  path=f'/owned/source/Task.{ext}';dest=f'/owned/library/Task.{ext}'
  item=FileItem(storage='local',type='file',path=path,name=f'Task.{ext}',extension=ext,size=23)
  target=None if null_target else FileItem(storage='local',type='file',path=dest,name=f'Task.{ext}',size=23)
  info=Info(success=success,fileitem=item,target_item=target,transfer_type=None if null_target else 'link',file_count=77,total_size=999,file_list=[path],file_list_new=[dest],message=None if success else 'owned per-task veto')
  meta=SimpleNamespace(name='Owned',year='2024',season=None,episode=None)
  media=SimpleNamespace(type=SimpleNamespace(value='电影'),category=None,title='Owned',year='2024',tmdb_id=123,imdb_id=None,tvdb_id=None,douban_id=None,bangumi_id=None,anilist_id=None,episode_group=None,source='themoviedb',to_dict=lambda:{'media_id':'123'},get_poster_image=lambda:None)
  task=SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,downloader=None,download_hash=None)
  history=ns['CallbackHistory']();trace=[];captured=[]
  def add_force(**kw):trace.append('history');captured.append(kw);return SimpleNamespace(id=101)
  history.add_force=add_force;ns['transferhis']=history
  consumer=ns['Consumer']() if success else ns['FailureConsumer']()
  prefix='_Consumer' if success else '_FailureConsumer'
  setattr(consumer,prefix+'__is_media_file',lambda item:kind=='main')
  setattr(consumer,prefix+'__is_subtitle_file',lambda item:kind=='subtitle')
  setattr(consumer,prefix+'__is_audio_file',lambda item:kind=='audio')
  events=[]
  def send(name,data):trace.append(name);events.append({'type':name,'history_id':data['transfer_history_id'],'fileitem':data['fileitem'].model_dump(),'transferinfo':data['transferinfo'].model_dump()})
  consumer.eventmanager=SimpleNamespace(send_event=send);ns['target_dir_path']=None
  result=consumer.consume(task,info) if success else consumer.consume_failure(task,info)
  rows.append({'kind':kind,'success':success,'null_target':null_target,'trace':trace,'history':captured[0],'events':events,'returned_history_id':result.id})
Path(__file__).with_name('transfer-callback-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/transfer.py:1135-1195','app/chain/transfer.py:1251-1310','app/db/transferhistory_oper.py:227-319'],'kind':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed per-task history/event prefix cases')
