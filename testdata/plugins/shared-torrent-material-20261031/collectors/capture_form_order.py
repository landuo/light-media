"""Execute ONLY three original form-input preparation nodes; no HTTP/imports/cache."""
import ast,copy,hashlib,json,re,sys
from pathlib import Path
HERE=Path(__file__).resolve().parent
SOURCE=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot/app/helper/torrent.py')
SHA='1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5'
def sha(raw):return hashlib.sha256(raw).hexdigest()
raw=SOURCE.read_bytes();assert sha(raw)==SHA
text=raw.decode('utf-8');tree=ast.parse(text)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='TorrentHelper')
fn=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='download_torrent')
assign=[n for n in ast.walk(fn) if isinstance(n,ast.Assign) and len(n.targets)==1 and isinstance(n.targets[0],ast.Name) and n.targets[0].id=='inputs'];assert len(assign)==1
init=[n for n in ast.walk(fn) if isinstance(n,ast.Assign) and len(n.targets)==1 and isinstance(n.targets[0],ast.Name) and n.targets[0].id=='data' and isinstance(n.value,ast.Dict) and not n.value.keys];assert len(init)==1
loop=[n for n in ast.walk(fn) if isinstance(n,ast.For) and isinstance(n.target,ast.Name) and n.target.id=='item' and isinstance(n.iter,ast.Name) and n.iter.id=='inputs'];assert len(loop)==1
nodes=[assign[0],init[0],loop[0]];bindings=[{'line':n.lineno,'end_line':n.end_lineno,'source':ast.get_source_segment(text,n),'source_sha256':sha(ast.get_source_segment(text,n).encode()),'ast_sha256':sha(ast.dump(n,include_attributes=False).encode())} for n in nodes]
owned=(HERE/'owned-input.html').read_bytes();environment={'__builtins__':{},'re':re,'form':('?',owned.decode())}
code=compile(ast.fix_missing_locations(ast.Module(body=copy.deepcopy(nodes),type_ignores=[])),str(SOURCE),'exec')
exec(code,environment)
result={'status':'OBSERVED_SELECTED_SOURCE_COMPONENT','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_path':str(SOURCE),'source_sha256':SHA,'driver_sha256':sha(Path(__file__).read_bytes()),'python':sys.version,'python_executable':str(Path(sys.executable).resolve()),'python_sha256':sha(Path(sys.executable).resolve().read_bytes()),'input_html':owned.decode(),'input_sha256':sha(owned),'selected_nodes':bindings,'regex_matches':environment['inputs'],'ordered_items':list(environment['data'].items()),'not_claimed':['full TorrentHelper invocation','requests import or runtime','actual HTTP body encoding/network/cache','target result']}
assert SOURCE.read_bytes()==raw and (HERE/'owned-input.html').read_bytes()==owned
print(json.dumps(result,ensure_ascii=False,indent=2))
