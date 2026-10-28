"""Fixed clone frontend functions executed on temporary files only."""
import ast,hashlib,json,tempfile
from pathlib import Path
from types import SimpleNamespace
from typing import Tuple
root=Path('/Users/accidia/projects/MoviePilot');p=root/'app/core/plugin.py';source=p.read_text();tree=ast.parse(source);klass=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='PluginManager')
names=['_modify_federation_files','_rename_federation_assets'];functions=[n for n in klass.body if isinstance(n,ast.FunctionDef) and n.name in names]
ns={'Path':Path,'Tuple':Tuple,'logger':SimpleNamespace(debug=lambda *a,**k:None,warning=lambda *a,**k:None,error=lambda *a,**k:None)}
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Fixed',bases=[],keywords=[],body=functions,decorator_list=[])],type_ignores=[])),'<fixed-clone-frontend>','exec'),ns)
inputs=[
 ('global-JS-CSS-key-class-and-path',{'remoteEntry.js':b'window.MoviePilotAPI.get("plugin/Owned/state"); const css__Owned__="owned-a.css"; const mixed="Owned"; const lower="owned"; const runtime="Ow"+"ned";window.MoviePilotAPI.get("plugin/"+runtime+"/state");', 'owned-a.css':b'.owned .Owned { --owned-color:purple }', 'owned-a.js':b'export const path="./owned-a.css";', 'other.txt':b'Owned owned'}),
 ('nested-content-only-top-level-renames',{'assets/owned-chunk.js':b'const id="Owned"; const css="./owned.css";', 'assets/owned.css':b'.owned {}','owned-root.js':b'import "./assets/owned-chunk.js"'}),
 ('case-condition-lowercase-replace-only',{'Owned-Chunk.js':b'Owned owned','OWNED.css':b'.Owned .owned {}','owned2.css':b'.Owned {}'}),
 ('rename-collision-does-not-overwrite',{'owned.js':b'Owned source','ownedcopy.js':b'Ownedcopy preexisting'}),
 ('UTF8-replacement-and-universal-newlines',{'owned.js':b'Owned\r\nowned\r\xe1\x80\xff\xff','owned.css':b'.Owned\r\n.owned\r\xed\xa0\x80'}),
 ('case-sensitive-extensions',{'owned.JS':b'Owned owned','owned.CSS':b'Owned owned','owned.json':b'Owned owned'}),
 ('all-lowercase-class',{'owned.js':b'owned css__owned__ ownedcopy', 'owned.css':b'.owned {}'}),
 ('suffix-contains-lowercase-source',{'owned.js':b'Owned owned css__Owned__', 'owned.css':b'.Owned .owned {}'}),
 ('empty-root',{}),
]
cases=[]
for name,files in inputs:
 with tempfile.TemporaryDirectory(prefix='fixed-clone-frontend-') as folder:
  dist=Path(folder)
  for relative,raw in files.items():
   f=dist/relative;f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(raw)
  original='owned' if name=='all-lowercase-class' else 'Owned'
  target='Ownedowned' if name=='suffix-contains-lowercase-source' else original+'copy'
  status=ns['Fixed']()._modify_federation_files(dist,original,target)
  result={str(f.relative_to(dist)):f.read_bytes().hex() for f in sorted(dist.rglob('*')) if f.is_file()}
  cases.append({'name':name,'source_id':original,'clone_id':target,'input_hex':{k:v.hex() for k,v in files.items()},'status':list(status),'expected_hex':result})
output={'fixed_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source':str(p.relative_to(root)),'sha256':hashlib.sha256(source.encode()).hexdigest(),'kind':'actual fixed clone frontend/rename AST with real owned temporary filesystem; no old project mutation','source_call_gate':'_modify_plugin_files:2044 only calls when plugin_dir/dist exists; Go declared Frontend.Root mapping must be explicitly documented','cases':cases}
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');print(len(cases),'fixed clone frontend tree scenarios')
