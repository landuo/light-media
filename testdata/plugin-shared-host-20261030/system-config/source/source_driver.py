"""Offline ordinary instance -> normally imported fixed DAO and WordsMatcher."""
import asyncio,hashlib,importlib.metadata,json,math,os,platform,sqlite3,sys
from pathlib import Path
NETWORK_ATTEMPTS=[]
def guard(event,args):
    if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:
        NETWORK_ATTEMPTS.append(event)
        raise RuntimeError('owned source observation prohibits network')
sys.addaudithook(guard)
from app.db import Engine,AsyncEngine,ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.core.meta.words import WordsMatcher
from app.schemas.types import SystemConfigKey
KEY=SystemConfigKey.CustomIdentifiers
class OwnedConfigPlugin:
    def __init__(self):self.systemconfig=SystemConfigOper()
    def get(self):return self.systemconfig.get(KEY)
    def set(self,value):return self.systemconfig.set(KEY,value)
    def prepare(self,title):return WordsMatcher().prepare(title)
def observed(value):
    if isinstance(value,float) and not math.isfinite(value):return {'$source_python_float':repr(value)}
    if isinstance(value,dict):return {k:observed(v) for k,v in value.items()}
    if isinstance(value,(tuple,list)):return [observed(v) for v in value]
    return value
def snapshot():
    with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as c:
        return [dict(zip(('id','key','value_sql','sqlite_type'),row)) for row in c.execute('SELECT id,key,value,typeof(value) FROM systemconfig WHERE key=? ORDER BY id',(KEY.value,))]
def error(e):
    o={'class':type(e).__name__,'module':type(e).__module__}
    if getattr(e,'orig',None) is not None:o['driver_class']=type(e.orig).__name__;o['sqlite_errorname']=getattr(e.orig,'sqlite_errorname',None)
    return o
def imported():
    return {str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in tuple(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file()}
async def run(plan):
    SystemConfig.__table__.create(bind=Engine,checkfirst=True)
    for v in plan.get('seed',[]):SystemConfig(key=KEY.value,value=v).create(None)
    plugin=OwnedConfigPlugin();initial={'get':plugin.get(),'database':snapshot()};records=[]
    for op in plan['operations']:
        r={'case':op['case'],'method':op['method']}
        if 'fixture_sql' in op:
            with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as c:c.executescript(op['fixture_sql'])
        try:
            method=op['method']
            if method=='set':result=plugin.set(op['value'])
            elif method=='get':result=plugin.get()
            elif method=='prepare':result=plugin.prepare(op['title'])
            elif method=='async_set':result=await plugin.systemconfig.async_set(KEY,op['value'])
            elif method=='ui_set':
                value=op['value']
                if isinstance(value,list):value=list(filter(None,value));value=value if value else None
                result=await plugin.systemconfig.async_set(KEY,value)
            elif method=='delete':result=plugin.systemconfig.delete(KEY)
            elif method=='deepcopy_get':
                result=plugin.get();result.append('mutated external result')
            elif method=='deepcopy_set':
                value=op['value'];result=plugin.set(value);value.append('mutated external request')
            else:raise ValueError(method)
            r['returned']=result
        except Exception as e:r['error']=error(e)
        r['get']=plugin.get();r['database']=snapshot();records.append(r)
    return {'scope':'owned ordinary plugin -> default SystemConfigOper -> actual SQLAlchemy SQLite; normal WordsMatcher consumer; ui_set is endpoint normalization plus actual async_set, not whole HTTP route','python':platform.python_version(),'sqlite':sqlite3.sqlite_version,'sqlalchemy':importlib.metadata.version('SQLAlchemy'),'initial':initial,'observations':records,'network_attempts':NETWORK_ATTEMPTS,'imported_files':imported()}
async def main():
    result=await run(json.loads(Path(sys.argv[1]).read_text()))
    ScopedSession.remove();Engine.dispose();await AsyncEngine.dispose()
    Path(sys.argv[2]).write_text(json.dumps(observed(result),ensure_ascii=False,indent=2,allow_nan=False)+'\n')
asyncio.run(main())
