"""Fixed ChainBase wrappers/dispatcher; provider outputs are owned boundaries."""
import base64
import ast
import copy
import hashlib
import inspect
import json
from pathlib import Path
import traceback
from types import FunctionType, SimpleNamespace
from typing import Any, Optional, Union, List, get_type_hints
from enum import Enum
from pydantic import BaseModel, Field

root = Path("/Users/accidia/projects/MoviePilot")
path = root / "app/chain/__init__.py"
owner = next(n for n in ast.parse(path.read_text()).body
             if isinstance(n, ast.ClassDef) and n.name == "ChainBase")
names = {"__is_valid_empty", "__handle_plugin_error", "__handle_system_error",
         "__handle_rate_limit_error", "__execute_plugin_modules",
         "__execute_system_modules", "run_module", "start_torrents",
         "stop_torrents", "remove_torrents", "list_torrents"}
nodes = [copy.deepcopy(n) for n in owner.body
         if isinstance(n, ast.FunctionDef) and n.name in names]
assert len(nodes) == len(names)
object_path = root / "app/utils/object.py"
check = next(n for n in ast.walk(ast.parse(object_path.read_text()))
             if isinstance(n, ast.FunctionDef) and n.name == "check_signature")
check.decorator_list = []
object_ns = dict(inspect=inspect, get_type_hints=get_type_hints,
                 FunctionType=FunctionType)
exec(compile(ast.fix_missing_locations(ast.Module(body=[check], type_ignores=[])),
             "fixed-object-signature", "exec"), object_ns)
class RateLimitExceededException(Exception):
    pass
logger = SimpleNamespace(info=lambda *a: None, debug=lambda *a: None,
                         error=lambda *a: None)
ns = dict(inspect=inspect, traceback=traceback, logger=logger,
          ObjectUtils=SimpleNamespace(check_signature=object_ns["check_signature"]),
          RateLimitExceededException=RateLimitExceededException,
          EventType=SimpleNamespace(SystemError="system.error"))
module = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0),
                         ast.ClassDef(name="ChainBase", bases=[], keywords=[], body=nodes, decorator_list=[])], type_ignores=[])
exec(compile(ast.fix_missing_locations(module), str(path), "exec"), ns)

def clean(value):
    if isinstance(value, tuple):
        return {"__plugin_module_tuple": [clean(x) for x in value]}
    if isinstance(value, list):
        return [clean(x) for x in value]
    if isinstance(value, dict):
        return {k: clean(v) for k, v in value.items()}
    return value


schema=root/'app/schemas/transfer.py'
node=next(n for n in ast.parse(schema.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='DownloaderTorrent')
modelns=dict(BaseModel=BaseModel,Field=Field,Optional=Optional,Path=Path,List=List)
exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'fixed-DownloaderTorrent','exec'),modelns)
DownloaderTorrent=modelns['DownloaderTorrent']
types=root/'app/schemas/types.py'
enumnode=next(n for n in ast.parse(types.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='TorrentStatus')
enum_ns=dict(ns,Enum=Enum)
exec(compile(ast.fix_missing_locations(ast.Module(body=[enumnode],type_ignores=[])),'fixed-TorrentStatus','exec'),enum_ns)
TorrentStatus=enum_ns['TorrentStatus']
chain_path=root/'app/chain/download.py'
chain_class=next(n for n in ast.parse(chain_path.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='DownloadChain')
method=copy.deepcopy(next(n for n in chain_class.body if isinstance(n,ast.FunctionDef) and n.name=='downloading'))
method.decorator_list=[]
method_ns=dict(ns,Optional=Optional,List=List,DownloaderTorrent=DownloaderTorrent,TorrentStatus=TorrentStatus,DownloadHistoryOper=lambda:SimpleNamespace(get_by_hashes=lambda ids:{}))
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-DownloadChain.downloading','exec'),method_ns)
ns['ChainBase'].downloading=method_ns['downloading']
base=dict(downloader='Owned downloader',hash='0123456789012345678901234567890123456789',title='Owned.S02E03.1080p',name='Owned',year='2026',season_episode='S02E03',path='/owned/Owned.S02E03.1080p',size=2048,progress=37,state='downloading',dlspeed='1.00 KB',upspeed='0 B',tags='light-media',save_path='/owned',content_path='/owned/Owned.S02E03.1080p',left_time='1时0秒',media={'title':'Owned media','poster':'/owned-poster.jpg'})
rows=[DownloaderTorrent(**base)]
core_row=DownloaderTorrent(**dict(base,hash='a'*40,title='Core owned'))
cases=[]
for label,plugin_result,plugin_failure,core_failure,selected in [
 ('null_core',None,False,False,'Owned downloader'),
 ('false_terminal',False,False,False,'Owned downloader'),
 ('empty_dict_terminal',{},False,False,'Owned downloader'),
 ('empty_string_terminal','',False,False,'Owned downloader'),
 ('empty_bytes_terminal',b'',False,False,'Owned downloader'),
 ('empty_list_core',[],False,False,'Owned downloader'),
 ('list_before_core',rows,False,False,'Owned downloader'),
 ('list_optional_downloader', [DownloaderTorrent(**dict(base,downloader=None))],False,False,'Owned downloader'),
 ('plugin_exception_core',None,True,False,'Owned downloader'),
 ('list_core_exception',rows,False,True,'Owned downloader'),
 ('all_null_tuple_core',(None,None),False,False,None),
]:
 observations=[];roles=[];events=[]
 def plugin(*args,**kwargs):
  observations.append(dict(kind='plugin',args=list(args),kwargs=kwargs))
  if plugin_failure:raise RuntimeError('owned plugin failure')
  return copy.deepcopy(plugin_result)
 class Qbittorrent:
  def get_priority(self):return 0
  def get_name(self):return 'Qbittorrent'
  def list_torrents(self,status=None,hashs=None,downloader=None,include_all_tags=False):
   observations.append(dict(kind='core',kwargs=dict(status=status,hashs=hashs,downloader=downloader,include_all_tags=include_all_tags)))
   if core_failure:raise RuntimeError('owned core failure')
   return [core_row.model_copy(deep=True)]
 c=ns['ChainBase']();c.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','Owned module override'):{'list_torrents':plugin}})
 c.modulemanager=SimpleNamespace(get_running_modules=lambda method:[Qbittorrent()])
 c.messagehelper=SimpleNamespace(put=lambda **kw:roles.append(kw['role']))
 c.eventmanager=SimpleNamespace(send_event=lambda kind,data:events.append({k:v for k,v in data.items() if k not in ('traceback','error')}))
 result=c.downloading(selected)
 def project(v):
  if isinstance(v,bytes):return {'__plugin_module_bytes':base64.b64encode(v).decode()}
  if isinstance(v,DownloaderTorrent):return v.model_dump(mode='json')
  if isinstance(v,Enum):return v.value
  if isinstance(v,tuple):return {'__plugin_module_tuple':[project(x) for x in v]}
  if isinstance(v,list):return [project(x) for x in v]
  if isinstance(v,dict):return {k:project(x) for k,x in v.items()}
  return v
 cases.append(dict(name=label,plugin_result=project(plugin_result),plugin_failure=plugin_failure,core_failure=core_failure,selected=selected,observations=project(observations),roles=roles,returned=project(result)))
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind='actual fixed ChainBase list wrapper/dispatcher/signature, DownloadChain.downloading and Pydantic DownloaderTorrent; history lookup and native provider outputs owned boundaries',source_sha256={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [path,object_path,schema,chain_path,types]},cases=cases,projection_samples=[DownloaderTorrent(**dict(base,hash=None,name=None,title=None,state=None,size=1e100)).model_dump(mode='json'),DownloaderTorrent(**dict(base,size=-10.5)).model_dump(mode='json')])
Path(__file__).with_name('downloader-list-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print('fixed downloader list cases',len(cases))
