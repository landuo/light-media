"""Read only pinned plugin source bytes/AST; never import the installed plugin."""
import ast,base64,hashlib,json,pathlib,subprocess,os,shlex
root=pathlib.Path(__file__).parent
manifest=json.loads((root/'frozen-source-manifest.json').read_text())
remote='''import ast,base64,hashlib,json,pathlib,importlib.metadata
items='''+repr(manifest)+'''
base=pathlib.Path('/app/app/plugins/mergemessagenotify')
class Strip(ast.NodeTransformer):
 def strip(self,n):
  self.generic_visit(n)
  if n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  return n
 visit_Module=strip
 visit_ClassDef=strip
 visit_FunctionDef=strip
 visit_AsyncFunctionDef=strip
out=[]
for item in items:
 p=base/item['path'];raw=p.read_bytes();sha=hashlib.sha256(raw).hexdigest()
 if sha!=item['source_sha256']:raise RuntimeError('pinned source changed: '+item['path'])
 tree=ast.parse(raw);portable=Strip().visit(ast.parse(raw))
 out.append({'path':item['path'],'sha256':sha,'bytes':len(raw),'source':base64.b64encode(raw).decode(),'ast':ast.unparse(portable),'ast_sha256':hashlib.sha256(ast.dump(portable,include_attributes=False).encode()).hexdigest(),'classes':{n.name:[m.name for m in n.body if isinstance(m,ast.FunctionDef)] for n in tree.body if isinstance(n,ast.ClassDef)}})
p=pathlib.Path('/app/app/schemas/types.py');raw=p.read_bytes();tree=ast.parse(raw);enums={}
for n in tree.body:
 if isinstance(n,ast.ClassDef) and n.name in ('NotificationType','EventType'):enums[n.name]=ast.unparse(n)
versions={}
for name in ('mako','apprise','requests','cryptography','pytz'):
 try:versions[name]=importlib.metadata.version(name)
 except importlib.metadata.PackageNotFoundError:versions[name]=None
print(json.dumps({'files':out,'types_source_sha256':hashlib.sha256(raw).hexdigest(),'enums':enums,'versions':versions}))
'''
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,shlex.join(['docker','exec','-i',container,'python3','-'])],input=remote,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
for item in d['files']:
 source=base64.b64decode(item.pop('source'));p=root/'source'/item['path'];p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(source)
 portable=item.pop('ast');p=root/'portable'/item['path'];p.parent.mkdir(parents=True,exist_ok=True);p.write_text(portable+'\n')
for n,s in d.pop('enums').items():(root/(n+'.py')).write_text(s+'\n')
(root/'installed-reference.json').write_text(json.dumps(d,indent=2)+'\n')
print(json.dumps({'files':len(d['files']),'source_bytes':sum(i['bytes'] for i in d['files']),'versions':d['versions'],'types_source_sha256':d['types_source_sha256']},indent=2))
