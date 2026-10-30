"""Independent ordinary local-storage contract. No permission/identity replacement probes.
Only new synthetic fixture files are changed; every original content is preserved
under pristine/ with a SHA256 receipt. No TLS/proxy/CA configuration or networking.
"""
import argparse,hashlib,importlib.util,json,os,shutil,socket,sys,traceback
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--base',required=True);p.add_argument('--source',required=True);p.add_argument('--runtime',required=True);p.add_argument('--out',required=True);p.add_argument('--cold',action='store_true');a=p.parse_args()
B=Path(a.base).resolve();S=Path(a.source).resolve();R=Path(a.runtime).resolve();O=Path(a.out).resolve()
if R==Path('/') or R==S or R==B or not O.is_relative_to(R.parent):raise SystemExit('owned runtime/output required')
if a.cold:
 if (R/'owned-fixture-marker').read_text()!='ordinary-local-storage-v2':raise SystemExit('not this owned fixture')
else:
 R.mkdir(parents=True,exist_ok=False);(R/'owned-fixture-marker').write_text('ordinary-local-storage-v2')
(R/'config').mkdir(exist_ok=True)
os.environ.clear();os.environ.update(HOME=str(R),CONFIG_DIR=str(R/'config'),PATH='/usr/bin:/bin',PYTHONDONTWRITEBYTECODE='1',TZ='UTC',API_TOKEN='owned-local-storage-fixture',DEV='false',PLUGIN_AUTO_RELOAD='false')
os.chdir(R);sys.dont_write_bytecode=True
sys.path[:0]=[str(B/'audits/s09-local-storage-source/deps01')]+[str(B/'audits/system-config-source/reachability'/d) for d in ('deps04','deps03','deps02','deps')]+[str(B/'worktrees/lifecycle/work/lifecycle/python-deps'),str(S)]
network=[]
def deny(*a,**kw):network.append('blocked network attempt');raise RuntimeError('ordinary local fixture has no network')
socket.socket.connect=deny;socket.socket.connect_ex=deny;socket.create_connection=deny
def network_audit(event,args):
 if event in {'socket.getaddrinfo','socket.gethostbyname','socket.gethostbyaddr','socket.connect','socket.connect_ex','socket.sendto','socket.sendmsg'}:
  network.append(event);raise RuntimeError('ordinary local fixture has no network or DNS')
sys.addaudithook(network_audit)
result={'scope':__doc__,'network_attempts':network,'cold':a.cold,'operations':[]};chain=None
try:
 import app.helper
 app.helper.__path__.append(str(B/'audits/resource-inputs/original-helper-inputs'))
 from app.chain.storage import StorageChain
 from app.core.module import ModuleManager
 from app.modules.filemanager import FileManagerModule
 from app.modules.filemanager.storages.local import LocalStorage
 from app.helper.module import ModuleHelper
 from app.schemas import FileItem
 from app.db import Engine
 from app.db.models.systemconfig import SystemConfig
 SystemConfig.metadata.create_all(Engine)
 discovery=[]
 def selected(cls,package_path,filter_func=lambda n,o:True):
  choices={'app.modules':[FileManagerModule],'app.modules.filemanager.storages':[LocalStorage]}
  if package_path not in choices:raise RuntimeError('unselected module discovery '+package_path)
  discovery.append(package_path);return [c for c in choices[package_path] if filter_func(c.__name__,c)]
 ModuleHelper.load=classmethod(selected)
 chain=StorageChain();result['discovery']=discovery;result['module_ids']=chain.modulemanager.get_module_ids()
 root=R/'files';pristine=R/'pristine'
 def encode(v):
  if hasattr(v,'model_dump'):return v.model_dump(mode='json')
  if isinstance(v,list):return [encode(x) for x in v]
  return v
 def observe(case,method,item,**kw):
  row={'case':case,'method':method,'input':item,'kwargs':kw}
  try:
   fi=FileItem(**item);row['fileitem']=encode(fi);row['returned']=encode(getattr(chain,method)(fi,**kw))
  except Exception as e:row['error']={'type':type(e).__name__,'message':str(e)}
  result['operations'].append(row)
 def copy_fixture(name,data):
  saved=pristine/hashlib.sha256(data).hexdigest()
  if not saved.exists():saved.write_bytes(data)
  else:assert saved.read_bytes()==data
  target=root/name;target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(saved,target)
  return target
 if not a.cold:
  root.mkdir();pristine.mkdir()
  for name in ['dir.z','dir.a','.hidden','@eaDir','empty','tree']:(root/name).mkdir()
  for name,data in [('z.mkv',b'z'),('a.TXT',b'abc'),('.dot',b'd'),('plain',b'p'),('dir.z/nested.mkv',b'nested'),('dir.a/child.nfo',b'nfo'),('.hidden/invisible.mkv',b'x'),('@eaDir/invisible.mkv',b'x'),('tree/child',b'tree')]:copy_fixture(name,data)
  for child in list(root.rglob('*'))+[root]:os.utime(child,(1700000000,1700000000))
  result['defaults']=[FileItem().model_dump(mode='json'),FileItem(path=None,storage=None,children=None).model_dump(mode='json')]
  for case,method,item,kw in [
   ('exists-file','exists',{'path':str(root/'z.mkv')},{}),('exists-dir','exists',{'path':str(root/'dir.z'),'type':'file'},{}),('exists-missing','exists',{'path':str(root/'missing')},{}),('exists-null-path','exists',{'path':None},{}),('exists-null-storage','exists',{'path':str(root/'z.mkv'),'storage':None},{}),('exists-both-null','exists',{'path':None,'storage':None},{}),
   ('list-shallow','list_files',{'path':str(root),'type':'file'},{}),('list-recursive','list_files',{'path':str(root)},{'recursion':True}),('list-file','list_files',{'path':str(root/'z.mkv'),'type':'dir'},{}),('list-missing','list_files',{'path':str(root/'missing')},{}),('list-null-storage','list_files',{'path':str(root),'storage':None},{}),
   ('delete-null-path','delete_file',{'path':None},{}),('delete-empty-path','delete_file',{'path':''},{}),('delete-missing','delete_file',{'path':str(root/'missing')},{}),('delete-null-storage','delete_file',{'path':str(root/'z.mkv'),'storage':None},{}),('delete-file-wrong-type','delete_file',{'path':str(root/'a.TXT'),'type':'dir'},{}),('delete-tree','delete_file',{'path':str(root/'tree'),'type':'file'},{}),('delete-repeat','delete_file',{'path':str(root/'tree')},{})]:observe(case,method,item,**kw)
  replacement=copy_fixture('\ufffd',b'replacement must remain');unicode=[]
  for name in ['\ud800','\udcff','\U0001f600','plain']:
   if name=='\U0001f600':copy_fixture(name,b'emoji owned fixture')
   for method in ['exists','list_files','delete_file']:
    raw=json.dumps({'path':str(root/name)},ensure_ascii=True,separators=(',',':')).encode();before=len(result['operations']);observe('json-'+hex(ord(name[0]))+'-'+method,method,json.loads(raw));row=result['operations'].pop();row['raw_input']=raw.decode();row['raw_sha256']=hashlib.sha256(raw).hexdigest();row['filename_codepoints']=[ord(c) for c in name];row['replacement_retained']=replacement.read_bytes()==b'replacement must remain';unicode.append(row)
  result['unicode_operations']=unicode
  # Whole installed class; only unrelated construction/optional effects excluded.
  path=B/'source-input/installed-plugin-sources/removelink/__init__.py';spec=importlib.util.spec_from_file_location('owned_source_removelink',path);mod=importlib.util.module_from_spec(spec);sys.modules[spec.name]=mod;spec.loader.exec_module(mod)
  plugin=object.__new__(mod.RemoveLink);plugin._storagechain=chain;plugin.strm_path_mappings=str(R/'strm')+':'+str(root);plugin._delete_history=False;plugin._delete_scrap_infos=False;plugin._notify=False
  consumer=copy_fixture('Consumer.2026.mkv',b'owned consumer fixture');result['consumer']={'mapping':plugin._parse_strm_path_mappings(),'before_exists':consumer.exists(),'returned':plugin.handle_strm_deleted(R/'strm/Consumer.2026.strm'),'after_exists':consumer.exists()}
  class OwnedStorageModule:
   method='';value=None
   def get_state(self):return True
   def get_name(self):return 'Owned storage module'
   def get_module(self):return {self.method:self.callback}
   def callback(self,**kwargs):return self.value
  provider=OwnedStorageModule();chain.pluginmanager._running_plugins['OwnedStorageModule']=provider;module_path=copy_fixture('module.mkv',b'owned module fixture');result['module_operations']=[]
  for name,method,value,operation in [('exists-false','get_file_item',False,'exists'),('exists-empty-dict','get_file_item',{},'exists'),('exists-truthy','get_file_item',{'ordinary':'value'},'exists'),('delete-false','delete_file',False,'delete_file'),('delete-true','delete_file',True,'delete_file'),('delete-null-native','delete_file',None,'delete_file'),('list-empty-merge','list_files',[],'list_files'),('list-scalar-terminal','list_files',False,'list_files')]:
   provider.method=method;provider.value=value;item=FileItem(path=str(module_path if operation!='list_files' else root));result['module_operations'].append({'case':name,'method':operation,'override_method':method,'override_value':encode(value),'returned':encode(getattr(chain,operation)(item)),'file_exists':module_path.exists()})
  chain.pluginmanager._running_plugins.clear();bulk=root/'bulk';bulk.mkdir()
  for i in range(11003):copy_fixture('bulk/item-%05d.mkv'%i,b'x')
  listing=chain.list_files(FileItem(path=str(bulk)));result['large']={'count':len(listing),'large_name_length':700*1024,'large_name_exists':chain.exists(FileItem(path=str(bulk/'item-00000.mkv'),name='x'*(700*1024)))}
 else:
  for name in ['a.TXT','tree','Consumer.2026.mkv','z.mkv','\ufffd'] :observe('cold-'+name,'exists',{'path':str(root/name)})
 result['pristine']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in pristine.iterdir()};assert all(k==v for k,v in result['pristine'].items())
except Exception as e:result['fatal']={'type':type(e).__name__,'message':str(e),'traceback':traceback.format_exc()}
finally:
 if chain:chain.messagequeue.stop();chain.modulemanager.stop()
 result['imports']={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in list(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
 O.write_text(json.dumps(result,indent=2,ensure_ascii=True)+'\n')
 print(json.dumps({'operations':len(result['operations']),'network':network,'fatal':result.get('fatal'),'pristine_count':len(result.get('pristine',{}))}))
 if 'fatal' in result:sys.exit(1)
