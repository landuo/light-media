"""Ordinary source rune/regex witnesses; no copied matcher or external services."""
import asyncio,hashlib,json,sys
from pathlib import Path
attempts=[]
def guard(event,args):
 if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:
  attempts.append(event);raise RuntimeError('offline owned source')
sys.addaudithook(guard)
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.core.meta.words import WordsMatcher
from app.schemas.types import SystemConfigKey
from starlette.responses import JSONResponse
SystemConfig.__table__.create(bind=Engine,checkfirst=True);dao=SystemConfigOper();matcher=WordsMatcher();records=[]
for case in json.loads(Path(sys.argv[1]).read_text()):
 rec={'case':case['case'],'input':case,'set_return':dao.set(SystemConfigKey.CustomIdentifiers,case['value'])}
 try:
  rec['prepared']=matcher.prepare(case['title'])
  try:rec['title_json']=JSONResponse(rec['prepared'][0]).body.decode()
  except Exception as e:rec['title_json_error']=type(e).__name__
  try:rec['prepared_json']=JSONResponse(rec['prepared']).body.decode()
  except Exception as e:rec['prepared_json_error']=type(e).__name__
 except Exception as e:rec['consumer_error']=type(e).__name__
 records.append(rec)
imported={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in tuple(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file() and '/source/MoviePilot/' in str(Path(m.__file__).resolve())}
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
Path(sys.argv[2]).write_text(json.dumps({'scope':'Unmodified default source DAO + normal WordsMatcher -> real JSONResponse; no source daemon or original authentication','observations':records,'network_attempts':attempts,'source_imports':imported},ensure_ascii=True,indent=2)+'\n')
