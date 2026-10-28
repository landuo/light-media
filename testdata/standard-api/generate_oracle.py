"""Execute fixed manager/route AST with owned callbacks and FastAPI only.
No original application/config/network or Python runtime dependency is shipped.
"""
import ast,copy,hashlib,json,sys
from pathlib import Path
from types import SimpleNamespace
from typing import Optional,List,Dict,Any
from fastapi import FastAPI,Depends,Response
from fastapi.routing import APIRoute
root=Path(sys.argv[1]) if len(sys.argv)>1 else Path('/Users/accidia/projects/MoviePilot')
manager=root/'app/core/plugin.py';endpoint=root/'app/api/endpoints/plugin.py'
mt=ast.parse(manager.read_text());et=ast.parse(endpoint.read_text())
klass=next(n for n in mt.body if isinstance(n,ast.ClassDef) and n.name=='PluginManager')
getter=next(n for n in klass.body if isinstance(n,ast.FunctionDef) and n.name=='get_plugin_apis')
ns=dict(Optional=Optional,List=List,Dict=Dict,Any=Any,Depends=Depends,ObjectUtils=SimpleNamespace(check_method=lambda x:True),logger=SimpleNamespace(error=lambda *a,**k:None,debug=lambda *a,**k:None),PLUGIN_PREFIX='/api/v1/plugin',PLUGIN_V2_PREFIX='/api/v2/plugin',PROTECTED_ROUTES=[])
def execute(nodes):exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'<fixed-api35>','exec'),ns)
execute([getter])
class Manager:
 def get_running_plugin_ids(self):return ['Source']
 get_plugin_apis=ns['get_plugin_apis']
manager_instance=Manager();ns['PluginManager']=lambda:manager_instance
for name in ['_update_plugin_api_routes','_remove_routes','_clean_protected_routes']:
 execute([next(n for n in et.body if isinstance(n,ast.FunctionDef) and n.name==name)])
def verify_apikey():pass
def verify_token():pass
ns.update(verify_apikey=verify_apikey,verify_token=verify_token)
async def callback():return {'pid':'owned'}
class Plugin:
 def __init__(self,values):self.values=values
 def get_api(self):return self.values
 def get_state(self):raise AssertionError('API registration must not ask business state')
inputs=[
 ('default-get', [{'key':'default','path':'/default'}]),
 ('empty-methods', [{'key':'empty','path':'/empty','methods':[]}]),
 ('anonymous-precedes-bear', [{'key':'anonymous','path':'/anonymous','methods':['GET'],'allow_anonymous':True,'auth':'bear'}]),
 ('truthy-anonymous-string', [{'key':'truthy','path':'/truthy','allow_anonymous':'false'}]),
 ('false-anonymous-unknown-auth', [{'key':'fallback','path':'/fallback','allow_anonymous':False,'auth':'unrecognized'}]),
 ('nonstring-auth-fallback', [{'key':'numeric','path':'/numeric','auth':7}]),
 ('method-set-uppercase', [{'key':'methodset','path':'/methodset','methods':['get','GET','patch']}]),
 ('bad-item-does-not-hide-later-route',[{'key':'before','path':'/before'},{'key':'bad','path':'/bad','methods':7},{'key':'after','path':'/after','methods':['PATCH']}]),
 ('missing-path-aborts-manager-batch',[{'key':'before','path':'/before'},{'key':'bad'},{'key':'after','path':'/after'}]),
]
cases=[]
for name,raw in inputs:
 app=FastAPI(docs_url=None,redoc_url=None,openapi_url=None);ns['app']=app
 values=copy.deepcopy(raw)
 for value in values:value.pop('key',None);value['endpoint']=callback
 manager_instance._running_plugins={'Source':Plugin(values)}
 ns['_update_plugin_api_routes']('Source','add')
 routes=[]
 for r in app.routes:
  if not isinstance(r,APIRoute) or not r.path.startswith('/api/v1/plugin/Source'):continue
  auth=[d.dependency.__name__ for d in r.dependencies]
  routes.append({'path':r.path.removeprefix('/api/v1/plugin/Source'),'methods':sorted(r.methods),'auth':'anonymous' if not auth else 'bear' if auth==['verify_token'] else 'apikey'})
 cases.append(dict(name=name,input=raw,routes=routes))
response=Response(status_code=204)
output={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','sources':{str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [manager,endpoint]},'kind':'fixed manager/routes AST + actual FastAPI with controlled callback; no original host','cases':cases,'bodyless_response':{'status':response.status_code,'body':response.body.decode()}}
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('fixed API route cases:',len(cases))
