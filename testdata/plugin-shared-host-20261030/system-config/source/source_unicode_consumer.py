"""Actual default DAO/WordsMatcher and response boundary; offline owned inputs."""
import asyncio,hashlib,json,os,sqlite3,sys
from pathlib import Path
attempts=[]
def guard(event,args):
 if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:attempts.append(event);raise RuntimeError('offline fixture')
sys.addaudithook(guard)
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.core.meta.words import WordsMatcher
from app.schemas.types import SystemConfigKey
from starlette.responses import JSONResponse
SystemConfig.__table__.create(bind=Engine,checkfirst=True)
oper=SystemConfigOper();matcher=WordsMatcher();key=SystemConfigKey.CustomIdentifiers
plan=json.loads(Path(sys.argv[1]).read_text());observations=[]
for case in plan:
 r={'case':case['case'],'input':case}
 try:r['set_return']=oper.set(key,case['rules'])
 except Exception as e:r['set_error']=type(e).__name__
 r['raw']=oper.get(key)
 try:
  r['prepared']=matcher.prepare(case['title'])
  try:r['title_response_body']=JSONResponse(r['prepared'][0]).body.decode('utf8')
  except Exception as e:r['title_response_error']=type(e).__name__
  try:r['response_body']=JSONResponse(r['prepared']).body.decode('utf8')
  except Exception as e:r['response_error']=type(e).__name__
 except Exception as e:r['consumer_error']=type(e).__name__
 with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as c:r['database']=c.execute('SELECT value FROM systemconfig WHERE key=?',(key.value,)).fetchall()
 observations.append(r)
loaded={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in tuple(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
Path(sys.argv[2]).write_text(json.dumps({'scope':'ordinary default SystemConfigOper -> normal WordsMatcher -> actual JSONResponse; no original HTTP authentication route','observations':observations,'network_attempts':attempts,'imported_files':loaded},ensure_ascii=True,indent=2)+'\n')
