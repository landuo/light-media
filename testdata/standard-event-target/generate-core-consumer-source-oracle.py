"""Execute the fixed concrete ordinary Command and Scheduler consumers."""
import ast
import copy
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

root = Path('/Users/accidia/projects/MoviePilot')
paths = [root/'app/command.py', root/'app/scheduler.py']
ns = {}
for path in paths:
    for node in ast.walk(ast.parse(path.read_text())):
        if isinstance(node, ast.FunctionDef) and node.name in {
                'command_event', 'module_reload_event', 'on_plugin_reload'}:
            node = copy.deepcopy(node)
            node.decorator_list = []
            for argument in node.args.args + node.args.kwonlyargs:
                argument.annotation = None
            node.returns = None
            exec(compile(ast.fix_missing_locations(ast.Module(
                body=[node], type_ignores=[])), str(path), 'exec'), ns)
cases = []
for command in ['/owned   A\tB', '/missing X', '', None, 123]:
    calls, finished = [], []
    ns['_finish_command_processing_status'] = lambda status, user_id: finished.append(
        dict(status=status, user=user_id))
    owner = SimpleNamespace(get=lambda cmd: cmd == '/owned',
                            execute=lambda **kwargs: calls.append(kwargs))
    data = dict(cmd=command, channel='Web', source='owned-source',
                user=9007199254740993, processing_status=None)
    exception = None
    try:
        ns['command_event'](owner, SimpleNamespace(event_data=data))
    except Exception as error:
        exception = type(error).__name__
    cases.append(dict(event='command.excute', data=data,
                      calls=calls, finished=finished, exception=exception))
for data in [dict(plugin_id='OwnedInput'), {}, dict(plugin_id='')]:
    calls = []
    ns['on_plugin_reload'](SimpleNamespace(update_plugin_job=lambda pid: calls.append(pid)),
                           SimpleNamespace(event_data=data))
    cases.append(dict(event='plugin.reload', data=data, calls=calls))
calls = []
ns['module_reload_event'](SimpleNamespace(init_commands=lambda: calls.append('init_commands')),
                          SimpleNamespace())
cases.append(dict(event='module.reload', data={}, calls=calls))
result = dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',
              kind='actual fixed concrete Command/Scheduler event consumer methods; command registry/executor/processing-status and scheduler update are counted boundaries',
              source_sha256={str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
                             for p in paths}, cases=cases)
Path(__file__).with_name('core-consumer-source-oracle.json').write_text(
    json.dumps(result, ensure_ascii=False, indent=2)+'\n')
print('fixed concrete core event consumer cases', len(cases))
