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
from typing import Any, Optional, Union, Set, get_type_hints

root = Path("/Users/accidia/projects/MoviePilot")
path = root / "app/chain/__init__.py"
owner = next(n for n in ast.parse(path.read_text()).body
             if isinstance(n, ast.ClassDef) and n.name == "ChainBase")
names = {"__is_valid_empty", "__handle_plugin_error", "__handle_system_error",
         "__handle_rate_limit_error", "__execute_plugin_modules",
         "__execute_system_modules", "run_module", "start_torrents",
         "stop_torrents", "remove_torrents", "download"}
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
    if isinstance(value, Path): return value.as_posix()
    if isinstance(value, set): return sorted(value)
    if isinstance(value, bytes):
        return {"__plugin_module_bytes": base64.b64encode(value).decode()}
    if isinstance(value, tuple):
        return {"__plugin_module_tuple": [clean(x) for x in value]}
    if isinstance(value, list):
        return [clean(x) for x in value]
    if isinstance(value, dict):
        return {k: clean(v) for k, v in value.items()}
    return value


chain_path=root/'app/chain/download.py'
cls=next(n for n in ast.parse(chain_path.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='DownloadChain')
fn=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='download_single')
call=next(copy.deepcopy(n) for n in fn.body if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id=='result')
pos=next(i for i,n in enumerate(fn.body) if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id=='result')
consume=copy.deepcopy(fn.body[pos+1])
assert isinstance(consume,ast.If) and ast.unparse(consume.test)=='result'
expected_hash='0123456789012345678901234567890123456789'
core_tuple=('Owned downloader',expected_hash,'Original','')
samples=[
 ('null_core',None,False,False),
 ('tuple_terminal',core_tuple,False,False),
 ('tuple_declined',(None,None,None,'owned declined'),False,False),
 ('false_terminal',False,False,False),
 ('empty_list_after_core',[],False,False),
 ('four_list_after_core',list(core_tuple),False,False),
 ('malformed_list_after_core',['owned'],False,False),
 ('true_not_unpackable',True,False,False),
 ('all_null_tuple_core',(None,None,None,None),False,False),
 ('plugin_exception_core',None,True,False),
 ('list_core_exception',list(core_tuple),False,True),
]
cases=[]
for name,plugin_result,plugin_failure,core_failure in samples:
 observations=[];roles=[];events=[]
 def plugin(*args,**kwargs):
  observations.append(dict(kind='plugin',args=clean(list(args)),kwargs=clean(kwargs)))
  if plugin_failure:raise RuntimeError('owned plugin failure')
  return copy.deepcopy(plugin_result)
 class Qbittorrent:
  def get_priority(self):return 0
  def get_name(self):return 'Qbittorrent'
  def download(self,content:Union[Path,str,bytes],download_dir:Path,cookie:str,episodes:Set[int]=None,category:Optional[str]=None,label:Optional[str]=None,downloader:Optional[str]=None):
   observations.append(dict(kind='core',kwargs=clean(dict(content=content,download_dir=download_dir,cookie=cookie,episodes=episodes,category=category,label=label,downloader=downloader))))
   if core_failure:raise RuntimeError('owned core failure')
   return core_tuple
 owner=ns['ChainBase']();owner.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','Owned module override'):{'download':plugin}})
 owner.modulemanager=SimpleNamespace(get_running_modules=lambda method:[Qbittorrent()]);owner.messagehelper=SimpleNamespace(put=lambda **kw:roles.append(kw['role']));owner.eventmanager=SimpleNamespace(send_event=lambda kind,data:events.append({k:v for k,v in data.items() if k not in ('error','traceback')}))
 env=dict(Optional=Optional,self=owner,torrent_content=b'owned\x00\xff',download_dir=Path('/owned/downloads'),_torrent=SimpleNamespace(site_cookie='owned-cookie'),episodes={2,4},_media=SimpleNamespace(category='华语电视剧'),label=None,downloader='Owned downloader',_site_downloader=None)
 exception=None
 try:
  exec(compile(ast.fix_missing_locations(ast.Module(body=[call,consume],type_ignores=[])),'fixed-DownloadSingle-result-consumer','exec'),env)
 except Exception as error:exception=type(error).__name__
 cases.append(dict(name=name,plugin_result=clean(plugin_result),plugin_failure=plugin_failure,core_failure=core_failure,observations=observations,roles=roles,returned=clean(env.get('result')),caller_hash=env.get('_hash'),caller_error=env.get('error_msg'),accepted=bool(env.get('_hash')) if exception is None else False,exception=exception))
selection_cases=[]
for explicit,site in [(None,None),(None,'Site downloader'),('Explicit downloader','Site downloader')]:
 selected=[]
 sample_env=env|dict(self=SimpleNamespace(download=lambda **kwargs:selected.append(clean(kwargs))or core_tuple),downloader=explicit,_site_downloader=site)
 exec(compile(ast.fix_missing_locations(ast.Module(body=[call],type_ignores=[])),'fixed-DownloadSingle-downloader-selection','exec'),sample_env)
 selection_cases.append(dict(explicit=explicit,site=site,selected=selected[0]['downloader']))
result=dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind='actual fixed ChainBase download wrapper/dispatch/signature plus exact DownloadChain.download_single result assignment/unpack; native core tuple and binary input are owned boundaries, not actual Add evidence',source_sha256={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest()for p in [path,object_path,chain_path]},cases=cases,selection_cases=selection_cases)
Path(__file__).with_name('downloader-add-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print('fixed download wrapper/caller cases',len(cases))
