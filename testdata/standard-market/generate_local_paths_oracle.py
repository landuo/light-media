"""Run pinned local path parsing; nonexistent paths and duplicates are valid."""
import ast,hashlib,json,subprocess,sys,tempfile,pwd,os
from pathlib import Path
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60';raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/helper/plugin.py'])
method=next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='get_local_repo_paths');method.decorator_list=[]
module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),method],type_ignores=[])
ns={'Path':Path};exec(compile(ast.fix_missing_locations(module),'fixed/helper/plugin.py','exec'),ns)
cases=[]
for text in [None,'',' , , ','relative/future',' ./a/../future , /owned/shared , /owned/shared ','/owned/one，two','/owned/a\nb','/owned/a, /owned/a', '/owned/no-parent/no-child']:
 ns['settings']=NS(PLUGIN_LOCAL_REPO_PATHS=text,ROOT_PATH=Path('/owned/plugin-root'))
 paths=ns['get_local_repo_paths']();cases.append(dict(value=text,root='/owned/plugin-root',paths=[str(p) for p in paths]))
identity=pwd.getpwuid(os.getuid())
for text in ['~/future','~'+identity.pw_name+'/future']:
 ns['settings']=NS(PLUGIN_LOCAL_REPO_PATHS=text,ROOT_PATH=Path('/owned/plugin-root'))
 expected=[str(p) for p in ns['get_local_repo_paths']()]
 prefix=str(Path.home().resolve()) if text.startswith('~/') else str(Path(identity.pw_dir).resolve())
 marker='${home}' if text.startswith('~/') else '${named_home}'
 cases.append(dict(value=text.replace('~'+identity.pw_name,'~${username}'),root='/owned/plugin-root',paths=[value.replace(prefix,marker,1) for value in expected]))
filesystem=[]
with tempfile.TemporaryDirectory() as temporary:
 root=Path(temporary).resolve();(root/'target/nested').mkdir(parents=True)
 (root/'link').symlink_to('target/nested',target_is_directory=True)
 (root/'dangling').symlink_to('future-parent',target_is_directory=True)
 (root/'regular').write_text('owned inert file')
 for text in ['link/../future','missing/../link/child','dangling/child','regular/child']:
  ns['settings']=NS(PLUGIN_LOCAL_REPO_PATHS=text,ROOT_PATH=root)
  filesystem.append(dict(value=text,paths=[str(p).replace(str(root),'${root}') for p in ns['get_local_repo_paths']()]))
Path(__file__).with_name('local-paths-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256=hashlib.sha256(raw).hexdigest(),kind='actual fixed method AST and native pathlib; nonexistent owned synthetic paths; no filesystem writes',cases=cases,filesystem=filesystem),ensure_ascii=False,indent=2)+'\n')
