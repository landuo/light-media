"""Fixed clone and reload/start methods against owned actual filesystem/code.
The original module-import loader is replaced only with compile/exec of the
owned cloned file; this preserves actual Python identifier syntax failures.
No old service, official authentication or excluded AI cache runs.
"""
import ast,copy,hashlib,json,tempfile,types,errno,sys,unicodedata
from pathlib import Path
from typing import *
source=Path('/Users/accidia/projects/MoviePilot/app/core/plugin.py');text=source.read_text();owner=next(n for n in ast.parse(text).body if isinstance(n,ast.ClassDef) and n.name=='PluginManager')
names={'clone_plugin','_modify_plugin_files','_modify_python_file','_modify_federation_files','_rename_federation_assets','reload_plugin','start','get_plugin_config'}
nodes=[copy.deepcopy(n) for n in owner.body if isinstance(n,ast.FunctionDef) and n.name in names];assert {n.name for n in nodes}==names
for f in nodes:
 if f.name=='start':f.body=[n for n in f.body if not(isinstance(n,ast.Expr) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Attribute) and n.value.func.attr=='clear_plugin_agent_tools_cache')]
logger=types.SimpleNamespace(**{k:lambda *a,**kw:None for k in ['debug','info','warning','warn','error']});settings=types.SimpleNamespace(ROOT_PATH=None)
ns=dict(Path=Path,Tuple=Tuple,List=List,Dict=Dict,Optional=Optional,Any=Any,logger=logger,settings=settings,EventType=types.SimpleNamespace(PluginReload='plugin.reload'),traceback=__import__('traceback'))
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='PluginManager',bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),'<fixed clone/reload/start>','exec'),ns);Manager=ns['PluginManager'];Manager.plugins=property(lambda self:self._plugins)
endpoint=Path('/Users/accidia/projects/MoviePilot/app/api/endpoints/plugin.py');endpoint_nodes=[copy.deepcopy(n) for n in ast.parse(endpoint.read_text()).body if isinstance(n,ast.FunctionDef) and n.name in ['clone_plugin','reload_plugin','register_plugin','_add_clone_to_plugin_folder','uninstall_plugin','_remove_plugin_from_folders']]
for f in endpoint_nodes:f.decorator_list=[]
ns.update(User=Any,Depends=lambda fn:None,get_current_active_superuser=lambda:None,schemas=types.SimpleNamespace(Response=lambda **kw:types.SimpleNamespace(**kw)),shutil=__import__('shutil'))
exec(compile(ast.fix_missing_locations(ast.Module(body=endpoint_nodes,type_ignores=[])),'<fixed clone HTTP and reload/register/folder>','exec'),ns)
original='OwnedModuleOverride';code='''class OwnedCloneFixture:
    plugin_name = "Owned original"
    plugin_desc = "owned source"
    plugin_version = "1.0"
    plugin_icon = ""
    plugin_config_prefix = "ownedclonefixture_"
    def init_plugin(self, config=None):
        self.config = config
        self.init_count = getattr(self, "init_count", 0) + 1
    def get_state(self):
        return bool(self.config and self.config.get("enabled"))
'''
code=code.replace('OwnedCloneFixture',original)
source_config={'enabled':True,'marker':'owned-source'}
def owned_exists(path):
 try:return path.exists()
 except OSError as error:
  if error.errno==errno.ENAMETOOLONG:return False
  raise
cases=[]
for suffix in ['', '_Copy', 'A'*21, 'A'*(96-len(original)), 'A'*(97-len(original)), '备份', 'ÉCopy', 'a.b', 'a-b', ' ', 'İCopy', 'ΟΣ', 'K', 'Ａ', 'e\u0301', 'a·b', 'A'*(255-len(original)), 'A'*(256-len(original)), '💫','!','a?b','a#b','a%b','a:b',"a'b",'a"b','a+b','a;b']:
 with tempfile.TemporaryDirectory() as directory:
  root=Path(directory);settings.ROOT_PATH=root;folder=root/'app/plugins'/original.lower();folder.mkdir(parents=True);(folder/'__init__.py').write_text(code);(folder/'dist').mkdir();(folder/'dist/remoteEntry.js').write_text("window.MoviePilotAPI.get('plugin/"+original+"/status');")
  initial={};exec(compile(code,'<owned source>','exec'),initial);klass=initial[original]
  manager=object.__new__(Manager);manager._plugins={original:klass};manager._running_plugins={};config={original:copy.deepcopy(source_config)};system={'installed':[original],'folders':{'owned':[original]}};observations=[]
  ns['PluginManager']=lambda:manager;ns['SystemConfigKey']=types.SimpleNamespace(UserInstalledPlugins='installed',PluginFolders='folders');ns['SystemConfigOper']=lambda:types.SimpleNamespace(get=lambda key:copy.deepcopy(config.get(key[7:]) if key.startswith('plugin.') else system.get(key)),set=lambda key,value:system.__setitem__(key,copy.deepcopy(value)))
  ns['eventmanager']=types.SimpleNamespace(send_event=lambda *a,**kw:observations.append({'stage':'reload-event','kind':a[0]}),enable_event_handler=lambda *a:None,disable_event_handler=lambda *a:None)
  manager.is_plugin_exists=lambda pid:pid in manager._plugins
  manager._config_key="plugin.%s"
  manager.save_plugin_config=lambda pid,value,force=False:config.__setitem__(pid,copy.deepcopy(value))
  manager.stop=lambda pid:manager._running_plugins.pop(pid,None)
  manager._PluginManager__set_and_check_auth_level=lambda **kw:True
  def load(pid,installed,check):
   path=root/'app/plugins'/pid.lower()/'__init__.py';local={}
   try:exec(compile(path.read_text(),str(path),'exec'),local)
   except Exception as err:observations.append({'stage':'load','error':type(err).__name__});return []
   return [v for v in local.values() if isinstance(v,type) and check(v)]
  manager._load_selective_plugins=load
  ns['Scheduler']=lambda:types.SimpleNamespace(update_plugin_job=lambda pid:observations.append({'stage':'register-services','plugin_id':pid}))
  ns['Command']=lambda:types.SimpleNamespace(init_commands=lambda pid:observations.append({'stage':'register-commands','plugin_id':pid}))
  ns['register_plugin_api']=lambda pid:observations.append({'stage':'register-api','plugin_id':pid,'running':pid in manager._running_plugins})
  ns['remove_plugin_api']=lambda pid:None
  ns['Scheduler']=lambda:types.SimpleNamespace(update_plugin_job=lambda pid:observations.append({'stage':'register-services','plugin_id':pid}),remove_plugin_job=lambda pid:None)
  manager.delete_plugin_config=lambda pid:config.pop(pid,None)
  manager.delete_plugin_data=lambda pid:None
  manager.remove_plugin=manager.stop
  response=ns['clone_plugin'](plugin_id=original,clone_data={'suffix':suffix})
  result=(response.success,response.message)
  target=original+suffix.lower();instance=manager._running_plugins.get(target);target_path=root/'app/plugins'/target.lower()
  row={'suffix':suffix,'source_id':original,'target_id':target,'target_length':len(target),'success':result[0],'message':result[1],'code_exists':owned_exists(target_path),'loaded':instance is not None,'init_count':getattr(instance,'init_count',None),'config':copy.deepcopy(config.get(target)),'public_config':manager.get_plugin_config(target),'installed':target in system['installed'],'folder_member':target in system['folders']['owned'],'observations':observations}
  if suffix and result[0]:
   manager._running_plugins.pop(target,None);manager._plugins.pop(target,None);manager.start(target)
   row['cold_loaded']=target in manager._running_plugins
   row['cold_public_config']=manager.get_plugin_config(target)
   response=ns['uninstall_plugin'](target)
   row['uninstall']={'success':response.success,'code_exists':owned_exists(target_path),'config':copy.deepcopy(config.get(target)),'public_config':manager.get_plugin_config(target),'installed':target in system['installed'],'folder_member':target in system['folders']['owned'],'loaded':target in manager._running_plugins,'class_registered':target in manager._plugins}
  cases.append(row)
out={'reference_runtime':{'python':sys.version,'unicode':unicodedata.unidata_version,'purpose':'development-only actual fixed-source replay; Go production has no Python dependency'},'source_config':source_config,'fixed_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':'app/core/plugin.py:1907-2135','source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'endpoint_sha256':hashlib.sha256(endpoint.read_bytes()).hexdigest(),'kind':'actual fixed clone HTTP/helper/copy/modify/reload/start/register/folder functions; owned source files, registries and Python compiler loader boundary','excluded_in_reference':['official auth is user-excluded and returns true at original check boundary','out-of-scope final AI tool cache call omitted from start AST'],'cases':cases}
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(len(cases),'actual clone filesystem/identifier cases')
