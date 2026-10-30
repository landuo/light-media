"""Whole fixed source endpoint/Manager/Base/DAO/Event components on owned SQLite/files.

Direct handler calls, NOT source auth/browser/full application startup. Parent source
packages import normally except explicit unrelated integration fixtures listed below.
No AST extraction or target output is used to calculate source observations.
"""
from enum import Enum
import subprocess
import contextlib
import hashlib
import importlib
import io
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import types

OUT = Path(__file__).resolve().parent
CASE = sys.argv[1]
assert CASE in ('missing','string','null','empty','dynamic-delete','getter-error','success-with-getter-error')
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
BASE = OUT.parents[1]
sys.dont_write_bytecode = True
sys.path[:0] = [str(BASE / 'audits/system-config-source/reachability/deps04'),
               str(BASE / 'audits/system-config-source/reachability/deps03'),
               str(BASE / 'audits/system-config-source/reachability/deps02'),
               str(BASE / 'worktrees/lifecycle/work/lifecycle/python-deps'), str(SOURCE)]
(OUT / 'source-runtime').mkdir(exist_ok=True)
temp = tempfile.TemporaryDirectory(prefix='owned-reset-', dir=OUT / 'source-runtime')
root = Path(temp.name)
(root / 'app.env').write_text('')
os.environ.clear()
os.environ.update(HOME=str(root), CONFIG_DIR=str(root), PATH='/usr/bin:/bin',
                  PYTHONDONTWRITEBYTECODE='1', TZ='UTC', API_TOKEN='owned-reset-component-only')
os.chdir(root)
def deny(*args, **kwargs):
    raise RuntimeError('owned component forbids all network')
socket.socket.connect = deny
socket.socket.connect_ex = deny
socket.create_connection = deny

fixtures = []
trace = []
def stub(name, reason, **attrs):
    module = types.ModuleType(name)
    module.__dict__.update(attrs)
    sys.modules[name] = module
    fixtures.append({'module': name, 'boundary': reason})
    return module
class Inert:
    def __getattr__(self, name):
        raise RuntimeError('unused integration accessed: ' + name)
class OrdinarySites(Inert):
    auth_level = 1
class SchedulerBoundary:
    def update_plugin_job(self, pid): trace.append(['register_jobs_boundary', pid])
    def remove_plugin_job(self, pid): trace.append(['remove_jobs_boundary', pid])
class CommandBoundary:
    def init_commands(self, pid): trace.append(['register_commands_boundary', pid])
def no_auth(*args, **kwargs):
    raise RuntimeError('auth not part of this direct-handler source witness')

stub('watchfiles', 'watcher not exercised; unexpected access raises', watch=deny)
stub('app.helper.sites', 'ordinary auth_level1 only; no external identity', SitesHelper=OrdinarySites)
stub('app.utils.crypto', 'nonordinary external auth not exercised', RSAUtils=Inert)
stub('app.chain', 'Base ChainBase constructed inert; no business chain calls', ChainBase=Inert)
stub('app.command', 'registration call trace only, not full command scheduler', Command=CommandBoundary)
stub('app.scheduler', 'registration/removal call trace only, not full scheduler', Scheduler=SchedulerBoundary)
stub('app.core.security', 'direct handler bypasses HTTP dependency execution',
     resource_token_cookie=no_auth, verify_apikey=no_auth,
     verify_resource_token=no_auth, verify_token=no_auth)
stub('app.db.user_oper', 'no account/identity DB used',
     get_current_active_superuser=no_auth, get_current_active_superuser_async=no_auth)

noise = io.StringIO()
result = {'scope': __doc__, 'backend': '8a83efeacd3e26b82be495e17d50146e31c96a60',
          'integration_fixtures': fixtures, 'cases': []}
with contextlib.redirect_stdout(noise), contextlib.redirect_stderr(noise):
    from fastapi import FastAPI
    factory = stub('app.factory', 'owned FastAPI object; actual source API route mutation runs', app=FastAPI())
    from app.core.config import settings
    from app.db import Base, Engine
    from app.db.systemconfig_oper import SystemConfigOper
    from app.db.plugindata_oper import PluginDataOper
    Base.metadata.create_all(Engine)
    from app.core.plugin import PluginManager
    import app.core.plugin as manager_module
    from app.core.event import eventmanager
    import app.api.endpoints.plugin as endpoint
    import app.plugins as base_module
    from app.schemas.types import SystemConfigKey
    from sqlalchemy import text
    assert settings.CONFIG_PATH == root
    class RootView:
        ROOT_PATH = root
        def __getattr__(self, name): return getattr(settings, name)
    manager_module.settings = RootView()
    endpoint.settings = RootView()
    manager_module.owned_review_trace = trace
    owned_plugins = root / 'app/plugins'
    owned_plugins.mkdir(parents=True)
    base_module.__path__.insert(0, str(owned_plugins))
    manager = PluginManager()
    config = SystemConfigOper()
    data = PluginDataOper()
    Base.metadata.create_all(Engine)
    import queue, threading, time
    from app.helper.message import MessageHelper
    from app.schemas.types import EventType
    manager_module.owned_event_refs = []
    manager_module.owned_error_seen = threading.Event()
    manager_module.owned_system_errors = []
    for pid, priority, observer in [('OwnedResetMutator', -5, False), ('OwnedResetObserver', 10, True)]:
        directory=owned_plugins/pid.lower();directory.mkdir()
        name_lines = ''
        if not observer and CASE in ('string','null','empty','getter-error','success-with-getter-error'):
            name_lines = """    @property
    def name(self):
        trace.append(['name_access', type(self).__name__])
        if self.config.get('name_getter_error'): raise RuntimeError('owned name getter failure')
        return self.config.get('name_value')
"""
        code = """import json
from app.plugins import _PluginBase
from app.core.plugin import owned_review_trace as trace, owned_event_refs as events, owned_error_seen as error_seen, owned_system_errors as errors
from app.core.event import eventmanager
from app.schemas.types import ChainEventType, EventType
class PID(_PluginBase):
    plugin_name='PID'
    plugin_version='1.0'
    plugin_order=PRIORITY
NAME_LINES
    def init_plugin(self, config=None):
        self.config=config or {}
        if self.config.get('dynamic_delete'): self.name='ordinary runtime name'
        trace.append(['init',type(self).__name__])
    def get_state(self):return self.config.get('enabled',False)
    def get_form(self):return [],{}
    def get_page(self):return []
    def get_api(self):return []
    @eventmanager.register(ChainEventType.PluginDataReset,priority=PRIORITY)
    def reset_first(self,event):
        events.append(event)
        value=event.event_data.model_dump() if hasattr(event.event_data,'model_dump') else event.event_data
        trace.append(['reset_first',type(self).__name__,value])
        if not OBSERVER:event.event_data={'plugin_id':'NOT_THE_ROUTE_TARGET','reset_config':False,'reset_data':False}
    @eventmanager.register(ChainEventType.PluginDataReset,priority=PRIORITY)
    def reset_second(self,event):
        trace.append(['reset_second',type(self).__name__,event.event_data])
        if not OBSERVER:
            event.event_data=None
            if self.config.get('dynamic_delete'):del self.name
            if self.config.get('raise_handler'):raise RuntimeError('owned handler failure after mutation')
    @eventmanager.register(EventType.SystemError)
    def system_error(self,event):
        if OBSERVER:
            errors.append(event.event_data)
            error_seen.set()
    def close(self):trace.append(['close',type(self).__name__])
    def stop_service(self):trace.append(['stop',type(self).__name__])
""".replace('PID',pid).replace('PRIORITY',str(priority)).replace('NAME_LINES',name_lines).replace('OBSERVER',repr(observer))
        (directory/'__init__.py').write_text(code)
    source_id, other_id='OwnedResetMutator','OwnedResetObserver'
    value={'string':'ordinary source name','null':None,'empty':''}.get(CASE)
    initial={'enabled':True,'marker':'retained','raise_handler':CASE!='success-with-getter-error','name_value':value,'name_getter_error':CASE in ('getter-error','success-with-getter-error'),'dynamic_delete':CASE=='dynamic-delete'}
    config.set(SystemConfigKey.UserInstalledPlugins,[source_id,other_id])
    config.set('plugin.'+source_id,initial)
    config.set('plugin.'+other_id,{'enabled':True,'marker':'other'})
    manager.start();eventmanager.start()
    before=manager.running_plugins[source_id];other=manager.running_plugins[other_id]
    data.save(source_id,'marker','retained-data');data.save(other_id,'marker','other-data')
    trace.clear();caught=None;response=None
    try:response=endpoint.reset_plugin(source_id)
    except Exception as error:caught={'class':type(error).__name__,'message':str(error)}
    expected_notification=CASE in ('string','null','empty')
    if expected_notification:assert manager_module.owned_error_seen.wait(5),'real SystemError observer not called'
    notifications=[]
    while True:
        try:notifications.append(json.loads(MessageHelper().sys_queue.get_nowait()))
        except queue.Empty:break
    result['case']=CASE
    result['input']=initial
    result['observation']={'success':None if response is None else response.success,'error':caught,'trace':trace.copy(),'event_data_after':manager_module.owned_event_refs[0].event_data,'notifications':notifications,'system_errors':manager_module.owned_system_errors.copy(),'same_target_object':manager.running_plugins.get(source_id) is before,'same_observer_object':manager.running_plugins.get(other_id) is other,'config':config.get('plugin.'+source_id),'data':data.get_data(source_id,'marker'),'observer_config':config.get('plugin.'+other_id),'observer_data':data.get_data(other_id,'marker')}
    eventmanager.stop();manager.stop()
    env=dict(os.environ);env['PYTHONPATH']=os.pathsep.join(sys.path)
    cold=subprocess.run([sys.executable,str(OUT/'source_reset_cold_read_v2.py')],env=env,capture_output=True,text=True,timeout=20)
    assert cold.returncode==0,cold.stderr
    result['cold']=json.loads(next(line.removeprefix('OWNED_RESULT=') for line in cold.stdout.splitlines() if line.startswith('OWNED_RESULT=')))
    result['cold_driver_sha256']=hashlib.sha256((OUT/'source_reset_cold_read_v2.py').read_bytes()).hexdigest()
    result['source_files']={};result['dependency_files']={}
    for module in tuple(sys.modules.values()):
        name=getattr(module,'__file__',None)
        if name and Path(name).is_file():
            p=Path(name).resolve()
            if p.is_relative_to(root):continue
            (result['source_files'] if p.is_relative_to(SOURCE) else result['dependency_files'])[str(p)]=hashlib.sha256(p.read_bytes()).hexdigest()
    result['fixture_sources']={str(p.relative_to(owned_plugins)):p.read_text() for p in owned_plugins.glob('*/__init__.py')}
    result['driver_sha256']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    result['runtime']={'python':sys.version,'sqlalchemy':importlib.import_module('sqlalchemy').__version__,'sqlite':importlib.import_module('sqlite3').sqlite_version}
    Engine.dispose()
(OUT/('source-'+CASE+'-run05.json')).write_text(json.dumps(result,ensure_ascii=False,indent=2,default=lambda obj: obj.value if isinstance(obj,Enum) else (_ for _ in ()).throw(TypeError(type(obj).__name__)))+'\n')
print(json.dumps({'case':CASE,'source_files':len(result['source_files']),'dependencies':len(result['dependency_files']),'success':result['observation']['success'],'error':result['observation']['error'],'notifications':len(result['observation']['notifications']),'system_errors':len(result['observation']['system_errors'])}))
