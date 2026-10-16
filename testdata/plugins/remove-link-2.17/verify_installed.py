import ast,hashlib,json,pathlib,subprocess
root=pathlib.Path(__file__).parent
remote=r'''import ast,hashlib,importlib.util,json,pathlib
raw=pathlib.Path('/app/app/plugins/removelink/__init__.py').read_bytes()
tree=ast.parse(raw)
class Strip(ast.NodeTransformer):
 def visit_FunctionDef(self,n):
  self.generic_visit(n)
  if n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  return n
methods={}
portable={}
for n in tree.body:
 if isinstance(n,(ast.ClassDef,ast.FunctionDef)):
  values=n.body if isinstance(n,ast.ClassDef) else [n]
  for f in values:
   if isinstance(f,ast.FunctionDef):
    name=(n.name+'.' if isinstance(n,ast.ClassDef) else '')+f.name
    stripped=Strip().visit(f)
    methods[name]=hashlib.sha256(ast.dump(stripped,include_attributes=False).encode()).hexdigest()
    portable[name]=ast.unparse(stripped)
klass=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='RemoveLink')
version=next(ast.literal_eval(n.value) for n in klass.body if isinstance(n,ast.Assign) and any(isinstance(x,ast.Name) and x.id=='plugin_version' for x in n.targets))
print(json.dumps({'path':'/app/app/plugins/removelink/__init__.py','sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'version':version,'method_ast_sha256':methods,'portable_methods':portable,'watchdog_available':importlib.util.find_spec('watchdog') is not None,'watchfiles_available':importlib.util.find_spec('watchfiles') is not None}))
'''
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -'],input=remote,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
tree=ast.parse((root/'source.py').read_text())
class Strip(ast.NodeTransformer):
 def visit_FunctionDef(self,n):
  self.generic_visit(n)
  if n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  return n
methods={}
for n in tree.body:
 if isinstance(n,(ast.ClassDef,ast.FunctionDef)):
  for f in n.body if isinstance(n,ast.ClassDef) else [n]:
   if isinstance(f,ast.FunctionDef):
    name=(n.name+'.' if isinstance(n,ast.ClassDef) else '')+f.name
    methods[name]=hashlib.sha256(ast.dump(Strip().visit(f),include_attributes=False).encode()).hexdigest()
live={k:hashlib.sha256(ast.dump(ast.parse(v).body[0],include_attributes=False).encode()).hexdigest() for k,v in d.pop('portable_methods').items()}
assert methods==live, 'live executable method AST differs'
d['comparable_method_ast_sha256']=live
d['local_sanitized_executable_ast_matches']=True
(root/'installed-reference.json').write_text(json.dumps(d,indent=2)+'\n')
print(json.dumps({k:v for k,v in d.items() if k!='method_ast_sha256'},indent=2))
