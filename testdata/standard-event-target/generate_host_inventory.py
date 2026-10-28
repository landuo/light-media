"""Read-only pinned event producer inventory; not a runtime coverage claim."""
import ast,hashlib,json,subprocess,sys
from pathlib import Path
repo=sys.argv[1];commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
def read(path):return subprocess.check_output(['git','-C',repo,'show',commit+':'+path])
paths=subprocess.check_output(['git','-C',repo,'ls-tree','-r','--name-only',commit,'app']).decode().splitlines()
enums={}
raw_types=read('app/schemas/types.py')
for node in ast.parse(raw_types).body:
 if isinstance(node,ast.ClassDef) and node.name in ('EventType','ChainEventType'):
  for field in node.body:
   if isinstance(field,ast.Assign) and isinstance(field.value,ast.Constant):
    for target in field.targets:
     if isinstance(target,ast.Name):enums[node.name+'.'+target.id]=field.value.value
items=[];hashes={'app/schemas/types.py':hashlib.sha256(raw_types).hexdigest()}
for path in paths:
 if not path.endswith('.py'):continue
 raw=read(path);tree=ast.parse(raw)
 for node in ast.walk(tree):
  if not isinstance(node,ast.Call) or not isinstance(node.func,ast.Attribute) or node.func.attr not in ('send_event','async_send_event'):continue
  receiver=ast.unparse(node.func.value)
  if not (receiver.endswith('eventmanager') or receiver.endswith('event_manager')):continue
  kwargs={k.arg:k.value for k in node.keywords};event=kwargs.get('etype',node.args[0] if node.args else None);data=kwargs.get('data',node.args[1] if len(node.args)>1 else None)
  event_expr=ast.unparse(event) if event is not None else None;wire=enums.get(event_expr)
  excluded=wire in {'workflow.execute','workflow.execution','agent.tokens.usage','agent.llm.provider'} or any(part in path.split('/') for part in ('agent','workflow')) or path in ('app/api/endpoints/agent.py','app/api/endpoints/workflow.py')
  items.append({'path':path,'line':node.lineno,'method':node.func.attr,'event_expression':event_expr,'wire_type':wire,'event_kind':'chain' if event_expr and event_expr.startswith('ChainEventType.') else 'broadcast' if event_expr and event_expr.startswith('EventType.') else 'dynamic','data_expression':ast.unparse(data) if data is not None else None,'scope':'按用户要求排除' if excluded else '插件平台普通事件：待沿宿主调用链核实'})
  hashes[path]=hashlib.sha256(raw).hexdigest()
items.sort(key=lambda x:(x['path'],x['line']))
out={'source_commit':commit,'kind':'static AST source inventory only; no execution or coverage claim; supporting source manifest for unique matrix36/37','source_sha256':hashes,'total_source_calls':len(items),'in_scope_source_calls':sum(i['scope']!='按用户要求排除' for i in items),'excluded_source_calls':sum(i['scope']=='按用户要求排除' for i in items),'calls':items}
target=Path(__file__).resolve().parents[1]/'plugin-module-scoped-20261025/fixed-event-call-inventory.json';target.write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:out[k] for k in ['total_source_calls','in_scope_source_calls','excluded_source_calls']}))
