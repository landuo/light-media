"""Independent ordinary RemoteIdentifiers callback / source async UI observations.
Original complete module imported; unrelated Base construction not executed.
All actual DAO/SQLite and WordsMatcher. Observation hook controls timing only.
"""
import asyncio,json,os,sqlite3,sys,threading
from pathlib import Path
attempts=[]
def guard(e,args):
 if e in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:attempts.append(e);raise RuntimeError('owned source has no network')
sys.addaudithook(guard)
import app.helper
app.helper.__path__.append(os.environ['OWNED_SOURCE_HELPERS'])
import app.plugins
app.plugins.__path__.append(os.environ['OWNED_INSTALLED_PLUGINS'])
from app.plugins.remoteidentifiers import RemoteIdentifiers
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.schemas.types import SystemConfigKey
from app.core.meta.words import WordsMatcher
from sqlalchemy import event
SystemConfig.__table__.create(bind=Engine,checkfirst=True)
p=object.__new__(RemoteIdentifiers);p.init_plugin({'enable':True,'onlyonce':False,'file_urls':'','flitter':True,'cron':'30 4 * * *'});callback=p.get_service()[0]['func'];dao=p.systemconfig;key=SystemConfigKey.CustomIdentifiers;mode=sys.argv[1]
def snapshot():
 with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:rows=[{'id':x[0],'value':json.loads(x[1])} for x in db.execute('SELECT id,value FROM systemconfig WHERE key=? ORDER BY id',(key.value,))]
 try:prepared=WordsMatcher().prepare('A')
 except Exception as e:prepared={'error':type(e).__name__}
 return {'get':dao.get(key),'rows':rows,'prepare':prepared}
out={'mode':mode,'scope':__doc__,'observations':{}}
if mode=='cold':out['observations']['cold']=snapshot()
elif mode=='unicode-remote':
 value=['A => \ud800','\ud800 => B'];out['observations']['ui_result']=asyncio.run(dao.async_set(key,value));out['observations']['after_ui']=snapshot();out['observations']['remote_return']=callback();out['observations']['after_remote']=snapshot()
else:
 if mode not in ['existing-empty-ui','absent-empty-ui']:raise ValueError(mode)
 if mode=='existing-empty-ui':asyncio.run(dao.async_set(key,['A => B']))
 out['observations']['initial']=snapshot();selected=threading.Event();resume=threading.Event();held=[]
 def hold(conn,cursor,statement,params,context,executemany):
  if not held and statement.lstrip().upper().startswith('SELECT') and 'systemconfig' in statement:
   held.append('actual original SELECT completed');selected.set()
   if not resume.wait(20):raise RuntimeError('timing window expired')
 event.listen(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 async def ui():
  try:out['observations']['ui_result']=await dao.async_set(key,None)
  except Exception as e:out['observations']['ui_error']=type(e).__name__
 thread=threading.Thread(target=lambda:asyncio.run(ui()));thread.start()
 if not selected.wait(10):raise RuntimeError('actual async SELECT not reached')
 try:out['observations']['remote_return']=callback();out['observations']['before_ui_resume']=snapshot()
 finally:resume.set()
 thread.join(20)
 if thread.is_alive():raise RuntimeError('source did not finish')
 event.remove(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 out['observations']['after_ui']=snapshot();out['observations']['next_remote_return']=callback();out['observations']['after_next_remote']=snapshot()
 try:out['observations']['next_ui_result']=asyncio.run(dao.async_set(key,['A => Later']))
 except Exception as e:out['observations']['next_ui_error']=type(e).__name__
 out['observations']['after_next_ui']=snapshot();out['timing']=held
p.stop_service();ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose());out['network_attempts']=attempts
Path(sys.argv[2]).write_text(json.dumps(out,ensure_ascii=True,indent=2)+'\n')
