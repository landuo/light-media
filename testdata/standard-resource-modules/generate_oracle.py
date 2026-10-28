"""Pinned site_resource/async_browse/module dispatch, owned indexer/network outputs."""
import ast,asyncio,contextlib,copy,dataclasses,hashlib,inspect,json,pathlib,traceback,types
from typing import *
from enum import Enum
root=pathlib.Path('/Users/accidia/projects/MoviePilot');paths=['app/chain/__init__.py','app/chain/torrents.py','app/core/context.py','app/utils/object.py','app/schemas/types.py','app/api/endpoints/site.py'];raw={p:(root/p).read_text() for p in paths}
chain=next(n for n in ast.parse(raw[paths[0]]).body if isinstance(n,ast.ClassDef) and n.name=='ChainBase')
names=['__is_valid_empty','__handle_plugin_error','__handle_system_error','__handle_rate_limit_error','__execute_plugin_modules','__execute_system_modules','__async_execute_plugin_modules','__async_execute_system_modules','run_module','async_run_module','refresh_torrents','async_refresh_torrents']
nodes=[copy.deepcopy(n) for n in chain.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names];assert len(nodes)==len(names)
class RateLimitExceededException(Exception):pass
async def thread(fn,*a,**kw):return fn(*a,**kw)
ns=dict(Enum=Enum,Optional=Optional,Union=Union,List=List,Dict=Dict,Any=Any,dataclass=dataclasses.dataclass,field=dataclasses.field,inspect=inspect,traceback=traceback,RateLimitExceededException=RateLimitExceededException,run_in_threadpool=thread,logger=types.SimpleNamespace(info=lambda *a:None,debug=lambda *a:None,error=lambda *a:None),EventType=types.SimpleNamespace(SystemError='system.error'))
def execute(nodes,filename):
 exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),*nodes],type_ignores=[])),filename,'exec'),ns)
check=next(n for n in ast.walk(ast.parse(raw[paths[3]])) if isinstance(n,ast.FunctionDef) and n.name=='check_signature');check.decorator_list=[];ons=dict(inspect=inspect,get_type_hints=get_type_hints,FunctionType=types.FunctionType);exec(compile(ast.fix_missing_locations(ast.Module(body=[check],type_ignores=[])),'fixed/object.py','exec'),ons);ns['ObjectUtils']=types.SimpleNamespace(check_signature=ons['check_signature'])
execute([ast.ClassDef(name='ChainBase',bases=[],keywords=[],body=nodes,decorator_list=[])],'fixed/chain.py')
execute([n for n in ast.parse(raw[paths[2]]).body if isinstance(n,ast.ClassDef) and n.name=='TorrentInfo'],'fixed/context.py');Torrent=ns['TorrentInfo']
torrents_cls=next(n for n in ast.parse(raw[paths[1]]).body if isinstance(n,ast.ClassDef) and n.name=='TorrentsChain');browse=copy.deepcopy(next(n for n in torrents_cls.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='async_browse'));browse.decorator_list=[];execute([ast.ClassDef(name='TorrentsChain',bases=[ast.Name(id='ChainBase',ctx=ast.Load())],keywords=[],body=[browse],decorator_list=[])],'fixed/torrents.py');Chain=ns['TorrentsChain']
origin='http://127.0.0.1:43100';site=dict(id=1,name='Owned resource site',domain=origin,url=origin,cookie='owned-site-cookie',ua='Owned-UA',apikey=None,token=None,rss=None,proxy=0,render=0,public=0,pri=1,downloader=None,filter=None,note=None,limit_interval=0,limit_count=0,limit_seconds=0,timeout=15,is_active=True)
async def indexer(domain):return copy.deepcopy(site)
ns['SitesHelper']=lambda:types.SimpleNamespace(async_get_indexer=indexer)
plugin_model=Torrent(site=1,site_name=site['name'],site_cookie='owned-plugin-secret',site_ua='Owned-UA',title='Owned plugin resource',description=None,page_url=origin+'/details.php?id=11',enclosure=origin+'/download.php?id=11&passkey=owned-private-material',size=1024,seeders=3,peers=2,downloadvolumefactor=0,uploadvolumefactor=1,labels=['owned-label'])
core_model=Torrent(site=1,site_name=site['name'],title='Owned core resource',page_url=origin+'/details.php?id=22',size=2048,seeders=2,downloadvolumefactor=1,uploadvolumefactor=1)
class Core:
 def get_priority(self):return 0
 def get_name(self):return 'Owned native indexer'
 async def async_refresh_torrents(self,site,keyword=None,cat=None,page=0):
  calls.append(dict(kind='core',method='async_refresh_torrents',kwargs=copy.deepcopy(dict(site=site,keyword=keyword,cat=cat,page=page))));return [copy.deepcopy(core_model)]
def clean(v):
 if isinstance(v,Enum):return v.value
 if isinstance(v,Torrent):return clean(v.to_dict())
 if isinstance(v,dict):return {k:clean(x) for k,x in v.items()}
 if isinstance(v,(tuple,list)):return [clean(x) for x in v]
 return v
cases=[]
for label,value in [('none',None),('empty_list',[]),('plugin_list',[plugin_model]),('false',False),('zero',0),('empty_dict',{}),('bad_scalar','bad scalar'),('failed','owned_raise')]:
 calls=[];errors=[]
 async def plugin(*a,**kw):
  calls.append(dict(kind='plugin',method='async_refresh_torrents',args=a,kwargs=copy.deepcopy(kw)))
  if label=='failed':raise RuntimeError('owned failure')
  return copy.deepcopy(value)
 c=Chain();c.pluginmanager=types.SimpleNamespace(get_plugin_modules=lambda:{('OwnedModuleOverride','owned'):{'async_refresh_torrents':plugin}});c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[Core()]);c.messagehelper=types.SimpleNamespace(put=lambda **kw:errors.append(kw['role']));c.eventmanager=types.SimpleNamespace(send_event=lambda *a:None)
 try:result=asyncio.run(c.async_browse(domain='127.0.0.1',keyword=None,cat=None,page=1));error=None
 except Exception as exc:result=None;error=type(exc).__name__
 # Execute the fixed endpoint's actual falsey guard/to_dict list projection.
 endpoint=next(n for n in ast.parse(raw[paths[5]]).body if isinstance(n,ast.AsyncFunctionDef) and n.name=='site_resource');tail=copy.deepcopy(endpoint.body[-2:]);projection=ast.FunctionDef(name='project',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='torrents')],kwonlyargs=[],kw_defaults=[],defaults=[]),body=tail,decorator_list=[]);execute([projection],'fixed/site_resource_tail.py')
 try:public=ns['project'](result);public_error=None
 except Exception as exc:public=None;public_error=type(exc).__name__
 cases.append(dict(name=label,plugin_result=clean(value) if label!='failed' else None,observations=clean(calls),returned=clean(result),error=error,endpoint_returned=clean(public),endpoint_error=public_error,error_roles=errors))
assert [len([c for c in r['observations'] if c['kind']=='core']) for r in cases]==[1,1,1,0,0,0,0,1]
out=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind='actual fixed ChainBase async dispatcher/wrapper + TorrentsChain.async_browse + site_resource falsey/to_dict tail, real TorrentInfo dataclass; site indexer registry and native network result are owned boundaries',normalization='dynamic owned loopback port only may substitute43100 in target tests; Go native one-based page2 maps sourcepage1',source_sha256={p:hashlib.sha256(s.encode()).hexdigest() for p,s in raw.items()},site=site,cases=cases)
pathlib.Path(__file__).with_name('source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print('actual browse/source cases',len(cases))
