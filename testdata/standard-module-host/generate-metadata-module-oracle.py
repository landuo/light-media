"""Fixed NFO wrapper/dispatcher/_save_file and system-only metadata_img.

Only plugin/core return values, MediaInfo fields and storage.upload_file are
controlled boundaries. _save_file really creates/writes/reads a temporary file.
"""
import ast
import base64
import copy
import gzip
import hashlib
import inspect
import json
import tempfile
import traceback
from pathlib import Path
from types import FunctionType, SimpleNamespace
from typing import get_type_hints, Optional, Union, Set

ROOT = Path('/Users/accidia/projects/MoviePilot')
TARGET = Path(__file__).resolve().parents[2]
chain = ROOT/'app/chain/__init__.py'
owner = next(n for n in ast.parse(chain.read_text()).body if isinstance(n, ast.ClassDef) and n.name == 'ChainBase')
names = {'__is_valid_empty', '__handle_plugin_error', '__handle_system_error', '__handle_rate_limit_error', '__execute_plugin_modules', '__execute_system_modules', 'run_module'}
nodes = [copy.deepcopy(n) for n in owner.body if isinstance(n, ast.FunctionDef) and n.name in names]
media = ROOT/'app/chain/media.py'
owner = next(n for n in ast.parse(media.read_text()).body if isinstance(n, ast.ClassDef) and n.name == 'MediaChain')
nodes += [copy.deepcopy(n) for n in owner.body if isinstance(n, ast.FunctionDef) and n.name in {'metadata_nfo', 'metadata_img', '_save_file', '_scrape_nfo_generic', '_should_scrape'}]
signature = ROOT/'app/utils/object.py'
check = next(copy.deepcopy(n) for n in ast.walk(ast.parse(signature.read_text())) if isinstance(n, ast.FunctionDef) and n.name == 'check_signature')
check.decorator_list=[]
env = dict(inspect=inspect, get_type_hints=get_type_hints, FunctionType=FunctionType)
exec(compile(ast.fix_missing_locations(ast.Module(body=[check],type_ignores=[])),str(signature),'exec'),env)
class RateLimitExceededException(Exception): pass
class MetaBase: pass
class MediaInfo: pass
logger = SimpleNamespace(**{x:lambda *a,**k:None for x in ('debug','info','warn','error')})
ns = dict(inspect=inspect, traceback=traceback, logger=logger, ObjectUtils=SimpleNamespace(check_signature=env['check_signature']),
          RateLimitExceededException=RateLimitExceededException, EventType=SimpleNamespace(SystemError='system.error'),
          MetaBase=MetaBase,MediaInfo=MediaInfo,Path=Path,NamedTemporaryFile=tempfile.NamedTemporaryFile,current_umask=0o022)
tree=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='MediaChain',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])
exec(compile(ast.fix_missing_locations(tree),str(media),'exec'),ns)
def clean(value):
    if isinstance(value, Path): return value.as_posix()
    if isinstance(value, bytes): return {'__plugin_module_bytes':base64.b64encode(value).decode()}
    if isinstance(value, tuple): return {'__plugin_module_tuple':[clean(v) for v in value]}
    if isinstance(value, list): return [clean(v) for v in value]
    if isinstance(value, dict): return {k:clean(v) for k,v in value.items()}
    if isinstance(value, (MetaBase,MediaInfo)): return clean(value.fields)
    return value
with gzip.open(TARGET/'testdata/plugins/metainfo-title-oracle.ndjson.gz','rt') as f:
    for line in f:
        row=json.loads(line)
        if row['record']=='case':
            data=row['data']
            if data['title']=='Synthetic Film 2024' and data['mode']=='rust' and not data['global_words'] and data['local_words'] is None:
                meta_fields=data['result']['to_dict'];break
meta=MetaBase();meta.fields=meta_fields
info=MediaInfo();info.fields={'type':'电影','title':'Owned metadata','tmdb_id':900043,'tmdb_info':{},'douban_info':{},'bangumi_info':{},'anilist_info':{}}
cases=[]
for name,value in [('none_core',None),('bytes_terminal',b'owned\x00\xff'),('empty_bytes_terminal',b''),('false_terminal',False),('empty_list_core',[]),('truthy_list_invalid',['owned']),('tuple_invalid',('owned',)),('text_terminal','<movie>Owned 中文</movie>')]:
    calls=[];saved=[]
    def plugin(*a,**kwargs): calls.append(dict(role='plugin',kwargs=clean(kwargs)));return copy.deepcopy(value)
    class Core:
        def get_priority(self):return 0
        def get_name(self):return 'TheMovieDb'
        def metadata_nfo(self,meta:MetaBase,mediainfo:MediaInfo,season:Optional[int]=None,episode:Optional[int]=None):
            calls.append(dict(role='core',kwargs=clean(dict(meta=meta,mediainfo=mediainfo,season=season,episode=episode))))
            return 'owned native core NFO'
    obj=ns['MediaChain']()
    obj.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','Owned'):{'metadata_nfo':plugin}})
    obj.modulemanager=SimpleNamespace(get_running_modules=lambda name:[Core()])
    obj.messagehelper=SimpleNamespace(put=lambda **kw:None)
    obj.eventmanager=SimpleNamespace(send_event=lambda *a,**kw:None)
    def upload_file(**kwargs):
        saved.append(clean(kwargs['path'].read_bytes()));return SimpleNamespace(path='/owned/film.nfo')
    obj.storagechain=SimpleNamespace(upload_file=upload_file)
    obj._cleanup_temp_file=lambda path:path.unlink(missing_ok=True) if path else None
    result=obj.metadata_nfo(meta,info,None,None)
    exception=None
    if result:
        try:obj._save_file(SimpleNamespace(path='/owned'),Path('/owned/film.nfo'),result)
        except Exception as error:exception=type(error).__name__
    cases.append(dict(name=name,plugin_result=clean(value),result=clean(result),calls=calls,saved=saved,exception=exception))

image_calls=[]
class ImageCore:
    def __init__(self,priority,value,fail=False):self.priority,self.value,self.fail=priority,value,fail
    def get_priority(self):return self.priority
    def get_name(self):return 'Owned image system'
    def metadata_img(self,**kwargs):
        image_calls.append(dict(priority=self.priority,kwargs=clean(kwargs)))
        if self.fail:raise RuntimeError('owned image error')
        return self.value
obj.modulemanager=SimpleNamespace(get_running_modules=lambda name:[ImageCore(2,{'poster.jpg':'second','banner.jpg':'third'}),ImageCore(0,{'poster.jpg':'first'}),ImageCore(1,None,True)])
obj.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:(_ for _ in ()).throw(AssertionError('metadata_img must not request plugins')))
images=obj.metadata_img(info,None,None)
ns['ScrapingMetadata']=SimpleNamespace(NFO=SimpleNamespace(value='NFO'))
existence_cases=[]
for exists,overwrite in [(True,False),(True,True),(False,False)]:
    calls=[]
    consumer=ns['MediaChain']()
    consumer.scraping_policies=SimpleNamespace(option=lambda *a:SimpleNamespace(is_skip=False,is_overwrite=False,type=SimpleNamespace(value='movie'),metadata=SimpleNamespace(value='NFO')))
    consumer._get_target_fileitem_and_path=lambda **kw:(SimpleNamespace(storage='local'),Path('/owned/film.nfo'))
    consumer.storagechain=SimpleNamespace(get_file_item=lambda **kw:exists)
    consumer.metadata_nfo=lambda **kw:calls.append('nfo') or 'owned NFO'
    consumer._save_file=lambda **kw:calls.append('save')
    consumer._scrape_nfo_generic(SimpleNamespace(),meta,info,SimpleNamespace(value='movie'),overwrite=overwrite)
    existence_cases.append(dict(exists=exists,overwrite=overwrite,calls=calls))
storage_path=ROOT/'app/chain/storage.py'
storage_owner=next(n for n in ast.parse(storage_path.read_text()).body if isinstance(n,ast.ClassDef) and n.name=='StorageChain')
storage_nodes=[copy.deepcopy(n) for n in storage_owner.body if isinstance(n,ast.FunctionDef) and n.name in {'get_file_item','get_parent_item','list_files','upload_file'}]
for n in storage_nodes: setattr(ns['MediaChain'],n.name, None)
storage_tree=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),ast.ClassDef(name='StorageChain',bases=[ast.Name(id='MediaChain',ctx=ast.Load())],keywords=[],body=storage_nodes,decorator_list=[])],type_ignores=[])
exec(compile(ast.fix_missing_locations(storage_tree),str(storage_path),'exec'),ns)
item={'storage':'local','path':'/owned/','type':'dir','name':'owned'}
file_item={'storage':'local','path':'/owned/film.nfo','type':'file','name':'film.nfo'}
storage_cases=[]
for name,method,kwargs,plugin_value,core_value in [
    ('get_none_core','get_file_item',dict(storage='local',path=Path('/owned')),None,item),
    ('parent_false_terminal','get_parent_item',dict(fileitem=item),False,item),
    ('get_list_ignores_core_scalar','get_file_item',dict(storage='local',path=Path('/owned')),[],item),
    ('list_accumulates_native_list','list_files',dict(fileitem=item,recursion=False),[file_item],[item]),
    ('upload_false_terminal','upload_file',dict(fileitem=item,path=Path('/tmp/owned.nfo'),new_name='film.nfo'),False,file_item),
    ('upload_list_runs_native','upload_file',dict(fileitem=item,path=Path('/tmp/owned.nfo'),new_name='film.nfo'),[],file_item),
]:
    calls=[]
    def plugin(**kwargs):calls.append(dict(role='plugin',kwargs=clean(kwargs)));return copy.deepcopy(plugin_value)
    class StorageCore:
        def get_priority(self):return 0
        def get_name(self):return '文件整理'
        def get_file_item(self,storage,path):return self.invoke(dict(storage=storage,path=path))
        def get_parent_item(self,fileitem):return self.invoke(dict(fileitem=fileitem))
        def list_files(self,fileitem,recursion=False):return self.invoke(dict(fileitem=fileitem,recursion=recursion))
        def upload_file(self,fileitem,path,new_name=None):return self.invoke(dict(fileitem=fileitem,path=path,new_name=new_name))
        def invoke(self,kwargs):calls.append(dict(role='core',kwargs=clean(kwargs)));return copy.deepcopy(core_value)
    storage=ns['StorageChain']()
    storage.pluginmanager=SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','Owned'):{method:plugin}})
    storage.modulemanager=SimpleNamespace(get_running_modules=lambda method:[StorageCore()])
    storage.messagehelper=SimpleNamespace(put=lambda **kw:None);storage.eventmanager=SimpleNamespace(send_event=lambda *a,**kw:None)
    returned=getattr(storage,method)(**kwargs)
    storage_cases.append(dict(name=name,method=method,kwargs=clean(kwargs),plugin_result=clean(plugin_value),core_result=clean(core_value),returned=clean(returned),calls=calls))
out=dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind=__doc__,
         source_sha256={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest()for p in [chain,media,signature,storage_path]},
         meta=meta_fields,information=info.fields,cases=cases,
         metadata_img=dict(plugin_calls=0,system_calls=image_calls,merged=images),storage=storage_cases,existence_cases=existence_cases)
Path(__file__).with_name('metadata-module-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print('fixed NFO cases',len(cases),'actual temp writes',sum(len(c['saved']) for c in cases),'system-only images source passed')
