"""Two missing Location observations from unchanged original redirect-loop AST only."""
import ast,copy,hashlib,json,sys,traceback
from pathlib import Path
from types import SimpleNamespace
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
SOURCE=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot/app/helper/torrent.py')
def sha(b):return hashlib.sha256(b).hexdigest()
raw=SOURCE.read_bytes();assert sha(raw)=='1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5'
text=raw.decode();tree=ast.parse(text);cls=next(x for x in tree.body if isinstance(x,ast.ClassDef) and x.name=='TorrentHelper');method=next(x for x in cls.body if isinstance(x,ast.FunctionDef) and x.name=='download_torrent');loops=[x for x in ast.walk(method) if isinstance(x,ast.While) and "status_code" in ast.unparse(x.test)];assert len(loops)==1;loop=loops[0]
assert isinstance(loop.body[0],ast.Assign) and ast.unparse(loop.body[0])=="url = req.headers['Location']"
wrapper=ast.FunctionDef(name='selected_source_redirect_loop',args=ast.arguments(posonlyargs=[],args=[ast.arg(arg='req')],vararg=None,kwonlyargs=[],kw_defaults=[],kwarg=None,defaults=[]),body=[copy.deepcopy(loop)],decorator_list=[])
ns={'__builtins__':{}};exec(compile(ast.fix_missing_locations(ast.Module(body=[wrapper],type_ignores=[])),str(SOURCE),'exec'),ns)
cases=[]
for status in [301,302]:
 row={'input':{'status_code':status,'headers':{},'response_truthy':True},'dependency':'owned SimpleNamespace truthy response; not requests.Response'}
 try:row['unexpected_return']=ns['selected_source_redirect_loop'](SimpleNamespace(status_code=status,headers={}))
 except Exception as error:row['exception']={'class':type(error).__name__,'module':type(error).__module__,'args':list(error.args),'traceback':[{'filename':f.filename,'line':f.lineno,'name':f.name,'source':f.line} for f in traceback.extract_tb(error.__traceback__)]}
 cases.append(row)
fragment=ast.get_source_segment(text,loop)
assert SOURCE.read_bytes()==raw
print(json.dumps({'status':'OBSERVED_SELECTED_ORIGINAL_REDIRECT_LOOP','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':sha(raw),'driver_sha256':sha(Path(__file__).read_bytes()),'python':sys.version,'python_sha256':sha(Path(sys.executable).resolve().read_bytes()),'source_node':{'first_line':loop.lineno,'last_line':loop.end_lineno,'source':fragment,'source_sha256':sha(fragment.encode()),'ast_sha256':sha(ast.dump(loop,include_attributes=False).encode())},'cases':cases,'not_claimed':['fullhelper or wrapper execution','requests.Response truth implementation','HTTP/cache/parser/notifications','target result']},ensure_ascii=False,indent=2))
