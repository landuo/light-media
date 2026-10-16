"""Read-only installed Python/re execution; no app imports/config or network probes."""
import json,pathlib,subprocess,hashlib
root=pathlib.Path(__file__).resolve().parent
reference=json.loads((root/'installed-source.json').read_text())
good='Active connections: 12\nserver accepts handled requests\n1000 995 12345\nReading: 2 Writing: 3 Waiting: 7\n'
cases=[('ascii',good),('python-special-case',good.replace('Active connections','Actİve connectıonſ').replace('Reading','Readİng').replace('Writing','Wrıtİng').replace('Waiting','Waİtıng')),('unicode15-decimal',good.translate(str.maketrans('0123456789',''.join(chr(0x1e4f0+i) for i in range(10))))),('unicode16-decimal',good.translate(str.maketrans('0123456789',''.join(chr(0x10d40+i) for i in range(10))))),('superscript-digit',good.replace('12','¹²')),('circled-digit',good.replace('12','①②')),('python-extra-whitespace',good.replace(': ',':\x1f')),('not-python-whitespace',good.replace(': ',':\u180e'))]
remote="""import ast,hashlib,json,pathlib,re,sys,unicodedata,typing
p=pathlib.Path('/app/app/plugins/mpserverstatus/__init__.py'); raw=p.read_bytes()
c=next(n for n in ast.parse(raw).body if isinstance(n,ast.ClassDef) and n.name=='MPServerStatus')
f=next(n for n in c.body if isinstance(n,ast.FunctionDef) and n.name=='_parse_status_text')
f.decorator_list=[]
ns={'re':re,'Dict':typing.Dict}
exec(compile(ast.Module(body=[f],type_ignores=[]),'<installed-parse-status-only>','exec'),ns)
inputs=json.loads(%r)
out=[]
for name,text in inputs:
    row={'name':name,'input':text}
    try: row['expected']=ns['_parse_status_text'](text)
    except Exception as e: row['error']=str(e)
    out.append(row)
print(json.dumps({'provenance':{'source_sha256':hashlib.sha256(raw).hexdigest(),'python':sys.version,'unicode':unicodedata.unidata_version,'regex_module':'stdlib re','execution':'actual installed parser AST; stdlib only; no app imports/config/network probes'},'cases':out},ensure_ascii=False))
""" % json.dumps(cases,ensure_ascii=False)
p=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129','docker exec -i moviepilot python -'],input=remote,text=True,capture_output=True,check=True)
result=json.loads(p.stdout)
assert result['provenance']['source_sha256']==reference['source_sha256']
assert result['provenance']['unicode']=='15.0.0'
(root.parent/'server-status-unicode-original-reference.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(result['cases']),'unicode':result['provenance']['unicode'],'sha256':hashlib.sha256((root.parent/'server-status-unicode-original-reference.json').read_bytes()).hexdigest()}))
