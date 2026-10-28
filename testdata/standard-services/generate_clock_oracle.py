"""Actual APScheduler 3.11.3 processing/execution with a controlled local clock.
No MoviePilot import, threads, remote service or application Python dependency.
"""
import hashlib,inspect,json,logging
from datetime import datetime,timezone,timedelta
import apscheduler,apscheduler.schedulers.base as scheduler_base,apscheduler.executors.base as executor_base
from apscheduler.schedulers.base import BaseScheduler,STATE_RUNNING
from apscheduler.executors.base import BaseExecutor,run_job
from apscheduler.triggers.interval import IntervalTrigger
logging.disable(logging.CRITICAL)
anchor=datetime(2026,10,26,tzinfo=timezone.utc);fixed=anchor+timedelta(seconds=3.5)
class FrozenDateTime(datetime):
 @classmethod
 def now(cls,tz=None):return fixed
scheduler_base.datetime=executor_base.datetime=FrozenDateTime
class RecordingExecutor(BaseExecutor):
 def __init__(self):super().__init__();self.pending=[]
 def _do_submit_job(self,job,run_times):self.pending.append((job,run_times))
 def finish(self):
  for job,times in self.pending:self._run_job_success(job.id,run_job(job,'default',times,'owned-clock-oracle'))
class ManualScheduler(BaseScheduler):
 def wakeup(self):pass
 def shutdown(self,wait=True):super().shutdown(wait)
cases=[]
for name,options in [('default-coalesced',{}),('uncoalesced-unlimited',{'coalesce':False,'misfire_grace_time':None}),('uncoalesced-default-grace',{'coalesce':False}),('invalid-zero-grace',{'misfire_grace_time':0}),('invalid-max-instances',{'max_instances':0}),('paused-next-time',{'next_run_time':None}),('unknown-executor-drops-clock',{'executor':'missing'}),('constructed-trigger-ignores-extra',{'ignored_trigger_field':'owned'})]:
 calls=[];ex=RecordingExecutor();scheduler=ManualScheduler(timezone=timezone.utc,executors={'default':ex});scheduler.start(paused=True)
 error=None
 try:
  arguments={'next_run_time':anchor,**options}
  scheduler.add_job(lambda:calls.append('actual callable'),trigger=IntervalTrigger(seconds=1,start_date=anchor,timezone=timezone.utc),id='owned',**arguments)
  scheduler.state=STATE_RUNNING;scheduler._process_jobs();ex.finish()
 except Exception as e:error=type(e).__name__
 finally:scheduler.shutdown()
 cases.append({'name':name,'options':options,'calls':len(calls),'error':error})
from apscheduler.util import convert_to_datetime
from zoneinfo import ZoneInfo
next_run_cases=[]
for value in [None,'2026-1-2 3:4:5.1Z','2026-10-26','2026-02-30',False]:
 error=None;output=None
 try:
  result=convert_to_datetime(value,ZoneInfo('Asia/Shanghai'),'next_run_time');output=result.astimezone(timezone.utc).isoformat() if result is not None else None
 except Exception as e:error=type(e).__name__
 next_run_cases.append({'input':value,'output':output,'error':error})
print(json.dumps({'version':apscheduler.__version__,'kind':'actual APScheduler process_jobs + executor run_job; fixed local clock and owned callable, no thread/network','source_sha256':{'scheduler':hashlib.sha256(inspect.getsource(scheduler_base).encode()).hexdigest(),'executor':hashlib.sha256(inspect.getsource(executor_base).encode()).hexdigest()},'elapsed_seconds':3.5,'interval_ms':1000,'cases':cases,'next_run_cases':next_run_cases},indent=2))
