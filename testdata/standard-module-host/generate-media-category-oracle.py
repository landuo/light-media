"""Fixed ordinary rule-editor category endpoint and module dispatcher.
Plugin/core functions are owned boundaries; dispatch and endpoint AST are fixed.
"""
import ast,copy,hashlib,json,subprocess
from pathlib import Path
from types import SimpleNamespace as NS
from pydantic import TypeAdapter
FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60'
ROOT=Path('/Users/accidia/projects/MoviePilot')
base=Path(__file__).with_name('generate-metadata-module-oracle.py')
env={'__file__':str(base)}
exec(compile(base.read_text().split('cases=[]')[0],str(base),'exec'),env)
source={p:subprocess.check_output(['git','show',FIXED+':'+p],cwd=ROOT) for p in ['app/chain/__init__.py','app/api/endpoints/media.py']}
assert (ROOT/'app/chain/__init__.py').read_bytes()==source['app/chain/__init__.py']
owner=next(n for n in ast.parse(source['app/chain/__init__.py']).body if isinstance(n,ast.ClassDef) and n.name=='ChainBase')
method=next(copy.deepcopy(n) for n in owner.body if isinstance(n,ast.FunctionDef) and n.name=='media_category')
method.returns=None
scope={};exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-media-category','exec'),scope)
env['ns']['MediaChain'].media_category=scope['media_category']
endpoint=next(copy.deepcopy(n) for n in ast.parse(source['app/api/endpoints/media.py']).body if isinstance(n,ast.AsyncFunctionDef) and n.name=='category')
endpoint=ast.FunctionDef(name=endpoint.name,args=ast.arguments(posonlyargs=[],args=[],kwonlyargs=[],kw_defaults=[],defaults=[]),body=endpoint.body,decorator_list=[])
rows=[]
for label,value,fail in [('native',None,False),('override',{'电影':['插件电影'],'电视剧':['插件剧集']},False),('empty_object',{},False),('false',False,False),('zero',0,False),('empty_list',[],False),('invalid_list',['bad'],False),('throw',None,True)]:
 calls=[];messages=[]
 def plugin():
  calls.append('source')
  if fail:raise RuntimeError('owned category failure')
  return copy.deepcopy(value)
 def clone():calls.append('clone');return None
 class Core:
  def get_priority(self):return 0
  def get_name(self):return 'TheMovieDb'
  def media_category(self):calls.append('native');return {'电影':['原生电影'],'电视剧':['原生剧集']}
 obj=env['ns']['MediaChain']();obj.pluginmanager=NS(get_plugin_modules=lambda:{('source','Source'):{'media_category':plugin},('clone','Clone'):{'media_category':clone}});obj.modulemanager=NS(get_running_modules=lambda name:[Core()]);obj.messagehelper=NS(put=lambda **kw:messages.append(kw));obj.eventmanager=NS(send_event=lambda *a:None)
 endpointenv={'MediaChain':lambda:obj};exec(compile(ast.fix_missing_locations(ast.Module(body=[endpoint],type_ignores=[])),'fixed-category-endpoint','exec'),endpointenv)
 try: result=TypeAdapter(dict).validate_python(endpointenv['category']());error=None
 except Exception as e:result=None;error=type(e).__name__
 rows.append(dict(name=label,plugin=value,fail=fail,result=result,error=error,calls=calls,messages=len(messages)))
Path(__file__).with_name('media-category-source-oracle.json').write_text(json.dumps({'fixed_backend':FIXED,'source_sha256':{p:hashlib.sha256(v).hexdigest()for p,v in source.items()},'boundary':__doc__,'cases':rows},ensure_ascii=False,indent=2)+'\n')
print(len(rows),'fixed category cases')
