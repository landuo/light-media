"""Pinned ordinary redo_transfer_history caller; controlled history, metadata and transfer.
Not the excluded AI retry scheduler. Actual source presence is checked in owned temp storage.
"""
import ast,copy,json,subprocess,tempfile
from pathlib import Path
from types import SimpleNamespace
fixed='8a83efeacd3e26b82be495e17d50146e31c96a60';root='/Users/accidia/projects/MoviePilot'
tree=ast.parse(subprocess.check_output(['git','-C',root,'show',fixed+':app/chain/transfer.py'],text=True));owner=next(n for n in tree.body if isinstance(n,ast.ClassDef)and n.name=='TransferChain')
methods=[copy.deepcopy(n)for n in owner.body if isinstance(n,ast.FunctionDef)and n.name in ['redo_transfer_history','__re_transfer']]
rows=[]
with tempfile.TemporaryDirectory(prefix='owned-source-redo-') as tmp:
 source=Path(tmp)/'Owned.2024.zh.srt';source.write_text('owned subtitle')
 for name,present,exists,dest,transfer in [('success',True,True,True,True),('without_target',True,True,False,True),('transfer_false',True,True,True,False),('history_missing',False,True,True,True),('source_missing',True,False,True,True)]:
  trace=[];path=source if exists else Path(tmp)/'missing.srt';history=SimpleNamespace(src=str(path),episode_group=None,dest_fileitem={'path':str(Path(tmp)/'old.srt')}if dest else None,src_fileitem={'path':str(path)},download_hash='owned-hash')
  class Media:
   def recognize_by_path(self,path,**kw):trace.append(dict(method='recognize_by_path',kwargs=kw));return SimpleNamespace(media_info=SimpleNamespace(title_year='Owned (2024)'))
  class Storage:
   def delete_file(self,item):trace.append(dict(method='delete_file'));return False
  ns={'Path':Path,'TransferHistoryOper':lambda:SimpleNamespace(get=lambda id:history if present else None),'MediaChain':Media,'StorageChain':Storage,'FileItem':lambda **kw:SimpleNamespace(**kw),'logger':SimpleNamespace(error=lambda*a:None,info=lambda*a:None)}
  module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='TransferChain',bases=[],keywords=[],body=methods,decorator_list=[])],type_ignores=[])
  exec(compile(ast.fix_missing_locations(module),'<fixed:ordinary redo>', 'exec'),ns);obj=ns['TransferChain']()
  def perform(**kw):trace.append(dict(method='do_transfer',force=kw['force'],background=kw['background'],manual=kw['manual'],source=kw['fileitem'].path.replace(tmp,'{root}')));return transfer,''if transfer else'owned rejection'
  obj.do_transfer=perform
  result=obj.redo_transfer_history(42);rows.append(dict(name=name,result=[result[0],result[1].replace(tmp,'{root}')],trace=trace))
Path(__file__).with_name('transfer-redo-source-oracle.json').write_text(json.dumps(dict(fixed_backend=fixed,source='app/chain/transfer.py:3795-3870',kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed ordinary redo cases; delete false is ignored and do_transfer still runs')
