"""Fresh real source DAO + SQLite + Starlette JSONResponse Unicode boundaries."""
import os,sys,json,asyncio,hashlib,importlib.metadata,platform,sqlite3
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "source"))
from source_driver import Subscribe,Engine,ScopedSession,AsyncEngine,OwnedSubscriptionsPlugin,sql_snapshot,NETWORK_ATTEMPTS,imported_files
from starlette.responses import JSONResponse

phase=sys.argv[1];out=Path(sys.argv[2]);
if phase not in {"warm", "cold", "recover"}: raise ValueError("phase must be warm/cold/recover")
if out.exists(): raise FileExistsError(out)
Subscribe.__table__.create(bind=Engine,checkfirst=True)
p=OwnedSubscriptionsPlugin();p.init_plugin()
columns=['note','sites','filter_groups','episode_priority']
raw=r'{"value":"\ud800","\udc00":"low-key","replacement":"\ufffd","pair":"\ud83d\ude42","nested":["\ud801",{"\udfff":5}],"big":1208925819614629174706176,"negative_zero":-0.0,"float":1.25e1}'
duplicate=r'{"\ud800":1,"\ud800":2,"\ufffd":3,"x":1,"x":2,"\ud83d\ude42":4,"\ud83d\ude42":5}'
plans=[(i+1,c,raw) for i,c in enumerate(columns)]+[(5,'note',duplicate),(6,'note',r'{"bad":"\ud800","overflow":1e999}')]
records=[]
def safe(value):
 import math
 if isinstance(value,float) and not math.isfinite(value):return {'$observation_nonfinite':repr(value)}
 if isinstance(value,dict):return {key:safe(item) for key,item in value.items()}
 if isinstance(value,list):return [safe(item) for item in value]
 return value

def response(value):
 try:
  r=JSONResponse(value)
  return {'status':'encoded','body_utf8':r.body.decode('utf-8')}
 except Exception as e:
  return {'status':'error','class':type(e).__name__,'module':type(e).__module__}

for sid,column,rawvalue in plans:
 rec={'id':sid,'column':column,'phase':phase}
 if phase=='warm':
  Subscribe(id=sid,name='Owned Unicode '+str(sid),type='电视剧',tmdbid=1000+sid).create(None)
  payload=json.loads('{"'+column+'":'+rawvalue+',"keyword":"committed-'+str(sid)+'"}')
  rec['input_raw']=rawvalue
  returned=p.update(sid=sid,payload=payload)
  try:rec['update_return']=returned.to_dict()
  except Exception as e:rec['update_return_error']={'class':type(e).__name__,'module':type(e).__module__}
 elif phase=='recover':
  p.update(sid=sid,payload={column:{'valid':'\ufffd','emoji':'\U0001f642'},'keyword':'recovered-'+str(sid)})
 row=p.get(sid=sid).to_dict();rec['get']=row
 rec['get_response']=response(row)
 rec['list_by_tmdbid']=[r.to_dict() for r in p.list_by_tmdbid(tmdbid=1000+sid)]
 rec['tmdb_response']=response(rec['list_by_tmdbid'])
 records.append(rec)
binding=[]
if phase=='warm':
 for sid,kind in [(101,'text'),(102,'mixed-json-text'),(103,'unknown-transient')]:
  Subscribe(id=sid,name='Owned bind '+str(sid),type='电视剧',tmdbid=sid,keyword='prior').create(None)
  if kind=='text':payload={'keyword':'\ud800'}
  elif kind=='mixed-json-text':payload={'keyword':'\ud800','note':{'bad':'\udc00'}}
  else:payload={'unknown\ud800':'\udc00','keyword':'ordinary-committed'}
  rec={'kind':kind,'id':sid}
  try:p.update(sid=sid,payload=payload);rec['update']='returned'
  except Exception as e:rec['error']={'class':type(e).__name__,'module':type(e).__module__,'driver_class':type(getattr(e,'orig',None)).__name__}
  rec['fresh']=p.get(sid=sid).to_dict();binding.append(rec)
 for method,args in [('list',{'state':'\ud800'}),('list_by_tmdbid',{'tmdbid':'\ud800'}),('list_by_tmdbid',{'tmdbid':101,'season':'\udc00'})]:
  rec={'method':method,'arguments':args}
  try:rec['rows']=[r.to_dict() for r in getattr(p,method)(**args)]
  except Exception as e:rec['error']={'class':type(e).__name__,'module':type(e).__module__,'driver_class':type(getattr(e,'orig',None)).__name__}
  binding.append(rec)
all_rows=[r.to_dict() for r in p.list()]
result={'binding':binding,'phase':phase,'records':records,'list_response':response(all_rows),'database':sql_snapshot(),'network_attempts':NETWORK_ATTEMPTS,'environment':{'python':platform.python_version(),'sqlite':sqlite3.sqlite_version,**{k:importlib.metadata.version(k) for k in ['SQLAlchemy','starlette','pydantic']}},'boundary':'real DAO commits and cold-reloads Python codepoints; actual installed Starlette JSONResponse renders using its original UTF-8 encoding path, no monkeypatch'}
out.write_text(json.dumps(safe(result),ensure_ascii=True,indent=2,allow_nan=False)+'\n')
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
