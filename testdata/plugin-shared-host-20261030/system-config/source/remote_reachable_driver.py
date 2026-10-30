"""Fixed normally imported installed producer -> default DAO -> WordsMatcher.
Explicit component boundary: __new__ avoids unrelated Base/ModuleManager startup.
UI value is the statically bound endpoint-normalized value; original async_set is
executed, not the whole endpoint/Depends/AI-importing scheduler. No network.
"""
import asyncio,hashlib,importlib,json,os,sqlite3,sys,threading
from pathlib import Path
attempts=[]
def guard(event,args):
 if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:
  attempts.append(event);raise RuntimeError('owned source observation prohibits network')
sys.addaudithook(guard)
import app.helper
app.helper.__path__.append(os.environ['OWNED_SOURCE_HELPERS'])
import app.plugins
app.plugins.__path__.append(os.environ['OWNED_INSTALLED_PLUGINS'])
from app.plugins.remoteidentifiers import RemoteIdentifiers
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.core.meta.words import WordsMatcher
from app.schemas.types import SystemConfigKey
from sqlalchemy import event
KEY=SystemConfigKey.CustomIdentifiers
SystemConfig.__table__.create(bind=Engine,checkfirst=True)
plugin=object.__new__(RemoteIdentifiers)
plugin.init_plugin({'enable':True,'onlyonce':False,'file_urls':'','flitter':True,'cron':'30 4 * * *'})
service=plugin.get_service()[0]
dao=plugin.systemconfig
marker='#========以下识别词由 RemoteIdentifiers 插件添加========#'
def snapshot():
 with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:
  rows=[{'id':v[0],'key':v[1],'value':json.loads(v[2])} for v in db.execute('SELECT id,key,value FROM systemconfig ORDER BY id')]
 return {'get':dao.get(KEY),'rows':rows,'prepare':WordsMatcher().prepare('A')}
def error(e):return {'class':type(e).__name__,'module':type(e).__module__}
mode=sys.argv[1]
out={'schema':1,'mode':mode,'scope':__doc__,'service':{'id':service['id'],'callback':service['func'].__name__},'observations':{}}
if mode=='cold':out['observations']['cold']=snapshot()
elif mode=='ordinary':
 out['cases']=[]
 for value in [None,['A => B'],['A => B',marker,'B => C']]:
  dao.set(KEY,value);before=snapshot();returned=service['func']()
  out['cases'].append({'initial':value,'before':before,'service_returned':returned,'after':snapshot()})
 out['ui_input']=['A => User']
 out['observations']['ui_result']=asyncio.run(dao.async_set(KEY,out['ui_input']))
 out['observations']['after_ui']=snapshot()
 service['func']();out['observations']['after_service']=snapshot()
else:
 if mode not in {'equal','absent'}:raise ValueError(mode)
 value=['A => B',marker,'B => C'] if mode=='equal' else ['A => User']
 if mode=='equal':dao.set(KEY,value)
 out['ui_input']=value;out['observations']['initial']=snapshot()
 selected=threading.Event();resume=threading.Event();held=[]
 def hold(connection,cursor,statement,parameters,context,executemany):
  if not held and statement.lstrip().upper().startswith('SELECT') and 'systemconfig' in statement:
   held.append('original async SELECT completed');selected.set()
   if not resume.wait(20):raise RuntimeError('owned timing release expired')
 event.listen(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 async def setter():
  try:out['observations']['ui_result']=await dao.async_set(KEY,value)
  except Exception as e:out['observations']['ui_error']=error(e)
 worker=threading.Thread(target=lambda:asyncio.run(setter()),name='owned-source-ui-async');worker.start()
 if not selected.wait(10):raise RuntimeError('actual async SELECT not reached')
 try:
  out['observations']['service_returned']=service['func']()
  out['observations']['before_ui_resume']=snapshot()
 finally:resume.set()
 worker.join(20)
 if worker.is_alive():raise RuntimeError('source UI did not finish')
 event.remove(AsyncEngine.sync_engine,'after_cursor_execute',hold)
 out['observations']['after_ui']=snapshot();out['timing']=held
 if mode=='absent':
  try:out['observations']['next_ui_result']=asyncio.run(dao.async_set(KEY,['A => Next']))
  except Exception as e:out['observations']['next_ui_error']=error(e)
plugin.stop_service();ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
out['network_attempts']=attempts
out['imported_files']={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in tuple(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
Path(sys.argv[2]).write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
