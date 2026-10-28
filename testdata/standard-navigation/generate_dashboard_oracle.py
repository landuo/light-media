"""Exact fixed manager/schema AST, Pydantic2.13.4, owned callbacks only."""
import ast,hashlib,inspect,json
from pathlib import Path
from types import SimpleNamespace
from typing import Optional,List,Dict,Any,Callable,Tuple
import pydantic
from pydantic import BaseModel,Field
from fastapi import HTTPException,status
root=Path('/Users/accidia/projects/MoviePilot');manager=root/'app/core/plugin.py';schema=root/'app/schemas/plugin.py'
ns=dict(Optional=Optional,List=List,Dict=Dict,Any=Any,BaseModel=BaseModel,Field=Field)
st=ast.parse(schema.read_text());classes=[n for n in st.body if isinstance(n,ast.ClassDef) and n.name in ['Plugin','PluginDashboard','PluginSidebarNavItem']]
exec(compile(ast.fix_missing_locations(ast.Module(body=classes,type_ignores=[])),'<fixed-dashboard-schema>','exec'),ns)
mt=ast.parse(manager.read_text());cls=next(n for n in mt.body if isinstance(n,ast.ClassDef) and n.name=='PluginManager');fn=next(n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='get_plugin_dashboard')
ns.update(inspect=inspect,Callable=Callable,Tuple=Tuple,schemas=SimpleNamespace(PluginDashboard=ns['PluginDashboard']),HTTPException=HTTPException,status=status,logger=SimpleNamespace(error=lambda *a:None))
exec(compile(ast.fix_missing_locations(ast.Module(body=[fn],type_ignores=[])),'<fixed-dashboard-manager>','exec'),ns)
inputs=[('normal',[{'md':8},{'refresh':2},[]]),('falsey-cols-attrs-and-null-elements',[[],False,None]),('nonempty-array-cols',[[{'md':8}],{},[]]),('non-dict-attrs',[{},'bad',[]]),('huge-numeric-cols',[1<<1100,{},[]]),('non-dict-elements',[{},{},['bad']]),('null-elements',[{},{},None]),('wrong-pair',[{},{}]),('no-widget',None)]
cases=[]
for name,value in inputs:
 observed={}
 class Plugin:
  plugin_name='Owned custom dashboard twin'
  def get_render_mode(self):return 'vuetify',''
  def get_dashboard(self,key,user_agent):observed.update(key=key,user_agent=user_agent);return value
 try:
  result=ns['get_plugin_dashboard'](SimpleNamespace(running_plugins={'Owned_clone':Plugin()}),'Owned_clone','summary')
  expected={'status':200,'value':None if result is None else result.model_dump()}
 except Exception as e:expected={'status':500,'error':type(e).__name__}
 cases.append(dict(name=name,input=value,expected=expected,observed=observed))
defaults=ns['PluginDashboard']().model_dump()
output=dict(defaults=defaults,fixed_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',kind='fixed manager+PluginDashboard schema AST with actual Pydantic; synthetic callback; complete source DTO including inherited defaults',pydantic=pydantic.__version__,sources={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [manager,schema,root/'requirements.in']},cases=cases)
nav=json.loads(Path(__file__).with_name('source-oracle.json').read_text())
wire=[]
for c in nav['cases']:
 try:expected=dict(status=200,value=[ns['PluginSidebarNavItem'].model_validate(row).model_dump() for row in c['expected']])
 except Exception as e:expected=dict(status=500,error=type(e).__name__)
 wire.append(dict(name=c['name'],input=c['expected'],expected=expected))
output['sidebar_wire_cases']=wire
Path(__file__).with_name('dashboard-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');print('fixed manager/schema dashboard:',len(cases),'cases; Pydantic',pydantic.__version__)
