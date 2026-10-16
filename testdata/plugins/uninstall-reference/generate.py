"""Execute installed PluginUnInstall methods with synthetic owned state only.

The source snapshot excludes all class metadata, including the private icon URL.
The real remove_plugin/stop/delete methods run against in-memory dictionaries.
Only UUID tempfile directories created by this script may be removed.
"""
import argparse
import ast
import copy
import hashlib
import json
import pathlib
import re
import shutil as actual_shutil
import tempfile
from types import SimpleNamespace as NS
from typing import Any, List, Dict, Tuple, Optional

parser = argparse.ArgumentParser()
parser.add_argument('--source-snapshot', type=pathlib.Path, required=True)
parser.add_argument('--output', type=pathlib.Path, required=True)
parser.add_argument('--form-output', type=pathlib.Path, required=True)
args = parser.parse_args()
source = json.loads(args.source_snapshot.read_text())
assert source['installed_source_sha256'] == '2210e9dea47eeccdc46208d515e45ce1222dcadf50dbfc63cbeb41f94e470487'
assert source['manager_source_sha256'] == 'b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7'
assert not re.search(r'https?://', source['source_without_metadata'])

class NoLog:
    def __getattr__(self, key):
        return lambda *args, **kwargs: None

class SyntheticFailure(Exception):
    pass

def execute(name, config, installed, package_ids=None, fail='', failed_package_ids=None):
    assert all(isinstance(x, str) and re.fullmatch('[A-Za-z][A-Za-z0-9_]*', x) for x in installed)
    trace = []
    stored_installed = list(installed)
    own_config = copy.deepcopy(config)
    configs = {x: {'synthetic': 'retained-config'} for x in installed}
    data = {x: {'synthetic': 'retained-data'} for x in installed}
    removed = []
    with tempfile.TemporaryDirectory(prefix='light-media-uninstall-reference-') as root_text:
        root = pathlib.Path(root_text)
        packages = root / 'app' / 'plugins'
        packages.mkdir(parents=True)
        for pid in package_ids if package_ids is not None else installed:
            (packages / pid.lower()).mkdir(exist_ok=True)
            (packages / pid.lower() / 'synthetic.txt').write_text('owned fixture only')
        def event(kind, **fields):
            trace.append(dict(kind=kind, **fields))
            if fail == kind:
                raise SyntheticFailure()
        class ConfigStore:
            def get(self, key):
                assert key == 'installed'
                event('installed')
                return stored_installed
            def set(self, key, value):
                assert key == 'installed'
                event('save_installed', value=copy.deepcopy(value))
                stored_installed[:] = value
            def delete(self, key):
                pid = key[len('plugin.'):]
                configs.pop(pid, None)
                return True
        store = ConfigStore()
        class DataStore:
            def del_data(self, pid):
                data.pop(pid, None)
        class Base:
            def update_config(self, value):
                nonlocal own_config
                event('save_own_config', value=copy.deepcopy(value))
                own_config = copy.deepcopy(value)
        class TrackedPath:
            def __init__(self, path):
                self.path = pathlib.Path(path)
            def __truediv__(self, child):
                return TrackedPath(self.path / child)
            def exists(self):
                assert self.path.parent == packages
                event('package_exists', directory=self.path.name)
                return self.path.exists()
        def rmtree(path, ignore_errors=False):
            assert isinstance(path, TrackedPath) and path.path.parent == packages and ignore_errors is True
            event('remove_package', directory=path.path.name)
            if path.path.name in [x.lower() for x in failed_package_ids or []]:
                return
            actual_shutil.rmtree(path.path)
        class Scheduler:
            def remove_plugin_job(self, pid):
                event('remove_jobs', id=pid)
        manager_ns = dict(Optional=Optional, SystemConfigOper=lambda: store,
                          PluginDataOper=DataStore, logger=NoLog(),
                          eventmanager=NS(disable_event_handler=lambda plugin_type: None))
        methods = [ast.parse(text).body[0] for text in source['manager_methods'].values()]
        manager_class = ast.ClassDef(name='PluginManager', bases=[], keywords=[], body=methods, decorator_list=[])
        exec(compile(ast.fix_missing_locations(ast.Module(body=[manager_class], type_ignores=[])), '<installed manager methods>', 'exec'), manager_ns)
        manager = manager_ns['PluginManager']()
        manager._plugins = {x: object() for x in installed}
        manager._running_plugins = {x: object() for x in installed}
        manager._config_key = 'plugin.%s'
        manager._PluginManager__stop_plugin = lambda plugin: None
        manager._clear_plugin_modules = lambda *args: None
        manager.clear_plugin_agent_tools_cache = lambda: None
        real_remove = manager.remove_plugin
        def remove(pid):
            event('remove_plugin', id=pid)
            return real_remove(pid)
        manager.remove_plugin = remove
        real_config, real_data = manager.delete_plugin_config, manager.delete_plugin_data
        def delete_config(pid, force=False):
            event('delete_config', id=pid, force=force)
            return real_config(pid, force=force)
        def delete_data(pid, force=False):
            event('delete_data', id=pid, force=force)
            return real_data(pid, force=force)
        manager.delete_plugin_config, manager.delete_plugin_data = delete_config, delete_data
        class Log(NoLog):
            def info(self, text):
                if text.startswith('插件 ') and text.endswith(' 已卸载'):
                    removed.append(text[3:-4])
        ns = dict(_PluginBase=Base, Any=Any, List=List, Dict=Dict, Tuple=Tuple,
                  shutil=NS(rmtree=rmtree), Path=TrackedPath,
                  settings=NS(ROOT_PATH=root), Scheduler=Scheduler,
                  PluginManager=lambda: manager, SystemConfigOper=lambda: store,
                  SystemConfigKey=NS(UserInstalledPlugins='installed'), logger=Log())
        exec(compile(source['source_without_metadata'], '<installed plugin methods without metadata>', 'exec'), ns)
        plugin = ns['PluginUnInstall']()
        error = ''
        try:
            plugin.init_plugin(copy.deepcopy(config))
        except Exception as exc:
            error = type(exc).__name__
        assert plugin.get_state() is False and plugin.get_api() is None and plugin.get_page() is None and plugin.stop_service() is None
        return dict(name=name, config=config, installed=installed,
                    package_ids=installed if package_ids is None else package_ids,
                    fail=fail, failed_package_ids=failed_package_ids or [],
                    expected=dict(trace=trace, error=error, remaining=stored_installed,
                                  own_config=own_config, removed=removed,
                                  config_ids=sorted(configs), data_ids=sorted(data),
                                  package_directories=sorted(p.name for p in packages.iterdir()),
                                  class_ids=sorted(manager._plugins)))

cases = []
for name, config in [('null', None), ('empty_object', {}), ('empty_array', []), ('false', False), ('zero', 0), ('empty_string', ''),
                     ('missing_selection', {'clear_config': True}), ('null_selection', {'plugin_ids': None}),
                     ('empty_selection', {'plugin_ids': []}), ('false_selection', {'plugin_ids': False})]:
    cases.append(execute(name, config, ['Alpha', 'Beta']))
for clear_config in (False, True):
    for clear_data in (False, True):
        cases.append(execute('flags-%s-%s' % (clear_config, clear_data),
                             {'plugin_ids': ['Beta'], 'clear_config': clear_config, 'clear_data': clear_data, 'unknown': 'discard after run'}, ['Alpha', 'Beta']))
cases.extend([
    execute('missing_flags_reset_null', {'plugin_ids': ['Alpha']}, ['Alpha', 'Beta']),
    execute('unknown_target_still_resets', {'plugin_ids': ['Unknown'], 'clear_config': None, 'clear_data': None}, ['Alpha', 'Beta']),
    execute('selected_duplicates_do_not_repeat', {'plugin_ids': ['Alpha', 'Alpha']}, ['Alpha', 'Beta']),
    execute('installed_duplicates_do_repeat', {'plugin_ids': ['Alpha'], 'clear_config': True, 'clear_data': True}, ['Alpha', 'Beta', 'Alpha']),
    execute('installed_order_not_selection_order', {'plugin_ids': ['Beta', 'Alpha']}, ['Alpha', 'Beta', 'Gamma']),
    execute('case_sensitive_ids', {'plugin_ids': ['alpha']}, ['Alpha', 'Beta']),
    execute('substring_container', {'plugin_ids': 'PrefixAlphaSuffix'}, ['Alpha', 'Al', 'Beta']),
    execute('mapping_membership_keys', {'plugin_ids': {'Beta': False}}, ['Alpha', 'Beta']),
    execute('truthy_scalar_errors_at_membership', {'plugin_ids': True}, ['Alpha']),
    execute('truthy_scalar_empty_installed_resets', {'plugin_ids': True}, []),
    execute('truthy_nonboolean_flags', {'plugin_ids': ['Alpha'], 'clear_config': 'False', 'clear_data': [False]}, ['Alpha']),
    execute('missing_package_still_requests_clears', {'plugin_ids': ['Alpha'], 'clear_config': True, 'clear_data': True}, ['Alpha'], package_ids=[]),
    execute('ignored_package_error', {'plugin_ids': ['Alpha'], 'clear_config': True, 'clear_data': True}, ['Alpha'], failed_package_ids=['Alpha']),
    execute('self_uninstall_preserves_and_resets_own_config', {'plugin_ids': ['PluginUnInstall'], 'clear_config': True, 'clear_data': True}, ['Alpha', 'PluginUnInstall']),
    execute('remove_jobs_failure_stops_before_class', {'plugin_ids': ['Alpha']}, ['Alpha', 'Beta'], fail='remove_jobs'),
    execute('save_installed_failure_no_own_reset', {'plugin_ids': ['Alpha']}, ['Alpha', 'Beta'], fail='save_installed'),
    execute('save_own_config_failure', {'plugin_ids': ['Alpha']}, ['Alpha', 'Beta'], fail='save_own_config'),
])

form_cases = []
for name, local in [
    ('empty', []),
    ('stable_installed_sort_and_self', [dict(id='Late', installed=True, plugin_order=1000, plugin_name='晚', plugin_version='1'), dict(id='Uninstalled', installed=False, plugin_order=0), dict(id='SameFirst', installed=True, plugin_order=10, plugin_name='首', plugin_version='2'), dict(id='SameSecond', installed=True, plugin_order=10, plugin_name='次', plugin_version='3'), dict(id='PluginUnInstall', installed=True, plugin_order=98, plugin_name='插件彻底卸载', plugin_version='2.2')]),
    ('defaults_and_falsey_ids', [dict(installed=True, plugin_order=1), dict(id='', installed=True, plugin_order=2), dict(id='Fallback', installed=True), dict(id='Nulls', installed=True, plugin_name=None, plugin_version=None)]),
    ('truthy_installed_and_string_order', [dict(id='B', installed=1, plugin_order='b'), dict(id='A', installed='yes', plugin_order='a')]),
    ('mixed_order_error', [dict(id='A', installed=True, plugin_order=1), dict(id='B', installed=True, plugin_order=None)]),
]:
    ns = dict(_PluginBase=object, Any=Any, List=List, Dict=Dict, Tuple=Tuple,
              PluginManager=lambda: NS(get_local_plugins=lambda: [NS(**p) for p in local]))
    exec(compile(source['source_without_metadata'], '<installed get_form methods>', 'exec'), ns)
    error, form, model, options = '', None, None, None
    try:
        form, model = ns['PluginUnInstall']().get_form()
        options = ns['PluginUnInstall'].get_local_plugins()
    except Exception as exc:
        error = type(exc).__name__
    form_cases.append(dict(name=name, local_plugins=local, options=options, form=form, model=model, error=error))
    if name == 'empty':
        args.form_output.write_text(json.dumps(dict(conf=form, model=model), ensure_ascii=False, indent=2) + '\n')

output = dict(source='Installed original PluginUnInstall 2.2 init/get_form and actual PluginManager stop/remove/delete AST; synthetic in-memory state and newly owned temporary package files only',
              installed_source_sha256=source['installed_source_sha256'], manager_source_sha256=source['manager_source_sha256'],
              private_class_metadata_omitted=True, source_snapshot_sha256=hashlib.sha256(args.source_snapshot.read_bytes()).hexdigest(),
              cases=cases, form_cases=form_cases)
args.output.write_text(json.dumps(output, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'execution_cases':len(cases), 'form_cases':len(form_cases), 'output_sha256':hashlib.sha256(args.output.read_bytes()).hexdigest()}, indent=2))
