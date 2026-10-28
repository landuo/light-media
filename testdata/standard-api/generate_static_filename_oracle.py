"""Execute fixed static/access AST through actual ASGI with raw Latin1 headers.
TestClient currently incorrectly decodes Latin1 raw headers as UTF8. This direct
ASGI transport records the original framework's bytes without that extra codec.
"""
import ast,asyncio,base64,hashlib,json,mimetypes,sys,tempfile,unicodedata,importlib.metadata
from pathlib import Path
from types import SimpleNamespace
from typing import Annotated,Optional
from urllib.parse import quote
import aiofiles
from anyio import Path as AsyncPath
from fastapi import FastAPI,APIRouter,Depends,HTTPException,Security,status
from fastapi.security import APIKeyCookie
from starlette.responses import StreamingResponse
source=Path('/Users/accidia/projects/MoviePilot/app/api/endpoints/plugin.py');tree=ast.parse(source.read_text());cases=[]
with tempfile.TemporaryDirectory(prefix='owned-static-header-') as tmp:
 root=Path(tmp);base=root/'app/plugins/owned/ui';base.mkdir(parents=True)
 samples=[('ascii','ordinary.css'),('latin1','ownedé.css'),('chinese','owned备份.css'),('emoji','owned💫.css'),('quoted','owneda"b.css'),('query-byte','owneda?b.css')]
 files={'ui/'+name:b'owned-css' for _,name in samples}
 files['ui/remoteEntry.js']=b'export const owned=true;'
 for name,value in files.items():(root/'app/plugins/owned'/name).write_bytes(value)
 class Manager:
  def get_plugin_auth_providers(self):return []
 def verify_resource_token(token):
  if token!='owned-resource':raise HTTPException(status_code=403,detail='token校验不通过')
 router=APIRouter();ns=dict(Annotated=Annotated,Optional=Optional,StreamingResponse=StreamingResponse,Depends=Depends,Security=Security,HTTPException=HTTPException,status=status,resource_token_cookie=APIKeyCookie(name='owned-resource',auto_error=False),verify_resource_token=verify_resource_token,PluginManager=Manager,settings=SimpleNamespace(ROOT_PATH=root),AsyncPath=AsyncPath,aiofiles=aiofiles,mimetypes=mimetypes,logger=SimpleNamespace(warning=lambda *a,**k:None,error=lambda *a,**k:None),router=router)
 for name in ['_is_plugin_auth_remote_file','_verify_plugin_static_file_access','plugin_static_file']:
  node=next(n for n in tree.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name==name)
  exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'<fixed-static-filename>','exec'),ns)
 app=FastAPI();app.include_router(router,prefix='/api/v1/plugin')
 async def capture(path):
  messages=[]
  scope={'type':'http','asgi':{'version':'3.0','spec_version':'2.4'},'http_version':'1.1','method':'GET','scheme':'http','root_path':'','path':'/api/v1/plugin/file/'+path,'raw_path':('/api/v1/plugin/file/'+quote(path,safe='/')).encode(),'query_string':b'','headers':[(b'cookie',b'owned-resource=owned-resource')],'server':('owned.invalid',80),'client':('127.0.0.1',1)}
  async def receive():return {'type':'http.request','body':b'','more_body':False}
  async def send(message):messages.append(message)
  await app(scope,receive,send)
  head=next(x for x in messages if x['type']=='http.response.start');headers=dict(head.get('headers',[]));body=b''.join(x.get('body',b'') for x in messages if x['type']=='http.response.body')
  return head['status'],headers,body
 for label,name in samples:
  path='Owned/ui/'+name;code,headers,body=asyncio.run(capture(path));disposition=headers.get(b'content-disposition',b'')
  cases.append({'name':label,'method':'GET','path':path,'session':True,'status':code,'content_type':headers.get(b'content-type',b'').decode('latin1') or None,'content_disposition':disposition.decode('latin1') or None,'header_bytes_base64':base64.b64encode(disposition).decode(),'allow':None,'content_range':None,'detail':json.loads(body).get('detail') if code!=200 else None,'bytes':len(body),'sha256':hashlib.sha256(body).hexdigest() if code==200 else None})
 out={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'kind':'actual fixed static-file/access AST, actual FastAPI/Starlette ASGI response headers and complete file stream; resource credential boundary controlled','runtime':{'python':sys.version,'unicode':unicodedata.unidata_version,**{p:importlib.metadata.version(p) for p in ['fastapi','starlette','aiofiles']}},'fixed_requirements':'requirements.in fastapi~=0.136.3,starlette~=1.3.1,aiofiles~=25.1.0','cases':cases,'files':{name:{'base64':base64.b64encode(value).decode()} for name,value in files.items()},'harness':'ASGI2.4 raw header/body capture; original TestClient attempt hit its UTF8-decoding error on legitimate Latin1 header, not a source endpoint failure'}
 Path(__file__).with_name('static-filename-source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
 print('fixed filename cases:',[(x['name'],x['status']) for x in cases])
