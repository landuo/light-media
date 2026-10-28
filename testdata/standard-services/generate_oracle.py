"""Fixed scheduler list replay. Imports no original app or service configuration."""
import ast, hashlib, json, sys, threading
from pathlib import Path
from types import SimpleNamespace
from datetime import datetime,timezone,timedelta
from typing import List
root=Path(sys.argv[1] if len(sys.argv)>1 else '/Users/accidia/projects/MoviePilot')
path=root/'app/scheduler.py';tree=ast.parse(path.read_text())
cls=next(x for x in tree.body if isinstance(x,ast.ClassDef) and x.name=='Scheduler')
method=next(x for x in cls.body if isinstance(x,ast.FunctionDef) and x.name=='list')
method.decorator_list=[]
class Info:
 def __init__(self,**kwargs):self.fields=kwargs
class FakeScheduler:
 running=True
 def __init__(self,jobs):self.jobs=jobs
 def get_jobs(self):return self.jobs
class Wrapper:pass
fixed=datetime(2026,10,26,tzinfo=timezone.utc)
class FrozenDateTime(datetime):
 @classmethod
 def now(cls,tz=None):return fixed
timer_path=root/'app/utils/timer.py'
timer_tree=ast.parse(timer_path.read_text())
timer_cls=next(x for x in timer_tree.body if isinstance(x,ast.ClassDef) and x.name=='TimerUtils')
timer=next(x for x in timer_cls.body if isinstance(x,ast.FunctionDef) and x.name=='time_difference')
timer.decorator_list=[]
timer_ns={'datetime':SimpleNamespace(datetime=FrozenDateTime,timezone=timezone)}
exec(compile(ast.fix_missing_locations(ast.Module(body=[timer],type_ignores=[])),'fixed-timer-difference','exec'),timer_ns)
ns={'List':List,'schemas':SimpleNamespace(ScheduleInfo=Info),'TimerUtils':SimpleNamespace(time_difference=timer_ns['time_difference'])}
exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-scheduler-list','exec'),ns)
Wrapper.list=ns['list']
cases=[]
for name,active in [('waiting-by-time',False),('running-first-next-time-null',True)]:
 definitions=[{'id':'OwnedInput_a-slow','delay':7200},{'id':'OwnedInput_z-fast','delay':3600},{'id':'OwnedInput_running','delay':10800}]
 obj=Wrapper();obj._lock=threading.RLock();obj._jobs={x['id']:{'name':x['id'],'provider_name':'Owned provider','running':active and x['id'].endswith('_running')} for x in definitions};obj._scheduler=FakeScheduler([SimpleNamespace(id=x['id'],name=x['id'],next_run_time=fixed+timedelta(seconds=x['delay'])) for x in definitions]);obj.get_progress=lambda id:None
 result=[{'id':x.fields['id'],'status':x.fields['status'],'next_run':x.fields.get('next_run')} for x in obj.list()]
 cases.append({'name':name,'definitions':definitions,'active':active,'expected':result})
paused_cases=[]
for name,delays in [('single-paused',[None]),('paused-mixed-sort-error',[None,3600])]:
 obj=Wrapper();obj._lock=threading.RLock();obj._jobs={f'OwnedInput_{j}':{'name':str(j),'provider_name':'Owned provider','running':False} for j in range(len(delays))};obj._scheduler=FakeScheduler([SimpleNamespace(id=f'OwnedInput_{j}',name=str(j),next_run_time=None if delay is None else fixed+timedelta(seconds=delay)) for j,delay in enumerate(delays)]);obj.get_progress=lambda id:None
 error=None;result=None
 try:result=[{'id':x.fields['id'],'status':x.fields['status'],'next_run':x.fields.get('next_run')} for x in obj.list()]
 except Exception as e:error=type(e).__name__
 paused_cases.append({'name':name,'delays':delays,'error':error,'expected':result})
print(json.dumps({'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':'app/scheduler.py:1393','source_sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'timer_source_sha256':hashlib.sha256(timer_path.read_bytes()).hexdigest(),'kind':'fixed AST projection; controlled clock/list dependencies, not runtime scheduler execution','cases':cases,'paused_cases':paused_cases},indent=2))
