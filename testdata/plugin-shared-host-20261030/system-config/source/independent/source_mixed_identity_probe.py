"""Unmodified default DAO + real SQLite; hooks control timing, not query/results."""
import asyncio,json,os,sqlite3,sys,threading
from pathlib import Path
attempts=[]
def guard(event,args):
 if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:
  attempts.append(event);raise RuntimeError('offline owned source')
sys.addaudithook(guard)
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.schemas.types import SystemConfigKey
from sqlalchemy import event
mode=sys.argv[1];key=SystemConfigKey.CustomIdentifiers
SystemConfig.__table__.create(bind=Engine,checkfirst=True);dao=SystemConfigOper()
def rows():
 with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:return [list(r) for r in db.execute('SELECT id,key,value FROM systemconfig ORDER BY id')]
def error(e):return {'class':type(e).__name__,'module':type(e).__module__}
out={'mode':mode,'observations':{}}
if mode=='cold':
 out['observations']['cold_get']=dao.get(key);out['observations']['rows']=rows()
else:
 if mode=='other-high-id':
  dao.set(key,['A']);SystemConfig(id=100,key='OwnedOtherOrdinary',value=['synthetic']).create(None)
 elif mode!='two-queued':raise ValueError('unknown owned case')
 out['observations']['initial']={'get':dao.get(key),'rows':rows()}
 selected=threading.Event();resume=threading.Event();held=[];second_started=threading.Event()
 def hold(connection,cursor,statement,parameters,context,executemany):
  if not held and statement.lstrip().upper().startswith('SELECT') and 'systemconfig' in statement:
   held.append('actual async SELECT completed');selected.set()
   if not resume.wait(20):raise RuntimeError('owned timing window expired')
 event.listen(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 async def setter(label,value):
  if label=='second':second_started.set()
  try:out['observations'][label+'_result']=await dao.async_set(key,value)
  except Exception as e:out['observations'][label+'_error']=error(e)
 async def sequence():
  if mode=='two-queued':await asyncio.gather(setter('first',['A']),setter('second',['C']))
  else:await setter('first',['C'])
 def side():asyncio.run(sequence())
 worker=threading.Thread(target=side,name='owned-real-source-async');worker.start()
 if not selected.wait(10):raise RuntimeError('source SELECT not observed')
 try:
  if mode=='other-high-id':
   out['observations']['sync_delete']=dao.set(key,None)
   out['observations']['sync_recreate']=dao.set(key,['B'])
  else:
   out['observations']['second_async_entered_before_release']=second_started.is_set()
   if not second_started.is_set():raise RuntimeError('second async did not enter original lock')
   out['observations']['sync_insert']=dao.set(key,['B'])
  out['observations']['before_async_resume']={'get':dao.get(key),'rows':rows()}
 finally:resume.set()
 worker.join(20)
 if worker.is_alive():raise RuntimeError('source async worker not finished')
 event.remove(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 out['observations']['after_async']={'get':dao.get(key),'rows':rows()};out['timing']=held
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose());out['network_attempts']=attempts
out['scope']='Normal default source SystemConfigOper and actual SQLAlchemy SQLite. Other key is owned synthetic ORM seed, not a new SDK key. Timing hook holds actual SELECT completion only. Two-row result is NOT a schema-wide upper bound.'
Path(sys.argv[2]).write_text(json.dumps(out,indent=2)+'\n')
