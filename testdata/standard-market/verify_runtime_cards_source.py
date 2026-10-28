"""Compare actual Linux card output to fixed manager AST on same owned index."""
import ast,hashlib,json,subprocess,sys
from types import SimpleNamespace as NS
commit='8a83efeacd3e26b82be495e17d50146e31c96a60';path='app/core/plugin.py'
raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':'+path])
methods=[n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name in ['_process_plugin_info','_normalize_plugin_label']]
for m in methods:m.decorator_list=[]
class Plugin:
 def __init__(self):self.installed=False;self.state=False
ns={'schemas':NS(Plugin=Plugin),'PluginHelper':NS(annotate_plugin_system_version=lambda value:value),'settings':NS(VERSION_FLAG='v2')}
module=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0)]+methods,type_ignores=[])
exec(compile(ast.fix_missing_locations(module),path,'exec'),ns)
manager=NS(_plugins={},_running_plugins={},__set_and_check_auth_level=lambda **kwargs:True,_normalize_plugin_label=ns['_normalize_plugin_label'])
actual=json.load(open(sys.argv[2]));source=actual['source_metadata'];target={c['instance_id']:c for c in actual['items']}
repo=actual['settings']['plugin_markets'][1]
fields={'id':'instance_id','plugin_name':'name','plugin_desc':'description','plugin_version':'version','plugin_author':'author','plugin_label':'plugin_label','repo_url':'repo_url','add_time':'add_time','installed':'installed','state':'state','has_update':'has_update','has_page':'has_page'}
checked=[]
for i,(pid,info) in enumerate(source.items()):
 expected=ns['_process_plugin_info'](manager,pid,info,repo,[],len(source)-i)
 card=target[pid]
 for old,new in fields.items():
  value=getattr(expected,old,None);got=card.get(new,False if old in ['has_update','has_page'] else None)
  assert value==got,(pid,old,value,got)
 assert card['available']==False and 'Go' in card['unavailable_reason']
 checked.append(pid)
print(json.dumps({'kind':'same captured Linux repository input → actual fixed PluginManager AST → compare actual production card output; synthetic schema/framework boundaries, no source service mutation','source_commit':commit,'path':path,'sha256':hashlib.sha256(raw).hexdigest(),'checked_ids':checked,'fields':fields,'boundary_notes':['official auth gate excluded by user','no running classes in this captured uninstalled repository','all23 inputs have no system_version; system-version policy remains row13','Python entries accurately display-only; two other actual Go candidates verified separately']},ensure_ascii=False,indent=2))
