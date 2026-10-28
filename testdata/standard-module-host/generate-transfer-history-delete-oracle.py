"""Pinned ordinary history deletion endpoint; owned in-memory DB/storage
boundaries record exactly which independent file is selected and in what order.
No AI redo endpoint is imported or executed.
"""
import ast,copy,json,subprocess
from pathlib import Path
from types import SimpleNamespace
from typing import Any,Optional
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';root='/Users/accidia/projects/MoviePilot'
tree=ast.parse(subprocess.check_output(['git','-C',root,'show',fixed+':app/api/endpoints/history.py'],text=True))
fn=copy.deepcopy(next(n for n in tree.body if isinstance(n,ast.FunctionDef)and n.name=='delete_transfer_history'));fn.decorator_list=[]
ns=dict(Any=Any,Optional=Optional,schemas=SimpleNamespace(TransferHistory=object,FileItem=lambda **kw:SimpleNamespace(**kw),Response=lambda **kw:kw),Session=object,User=object,Depends=lambda x:None,get_db=None,get_current_active_manage_user=None,Path=Path,EventType=SimpleNamespace(DownloadFileDeleted='download.file.deleted'))
exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<fixed:ordinary transfer history delete endpoint>','exec'),ns)
rows=[]
for kind in ['subtitle','audio']:
 for src,dest in [(False,False),(False,True),(True,False),(True,True)]:
  trace=[];ext='srt'if kind=='subtitle'else'flac';source='/owned/source/Main.'+ext;target='/owned/library/Main.'+ext
  history=SimpleNamespace(id=73,src=source,src_fileitem={'path':source},dest_fileitem={'path':target},download_hash=None)
  ns['TransferHistory']=SimpleNamespace(get=lambda db,id:history,delete=lambda db,id:trace.append(dict(action='delete_history',id=id)))
  ns['DownloadFiles']=SimpleNamespace(delete_by_fullpath=lambda db,path:trace.append(dict(action='delete_download_file',path=path)))
  class Storage:
   def delete_media_file(self,item):trace.append(dict(action='delete_file',path=item.path));return True
  ns['StorageChain']=Storage;ns['eventmanager']=SimpleNamespace(send_event=lambda event,payload:trace.append(dict(action='event',event=event,payload=payload)))
  result=ns['delete_transfer_history'](SimpleNamespace(id=73),deletesrc=src,deletedest=dest,db=None,_=None)
  rows.append(dict(kind=kind,delete_source=src,delete_target=dest,result=result,trace=trace))
failures=[]
for src,dest,source_return,target_return in [(False,True,True,False),(True,False,False,True),(True,True,True,False),(True,True,False,True),(True,True,False,False)]:
 trace=[];source='/owned/source/Main.srt';target='/owned/library/Main.srt'
 history=SimpleNamespace(id=73,src=source,src_fileitem={'path':source},dest_fileitem={'path':target},download_hash=None)
 class Storage:
  def delete_media_file(self,item):
   trace.append(dict(action='delete_file',path=item.path));return source_return if item.path==source else target_return
 ns['StorageChain']=Storage
 result=ns['delete_transfer_history'](SimpleNamespace(id=73),deletesrc=src,deletedest=dest,db=None,_=None)
 failures.append(dict(delete_source=src,delete_target=dest,source_return=source_return,target_return=target_return,result=result,trace=trace))
Path(__file__).with_name('transfer-history-delete-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source='app/api/endpoints/history.py:221-260',kind=__doc__,cases=rows,failure_cases=failures),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed independent history file choices,',len(failures),'false-return branches')
