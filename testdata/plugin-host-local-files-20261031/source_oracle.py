import ast, hashlib, json, subprocess, tempfile
from pathlib import Path
from types import SimpleNamespace
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',commit+':app/plugins/__init__.py'],text=True)
tree=ast.parse(source)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='_PluginBase')
method=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='get_data_path')
with tempfile.TemporaryDirectory(dir='work/plugin-host-resources-20261031/tmp') as owned:
 namespace={'Path':Path,'Optional':__import__('typing').Optional,'settings':SimpleNamespace(PLUGIN_DATA_PATH=Path(owned)/'plugin-data')}
 wrapper=ast.ClassDef(name='_PluginBase',bases=[],keywords=[],body=[method],decorator_list=[])
 exec(compile(ast.fix_missing_locations(ast.Module(body=[wrapper],type_ignores=[])),'fixed-get-data-path','exec'),namespace)
 origin=type('OwnedLocalFiles',(namespace['_PluginBase'],),{})()
 clone=type('OwnedLocalFilesfiles',(namespace['_PluginBase'],),{})()
 a,b=origin.get_data_path(),clone.get_data_path()
 backup=Path(owned)/'backup';backup.mkdir()
 values={'a.torrent':b'owned synthetic torrent bytes\x00\xff','a.fastresume':b'owned synthetic resume bytes\x00\xff'}
 observed={}
 for name,value in values.items():
  f=backup/name;f.write_bytes(value);observed[name]={'exists':f.exists(),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()}
 temp=a/'a.torrent';temp.write_bytes((backup/'a.torrent').read_bytes())
 observed['temp_equal']=temp.read_bytes()==values['a.torrent'];observed['separate_namespaces']=a!=b and a.is_dir() and b.is_dir()
 try:(backup/'missing').read_bytes()
 except FileNotFoundError:observed['missing']='FileNotFoundError'
 temp.unlink();observed['backup_preserved']=all((backup/name).read_bytes()==value for name,value in values.items())
 out={'classification':'fixed Base AST method replay and Python Path runtime, not full MoviePilot startup','source_commit':commit,'source_symbol':'app/plugins/__init__.py:get_data_path','source_lines':[method.lineno,method.end_lineno],'source_sha256':hashlib.sha256(source.encode()).hexdigest(),'installed_plugin_reference':'testdata/plugins/torrent-transfer-1.10.3/torrenttransfer.py:785,825,836,844,857','observations':observed}
 Path('testdata/plugin-host-local-files-20261031/evidence/source-oracle.json').write_text(json.dumps(out,indent=2)+'\n')
 print(json.dumps(out))
