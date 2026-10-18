"""Execute original installed methods with synthetic side-effect dependencies only."""
import ast, copy, datetime, glob, hashlib, json, os, pathlib, re, shutil, sys, tempfile, time, types
from typing import Any,List,Dict,Tuple,Optional
from zoneinfo import ZoneInfo
input_dir=pathlib.Path(__file__).parent
R=pathlib.Path(os.environ.get('AUTOBACKUP_ORACLE_OUTPUT',str(input_dir)))
if R != input_dir:
 R.mkdir(parents=True,exist_ok=True)
 shutil.copyfile(input_dir/'installed-plugin.py',R/'installed-plugin.py')
SOURCE=R/'installed-plugin.py'
raw=SOURCE.read_bytes();tree=ast.parse(raw)
klass=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='AutoBackup')
code=compile(ast.fix_missing_locations(ast.Module(body=[klass],type_ignores=[])),str(SOURCE),'exec')
BASE=datetime.datetime(2026,10,18,9,2,3,tzinfo=ZoneInfo('Asia/Shanghai'))
class Clock(datetime.datetime):
 @classmethod
 def now(cls,tz=None):return BASE.astimezone(tz) if tz else BASE.replace(tzinfo=None)
class Log:
 def __getattr__(self,k):
  def record(*args,**kwargs):
   message=args[0] % args[1:] if len(args)>1 else args[0]
   active.logs.append([k,str(message)])
  return record
class Response:
 def __init__(self,**kw):self.value=dict(success=kw['success'],message=kw.get('message'),message_i18n=kw.get('message'),data={})
 def dump(self):return self.value
class Path(pathlib.PurePosixPath):
 files={};trace=[];faults={}
 def is_file(self):return self.files.get(str(self),{}).get('kind','file')=='file'
 def is_dir(self):return self.files.get(str(self),{}).get('kind')=='dir'
 def unlink(self):
  self.trace.append(['unlink',str(self)])
  if str(self) in self.faults:raise RuntimeError(self.faults[str(self)])
class Base:
 def get_data_path(self):return Path('/owned/plugin')
 def update_config(self,c):
  self.trace.append(['config',copy.deepcopy(c)])
  if self.fault=='config':raise RuntimeError('config failed')
 def post_message(self,**kw):
  self.trace.append(['notice',kw])
  if self.fault=='notice':raise RuntimeError('notice failed')
class Scheduler:
 def __init__(self,**kw):self.running=False;self.jobs=[];active.trace.append(['scheduler_new',kw])
 def add_job(self,**kw):
  self.jobs.append(kw);active.trace.append(['scheduler_add',dict(trigger=kw['trigger'],run_date=kw['run_date'].isoformat(),name=kw['name'])])
 def get_jobs(self):return self.jobs
 def print_jobs(self):active.trace.append(['scheduler_print'])
 def start(self):self.running=True;active.trace.append(['scheduler_start'])
 def remove_all_jobs(self):self.jobs=[];active.trace.append(['scheduler_clear'])
 def shutdown(self):self.running=False;active.trace.append(['scheduler_shutdown'])
class Cron:
 @staticmethod
 def from_crontab(v):
  if not isinstance(v,str):raise AttributeError('split')
  parts=v.split()
  if len(parts)!=5:raise ValueError('cron')
  return {'cron':v,'fields':['*',parts[3],parts[2],'*',parts[4],parts[1],parts[0],'0']}
class DAV:
 def __init__(self,options):
  active.trace.append(['dav_init',options]);self.cfg=active.dav
  if self.cfg.get('init_error'):raise RuntimeError(self.cfg['init_error'])
 def list(self,p):
  active.trace.append(['dav_list',p]);self.cfg['lists']=self.cfg.get('lists',0)+1
  if self.cfg.get('list_error'):raise RuntimeError(self.cfg['list_error'])
  return self.cfg.get('files',[])
 def upload_sync(self,remote_path,local_path,callback):
  active.trace.append(['dav_upload',remote_path,local_path])
  if self.cfg.get('upload_error'):raise RuntimeError(self.cfg['upload_error'])
  callback()
 def check(self,p):
  active.trace.append(['dav_check',p])
  if self.cfg.get('check_error'):raise RuntimeError(self.cfg['check_error'])
  return self.cfg.get('check',True)
 def clean(self,p):
  active.trace.append(['dav_delete',p])
  if p in self.cfg.get('delete_errors',[]):raise RuntimeError('delete failed')
web=types.ModuleType('webdav3');webclient=types.ModuleType('webdav3.client');webclient.Client=DAV
sys.modules['webdav3']=web;sys.modules['webdav3.client']=webclient
settings=types.SimpleNamespace(TZ='Asia/Shanghai',API_TOKEN='owned-api')
ns=dict(__name__='offline_original_autobackup',glob=types.SimpleNamespace(glob=lambda pattern: list(Path.files)),os=types.SimpleNamespace(path=types.SimpleNamespace(getctime=lambda p:Path.files[p]['ctime'],basename=os.path.basename)),re=re,shutil=types.SimpleNamespace(rmtree=lambda p:Path.trace.append(['rmtree',str(p)])),time=types.SimpleNamespace(strftime=lambda fmt,*args:BASE.strftime(fmt),localtime=lambda _:None,time=lambda:BASE.timestamp()),datetime=Clock,timedelta=datetime.timedelta,Path=Path,Any=Any,List=List,Dict=Dict,Tuple=Tuple,Optional=Optional,pytz=types.SimpleNamespace(timezone=ZoneInfo),BackgroundScheduler=Scheduler,CronTrigger=Cron,Session=object,db_query=lambda f:f,settings=settings,SystemHelper=object,SystemUtils=object,logger=Log(),_PluginBase=Base,schemas=types.SimpleNamespace(Response=Response),NotificationType=types.SimpleNamespace(SiteMessage='SiteMessage'),urljoin=__import__('urllib.parse',fromlist=['urljoin']).urljoin)
exec(code,ns);Plugin=ns['AutoBackup']
keys=['enabled','cron','cnt','notify','onlyonce','back_path','webdav_enabled','webdav_hostname','webdav_login','webdav_password','webdav_digest_auth','webdav_max_count','webdav_notify','webdav_disable_check']
configs=[None,{},dict(enabled=False),dict(enabled=True,cron='0 3 * * *',onlyonce=True,notify=True,cnt='3'),dict(onlyonce=True,back_path='/owned/backup',webdav_enabled=True,webdav_hostname='https://dav.invalid/path',webdav_login='owned',webdav_password='synthetic',webdav_digest_auth=True,webdav_disable_check=True)]
cases=[];active=None
for i,cfg in enumerate(configs):
 for repeat in (False,True):
  p=Plugin();p.trace=[];p.logs=[];p.fault='';p.dav={};active=p;Path.trace=p.trace;Path.files={};Path.faults={}
  err=None
  try:
   p.init_plugin(copy.deepcopy(cfg))
   if repeat:p.init_plugin({})
   state=p.get_state();services=p.get_service()
  except Exception as e:err=type(e).__name__;state=None;services=None
  cases.append(dict(kind='init',input=cfg,repeat=repeat,trace=p.trace,logs=p.logs,state=state,service=services,error=err))
for cfg,fault in [(configs[3],'config'),(dict(enabled=True,cron='bad'),'')]:
 p=Plugin();p.trace=[];p.logs=[];p.fault=fault;p.dav={};active=p;err=None
 try:p.init_plugin(copy.deepcopy(cfg));p.get_service()
 except Exception as e:err=type(e).__name__
 cases.append(dict(kind='init',input=cfg,repeat=False,fault=fault,trace=p.trace,logs=p.logs,error=err))
base=dict(back_path='/owned/backup',notify=True)
variants=[{},dict(cnt=2),dict(cnt=10),dict(cnt=0),dict(cnt='0'),dict(cnt=-2),dict(cnt='bad'),dict(cnt=True),dict(cnt='２'),dict(cnt=2.9),dict(cnt=1e100),dict(cnt=-1e100),dict(cnt=1e-100),dict(notify=False)]
for n,v in enumerate(variants):
 for success in (True,False):
  p=Plugin();p.trace=[];p.logs=[];p.fault='';p.dav={};active=p;Path.trace=p.trace;Path.files={f'/owned/backup/bk_{i}':dict(kind='dir' if i==1 else 'file',ctime=i) for i in (3,1,2)};Path.faults={};cfg=base|v
  p.init_plugin(copy.deepcopy(cfg));p.trace.clear()
  def backup_file(bk_path):p.trace.append(['backup_file',str(bk_path)]);return '/owned/backup/bk_20261018090203.zip' if success else None
  p.backup_file=backup_file;err=None;result=None
  try:result=p._AutoBackup__backup()
  except Exception as e:err=type(e).__name__
  cases.append(dict(kind='backup',input=cfg,files=Path.files,zip_success=success,dav={},trace=p.trace,logs=p.logs,result=result,error=err))
dav_cases=[{},dict(init_error='init failed'),dict(list_error='list failed'),dict(upload_error='put failed'),dict(check=False),dict(check_error='head failed'),dict(files=['bk_20260101000000.zip','bk_20260201000000.zip','bk_20260301000000.zip','other']),dict(files=['bk_20260101000000.zip','bk_20260201000000.zip'],delete_errors=['/bk_20260101000000.zip']),dict(files=['bk_20260101000000.zip.foo','bk_20260201000000.zip']),dict(files=['bk_99999999999999.zip']),dict(files=['bk_２０２６０１０１００００００.zip']),dict(files=['bk_２０２６0101000000.zip','bk_20260201000000.zip']),dict(files=['bk_202601010１0000.zip','bk_20260201000000.zip']),dict(files=['bk_2026010100000１.zip','bk_20260201000000.zip'])]
for dv in dav_cases:
 for count in (1,'0',-2):
  p=Plugin();p.trace=[];p.logs=[];p.fault='';p.dav=copy.deepcopy(dv);active=p;Path.trace=p.trace;Path.files={};Path.faults={};cfg=base|dict(webdav_enabled=True,webdav_hostname='https://dav.invalid/root',webdav_login='owned',webdav_password='synthetic',webdav_max_count=count,webdav_notify=True)
  p.init_plugin(copy.deepcopy(cfg));p.trace.clear()
  def backup_file(bk_path):p.trace.append(['backup_file',str(bk_path)]);return '/owned/backup/bk_20261018090203.zip'
  p.backup_file=backup_file;err=None;result=None
  try:result=p._AutoBackup__backup()
  except Exception as e:err=type(e).__name__
  cases.append(dict(kind='backup',input=cfg,files={},zip_success=True,dav=dv,trace=p.trace,logs=p.logs,result=result,error=err))
# Form has a dynamic default data path; execute entire original method.
p=Plugin();p.trace=[];p.logs=[];active=p
form,defaults=p.get_form();(R/'form.json').write_text(json.dumps(dict(schema=form,defaults=defaults),ensure_ascii=False,indent=2)+'\n')
metadata={k:getattr(p,k) for k in ['plugin_name','plugin_desc','plugin_icon','plugin_version','plugin_author','author_url','plugin_config_prefix','plugin_order','auth_level']}
api=p.get_api();api[0]['endpoint']=api[0]['endpoint'].__name__
(R/'metadata.json').write_text(json.dumps(dict(metadata=metadata,api=api,commands=p.get_command(),page=p.get_page()),ensure_ascii=False,indent=2)+'\n')
(R/'business-oracle.json').write_text(json.dumps(dict(source_sha256=hashlib.sha256(raw).hexdigest(),runtime=sys.version,cases=cases),ensure_ascii=False,indent=2,default=lambda v:v.__name__ if callable(v) else str(v))+'\n')
print(json.dumps(dict(source_sha256=hashlib.sha256(raw).hexdigest(),cases=len(cases),form_nodes=sum(isinstance(n,dict) for n in ast.walk(klass))),ensure_ascii=False))
