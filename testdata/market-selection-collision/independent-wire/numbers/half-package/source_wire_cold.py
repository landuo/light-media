"""Fixed full source endpoint/Manager/helper/Settings/DAO + actual normal product mirror-prefix HTTP.

No source algorithm extraction. Complete source FastAPI router ASGI serialization
uses an owned admin dependency; source authentication/startup are not claimed.
Warm and cold are separate interpreter processes using the same owned config/DB.
"""
import asyncio, contextlib, hashlib, importlib, io, json, os, pathlib, socket, sys

HERE = pathlib.Path(__file__).resolve().parent.parent
OUT = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_OUTPUT']).resolve()
COLD = '--cold' in sys.argv
if not COLD: OUT.mkdir()
else: sys.argv = [sys.argv[0], '--cold', json.loads((OUT/'private-owned-runtime.json').read_text())['root']]
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
if not COLD:
 n['tmp']._finalizer.detach()
 (OUT/'private-owned-runtime.json').write_text(json.dumps({'root':str(n['root'])}))
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
from app.utils.http import aclose_shared_async_transports
import httpx
INPUT=HERE/'half-package'/'mixed-entry-inputs.json'
inputs=json.loads(INPUT.read_text());state={};observations=[]
def response(host,path):
 assert host=='raw.githubusercontent.com'
 filename=path.split('?',1)[0].rsplit('/',1)[-1]
 assert filename in ('package.json','package.v2.json')
 return 200,state['body'] if filename==state['filename'] else '{}'
proxy=OwnedHTTPMirror(response)
settings.update_setting('GITHUB_PROXY',proxy.url);assert not settings.PROXY_HOST;settings.update_setting('PLUGIN_LOCAL_REPO_PATHS','')
wire_app=FastAPI()
async def owned_admin():return None
wire_app.dependency_overrides[api.get_current_active_superuser_async]=owned_admin
wire_app.include_router(api.router,prefix='/plugin')
async def execute():
 async with httpx.AsyncClient(transport=httpx.ASGITransport(app=wire_app,raise_app_exceptions=False),base_url='http://owned-asgi.invalid',trust_env=False) as client:
  for example in inputs['cases']:
   for band,filename in [('base','package.json'),('higher','package.v2.json')]:
    settings.update_setting('PLUGIN_MARKET','https://github.com/Owned/WireCold'+example['name'].replace('-','')+band)
    phases=[]
    for phase in range(2):
     force=not COLD and phase==0
     package=example['package'] if force else {'OwnedCold':{'name':'Owned cold','version':'3.0','v2':True}}
     state.update(body=example['package_json'] if force else json.dumps(package),filename=filename)
     before=len(proxy.requests)
     result=await client.get('/plugin/',params={'state':'market','force':str(force).lower()})
     try:payload=result.json()
     except ValueError:payload=result.text
     phases.append({'force':force,'status':result.status_code,'body':payload,'response_sha256':hashlib.sha256(result.content).hexdigest(),'upstream_body_sha256':hashlib.sha256(state['body'].encode()).hexdigest(),'requests':proxy.requests[before:]})
    observations.append({'name':example['name'],'band':band,'phases':phases})
 await aclose_shared_async_transports()
try:
 with contextlib.redirect_stdout(noise),contextlib.redirect_stderr(noise):asyncio.run(execute())
 assert not proxy.rejected
 imports={}
 for module in tuple(sys.modules.values()):
  path=getattr(module,'__file__',None)
  if path and pathlib.Path(path).is_file():imports[str(pathlib.Path(path).resolve())]=hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
 output={'source_commit':inputs['source_commit'],'source_files':fixed_source,'driver_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'input_sha256':hashlib.sha256(INPUT.read_bytes()).hexdigest(),'phase':'cold' if COLD else 'warm','pid':os.getpid(),'observations':observations,'rejected':proxy.rejected,'boundary':__doc__,'imports':imports,'unused_integration_fixtures':n['STUBS'],'source_network_observations':n.get('NETWORK_OBSERVATIONS',[])}
 (OUT/('cold-result.json' if COLD else 'warm-result.json')).write_text(json.dumps(output,indent=2)+'\n')
 print(json.dumps({'phase':output['phase'],'wire_cases':len(observations),'wire_statuses':[p['status'] for x in observations for p in x['phases']]}))
finally:proxy.close();n['Engine'].dispose()
