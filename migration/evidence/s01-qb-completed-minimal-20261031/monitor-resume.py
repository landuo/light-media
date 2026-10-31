from pathlib import Path
import datetime,json,os,shutil,signal,subprocess,sys,time
E=Path(__file__).resolve().parent
MODE='normal'
PLAN=json.loads((E/'normal-resume-plan.json').read_text());assert PLAN['state']=='frozen'
OUT=E/(PLAN['run_directory']+'-monitor');OUT.mkdir(exist_ok=False)
def record():return {'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'scratch_free':shutil.disk_usage(E).free,'tmp_free':shutil.disk_usage('/tmp').free}
def interrupted(signum,frame):raise InterruptedError('monitor signal '+str(signum))
signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
first=record();minimum=first.copy();reason=None;error=None;p=None;cleanup=[];exitcode=None
PLAN['scratch_growth_budget_bytes']=max(0,first['scratch_free']-(1<<30)) # same physical reserve, not a separate arbitrary cap
if first['scratch_free']<1<<30 or first['tmp_free']<3*(1<<29):raise RuntimeError('pre-start reserve not available')
try:
 with (OUT/'runner.log').open('xb') as output,(OUT/'resources.jsonl').open('x') as log:
  p=subprocess.Popen([sys.executable,str(E/'run-resume.py')],stdout=output,stderr=subprocess.STDOUT,start_new_session=True)
  (OUT/'runner.pid').write_text(str(p.pid)+'\n');print('runner_pid',p.pid,'log',str(OUT/'runner.log'),flush=True)
  while p.poll() is None:
   row=record();log.write(json.dumps(row)+'\n');log.flush();minimum['scratch_free']=min(minimum['scratch_free'],row['scratch_free']);minimum['tmp_free']=min(minimum['tmp_free'],row['tmp_free']);growth=first['scratch_free']-row['scratch_free']
   if row['scratch_free']<1<<30 or row['tmp_free']<3*(1<<29) or growth>PLAN['scratch_growth_budget_bytes']:
    reason='scratch_growth_budget' if growth>PLAN['scratch_growth_budget_bytes'] else 'reserved_free_space';break
   time.sleep(2)
except BaseException as exc:
 error={'type':type(exc).__name__,'message':str(exc)}
finally:
 # Cleanup is confined to the session we created. No foreign process scan,
 # service relaunch, environment rewrite or retry on an exception.
 signal.signal(signal.SIGTERM,signal.SIG_IGN);signal.signal(signal.SIGINT,signal.SIG_IGN)
 if p is not None:
  if p.poll() is None:
   try:os.killpg(p.pid,signal.SIGTERM);cleanup.append('owned_group_TERM')
   except ProcessLookupError:cleanup.append('owned_group_already_gone')
   try:p.wait(timeout=15)
   except subprocess.TimeoutExpired:
    try:os.killpg(p.pid,signal.SIGKILL);cleanup.append('owned_group_KILL')
    except ProcessLookupError:cleanup.append('owned_group_already_gone_before_KILL')
    p.wait()
  exitcode=p.wait()
  # The created process group may still contain an owned worker after the
  # runner exits. Never infer that its exit means the group is empty.
  def owned_group_exists():
   try:os.killpg(p.pid,0);return True
   except ProcessLookupError:return False
  if owned_group_exists():
   if reason is None:reason='owned_group_residual_after_runner_exit'
   try:os.killpg(p.pid,signal.SIGTERM);cleanup.append('residual_owned_group_TERM')
   except ProcessLookupError:cleanup.append('residual_group_already_gone')
   until=time.monotonic()+2
   while owned_group_exists() and time.monotonic()<until:time.sleep(0.05)
   if owned_group_exists():
    try:os.killpg(p.pid,signal.SIGKILL);cleanup.append('residual_owned_group_KILL')
    except ProcessLookupError:cleanup.append('residual_group_gone_before_KILL')
 final=record();minimum['scratch_free']=min(minimum['scratch_free'],final['scratch_free']);minimum['tmp_free']=min(minimum['tmp_free'],final['tmp_free']);growth=first['scratch_free']-final['scratch_free']
 if reason is None and growth>PLAN['scratch_growth_budget_bytes']:reason='terminal_scratch_growth_budget'
 if reason is None and (final['scratch_free']<1<<30 or final['tmp_free']<3*(1<<29)):reason='terminal_reserved_free_space'
 result={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit':exitcode,'limit_stop':reason,'interruption_or_error':error,'cleanup':cleanup,'first':first,'observed_minimum':minimum,'final':final,'sampling_seconds':2,'not_exact_instantaneous_peak':True,'signals_only_owned_pgid':p.pid if p else None,'process_enumeration':False,'rss_measured':False,'scratch_growth_budget_bytes':PLAN['scratch_growth_budget_bytes']}
 (OUT/'result.json').write_text(json.dumps(result,indent=2)+'\n');(OUT/'monitor.exit').write_text(str(0 if exitcode==0 and reason is None and error is None else 1)+'\n');print(json.dumps(result),flush=True)
sys.exit(0 if exitcode==0 and reason is None and error is None else 1)
