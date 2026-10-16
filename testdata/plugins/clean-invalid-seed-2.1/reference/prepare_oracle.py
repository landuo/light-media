import ast,copy,datetime,json,pathlib,types,builtins,hashlib,subprocess
root=pathlib.Path(__file__).parent
# Only requested source methods are executed; original imports/plugin registration
# are omitted. All side-effect dependencies below are synthetic.
source=(root/'source.py').read_text();tree=ast.parse(source)
klass=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='CleanInvalidSeed')
for n in klass.body:
 if isinstance(n,ast.FunctionDef):n.decorator_list=[d for d in n.decorator_list if isinstance(d,ast.Name) and d.id in ('property','staticmethod')]
module=ast.Module(body=[klass],type_ignores=[])
# Utility bodies are extracted read-only from installed source, not rewritten.
remote="""import ast,json,pathlib,hashlib\np=pathlib.Path('/app/app/utils/string.py');raw=p.read_bytes();t=ast.parse(raw);c=next(n for n in t.body if isinstance(n,ast.ClassDef) and n.name=='StringUtils');names=('get_url_netloc','str_filesize');print(json.dumps({'sha256':hashlib.sha256(raw).hexdigest(),'methods':{n.name:ast.unparse(n) for n in c.body if isinstance(n,ast.FunctionDef) and n.name in names}}))"""
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -'],input=remote,text=True,capture_output=True,check=True)
(root/'stringutils-reference.json').write_text(r.stdout)
# Generation/execution is on actual installed Python; all dependencies are fakes.
script=pathlib.Path(__file__).with_name('oracle_runtime.py').read_text()
payload={'class_source':ast.unparse(module),'utils':json.loads(r.stdout),'cases':json.loads((root/'cases.json').read_text())}
run=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -c '+__import__('shlex').quote(script)],input=json.dumps(payload),text=True,capture_output=True,check=True)
d=json.loads(run.stdout)
(root/'oracle.json').write_text(json.dumps(d,ensure_ascii=False,indent=2)+'\n')
(root.parent/'form.json').write_text(json.dumps(d['form'],ensure_ascii=False,separators=(',',':'))+'\n')
(root.parent/'commands.json').write_text(json.dumps(d['commands'],ensure_ascii=False,separators=(',',':'))+'\n')
print(json.dumps({'cases':len(d['cases']),'helper_cases':len(d['helpers']),'errors':[(v['id'],[s['error'] for s in v['expected']['steps'] if s['error']]) for v in d['cases'] if any(s['error'] for s in v['expected']['steps'])],'python':d['python']},ensure_ascii=False,indent=2))
