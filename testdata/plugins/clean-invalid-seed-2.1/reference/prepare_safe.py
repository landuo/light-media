import ast,json,pathlib,subprocess,shlex
root=pathlib.Path(__file__).parent
source=ast.parse((root/'source.py').read_text());c=next(n for n in source.body if isinstance(n,ast.ClassDef) and n.name=='CleanInvalidSeed')
methods=[]
for n in c.body:
 if isinstance(n,ast.FunctionDef) and n.name in ('safe_hasattr','safe_getattr'):n.decorator_list=[];methods.append(ast.unparse(n))
cases=[]
for op in ('has','get'):
 for seq in [[{'value':3}],[{'value':None}],[{'error':'AttributeError'}],[{'error':'RuntimeError'}],[{'value':3},{'value':7}],[{'value':3},{'error':'AttributeError'}],[{'value':3},{'error':'RuntimeError'}]]:cases.append({'op':op,'sequence':seq})
script=r'''import sys,json,types,builtins
p=json.load(sys.stdin)
class Quiet:
 def __getattr__(self,n):return lambda *a,**kw:None
class Thread:
 def __init__(self,target):self.target=target
 def start(self):self.target()
 def join(self,t):pass
 def is_alive(self):return False
old=builtins.__import__
def imp(n,*a,**kw):return types.SimpleNamespace(Thread=Thread) if n=='threading' else old(n,*a,**kw)
e={'logger':Quiet(),'__builtins__':{**vars(builtins),'__import__':imp}}
for s in p['methods']:exec(s,e)
class Bag:
 def __init__(self,seq):self.seq=seq;self.reads=0
 def __getattr__(self,n):
  i=self.reads;self.reads+=1;v=self.seq[min(i,len(self.seq)-1)]
  if 'error' in v:raise getattr(builtins,v['error'])('synthetic')
  return v['value']
out=[]
for c in p['cases']:
 b=Bag(c['sequence']);r=e['safe_hasattr'](None,b,'test') if c['op']=='has' else e['safe_getattr'](None,b,'test','DEFAULT');out.append({'input':c,'expected':{'result':r,'reads':b.reads}})
json.dump(out,sys.stdout)'''
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','admin@192.168.8.129','docker exec -i moviepilot python3 -c '+shlex.quote(script)],input=json.dumps({'methods':methods,'cases':cases}),text=True,capture_output=True,check=True)
(root/'safe-oracle.json').write_text(json.dumps(json.loads(r.stdout),indent=2)+'\n');print(len(cases))
