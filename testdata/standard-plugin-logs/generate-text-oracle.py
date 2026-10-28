"""Execute only the fixed full-text line reversal expression; no app import."""
import ast,hashlib,json,pathlib,sys
p=pathlib.Path(sys.argv[1] if len(sys.argv)>1 else '/Users/accidia/projects/MoviePilot/app/api/endpoints/system.py')
text=p.read_text();tree=ast.parse(text);function=next(n for n in tree.body if isinstance(n,ast.AsyncFunctionDef) and n.name=='get_logging')
expression=next(n.value for n in ast.walk(function) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='text' for t in n.targets) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Attribute) and n.value.func.attr=='join')
code=compile(ast.Expression(expression),'<fixed full log reversal>','eval')
cases=[]
for name,raw in [('multiline-message','【INFO】OWNED_TIMESTAMP - plugins - first\nTraceback (owned):\n  detail\n'),('two-records','【INFO】OWNED_TIMESTAMP - plugins - first\ncontinuation\n【ERROR】OWNED_TIMESTAMP - plugins - second\n'),('empty','')]:
 cases.append(dict(name=name,input=raw,expected=eval(code,{},dict(text=raw))))
output=dict(source=dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60',path='app/api/endpoints/system.py',sha256=hashlib.sha256(p.read_bytes()).hexdigest(),expression=ast.unparse(expression)),kind='fixed AST full-text expression; synthetic owned log file values',cases=cases)
pathlib.Path(__file__).with_name('text-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('fixed full-text cases',len(cases))
