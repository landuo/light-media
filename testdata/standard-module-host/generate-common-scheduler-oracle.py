"""Fixed common scheduler registration and ChainBase wrapper/dispatch.
Clock registration arguments and the void wrapper are actual AST; only provider functions are owned.
"""
import ast,copy,hashlib,json,subprocess
from pathlib import Path
from types import SimpleNamespace as NS
FIXED='8a83efeacd3e26b82be495e17d50146e31c96a60';ROOT=Path('/Users/accidia/projects/MoviePilot')
base=Path(__file__).with_name('generate-metadata-module-oracle.py');env={'__file__':str(base)}
exec(compile(base.read_text().split('cases=[]')[0],str(base),'exec'),env)
source={p:subprocess.check_output(['git','show',FIXED+':'+p],cwd=ROOT) for p in ['app/chain/__init__.py','app/scheduler.py']}
assert (ROOT/'app/chain/__init__.py').read_bytes()==source['app/chain/__init__.py']
owner=next(n for n in ast.parse(source['app/chain/__init__.py']).body if isinstance(n,ast.ClassDef) and n.name=='ChainBase')
method=next(copy.deepcopy(n) for n in owner.body if isinstance(n,ast.FunctionDef) and n.name=='scheduler_job');method.returns=None
ns={};exec(compile(ast.fix_missing_locations(ast.Module(body=[method],type_ignores=[])),'fixed-common','exec'),ns);env['ns']['MediaChain'].scheduler_job=ns['scheduler_job']
registration=next(copy.deepcopy(n)for n in ast.walk(ast.parse(source['app/scheduler.py']))if isinstance(n,ast.Call)and isinstance(n.func,ast.Attribute)and n.func.attr=='add_job'and any(k.arg=='id'and isinstance(k.value,ast.Constant)and k.value.value=='scheduler_job'for k in n.keywords))
clock=[]
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.Expr(value=registration)],type_ignores=[])),'fixed-registration','exec'),{'self':NS(start='Scheduler.start',_scheduler=NS(add_job=lambda *a,**kw:clock.append({'args':a,'kwargs':kw})))})
rows=[]
for name,value,fail in [('none',None,False),('false',False,False),('list',[],False),('object',{},False),('throw',None,True)]:
 calls=[];messages=[]
 def plugin():
  calls.append('source')
  if fail:raise RuntimeError('owned timer failure')
  return copy.deepcopy(value)
 def clone():calls.append('clone');return None
 obj=env['ns']['MediaChain']();obj.pluginmanager=NS(get_plugin_modules=lambda:{('source','source'):{'scheduler_job':plugin},('clone','clone'):{'scheduler_job':clone}});obj.modulemanager=NS(get_running_modules=lambda name:[]);obj.messagehelper=NS(put=lambda **kw:messages.append(kw));obj.eventmanager=NS(send_event=lambda *a:None)
 result=obj.scheduler_job();rows.append(dict(name=name,plugin=value,fail=fail,result=result,calls=calls,messages=len(messages)))
Path(__file__).with_name('common-scheduler-source-oracle.json').write_text(json.dumps({'fixed_backend':FIXED,'source_sha256':{p:hashlib.sha256(v).hexdigest()for p,v in source.items()},'boundary':__doc__,'clock':clock,'cases':rows},ensure_ascii=False,indent=2)+'\n');print(len(rows),'fixed common scheduler cases',clock)
