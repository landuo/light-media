"""Read-only stdlib AST inspection of exactly the previously bound 77 files."""
import ast,hashlib,json,re,sys
from pathlib import Path
expected=json.loads(sys.stdin.readline())
root=Path('/app/app/plugins');results=[]
def name(node):
 if isinstance(node,ast.Name):return node.id
 if isinstance(node,ast.Attribute):
  parent=name(node.value)
  return parent+'.'+node.attr if parent else '.'+node.attr
 if isinstance(node,ast.Call):return (name(node.func)or'call')+'()'
 if isinstance(node,ast.Subscript):return (name(node.value)or'item')+'[]'
 return type(node).__name__
def safe(node):
 if isinstance(node,ast.Constant):
  if node.value is None or isinstance(node.value,(bool,int,float)):return {'constant':node.value}
  if isinstance(node.value,str)and (re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,95}',node.value)or node.value.startswith('app.plugins.')or node.value in {'http://','https://','github.com','raw.githubusercontent.com'}):return {'identifier_literal':node.value}
  return {'constant_type':type(node.value).__name__}
 if isinstance(node,ast.Call):return {'call':name(node.func),'args':[safe(x)for x in node.args],'keywords':{x.arg or'**':safe(x.value)for x in node.keywords},'receiver':safe(node.func.value)if isinstance(node.func,ast.Attribute)and isinstance(node.func.value,ast.Call)else None}
 if isinstance(node,(ast.List,ast.Tuple,ast.Set)):return {'sequence':[safe(x)for x in node.elts]}
 if isinstance(node,(ast.If,ast.Compare,ast.BoolOp,ast.IfExp,ast.Return,ast.Assign,ast.keyword,ast.JoinedStr,ast.FormattedValue)):
  return {'node':type(node).__name__,'fields':{k:([safe(x)for x in value]if isinstance(value,list)else safe(value))for k,value in ast.iter_fields(node)if isinstance(value,(ast.AST,list))and k not in {'ctx','type_comment'}}}
 return {'expression':name(node)}
for row in expected:
 p=root/row['file'];raw=p.read_bytes();digest=hashlib.sha256(raw).hexdigest();out={'file':row['file'],'plugin':row['plugin'],'expected_sha256':row['sha256'],'sha256':digest,'matches':digest==row['sha256']}
 if not out['matches']:results.append(out);continue
 tree=ast.parse(raw.decode('utf-8-sig'));parents={id(child):parent for parent in ast.walk(tree)for child in ast.iter_child_nodes(parent)}
 imports=[];settings_names={'settings'}
 for n in ast.walk(tree):
  if isinstance(n,ast.ImportFrom):
   for a in n.names:
    imports.append({'line':n.lineno,'module':n.module,'name':a.name,'alias':a.asname})
    if n.module=='app.core.config'and a.name=='settings':settings_names.add(a.asname or a.name)
 attrs=[];dynamic=[];factories=[];focus=[]
 for n in ast.walk(tree):
  if isinstance(n,ast.Attribute)and isinstance(n.value,ast.Name)and n.value.id in settings_names:
   parent=parents.get(id(n));ancestor=parent;owner=None
   while ancestor is not None:
    if isinstance(ancestor,(ast.FunctionDef,ast.AsyncFunctionDef)):
     owner=ancestor.name;break
    ancestor=parents.get(id(ancestor))
   attrs.append({'line':n.lineno,'attribute':n.attr,'owner':owner,'parent_type':type(parent).__name__})
  if not isinstance(n,ast.Call):continue
  callee=name(n.func)
  if isinstance(n.func,ast.Name)and n.func.id in {'getattr','hasattr','setattr'}:
   parent=parents.get(id(n));dynamic.append({'line':n.lineno,'callee':callee,'arguments':[safe(x)for x in n.args],'called_result':isinstance(parent,ast.Call)and parent.func is n,'call_keywords':{k.arg or'**':safe(k.value)for k in parent.keywords}if isinstance(parent,ast.Call)and parent.func is n else {}})
  if any(term in callee for term in ['Helper','Factory','ModuleHelper','ObjectUtils','SystemUtils']):factories.append({'line':n.lineno,'callee':callee,'args':[safe(x)for x in n.args],'keywords':{x.arg or'**':safe(x.value)for x in n.keywords}})
  if any(term in callee for term in ['PlaywrightHelper','OcrHelper','SystemHelper','systemconfig','StorageChain']):focus.append({'line':n.lineno,'callee':callee,'args':[safe(x)for x in n.args],'keywords':{x.arg or'**':safe(x.value)for x in n.keywords}})
 functions=[{'name':n.name,'line':n.lineno,'end_line':n.end_lineno,'body':[safe(x)for x in n.body]}for n in ast.walk(tree)if isinstance(n,ast.FunctionDef)and (row['file']=='remoteidentifiers/__init__.py'and n.name=='__get_response_text')]
 out.update(focused_functions=functions,imports=imports,settings_attributes=sorted(attrs,key=lambda x:x['line']),dynamic_calls=sorted(dynamic,key=lambda x:x['line']),factory_calls=sorted(factories,key=lambda x:x['line']),focus_calls=sorted(focus,key=lambda x:x['line']));results.append(out)
print(json.dumps({'classification':'read-only installed source AST: no app/plugin imports or runtime/config/DB/network operations; arbitrary literals redacted','files':results},ensure_ascii=False))
