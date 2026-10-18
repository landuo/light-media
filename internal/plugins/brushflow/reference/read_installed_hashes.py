import pathlib,hashlib,ast,json,signal
signal.alarm(12)
items=[]
for name in ['brushflowlanduo','torrenttransfer','torrenttransfermusic']:
 root=pathlib.Path('/app/app/plugins')/name
 files=[]
 if root.is_dir():
  for p in sorted(root.rglob('*.py')):
   if '__pycache__'in p.parts:continue
   raw=p.read_bytes();files.append({'path':str(p.relative_to(root)),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest()})
  versions=[]
  entry=root/'__init__.py'
  if entry.is_file():
   for n in ast.walk(ast.parse(entry.read_bytes())):
    if isinstance(n,ast.Assign)and any(isinstance(t,ast.Name)and t.id=='plugin_version'for t in n.targets):
     try:versions.append(ast.literal_eval(n.value))
     except Exception:pass
  items.append({'plugin':name,'versions':versions,'files':files})
 else:items.append({'plugin':name,'missing':True})
print(json.dumps({'scope':'fixed source Python files only; no application import/config/KV','items':items}))
