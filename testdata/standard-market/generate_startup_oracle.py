"""Replay pinned startup functions with owned executor/providers, never services."""
import ast, asyncio, hashlib, json, subprocess, sys
from pathlib import Path
from types import SimpleNamespace as NS

commit = '8a83efeacd3e26b82be495e17d50146e31c96a60'
paths = ['app/startup/lifecycle.py', 'app/startup/plugins_initializer.py']
sources = {path: subprocess.check_output(['git', '-C', sys.argv[1], 'show', commit+':'+path]) for path in paths}
selected = {'init_extra', 'sync_plugins', 'execute_task'}
nodes = [node for raw in sources.values() for node in ast.parse(raw).body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name in selected]
assert len(nodes) == 3
cases = []
for kind, safe, sync_result, deps_result in [('noop', False, [], []), ('synced', False, ['Owned'], []), ('dependencies', False, [], ['owned-dependency']), ('safe', True, ['Owned'], []), ('sync_exception', False, None, [])]:
    calls = []
    def record(name): return lambda *args, **kwargs: calls.append(name)
    class Loop:
        async def run_in_executor(self, executor, task): return task()
    def sync():
        calls.append('sync')
        if sync_result is None: raise RuntimeError('owned fixture sync failure')
        return sync_result
    def dependencies(): calls.append('dependencies'); return deps_result
    async def report(): calls.append('usage')
    manager = NS(sync=sync, install_plugin_missing_dependencies=dependencies, init_config=record('init_config'))
    ns = dict(settings=NS(MOVIEPILOT_SAFE_MODE=safe), global_vars=NS(loop=Loop()), PluginManager=lambda:manager, SystemHelper=lambda:NS(set_system_modified=record('system_modified')), SystemChain=lambda:NS(restart_finish=record('restart_finish')), MoviePilotServerHelper=NS(async_report_usage=report), logger=NS(**{key:lambda *args,**kwargs:None for key in ['debug','info','error']}), register_plugin_api=record('register_api'), init_plugin_scheduler=record('register_scheduler'), restart_command=record('restart_command'))
    exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])), 'fixed/startup', 'exec'), ns)
    asyncio.run(ns['init_extra']())
    if kind == 'safe': assert calls == ['system_modified', 'restart_finish']
    elif kind in ['noop','sync_exception']: assert calls == ['sync','dependencies','system_modified','restart_finish','usage']
    else: assert calls == ['sync','dependencies','init_config','register_api','register_scheduler','restart_command','system_modified','restart_finish','usage']
    cases.append(dict(kind=kind, calls=calls))
Path(__file__).with_name('startup-oracle.json').write_text(json.dumps(dict(source_commit=commit, source_sha256={path:hashlib.sha256(raw).hexdigest() for path,raw in sources.items()}, kind='actual fixed async functions; synthetic executor/provider boundaries, not production startup evidence', cases=cases), indent=2)+'\n')
