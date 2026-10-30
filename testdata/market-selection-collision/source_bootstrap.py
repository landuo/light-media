"""Whole fixed Settings/System HTTP module bootstrap; unused integrations explicit."""
import os,sys,tempfile,socket,types,contextlib,io,importlib
from pathlib import Path

NETWORK_OBSERVATIONS=[]
def owned_source_network_audit(event,args):
 if event in ('socket.getaddrinfo','socket.gethostbyname','socket.gethostbyaddr'):
  host=args[0]
  allowed=host in ('127.0.0.1','::1','localhost',b'127.0.0.1',b'::1',b'localhost')
  NETWORK_OBSERVATIONS.append({'event':event,'loopback':allowed})
  if not allowed:raise RuntimeError('owned source fixture rejects non-loopback DNS')
 elif event=='socket.connect':
  address=args[1]
  allowed=isinstance(address,tuple) and address[0] in ('127.0.0.1','::1')
  NETWORK_OBSERVATIONS.append({'event':event,'loopback':allowed})
  if not allowed:raise RuntimeError('owned source fixture rejects non-loopback connect')
sys.addaudithook(owned_source_network_audit)

OUT=Path(OWNED_SOURCE_OUTPUT).resolve()
(OUT/'source-temp').mkdir(exist_ok=True)
tempfile.tempdir=str(OUT/'source-temp')
cold=len(sys.argv)==3 and sys.argv[1]=='--cold'
if cold:
 root=Path(sys.argv[2]).resolve();assert root.is_relative_to(OUT/'source-temp') and root.name.startswith('owned-system-') and root.is_dir()
else:
 tmp=tempfile.TemporaryDirectory(prefix='owned-system-');root=Path(tmp.name)
 (root/'app.env').write_text('')
os.environ.clear();os.environ.update({'HOME':str(root),'CONFIG_DIR':str(root),'PATH':'/usr/bin:/bin','PYTHONDONTWRITEBYTECODE':'1','TZ':'UTC','API_TOKEN':'owned-synthetic-settings-probe-only'})
sys.dont_write_bytecode=True
SOURCE=Path(OWNED_SOURCE_ROOT).resolve()
sys.path.insert(0,str(SOURCE))  # Other source dependencies come from the selected interpreter/PYTHONPATH.
def deny(*a,**k):raise RuntimeError('owned system probe network denied')
socket.socket.connect=deny;socket.socket.connect_ex=deny;socket.create_connection=deny
class Unused:
 def __init__(self,*a,**k):raise RuntimeError('unused source integration called')
def auth_fixture(*a,**k):raise RuntimeError('authentication is not exercised by this component witness')
STUBS=[]
def stub(name,**attrs):
 m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m;STUBS.append(name);return m
stub('pillow_avif')
stub('app.helper.sites',SitesHelper=Unused)
stub('app.chain.media',MediaChain=Unused)
stub('app.chain.mediaserver',MediaServerChain=Unused)
stub('app.chain.search',SearchChain=Unused)
stub('app.chain.system',SystemChain=Unused)
stub('app.core.metainfo',MetaInfo=Unused)
stub('app.core.module',ModuleManager=Unused)
stub('app.core.security',verify_apitoken=auth_fixture,verify_resource_token=auth_fixture,verify_token=auth_fixture)
stub('app.db.user_oper',get_current_active_superuser=auth_fixture,get_current_active_superuser_async=auth_fixture,get_current_active_user_async=auth_fixture)
for name,cls in [('image','ImageHelper'),('message','MessageHelper'),('progress','ProgressHelper'),('rule','RuleHelper'),('server','MoviePilotServerHelper'),('system','SystemHelper')]:stub('app.helper.'+name,**{cls:Unused})
stub('app.scheduler',Scheduler=Unused)
stub('app.utils.crypto',HashUtils=Unused)
stub('app.utils.security',SecurityUtils=Unused)
stub('app.utils.rust_accel')
noise=io.StringIO()
with contextlib.redirect_stdout(noise),contextlib.redirect_stderr(noise):
 from app.core.config import settings,Settings
 from app.core.event import eventmanager
 from app.db import Base,Engine
 Base.metadata.create_all(Engine)
 import app.api.endpoints.system as endpoint
 Base.metadata.create_all(Engine)
