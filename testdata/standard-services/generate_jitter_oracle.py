"""Actual fixed APScheduler dependency, deterministic uniform draw for comparison."""
import hashlib,inspect,json,random
from datetime import datetime,timezone,timedelta
import apscheduler
from apscheduler.triggers.interval import IntervalTrigger
from apscheduler.triggers.cron import CronTrigger
anchor=datetime(2026,10,26,tzinfo=timezone.utc)
random.uniform=lambda low,high:low+(high-low)*0.25
cases=[]
for name,kind,end in [('interval-jitter-advances-previous','interval',None),('cron-jitter-retains-calendar','cron',None),('interval-jitter-end-exhausted','interval',anchor+timedelta(seconds=0.5)),('cron-jitter-clips-end','cron',anchor+timedelta(seconds=0.5))]:
 kwargs={'timezone':timezone.utc,'jitter':4,'start_date':anchor,'end_date':end}
 trigger=IntervalTrigger(seconds=10,**kwargs) if kind=='interval' else CronTrigger(second='*/10',**kwargs)
 times=[];previous=None;now=anchor
 for index in range(2):
  value=trigger.get_next_fire_time(previous,now)
  if value is None:break
  times.append(value.isoformat());previous=value;now=value+timedelta(microseconds=1)
 cases.append({'name':name,'kind':kind,'jitter':4,'draw':0.25,'start':anchor.isoformat(),'end':end.isoformat() if end else None,'times':times})
print(json.dumps({'version':apscheduler.__version__,'kind':'actual IntervalTrigger/CronTrigger with controlled uniform draw; native Go RNG sequences differ, uniform algorithm preserved','source_sha256':{'interval':hashlib.sha256(inspect.getsource(IntervalTrigger).encode()).hexdigest(),'cron':hashlib.sha256(inspect.getsource(CronTrigger).encode()).hexdigest()},'cases':cases},indent=2))
