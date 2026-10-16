"""Offline AST failure-prefix oracle; no MoviePilot imports, filesystem or network.
Only source lifecycle/command methods execute. Stub clean failure is labelled;
the already-proven native/parser oracles are not duplicated by this test.
"""
import ast,copy,datetime,hashlib,json,pathlib,types,warnings
warnings.filterwarnings("ignore",category=SyntaxWarning)
source=pathlib.Path(__file__).resolve().parents[4]/'work/plugin-source/CleanInvalidSeed/__init__.py'
b=source.read_bytes()
assert hashlib.sha256(b).hexdigest()=='fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda'
tree=ast.parse(b)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='CleanInvalidSeed')
methods={n.name:n for n in cls.body if isinstance(n,ast.FunctionDef)}
class Quiet:
 def __getattr__(self,k):return lambda *a,**kw:None
class Clock:
 @staticmethod
 def now(tz=None):return datetime.datetime(2026,10,17,1,tzinfo=datetime.timezone.utc)
results=[]
for case in ['toggle_second_notice_failure','command_clean_stub_failure','onlyonce_config_failure']:
 trace=[];committed=[];jobs=[]
 class Base:
  def post_message(self,**kw):
   trace.append('notice')
   if case=='toggle_second_notice_failure' and trace.count('notice')==2:raise OSError('synthetic notice failure')
   committed.append('notice')
  def update_config(self,c):
   trace.append('config')
   if case=='onlyonce_config_failure':raise OSError('synthetic config failure')
   self.saved=copy.deepcopy(c);committed.append('config')
  def clean_invalid_seed(self):
   trace.append('clean_stub')
   raise OSError('synthetic clean boundary failure')
 class Scheduler:
  def __init__(self,**kw):self.jobs=[];self.running=False;jobs.append(self)
  def add_job(self,**kw):trace.append('date_add');self.jobs.append(kw)
  def get_jobs(self):return self.jobs
  def print_jobs(self):pass
  def start(self):trace.append('date_start');self.running=True
  def remove_all_jobs(self):self.jobs=[]
  def shutdown(self):self.running=False
 selected=[]
 for n in cls.body:
  if isinstance(n,(ast.Assign,ast.AnnAssign)):
   # Class constants are literals; do not execute arbitrary module imports.
   try:ast.literal_eval(n.value)
   except Exception:continue
   selected.append(copy.deepcopy(n))
 for name in ['init_plugin','_update_config','handle_commands','stop_service']:
  n=copy.deepcopy(methods[name]);n.decorator_list=[];n.returns=None
  for a in n.args.args:a.annotation=None
  selected.append(n)
 newcls=ast.ClassDef(name='Source',bases=[ast.Name(id='Base',ctx=ast.Load())],keywords=[],body=selected,decorator_list=[])
 env={'Base':Base,'DownloaderHelper':lambda:None,'logger':Quiet(),'BackgroundScheduler':Scheduler,'settings':types.SimpleNamespace(TZ='UTC'),'datetime':Clock,'timedelta':datetime.timedelta,'pytz':types.SimpleNamespace(timezone=lambda _:datetime.timezone.utc)}
 mod=ast.fix_missing_locations(ast.Module(body=[newcls],type_ignores=[]));exec(compile(mod,'<installed-CleanInvalidSeed-method-AST>','exec'),env)
 p=env['Source']()
 error=None
 try:
  if case=='onlyonce_config_failure':p.init_plugin({'onlyonce':True,'enabled':False,'min_seeding_days':0})
  else:
   p.init_plugin({'enabled':False,'min_seeding_days':0,'downloaders':[]});trace.clear();committed.clear();jobs.clear()
   p.handle_commands(types.SimpleNamespace(event_data={'action':'toggle_notify_all' if case.startswith('toggle') else 'detect_invalid_torrents','channel':'fixture','user':'fixture'}))
 except Exception as e:error=type(e).__name__
 results.append({'case':case,'attempts':trace,'committed_prefix':committed,'error':error,'saved_notify_all':getattr(p,'saved',{}).get('notify_all'),'scheduler_running':[s.running for s in jobs],'date_jobs':[len(s.jobs) for s in jobs]})
print(json.dumps({'source_sha256':hashlib.sha256(b).hexdigest(),'cases':results},ensure_ascii=False,indent=2))
