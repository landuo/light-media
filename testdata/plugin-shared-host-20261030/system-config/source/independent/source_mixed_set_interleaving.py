"""Real DAO mixed sync/async scheduling witness; only DB event timing is held."""
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
SystemConfig.__table__.create(bind=Engine,checkfirst=True)
dao=SystemConfigOper();key=SystemConfigKey.CustomIdentifiers;dao.set(key,['A => DiskInitial'])
query_complete=threading.Event();release_query=threading.Event();observations={};captured=[]
def hold_actual_select(connection,cursor,statement,parameters,context,executemany):
 if statement.lstrip().upper().startswith('SELECT') and 'systemconfig' in statement:
  captured.append('actual asynchronous SELECT completed before returning DAO result')
  query_complete.set()
  if not release_query.wait(10):raise RuntimeError('owned timing window expired')
event.listen(AsyncEngine.sync_engine,'after_cursor_execute',hold_actual_select)
def asynchronous_ui_component():
 try:observations['async_result']=asyncio.run(dao.async_set(key,['A => DiskInitial']))
 except Exception as e:observations['async_error']=type(e).__name__
worker=threading.Thread(target=asynchronous_ui_component,name='owned-async-DAO')
worker.start()
if not query_complete.wait(10):raise RuntimeError('actual source async SELECT was not reached')
try:
 observations['sync_result']=dao.set(key,['A => PluginNew'])
 observations['between_sync_and_async_publish']=dao.get(key)
finally:release_query.set()
worker.join(10)
if worker.is_alive():raise RuntimeError('owned source worker did not finish')
event.remove(AsyncEngine.sync_engine,'after_cursor_execute',hold_actual_select)
observations['final_hot']=dao.get(key)
with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:observations['final_database']=db.execute('SELECT value FROM systemconfig WHERE key=?',(key.value,)).fetchall()
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
Path(sys.argv[1]).write_text(json.dumps({'scope':'Normal default original DAO. SQLAlchemy after_cursor_execute event only pauses the real async SELECT after execution; no mocked query, result, or DAO implementation. No full HTTP endpoint.','observations':observations,'timing':captured,'network_attempts':attempts},indent=2)+'\n')
