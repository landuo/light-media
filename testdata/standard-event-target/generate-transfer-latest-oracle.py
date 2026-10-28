"""Pinned transfer_media/__delete_version_files/__transfer_file on owned files.
Naming root and parsed metadata are counted boundaries, separately source-tested.
"""
import ast,copy,hashlib,json,os,pathlib,re,sys,tempfile,types
from typing import Any,Optional,List,Tuple
from pydantic import BaseModel,Field
root=pathlib.Path('/Users/accidia/projects/MoviePilot')
paths=['app/schemas/file.py','app/schemas/event.py','app/schemas/transfer.py','app/modules/filemanager/transhandler.py','app/modules/filemanager/storages/local.py']
raw={p:(root/p).read_text()for p in paths}
ns=dict(BaseModel=BaseModel,Field=Field,Any=Any,Optional=Optional,List=List,Tuple=Tuple,Path=pathlib.Path,MetaBase=Any,MediaInfo=Any,StorageBase=Any,re=re,StorageQueryError=RuntimeError,TmdbEpisode=Any)
def execute(nodes,name):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),name,'exec'),ns)
for path,names in [(paths[0],['FileURI','FileItem']),(paths[1],['BaseEventData','ChainEventData','TransferInterceptEventData']),(paths[2],['TransferInfo'])]:execute([n for n in ast.parse(raw[path]).body if isinstance(n,ast.ClassDef)and n.name in names],path)
for name in ['FileURI','FileItem','TransferInterceptEventData','TransferInfo']:ns[name].model_rebuild(_types_namespace=ns)
functions={}
for path,names in [(paths[3],['transfer_media','__delete_version_files','__transfer_file','__transfer_command','__update_result']),(paths[4],['__get_fileitem','__get_diritem'])]:
 for name in names:
  fun=copy.deepcopy(next(n for n in ast.walk(ast.parse(raw[path]))if isinstance(n,ast.FunctionDef)and n.name==name));fun.decorator_list=[];execute([fun],path);functions[name]=ns[name]
ns['schemas']=types.SimpleNamespace(FileItem=ns['FileItem'])
ns['logger']=types.SimpleNamespace(info=lambda*a:None,debug=lambda*a:None,warn=lambda*a:None,error=lambda*a:None)
ns['MediaType']=types.SimpleNamespace(TV='电视剧')
ns['settings']=types.SimpleNamespace(RENAME_FORMAT=lambda t:'Latest/New.mkv',RMT_SUBEXT=['.srt'],RMT_AUDIOEXT=['.flac'],RMT_MEDIAEXT=['.mkv'])
ns['DirectoryHelper']=types.SimpleNamespace(get_media_root_path=lambda fmt,rename_path:rename_path.parent)
ns['MetaInfoPath']=lambda path:types.SimpleNamespace(season='',episode='',part=None)
ns['ChainEventType']=types.SimpleNamespace(TransferIntercept='transfer.intercept')
cases=[]
for mode in ['normal','cancel','delete-false']:
 with tempfile.TemporaryDirectory(prefix='owned-fixed-latest-')as tmp:
  base=pathlib.Path(tmp);source=base/'source'/'Current.mkv';source.parent.mkdir();source.write_bytes(b'owned video')
  target=base/'library'/'Latest'/'New.mkv';target.parent.mkdir(parents=True);old=target.with_name('Old.mkv');old.write_bytes(b'old version');subtitle=target.with_name('Subtitle.srt');subtitle.write_bytes(b'keep subtitle')
  calls=[]
  local=types.SimpleNamespace(schema=types.SimpleNamespace(value='local'))
  def item(path):return functions['__get_diritem'](local,path)if path.is_dir()else functions['__get_fileitem'](local,path)
  class Storage:
   def get_folder(self,path):calls.append('get_folder');path.mkdir(parents=True,exist_ok=True);return item(path)
   def get_item(self,path):calls.append('get_item');return item(path)if path.exists()else None
   def get_item_strict(self,path):calls.append('get_item_strict');return item(path)if path.exists()else None
   def list(self,fileitem):calls.append('list');return[item(p)for p in pathlib.Path(fileitem.path).iterdir()]
   def delete(self,fileitem):
    calls.append('delete')
    if mode=='delete-false':return False
    pathlib.Path(fileitem.path).unlink();return True
   def link(self,fileitem,path):calls.append('link');os.link(fileitem.path,path);return True
  def send(kind,data):calls.append(kind);data.cancel=mode=='cancel';data.reason='latest veto'if data.cancel else'';return types.SimpleNamespace(event_data=data)
  ns['eventmanager']=types.SimpleNamespace(send_event=send)
  owner=types.SimpleNamespace()
  owner.get_rename_path=lambda**kwargs:target
  owner.get_naming_dict=lambda**kwargs:{}
  for name in ['__delete_version_files','__update_result']:setattr(owner,name,functions[name])
  for name in ['__transfer_file','__transfer_command']:setattr(owner,name,lambda *args,_name=name,**kwargs:functions[_name](owner,*args,**kwargs))
  # __transfer_command is static and has no self argument.
  owner.__transfer_command=functions['__transfer_command']
  result=functions['transfer_media'](owner,item(source),types.SimpleNamespace(),types.SimpleNamespace(type='电影'),'local',base/'library','link',Storage(),Storage(),overwrite_mode='latest')
  cases.append(dict(name=mode,calls=calls,success=result.success,old_exists=old.exists(),new_exists=target.exists(),source_exists=source.exists(),subtitle_exists=subtitle.exists(),message=result.message))
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',python=sys.version,kind='actual fixed functions/Pydantic and real owned filesystem; naming-root/parsed identity counted boundaries, not full discovery/runtime',source_sha256={p:hashlib.sha256(s.encode()).hexdigest()for p,s in raw.items()},cases=cases)
pathlib.Path(__file__).with_name('transfer-latest-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print('fixed actual latest cases',len(cases))
