"""独立 23/24 源观察：完整模块、真实 SQLite 和普通 owned HTTP peer。"""
import asyncio,hashlib,importlib.metadata,json,os,socket,sys,threading,types
from pathlib import Path
from contextlib import asynccontextmanager
import argparse,subprocess,tempfile
HERE=Path(__file__).resolve().parent
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source-root',required=True,type=Path)
parser.add_argument('--dependency-root',required=True,type=Path,action='append')
parser.add_argument('--output',required=True,type=Path)
args=parser.parse_args()
SRC=args.source_root.resolve(strict=True);OUT=args.output.resolve();OUT.mkdir(parents=True,exist_ok=False)
assert subprocess.check_output(['git','-C',str(SRC),'rev-parse','HEAD'],text=True).strip()=='8a83efeacd3e26b82be495e17d50146e31c96a60'
subprocess.run(['git','-C',str(SRC),'diff','--quiet','HEAD','--'],check=True)
for name,expected in json.loads((HERE/'source-query-bindings.json').read_text())['files'].items():
    assert hashlib.sha256((SRC/name).read_bytes()).hexdigest()==expected,'fixed source changed: '+name
owned=tempfile.TemporaryDirectory(prefix='runtime-',dir=OUT)
ROOT=Path(owned.name);(ROOT/'app.env').write_text('')
sys.dont_write_bytecode=True
sys.path[:0]=[str(p.resolve(strict=True)) for p in args.dependency_root]+[str(SRC)]
os.environ.clear();os.environ.update(HOME=str(ROOT),CONFIG_DIR=str(ROOT),PATH='/usr/bin:/bin',API_TOKEN='owned-rating-independent-synthetic',PYTHONDONTWRITEBYTECODE='1',TZ='UTC',PLUGIN_AUTO_RELOAD='false',DEV='false',USAGE_STATISTIC_SHARE='false',PLUGIN_STATISTIC_SHARE='true',MP_SERVER_HOST='',CACHE_BACKEND_TYPE='memory',LANGSMITH_TRACING='false')
os.chdir(ROOT)
allowed_ports=set();rejections=[]
def network_audit(event,args):
    """仅准许本进程普通 HTTP peer，禁止外部访问。"""
    if event=='socket.connect':
        address=args[1]
        if not isinstance(address,tuple) or address[0]!='127.0.0.1' or address[1] not in allowed_ports:
            rejections.append(event);raise RuntimeError('NON_OWNED_NETWORK_REJECTED')
    if event in ('socket.getaddrinfo','socket.gethostbyname') and args[0] not in ('127.0.0.1',b'127.0.0.1'):
        rejections.append(event);raise RuntimeError('NON_OWNED_DNS_REJECTED')
sys.addaudithook(network_audit)
fixtures=[]
def stub(name,reason,**attrs):
    """仅隔离未调用集成，不替代统计或评分实现。"""
    m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m;fixtures.append({'module':name,'reason':reason});return m
class Unused:
    """实例化或访问范围外集成均令夹具失败。"""
    def __init__(self,*a,**k):raise RuntimeError('UNUSED_INTEGRATION_CALLED')
@asynccontextmanager
async def no_daemon(app):
    """不启动业务模块、外部集成或定时器。"""
    yield
stub('app.startup.lifecycle','daemon lifecycle outside slice',lifespan=no_daemon)
stub('app.helper.sites','external official resource absent and excluded; constructor raises',SitesHelper=Unused)
stub('app.chain','not executing business plugins',ChainBase=Unused)
stub('app.helper.message','no user message or business plugin',MessageHelper=Unused)
stub('app.command','install/start registration outside API rating witness',Command=Unused)
stub('app.scheduler','no scheduled business plugins',Scheduler=Unused)
from app.core.config import settings,global_vars
from app.db import Base,Engine
from app.db import models
Base.metadata.create_all(Engine)
from app.factory import app
from app.api.endpoints import plugin as endpoint
from app.helper.server import MoviePilotServerHelper as H
from app.db.systemconfig_oper import SystemConfigOper
from app.schemas.types import SystemConfigKey as K
from app.db.user_oper import get_current_active_superuser,get_current_active_superuser_async
from app.core.security import verify_token
from app.utils.http import aclose_shared_async_transports
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
import httpx
state={'body':'{}','status':200};wire=[];uid_seen=[]
INVALID_REQUEST=b'{"rating":4.1,"extra":"\xff"}'
INVALID_PEER=b'{"plugin_id":"Owned","average_rating":4.2,"extra":"\xff"}'
REQUEST_BYTES={'__INVALID_UTF8_REQUEST__':INVALID_REQUEST}
PEER_BYTES={'__INVALID_UTF8_PEER__':INVALID_PEER}
REQUEST_BYTES['__WTF8_REQUEST__']=b'{"rating":4.1,"extra":"\xed\xa0\x80"}'
PEER_BYTES['__WTF8_PEER__']=b'{"plugin_id":"Owned","average_rating":4.2,"extra":"\xed\xa0\x80"}'
for encoding,bom in [('utf-8',b'\xef\xbb\xbf'),('utf-16-le',b''),('utf-16-be',b''),('utf-32-le',b''),('utf-32-be',b''),('utf-16-le',b'\xff\xfe'),('utf-16-be',b'\xfe\xff'),('utf-32-le',b'\xff\xfe\x00\x00'),('utf-32-be',b'\x00\x00\xfe\xff')]:
    suffix=encoding+('-bom' if bom else '-no-bom')
    REQUEST_BYTES['__REQUEST_'+suffix+'__']=bom+'{"rating":4.1}'.encode(encoding)
    PEER_BYTES['__PEER_'+suffix+'__']=bom+'{"plugin_id":"Owned","average_rating":4.2}'.encode(encoding)
def peer_bytes():
    return PEER_BYTES.get(state['body'],state['body'].encode())
class Peer(BaseHTTPRequestHandler):
    """只返回选择的静态合成响应，不代理、不转发。"""
    def do_GET(self):self.respond()
    def do_POST(self):self.respond()
    def respond(self):
        body=self.rfile.read(int(self.headers.get('Content-Length','0')))
        uid=self.headers.get('X-MoviePilot-User-Uid');uid_seen.append(uid)
        row={'method':self.command,'path':self.path,'body':json.loads(body) if body else None,'uid_present':bool(uid),'uid_length':len(uid or ''),'user_agent_equal':self.headers.get('User-Agent')==settings.USER_AGENT,'content_type':self.headers.get('Content-Type')}
        wire.append(row);self.send_response(state['status']);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(peer_bytes())));self.end_headers();self.wfile.write(peer_bytes())
    def log_message(self,*a):pass
peer=ThreadingHTTPServer(('127.0.0.1',0),Peer);allowed_ports.add(peer.server_port)
thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start()
settings.MP_SERVER_HOST=f'http://127.0.0.1:{peer.server_port}/'
app.include_router(endpoint.router,prefix='/api/v1/plugin')
app.include_router(endpoint.router,prefix='/api/v2/plugin')
async def owned_authorized_subject():
    """仅替代已声明排除的原鉴权 dependency，不替代 UID 或业务。"""
    return None
for dep in (get_current_active_superuser,get_current_active_superuser_async,verify_token):app.dependency_overrides[dep]=owned_authorized_subject
config=SystemConfigOper();config.set(K.UserInstalledPlugins,['Owned','space +中'])
observations=[]
def record(name,result,start,**extra):
    """保存每个调用和真实 peer 请求。"""
    observations.append({'case':name,'result':result,'wire':wire[start:],**extra})
async def run_api():
    """已知 Starlette 查询/尾斜杠共因的真实入口定向复核。"""
    scopes=[]
    async def observed_app(scope,receive,send):
        """仅记录实际 ASGI 原始路径/查询，不改请求或源应用。"""
        if scope['type']=='http':scopes.append({'path':scope['path'],'raw_path_hex':scope.get('raw_path',b'').hex(),'query_hex':scope['query_string'].hex()})
        await app(scope,receive,send)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=observed_app,raise_app_exceptions=False),base_url='http://owned-asgi',follow_redirects=False) as c:
        cases=[]
        queries=[('absent',''),('empty','?plugin_ids='),('no-equals','?plugin_ids'),('duplicate-last','?plugin_ids=First&plugin_ids=Last'),('duplicate-empty-last','?plugin_ids=First&plugin_ids='),('duplicate-empty-first','?plugin_ids=&plugin_ids=Last'),('semicolon-value','?plugin_ids=A;B'),('semicolon-last','?plugin_ids=First&plugin_ids=A;B'),('semicolon-other','?other=A;B&plugin_ids=Last'),('semicolon-key-separator','?other=A;plugin_ids=Last'),('encoded-semicolon','?plugin_ids=A%3BB'),('percent-lone','?plugin_ids=%'),('percent-short','?plugin_ids=%2'),('percent-invalidhex','?plugin_ids=%GG'),('percent-invalid-last','?plugin_ids=First&plugin_ids=%GG'),('utf8-invalid','?plugin_ids=%FF'),('utf8-truncated','?plugin_ids=%E4%B8'),('utf8-surrogate','?plugin_ids=%ED%A0%80'),('utf8-invalid-last','?plugin_ids=First&plugin_ids=%FF'),('encoded-key','?%70lugin_ids=Last'),('double-percent','?plugin_ids=%25GG'),('plus-space','?plugin_ids=A+B%2BC')]
        for name,query in queries:cases.append(('query-'+name,'GET','rating'+query,None,'{}'))
        for route in ('statistic','rating','rating/Owned'):
            peer_body='{"plugin_id":"Owned"}' if route=='rating/Owned' else '{}'
            for method in ('GET','HEAD','POST'):
                for slashes in range(4):
                    suffix='/'*slashes
                    cases.append(('slash-'+route.replace('/','-')+'-'+method+'-'+str(slashes),method,route+suffix+'?plugin_ids=A%3BB&plugin_ids=', '{"rating":4.1}' if method=='POST' else None,peer_body))
        for version in ('v1','v2'):
            for name,method,route,raw,body in cases:
                config.set(K.UserInstalledPlugins,['Owned'])
                settings.PLUGIN_STATISTIC_SHARE=True;state.update(body=body,status=200);start=len(wire)
                response=await c.request(method,'/api/'+version+'/plugin/'+route,content=raw,headers={'Content-Type':'application/json'})
                try:result=response.json()
                except Exception:result=response.text
                record(name,result,start,version=version,status=response.status_code,body_hex=response.content.hex(),content_type=response.headers.get('Content-Type'),location=response.headers.get('Location'),allow=response.headers.get('Allow'),scope=scopes[-1],request={'version':version,'method':method,'route':route,'raw':raw,'raw_hex':None,'peer_body_hex':None,'installed_ids':['Owned'],'peer_body':body,'peer_status':200,'share':True,'content_type':'application/json'})
    await aclose_shared_async_transports()
def run_helpers():
    """真实 helper、DAO 验证报告标记与缓存；没有替换 UID。"""
    settings.PLUGIN_STATISTIC_SHARE=True;state.update(status=500,body='not-json')
    config.set(K.PluginInstallReport,None);config.set(K.UserInstalledPlugins,['Owned','Clone','Owned'])
    for name,status in [('first-fail',500),('retry-success',200),('marked-skip',500)]:
        state['status']=status;start=len(wire);H.init_plugin_report();record(name,config.get(K.PluginInstallReport),start)
    config.set(K.PluginInstallReport,None);config.set(K.UserInstalledPlugins,[]);start=len(wire);H.init_plugin_report();record('empty-no-mark',config.get(K.PluginInstallReport),start)
    config.set(K.UserInstalledPlugins,['Owned']);settings.PLUGIN_STATISTIC_SHARE=False;start=len(wire);H.init_plugin_report();record('disabled-no-mark',config.get(K.PluginInstallReport),start)
    settings.PLUGIN_STATISTIC_SHARE=True;state.update(status=200,body='arbitrary')
    items=[('Owned','local://Owned?path=%2Fprivate%2Fowned&version=v%201.2&version=second&secret=synthetic'),('','ignored'),('Double','local://a%20b?version=1'),('Bad','local://?path=owned'),('Plain','https://example.invalid/repo?synthetic=public')]
    for name,values in [('explicit-items',items),('explicit-empty-fallback',[])]:
        start=len(wire);record(name,H.install_plugin_report(values),start)
    for pid,repo in [('','local://empty'),('space +中','local://space%20%2B%E4%B8%AD?path=private&version=%E4%B8%AD')]:
        start=len(wire);record('single-'+pid,H.install_plugin_reg(pid,repo),start)
    H.get_plugin_statistic.cache_clear();settings.PLUGIN_STATISTIC_SHARE=False;start=len(wire);record('cache-disabled',H.get_plugin_statistic(),start)
    settings.PLUGIN_STATISTIC_SHARE=True;state['body']='{"Owned":81}';start=len(wire);record('cache-enable-still-empty',H.get_plugin_statistic(),start)
    H.get_plugin_statistic.cache_clear();start=len(wire);record('cache-clear-first',H.get_plugin_statistic(),start)
    state['body']='{"Owned":82}';start=len(wire);record('cache-hit',H.get_plugin_statistic(),start)
    config.set(K.PluginInstallReport,'1')
try:
    asyncio.run(run_api())
finally:
    peer.shutdown();peer.server_close();thread.join();global_vars.stop_system()
imports={}
for module in list(sys.modules.values()):
    path=getattr(module,'__file__',None)
    if path and Path(path).is_file():imports[str(Path(path).resolve())]=hashlib.sha256(Path(path).read_bytes()).hexdigest()
result={'source_sha':'8a83efeacd3e26b82be495e17d50146e31c96a60','driver_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'kind':'full normally imported source factory/router/helper/schema/SQLite plus owned HTTP peer, no daemon/JWT/browser acceptance','fixtures':fixtures,'versions':{x:importlib.metadata.version(x) for x in ('fastapi','pydantic','httpx','cachetools')},'observations':observations,'all_uid_present_equal':bool(uid_seen) and all(x and x==uid_seen[0] for x in uid_seen),'network_rejections':rejections,'imports':imports}
oracle=json.loads((HERE/'source-query-slash.json').read_text())
assert result['versions']==oracle['versions'] and len(observations)==116
assert observations==oracle['observations'],'actual full source query/slash116 differs from independent oracle'
from starlette.datastructures import QueryParams
for row in json.loads((HERE/'source-query-decoding.json').read_text())['cases']:
    q=QueryParams(bytes.fromhex(row['raw_hex']))
    assert ('plugin_ids' in q)==row['present'] and q.get('plugin_ids','')==row['value']
result['frozen_oracle_equal']=True
Engine.dispose();owned.cleanup()
(OUT/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(observations),'wire':len(wire),'uid_consistent':result['all_uid_present_equal'],'rejections':rejections,'versions':result['versions']}))
