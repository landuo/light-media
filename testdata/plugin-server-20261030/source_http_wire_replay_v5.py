"""固定源完整 factory/router/SQLite/server helper 的 owned HTTP replay；依赖目录只读。"""
import asyncio, hashlib, importlib.metadata, json, os, socket, sys, threading, time, types
from pathlib import Path
BASE=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-20261030')
SRC=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
OUT=Path(sys.argv[1]).resolve();OUT.mkdir(parents=True,exist_ok=False)
ROOT=OUT/'runtime';ROOT.mkdir();(ROOT/'app.env').write_text('')
sys.dont_write_bytecode=True
sys.path[:0]=[str(BASE/'audits/system-config-source/reachability/deps03'),str(BASE/'audits/system-config-source/reachability/deps02'),str(BASE/'audits/system-config-source/reachability/deps'),str(BASE/'worktrees/lifecycle/work/lifecycle/python-deps'),str(SRC)]
os.environ.clear();os.environ.update(HOME=str(ROOT),CONFIG_DIR=str(ROOT),PATH='/usr/bin:/bin',PYTHONDONTWRITEBYTECODE='1',TZ='UTC',PLUGIN_AUTO_RELOAD='false',DEV='false',USAGE_STATISTIC_SHARE='false',PLUGIN_STATISTIC_SHARE='false',MP_SERVER_HOST='',CACHE_BACKEND_TYPE='memory')
os.chdir(ROOT)
blocked=[]
def deny(*a,**k):
    """禁止全部外部网络，仅运行内存和独占文件。"""
    blocked.append('network');raise RuntimeError('all network forbidden by owned replay')
original_connect=socket.socket.connect
def owned_connect(self,address):
    if isinstance(address,tuple) and address[0]=='127.0.0.1' and address[1]==allowed_port[0]:return original_connect(self,address)
    return deny(address)
allowed_port=[-1];socket.socket.connect=owned_connect
fixtures=[];notices=[]
def stub(name,reason,**attrs):
    """显式声明范围外依赖，不替换待验组件。"""
    m=types.ModuleType(name);m.__dict__.update(attrs);sys.modules[name]=m;fixtures.append({'module':name,'boundary':reason});return m
class Inert:
    """范围外的业务方法一旦访问即失败。"""
    def __getattr__(self,name):raise RuntimeError('excluded business accessed: '+name)
class MessageBoundary:
    """仅记录服务失败通知交接，不发送任何消息。"""
    def put(self,**kwargs):notices.append(kwargs)
class ChainBoundary:
    """本切片不执行系统媒体、工作流和通知业务。"""
    def __init__(self):self.messagehelper=MessageBoundary()
    def send(self,*a,**k):notices.append({'send_boundary':True})
    def __getattr__(self,name):raise RuntimeError('business chain accessed: '+name)
chain=stub('app.chain','non-plugin host business not executed',ChainBase=ChainBoundary);chain.__path__=[]
for mod,name in [('mediaserver','MediaServerChain'),('recommend','RecommendChain'),('site','SiteChain'),('subscribe','SubscribeChain'),('transfer','TransferChain'),('workflow','WorkflowChain')]:stub('app.chain.'+mod,'non-plugin builtin jobs not initialized',**{name:Inert})
stub('watchfiles','not a watcher slice',watch=deny)
class SitesBoundary(Inert):auth_level=1
stub('app.helper.sites','official licensing excluded',SitesHelper=SitesBoundary)
stub('app.helper.image','wallpaper not in plugin jobs',WallpaperHelper=Inert)
stub('app.helper.message','record notice boundary only',MessageHelper=MessageBoundary)
from app.core.config import settings, global_vars
assert settings.CONFIG_PATH==ROOT and settings.CACHE_BACKEND_TYPE!='redis'
from app.db import Base,Engine
from app.db import models
Base.metadata.create_all(Engine)
from app.core.plugin import PluginManager
from app.plugins import _PluginBase
from app.helper.progress import ProgressHelper
from app.core.cache import MemoryBackend
import app.scheduler as scheduler_module
from app.scheduler import Scheduler
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.interval import IntervalTrigger
from apscheduler.triggers.date import DateTrigger
from datetime import datetime,timedelta,timezone

from contextlib import asynccontextmanager
@asynccontextmanager
async def no_daemon(app):yield
stub('app.startup.lifecycle','daemon startup excluded; factory/middleware/router are actual',lifespan=no_daemon)
stub('app.command','unselected command/install lifecycle not executed',Command=Inert)
from app.factory import app
from app.api.endpoints import plugin as endpoint
from app.helper.server import MoviePilotServerHelper
from app.db.systemconfig_oper import SystemConfigOper
from app.schemas.types import SystemConfigKey
from app.db.user_oper import get_current_active_superuser,get_current_active_superuser_async
from app.core.security import verify_token
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
import httpx
center={'body':'{}','status':200};requests=[]
class Peer(BaseHTTPRequestHandler):
    def do_GET(self):
        requests.append({'method':'GET','path':self.path,'uid':bool(self.headers.get('X-MoviePilot-User-Uid'))});self.send_response(center['status']);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(center['body'] if isinstance(center['body'],bytes) else center['body'].encode())
    def do_POST(self):
        body=self.rfile.read(int(self.headers.get('Content-Length','0')));requests.append({'method':'POST','path':self.path,'body':json.loads(body) if body else None,'uid':bool(self.headers.get('X-MoviePilot-User-Uid'))});self.send_response(center['status']);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(center['body'] if isinstance(center['body'],bytes) else center['body'].encode())
    def log_message(self,*args):pass
peer=ThreadingHTTPServer(('127.0.0.1',0),Peer);allowed_port[0]=peer.server_port;thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start()
settings.MP_SERVER_HOST='http://127.0.0.1:'+str(peer.server_port);settings.PLUGIN_STATISTIC_SHARE=True;MoviePilotServerHelper._user_uid='owned-synthetic-statistics-user'
app.include_router(endpoint.router,prefix='/api/v1/plugin')
app.include_router(endpoint.router,prefix='/api/v2/plugin')
async def owned_user():return None
for dependency in [get_current_active_superuser,get_current_active_superuser_async,verify_token]:app.dependency_overrides[dependency]=owned_user
SystemConfigOper().set(SystemConfigKey.UserInstalledPlugins,['Owned'])

async def run():
    observations=[]
    values=[None,True,False,0,0.1,5,5.1,-0.1,4.55,[],{},'', 'bad','nan','NaN','+NaN','-NaN','inf','Infinity','-Infinity','+inf','1e999','1e-999','4.5',' 4.5 ','4.5\u00a0','4.5\x1c','٤.٥','４.５','1_.0','1._0','1e_0','1_e0','_1','1_','1__0','1_0e-1',123456789012345678901234567890]
    for n in range(1,51):values.append(n/10)
    for n in [0.1,0.2,1,2.5,4.5,5]:
        for epsilon in [1e-12,1e-11,1e-10,2e-10,5e-10,9e-10,1e-9,1e-8,1e-7]:
            values.extend([n+epsilon,n-epsilon])
    cases=[]
    for i,value in enumerate(values):cases.append({'case':'request-value-'+str(i),'method':'POST','path':'rating/Owned','request_body':json.dumps({'rating':value},ensure_ascii=False),'remote_body':'{"plugin_id":"Owned","average_rating":4.5}','remote_status':200})
    for i,raw in enumerate(['{}','null','[]','0','"text"','true','',' ','{','{"rating":','{"rating": 1,}','{"rating":1} null','broken','{"rating":NaN}','{"rating":Infinity}','{"rating":-Infinity}','{"rating":1e999}','{"rating":1,"rating":4.5}']):cases.append({'case':'request-shape-'+str(i),'method':'POST','path':'rating/Owned','request_body':raw,'remote_body':'{}','remote_status':200})
    responses=['{}','null','[]','true','"text"','broken','{"plugin_id":"Owned"}','{"plugin_id":null}','{"plugin_id":3}','{"plugin_id":true}','{"plugin_id":"Owned","average_rating":null}','{"plugin_id":"Owned","average_rating":"NaN"}','{"plugin_id":"Owned","average_rating":"Infinity","user_rating":"-inf"}','{"plugin_id":"Owned","average_rating":NaN}','{"plugin_id":"Owned","average_rating":1e999}','{"plugin_id":"Owned","rating_count":99999999999999999999999999999999999}','{"plugin_id":"Owned","rating_count":1.0}','{"plugin_id":"Owned","rating_count":1e19}','{"plugin_id":"Owned","rating_count":"1_000"}','{"plugin_id":"Owned","rating_count":"４"}','{"plugin_id":"Owned","rating_count":null}','{"plugin_id":"Owned","extra":true}']
    responses.extend(['{"plugin_id":"Owned","average_rating":4.2,"\\ud800":1}','{"plugin_id":"\\ud800","average_rating":4.2}','{"plugin_id":"Owned","extra":"\\ud800"}'])
    for i,raw in enumerate(responses):cases.append({'case':'response-'+str(i),'method':'GET','path':'rating/Owned','request_body':None,'remote_body':raw,'remote_status':200})
    for i,raw in enumerate(['{"Owned":23}','[]','null','broken','{"Owned":NaN}','{"Owned":1e999}','{"Owned":9999999999999999999999999999999999}']):cases.append({'case':'statistics-'+str(i),'method':'GET','path':'statistic','request_body':None,'remote_body':raw,'remote_status':200})
    for i,(path,body) in enumerate([('rating','{}'),('rating?plugin_ids=Owned,Other','{"Owned":{"plugin_id":"Owned"}}'),('rating?plugin_ids=','{}'),('rating?plugin_ids=First&plugin_ids=Last,Owned','{}'),('rating','[]'),('rating','null'),('rating','broken'),('rating','{"Owned":null}'),('rating','{"Owned":{"plugin_id":"Owned","average_rating":"NaN"}}')]):cases.append({'case':'batch-'+str(i),'method':'GET','path':path,'request_body':None,'remote_body':body,'remote_status':200})
    for i,raw in enumerate(['null','[]','false','0','"text"','broken','{"success":false}','NaN','{"value":NaN}']):cases.append({'case':'submit-result-'+str(i),'method':'POST','path':'rating/Owned','request_body':'{"rating":4.5}','remote_body':raw,'remote_status':200})
    for i,(method,path,payload) in enumerate([('GET','statistic',None),('GET','rating',None),('GET','rating/Owned',None),('POST','rating/Owned','{"rating":4.5}')]):cases.append({'case':'center503-'+str(i),'method':method,'path':path,'request_body':payload,'remote_body':'{}','remote_status':503})
    cases.append({'case':'missing-install','method':'POST','path':'rating/Missing','request_body':'{"rating":4.5}','remote_body':'{}','remote_status':200})
    for i,(method,path,request,remote) in enumerate([
        ('GET','statistic',None,'{"\\ud800":23}'),('GET','statistic',None,'{"Owned":"\\ud800"}'),
        ('GET','rating',None,'{"\\ud800":{"plugin_id":"Owned","average_rating":4.2}}'),
        ('POST','rating/Owned','{"rating":4.5}','{"\\ud800":1}'),
        ('POST','rating/Owned','{"rating":4.5}','{"value":"\\ud800"}'),
        ('POST','rating/Owned','{"rating":4.5,"\\ud800":1}','{}'),
        ('POST','rating/Owned','{"rating":4.5,"extra":"\\ud800"}','{}')]):cases.append({'case':'unicode-'+str(i),'method':method,'path':path,'request_body':request,'remote_body':remote,'remote_status':200})
    for i,content_type in enumerate(['Application/JSON','application/problem+json','text/plain','']):cases.append({'case':'content-type-'+str(i),'method':'POST','path':'rating/Owned','request_body':'{"rating":4.5}','remote_body':'{}','remote_status':200,'request_content_type':content_type})
    import codecs
    encodings=[('utf8bom','utf-8-sig',b''),('utf16le','utf-16-le',b''),('utf16be','utf-16-be',b''),('utf16lebom','utf-16-le',codecs.BOM_UTF16_LE),('utf16bebom','utf-16-be',codecs.BOM_UTF16_BE),('utf32le','utf-32-le',b''),('utf32be','utf-32-be',b''),('utf32lebom','utf-32-le',codecs.BOM_UTF32_LE),('utf32bebom','utf-32-be',codecs.BOM_UTF32_BE)]
    for name,encoding,bom in encodings:
        request=bom+'{"rating":4.5}'.encode(encoding);remote=bom+'{"plugin_id":"Owned","average_rating":4.2}'.encode(encoding)
        cases.append({'case':'request-encoding-'+name,'method':'POST','path':'rating/Owned','request_body':None,'request_body_hex':request.hex(),'remote_body':'{}','remote_status':200})
        cases.append({'case':'response-encoding-'+name,'method':'GET','path':'rating/Owned','request_body':None,'remote_body':None,'remote_body_hex':remote.hex(),'remote_status':200})
    for name,invalid in [('invalid-utf8',bytes.fromhex('ff')),('wtf8',bytes.fromhex('eda080'))]:
        raw=b'{"rating":4.5,"extra":"'+invalid+b'"}'
        cases.append({'case':'request-'+name,'method':'POST','path':'rating/Owned','request_body':None,'request_body_hex':raw.hex(),'remote_body':'{}','remote_status':200})
        remote=b'{"plugin_id":"Owned","average_rating":4.2,"extra":"'+invalid+b'"}'
        for method,path in [('GET','statistic'),('GET','rating'),('GET','rating/Owned'),('POST','rating/Owned')]:cases.append({'case':'response-'+name+'-'+method+'-'+path,'method':method,'path':path,'request_body':'{"rating":4.5}' if method=='POST' else None,'remote_body':None,'remote_body_hex':remote.hex(),'remote_status':200})
    cases.append({'case':'request-invalid-hex-escape','method':'POST','path':'rating/Owned','request_body':r'{"rating":4.1,"extra":"\u+123"}','remote_body':'{}','remote_status':200})
    cases.append({'case':'request-python-int-decoder-limit','method':'POST','path':'rating/Owned','request_body':'{"rating":'+('9'*4301)+'}','remote_body':'{}','remote_status':200})
    for path in ['statistic','rating','rating/Owned']:
        for method in ['HEAD','OPTIONS','PATCH']:
            cases.append({'case':'method-'+method+'-'+path,'method':method,'path':path,'request_body':None,'remote_body':'{}','remote_status':200})
        cases.append({'case':'trailing-'+path,'method':'GET','path':path+'/','request_body':None,'remote_body':'{}','remote_status':200})
    for path in ['statistic','rating']:
        cases.append({'case':'method-POST-'+path,'method':'POST','path':path,'request_body':'{}','remote_body':'{}','remote_status':200})
    for method in ['PUT','DELETE']:
        cases.append({'case':'method-'+method+'-rating-item','method':method,'path':'rating/Owned','request_body':'{}','remote_body':'{}','remote_status':200})
    # The roots' PUT/DELETE select the generic config/uninstall routes (29/17),
    # not these statistics/rating endpoints. Do not mislabel those as 405.
    cases=[{**c,'version':version,'case':version+'-'+c['case']} for version in ['v1','v2'] for c in cases]
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app,raise_app_exceptions=False),base_url='http://owned-asgi') as client:
        for c in cases:
            center['body']=bytes.fromhex(c['remote_body_hex']) if c.get('remote_body_hex') else c['remote_body'];center['status']=c['remote_status'];before=len(requests)
            response=await client.request(c['method'],'/api/'+c['version']+'/plugin/'+c['path'],content=bytes.fromhex(c['request_body_hex']) if c.get('request_body_hex') else c['request_body'],headers={'Content-Type':c.get('request_content_type','application/json')})
            observations.append({**c,'status':response.status_code,'content_type':response.headers.get('content-type'),'body':response.text,'body_hex':response.content.hex(),'allow':response.headers.get('allow'),'location':response.headers.get('location'),'peer_calls':requests[before:]})
    return observations
try:observations=asyncio.run(run())
finally:peer.shutdown();peer.server_close();thread.join();global_vars.stop_system()
imports={}
for m in list(sys.modules.values()):
    p=getattr(m,'__file__',None)
    if p and Path(p).is_file():imports[str(Path(p).resolve())]=hashlib.sha256(Path(p).read_bytes()).hexdigest()
result={'source':'8a83efeacd3e26b82be495e17d50146e31c96a60','kind':'normally imported complete factory+router+helper+schemas+SQLite with actual owned HTTP center; no full daemon/JWT/browser','versions':{x:importlib.metadata.version(x) for x in ['fastapi','pydantic','pydantic-core','httpx','cachetools']},'fixtures':fixtures,'observations':observations,'network_rejections':blocked,'imports':imports}
(OUT/'result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(observations),'versions':result['versions'],'network_rejections':blocked,'imports':len(imports)},ensure_ascii=False))
