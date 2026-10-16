"""Offline source AST oracle: never import MoviePilot or access its config/services."""
import ast,copy,dataclasses,datetime,hashlib,json,os,pathlib,shutil,sys,tempfile,threading,time,traceback,types
ROOT=pathlib.Path(__file__).parent
SOURCE=ROOT/'source.py'
class NullLogger:
 def __init__(self):self.errors=0
 def __getattr__(self,n):
  def record(*a,**k):
   if n=='error':self.errors+=1
  return record
class Clock(datetime.datetime):
 current=datetime.datetime(2026,1,1)
 @classmethod
 def now(cls):return cls.current
class Timer:
 def __init__(self,seconds,callback):self.seconds=seconds;self.daemon=False
 def start(self):pass
 def cancel(self):pass
class Observer:
 def schedule(self,*a,**k):pass
 def start(self):pass
 def stop(self):pass
 def join(self,*a):pass
class History:
 def __init__(self,trace,missing=(),failures=()):self.trace=trace;self.missing=missing;self.failures=failures
 def get_by_src(self,p):return None if 'src:'+str(p) in self.missing else types.SimpleNamespace(id='src:'+str(p))
 def get_by_dest(self,p):return None if 'dest:'+str(p) in self.missing else types.SimpleNamespace(id='dest:'+str(p))
 def delete(self,key):
  self.trace.append({'op':'history','id':key})
  if key in self.failures:raise RuntimeError('synthetic history failure')
class Storage:
 def __init__(self,trace,items,errors=()):self.trace=trace;self.items=[Item(**x) for x in items];self.errors=errors
 def exists(self,item):
  if 'exists:'+item.path.rstrip('/') in self.errors:raise RuntimeError('synthetic storage failure')
  return any(x.storage==item.storage and x.path.rstrip('/')==item.path.rstrip('/') and x.type=='dir' for x in self.items)
 def list_files(self,item,recursion=False):
  if 'list:'+item.path.rstrip('/') in self.errors:raise RuntimeError('synthetic storage failure')
  return [x for x in self.items if x.storage==item.storage and str(pathlib.Path(x.path).parent)==item.path.rstrip('/') and x.path.rstrip('/')!=item.path.rstrip('/')]
 def delete_file(self,item):
  self.trace.append({'op':'storage_delete','storage':item.storage,'path':item.path,'type':item.type,'fileid':getattr(item,'fileid','')})
  if 'delete:'+item.path in self.errors:raise RuntimeError('synthetic storage failure')
  if getattr(item,'fail',False):return False
  self.items=[x for x in self.items if not (x.storage==item.storage and x.path==item.path)]
  return True
class Item:
 def __init__(self,storage='',path='',type='',name='',extension='',fileid='',**kw):
  self.storage=storage;self.path=path;self.type=type;self.name=name;self.extension=extension;self.fileid=fileid
  for k,v in kw.items():setattr(self,k,v)
 def model_copy(self,update):
  x=copy.copy(self)
  for k,v in update.items():setattr(x,k,v)
  return x

def source_namespace():
 tree=ast.parse(SOURCE.read_text())
 wanted={'is_trickplay_path','updateState','FileInfo','DeletionTask','FileMonitorHandler','RemoveLink'}
 tree.body=[n for n in tree.body if isinstance(n,(ast.ClassDef,ast.FunctionDef)) and n.name in wanted]
 ns={'os':os,'copy':copy,'shutil':shutil,'threading':threading,'time':time,'traceback':traceback,'Path':pathlib.Path,'List':list,'Tuple':tuple,'Dict':dict,'Any':object,'NamedTuple':__import__('typing').NamedTuple,'dataclass':dataclasses.dataclass,'datetime':Clock,'timedelta':datetime.timedelta,'state_lock':threading.Lock(),'deletion_queue_lock':threading.Lock(),'logger':NullLogger(),'_PluginBase':type('Base',(),{}),'FileSystemEventHandler':object,'NotificationType':types.SimpleNamespace(SiteMessage='SiteMessage'),'EventType':types.SimpleNamespace(DownloadFileDeleted='DownloadFileDeleted'),'schemas':types.SimpleNamespace(FileItem=Item)}
 exec(compile(tree,str(SOURCE),'exec'),ns)
 return ns
NS=source_namespace()
form,model=NS['RemoveLink']().get_form()
(ROOT/'form.json').write_text(json.dumps({'conf':form,'model':model},ensure_ascii=False,separators=(',',':'))+'\n')

def run_case(case):
 with tempfile.TemporaryDirectory(prefix='remove-link-fixture-') as tmp:
  root=pathlib.Path(tmp);(root/'.fixture-owner').write_text('owned')
  trace=[];storage=Storage(trace,case.get('storage',[]),case.get('storage_errors',[]));p=NS['RemoveLink']();logger=NullLogger();NS['logger']=logger
  p.file_state={};p.deletion_queue=[];p._observer=[];p._deletion_timer=None
  p._RemoveLink__choose_observer=lambda:Observer()
  p.post_message=lambda **k:trace.append({'op':'notify',**k})
  p.systemmessage=types.SimpleNamespace(put=lambda *a,**k:trace.append({'op':'system','text':str(a)}))
  NS['eventmanager']=types.SimpleNamespace(send_event=lambda event,data:trace.append({'op':'event','event':event,**data}))
  NS['TransferHistoryOper']=lambda:History(trace,expand(case.get('missing_history',[])),expand(case.get('history_failures',[])));NS['StorageChain']=lambda:storage
  fake_threading=types.SimpleNamespace(Timer=Timer)
  NS['threading']=fake_threading
  def expand(v):
   if isinstance(v,str):return v.replace('@ROOT@',tmp)
   if isinstance(v,list):return [expand(x) for x in v]
   if isinstance(v,dict):return {k:expand(x) for k,x in v.items()}
   return v
  def path(name):return root/name
  for name,content in case.get('files',{}).items():
   f=path(name);f.parent.mkdir(parents=True,exist_ok=True);f.write_text(content)
  for pair in case.get('links',[]):
   f=path(pair['path']);f.parent.mkdir(parents=True,exist_ok=True);os.link(path(pair['target']),f)
  for d in case.get('dirs',[]):path(d).mkdir(parents=True,exist_ok=True)
  Clock.current=datetime.datetime(2026,1,1)
  init_error=None
  try:p.init_plugin(expand(case.get('config',{})))
  except Exception as e:init_error=type(e).__name__+':'+str(e)
  case['scan_order']=[x.replace(tmp,'/fixture') for x in p.file_state]
  for name,identity in case.get('identities',{}).items():
   old=p.file_state[str(path(name))];p.file_state[str(path(name))]=NS['FileInfo'](identity['dev'],identity['inode'],old.add_time)
  modules={}
  for name in ['app','app.core','app.core.config']:
   modules[name]=sys.modules.get(name);sys.modules[name]=types.ModuleType(name)
  sys.modules['app.core.config'].settings=types.SimpleNamespace(RMT_MEDIAEXT=case.get('media_exts',['.mkv','.mp4']))
  error=init_error
  original_unlink=pathlib.Path.unlink
  deny={str(path(x)) for x in case.get('deny_unlink',[])}
  def unlink_guard(file,*a,**kw):
   if str(file) in deny:raise PermissionError('synthetic unlink denied')
   return original_unlink(file,*a,**kw)
  pathlib.Path.unlink=unlink_guard
  try:
   for step in ([] if init_error else case.get('steps',[])):
    Clock.current=datetime.datetime(2026,1,1)+datetime.timedelta(seconds=step.get('at',0))
    f=path(step.get('path',''))
    mode=step.get('mode','hardlink')
    handler=NS['FileMonitorHandler'](str(root),p,monitor_type=mode)
    op=step['op']
    if op=='delete':
     if f.is_dir():shutil.rmtree(f)
     elif f.exists():f.unlink()
     handler.on_deleted(types.SimpleNamespace(src_path=str(f),is_directory=step.get('directory',False)))
    elif op=='create':
     f.parent.mkdir(parents=True,exist_ok=True)
     if 'link' in step:os.link(path(step['link']),f)
     else:f.write_text(step.get('content','new'))
     handler.on_created(types.SimpleNamespace(src_path=str(f),is_directory=False))
    elif op=='move':
     d=path(step['dest']);d.parent.mkdir(parents=True,exist_ok=True);f.rename(d)
     if step.get('missing'):d.unlink()
     handler.on_moved(types.SimpleNamespace(src_path=str(f),dest_path=str(d),is_directory=False))
    elif op=='tick':p._process_deletion_queue()
    elif op=='stop':p.stop_service()
    elif op=='reload':p.init_plugin(expand(step['config']))
    elif op=='external_remove':f.unlink()
    elif op=='replace_without_event':f.unlink();f.write_text('replacement')
    else:raise ValueError(op)
  except Exception as e:error=type(e).__name__+':'+str(e)
  finally:
   pathlib.Path.unlink=original_unlink
   for name,value in modules.items():
    if value is None:sys.modules.pop(name,None)
    else:sys.modules[name]=value
  def normalize(v):
   if isinstance(v,str):return v.replace(tmp,'/fixture')
   if isinstance(v,list):return [normalize(x) for x in v]
   if isinstance(v,dict):return {k:normalize(x) for k,x in v.items()}
   return v
  files=sorted(str(f.relative_to(root))+('/' if f.is_dir() else '') for f in root.rglob('*') if f.name!='.fixture-owner')
  queue=[{'path':str(t.file_path),'at':(t.timestamp-datetime.datetime(2026,1,1)).total_seconds(),'processed':t.processed} for t in p.deletion_queue]
  result={'files':files,'state':list(p.file_state),'queue':queue,'trace':trace,'delay':p._delay_seconds,'enabled':p.get_state(),'error':error,'logged_errors':logger.errors}
  return normalize(result)

def config(**kw):return {'enabled':True,'monitor_dirs':'@ROOT@/m','delayed_deletion':False,**kw}
def base(name,**kw):return {'name':name,'config':config(), 'files':{'m/a.mkv':'video','m/keep.bin':'keep'},'links':[{'path':'m/b.mkv','target':'m/a.mkv'}],'steps':[{'op':'delete','path':'m/a.mkv','at':100}],**kw}
cases=[]
for flags in [dict(),dict(delete_torrents=True,delete_history=True,notify=True),dict(delete_scrap_infos=True,delete_torrents=True,delete_history=True,notify=True)]:
 c=base('immediate-'+str(len(cases)));c['config']=config(**flags);c['files'].update({'m/a.nfo':'a','m/b.zh.srt':'b','m/poster.jpg':'poster'});cases.append(c)
for delay in [True,False,None,0,9,10,30,86401,'42','bad',2.9,[],{}]:
 c=base('delay-config-'+str(len(cases)));c['config']=config(delayed_deletion=True,delay_seconds=delay);c['steps']+=[{'op':'tick','at':200000}];cases.append(c)
for at,create,link in [(125,True,True),(125,False,False),(50,True,True),(130,True,False)]:
 c=base('delayed-recreate-'+str(len(cases)));c['config']=config(delayed_deletion=True,delay_seconds=30,delete_history=True,delete_torrents=True,delete_scrap_infos=True,notify=True);c['files']['m/a.nfo']='old'
 if create:c['steps'].append({'op':'create','path':'m/a.mkv' if not link else 'm/new.mkv','at':at,**({'link':'m/b.mkv'} if link else {})})
 c['steps'].append({'op':'tick','at':140});cases.append(c)
for exclusion in ['@ROOT@/m/b.mkv','@ROOT@/m/b','@ROOT@/m',' @ROOT@/m/b.mkv \n']:
 c=base('excluded-'+str(len(cases)));c['config']=config(exclude_dirs=exclusion,delete_history=True,delete_torrents=True,notify=True);cases.append(c)
for ext in ['.tmp','.part','.TMP','.nfo','.trickplay/a.mkv']:
 c=base('initial-temp-'+str(len(cases)));c['files']={'m/a'+ext:'video','m/keep.bin':'keep'};c['links']=[];c['steps']=[{'op':'delete','path':'m/a'+ext,'at':100}];c['config']=config(delete_history=True,delete_torrents=True);cases.append(c)
for missing in [False,True]:
 c=base('move-'+str(len(cases)));c['steps']=[{'op':'move','path':'m/a.mkv','dest':'m/new.mkv','missing':missing,'at':100}];c['config']=config(delete_torrents=True,delete_history=True,notify=True);cases.append(c)
for stop in [True,False]:
 c=base('stop-'+str(len(cases)));c['config']=config(delayed_deletion=True,delay_seconds=86400,delete_history=True);c['steps'].append({'op':'stop' if stop else 'tick','at':101});cases.append(c)
c=base('root-is-not-boundary');c['files']={'m/a.mkv':'video','m/a.nfo':'meta'};c['links']=[];c['config']=config(delete_scrap_infos=True,notify=True);cases.append(c)
c=base('entity-replaced-without-event');c['steps']=[{'op':'replace_without_event','path':'m/b.mkv','at':90},{'op':'delete','path':'m/a.mkv','at':100}];cases.append(c)
c=base('directory-event-before-filters');c['links']=[];c['config']=config(exclude_keywords='m',delete_torrents=True);c['steps']=[{'op':'delete','path':'m','directory':True,'at':100}];cases.append(c)
for storage,upper,fail in [('local',False,False),('alist',False,False),('openlist',False,False),('u115',True,False),('alipan',False,True)]:
 suffix='.STRM' if upper else '.strm'
 items=[{'storage':storage,'path':'/cloud','type':'dir','name':'cloud','fileid':'root'},{'storage':storage,'path':'/cloud/series','type':'dir','name':'series','fileid':'dirid'},{'storage':storage,'path':'/cloud/series/a.mkv','type':'file','name':'a.mkv','extension':'mkv','fileid':'videoid','fail':fail},{'storage':storage,'path':'/cloud/series/a.nfo','type':'file','name':'a.nfo','extension':'nfo','fileid':'nfoid'},{'storage':storage,'path':'/cloud/series/poster.jpg','type':'file','name':'poster.jpg','extension':'jpg','fileid':'posterid'}]
 c={'name':'strm-'+storage+'-'+str(upper)+'-'+str(fail),'config':config(monitor_strm_deletion=True,strm_path_mappings='@ROOT@/m:'+storage+':/cloud/series',delete_scrap_infos=True,delete_history=True,delete_torrents=True,notify=True),'files':{'m/a'+suffix:'stream','m/a.nfo':'meta','m/keep.bin':'keep'},'storage':items,'steps':[{'op':'delete','path':'m/a'+suffix,'mode':'strm','at':100}]};cases.append(c)
for delayed in [False,True]:
 for which in ['a','b']:
  c=base('history-failure-'+str(delayed)+'-'+which);c['config']=config(delayed_deletion=delayed,delete_history=True,delete_torrents=True,notify=True);c['history_failures']=['src:@ROOT@/m/'+which+'.mkv'];c['steps'].append({'op':'tick','at':200});cases.append(c)
c=base('missing-history');c['config']=config(delete_history=True,delete_torrents=True);c['missing_history']=['src:@ROOT@/m/a.mkv'];cases.append(c)
c=base('dev-is-part-of-identity');c['identities']={'m/a.mkv':{'dev':1,'inode':100},'m/b.mkv':{'dev':2,'inode':100}};cases.append(c)
c=base('same-dev-inode-is-match');c['identities']={'m/a.mkv':{'dev':2,'inode':100},'m/b.mkv':{'dev':2,'inode':100}};cases.append(c)
for ext in ['.tmp','.temp','.part','.TMP']:
 c=base('created-filter-'+ext);c['links']=[];c['steps']=[{'op':'create','path':'m/new'+ext,'at':50},{'op':'delete','path':'m/new'+ext,'at':100}];c['config']=config(delete_history=True,delete_torrents=True);cases.append(c)
for seconds in ['999999999999999999999999999999999999','1_000','١٢','１２','2.9','0x10',1e100]:
 c=base('delay-extra-'+str(seconds));c['config']=config(delayed_deletion=True,delay_seconds=seconds);cases.append(c)
c=base('reload-new-config-drains-old-queue');c['config']=config(delayed_deletion=True,delay_seconds=1000,delete_history=False);c['steps'].append({'op':'reload','at':101,'config':config(delayed_deletion=False,delete_history=True)});cases.append(c)
for operation in ['exists:/cloud/series','list:/cloud/series','delete:/cloud/series/a.mkv','delete:/cloud/series/a.nfo']:
 c=copy.deepcopy(next(x for x in cases if x['name']=='strm-local-False-False'));c['name']='storage-error-'+operation;c['storage_errors']=[operation];cases.append(c)
for field,value in [('custom_scrap_extensions',True),('monitor_dirs',True),('exclude_keywords',True),('exclude_dirs',True)]:
 c=base('typed-config-'+field);c['config']=config(delete_torrents=True,**{field:value});cases.append(c)
c=base('inactive-unused-typed-monitor');c['config']=config(enabled=False,monitor_dirs={'unexpected':'old scalar'});c['steps']=[];cases.append(c)
for target in ['m/b.mkv','m/a.nfo']:
 c=base('unlink-denied-'+target);c['config']=config(delete_scrap_infos=True,delete_history=True,delete_torrents=True,notify=True);c['files']['m/a.nfo']='meta';c['links'].append({'path':'m/c.mkv','target':'m/a.mkv'});c['deny_unlink']=[target];cases.append(c)
c=base('stale-missing-link');c['links'].append({'path':'m/c.mkv','target':'m/a.mkv'});c['config']=config(delete_history=True,delete_torrents=True,notify=True);c['steps'].insert(0,{'op':'external_remove','path':'m/b.mkv','at':99});cases.append(c)
output={'source_sha256':json.load(open(ROOT/'installed-reference.json'))['sha256'],'cases':[dict(input=c,expected=run_case(c)) for c in cases]}
(ROOT/'oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'form_fields':list(model),'source_sha256':output['source_sha256']},ensure_ascii=False))
# Pure helpers are taken from the same selected source AST, not restated logic.
helper=[]
def check(kind,args,fn):
 try:result=fn();error=None
 except Exception as e:result=None;error=type(e).__name__+':'+str(e)
 helper.append({'kind':kind,'args':args,'result':result,'error':error})
p=NS['RemoveLink']()
for value in ['', 'NFO，.JSON\n-mediainfo.JSON, txt, NFO', 'İ,ΟΣ,ΟΣΑ', '.nfo,,.srt，.😀', '...,.a.b,-suffix']:
 check('extensions',[value],lambda:NS['RemoveLink']._parse_custom_scrap_extensions(value))
for value in ['/m/a.TRICKPLAY/file.mkv','/m/a.trickplay/../file.mkv','/m/a.trickplay.txt','relative/.trickplay/a','/m/a.mkv']:
 check('trickplay',[value],lambda:NS['is_trickplay_path'](pathlib.Path(value)))
for value in ['','.', '/', '//', '/m//a/../b.STRM', '.mkv', '...foo', 'foo.', 'foo.tar.gz', '//server/a.strm']:
 check('path',[value],lambda:[str(pathlib.Path(value)),str(pathlib.Path(value).parent),pathlib.Path(value).name,pathlib.Path(value).stem,pathlib.Path(value).suffix])
for raw,value in [('/m:/cloud','/m/a.strm'),('/m:alist:/cloud','/movie/a.strm'),('/m:u115:/x\n/m/sub:alist:/y','/m/sub/a.strm'),('/m:/a\n/m/sub:/b\n/m:/last','/m/a.STRM'),(':/root','/any/a.strm'),('/m:/x','/m/../a.strm'),('//server:/x','//server/a.strm'),('/m:/x','/unmatched/a.strm')]:
 p.strm_path_mappings=raw
 check('mapping',[raw,value],lambda:p._get_storage_path_from_strm(pathlib.Path(value)))
for value,parent in [('/a/b','/a'),('/ab','/a'),('/a','/a'),('a/b','/a'),('/a/../b','/b'),('/a/b',''),('//server/a','//server')]:
 check('child',[value,parent],lambda:NS['RemoveLink']._is_same_or_child_path(pathlib.Path(value),parent))
for value,ext in [('/a.NFO',''),('/.nfo',''),('/foo-mediainfo.JSON','-mediainfo.json'),('/ΟΣ','ος'),('/İ','i\u0307'),('/a.json','.txt'),('/movie.mkv.nfo','')]:
 p._custom_scrap_extensions=p._parse_custom_scrap_extensions(ext)
 check('scrap',[value,ext],lambda:p._is_scrap_file(pathlib.Path(value)))
(ROOT/'helpers-oracle.json').write_text(json.dumps({'cases':helper},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'helper_cases':len(helper)}))
