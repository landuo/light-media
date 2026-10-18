"""Whole installed class AST + synthetic IO, local reference-only Python.
No application imports/config/database/network or real downloader mutation.
"""
from __future__ import annotations
import ast,base64,copy,datetime,hashlib,io,json,os,pathlib,sys,types,contextlib,zipfile
from threading import Event
root=pathlib.Path(__file__).resolve().parent
runtime=pathlib.Path(os.environ.get('TRANSFER_ORACLE_RUNTIME',str(root/'oracle-runtime')))
sys.path.insert(0,str(runtime))
from bencode import bdecode,bencode
source=root/'torrenttransfer.py'
raw=source.read_bytes();assert hashlib.sha256(raw).hexdigest()=='e5f513a57cf42ee8778744ba30cf669f93a5a3e6c7589ca0b2e3b9c483651bd5'
tree=ast.parse(raw);tree.body=[x for x in tree.body if isinstance(x,ast.ClassDef)]
QHASH='1'*40;AHASH='2'*40
class Attr(dict):
 def __getattr__(self,k):return self[k]
def tr_obj(v):
 if isinstance(v,dict):return Attr({k:tr_obj(x) for k,x in v.items()})
 if isinstance(v,list):return [tr_obj(x) for x in v]
 return v
def qb_torrent(**kw):return {'hash':QHASH,'save_path':'/source/电影','tags':'已整理, 自有','category':'电影','state':'pausedUP','progress':1,'amount_left':0,**kw}
def tr_torrent(**kw):return {'hashString':QHASH,'download_dir':'/source/电影','labels':['已整理','自有'],'status':{'stopped':True},'percent_done':1,**kw}
class Env:
 def __init__(self,config,scenario):
  self.config=copy.deepcopy(config);self.scenario=scenario;self.calls=[];self.logs=[];self.history={};self.writes=[];self.messages=[];self.prints=[];self.fs={};self.added=False
  content=scenario.get('torrent_b64')
  if not scenario.get('missing_file'):self.fs['/backup/'+QHASH+'.torrent']=base64.b64decode(content) if content is not None else bencode({'announce':'https://tracker.invalid/announce','info':{'name':'owned synthetic','length':1}})
  if 'resume_b64'in scenario:self.fs['/backup/'+QHASH+'.fastresume']=base64.b64decode(scenario['resume_b64'])
  self.fromtype=scenario.get('fromtype','qbittorrent');self.totype=scenario.get('totype','qbittorrent')
  self.fromtor=scenario.get('torrent',qb_torrent() if self.fromtype=='qbittorrent'else tr_torrent())
 def call(self,op,**kw):self.calls.append({'op':op,**kw})
 def run(self):
  env=self
  class FakePath:
   def __init__(self,p):self.p=str(p)
   def __truediv__(self,s):return FakePath(os.path.join(self.p,s))
   def __str__(self):return self.p
   def exists(self):env.call('exists',path=self.p);return (self.p=='/backup'and not env.scenario.get('missing_root'))or self.p in env.fs
   def read_bytes(self):env.call('read',path=self.p);return env.fs[self.p]
   def write_bytes(self,b):env.call('temp',name=os.path.basename(self.p),content=base64.b64encode(b).decode());env.fs[self.p]=b;return len(b)
  class Logger:
   def __getattr__(self,k):
    def emit(msg):env.logs.append({'line':sys._getframe(1).f_lineno,'level':{'info':'INFO','warning':'WARN','warn':'WARN','error':'ERROR'}[k],'message':str(msg)})
    return emit
  class Base:
   def update_config(self,config):env.call('config',value=copy.deepcopy(config));env.writes.append(copy.deepcopy(config))
   def save_data(self,key,value):env.call('save',key=key,value=copy.deepcopy(value));env.history[key]=copy.deepcopy(value)
   def post_message(self,**kw):env.call('notice',value=kw);env.messages.append(kw)
  class Message:
   def put(self,text,title):env.call('system',title=title,text=text)
  class Downloader:
   def __init__(self,name,kind):self.name=name;self.kind=kind
   def __str__(self):return self.name
   def is_inactive(self):return env.scenario.get('inactive')==self.name
   def get_completed_torrents(self):env.call('completed',name=self.name);return []if env.scenario.get('no_torrents')else [tr_obj(copy.deepcopy(env.fromtor))]
   def get_torrents(self,ids):
    env.call('torrents',name=self.name,ids=ids)
    if not env.added and env.scenario.get('duplicate'):return [tr_obj(copy.deepcopy(env.fromtor))],False
    if not env.added:return [],False
    mode=env.scenario.get('recheck','ready')
    if mode=='none':return None,True
    if mode=='empty':return [],False
    t=qb_torrent(hash=AHASH,state='pausedUP'if mode=='ready'else'checkingUP')if self.kind=='qbittorrent'else tr_torrent(hashString=AHASH,status={'stopped':mode=='ready'})
    return [tr_obj(t)],False
   def add_torrent(self,**kw):
    spec={'content':base64.b64encode(kw['content']).decode(),'save_path':kw['download_dir'],'tags':kw.get('tag',kw.get('labels',[])),'category':kw.get('category'),'skip_checking':kw.get('is_skip_checking',False)}
    if env.config.get('remainoldtag'):spec['tags']=sorted(spec['tags'])
    env.call('add',name=self.name,spec=spec);env.added=not env.scenario.get('add_failure')
    if self.kind=='qbittorrent':return (env.added,[])
    return Attr(hashString=AHASH)if env.added else None
   def get_torrent_id_by_tag(self,tags):env.call('find',name=self.name,tag=tags);return AHASH if env.added and not env.scenario.get('find_missing')else None
   def recheck_torrents(self,ids):env.call('recheck',name=self.name,ids=ids);return not env.scenario.get('control_false')
   def delete_torrents(self,delete_file,ids):assert delete_file is False;env.call('delete',name=self.name,ids=ids);return not env.scenario.get('control_false')
   def start_torrents(self,ids):env.call('start',name=self.name,ids=ids);return not env.scenario.get('control_false')
  services={n:Attr(name=n,type=t,instance=Downloader(n,t))for n,t in [('source',self.fromtype),('destination',self.totype)]}
  class Helper:
   def get_service(self,name):env.call('service',name=name);return None if env.scenario.get('missing_service')==name else services.get(name)
   def is_downloader(self,name,service):return name==service.type
   def get_configs(self):return {n:Attr(name=n)for n in services}
  class Random:
   @staticmethod
   def generate_random_str(n):env.call('random',n=n);return 'r'*n
  class Scheduler:
   def __init__(self,**kw):self.jobs=[];self.running=False
   def add_job(self,fn,kind,**kw):self.jobs.append({'id':fn.__name__,'kind':kind,'delay':3 if kind=='date' else 0,'interval':int(kw.get('minutes',0)*60)})
   def get_jobs(self):return self.jobs
   def print_jobs(self):pass
   def start(self):self.running=True
   def remove_all_jobs(self):self.jobs=[]
   def shutdown(self):self.running=False
  class Trigger:
   @staticmethod
   def from_crontab(s):return s
  ns={'_PluginBase':Base,'logger':Logger(),'Event':Event,'Path':FakePath,'os':os,'settings':Attr(TZ='UTC',TEMP_PATH=FakePath('/temp')),'DownloaderHelper':Helper,'StringUtils':Random,'bdecode':bdecode,'bencode':bencode,'BackgroundScheduler':Scheduler,'CronTrigger':Trigger,'datetime':datetime.datetime,'timedelta':datetime.timedelta,'pytz':Attr(timezone=lambda _:datetime.timezone.utc),'NotificationType':Attr(SiteMessage='SiteMessage')}
  exec(compile(tree,str(source),'exec'),ns)
  obj=ns['TorrentTransfer']();obj.systemmessage=Message();obj._event=Event();obj._recheck_torrents=copy.deepcopy(env.scenario.get('queue',{}));obj._is_recheck_running=env.scenario.get('running',False)
  error=None
  out=io.StringIO()
  with contextlib.redirect_stdout(out):
   try:
    obj.init_plugin(copy.deepcopy(self.config))
    if self.scenario.get('stop'):obj._event.set()
    if self.scenario.get('init_only'):pass
    elif self.scenario.get('only_recheck'):obj.check_recheck()
    else:obj.transfer()
   except Exception as e:error={'type':type(e).__name__,'message':str(e)}
  self.prints=out.getvalue().splitlines()
  return {'calls':self.calls,'logs':self.logs,'history':self.history,'messages':self.messages,'writes':self.writes,'queue':obj._recheck_torrents,'running':obj._is_recheck_running,'state':obj.get_state(),'jobs':obj._scheduler.jobs if obj._scheduler else [],'error':error,'stdout':self.prints}
base={'enabled':False,'fromdownloader':'source','todownloader':'destination','fromtorrentpath':'/backup','frompath':'/source','topath':'/destination','add_torrent_tags':'已整理,转移做种','transferemptylabel':False,'autostart':True,'skipverify':False,'notify':True,'deletesource':False,'deleteduplicate':False,'remainoldcat':False,'remainoldtag':False}
cases=[]
def case(name,config=None,scenario=None):
 c={**base,**(config or{})};s=scenario or{};cases.append({'name':name,'config':c,'scenario':s,'expected':Env(c,s).run()})
for a in ['qbittorrent','transmission']:
 for b in ['qbittorrent','transmission']:
  for auto,skip in [(False,False),(True,False),(False,True),(True,True)]:case(f'{a}-{b}-auto{auto}-skip{skip}',{'autostart':auto,'skipverify':skip},{'fromtype':a,'totype':b})
for name,c,s in [
 ('no-completed',{}, {'no_torrents':True}),('stop-before-filter',{}, {'stop':True}),('missing-root',{}, {'missing_root':True}),('same-downloader',{'todownloader':'source'},{}),('missing-source',{}, {'missing_service':'source'}),('inactive-destination',{}, {'inactive':'destination'}),
 ('path-excluded',{'nopaths':'/other\n/source'},{}),('path-prefix-source-semantic',{'nopaths':'/sour'},{}),('category-missing',{'includecategory':'电视,电影 '},{}),('category-included',{'includecategory':'电影,电视'},{}),
 ('excluded-label',{'nolabels':'电视,自有'},{}),('all-required-labels',{'includelabels':'已整理,自有'},{}),('one-required-missing',{'includelabels':'已整理,missing'},{}),('empty-disabled',{}, {'torrent':qb_torrent(tags='')}),('empty-enabled-bypass-labels',{'transferemptylabel':True,'includelabels':'missing'}, {'torrent':qb_torrent(tags='')}),
 ('missing-torrent-file',{}, {'missing_file':True}),('duplicate-skip',{}, {'duplicate':True}),('duplicate-delete',{'deleteduplicate':True},{'duplicate':True}),('duplicate-delete-false-count-still-increments',{'deleteduplicate':True},{'duplicate':True,'control_false':True}),
 ('path-convert-fails',{'frompath':'/not-source'},{}),('path-no-conversion',{'topath':''},{}),('no-save-to-root',{}, {'torrent':qb_torrent(save_path='')}),
 ('empty-torrent',{}, {'torrent_b64':''}),('invalid-torrent',{}, {'torrent_b64':base64.b64encode(b'garbage').decode()}),('missing-announce-no-resume',{}, {'torrent_b64':base64.b64encode(bencode({'info':{'name':'owned','length':1}})).decode()}),
 ('restore-trackers',{}, {'torrent_b64':base64.b64encode(bencode({'info':{'name':'owned','length':1}})).decode(),'resume_b64':base64.b64encode(bencode({'trackers':[['https://a.invalid/announce','https://b.invalid/announce'],['https://c.invalid/announce']]})).decode()}),
 ('restore-empty-trackers-keeps-original',{}, {'torrent_b64':base64.b64encode(bencode({'info':{'name':'owned','length':1}})).decode(),'resume_b64':base64.b64encode(bencode({'trackers':[]})).decode()}),
 ('invalid-resume',{}, {'torrent_b64':base64.b64encode(bencode({'info':{'name':'owned','length':1}})).decode(),'resume_b64':base64.b64encode(b'garbage').decode()}),
 ('keep-category-tags',{'remainoldtag':True,'remainoldcat':True,'add_torrent_tags':' 已整理, 转移做种 '},{}),('delete-after-success',{'deletesource':True},{}),('delete-false-still-save',{'deletesource':True},{'control_false':True}),('qb-false-tuple-truthy',{}, {'add_failure':True}),('tr-add-failure',{}, {'totype':'transmission','add_failure':True}),('qb-missing-id',{}, {'find_missing':True}),
 ('check-no-ready',{}, {'recheck':'waiting'}),('check-none',{}, {'recheck':'none'}),('check-empty',{}, {'recheck':'empty'}),('check-already-running',{}, {'running':True}),
 ('enabled-cron-init',{'enabled':True,'cron':'0 */2 * * *'}, {'init_only':True}),('once-init',{'onlyonce':True},{'init_only':True}),('once-invalid',{'onlyonce':True},{'init_only':True,'missing_root':True})]:case(name,c,s)
helpers=[]
for save,a,b in [('',None,'/dest'),('/data/a','/data','/other'),('/data2/a','/data','/other'),('/else','/data','/other'),('/data','', '/other'),('C:\\data\\a','C:\\data','D:\\dest'),('//data//a','//data','/dest'),('/data/a','/data',None)]:
 result=Env(base,{});ns={};exec(compile(tree,str(source),'exec'),{'_PluginBase':object,'Event':Event},ns);cls=ns['TorrentTransfer'];cls._TorrentTransfer__convert_save_path.__globals__['os']=os;v=cls._TorrentTransfer__convert_save_path(save,a,b);helpers.append({'save':save,'from':a,'to':b,'result':v})
report={'source_sha256':hashlib.sha256(raw).hexdigest(),'python':sys.version,'scope':'whole actual class AST; exact fast-bencode1.1.8; synthetic downloader/scheduler/path/DB only; no original service imports/config/KV/IO','cases':cases,'paths':helpers,'wheels':{p.name:hashlib.sha256(p.read_bytes()).hexdigest()for p in(root/'oracle-wheels').glob('*.whl')}}
(root/'source-oracle.json').write_text(json.dumps(report,ensure_ascii=False,separators=(',',':'))+'\n');print(json.dumps({'cases':len(cases),'paths':len(helpers),'source_sha256':report['source_sha256']}))
