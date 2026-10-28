"""Execute pinned manager class/version getters with synthetic boundaries only."""
import ast, hashlib, json, subprocess, sys
from pathlib import Path
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/core/plugin.py'])
cls=next(n for n in ast.parse(raw).body if isinstance(n,ast.ClassDef) and n.name=='PluginManager')
methods=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ['get_local_plugins','get_local_plugin_version','_process_plugin_info']]
trace=[]
class Plugin:
 def __init__(self): self.plugin_order=0;self.installed=False;self.state=False
class State:
 def __init__(self,name,fail=False):self.name,self.fail=name,fail
 def get_state(self):
  trace.append(self.name)
  if self.fail:raise RuntimeError('owned failure')
  return True
manager=NS(_plugins={'A':NS(plugin_version='9.7',plugin_order=30),'B':NS(plugin_version='2.0',plugin_order=10),'C':NS(plugin_version='3.0',plugin_order=20)},_running_plugins={'B':State('B',True),'A':State('A')},_PluginManager__set_and_check_auth_level=lambda **k:True)
# Extracted methods no longer undergo class-scope name mangling.
manager.__set_and_check_auth_level=lambda **k:True
manager._normalize_plugin_label=lambda value:None
ns=dict(SystemConfigOper=lambda:NS(get=lambda k:['A','C','Ghost']),SystemConfigKey=NS(UserInstalledPlugins='installed'),schemas=NS(Plugin=Plugin),logger=NS(error=lambda *a:None),PluginHelper=NS(annotate_plugin_system_version=lambda x:x),settings=NS(VERSION_FLAG='v2'),StringUtils=NS(compare_version=lambda a,op,b:float(a)<float(b)))
mod=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+methods,type_ignores=[])
exec(compile(ast.fix_missing_locations(mod),'app/core/plugin.py','exec'),ns)
local=ns['get_local_plugins'](manager)
versions={id:ns['get_local_plugin_version'](manager,id) for id in ['A','B','C','Ghost']}
version_trace_count=len(trace)
for _ in range(2):ns['_process_plugin_info'](manager,'A',{'version':'2.0','v2':True},'owned-repo',['A'],1)
market_trace=trace[version_trace_count:]
trace=trace[:version_trace_count]
result={'market_cached_declaration_state_trace':market_trace,'source_commit':commit,'source_sha256':hashlib.sha256(raw).hexdigest(),'class_insertion_order':list(manager._plugins),'running_insertion_order':list(manager._running_plugins),'state_trace':trace,'local_sorted':[{'id':p.id,'installed':p.installed,'state':p.state,'version':p.plugin_version} for p in local],'installed_class_versions':versions,'version_getter_additional_state_calls':len(trace)-2}
Path(__file__).with_name('class-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
