"""Fixed manager AST replay with owned objects; never imports the old host."""
import ast,hashlib,json
from pathlib import Path
from types import SimpleNamespace
from typing import List,Dict,Any
source=Path('/Users/accidia/projects/MoviePilot/app/core/plugin.py')
tree=ast.parse(source.read_text());cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='PluginManager')
fn=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='get_plugin_sidebar_nav')
ns=dict(List=List,Dict=Dict,Any=Any,ObjectUtils=SimpleNamespace(check_method=lambda x:True),logger=SimpleNamespace(warning=lambda *a:None,error=lambda *a:None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<fixed-sidebar33>','exec'),ns)
class Plugin:
 def __init__(self,rows,name):self.rows=rows;self.plugin_name=name
 def get_state(self):return True
 def get_render_mode(self):return 'vue',''
 def get_sidebar_nav(self):return self.rows
inputs=[
 ('defaults-and-filter','[null,{},"bad",{"key":"  details  "},{"nav_key":"bad/path"},{"nav_key":"bad key"}]'),
 ('python-strings-and-unicode-int','[{"nav_key":true,"order":"  +١_٢  ","title":[1],"icon":{"x":2}},{"nav_key":[true,null],"order":-2.9},{"nav_key":"zero","order":false}]'),
 ('hash-keys-not-asset-paths','[{"nav_key":"."},{"nav_key":".."},{"nav_key":"my\\ttool"},{"nav_key":"my\\\\tool"},{"nav_key":"a?b"},{"nav_key":"a#b"}]'),
 ('section-permission-order','[{"nav_key":"system","section":"invalid","permission":true,"order":"bad"},{"nav_key":"start","section":"START","permission":"admin","order":true},{"nav_key":"lower","section":"discovery","permission":"search","order":"1_000"}]'),
 ('huge-integer','[{"nav_key":"big","order":900719925474099312345},{"nav_key":"before","order":900719925474099312344}]'),
 ('partial-overflow-boundary','[{"nav_key":"first"},{"nav_key":"overflow","order":1e1000},{"nav_key":"never"}]'),
 ('dict-is-iterable-but-not-rows','{"nav_key":"ignored"}'),
 ('truthy-noniterable-is-caught','true'),
]
cases=[]
for name,raw in inputs:
 manager=SimpleNamespace(_running_plugins={'Owned_clone':Plugin(json.loads(raw),'Clone display name')})
 cases.append(dict(name=name,raw=raw,expected=ns['get_plugin_sidebar_nav'](manager)))
output=dict(fixed_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',source='app/core/plugin.py:1133',sha256=hashlib.sha256(source.read_bytes()).hexdigest(),kind='fixed manager AST; synthetic owned plugin objects',cases=cases)
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('fixed sidebar cases:',len(cases))
# Python unpacking accepts more than a typed Go SDK pair. Keep those source
# outcomes visible rather than incorrectly calling every non-string an error.
import posixpath
remote_names=['get_plugin_remote_entry','get_plugin_remotes']
remote_functions=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in remote_names]
remote_cls=ast.ClassDef(name='RemoteManager',bases=[],keywords=[],body=remote_functions,decorator_list=[])
ns.update(posixpath=posixpath,Optional=__import__('typing').Optional)
exec(compile(ast.fix_missing_locations(ast.Module(body=[remote_cls],type_ignores=[])),'<fixed-remotes31>','exec'),ns)
render_cases=[]
for name,value in [('one-member',['vue']),('three-members',['vue','assets','extra']),('null',None),('two-character-string','ab'),('nonstring-mode',[False,7]),('nonstring-root',['vue',7]),('two-key-dictionary',{'vue':'x','root':'y'}),('normal-pair',['vue','assets'])]:
 plugin=SimpleNamespace(get_render_mode=lambda value=value:value,plugin_name='Owned');manager=ns['RemoteManager']();manager._running_plugins={'Owned':plugin}
 try:expected=dict(rows=manager.get_plugin_remotes(),error=None)
 except Exception as e:expected=dict(rows=None,error=type(e).__name__)
 typed=isinstance(value,list) and len(value)==2 and all(isinstance(x,str) for x in value)
 render_cases.append(dict(name=name,input=value,expected=expected,strict_go_pair=typed,adaptation=None if typed or expected['error'] else 'Go SDK get_render_mode declares a two-string JSON pair; arbitrary Python iterable/non-string pairs cannot satisfy that typed protocol'))
output['render_cases']=render_cases
Path(__file__).with_name('source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n')
print('fixed render unpack cases:',len(render_cases))
