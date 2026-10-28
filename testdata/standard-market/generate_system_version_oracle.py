"""Execute fixed PluginHelper methods with packaging and owned versions only."""
import ast,hashlib,json,subprocess,sys
from pathlib import Path
from typing import Optional,Tuple
from packaging.version import Version,InvalidVersion
from packaging.specifiers import SpecifierSet,InvalidSpecifier
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):return subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+path])
raw=source('app/helper/plugin.py');tree=ast.parse(raw)
methods=[n for n in ast.walk(tree) if isinstance(n,ast.FunctionDef) and n.name in ['get_current_system_version','check_plugin_system_version']]
cls=ast.ClassDef(name='PluginHelper',bases=[],keywords=[],body=methods,decorator_list=[])
ns=dict(Optional=Optional,Tuple=Tuple,Version=Version,InvalidVersion=InvalidVersion,SpecifierSet=SpecifierSet,InvalidSpecifier=InvalidSpecifier,PLUGIN_SYSTEM_VERSION_FIELD='system_version',logger=type('Logger',(),{'error':lambda *a:None})())
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'fixed/app/helper/plugin.py','exec'),ns)
inputs=[None,'',' ',True,1,[],{},'>=2.12.0,<3','>=3','<=2.15.7','!=2.15.7','==2.15.*','!=2.15.*','~=2.15.0','~=2.15','~=2','==2.15.7+local','>=2.15.7+local','===2.15.7','===v2.15.7','===other','===','2.15.7','=2.15.7','*','>=2 || <3','>=2 <3','>=2,,<3',',', '==2.15.7.post1.*','==2.15.7a1.*','==2.15.7.dev1.*','==2.15.7+local.*','==2.*.7','>2.*','>=2.15.7rc1','<2.15.7.post1','<2.15.7','>2.15.7','!=2.15.7+other','===2.15.7+LOCAL','>2.15.7a1']
versions=['v2.15.7','invalid','2.15.7rc1','2.15.7.post1','2.15.7+local','1!2.15.7','2.15.7.dev1','2.15.7a1.post1','2.15.7.post1.dev1']
# Additional independent PEP440 combinations are evaluated by the fixed
# method itself; these are not assertions copied from the Go implementation.
inputs += [op+value for op in ['==','!=','>=','<=','>','<','~=','==='] for value in ['2.15.7','2.15.7.0','2.15.7a1','2.15.7rc1','2.15.7.post1','2.15.7.dev1','2.15.7a1.post1','2.15.7.post1.dev1','v2.15.7','2.15.7+local','1!2.15.7','2.15.*','2.15.7.post1.*','2.15.7a1.*']]
inputs=list({json.dumps(value,sort_keys=True):value for value in inputs}.values())
cases=[]
for host in versions:
 ns['APP_VERSION']=host
 for value in inputs:
  ok,message=ns['PluginHelper'].check_plugin_system_version({'system_version':value})
  cases.append(dict(host=host,value=value,compatible=ok,message=message))
Path(__file__).with_name('system-version-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_sha256=hashlib.sha256(raw).hexdigest(),fixed_version_source=source('version.py').decode().splitlines()[0],kind='actual fixed PluginHelper AST; real packaging; owned inputs; no production Python runtime',cases=cases),ensure_ascii=False,indent=2)+'\n')
