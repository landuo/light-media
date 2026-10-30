"""Fixed full source endpoint/Manager/helper/Settings/DAO + actual normal product mirror-prefix HTTP.

No source algorithm extraction. Direct endpoint calls do not exercise source
authentication. Explicit bootstrap fixtures cover unused unrelated integrations.
"""
import asyncio, contextlib, hashlib, importlib, io, json, os, pathlib, socket, sys

HERE = pathlib.Path(__file__).resolve().parent
OUT = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_OUTPUT']).resolve(); OUT.mkdir()
SOURCE_ROOT = pathlib.Path(os.environ['MOVIEPILOT_SOURCE']).resolve()
INPUT = HERE / 'upstream-inputs.json'
fixed_source = {
    'app/core/plugin.py': 'b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7',
    'app/helper/plugin.py': 'a7455fbb0f0e09267476b2628459dcfd1af26a94e9fc4eea04b51e5f08dfdcba',
    'app/utils/string.py': 'b21856bf4b309d20fe9d5a68538fbb0ddc7a2c23bbde424e5700caf57587297a',
    'app/api/endpoints/plugin.py': '16e96285764587b266c07af21b6df140836c00f916d6626b87584a492a4031ce',
    'app/schemas/plugin.py': 'd57a96c4ab5ec4525ce56e40dccffa93da719626449ed9bf7be80f47809e0f1a',
    'app/core/config.py': '3be390c00277672706c3cb2f28d480d1e6d67541b6fd4afaaf490a0ee3966a6e',
}
for relative, digest in fixed_source.items():
    assert hashlib.sha256((SOURCE_ROOT / relative).read_bytes()).hexdigest() == digest, 'fixed source changed: ' + relative
inputs = json.loads(INPUT.read_text())
real_connect, real_connect_ex = socket.socket.connect, socket.socket.connect_ex
bootstrap = HERE / 'source_bootstrap.py'
n = {'__file__': str(bootstrap), '__name__': '__owned_selection_bootstrap__', 'OWNED_SOURCE_OUTPUT': str(OUT), 'OWNED_SOURCE_ROOT': str(SOURCE_ROOT)}
exec(compile(bootstrap.read_text(), str(bootstrap), 'exec'), n)
def loopback(self, address):
    if not isinstance(address, tuple) or address[0] not in ('127.0.0.1', '::1'):
        raise RuntimeError('nonowned source network denied')
    return real_connect(self, address)
socket.socket.connect = loopback
socket.socket.connect_ex = lambda self, address: real_connect_ex(self, address) if isinstance(address, tuple) and address[0] in ('127.0.0.1', '::1') else (_ for _ in ()).throw(RuntimeError('nonowned source network denied'))
settings, root, stub, Unused, deny = n['settings'], n['root'], n['stub'], n['Unused'], n['deny']
class OrdinarySites:
    auth_level = 1
    def __getattr__(self, name):
        raise RuntimeError('unused nonordinary source integration')
stub('app.helper.sites', SitesHelper=OrdinarySites)
sys.modules['app.utils.crypto'].RSAUtils = Unused
stub('watchfiles', watch=deny)
stub('app.command', Command=Unused)
security = sys.modules['app.core.security']
security.resource_token_cookie = n['auth_fixture']
security.verify_apikey = n['auth_fixture']
from fastapi import FastAPI
stub('app.factory', app=FastAPI())
noise = io.StringIO()
with contextlib.redirect_stdout(noise), contextlib.redirect_stderr(noise):
    from app.core.plugin import PluginManager
    from app.helper.plugin import PluginHelper
    import app.core.plugin as manager_module
    class OwnedRootView:
        ROOT_PATH = root
        def __getattr__(self, name):
            return getattr(settings, name)
    manager_module.settings = OwnedRootView()
    manager = PluginManager()
    api = importlib.import_module('app.api.endpoints.plugin')
    n['Base'].metadata.create_all(n['Engine'])
from owned_http_mirror import OwnedHTTPMirror, assert_clean_environment
assert_clean_environment()
state = {}
def response(host, path):
    assert host == 'raw.githubusercontent.com'
    parts = path.split('?', 1)[0].strip('/').split('/')
    assert len(parts) == 4 and parts[0] == 'Owned' and parts[2] == 'main'
    repo, filename = parts[1], parts[3]
    assert repo in state['packages'] and filename in ('package.json', 'package.v2.json')
    return 200, json.dumps(state['packages'][repo]['higher' if filename == 'package.v2.json' else 'base'])
proxy = OwnedHTTPMirror(response)
settings.update_setting('GITHUB_PROXY', proxy.url); assert not settings.PROXY_HOST
observations = []
history_cases = {}
additional_direct_cases = []
def projection(rows):
    return [{'id': row.id, 'version': row.plugin_version, 'repo': 'local' if PluginHelper.is_local_repo_url(row.repo_url) else row.repo_url, 'history': row.history} for row in rows]
async def execute():
    from app.utils.http import aclose_shared_async_transports
    for number, case in enumerate(inputs['cases']):
        state.clear(); state.update(case)
        settings.update_setting('PLUGIN_MARKET', ','.join(case['markets']))
        local = root / ('local-' + str(number))
        local.mkdir(); (local / 'plugins').mkdir()
        (local / 'package.json').write_text(json.dumps(case['local']))
        for pid in case['local']:
            (local / 'plugins' / pid.lower()).mkdir()
        settings.update_setting('PLUGIN_LOCAL_REPO_PATHS', str(local) if case['local'] else '')
        request_start = len(proxy.requests)
        local_candidates = PluginHelper().get_local_plugin_candidates()
        local_rows = manager.get_local_repo_plugins()
        online_rows = await manager.async_get_online_plugins(force=True)
        market_rows = await api.all_plugins(None, state='market', force=True)
        observations.append({'name': case['name'], 'local_helper_ids': list(local_candidates), 'local_rows': projection(local_rows), 'online_rows': projection(online_rows), 'market_rows': projection(market_rows), 'requests': proxy.requests[request_start:]})
    # Actual public source local installation and Manager construction of both
    # ordinary synthetic classes. Base/Chain construction is not claimed: these
    # tiny test classes do not inherit _PluginBase or use messaging integrations.
    import app.helper.plugin as helper_module
    helper_module.PLUGIN_DIR = root / 'app/plugins'
    helper_module.PLUGIN_DIR.mkdir(parents=True)
    stub('app.chain', ChainBase=type('UnusedChainBase', (), {}))
    stub('app.helper.message', MessageHelper=Unused)
    import app.plugins
    app.plugins.__path__.insert(0, str(helper_module.PLUGIN_DIR))
    local_case_number = next(i for i, value in enumerate(inputs['cases']) if value['local'])
    local_case = inputs['cases'][local_case_number]
    local = root / ('local-' + str(local_case_number))
    settings.update_setting('PLUGIN_LOCAL_REPO_PATHS', str(local))
    from app.db.systemconfig_oper import SystemConfigOper
    from app.schemas.types import SystemConfigKey
    ids = list(local_case['local'])
    SystemConfigOper().set(SystemConfigKey.UserInstalledPlugins, ids)
    history_cases['installed'] = []
    for pid in ids:
        version = local_case['local'][pid]['version']
        module = local / 'plugins' / pid.lower() / '__init__.py'
        module.write_text('class ' + pid + ':\n    plugin_name=' + repr(pid) + '\n    plugin_version=' + repr(version) + '\n    def init_plugin(self, config=None): self.count = getattr(self, "count", 0) + 1\n    def get_state(self): return False\n    def get_form(self): return [], {}\n    def get_page(self): return []\n    def stop_service(self): pass\n')
        ok, _ = PluginHelper().install(pid, PluginHelper.make_local_repo_url(pid, local))
        assert ok
        manager.start(pid)
        assert manager.running_plugins[pid].count == 1
        history_cases['installed'].append({'id': pid, 'version': version, 'actual_public_install': True, 'manager_init_count': 1})
    history_cases['local'] = {}
    for pid in ids:
        value = await api.plugin_history(pid, None, force=True)
        history_cases['local'][pid] = {'history': value.history, 'version': value.plugin_version, 'repo': 'local' if PluginHelper.is_local_repo_url(value.repo_url) else value.repo_url}
    state.clear(); state.update(inputs['cases'][0])
    settings.update_setting('PLUGIN_MARKET', ','.join(state['markets']))
    settings.update_setting('PLUGIN_LOCAL_REPO_PATHS', '')
    history_cases['online'] = {}
    for pid in ids:
        value = await api.plugin_history(pid, None, force=True)
        history_cases['online'][pid] = {'history': value.history, 'version': value.plugin_version, 'repo': value.repo_url}
    manager.stop()
    # Further direct source observations distinguish unknown/local rank and
    # dictionary replacement position, including repeated/empty configured URLs.
    from app.schemas import Plugin
    early, late = inputs['cases'][0]['markets']
    scenarios = [
        ('unknown precedes local and retains unknown insertion order', [early, late], [], [('OwnedX', '1.0', 'local://OwnedX'), ('OwnedX', '1.0', 'https://github.com/Owned/Unknown'), ('OwnedY', '1.0', 'https://github.com/Owned/Other')]),
        ('duplicate markets use first index and empties omitted', [early, '', late, early], [], [('OwnedX', '1.0', late), ('OwnedX', '1.0', early)]),
        ('case sensitive local prefix is not normalized', [], [], [('OwnedX', '1.0', 'local://OwnedX'), ('OwnedX', '1.0', 'LOCAL://OwnedX')]),
        ('per ID replacement preserves first insertion position', [early, late], [], [('OwnedX', '1.0', early), ('OwnedY', '1.0', early), ('OwnedX', '2.0', late)]),
        ('empty declarations', [], [], []),
    ]
    for name, markets, higher, base in scenarios:
        settings.update_setting('PLUGIN_MARKET', ','.join(markets))
        make = lambda rows: [Plugin(id=pid, plugin_version=version, repo_url=repo) for pid, version, repo in rows]
        selected = manager.process_plugins_list(make(higher), make(base))
        additional_direct_cases.append({'name': name, 'markets': markets, 'higher': [{'id': p, 'version': v, 'repo': r} for p, v, r in higher], 'base': [{'id': p, 'version': v, 'repo': r} for p, v, r in base], 'selected': [{'id': p.id, 'version': p.plugin_version, 'repo': p.repo_url} for p in selected]})
    await aclose_shared_async_transports()
try:
    with contextlib.redirect_stdout(noise), contextlib.redirect_stderr(noise):
        asyncio.run(execute())
    imports = {}
    for module in tuple(sys.modules.values()):
        path = getattr(module, '__file__', None)
        if path and pathlib.Path(path).is_file():
            imports[str(pathlib.Path(path).resolve())] = hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
    import importlib.metadata
    versions = {name: importlib.metadata.version(name) for name in ('packaging', 'httpx', 'httpcore', 'SQLAlchemy')}
    assert not proxy.rejected, 'source attempted a nonowned destination'
    output = {'dependency_versions': versions, 'source_files': fixed_source, 'source_commit': inputs['source_commit'], 'input_sha256': hashlib.sha256(INPUT.read_bytes()).hexdigest(), 'driver_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(), 'observations': observations, 'history_cases': history_cases, 'additional_direct_cases': additional_direct_cases, 'imports': imports, 'bootstrap_sha256': hashlib.sha256(bootstrap.read_bytes()).hexdigest(), 'boundary': __doc__ + ' Public helper installation and full Manager load/init use plain owned synthetic classes; unused Base/Chain imports are fixtures, not full business plugin initialization.', 'unused_integration_fixtures': n['STUBS'], 'source_network_observations': n.get('NETWORK_OBSERVATIONS',[])}
    (OUT / 'result.json').write_text(json.dumps(output, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'source_upstream_cases': len(observations), 'requests': len(proxy.requests), 'imports': len(imports)}))
finally:
    proxy.close(); n['Engine'].dispose()
