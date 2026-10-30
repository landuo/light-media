"""完整固定 Scheduler/Manager/ProgressHelper/MemoryBackend 组件回放，所有输入为独占合成数据。"""
import asyncio, hashlib, importlib.metadata, json, os, socket, sys, threading, time, types
from pathlib import Path
import tempfile
import sys
sys.dont_write_bytecode = True
from replay_support import HERE, prepare, prohibit_network, provenance, normalized_manual, imported_versions
args = prepare(source=True)
SRC = args.source_root
OUT = args.output
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
    def run(self,mode='list-fail',progress=37,progress_callback=None):
        calls.append(mode)
        if progress_callback:progress_callback(value=progress,text='owned progress',data={'owned':9007199254740993})
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
    plugin.services=[{'id':sid,'name':'Owned '+sid,'func':plugin.run,'trigger':trigger or IntervalTrigger(hours=1,timezone='UTC'),'func_kwargs':{'mode':mode,'progress':37},'kwargs':kwargs or {}}]
    scheduler.update_plugin_job('OwnedServices')
def snapshot(job='OwnedServices_manual'):
    """记录真实进度模型，显式剔除不可复现时间。"""
    p=scheduler.get_progress(job)
    return p.model_dump(mode='json') if p else None
try:
    for mode in ['tuple-fail','tuple-empty','list-fail','error','error-empty','error-sensitive']:
        register(mode=mode);scheduler.start('OwnedServices_manual');observations.append({'case':'manual_'+mode,'progress':snapshot(),'calls':list(calls),'last_notice':notices[-1] if mode.startswith('error') else None})
    before=len(calls);scheduler.start('missing');observations.append({'case':'missing_noop','unchanged':len(calls)==before})
    register(sid='clock',mode='list-fail',trigger=DateTrigger(run_date=datetime.now(timezone.utc)+timedelta(seconds=.2)))
    deadline=time.monotonic()+3
    while len(calls)==before and time.monotonic()<deadline:time.sleep(.02)
    assert len(calls)==before+1
    observations.append({'case':'real_date_clock','progress':snapshot('OwnedServices_clock'),'listed_ids':[x.id for x in scheduler.list()]})
    register();plugin.config['enabled']=False;scheduler.update_plugin_job('OwnedServices');observations.append({'case':'disabled_removes','jobs':list(scheduler._jobs),'clock_jobs':[x.id for x in scheduler._scheduler.get_jobs()]});plugin.config['enabled']=True
    register();plugin.config['getter_error']=True;scheduler.update_plugin_job('OwnedServices');observations.append({'case':'getter_failure_removes','jobs':list(scheduler._jobs)});plugin.config.pop('getter_error')
    plugin.services=[{'id':'suffix|first','name':'First','func':plugin.run,'trigger':IntervalTrigger(hours=1,timezone='UTC'),'func_kwargs':{'mode':'tuple-fail'}},{'id':'suffix|last','name':'Last','func':plugin.run,'trigger':'owned-invalid-trigger','func_kwargs':{'mode':'list-fail'}}]
    scheduler.update_plugin_job('OwnedServices');scheduler.start('OwnedServices_suffix')
    observations.append({'case':'last_invalid_trigger_retains_manual_metadata','progress':snapshot('OwnedServices_suffix'),'clock_jobs':[x.id for x in scheduler._scheduler.get_jobs()]})
    plugin.services=[{'id':'paused','name':'Paused','func':plugin.run,'trigger':IntervalTrigger(hours=1,timezone='UTC'),'kwargs':{'next_run_time':None}},{'id':'waiting','name':'Waiting','func':plugin.run,'trigger':IntervalTrigger(hours=1,timezone='UTC')}]
    scheduler.update_plugin_job('OwnedServices');paused_error=None
    try:scheduler.list()
    except Exception as e:paused_error=type(e).__name__
    observations.append({'case':'paused_mixed_source_list_error','error':paused_error})
    from app.schemas.dashboard import ScheduleProgress
    normal={'id':'owned','name':'Owned','provider':'Provider','enable':True,'value':37,'text':'Owned progress','status':'running','success':None,'started_at':None,'finished_at':None,'error':None,'data':{}}
    schema_cases=[]
    for label,patch in [('normal',{}),('success-string',{'success':'false'}),('success-numeric',{'success':1}),('name-invalid',{'name':1}),('success-invalid',{'success':[]})]:
        output=None;error=None
        try:output=ScheduleProgress.model_validate({**normal,**patch}).model_dump()
        except Exception as e:error=type(e).__name__
        schema_cases.append({'name':label,'input':{**normal,**patch},'output':output,'error':error})
    observations.append({'case':'normal_import_progress_schema','cases':schema_cases})
    # Both old and newly declared functions stay real awaitables. Source finish
    # resolves by logical ID, so old completion intentionally affects new job.
    async def async_cases():
        old_gate=asyncio.Event();new_gate=asyncio.Event();entered=[]
        async def owned_async(label,progress_callback=None):
            entered.append(label)
            progress_callback(value=37 if label=='old' else 51,text=label)
            await (old_gate if label=='old' else new_gate).wait()
            return (False,'old failure') if label=='old' else [False,'ordinary list']
        def declare(label):
            plugin.services=[{'id':'async','name':label,'func':owned_async,'trigger':IntervalTrigger(hours=1,timezone='UTC'),'func_kwargs':{'label':label}}];scheduler.update_plugin_job('OwnedServices')
        declare('old');scheduler.start('OwnedServices_async');await asyncio.sleep(.02);assert entered==['old']
        declare('new');scheduler.start('OwnedServices_async');await asyncio.sleep(.02);assert entered==['old','new']
        old_gate.set();await asyncio.sleep(.02);after_old=snapshot('OwnedServices_async')
        new_gate.set();await asyncio.sleep(.02);after_new=snapshot('OwnedServices_async')
        observations.append({'case':'async_replaced_same_id','entered':entered,'after_old':after_old,'after_new':after_new})
    asyncio.run(async_cases())
    # Real MemoryBackend/cachetools region. Only this private region clock is
    # controlled; no helper/cache class or algorithm is replaced.
    scheduler.remove_plugin_job('OwnedServices')
    probe=ProgressHelper('seed');probe.start()
    region=MemoryBackend._region_caches['region:progress'];region.clear();clock=[0.0]
    region.timer._Timer__timer=lambda:clock[0]
    for i in range(1024):clock[0]=float(i);ProgressHelper(str(i)).start()
    ProgressHelper('0').get();ProgressHelper('1024').start()
    lru={'read_key_retained':ProgressHelper('0').get() is not None,'least_recent_key_retained':ProgressHelper('1').get() is not None,'inserted_key_retained':ProgressHelper('1024').get() is not None}
    clock[0]=100000.;ProgressHelper('expiry').start();clock[0]+=86400.;at_expiry=ProgressHelper('expiry').get();ProgressHelper('expiry').update(value=51,text='must not revive');after_update=ProgressHelper('expiry').get();ProgressHelper('expiry').end(value=100,text='must not revive');after_end=ProgressHelper('expiry').get()
    observations.append({'case':'actual_full_progress_memory_tlru','lru':lru,'expiry':{'at_expiry':at_expiry,'after_update':after_update,'after_end':after_end}})
finally:scheduler.stop();manager._running_plugins={};manager._plugins={};global_vars.stop_system()
bindings = provenance(args)
result={'source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'whole normal-import components, owned plugin object, actual APS clock and default MemoryBackend; not full app startup/auth/browser','versions':imported_versions(),'fixtures':fixtures,'observations':observations,'notices_count':len(notices),'network_attempts':blocked,'provenance':bindings}
expected = json.loads((HERE / 'whole-source-progress.json').read_text())
result['frozen_manual_equal'] = normalized_manual(observations) == {c['case']: c['raw_source_progress'] for c in expected['cases']}
(OUT/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
os.chdir(OUT)
owned_runtime.cleanup()
print(json.dumps({'cases':len(observations),'manual_cases_equal':result['frozen_manual_equal'],'versions':result['versions'],'network_attempts':blocked,'imports':len(bindings['imports'])}))
assert result['frozen_manual_equal'] and not blocked
