"""Actual fixed monitor/candidate AST, owned settings and paths only."""
import ast,hashlib,json,subprocess,sys,tempfile
from pathlib import Path
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/core/plugin.py'])
methods=[n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name in ['reload_monitor','_get_local_plugin_candidate_from_path']]
module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+methods,type_ignores=[])
for n in methods:n.decorator_list=[]
ns={'Path':Path,'logger':NS(error=lambda *a:None)};exec(compile(ast.fix_missing_locations(module),'fixed/core/plugin.py','exec'),ns)
cases=[]
with tempfile.TemporaryDirectory() as temporary:
 root=Path(temporary).resolve()
 class Helper:
  @staticmethod
  def get_local_repo_paths():return [root]
  def get_local_plugin_candidate(self,**kwargs):return {'strict_system_version':kwargs['strict_system_version'],'strict_compat':kwargs['strict_compat']}
 ns['PluginHelper']=Helper
 for developer in [False,True]:
  for reload in [False,True]:
   actions=[];ns['settings']=NS(DEV=developer,PLUGIN_AUTO_RELOAD=reload)
   owner=NS(stop_monitor=lambda:actions.append('stop'),__start_monitor=lambda:actions.append('start'))
   ns['reload_monitor'](owner)
   candidate=ns['_get_local_plugin_candidate_from_path'](root/'plugins.v2/owned/__init__.py')
   assert candidate is not None
   cases.append(dict(developer=developer,auto_reload=reload,actions=actions,candidate=candidate))
Path(__file__).with_name('local-watch-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256=hashlib.sha256(raw).hexdigest(),kind='actual fixed method AST; owned thread/helper boundaries and temporary path; not watcher execution',cases=cases),indent=2)+'\n')
