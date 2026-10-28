"""Actual fixed APScheduler executor admission across same-ID replacement."""
import hashlib,inspect,json,logging,threading
from datetime import datetime,timezone
import apscheduler,apscheduler.schedulers.base as scheduler_base
from apscheduler.schedulers.base import BaseScheduler,STATE_RUNNING
from apscheduler.executors.pool import ThreadPoolExecutor
from apscheduler.triggers.date import DateTrigger
logging.disable(logging.CRITICAL)
fixed=datetime(2026,10,26,tzinfo=timezone.utc)
class FrozenDateTime(datetime):
 @classmethod
 def now(cls,tz=None):return fixed
scheduler_base.datetime=FrozenDateTime
class ManualScheduler(BaseScheduler):
 def wakeup(self):pass
 def shutdown(self,wait=True):super().shutdown(wait)
cases=[]
for maximum in [1,2]:
 old_reached,new_reached,old_release,new_release=[threading.Event() for _ in range(4)]
 calls={'old':0,'new':0}
 def old():calls['old']+=1;old_reached.set();old_release.wait(10)
 def new():calls['new']+=1;new_reached.set();new_release.wait(10)
 scheduler=ManualScheduler(timezone=timezone.utc,executors={'default':ThreadPoolExecutor(2)})
 scheduler.start(paused=True)
 try:
  scheduler.add_job(old,trigger=DateTrigger(run_date=fixed,timezone=timezone.utc),id='owned',next_run_time=fixed,misfire_grace_time=None)
  scheduler.state=STATE_RUNNING;scheduler._process_jobs();assert old_reached.wait(5)
  # The date timer is gone, but its actual executor instance is still active.
  scheduler.add_job(new,trigger=DateTrigger(run_date=fixed,timezone=timezone.utc),id='owned',next_run_time=fixed,misfire_grace_time=None,max_instances=maximum)
  scheduler._process_jobs()
  if maximum==2:assert new_reached.wait(5)
  observed={'old':old_reached.is_set(),'new':new_reached.is_set(),'active':scheduler._lookup_executor('default')._instances['owned']}
 finally:
  old_release.set();new_release.set();scheduler.shutdown()
 cases.append({'maximum':maximum,'observed_while_old_held':observed,'calls':calls})
print(json.dumps({'version':apscheduler.__version__,'kind':'actual APScheduler ManualScheduler + real ThreadPoolExecutor held callables, same timer ID replaced while old executor instance lives; no original/user service','source_sha256':{'scheduler':hashlib.sha256(inspect.getsource(scheduler_base).encode()).hexdigest()},'cases':cases},indent=2))
