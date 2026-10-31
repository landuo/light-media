#!/usr/bin/env python3
"""Prepared-only source method observation. Coordinator executes after admission.

One empty-PID remove on two owned objects. No source/package imports, fake
modules, import hooks, DB, subprocess, socket or HTTP operations. Output stdout
only. The source's real cache invalidation runs against default finders, with
an asserted-empty app.plugins module prefix so no module is removed.
"""
import ast
import hashlib
import importlib
import json
import sys
import threading
from pathlib import Path
from typing import Any, Optional

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s10-remove-source-addendum'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot/app/core/plugin.py')
SOURCE_SHA = 'b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7'
METHODS = ('clear_plugin_agent_tools_cache', 'stop', '__stop_plugin', 'remove_plugin', '_clear_plugin_modules')


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def bound_inputs():
    result = []
    for line in (AUDIT / 'input-bindings.sha256').read_text().splitlines():
        digest, filename = line.split('  ', 1)
        path = Path(filename)
        assert path.is_file() and sha(path) == digest, filename
        result.append({'path': filename, 'sha256': digest})
    return result


def absent_modules():
    matches = [name for name in sys.modules
               if name == 'app.plugins' or name.startswith('app.plugins.')]
    assert not matches, 'real source plugin module prefix must be absent'
    assert not any(name == 'requests' or name.startswith('requests.') or
                   name == 'urllib3' or name.startswith('urllib3.')
                   for name in sys.modules), 'prohibited HTTP package loaded'
    return matches


assert len(sys.argv) == 1
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
INPUTS = bound_inputs()
assert sha(SOURCE) == SOURCE_SHA
BEFORE_THREADS = [t.name for t in threading.enumerate()]
assert BEFORE_THREADS == ['MainThread']
# -I -S starts with these standard finders. Do not install or replace a finder.
FINDERS = [(finder.__module__, finder.__name__) for finder in sys.meta_path]
assert FINDERS == [('_frozen_importlib', 'BuiltinImporter'),
                   ('_frozen_importlib', 'FrozenImporter'),
                   ('_frozen_importlib_external', 'PathFinder')]
BEFORE_PREFIX = absent_modules()
raw_source = SOURCE.read_bytes()
tree = ast.parse(raw_source, filename=str(SOURCE))
original = next(node for node in tree.body
                if isinstance(node, ast.ClassDef) and node.name == 'PluginManager')
selected = [node for node in original.body
            if isinstance(node, ast.FunctionDef) and node.name in METHODS]
assert {node.name for node in selected} == set(METHODS)
assert not any(isinstance(node, (ast.Import, ast.ImportFrom))
               for method in selected for node in ast.walk(method))
records = []
for node in selected:
    first = min([node.lineno] + [d.lineno for d in node.decorator_list])
    fragment = b''.join(raw_source.splitlines(keepends=True)[first - 1:node.end_lineno])
    records.append({'method': node.name, 'first_line': first, 'last_line': node.end_lineno,
                    'source_bytes_sha256': hashlib.sha256(fragment).hexdigest(),
                    'ast_sha256': hashlib.sha256(ast.dump(node, include_attributes=True).encode()).hexdigest()})

TRACE = []


class OwnedLogger:
    def __getattr__(self, level):
        return lambda message: TRACE.append(['log', level, str(message)])


class OwnedEvents:
    def disable_event_handler(self, cls):
        TRACE.append(['event', 'disable', cls.__name__])


class OwnedFirst:
    def close(self):
        TRACE.append(['close', 'OwnedFirst'])

    def stop_service(self):
        TRACE.append(['stop_service', 'OwnedFirst'])

    def get_name(self):
        return 'OwnedFirst'


class OwnedSecond:
    def close(self):
        TRACE.append(['close', 'OwnedSecond'])
        raise ValueError('owned second close failure')

    def stop_service(self):
        TRACE.append(['stop_service', 'OwnedSecond'])

    def get_name(self):
        return 'OwnedSecond'


# Preserve source class name/private mangling and the exact selected nodes,
# decorators, annotations and defaults. Only the class envelope is controlled:
# no original constructor, ConfigReloadMixin, Singleton or module globals run.
envelope = ast.ClassDef(name='PluginManager', bases=[], keywords=[], body=selected,
                        decorator_list=[], type_params=[])
module = ast.fix_missing_locations(ast.Module(body=[envelope], type_ignores=[]))
namespace = {'__builtins__': __builtins__, '__name__': 'owned_source_remove',
             'Any': Any, 'Optional': Optional, 'sys': sys, 'importlib': importlib,
             'logger': OwnedLogger(), 'eventmanager': OwnedEvents()}
exec(compile(module, str(SOURCE), 'exec', dont_inherit=True), namespace)
manager = namespace['PluginManager']()
manager._plugins = {'OwnedFirst': OwnedFirst, 'OwnedSecond': OwnedSecond}
manager._running_plugins = {'OwnedFirst': OwnedFirst(), 'OwnedSecond': OwnedSecond()}
manager._plugin_agent_tools_cache_lock = threading.Lock()
manager._plugin_agent_tools_cache = {'owned': 'cached'}
manager._plugin_agent_tools_revision = 7


def snapshot():
    return {'classes': list(manager._plugins), 'running': list(manager._running_plugins),
            'agent_cache': dict(manager._plugin_agent_tools_cache),
            'agent_revision': manager._plugin_agent_tools_revision}


before = snapshot()
# Recheck immediately before the original all-prefix cache-cleanup branch.
absent_modules()
try:
    returned = manager.remove_plugin('')
    outcome = {'returned': returned, 'type': type(returned).__name__}
except Exception as error:
    outcome = {'exception': type(error).__name__, 'message': str(error)}
after = snapshot()
AFTER_PREFIX = absent_modules()
AFTER_THREADS = [t.name for t in threading.enumerate()]
assert AFTER_THREADS == BEFORE_THREADS
assert [(finder.__module__, finder.__name__) for finder in sys.meta_path] == FINDERS
assert INPUTS == bound_inputs()
print(json.dumps({
    'schema': 's10-source-empty-remove-component-v1',
    'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
    'source_file': str(SOURCE), 'source_sha256': SOURCE_SHA,
    'driver_sha256': sha(__file__),
    'input_manifest_sha256': sha(AUDIT / 'input-bindings.sha256'),
    'python_version': sys.version, 'python_executable': sys.executable,
    'python_executable_sha256': sha(sys.executable),
    'selected_original_nodes': records,
    'case': 'remove_empty_pid_all_two_owned_objects_second_close_error',
    'input': {'plugin_id': '', 'second_close_raises': 'ValueError: owned second close failure'},
    'before': before, 'result': outcome, 'trace': TRACE, 'after': after,
    'module_prefix_before': BEFORE_PREFIX, 'module_prefix_after': AFTER_PREFIX,
    'standard_meta_path_before_after': FINDERS,
    'threads_before': BEFORE_THREADS, 'threads_after': AFTER_THREADS,
    'requests_urllib3_absent': True,
    'boundary': 'one selected-source empty-all branch with two owned callbacks; continuation means final registry/cache cleanup after second close error, not a third plugin; real default-finder invalidation, empty module eviction; no full source host, DB, HTTP, timer, process, recursion or arbitrary plugin claim'
}, ensure_ascii=False, indent=2, allow_nan=False))
