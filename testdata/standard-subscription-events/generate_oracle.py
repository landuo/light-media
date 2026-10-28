"""Replay only pinned SubscribeModifiedEventData._diff_fields; no app import."""
import ast,hashlib,json,subprocess,sys
from pathlib import Path
commit='8a83efeacd3e26b82be495e17d50146e31c96a60';raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/schemas/event.py'])
cls=next(n for n in ast.parse(raw).body if isinstance(n,ast.ClassDef) and n.name=='SubscribeModifiedEventData')
method=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='_diff_fields');method.decorator_list=[]
mod=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),method],type_ignores=[]);ns={};exec(compile(ast.fix_missing_locations(mod),'app/schemas/event.py','exec'),ns)
cases=[({},{}),({'gone':None},{}),({'flag':True,'zero':False,'nested':[True,{'n':1.0}]},{'flag':1,'zero':0,'nested':[1,{'n':1}]}),({'id':1,'state':'R','custom_words':'old','total_episode':2},{'id':1,'state':'S','custom_words':'new','total_episode':3}),({'x':9007199254740992},{'x':9007199254740993}),({'z':[],'a':{}},{'z':None,'a':False}),({'x':0.1},{'x':0.10000000000000001}),({'removed':1},{'added':2}),({'nested':{'old':None}},{'nested':{}})]
out={'source_commit':commit,'path':'app/schemas/event.py','sha256':hashlib.sha256(raw).hexdigest(),'kind':'actual pinned AST; synthetic snapshot input values','cases':[{'before':old,'after':new,'fields':ns['_diff_fields'](old,new)} for old,new in cases]}
model_raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/db/models/subscribe.py'])
model=next(n for n in ast.parse(model_raw).body if isinstance(n,ast.ClassDef) and n.name=='Subscribe')
out['model_sha256']=hashlib.sha256(model_raw).hexdigest()
out['columns']=sorted(t.id for n in model.body if isinstance(n,ast.Assign) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Name) and n.value.func.id in ['Column','get_id_column'] for t in n.targets if isinstance(t,ast.Name))
schema=ast.parse(raw)
check=next(n for n in schema.body if isinstance(n,ast.ClassDef) and n.name=='SubscribeCompletionCheckEventData')
out['completion_check_defaults']={n.target.id:next(k.value.value for k in n.value.keywords if k.arg=='default') for n in check.body if isinstance(n,ast.AnnAssign) and isinstance(n.value,ast.Call) and isinstance(n.target,ast.Name)}
chain_raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/chain/subscribe.py'])
finish=next(n for n in ast.walk(ast.parse(chain_raw)) if isinstance(n,ast.FunctionDef) and n.name=='__finish_subscribe')
ordered=[]
for n in ast.walk(finish):
 if not isinstance(n,ast.Call) or not isinstance(n.func,ast.Attribute):continue
 name=n.func.attr
 if name in ['add_history','delete','post_message','sub_done_async']:ordered.append((n.lineno,name))
 elif name=='send_event' and n.args:ordered.append((n.lineno,ast.unparse(n.args[0])))
out['completion_source']={'path':'app/chain/subscribe.py','sha256':hashlib.sha256(chain_raw).hexdigest(),'ordered_calls':[{'line':line,'name':name} for line,name in sorted(ordered)]}
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
