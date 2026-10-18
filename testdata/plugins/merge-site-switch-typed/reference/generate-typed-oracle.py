import ast, json, copy, hashlib
from pathlib import Path
from typing import Any, List, Dict, Tuple
from types import SimpleNamespace

source=(Path(__file__).parent / '__init__.py').read_text()
tree=ast.parse(source)
cls=next(n for n in tree.body if isinstance(n,ast.ClassDef))
cls.bases=[];cls.keywords=[]
body=[]
for n in cls.body:
    if isinstance(n,ast.FunctionDef):
        n.decorator_list=[d for d in n.decorator_list if isinstance(d,ast.Name) and d.id in ('classmethod','staticmethod')]
        body.append(n)
    elif isinstance(n,(ast.Assign,ast.AnnAssign)):
        try:ast.literal_eval(n.value);body.append(n)
        except (ValueError,TypeError):pass
cls.body=body
class Logger:
    def __getattr__(self,k):return lambda *a,**kw:None
logger=Logger()
class Keys:
    UserInstalledPlugins='installed';IndexerSites='search';RssSites='rss'
SystemConfigKey=Keys
class Event:pass
class PluginManager:
    def init_plugin(self,plugin_id,config):reloads.append(plugin_id)
class Scheduler:
    def update_plugin_job(self,plugin_id):pass
exec(compile(ast.Module(body=[cls],type_ignores=[]),'<original-merge-sites>','exec'))
all_plugins=['AutoSignIn','SiteStatistic','IYUUAutoSeed','BrushFlow','CrossSeed','CustomSites']
class SiteOper:
    def __init__(self,rows):self.rows=copy.deepcopy(rows)
    def list_order_by_pri(self):return [SimpleNamespace(**row) for row in self.rows]
    def update(self,site_id,patch):
        for row in self.rows:
            if row['id']==site_id:row.update(patch)
class ConfigOper:
    def __init__(self,values):self.values=copy.deepcopy(values)
    def get(self,key):return self.values.get(key)
    def set(self,key,value):self.values[key]=copy.deepcopy(value)

def fixture(config,installed=all_plugins):
    instance=MergeSiteSwitch()
    instance._MergeSiteSwitch__config=copy.deepcopy(config)
    instance._MergeSiteSwitch__site_oper=SiteOper([dict(id='s1',name='系统一',is_active=True),dict(id='s2',name='系统二',is_active=False)])
    instance._MergeSiteSwitch__system_config_oper=ConfigOper(dict(installed=installed,search=['s1'],rss=['s1']))
    configs={'MergeSiteSwitch':copy.deepcopy(config),'CustomSites':dict(enabled=True,sites=[dict(id='c1',name='自定义一')]),'AutoSignIn':dict(enabled=True,sign_sites=['s1','c1'],login_sites=['c1']),'SiteStatistic':dict(enabled=True,statistic_sites=['c1']),'IYUUAutoSeed':dict(enabled=True,sites=['c1']),'BrushFlow':dict(brushsites=['s1']),'CrossSeed':dict(sites=['c1'])}
    instance.get_config=lambda plugin_id=None:copy.deepcopy(configs.get(plugin_id or 'MergeSiteSwitch',{}))
    def update_config(config,plugin_id=None):configs[plugin_id or 'MergeSiteSwitch']=copy.deepcopy(config)
    instance.update_config=update_config
    return instance,configs

reloads=[]
base_fixture=fixture

def numeric_fixture(config, installed):
 obj,configs=base_fixture(config,installed)
 obj._MergeSiteSwitch__site_oper=SiteOper([dict(id=7,name='数字七',is_active=True),dict(id=19,name='数字十九',is_active=False)])
 obj._MergeSiteSwitch__system_config_oper=ConfigOper(dict(installed=installed,search=[7],rss=[7]))
 configs['CustomSites']=dict(enabled=True,sites=[dict(id='7',name='自定义七'),dict(id='c1',name='自定义一')])
 configs['AutoSignIn']=dict(enabled=True,sign_sites=[7,'7','all'],login_sites=['c1'])
 configs['SiteStatistic']=dict(enabled=True,statistic_sites=['7'])
 configs['IYUUAutoSeed']=dict(enabled=True,sites=['c1'])
 configs['BrushFlow']=dict(brushsites=[7])
 configs['CrossSeed']=dict(sites=['7'])
 return obj,configs

cases=[]
def run(config, installed=all_plugins, events=[]):
 global reloads
 reloads=[]
 obj,configs=numeric_fixture(config,installed)
 obj._MergeSiteSwitch__set_config(copy.deepcopy(config))
 normalized=copy.deepcopy(configs['MergeSiteSwitch'])
 state=obj.get_state()
 for site_id,status in events:
  if obj._MergeSiteSwitch__check_any_follow_enable_sites():
   obj._MergeSiteSwitch__update_site_ids_for_site_event(site_id,status)
 cases.append(dict(config=config,installed=installed,events=[dict(site_id=i,enabled=s) for i,s in events],state=state,normalized=normalized,system=obj._MergeSiteSwitch__system_config_oper.values,plugins=configs,enabled=[r['id'] for r in obj._MergeSiteSwitch__site_oper.rows if r['is_active']],reloads=copy.deepcopy(reloads)))

run({})
for enable in [[],[7],[19],[7,19],[7,7,19],[9007199254740993],[9223372036854775807]]:
 for installed in [all_plugins,[],['CrossSeed'],['BrushFlowLanduo']]:
  run(dict(enable_sites=enable, search_follow_enable_sites=True, rss_follow_enable_sites=True),installed,[(7,True),(19,False),(7,False),(7,False),(19,True)])
for prefix in ['search','rss','signin','login','statistic','iyuu_seed','brush_flow','cross_seed']:
 for installed in [all_plugins,[],['CrossSeed'],['BrushFlowLanduo']]:
  config=dict(enable_sites=[19],signin_sites=[7,'7','all','missing'],login_sites=['c1','7'],statistic_sites=['7'],iyuu_seed_sites=['c1'],cross_seed_sites=['7'])
  config[prefix+'_follow_enable_sites']=True
  run(config,installed,[(7,True),(19,False),(7,False)])
flags={p+'_follow_enable_sites':True for p in ['search','rss','signin','login','statistic','iyuu_seed','brush_flow','cross_seed']}
run(dict(flags,enable_sites=[19,19],signin_sites=['7','7'],login_sites=['7']),all_plugins,[(19,False),(19,False),(7,True),(7,True)])
run(dict(cross_seed_follow_enable_sites=True,enable_sites=[7],cross_seed_sites=['7']),all_plugins,[(7,False)])
run(dict(search_follow_enable_sites=True,enable_sites=[7],cross_seed_follow_enable_sites=True,cross_seed_sites=['7',7,7]),all_plugins,[(7,False)])
obj,configs=numeric_fixture({},all_plugins)
form,model=obj.get_form()
result=dict(source_sha256=hashlib.sha256(source.encode()).hexdigest(),cases=cases,form=form,model=model)
out=Path(__file__).parent.parent/'app/testdata/plugins/merge-sites-typed-original-oracle.json'
out.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(cases=len(cases),source_sha256=result['source_sha256'],output_sha256=hashlib.sha256(out.read_bytes()).hexdigest())))
