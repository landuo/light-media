"""Read source only; never import/instantiate the installed plugin."""
import ast,hashlib,json,pathlib,subprocess
root=pathlib.Path(__file__).parent
remote=r'''import ast,hashlib,json,pathlib
p=pathlib.Path('/app/app/plugins/cleaninvalidseed/__init__.py');raw=p.read_bytes();tree=ast.parse(raw)
class Strip(ast.NodeTransformer):
 def strip(self,n):
  self.generic_visit(n)
  if n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  return n
 visit_Module=strip
 visit_ClassDef=strip
 visit_FunctionDef=strip
 visit_AsyncFunctionDef=strip
portable=Strip().visit(tree)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='CleanInvalidSeed')
v=next(ast.literal_eval(n.value) for n in cls.body if isinstance(n,ast.Assign) and any(isinstance(x,ast.Name) and x.id=='plugin_version' for x in n.targets))
print(json.dumps({'path':str(p),'sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'version':v,'portable_module':ast.unparse(portable),'module_ast_sha256':hashlib.sha256(ast.dump(portable,include_attributes=False).encode()).hexdigest(),'methods':{n.name:hashlib.sha256(ast.dump(n,include_attributes=False).encode()).hexdigest() for n in cls.body if isinstance(n,ast.FunctionDef)}}))
'''
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -'],input=remote,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
class Strip(ast.NodeTransformer):
 def strip(self,n):
  self.generic_visit(n)
  if n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  return n
 visit_Module=strip
 visit_ClassDef=strip
 visit_FunctionDef=strip
 visit_AsyncFunctionDef=strip
local=pathlib.Path('work/plugin-source/CleanInvalidSeed/__init__.py').read_bytes()
local_ast=ast.dump(Strip().visit(ast.parse(local)),include_attributes=False)
installed=d.pop('portable_module')
assert local_ast==ast.dump(ast.parse(installed),include_attributes=False),'installed executable AST differs'
d['local_source_sha256']=hashlib.sha256(local).hexdigest();d['local_executable_ast_matches']=True
(root/'source.py').write_text(installed+'\n')
(root/'installed-reference.json').write_text(json.dumps(d,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in d.items() if k!='methods'},ensure_ascii=False,indent=2))
