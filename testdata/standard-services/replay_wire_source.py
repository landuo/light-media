"""完整固定 Scheduler/Manager/ProgressHelper/MemoryBackend 组件回放，所有输入为独占合成数据。"""
import asyncio, hashlib, importlib.metadata, json, os, socket, sys, threading, time, types
from pathlib import Path
import tempfile
sys.dont_write_bytecode = True
from replay_support import HERE, COMMIT, prepare, prohibit_network, provenance, imported_versions, digest
args = prepare(source=True)
SRC, OUT = args.source_root, args.output
binding = json.loads((HERE / 'wire-source-bindings.json').read_text())
assert binding['source_commit'] == COMMIT
for name, expected in binding['files'].items():
    if digest(SRC / name) != expected:
        raise RuntimeError('fixed HTTP source changed: ' + name)
owned_runtime = tempfile.TemporaryDirectory(prefix='runtime-', dir=OUT)
ROOT = Path(owned_runtime.name)
(ROOT / 'app.env').write_text('')
os.environ.clear();os.environ.update(HOME=str(ROOT),CONFIG_DIR=str(ROOT),PATH='/usr/bin:/bin',PYTHONDONTWRITEBYTECODE='1',TZ='UTC',PLUGIN_AUTO_RELOAD='false',DEV='false',USAGE_STATISTIC_SHARE='false',PLUGIN_STATISTIC_SHARE='false',MP_SERVER_HOST='',CACHE_BACKEND_TYPE='memory')
os.chdir(ROOT)
blocked, deny = prohibit_network()
fixtures=[];notices=[]
def stub(name,reason,**attrs):
    """显式声明范围外依赖，不替换待验组件。"""
    m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m;fixtures.append({'module':name,'boundary':reason});return m
class Inert:
    """范围外的业务方法一旦访问即失败。"""
    def __getattr__(self,name):raise RuntimeError('excluded business accessed: '+name)
class MessageBoundary:
    """仅记录服务失败通知交接，不发送任何消息。"""
    def put(self,**kwargs):notices.append(kwargs)
class ChainBoundary:
    """本切片不执行系统媒体、工作流和通知业务。"""
    def __init__(self):self.messagehelper=MessageBoundary()
    def send(self,*a,**k):notices.append({'send_boundary':True})
    def __getattr__(self,name):raise RuntimeError('business chain accessed: '+name)
chain=stub('app.chain','non-plugin host business not executed',ChainBase=ChainBoundary);chain.__path__=[]
for mod,name in [('mediaserver','MediaServerChain'),('recommend','RecommendChain'),('site','SiteChain'),('subscribe','SubscribeChain'),('transfer','TransferChain'),('workflow','WorkflowChain')]:stub('app.chain.'+mod,'non-plugin builtin jobs not initialized',**{name:Inert})
stub('watchfiles','not a watcher slice',watch=deny)
class SitesBoundary(Inert):auth_level=1
stub('app.helper.sites','official licensing excluded',SitesHelper=SitesBoundary)
stub('app.helper.image','wallpaper not in plugin jobs',WallpaperHelper=Inert)
stub('app.helper.message','record notice boundary only',MessageHelper=MessageBoundary)
from app.core.config import settings, global_vars
assert settings.CONFIG_PATH==ROOT and settings.CACHE_BACKEND_TYPE!='redis'
from app.db import Base,Engine
from app.db import models
Base.metadata.create_all(Engine)
from app.core.plugin import PluginManager
from app.plugins import _PluginBase
from app.helper.progress import ProgressHelper
from app.core.cache import MemoryBackend
import app.scheduler as scheduler_module
from app.scheduler import Scheduler
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.interval import IntervalTrigger
from apscheduler.triggers.date import DateTrigger
from datetime import datetime,timedelta,timezone
# DEV constructor runs the actual constructor/init and leaves system jobs stopped.
# We then supply a real isolated APScheduler, not a fake scheduler implementation.
settings.DEV=True
scheduler=Scheduler()
settings.DEV=False
scheduler._scheduler=BackgroundScheduler(timezone='UTC')
scheduler._scheduler.start()
manager=PluginManager();observations=[];calls=[]
class OwnedServices(_PluginBase):
    """合法普通插件样本：方法由真实Manager getter与Scheduler消费。"""
    plugin_name='Owned services'
    def init_plugin(self,config=None):self.config=config or {};self.services=self.config.get('services',[])
    def get_state(self):return self.config.get('enabled',True)
    def get_api(self):return []
    def get_form(self):return [],{}
    def get_page(self):return []
    def get_service(self):
        if self.config.get('getter_error'):raise RuntimeError('owned getter failure')
        return self.services
    def run(self,mode='list-fail',progress=37,gate='',text='owned progress update',progress_callback=None):
        calls.append(mode)
        if progress_callback:progress_callback(value=progress,text=text,data={'owned':9007199254740993,'actual_input':{'mode':mode,'progress':progress,'gate':gate,'text':text}})
        if mode=='tuple-fail':return False,'owned service failure'
        if mode=='tuple-empty':return (False,)
        if mode=='error':raise RuntimeError('owned callback error')
        if mode=='error-empty':raise RuntimeError('')
        if mode=='error-sensitive':raise RuntimeError('owned callback password=OWNED_PASSWORD token=OWNED_TOKEN https://owned.invalid/private')
        return [False,'ordinary list']
    def stop_service(self):pass
plugin=OwnedServices();plugin.init_plugin({'enabled':True});manager._plugins={'OwnedServices':OwnedServices};manager._running_plugins={'OwnedServices':plugin}
def register(sid='manual',mode='list-fail',trigger=None,kwargs=None):
    """以真实插件声明重新登记服务。"""
    plugin.services=[{'id':sid,'name':'Owned '+sid,'func':plugin.run,'trigger':trigger or IntervalTrigger(hours=1,timezone='UTC'),'func_kwargs':{'mode':mode,'progress':37,'gate':'','text':'owned progress update'},'kwargs':kwargs or {}}]
    scheduler.update_plugin_job('OwnedServices')
def snapshot(job='OwnedServices_manual'):
    """记录真实进度模型，显式剔除不可复现时间。"""
    p=scheduler.get_progress(job)
    return p.model_dump(mode='json') if p else None

# Whole source endpoint modules and factory middleware; only unused host/auth/lifespan edges are fixture boundaries.
from contextlib import asynccontextmanager
for mod,name in [('dashboard','DashboardChain'),('storage','StorageChain'),('media','MediaChain'),('search','SearchChain'),('system','SystemChain')]:stub('app.chain.'+mod,'unrelated endpoint business never executed',**{name:Inert})
stub('pillow_avif','unused image codec')
sys.modules['app.helper.image'].ImageHelper=Inert
stub('app.core.metainfo','unrelated media metadata',MetaInfo=Inert)
stub('app.core.module','unrelated module dispatch',ModuleManager=Inert)
@asynccontextmanager
async def owned_lifespan(app):yield
stub('app.startup.lifecycle','source full daemon startup not executed',lifespan=owned_lifespan)
from app.api.endpoints import dashboard,system
from app.factory import create_app
from fastapi import FastAPI
import httpx
wire=create_app()
async def owned_admin():return None
for api in [dashboard,system]:
 for name in ['get_current_active_superuser','get_current_active_superuser_async','get_current_active_user_async','verify_apitoken']:
  if hasattr(api,name):wire.dependency_overrides[getattr(api,name)]=owned_admin
for prefix in ['/api/v1','/api/v2']:
 wire.include_router(dashboard.router,prefix=prefix+'/dashboard')
 wire.include_router(system.router,prefix=prefix+'/system')
wire_observations=[]
async def capture(client,case,path,method="GET"):
 r=await client.request(method,path,headers={'Accept-Language':'zh-CN'})
 wire_observations.append({'case':case,'path':path,'method':method,'allow':r.headers.get('allow'),'location':r.headers.get('location'),'status':r.status_code,'content_type':r.headers.get('content-type'),'body':r.text,'body_sha256':hashlib.sha256(r.content).hexdigest()})
async def execute_wire():
 async with httpx.AsyncClient(transport=httpx.ASGITransport(app=wire,raise_app_exceptions=False),base_url='http://owned-asgi.invalid',trust_env=False) as client:
  for v in ['v1','v2']:
   base='/api/'+v
   await capture(client,'empty_list_'+v,base+'/dashboard/schedule')
   await capture(client,'missing_jobid_'+v,base+'/system/runscheduler')
   await capture(client,'missing_jobid_token_'+v,base+'/system/runscheduler2')
   await capture(client,'empty_jobid_'+v,base+'/system/runscheduler?jobid=')
   await capture(client,'unknown_jobid_'+v,base+'/system/runscheduler?jobid=unknown')
   await capture(client,'unknown_progress_'+v,base+'/dashboard/schedule/unknown/progress')
   await capture(client,'empty_list_token_'+v,base+'/dashboard/schedule2')
   await capture(client,'unknown_progress_token_'+v,base+'/dashboard/schedule2/unknown/progress')
  for mode,kwargs in [('waiting',{}),('single_paused',{'next_run_time':None})]:
   register(kwargs=kwargs)
   for v in ['v1','v2']:
    for suffix in ['schedule','schedule2']:await capture(client,mode+'_list_'+v+'_'+suffix,'/api/'+v+'/dashboard/'+suffix)
  for v in ['v1','v2']:
   for route in ['dashboard/schedule','dashboard/schedule2','dashboard/schedule/unknown/progress','dashboard/schedule2/unknown/progress','system/runscheduler','system/runscheduler2']:
    await capture(client,'post_not_allowed_'+v+'_'+route,'/api/'+v+'/'+route,'POST')
    await capture(client,'trailing_slash_'+v+'_'+route,'/api/'+v+'/'+route+'/')
  plugin.services=[{'id':'paused','name':'Paused','func':plugin.run,'trigger':IntervalTrigger(hours=1,timezone='UTC'),'kwargs':{'next_run_time':None}},{'id':'waiting','name':'Waiting','func':plugin.run,'trigger':IntervalTrigger(hours=1,timezone='UTC')}]
  scheduler.update_plugin_job('OwnedServices')
  for v in ['v1','v2']:
   for suffix in ['schedule','schedule2']:await capture(client,'paused_mixed_'+v+'_'+suffix,'/api/'+v+'/dashboard/'+suffix)
  register(mode='error-empty')
  for v in ['v1','v2']:
   await capture(client,'callback_empty_error_request_'+v,'/api/'+v+'/system/runscheduler?jobid=OwnedServices_manual')
   await capture(client,'callback_empty_error_progress_'+v,'/api/'+v+'/dashboard/schedule/OwnedServices_manual/progress')
   await capture(client,'callback_empty_error_progress_token_'+v,'/api/'+v+'/dashboard/schedule2/OwnedServices_manual/progress')
   await capture(client,'callback_empty_error_list_'+v,'/api/'+v+'/dashboard/schedule')
  p=ProgressHelper(scheduler._get_progress_key('OwnedServices_manual'));p.start();p.update(data={'success':[]})
  for v in ['v1','v2']:
   await capture(client,'bad_progress_model_'+v,'/api/'+v+'/dashboard/schedule/OwnedServices_manual/progress')
   await capture(client,'bad_progress_model_token_'+v,'/api/'+v+'/dashboard/schedule2/OwnedServices_manual/progress')
   await capture(client,'bad_progress_model_list_'+v,'/api/'+v+'/dashboard/schedule')
  # Independent source run05 counterexamples: preserve raw escaped/repeated
  # queries, and use the last repeated jobid rather than the first.
  for v in ['v1','v2']:
   for route in ['dashboard/schedule','dashboard/schedule2','dashboard/schedule/unknown/progress','dashboard/schedule2/unknown/progress','system/runscheduler','system/runscheduler2']:
    await capture(client,'redirect_query_'+v+'_'+route,'/api/'+v+'/'+route+'/?jobid=%E4%BD%A0%2F%2B+%26&jobid=&token=owned%2Btoken&x=1&x=2')
   for route in ['system/runscheduler','system/runscheduler2']:
    await capture(client,'duplicate_empty_last_'+v+'_'+route,'/api/'+v+'/'+route+'?jobid=unknown&jobid=')
    await capture(client,'duplicate_unknown_last_'+v+'_'+route,'/api/'+v+'/'+route+'?jobid=&jobid=unknown')


  # Independently discovered raw-query and repeated-slash cases. The fixed
  # source handlers are unchanged, and HEAD bodies are captured through HTTPX.
  for v in ['v1','v2']:
   for route in ['dashboard/schedule','dashboard/schedule2','dashboard/schedule/unknown/progress','dashboard/schedule2/unknown/progress','system/runscheduler','system/runscheduler2']:
    await capture(client,v+route+'_double_slash','/api/'+v+'/'+route+'//?jobid=unknown')
    await capture(client,v+route+'_head','/api/'+v+'/'+route+'?jobid=unknown','HEAD')
   for route in ['system/runscheduler','system/runscheduler2']:
    for value in ['unknown;','unknown;x=y','%3B','unknown','unknown%zz','unknown%','unknown%2','unknown%FF']:
     await capture(client,v+route+'_query_'+value,'/api/'+v+'/'+route+'?jobid='+value)

def comparable_wire(observations):
    """Keep every field/number/null; normalize only non-null execution times."""
    def normalize(value):
        if isinstance(value, list):
            return [normalize(item) for item in value]
        if isinstance(value, dict):
            out = {}
            for key, item in value.items():
                if key in ('started_at', 'finished_at') and isinstance(item, str):
                    datetime.strptime(item, '%Y-%m-%d %H:%M:%S')
                    out[key] = '<observed-local-time>'
                else:
                    out[key] = normalize(item)
            return out
        return value
    result = []
    for row in observations:
        row = dict(row)
        row.pop('body_sha256', None)  # Original raw bytes/hash remain in report.
        if row['content_type'] == 'application/json' and row['method'] != 'HEAD':
            row['body'] = normalize(json.loads(row['body']))
        result.append(row)
    return result

try:
 asyncio.run(execute_wire())
 from starlette.datastructures import QueryParams
 decoding_oracle=json.loads((HERE/'wire-query-decoding-source-oracle.json').read_text())
 for row in decoding_oracle['cases']:
  decoded=QueryParams(bytes.fromhex(row['raw_hex']))
  assert ('jobid' in decoded)==row['present'] and decoded.get('jobid','')==row['value'], 'actual source raw query decoder differs'
 versions=imported_versions()
 for name in ['fastapi','starlette','httpx']:
  actual=sys.modules[name].__version__
  assert actual==importlib.metadata.version(name), 'executing HTTP dependency differs: '+name
  versions[name]=actual
 oracle=json.loads((HERE/'wire-source-oracle.json').read_text())
 query_oracle=json.loads((HERE/'wire-query-source-oracle.json').read_text())
 boundary_oracle=json.loads((HERE/'wire-boundary-source-oracle.json').read_text())
 assert oracle['source_commit']==query_oracle['source_commit']==boundary_oracle['source_commit']==COMMIT and len(wire_observations)==142
 assert comparable_wire(wire_observations[:66])==comparable_wire(oracle['observations']), 'actual source wire differs from frozen oracle'
 assert comparable_wire(wire_observations[66:86])==comparable_wire(query_oracle['observations']), 'actual source query20 differs from independent frozen oracle'
 assert comparable_wire(wire_observations[86:])==comparable_wire(boundary_oracle['observations']), 'actual source boundary56 differs from independent frozen oracle'
 result={'source_commit':COMMIT,'kind':'normally imported complete dashboard/system routers + actual factory V2ResponseMiddleware + actual Scheduler/PluginManager/ProgressHelper/MemoryBackend; same-input owned callback data; auth overridden, no daemon lifespan/network', 'versions':versions,'observations':wire_observations,'provenance':provenance(args),'fixtures':fixtures,'network_attempts':blocked,'frozen_oracle_equal':True}
 (OUT/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'observations':len(wire_observations),'statuses':[x['status'] for x in wire_observations],'network':blocked}))
finally:scheduler.stop();Engine.dispose();owned_runtime.cleanup()
