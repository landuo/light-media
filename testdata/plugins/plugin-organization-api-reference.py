"""Read-only installed AST+ASGI reference. No MoviePilot module/config import."""
import ast, hashlib, json, warnings, copy, sys
from pathlib import Path
from types import SimpleNamespace
from typing import Any, List, Optional, Annotated, Union
from fastapi import APIRouter, FastAPI, Depends, Body
from fastapi.testclient import TestClient
from pydantic import BaseModel, Field, model_validator
warnings.filterwarnings('ignore')
source_root=Path(sys.argv[1]) if len(sys.argv)>1 else Path('/app')
paths={key:source_root/path for key,path in {'plugin':'app/api/endpoints/plugin.py','user':'app/api/endpoints/user.py','response':'app/schemas/response.py'}.items()}
trees={k:ast.parse(p.read_text()) for k,p in paths.items()}
state={'folders':{},'user':{}}
class SystemConfigOper:
 def get(self,key): return state['folders']
 def set(self,key,value): state['folders']=value
 async def async_set(self,key,value): self.set(key,value)
class UserConfigOper:
 def get(self,username,key): return state['user'].get(key)
 def set(self,username,key,value): state['user'][key]=value
class User(BaseModel): name:str='synthetic-admin'
async def admin(): return User()
ns=dict(Any=Any,List=List,Optional=Optional,Annotated=Annotated,Union=Union,Depends=Depends,Body=Body,User=User,
 BaseModel=BaseModel,Field=Field,model_validator=model_validator,
 get_current_active_superuser_async=admin,get_current_active_user=admin,
 SystemConfigOper=SystemConfigOper,UserConfigOper=UserConfigOper,SystemConfigKey=SimpleNamespace(PluginFolders='folders'),
 LocaleHelper=SimpleNamespace(get_current_locale=lambda:'zh-CN',translate_text=lambda text,locale:text),
 logger=SimpleNamespace(error=lambda *a,**k:None,info=lambda *a,**k:None,debug=lambda *a,**k:None))
def execute(nodes):
 exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<original-organization-reference>','exec'),ns)
execute([next(n for n in trees['response'].body if isinstance(n,ast.ClassDef) and n.name=='Response')])
ns['schemas']=SimpleNamespace(Response=ns['Response'])
plugin_router=APIRouter();ns['router']=plugin_router
names=['get_plugin_folders','save_plugin_folders','create_plugin_folder','delete_plugin_folder','update_folder_plugins']
for name in names: execute([next(n for n in trees['plugin'].body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name==name)])
user_router=APIRouter();ns['router']=user_router
for name in ['get_config','set_config']:execute([next(n for n in trees['user'].body if isinstance(n,ast.FunctionDef) and n.name==name)])
app=FastAPI(docs_url=None,openapi_url=None)
for version in ['v1','v2']:
 app.include_router(plugin_router,prefix='/api/'+version+'/plugin')
 app.include_router(user_router,prefix='/api/'+version+'/user')
cases=[]
def case(name,method,endpoint,body=None,version='v1'):
 with TestClient(app) as client:
  response=client.request(method,'/api/'+version+'/'+endpoint,json=body)
 cases.append(dict(name=name,method=method,endpoint=endpoint,body=body,version=version,status=response.status_code,allow=response.headers.get('allow'),result=response.json(),folders=copy.deepcopy(state['folders']),user=copy.deepcopy(state['user'])))
case('empty-folders','GET','plugin/folders')
case('unset-order','GET','user/config/PluginOrder')
case('create-chinese','POST','plugin/folders/剧集插件')
case('duplicate-create','POST','plugin/folders/剧集插件')
case('mixed-old-new-save','POST','plugin/folders',{'旧格式':['One'],'新格式':{'plugins':['Two'],'order':0,'showIcon':False,'future':{'keep':0}},'空目录':[]})
case('get-preserves-false-zero-extra','GET','plugin/folders',version='v2')
case('existing-new-format-members-becomes-list','PUT','plugin/folders/新格式/plugins',['Two','Three'])
case('unknown-folder-members-creates','PUT','plugin/folders/未创建/plugins',['Four'])
case('delete-existing','DELETE','plugin/folders/旧格式')
case('delete-absent','DELETE','plugin/folders/旧格式')
case('save-order-fractional','POST','user/config/PluginOrder',[{'id':'新格式','type':'folder','order':0},{'id':'Two','type':'plugin','order':0.11},{'id':'Three'}])
case('get-order','GET','user/config/PluginOrder')
case('set-folders-does-not-change-order','POST','plugin/folders',{'重命名':{'plugins':['Two','Three'],'showIcon':False}})
case('get-order-after-folder-rename','GET','user/config/PluginOrder')
case('clear-folders','POST','plugin/folders',{})
case('get-cleared','GET','plugin/folders')
case('wrong-folders-method','PUT','plugin/folders',{})
case('wrong-create-method','PUT','plugin/folders/新名字',{})
case('invalid-folder-body','POST','plugin/folders',[])
case('invalid-members-body','PUT','plugin/folders/新名字/plugins',{'plugins':['One']})
case('opaque-folders-are-inert-json','POST','plugin/folders',{'../literal':{'plugins':None,'future':False},' ':['../inert'],'ignored':23})
case('get-opaque-folders','GET','plugin/folders')
case('nullable-order','POST','user/config/PluginOrder',None)
case('get-nullable-order','GET','user/config/PluginOrder')
case('opaque-dictionary-order','POST','user/config/PluginOrder',{'future':False})
case('get-opaque-order','GET','user/config/PluginOrder')
case('integer-order','POST','user/config/PluginOrder',2)
case('get-integer-order','GET','user/config/PluginOrder')
case('integral-float-order','POST','user/config/PluginOrder',2.0)
case('get-integral-float-order','GET','user/config/PluginOrder')
case('zero-float-order','POST','user/config/PluginOrder',0.0)
case('get-zero-float-order','GET','user/config/PluginOrder')
for helper in ['_add_clone_to_plugin_folder','_remove_plugin_from_folders']:
 execute([next(n for n in trees['plugin'].body if isinstance(n,ast.FunctionDef) and n.name==helper)])
state['folders']={'A':[17,'source',False,'source',{'id':'source'}],'ignored':23}
membership_cases=[]
for operation in ['clone','clone','remove']:
 if operation=='clone': ns['_add_clone_to_plugin_folder']('source','copy')
 else: ns['_remove_plugin_from_folders']('source')
 membership_cases.append({'operation':operation,'folders':copy.deepcopy(state['folders'])})
print(json.dumps(dict(source_sha256={k:hashlib.sha256(p.read_bytes()).hexdigest() for k,p in paths.items()},scope='Actual installed FastAPI/Pydantic AST with controlled in-memory configuration and synthetic admin; no original app import/config/credentials/network',cases=cases,membership_cases=membership_cases),ensure_ascii=False,separators=(',',':')))
