import ast,bisect,builtins,copy,datetime,json,pathlib,re,sys,types,urllib.parse
from typing import Any,List,Dict,Tuple,Optional,Union
from apscheduler.triggers.cron import CronTrigger as ActualCron
from qbittorrentapi import TorrentDictionary
from transmission_rpc.torrent import Torrent
payload=json.load(sys.stdin)
trace=[];spec={};files={};jobs=[]
class Quiet:
 def __getattr__(self,n):return lambda *a,**kw:None
class Thread:
 def __init__(self,target):self.target=target
 def start(self):self.target()
 def join(self,timeout):pass
 def is_alive(self):return False
class Obj:
 def __init__(self,attrs=None,faults=None,text=None,mapping=False):self.attrs=attrs or {};self.faults=faults or {};self.text=text;self.mapping=mapping
 def __getattr__(self,k):
  if k in self.faults:raise RuntimeError('synthetic attribute failure')
  if k=='get' and self.mapping:return lambda name,default=None:self.attrs.get(name,default)
  if k in self.attrs:return self.attrs[k]
  raise AttributeError(k)
 def __str__(self):return self.text if self.text is not None else '<synthetic-object>'
 def __repr__(self):return str(self)
def convert(v):
 if isinstance(v,list):return [convert(x) for x in v]
 if isinstance(v,dict):
  if '$object' in v:return Obj({k:convert(x) for k,x in v['$object'].items()},v.get('$faults'),v.get('$text'),v.get('$mapping',False))
  return {k:convert(x) for k,x in v.items()}
 return v
class Path:
 def __init__(self,p):self.p=str(pathlib.PurePosixPath(p))
 def __str__(self):return self.p
 @property
 def name(self):return pathlib.PurePosixPath(self.p).name
 def exists(self):return self.p in files
 def is_file(self):return files.get(self.p,{}).get('kind')=='file'
 def is_dir(self):return files.get(self.p,{}).get('kind')=='dir'
 def stat(self):
  f=files[self.p]
  if f.get('stat_error'):raise OSError('synthetic stat failure')
  return types.SimpleNamespace(st_size=f.get('size',0),st_mtime=f.get('mtime',0))
 def iterdir(self):
  if files.get(self.p,{}).get('list_error'):raise OSError('synthetic list failure')
  return iter([Path(p) for p in files if p!=self.p and str(pathlib.PurePosixPath(p).parent)==self.p])
 def rglob(self,p):return iter([Path(k) for k in files if k.startswith(self.p.rstrip('/')+'/')])
 def unlink(self):
  trace.append({'op':'unlink','path':self.p})
  if files[self.p].get('delete_error'):raise OSError('synthetic unlink failure')
  del files[self.p]
class Shutil:
 @staticmethod
 def rmtree(p):
  p=str(p);trace.append({'op':'rmtree','path':p})
  if files[p].get('delete_error'):raise OSError('synthetic rmtree failure')
  for k in list(files):
   if k==p or k.startswith(p.rstrip('/')+'/'):del files[k]
class Clock:
 @staticmethod
 def now(tz=None):
  value=datetime.datetime.fromtimestamp(spec.get('now',2000000000),datetime.timezone.utc)
  return value
class Scheduler:
 def __init__(self,timezone):self.timezone=timezone;self.running=False;self.jobs=[];jobs.append(self)
 def add_job(self,func,trigger,run_date,name):self.jobs.append({'trigger':trigger,'run_at':run_date.timestamp(),'name':name})
 def get_jobs(self):return self.jobs
 def print_jobs(self):pass
 def start(self):self.running=True
 def remove_all_jobs(self):self.jobs=[]
 def shutdown(self):self.running=False
class Service:
 def __init__(self,s):self.s=s;self.name=s['name'];self.instance=Downloader(s)
class Downloader:
 def __init__(self,s):self.s=s
 def __bool__(self):return not self.s.get('false_instance',False)
 def is_inactive(self):return not self.s.get('active',True)
 def get_torrents(self):
  trace.append({'op':'list','service':self.s['name']})
  if self.s.get('list_throw'):raise RuntimeError('synthetic list failure')
  if self.s.get('native'):
   if self.s['kind']=='qbittorrent':return [TorrentDictionary(data=t,client=NativeQB(self.s)) for t in self.s['native_torrents']],False
   return [Torrent(fields=t) for t in self.s['native_torrents']],False
  return [convert(t) for t in self.s.get('torrents',[])],self.s.get('list_error',False)
 def delete_torrents(self,delete,ids):
  trace.append({'op':'delete_torrent','service':self.s['name'],'hash':ids,'delete_files':delete})
  if self.s.get('delete_throw'):raise RuntimeError('synthetic delete failure')
  return self.s.get('delete_return',True)
 def set_torrents_tag(self,ids,tags):return self._tags('qb_tags',ids,tags)
 def set_torrent_tag(self,ids,tags):return self._tags('tr_tags',ids,tags)
 def _tags(self,op,ids,tags):
  trace.append({'op':op,'service':self.s['name'],'hash':ids,'tags':tags})
  if self.s.get('tag_throw'):raise RuntimeError('synthetic label failure')
  return self.s.get('tag_return',True)
class NativeQB:
 def __init__(self,s):self.s=s
 def torrents_trackers(self,torrent_hash):
  trace.append({'op':'trackers','service':self.s['name'],'hash':torrent_hash})
  return copy.deepcopy(self.s.get('tracker_by_hash',{}).get(torrent_hash,[]))
class Helper:
 def get_configs(self):return {s['name']:types.SimpleNamespace(name=s['name']) for s in spec.get('services',[])}
 def get_services(self,name_filters):return {s['name']:Service(s) for s in spec.get('services',[]) if s['name'] in name_filters}
 def is_downloader(self,service_type,service):return service.s.get('kind')==service_type
class Base:
 def post_message(self,**kw):trace.append({'op':'notice',**kw})
 def update_config(self,c):trace.append({'op':'config','config':c})
class Cron:
 @staticmethod
 def from_crontab(expr):
  t=ActualCron.from_crontab(expr,timezone='UTC')
  return {'cron':expr,'fields':[str(f) for f in t.fields]}
class U:pass
uenv={'re':re,'bisect':bisect,'parse':urllib.parse,'Union':Union,'Tuple':Tuple}
for name,src in payload['utils']['methods'].items():
 n=ast.parse(src).body[0];n.decorator_list=[];exec(compile(ast.Module([n],[]),'<installed-stringutils>','exec'),uenv);setattr(U,name,staticmethod(uenv[name]))
imp=builtins.__import__
def controlled_import(name,*a,**kw):
 if name=='threading':return types.SimpleNamespace(Thread=Thread)
 return imp(name,*a,**kw)
env={'_PluginBase':Base,'Any':Any,'List':List,'Dict':Dict,'Tuple':Tuple,'Optional':Optional,'ServiceInfo':object,'BackgroundScheduler':Scheduler,'DownloaderHelper':Helper,'settings':types.SimpleNamespace(TZ='UTC'),'CronTrigger':Cron,'logger':Quiet(),'StringUtils':U,'Event':object,'EventType':types.SimpleNamespace(PluginAction='PluginAction'),'NotificationType':types.SimpleNamespace(SiteMessage='SiteMessage'),'datetime':Clock,'timedelta':datetime.timedelta,'pytz':types.SimpleNamespace(timezone=lambda x:datetime.timezone.utc),'Path':Path,'shutil':Shutil,'__builtins__':{**vars(builtins),'__import__':controlled_import}}
exec(compile(payload['class_source'],'<installed-CleanInvalidSeed-AST>','exec'),env);Plugin=env['CleanInvalidSeed']
fields=['enabled','cron','notify','onlyonce','detect_invalid_files','delete_invalid_files','delete_invalid_torrents','notify_all','label_only','label','download_dirs','exclude_keywords','exclude_categories','exclude_labels','more_logs','min_seeding_days','custom_error_msg','downloaders']
def norm(v):
 if callable(v):return v.__name__
 if isinstance(v,dict):return {k:norm(x) for k,x in v.items()}
 if isinstance(v,list):return [norm(x) for x in v]
 return v
def state(p):return {k:getattr(p,'_'+k) for k in fields if hasattr(p,'_'+k)}
results=[]
for spec in payload['cases']:
 trace=[];jobs=[];files={f['path']:dict(f) for f in spec.get('files',[])};p=Plugin();steps=[]
 for step in spec.get('steps',[{'action':'init','config':spec.get('config')},{'action':'clean'}]):
  try:
   a=step['action'];r=None
   if a=='init':r=p.init_plugin(step.get('config'))
   elif a=='clean':r=p.clean_invalid_seed()
   elif a=='files':r=p.detect_invalid_files()
   elif a=='command':r=p.handle_commands(types.SimpleNamespace(event_data=step.get('event')))
   elif a=='service':r=p.get_service()
   elif a=='state':r=p.get_state()
   elif a=='stop':r=p.stop_service()
   elif a=='helper':r=getattr(p,step['method'])(*[convert(v) for v in step.get('args',[])])
   steps.append({'result':norm(r),'error':None})
  except Exception as e:steps.append({'result':None,'error':type(e).__name__})
 expected={'steps':steps,'trace':trace,'state':state(p),'jobs':[{'running':s.running,'jobs':s.jobs} for s in jobs],'files':list(files)}
 results.append({'id':spec['id'],'input':spec,'expected':expected})
spec={'services':[{'name':'Synthetic qB','kind':'qbittorrent'},{'name':'Synthetic TR','kind':'transmission'}]};p=Plugin();p.downloader_helper=Helper();f,m=p.get_form()
helpers=[]
for value in [0,1,1023,1024,1025,1048575,1048576,2**40,2**53+1,None,'1 KiB','2.5GB','-1','abc','1..2','00123','B B B','iB iB iB','ib','1kb','１２３','²','١٢.٥','0.0000001','1.1.1','  1  ',True,False]:
 try:r=U.str_filesize(value);err=None
 except Exception as e:r=None;err=type(e).__name__
 helpers.append({'method':'str_filesize','input':value,'result':r,'error':err})
for value in ['',None,'udp://tracker.synthetic:80/a','http://Tracker.Synthetic:80/a?x=y','https://u:p@tracker.synthetic/a','httpx:abc','https://[::1]:80/a','http://bad[bracket','HTTPS://UPPER/a','http://tracker.synthetic/%zz','http://tracker.synthetic:bad/a','http://[v1.xyz]/a','http://[vQ.x]/','http://prefix[::1]/','http://[::1]suffix/','http://u[]@host/a','http://[127.0.0.1]/a','http://[fe80::1%zone]/a','http://[fe80::1%]/a','http://[fe80::1%a%b]/a','http://track℀er.synthetic/a','http://tracker.synthetic＠evil/a','http://tracker.synthetic/a\n','htt\tp://tracker.synthetic/a']:
 try:r=list(U.get_url_netloc(value));err=None
 except Exception as e:r=None;err=type(e).__name__
 helpers.append({'method':'get_url_netloc','input':value,'result':r,'error':err})
json.dump({'python':sys.version,'cases':results,'form':{'conf':f,'model':m},'commands':p.get_command(),'api':p.get_api(),'page':p.get_page(),'helpers':helpers},sys.stdout,ensure_ascii=False)
