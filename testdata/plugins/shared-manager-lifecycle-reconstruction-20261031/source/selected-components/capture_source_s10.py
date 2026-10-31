#!/usr/bin/env python3
"""UNEXECUTED DRAFT: selected unchanged source bodies, owned SQLite, stopped APScheduler.

Run only after independent static review and coordinator grant, with -I -S -B.
No source-package import, fabricated sys.modules entry, import hook, HTTP, or probe.
"""
from __future__ import annotations

import abc
import ast
import asyncio
import copy
import dis
import hashlib
import importlib
import inspect as pyinspect
import json
import re
import sys
import textwrap
import threading
import traceback
from datetime import datetime, timezone
from enum import Enum
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable, Dict, List, Optional, Self, Tuple, Type, Union

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s10-source-preparation'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
SITE = ROOT / 'source-runtime/s10-venv/lib/python3.12/site-packages'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert not any(n == 'requests' or n.startswith('requests.') or
               n == 'urllib3' or n.startswith('urllib3.') for n in sys.modules)
assert len(sys.argv) == 3 and sys.argv[1] in ('warm', 'cold')
PHASE = sys.argv[1]
assert re.fullmatch(r'capture[0-9]+', sys.argv[2])
RUN = AUDIT / sys.argv[2]
DB = RUN / 'owned.sqlite3'


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def check_manifest(name):
    rows = []
    for line in (AUDIT / name).read_text().splitlines():
        digest, filename = line.split('  ', 1)
        path = Path(filename)
        assert path.is_file() and sha(path) == digest, filename
        rows.append({'path': filename, 'sha256': digest})
    return rows


INPUTS = check_manifest('collector-inputs.sha256')
THREADS_BEFORE = [t.name for t in threading.enumerate()]
MODULES_BEFORE = set(sys.modules)
# Explicit isolated installed-dependency root, no site/.pth processing or module replacement.
sys.path.insert(0, str(SITE))
from sqlalchemy import Column, Integer, Sequence, String, JSON, Index, and_, create_engine, inspect, text
from sqlalchemy.orm import Session, as_declarative, declared_attr, scoped_session, sessionmaker
from sqlalchemy.ext.asyncio import AsyncSession
from pydantic import BaseModel, Field
from apscheduler.jobstores.base import JobLookupError
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.date import DateTrigger

assert not any(n == 'requests' or n.startswith('requests.') or
               n == 'urllib3' or n.startswith('urllib3.') for n in sys.modules)

TRACE = []
SELECTED = []


class LogSink:
    def __getattr__(self, level):
        return lambda message: TRACE.append(['log', level, str(message)])


class EventSink:
    def enable_event_handler(self, cls):
        TRACE.append(['event', 'enable', cls.__name__])

    def disable_event_handler(self, cls):
        TRACE.append(['event', 'disable', cls.__name__])


logger = LogSink()
eventmanager = EventSink()
settings = SimpleNamespace(DB_TYPE='sqlite')
AUTH_LEVEL = 1


def SitesHelper():
    # Explicit auth-level integration input, NOT the unavailable real helper/module.
    return SimpleNamespace(auth_level=AUTH_LEVEL)


def SchedulerChain():
    # Explicit notification sink, no ChainBase or host startup.
    return SimpleNamespace(messagehelper=SimpleNamespace(
        put=lambda **kw: TRACE.append(['notification', kw])))


def compile_nodes(path, names=(), class_name=None, methods=None, remove_bases=False, namespace=None):
    """Select original AST nodes; only class envelope/bases are adapted as declared.

    Original selected function bodies, decorators, defaults and field declarations
    are unchanged. Dropping unrelated methods is not labelled a full source module.
    """
    raw = (SOURCE / path).read_bytes()
    tree = ast.parse(raw, filename=str(SOURCE / path))
    selected = []
    if class_name:
        node = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == class_name)
        chosen = [n for n in node.body if not isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
                  or methods is None or n.name in methods]
        if methods is not None:
            assert set(methods) == {n.name for n in chosen if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))}
        for child in chosen:
            start = min([child.lineno] + [d.lineno for d in getattr(child, 'decorator_list', [])])
            fragment = b''.join(raw.splitlines(keepends=True)[start - 1:child.end_lineno])
            SELECTED.append({'file': path, 'class': class_name,
                             'name': getattr(child, 'name', '<field-or-docstring>'),
                             'first_line': start, 'last_line': child.end_lineno,
                             'source_bytes_sha256': hashlib.sha256(fragment).hexdigest(),
                             'ast_sha256': hashlib.sha256(ast.dump(child, include_attributes=True).encode()).hexdigest()})
        node.body = chosen
        if remove_bases:
            node.bases = []
        selected.append(node)
    else:
        selected = [n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.ClassDef)) and n.name in names]
        assert set(names) == {n.name for n in selected}
        for node in selected:
            start = min([node.lineno] + [d.lineno for d in getattr(node, 'decorator_list', [])])
            fragment = b''.join(raw.splitlines(keepends=True)[start - 1:node.end_lineno])
            SELECTED.append({'file': path, 'name': node.name, 'first_line': start,
                             'last_line': node.end_lineno,
                             'source_bytes_sha256': hashlib.sha256(fragment).hexdigest(),
                             'ast_sha256': hashlib.sha256(ast.dump(node, include_attributes=True).encode()).hexdigest()})
    assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(ast.Module(body=selected, type_ignores=[])))
    module = ast.Module(body=selected, type_ignores=[])
    ns = globals() if namespace is None else namespace
    # Source files have no future-annotations import: do not inherit this driver's flag.
    exec(compile(ast.fix_missing_locations(module), str(SOURCE / path), 'exec', dont_inherit=True), ns)
    return ns[class_name] if class_name else None


# Real source definitions; unused source constructors/global import side effects are not executed.
compile_nodes('app/utils/singleton.py', names=('Singleton', 'SingletonClass'))
compile_nodes('app/schemas/types.py', names=('SystemConfigKey',))
compile_nodes('app/schemas/plugin.py', class_name='Plugin')
schemas = SimpleNamespace(Plugin=Plugin)
# ObjectUtils needs stdlib inspect, while ORM Base.update needs SQLAlchemy inspect.
# Evaluate its original AST in its own explicit dependency namespace, no method patch.
ObjectUtils = compile_nodes('app/utils/object.py', class_name='ObjectUtils',
                            methods=('check_method',), namespace=dict(globals(), inspect=pyinspect))

compile_nodes('app/db/__init__.py', names=('get_id_column', '_get_args_db', '_update_args_db', 'db_query', 'db_update', 'DbOper'))
compile_nodes('app/db/__init__.py', class_name='Base', methods=('create', 'get', 'update', 'delete', 'list', '__tablename__'))
compile_nodes('app/db/models/systemconfig.py', class_name='SystemConfig', methods=('get_by_key',))
compile_nodes('app/db/models/plugindata.py', class_name='PluginData', methods=(
    'get_plugin_data', 'get_plugin_data_by_key', 'del_plugin_data_by_key', 'del_plugin_data'))
compile_nodes('app/db/systemconfig_oper.py', class_name='SystemConfigOper', methods=('__init__', 'set', 'get', 'all', 'delete'))
compile_nodes('app/db/plugindata_oper.py', class_name='PluginDataOper', methods=('del_data',))
compile_nodes('app/core/plugin.py', class_name='PluginManager', methods=(
    'init_plugin', 'remove_plugin', 'stop', '__stop_plugin', '_clear_plugin_modules',
    'clear_plugin_agent_tools_cache', 'delete_plugin_config', 'delete_plugin_data',
    'get_plugin_state', 'get_local_plugins', '__set_and_check_auth_level',
    'get_plugin_services', 'get_plugin_attr'), remove_bases=True)
compile_nodes('app/scheduler.py', class_name='Scheduler', methods=('remove_plugin_job', 'update_plugin_job'), remove_bases=True)


def exc_record(call):
    try:
        value = call()
        return {'returned': value, 'type': type(value).__name__}
    except Exception as exc:
        return {'exception': type(exc).__name__, 'message': str(exc)}


class Owned:
    plugin_name = 'Owned'
    plugin_order = 2
    auth_level = 1

    def __init__(self, state=True, fail=None):
        self.state = state
        self.fail = fail
        self.services = []

    def init_plugin(self, conf):
        TRACE.append(['init', conf])
        if self.fail == 'init':
            raise ValueError('owned init failure')

    def get_state(self):
        TRACE.append(['get_state'])
        if self.fail == 'state':
            raise ValueError('owned state failure')
        return self.state

    def close(self):
        TRACE.append(['close'])
        if self.fail == 'close':
            raise ValueError('owned close failure')

    def stop_service(self):
        TRACE.append(['stop_service'])

    def get_name(self):
        return 'Owned'

    def get_page(self):
        return []

    def get_service(self):
        return self.services


class OwnedEmptyPage(Owned):
    plugin_order = 2

    def get_page(self):
        pass


class OwnedRestricted(Owned):
    plugin_order = 1
    auth_level = 3


def manager(classes=None, running=None):
    obj = PluginManager()
    obj._plugins = dict(classes or {})
    obj._running_plugins = dict(running or {})
    obj._config_key = 'plugin.%s'
    obj._plugin_agent_tools_cache = {'owned': 'cached'}
    obj._plugin_agent_tools_cache_lock = threading.Lock()
    obj._plugin_agent_tools_revision = 7
    return obj


def manager_snapshot(obj):
    return {'classes': list(obj._plugins), 'running': list(obj._running_plugins),
            'agent_cache': obj._plugin_agent_tools_cache.copy(),
            'agent_revision': obj._plugin_agent_tools_revision}


def rows():
    with SessionFactory() as session:
        return {'systemconfig': [{'key': row.key, 'value': row.value} for row in session.query(SystemConfig).order_by(SystemConfig.key)],
                'plugindata': [{'plugin_id': row.plugin_id, 'key': row.key, 'value': row.value} for row in session.query(PluginData).order_by(PluginData.plugin_id, PluginData.key)]}


def manager_cases():
    records = []
    for label, obj in [('missing', None), ('enabled', Owned()), ('disabled', Owned(False)), ('init_error', Owned(fail='init'))]:
        m = manager({'Owned': Owned}, {'Owned': obj} if obj else {})
        TRACE.clear()
        result = exc_record(lambda: m.init_plugin('Owned', {'owned': 1}))
        records.append({'case': 'init_' + label, 'result': result, 'trace': list(TRACE), 'after': manager_snapshot(m)})
    for label, obj in [('class_only', None), ('running', Owned()), ('close_error', Owned(fail='close'))]:
        m = manager({'Owned': Owned, 'Other': Owned}, {'Owned': obj} if obj else {})
        assert not any(n == 'app.plugins.owned' or n.startswith('app.plugins.owned.') for n in sys.modules)
        TRACE.clear()
        result = exc_record(lambda: m.remove_plugin('Owned'))
        records.append({'case': 'remove_' + label, 'result': result, 'trace': list(TRACE), 'after': manager_snapshot(m), 'module_eviction': 'no matching real modules; nonempty branch unobserved'})
    for label, obj in [('missing', None), ('json_value', Owned({'active': [1, 'owned']})), ('callback_error', Owned(fail='state'))]:
        m = manager(running={'Owned': obj} if obj else {})
        records.append({'case': 'get_state_' + label, 'result': exc_record(lambda: m.get_plugin_state('Owned'))})
    return records


def sqlite_cases():
    op = SystemConfigOper()
    op.set('plugin.Owned', {'owned': 1})
    op.set('plugin.Other', {'keep': True})
    op.set('unrelated', 'keep')
    with SessionFactory() as session:
        for pid, key in [('Owned', 'a'), ('Owned', 'b'), ('Other', 'a')]:
            PluginData(plugin_id=pid, key=key, value={'owned': pid}).create(session)
    m = manager()
    records = [{'case': 'seed', 'rows': rows()}]
    records.append({'case': 'missing_unforced', 'config_result': m.delete_plugin_config('Owned'), 'data_result': m.delete_plugin_data('Owned'), 'rows': rows()})
    records.append({'case': 'force_both', 'config_result': m.delete_plugin_config('Owned', force=True), 'data_result': m.delete_plugin_data('Owned', force=True), 'cache': op.get('plugin.Owned'), 'rows': rows()})
    m._plugins['Owned'] = Owned
    records.append({'case': 'known_missing_rows', 'config_result': m.delete_plugin_config('Owned'), 'data_result': m.delete_plugin_data('Owned'), 'rows': rows()})
    op.set('plugin.Owned', {'must_remain_in_db': True})
    with Engine.begin() as connection:
        connection.exec_driver_sql("CREATE TRIGGER owned_config_delete_abort BEFORE DELETE ON systemconfig WHEN OLD.key='plugin.Owned' BEGIN SELECT RAISE(ABORT, 'owned delete denied'); END")
    result = exc_record(lambda: m.delete_plugin_config('Owned'))
    records.append({'case': 'actual_sqlite_delete_failure', 'result': result, 'cache': op.get('plugin.Owned'), 'rows': rows()})
    with Engine.begin() as connection:
        connection.exec_driver_sql('DROP TRIGGER owned_config_delete_abort')
    return records


def descriptor_cases():
    global AUTH_LEVEL
    SystemConfigOper().set(SystemConfigKey.UserInstalledPlugins, ['Owned'])
    m = manager({'Empty': OwnedEmptyPage, 'Owned': Owned, 'Restricted': OwnedRestricted},
                {'Empty': OwnedEmptyPage(fail='state'), 'Owned': Owned({'active': [1, 'owned']}), 'Restricted': OwnedRestricted()})
    output = []
    for level in (1, 3):
        AUTH_LEVEL = level
        TRACE.clear()
        descriptors = m.get_local_plugins()
        output.append({'auth_level_input': level,
                       'descriptors': [x.model_dump(mode='python', warnings=False) for x in descriptors],
                       'state_types': {x.id: type(x.state).__name__ for x in descriptors},
                       'trace': list(TRACE)})
    return {'plugin_model_config': dict(Plugin.model_config),
            'defaults': Plugin().model_dump(mode='python'), 'cases': output,
            'auth_boundary': 'real threshold helper with owned numeric provider; no actual SitesHelper/RSA'}


def unused_job(job_id=None):
    raise AssertionError('owned scheduler must never execute a job')


def scheduler_snapshot(s):
    return {'registry': {key: {k: (v.__name__ if callable(v) else v) for k, v in value.items()} for key, value in s._jobs.items()},
            'pending_jobs': [{'id': j.id, 'name': j.name, 'kwargs': j.kwargs} for j in s._scheduler.get_jobs()],
            'scheduler_state': s._scheduler.state, 'thread_created': s._scheduler._thread is not None}


def scheduler_cases():
    obj = Owned()
    manager({'Owned': Owned}, {'Owned': obj})
    s = Scheduler()
    s._lock = threading.RLock()
    s._jobs = {}
    s.start = unused_job  # callback identity boundary; scheduler stays stopped.
    s._scheduler = None
    no_scheduler = [exc_record(lambda: s.remove_plugin_job('Owned')), exc_record(lambda: s.update_plugin_job('Owned'))]
    s._scheduler = BackgroundScheduler(timezone=timezone.utc)
    assert s._scheduler.state == 0 and s._scheduler._thread is None
    empty_pid = exc_record(lambda: s.update_plugin_job(''))
    trigger = DateTrigger(run_date=datetime(2099, 1, 1, tzinfo=timezone.utc), timezone=timezone.utc)
    s._jobs = {'cross': {'pid': 'Other', 'name': 'cross-owned'}, 'Owned_old': {'pid': 'Owned', 'name': 'old'}}
    for jid in ('cross|a', 'cross|b', 'Owned_old|v'):
        s._scheduler.add_job(unused_job, trigger, id=jid)
    TRACE.clear()
    cross_result = exc_record(lambda: s.remove_plugin_job('Owned', 'cross'))
    removed_cross = {'result': cross_result, 'after': scheduler_snapshot(s), 'trace': list(TRACE)}
    obj.services = [
        {'id': 'alpha|one', 'name': 'first', 'func': unused_job, 'trigger': trigger, 'func_kwargs': {'owned': 1}},
        {'id': 'alpha|two', 'name': 'replacement', 'func': unused_job, 'trigger': trigger},
        {'id': 'broken', 'name': 'invalid max_instances', 'func': unused_job, 'trigger': trigger, 'kwargs': {'max_instances': 0}},
        {'id': 'later', 'name': 'after failure', 'func': unused_job, 'trigger': trigger},
    ]
    TRACE.clear()
    update_result = exc_record(lambda: s.update_plugin_job('Owned'))
    updated = {'result': update_result, 'after': scheduler_snapshot(s), 'trace': list(TRACE)}
    TRACE.clear()
    remove_result = exc_record(lambda: s.remove_plugin_job('Owned'))
    removed_all = {'result': remove_result, 'after': scheduler_snapshot(s), 'trace': list(TRACE)}
    assert s._scheduler.state == 0 and s._scheduler._thread is None
    return {'no_scheduler': no_scheduler, 'empty_pid': empty_pid, 'explicit_id_ignores_pid': removed_cross,
            'update_same_prefix_and_add_failure': updated, 'remove_all': removed_all,
            'engine_layer': 'actual APScheduler 3.11.2 pending jobs; never started; no firing/worker evidence'}


if PHASE == 'warm':
    RUN.mkdir(mode=0o700)  # refuses an existing run directory; no overwrite or old DB access.
else:
    assert RUN.is_dir() and DB.is_file() and not DB.is_symlink()
    previous = json.loads((RUN / 'warm-receipt.json').read_text())
    assert previous['database_sha256'] == sha(DB)
    assert previous['driver_sha256'] == sha(__file__)
    assert previous['input_manifest_sha256'] == sha(AUDIT / 'collector-inputs.sha256')

# Actual SQLite only; no source global Engine/AsyncEngine initialization.
url = 'sqlite:///' + str(DB) if PHASE == 'warm' else 'sqlite:///file:' + str(DB) + '?mode=ro&uri=true'
Engine = create_engine(url)
SessionFactory = sessionmaker(bind=Engine)
ScopedSession = scoped_session(SessionFactory)
try:
    if PHASE == 'warm':
        Base.metadata.create_all(Engine)
        result = {'manager_algorithms': manager_cases(), 'actual_sqlite': sqlite_cases(),
                  'actual_schema': descriptor_cases(), 'actual_stopped_scheduler': scheduler_cases()}
        result['terminal_rows'] = rows()
    else:
        result = {'cold_rows': rows(), 'cold_config_after_prior_failure': SystemConfigOper().get('plugin.Owned'),
                  'cold_process': 'separate coordinator invocation, readonly SQLite URL'}
finally:
    ScopedSession.remove()
    Engine.dispose()

assert not any(n == 'requests' or n.startswith('requests.') or
               n == 'urllib3' or n.startswith('urllib3.') for n in sys.modules)
assert THREADS_BEFORE == [t.name for t in threading.enumerate()]
imported = []
for name in sorted(sys.modules):
    filename = getattr(sys.modules[name], '__file__', None)
    if filename and Path(filename).is_file():
        imported.append({'module': name, 'file': filename, 'sha256': sha(filename),
                         'present_before_vendor_imports': name in MODULES_BEFORE})
result.update({'phase': PHASE, 'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
               'environment': 'new isolated pinned component environment, not historical production environment',
               'driver_sha256': sha(__file__), 'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256'),
               'python_executable': sys.executable, 'python_executable_sha256': sha(sys.executable),
               'selected_original_nodes': SELECTED, 'imported_file_bindings': imported,
               'threads_before': THREADS_BEFORE, 'threads_after': [t.name for t in threading.enumerate()],
               'socket_module_loaded': 'socket' in sys.modules,
               'requests_urllib3_absent': True,
               'boundary': 'selected source bodies + actual SQLite/schema/stopped APScheduler; owned callback/event/auth/message inputs; not full source host, HTTP, real plugins, site auth or timer firing'})
if PHASE == 'warm':
    (RUN / 'warm-receipt.json').write_text(json.dumps({'database_sha256': sha(DB), 'driver_sha256': sha(__file__),
        'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256')}, indent=2) + '\n')
check_manifest('collector-inputs.sha256')
print(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False))
