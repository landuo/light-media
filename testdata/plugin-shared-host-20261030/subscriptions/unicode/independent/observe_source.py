"""独立输入运行真实固定源 DAO、SQLite 提交与实际 JSONResponse。"""
import os,sys,json,asyncio,hashlib,math
from pathlib import Path
sys.path.insert(0,os.environ['SOURCE_BOOTSTRAP'])
from source_driver import Subscribe,Engine,ScopedSession,AsyncEngine,OwnedSubscriptionsPlugin,sql_snapshot,NETWORK_ATTEMPTS,imported_files
from starlette.responses import JSONResponse
phase,input_path,out_path=sys.argv[1:];out=Path(out_path)
if out.exists():raise FileExistsError(out)
cases=json.loads(Path(input_path).read_text());Subscribe.__table__.create(bind=Engine,checkfirst=True)
p=OwnedSubscriptionsPlugin();p.init_plugin()
def render(value):
 try:JSONResponse(value);return None
 except Exception as e:return type(e).__name__
def safe(v):
 if isinstance(v,float) and not math.isfinite(v):return {'$independent_nonfinite':repr(v)}
 if isinstance(v,list):return [safe(x) for x in v]
 if isinstance(v,dict):return {k:safe(x) for k,x in v.items()}
 return v
records=[]
for c in cases:
 sid=c['id'];raw=c['envelope'];incoming=json.loads(raw);r={'id':sid,'name':c['name'],'envelope_sha256':hashlib.sha256(raw.encode()).hexdigest()}
 if phase=='warm':
  Subscribe(id=sid,name='Owned S05',type='电视剧',tmdbid=7001,keyword='',state='N',sites=[],filter_groups=[]).create(None)
  if c['deferred_commit_fault']:
   with Engine.begin() as con:
    con.exec_driver_sql('PRAGMA foreign_keys=ON');assert con.exec_driver_sql('PRAGMA foreign_keys').scalar()==1
    con.exec_driver_sql('CREATE TABLE independent_parent(id INTEGER PRIMARY KEY)')
    con.exec_driver_sql('CREATE TABLE independent_guard(id INTEGER REFERENCES independent_parent(id) DEFERRABLE INITIALLY DEFERRED)')
    con.exec_driver_sql('CREATE TRIGGER independent_fault AFTER UPDATE ON subscribe WHEN NEW.id='+str(sid)+' BEGIN INSERT INTO independent_guard VALUES(999); END')
  try:
   m=incoming['method'].split('.')[-1];args=incoming['payload']
   if m=='update':p.update(sid=sid,payload=args['patch'])
   else:
    result=getattr(p,m)(**args);r['operation_response_error']=render([x.to_dict() for x in result])
   r['operation_error']=None
  except Exception as e:r['operation_error']={'class':type(e).__name__,'driver':type(getattr(e,'orig',None)).__name__}
  if c['deferred_commit_fault']:
   with Engine.begin() as con:con.exec_driver_sql('DROP TRIGGER independent_fault')
 elif phase=='repair':p.update(sid=sid,payload={'note':{'valid':'\ufffd','pair':'\U0001f680'},'sites':[],'filter_groups':[],'episode_priority':{'1':2},'keyword':'repaired'})
 row=p.get(sid=sid).to_dict();r['fresh']={k:row[k] for k in ['note','sites','filter_groups','episode_priority','keyword']};r['fresh_response_error']=render(row)
 records.append(r)
output={'phase':phase,'input_sha256':hashlib.sha256(Path(input_path).read_bytes()).hexdigest(),'records':records,'network_attempts':NETWORK_ATTEMPTS,'database':sql_snapshot(),'imported_files':imported_files()}
out.write_text(json.dumps(safe(output),ensure_ascii=True,indent=2,allow_nan=False)+'\n')
ScopedSession.remove();Engine.dispose();asyncio.run(AsyncEngine.dispose())
