"""Real installed method AST, exclusively owned in-memory and tempfile fixtures.
No app imports, original configuration, actual packages or live services.
"""
import ast,copy,hashlib,json,pathlib,shutil,tempfile,threading
from types import SimpleNamespace as NS
from typing import Any,Union,Optional,List,Dict,Tuple
HERE=pathlib.Path(__file__).parent
snapshot=HERE/'fault-source-without-metadata.json'
s=json.loads(snapshot.read_text()); deps=s['dependencies']
class SyntheticFailure(Exception):pass
class JobLookupError(Exception):pass
class SystemConfigKey:
 UserInstalledPlugins='installed'
 def __init__(self,value):self.value=value
class SilentLog:
 def __getattr__(self,key):return lambda *a,**k:None

def cls(name, methods, scope):
 tree=ast.ClassDef(name=name,bases=[],keywords=[],body=[ast.parse(m).body[0] for m in methods],decorator_list=[])
 exec(compile(ast.fix_missing_locations(ast.Module(body=[tree],type_ignores=[])),'installed-bounded-AST','exec'),scope)
 return scope[name]

def case(stage='',target='Alpha',clear=True):
 trace=[]; installed=['Alpha','Beta','PluginUnInstall']; initial={'clear_config':clear,'clear_data':clear,'plugin_ids':['Alpha','Beta']}
 durable={'installed':installed[:],'plugin.PluginUnInstall':copy.deepcopy(initial)}; cache=copy.deepcopy(durable)
 configs={p:True for p in installed}; data={p:True for p in installed}
 jobs={p+'.job':{'pid':p,'name':'owned'} for p in installed}; backend={k+'|owned':True for k in jobs}; notices=[]
 def note(kind,pid=''):
  trace.append({'stage':kind,'target':pid})
  if (stage==kind or (stage=='scheduler_notice' and kind=='scheduler_get')) and (not pid or pid==target):raise SyntheticFailure(kind)
 class ConfigModel:
  def __init__(self,key,value=None):self.key=key;self.value=value;self.id=key
  @classmethod
  def get_by_key(c,db,key):return ConfigModel(key,copy.deepcopy(durable[key])) if key in durable else None
  def update(self,db,payload):
   note('commit_installed' if self.key=='installed' else 'commit_own')
   durable[self.key]=copy.deepcopy(payload['value'])
  def create(self,db):
   note('commit_installed' if self.key=='installed' else 'commit_own');durable[self.key]=copy.deepcopy(self.value)
  def delete(self,db,rid):note('commit_installed' if self.key=='installed' else 'commit_own');durable.pop(self.key,None)
 sc={'Union':Union,'SystemConfigKey':SystemConfigKey,'Any':Any,'Optional':Optional,'copy':copy,'SystemConfig':ConfigModel}
 Store=cls('SystemConfigOper',deps['/app/app/db/systemconfig_oper.py']['methods'].values(),sc)
 store=Store();store._rlock=threading.RLock();store._db=object();store._SystemConfigOper__SYSTEMCONF=cache
 store.get=lambda key:copy.deepcopy(cache.get(key))
 class Plugin:
  def __init__(self,pid):self.pid=pid
  def close(self):note('close',self.pid)
  def stop_service(self):note('stop_service',self.pid)
  def get_name(self):return self.pid
 fake_sys=NS(modules={'app.plugins.'+p.lower():object() for p in installed})
 ns={'Optional':Optional,'SystemConfigOper':lambda:store,'PluginDataOper':lambda:NS(del_data=lambda p:data.pop(p,None)),
     'logger':SilentLog(),'eventmanager':NS(disable_event_handler=lambda c:note('disable_events')), 'sys':fake_sys,'importlib':NS(invalidate_caches=lambda:note('invalidate_modules'))}
 Manager=cls('PluginManager',deps['/app/app/core/plugin.py']['methods'].values(),ns)
 manager=Manager();manager._plugins={p:Plugin(p) for p in installed};manager._running_plugins=manager._plugins.copy();manager._config_key='plugin.%s';manager._plugin_agent_tools_cache_lock=threading.RLock();manager._plugin_agent_tools_cache={'owned':True};manager._plugin_agent_tools_revision=0
 manager.get_plugin_attr=lambda pid,k:pid
 class Backend:
  def get_jobs(self):
   note('scheduler_get',scheduler.pid)
   return [NS(id=k) for k in backend]
  def remove_job(self,jid):
   note('scheduler_remove',scheduler.pid);backend.pop(jid,None)
 def notification(**kw):note('scheduler_notice',scheduler.pid);notices.append(kw['title'])
 sch_ns={'Optional':Optional,'PluginManager':lambda:manager,'JobLookupError':JobLookupError,'SchedulerChain':lambda:NS(messagehelper=NS(put=notification)),'logger':SilentLog()}
 Scheduler=cls('Scheduler',deps['/app/app/scheduler.py']['methods'].values(),sch_ns)
 scheduler=Scheduler();scheduler._scheduler=Backend();scheduler._lock=threading.RLock();scheduler._jobs=jobs
 orig_remove=scheduler.remove_plugin_job
 def remove(pid):scheduler.pid=pid;note('remove_jobs_entry',pid);return orig_remove(pid)
 scheduler.remove_plugin_job=remove
 b={'SystemConfigOper':lambda:store}
 Base=cls('_PluginBase',deps['/app/app/plugins/__init__.py']['methods'].values(),b);Base.systemconfig=store
 with tempfile.TemporaryDirectory(prefix='owned-pu-fault-') as text:
  root=pathlib.Path(text);pkg=root/'app/plugins';pkg.mkdir(parents=True)
  for p in installed:(pkg/p.lower()).mkdir()
  pns={'_PluginBase':Base,'Any':Any,'List':List,'Dict':Dict,'Tuple':Tuple,'shutil':shutil,'Path':pathlib.Path,'settings':NS(ROOT_PATH=root),'Scheduler':lambda:scheduler,'PluginManager':lambda:manager,'SystemConfigOper':lambda:store,'SystemConfigKey':SystemConfigKey,'logger':SilentLog()}
  exec(compile(s['plugin_source'],'installed-plugin-without-metadata','exec'),pns)
  error=''
  try:pns['PluginUnInstall']().init_plugin(copy.deepcopy(initial))
  except Exception as e:error=type(e).__name__
  return {'fault':stage,'target':target,'error':error,'trace':trace,'cache_installed':cache['installed'],'durable_installed':durable['installed'],'cache_own':cache['plugin.PluginUnInstall'],'durable_own':durable['plugin.PluginUnInstall'],'classes':list(manager._plugins),'jobs':list(jobs),'backend_jobs':list(backend),'packages':sorted(p.name for p in pkg.iterdir()),'notices':len(notices),'configs_retained':sorted(configs),'data_retained':sorted(data)}

cases=[case()]
for fault in ['close','stop_service','scheduler_get','scheduler_remove','scheduler_notice','commit_installed','commit_own','remove_jobs_entry']:
 for target in (['Alpha','Beta'] if fault not in ['commit_installed','commit_own'] else ['Alpha']):cases.append(case(fault,target))
# A notification failure escapes the scheduler's own catch, keeping its already
# popped job dictionary but stopping before that target's class is removed.
# Compose both faults without changing installed methods in a separate fixture.
out={'snapshot_sha256':hashlib.sha256(snapshot.read_bytes()).hexdigest(),'installed_plugin_sha256':s['plugin_source_sha256'],'scope':'owned method AST only; model commit stub follows real db_update commit/rollback/rethrow','cases':cases}
p=HERE/'fault-oracle.json';p.write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'outcomes':[{'fault':c['fault'],'target':c['target'],'error':c['error'],'classes':len(c['classes']),'durable_installed':len(c['durable_installed']),'cache_selection':len(c['cache_own']['plugin_ids']),'durable_selection':len(c['durable_own']['plugin_ids'])} for c in cases]},indent=2))
