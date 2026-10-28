"""Pinned complete single-extra planner, with controlled storage listing and main metadata.
Only the selected extra is planned; ambiguous/no parent supplies no inherited metadata.
"""
from pathlib import Path
import ast,copy,json,subprocess
from types import SimpleNamespace
p=Path(__file__).with_name('generate-transfer-collection-oracle.py');env={'__file__':str(p)}
exec(compile(p.read_text().split('rows=[]')[0].replace('return _plan_file_items([(fileitem,False)])','return _plan_file_items([(fileitem,False)] if _is_allowed_transfer_item(fileitem,False) else [])'),str(p),'exec'),env)
ns,chain,FileItem=env['ns'],env['chain'],env['FileItem'];main,sub,audio=env['main'],env['sub'],env['audio']
ns['_build_main_meta']=lambda item,*args: {'main_path':item.path,'title':'Main metadata','quality':'2160p'}
ns['min_filesize']=10
rows=[]
for name,current,listing,parent in [('subtitle',sub,[main,sub,audio],True),('audio',audio,[main,sub,audio],True),('ambiguous',sub,[main,env['item']('Owned.2024.mp4'),sub],True),('unrelated',env['item']('Other.zh.srt'),[main,sub],True),('no_parent',sub,[main],False)]:
 trace=[]
 class Storage:
  def get_parent_item(self,item):trace.append('get_parent_item');return FileItem.model_validate(env['parent']) if parent else None
  def list_files(self,item,recursion=False):trace.append('list_files');assert recursion is False;return [FileItem.model_validate(x)for x in listing]
 ns['StorageChain']=Storage
 planned,inherited=chain.plan(FileItem.model_validate(current))
 rows.append(dict(name=name,current=current,listing=listing,parent=parent,min_filesize=10,output=[x.model_dump(mode='json')for x,_ in planned],inherited=list(inherited.values()),trace=trace))
 assert len(planned)==1 and planned[0][0].path==current['path']
Path(__file__).with_name('transfer-single-extra-source-oracle.json').write_text(json.dumps(dict(fixed_backend=env['fixed'],source='app/chain/transfer.py:3195-3292',kind=__doc__,cases=rows),ensure_ascii=False,indent=2)+'\n')
print(len(rows),'pinned complete single-extra planner cases')
