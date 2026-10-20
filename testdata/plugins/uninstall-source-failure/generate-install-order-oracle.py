"""Installed endpoint method AST, fake installer/registration/reload only."""
import ast,asyncio,hashlib,json,pathlib
from typing import Any,Optional
from types import SimpleNamespace as NS
root=pathlib.Path(__file__).parent;p=root/'install-order-source.json';s=json.loads(p.read_text())
assert s['installed_api_sha256']=='16e96285764587b266c07af21b6df140836c00f916d6626b87584a492a4031ce'
async def execute(name,initial,pid,force=False,state=True,repo='owned:synthetic-only'):
 installed=initial[:];trace=[]
 class Store:
  def get(self,key):assert key=='installed';return installed[:]
  async def async_set(self,key,value):assert key=='installed';installed[:]=value;trace.append('save_installed')
 class Helper:
  async def async_install(self,**kw):trace.append('install');return state,'owned failed install' if not state else ''
  async def async_get_plugin_system_version_check_message(self,*a):return ''
 async def register(**kw):trace.append('register')
 async def threadpool(fn,pid):trace.append('reload');fn(pid)
 scope={'Any':Any,'Optional':Optional,'User':object,'Depends':lambda x:None,'get_current_active_superuser_async':lambda:None,'SystemConfigOper':Store,'SystemConfigKey':NS(UserInstalledPlugins='installed'),'PluginHelper':Helper,'PluginManager':lambda:NS(get_plugin_ids=lambda:initial[:]),'MoviePilotServerHelper':NS(async_install_plugin_reg=register),'schemas':NS(Response=lambda **kw:NS(**kw)),'run_in_threadpool':threadpool,'reload_plugin':lambda pid:None}
 tree=ast.parse(s['install_method']);tree.body[0].decorator_list=[]
 exec(compile(ast.fix_missing_locations(tree),'installed-install-method','exec'),scope)
 result=await scope['install'](pid,repo_url=repo,force=force)
 return {'name':name,'initial':initial,'id':pid,'force':force,'install_success':state,'expected_installed':installed,'response_success':result.success,'trace':trace}
async def main():
 cases=[]
 for args in [('new',['Alpha','Beta'],'Gamma'),('reinstall',['Beta'],'Alpha'),('already_installed',['Alpha','Beta'],'Alpha'),('forced_upgrade',['Alpha','Beta'],'Alpha',True),('failed_new',['Alpha','Beta'],'Gamma',False,False)]:cases.append(await execute(*args))
 out={'source_sha256':s['installed_api_sha256'],'snapshot_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'scope':'owned only; no source app imports, repositories, registration, reload, or real credentials','cases':cases}
 target=root/'install-order-oracle.json';target.write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({'cases':len(cases),'sha256':hashlib.sha256(target.read_bytes()).hexdigest()}))
asyncio.run(main())
