"""Independent no-path local delete/module contract. No permission/identity replacement probes.
Only null and empty deletion paths reach the native provider; its first guard returns
false without reading filesystem paths. No empty-path exists/list operations run.
Only new synthetic fixture files are changed; every original content is preserved
under pristine/ with a SHA256 receipt. No TLS/proxy/CA configuration or networking.
"""
import argparse,hashlib,importlib.util,json,os,shutil,socket,sys,traceback
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--base',required=True);p.add_argument('--source',required=True);p.add_argument('--runtime',required=True);p.add_argument('--out',required=True);p.add_argument('--cold',action='store_true');a=p.parse_args()
B=Path(a.base).resolve();S=Path(a.source).resolve();R=Path(a.runtime).resolve();O=Path(a.out).resolve()
if R==Path('/') or R==S or R==B or not O.is_relative_to(R.parent):raise SystemExit('owned runtime/output required')
if a.cold:
 if (R/'owned-fixture-marker').read_text()!='ordinary-local-storage-pathless-module-v1':raise SystemExit('not this owned fixture')
else:
 R.mkdir(parents=True,exist_ok=False);(R/'owned-fixture-marker').write_text('ordinary-local-storage-pathless-module-v1')
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
 root.mkdir();pristine.mkdir()
 module_path=copy_fixture('module.mkv',b'owned null-storage module file retained')
 class OwnedStorageModule:
  method='';value=None;calls=[]
  def get_state(self):return True
  def get_name(self):return 'Owned null storage module'
  def get_module(self):return {self.method:self.callback}
  def callback(self,**kwargs):self.calls.append(encode(kwargs));return self.value
 provider=OwnedStorageModule();chain.pluginmanager._running_plugins['OwnedStorageModule']=provider
 for label,path_value in [('null',None),('empty','')]:
  for index,value in enumerate([None,False,True,{'ordinary':'value'},[]]):
   provider.method='delete_file';provider.value=value;provider.calls=[]
   observe('delete-'+label+'-'+str(index),'delete_file',{'path':path_value,'storage':'local'})
   result['operations'][-1]['override_value']=value
   result['operations'][-1]['callback_count']=len(provider.calls)
   result['operations'][-1]['file_retained']=module_path.read_bytes()==b'owned null-storage module file retained'
 result['pristine']={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in pristine.iterdir()};assert all(k==v for k,v in result['pristine'].items())
except Exception as e:result['fatal']={'type':type(e).__name__,'message':str(e),'traceback':traceback.format_exc()}
finally:
 if chain:chain.messagequeue.stop();chain.modulemanager.stop()
 result['imports']={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in list(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
 O.write_text(json.dumps(result,indent=2,ensure_ascii=True)+'\n')
 print(json.dumps({'operations':len(result['operations']),'network':network,'fatal':result.get('fatal'),'pristine_count':len(result.get('pristine',{}))}))
 if 'fatal' in result:sys.exit(1)
