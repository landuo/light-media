"""Run fixed plugin_static_file and access helper AST on owned temporary files.
Host credential decoding/provider registry are controlled dependencies; the
source path checks, MIME/header construction and actual8KiB stream are executed.
"""
import ast,base64,hashlib,json,mimetypes,sys,tempfile
from pathlib import Path
from types import SimpleNamespace
from typing import Annotated,Optional
import aiofiles
from anyio import Path as AsyncPath
from fastapi import FastAPI,APIRouter,Depends,HTTPException,Security,status
from fastapi.security import APIKeyCookie
from fastapi.testclient import TestClient
from starlette.responses import StreamingResponse,Response
source=Path(sys.argv[1] if len(sys.argv)>1 else '/Users/accidia/projects/MoviePilot/app/api/endpoints/plugin.py')
tree=ast.parse(source.read_text());cases=[]
with tempfile.TemporaryDirectory(prefix='owned-api35-source-') as directory:
 root=Path(directory);base=root/'app/plugins/owned';(base/'ui').mkdir(parents=True)
 files={'ui/remoteEntry.js':b'export const owned=true;','ui/part.mjs':b'export default 1;','ui/style.css':b'.owned{color:purple}','opaque.bin':bytes([255,0,1,254,10])*131072,'asset..data.txt':b'owned','outside.txt':b'outside authentication dir'}
 for name,value in files.items():(base/name).write_bytes(value)
 provider={'remote':{'id':'Owned','url':'plugin/file/owned/ui/remoteEntry.js'},'enabled':False}
 class Manager:
  def get_plugin_auth_providers(self):return [provider]
 def verify_resource_token(token):
  if token!='owned-resource':raise HTTPException(status_code=403,detail='token校验不通过')
 router=APIRouter();ns=dict(Annotated=Annotated,Optional=Optional,StreamingResponse=StreamingResponse,Depends=Depends,Security=Security,HTTPException=HTTPException,status=status,resource_token_cookie=APIKeyCookie(name='owned-resource',auto_error=False),verify_resource_token=verify_resource_token,PluginManager=Manager,settings=SimpleNamespace(ROOT_PATH=root),AsyncPath=AsyncPath,aiofiles=aiofiles,mimetypes=mimetypes,logger=SimpleNamespace(warning=lambda *a,**k:None,error=lambda *a,**k:None),router=router)
 for name in ['_is_plugin_auth_remote_file','_verify_plugin_static_file_access','plugin_static_file']:
  node=next(n for n in tree.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name==name)
  exec(compile(ast.fix_missing_locations(ast.Module(body=[node],type_ignores=[])),'<fixed-static35>','exec'),ns)
 app=FastAPI();app.include_router(router,prefix='/api/v1/plugin')
 with TestClient(app) as client:
  for name,method,path,cookie,headers in [
   ('authenticated-js','GET','Owned/ui/remoteEntry.js',True,{}),
   ('anonymous-declared-mjs','GET','owned/ui/part.mjs',False,{}),
   ('anonymous-outside-directory','GET','owned/outside.txt',False,{}),
   ('binary-range-is-full-stream','GET','owned/opaque.bin',True,{'Range':'bytes=0-1'}),
   ('missing-file','GET','owned/missing.bin',True,{}),
   ('directory','GET','owned/ui',True,{}),
   ('double-dot-substring','GET','owned/asset..data.txt',True,{}),
   ('head-is-not-get','HEAD','owned/ui/remoteEntry.js',True,{})]:
   client.cookies.clear()
   if cookie:client.cookies.set('owned-resource','owned-resource')
   response=client.request(method,'/api/v1/plugin/file/'+path,headers=headers)
   cases.append({'name':name,'method':method,'path':path,'session':cookie,'status':response.status_code,'content_type':response.headers.get('content-type'),'content_disposition':response.headers.get('content-disposition'),'allow':response.headers.get('allow'),'content_range':response.headers.get('content-range'),'bytes':len(response.content),'sha256':hashlib.sha256(response.content).hexdigest() if response.status_code==200 else None})
 output={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'kind':'fixed static-file/access AST, real temporary files/actual streaming response; credential decoder/provider lookup controlled','cases':cases,'files':{name:({'pattern':[255,0,1,254,10],'repetitions':131072} if name=='opaque.bin' else {'base64':base64.b64encode(value).decode()}) for name,value in files.items()},'binary_response':{'status':200,'content_type':'application/octet-stream','bytes':[255,0,1,254,10]}}
 # FastAPI's native Response demonstrates the binary payload contract directly.
 response=Response(bytes(output['binary_response']['bytes']),media_type='application/octet-stream',headers={'Content-Disposition':'attachment; filename="owned.bin"'})
 output['binary_response']['sha256']=hashlib.sha256(response.body).hexdigest()
 Path(__file__).with_name('static-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('fixed static-file cases:',len(cases))
