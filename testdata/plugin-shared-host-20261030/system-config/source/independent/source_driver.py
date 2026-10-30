"""Independent owned plugin -> unmodified default DAO and WordsMatcher; no AST/stubs."""
import asyncio, hashlib, importlib.metadata, json, math, os, platform, sqlite3, sys
from pathlib import Path

NETWORK=[]
def guard(event,args):
    if event in {'socket.connect','socket.connect_ex','socket.getaddrinfo','socket.sendto'}:
        NETWORK.append(event)
        raise RuntimeError('offline owned component experiment')
sys.addaudithook(guard)
from app.db import Engine, AsyncEngine, ScopedSession
from app.db.models.systemconfig import SystemConfig
from app.db.systemconfig_oper import SystemConfigOper
from app.core.meta.words import WordsMatcher
from app.schemas.types import SystemConfigKey
from starlette.responses import JSONResponse

KEY=SystemConfigKey.CustomIdentifiers
class OrdinaryPlugin:
    def __init__(self): self.dao=SystemConfigOper()
    def get(self): return self.dao.get(KEY)
    def set(self,value): return self.dao.set(KEY,value)
    async def async_set(self,value): return await self.dao.async_set(KEY,value)
    def prepare(self,title): return WordsMatcher().prepare(title)

def observe(value):
    if isinstance(value,float) and not math.isfinite(value):return {'$python_float':repr(value)}
    if isinstance(value,dict):return {k:observe(v) for k,v in value.items()}
    if isinstance(value,(list,tuple)):return [observe(v) for v in value]
    return value
def err(exc):
    out={'class':type(exc).__name__,'module':type(exc).__module__}
    if getattr(exc,'orig',None) is not None:
        out.update(driver_class=type(exc.orig).__name__,sqlite_errorname=getattr(exc.orig,'sqlite_errorname',None))
    return out
def database():
    with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:
        return [dict(zip(('id','value','sqlite_type'),row)) for row in db.execute('SELECT id,value,typeof(value) FROM systemconfig WHERE key=? ORDER BY id',(KEY.value,))]
def state(plugin):
    value=plugin.get()
    result={'get':value,'python_type':type(value).__name__,'database':database()}
    if isinstance(value,dict):result['iteration_order']=list(value)
    try:result['raw_json']=JSONResponse(value).body.decode()
    except Exception as exc:result['raw_json_error']=err(exc)
    return result
async def run(plan):
    SystemConfig.__table__.create(bind=Engine,checkfirst=True)
    for value in plan.get('seed',[]):SystemConfig(key=KEY.value,value=value).create(None)
    plugin=OrdinaryPlugin()
    out={'initial':state(plugin),'observations':[]}
    for op in plan['operations']:
        rec={'case':op['case'],'method':op['method']}
        try:
            if 'sql' in op:
                with sqlite3.connect(Path(os.environ['CONFIG_DIR'])/'user.db') as db:db.executescript(op['sql'])
            method=op['method']
            if method=='set':rec['returned']=plugin.set(op['value'])
            elif method=='async_set':rec['returned']=await plugin.async_set(op['value'])
            elif method=='get':rec['returned']=plugin.get()
            elif method=='prepare':rec['returned']=plugin.prepare(op.get('title','A'))
            elif method=='mutate_return':
                val=plugin.get();val['injected => BAD']=1;rec['mutated_only_return']=val
            elif method=='mutate_input':
                val=op['value'];rec['returned']=plugin.set(val);val.append('A => BAD')
            else:raise ValueError('unknown experiment operation')
        except Exception as exc:rec['error']=err(exc)
        rec['state']=state(plugin);out['observations'].append(rec)
    return out
async def main():
    try:out=await run(json.loads(Path(sys.argv[1]).read_text()))
    except Exception as exc:out={'initialization_error':err(exc),'database':database()}
    out.update(scope='Owned ordinary plugin -> normal unmodified SystemConfigOper default sessions -> SQLite; normal WordsMatcher; raw JSONResponse probe is not original full HTTP endpoint',network_attempts=NETWORK,python=platform.python_version(),sqlite=sqlite3.sqlite_version,sqlalchemy=importlib.metadata.version('SQLAlchemy'))
    out['source_imports']={str(Path(m.__file__).resolve()):hashlib.sha256(Path(m.__file__).read_bytes()).hexdigest() for m in tuple(sys.modules.values()) if getattr(m,'__file__',None) and Path(m.__file__).is_file() and '/source/MoviePilot/' in str(Path(m.__file__).resolve())}
    ScopedSession.remove();Engine.dispose();await AsyncEngine.dispose()
    Path(sys.argv[2]).write_text(json.dumps(observe(out),ensure_ascii=False,indent=2,allow_nan=False)+'\n')
asyncio.run(main())
