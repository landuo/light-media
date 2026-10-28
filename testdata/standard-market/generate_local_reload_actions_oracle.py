"""Actual fixed watcher loop; owned single event/classifier/copy boundaries."""
import ast,hashlib,json,subprocess,sys,time
from pathlib import Path
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60';raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/core/plugin.py'])
method=next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='_run_file_watcher');method.decorator_list=[]
ns={'Path':Path,'time':time,'settings':NS(ROOT_PATH=Path('/owned/runtime')),'PluginHelper':NS(get_local_repo_paths=lambda:[]),'logger':NS(**{key:lambda *a:None for key in ['info','debug','warn','error']})}
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed/core/plugin.py','exec'),ns)
cases=[]
for kind in ['frontend_ready','frontend_incomplete','frontend_incompatible','external_python','runtime_python','metadata','requirements']:
 actions=[];candidate={'id':'Owned','compatible':kind!='frontend_incompatible','package_version':'v2'}
 name='remoteEntry.js' if kind.startswith('frontend') else '__init__.py' if kind.endswith('python') else 'requirements.txt' if kind=='requirements' else 'package.v2.json'
 event=Path('/owned/local/plugins.v2/owned')/name
 ns['watch']=lambda *a,**kw:[{(2,str(event))}]
 def sync(pid,entry):actions.append('sync');return True
 owner=NS(_stop_monitor_event=object(),_recent_local_sync={},_get_federated_plugin_change=lambda path:('Owned',candidate,kind!='frontend_incomplete') if kind.startswith('frontend') else None,_get_plugin_id_from_path=lambda path:'Owned' if kind=='runtime_python' else None,_get_local_plugin_candidate_from_path=lambda path:candidate,_sync_local_plugin_if_installed=sync,reload_plugin=lambda pid:actions.append('reload'))
 ns['_run_file_watcher'](owner)
 cases.append(dict(kind=kind,actions=actions))
Path(__file__).with_name('local-reload-actions-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256=hashlib.sha256(raw).hexdigest(),kind='actual fixed _run_file_watcher; owned finite watch/classifier/copy boundaries; not real monitoring',cases=cases),indent=2)+'\n')
