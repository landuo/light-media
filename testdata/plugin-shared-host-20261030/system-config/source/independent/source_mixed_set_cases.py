"""Default source DAO concurrency edges over real SQLite, with timing observation only."""
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
 with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:return [list(r) for r in db.execute('SELECT id,key,value FROM systemconfig WHERE key=? ORDER BY id',(key.value,))]
def error(e):return {'class':type(e).__name__,'module':type(e).__module__}
out={'mode':mode,'observations':{}}
if mode=='cold':
 out['observations']['cold_get']=dao.get(key);out['observations']['rows']=rows()
 try:out['observations']['async_after_cold']=asyncio.run(dao.async_set(key,['D']))
 except Exception as e:out['observations']['async_after_cold_error']=error(e)
else:
 if mode!='absent-insert':dao.set(key,['A'])
 selected=threading.Event();resume=threading.Event();held=[]
 def hold(connection,cursor,statement,parameters,context,executemany):
  if not held and statement.lstrip().upper().startswith('SELECT') and 'systemconfig' in statement:
   held.append('actual async SELECT completed');selected.set()
   if not resume.wait(10):raise RuntimeError('owned timing window expired')
 event.listen(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 def async_side():
  try:out['observations']['async_result']=asyncio.run(dao.async_set(key,['C'] if mode!='absent-insert' else ['A']))
  except Exception as e:out['observations']['async_error']=error(e)
 worker=threading.Thread(target=async_side,name='owned-source-async');worker.start()
 if not selected.wait(10):raise RuntimeError('source SELECT not observed')
 try:
  if mode=='absent-insert':out['observations']['sync_insert']=dao.set(key,['B'])
  else:
   out['observations']['sync_delete']=dao.set(key,None)
   if mode=='delete-recreate':out['observations']['sync_recreate']=dao.set(key,['B'])
  out['observations']['before_async_resume']={'get':dao.get(key),'rows':rows()}
 finally:resume.set()
 worker.join(10)
 if worker.is_alive():raise RuntimeError('source async worker not finished')
 event.remove(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 out['observations']['after_async']={'get':dao.get(key),'rows':rows()}
 try:out['observations']['next_async_set']=asyncio.run(dao.async_set(key,['D']))
 except Exception as e:out['observations']['next_async_set_error']=error(e)
 out['observations']['after_followup']={'get':dao.get(key),'rows':rows()};out['timing']=held
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose());out['network_attempts']=attempts
out['scope']='Unmodified source default synchronous/asynchronous DAO, real SQLite, event hook only holds actual SELECT completion; not a mocked query/DAO/result or full authenticated UI.'
Path(sys.argv[2]).write_text(json.dumps(out,indent=2)+'\n')
