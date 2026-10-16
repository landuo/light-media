"""Read-only inventory of pinned source; never imports the application."""
import ast,hashlib,json,pathlib
p=pathlib.Path(__file__).parent
out={}
for name in ['__init__.py','client.py','crypto.py','schemas.py']:
 raw=(p/'source'/name).read_bytes(); tree=ast.parse(raw)
 classes=[]
 for node in tree.body:
  if not isinstance(node,ast.ClassDef): continue
  methods=[]
  for f in node.body:
   if isinstance(f,(ast.FunctionDef,ast.AsyncFunctionDef)):
    calls=[]
    for n in ast.walk(f):
     if isinstance(n,ast.Call): calls.append(ast.unparse(n.func))
    methods.append({'name':f.name,'line':f.lineno,'end_line':f.end_lineno,'async':isinstance(f,ast.AsyncFunctionDef),'signature':ast.unparse(f.args),'calls':list(dict.fromkeys(calls))})
  classes.append({'name':node.name,'methods':methods})
 out[name]={'sha256':hashlib.sha256(raw).hexdigest(),'classes':classes}
(p/'method-contracts.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
# The exact get_form return is literal, not a guessed frontend schema.
tree=ast.parse((p/'source/__init__.py').read_text()); c=next(n for n in tree.body if isinstance(n,ast.ClassDef));f=next(n for n in c.body if isinstance(n,ast.FunctionDef) and n.name=='get_form');value=ast.literal_eval(f.body[0].value)
(p.parent/'form.json').write_text(json.dumps({'form':value[0],'defaults':value[1]},ensure_ascii=False,separators=(',',':'))+'\n')
print(json.dumps({k:sum(len(c['methods']) for c in v['classes']) for k,v in out.items()}))
