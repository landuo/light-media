"""Fixed successful transfer callback/history destination consumption.
Executes only the ordinary successful-history/broadcast statements, plus the
real add_success and target-dir selection methods. History storage and media
fields are controlled; effects use real owned link/copy files. No AI branch
or external service runs.
"""
import ast,copy,json,os,shutil,tempfile
from pathlib import Path
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-module-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('Info=')[0],str(p),'exec'),env)
ns,one,run=env['ns'],env['one'],env['run'];Info,FileItem=ns['TransferInfo'],ns['FileItem']
owner=one('app/db/transferhistory_oper.py',ast.ClassDef,'TransferHistoryOper');run([ast.ClassDef(name='History',bases=[],keywords=[],body=[n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='add_success'],decorator_list=[])],'<fixed:add_success>')
owner=one('app/chain/transfer.py',ast.ClassDef,'TransferChain');fn=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='__default_callback');branch=next(n for n in fn.body if isinstance(n,ast.If)and ast.unparse(n.test)=='not transferinfo.success')
# Exact first three success statements: log, add_success, typed completion
# broadcast. Subsequent batch aggregation is audited separately.
body=copy.deepcopy(branch.orelse[:3]);body.append(ast.Return(value=ast.Name(id='history',ctx=ast.Load())))
consume=ast.FunctionDef(name='consume',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='self'),ast.arg(arg='task'),ast.arg(arg='transferinfo')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=body,decorator_list=[])
root_method=next(n for n in owner.body if isinstance(n,ast.FunctionDef)and n.name=='__get_transfer_target_dir_path')
run([ast.ClassDef(name='Consumer',bases=[],keywords=[],body=[consume,root_method],decorator_list=[])],'<fixed:ordinary success history/event>')
ns['EventType']=SimpleNamespace(TransferComplete='transfer.complete',SubtitleTransferComplete='transfer.subtitle.complete',AudioTransferComplete='transfer.audio.complete')
rows=[]
for name,mode in [('link','link'),('copy','copy')]:
 with tempfile.TemporaryDirectory(prefix='owned-target-source-')as td:
  root=Path(td);source=root/'source'/'Owned.mkv';source.parent.mkdir();source.write_bytes(b'owned transfer content');native=root/'library'/'Owned (2024)'/'Owned.mkv';target=root/'library'/'Plugin chosen folder'/'Mapped source result.mkv';target.parent.mkdir(parents=True)
  if mode=='link':os.link(source,target)
  else:shutil.copyfile(source,target)
  item=FileItem(storage='local',type='file',path=str(source),name=source.name,size=source.stat().st_size)
  target_item=FileItem(storage='local',type='file',path=str(target),name=target.name,size=target.stat().st_size)
  info=Info(success=True,fileitem=item,target_item=target_item,target_diritem=FileItem(storage='local',type='dir',path=str(target.parent),name=target.parent.name),transfer_type=mode,file_list=[str(source)],file_list_new=[str(target)],file_count=1,total_size=source.stat().st_size)
  meta=SimpleNamespace(season=None,episode=None)
  media=SimpleNamespace(type=SimpleNamespace(value='电影'),category=None,title='Owned',year='2024',tmdb_id=123,imdb_id=None,tvdb_id=None,douban_id=None,bangumi_id=None,anilist_id=None,source='themoviedb',to_dict=lambda:{'media_id':'123'},get_poster_image=lambda:None)
  task=SimpleNamespace(fileitem=item,meta=meta,mediainfo=media,downloader=None,download_hash=None)
  history=ns['History']();captured=[]
  def add_force(**kw):captured.append(kw);return SimpleNamespace(id=101)
  history.add_force=add_force;ns['transferhis']=history
  consumer=ns['Consumer']();consumer._Consumer__is_media_file=lambda item:True;consumer._Consumer__is_subtitle_file=lambda item:False;consumer._Consumer__is_audio_file=lambda item:False;events=[];consumer.eventmanager=SimpleNamespace(send_event=lambda kind,data:events.append({'type':kind,'target':data['transferinfo'].target_item.path,'history_id':data['transfer_history_id']}))
  ns['target_dir_path']=consumer._Consumer__get_transfer_target_dir_path(info)
  result=consumer.consume(task,info)
  row={'name':name,'transfer_type':mode,'relative_target':target.relative_to(root/'library').as_posix(),'history':captured[0],'events':events,'returned_history_id':result.id,'native_target_exists':native.exists(),'same_inode':os.stat(source).st_ino==os.stat(target).st_ino,'content':target.read_text()}
  rows.append(json.loads(json.dumps(row,default=str,ensure_ascii=False).replace(td,'{root}')))
Path(__file__).with_name('transfer-target-source-oracle.json').write_text(json.dumps({'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':['app/chain/transfer.py:1251-1279','app/chain/transfer.py:1391-1408','app/db/transferhistory_oper.py:227-261'],'kind':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print(len(rows),'actual fixed success callback/history destination cases')
