"""Independent ordinary missing-descendant contract; exclusive synthetic tree, no permission/link/race probes."""
import hashlib,json,os,socket,sys,traceback
from pathlib import Path
B=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-20261030'); S=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot'); E=B/'audits/recovery-storage-independent/candidate05'; R=E/'source-owned'; R.mkdir(exist_ok=False); (R/'config').mkdir(); (R/'files').mkdir(); (R/'pristine').mkdir()
sys.dont_write_bytecode=True
os.environ.clear();os.environ.update(HOME=str(R),CONFIG_DIR=str(R/'config'),PATH='/usr/bin:/bin',PYTHONDONTWRITEBYTECODE='1',TZ='UTC',API_TOKEN='owned-storage-independent',DEV='false',PLUGIN_AUTO_RELOAD='false');os.chdir(R)
sys.path[:0]=[str(B/'audits/s09-local-storage-source/deps01')]+[str(B/'audits/system-config-source/reachability'/d) for d in ('deps04','deps03','deps02','deps')]+[str(B/'worktrees/lifecycle/work/lifecycle/python-deps'),str(S)]
network=[]
def deny(*args,**kw): network.append('blocked'); raise RuntimeError('network forbidden')
socket.socket.connect=deny;socket.socket.connect_ex=deny;socket.create_connection=deny
def audit(event,args):
 if event in {'socket.getaddrinfo','socket.gethostbyname','socket.gethostbyaddr','socket.connect','socket.connect_ex','socket.sendto','socket.sendmsg'}:network.append(event);raise RuntimeError('network forbidden')
sys.addaudithook(audit)
result={'network_attempts':network,'operations':[]};chain=None
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
 def selected(cls,package_path,filter_func=lambda n,o:True):
  choices={'app.modules':[FileManagerModule],'app.modules.filemanager.storages':[LocalStorage]}
  return [c for c in choices[package_path] if filter_func(c.__name__,c)]
 ModuleHelper.load=classmethod(selected)
 chain=StorageChain()
 content=b'independent synthetic regular file preserved';h=hashlib.sha256(content).hexdigest();(R/'pristine'/h).write_bytes(content);regular=R/'files'/'regular.mkv';regular.write_bytes(content)
 result['timestamps']=[]
 for label,seconds,nanos in [('epoch-three',0,3),('pre-epoch-fraction',-1,999999997),('2038-boundary',2147483648,999999999),('unixnano-overflow-edge',9223372036,854775808),('year2300',10413792000,123456789),('ordinary-recovery',1700000000,123456789)]:
  ns=seconds*1000000000+nanos;os.utime(regular,ns=(ns,ns));st=regular.stat()
  if st.st_mtime_ns!=ns:raise RuntimeError('filesystem did not preserve timestamp')
  row={'case':label,'seconds':seconds,'nanoseconds':nanos,'actual_ns':st.st_mtime_ns,'actual_float':st.st_mtime}
  row['returned']=[x.model_dump(mode='json') for x in chain.list_files(FileItem(path=str(regular)))];row['get_file_item']=chain.get_file_item(storage='local',path=regular).model_dump(mode='json');result['operations'].append(row)
 result['content_unchanged']=regular.read_bytes()==content
except Exception as err:result['fatal']={'type':type(err).__name__,'message':str(err),'traceback':traceback.format_exc()}
finally:
 if chain:chain.messagequeue.stop();chain.modulemanager.stop()
 result['imports']={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in list(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
 (E/'source-mtime-independent.json').write_text(json.dumps(result,indent=2,ensure_ascii=True)+'\n');print(json.dumps({k:v for k,v in result.items() if k!='imports'}))
 if result.get('fatal') or network:sys.exit(1)
