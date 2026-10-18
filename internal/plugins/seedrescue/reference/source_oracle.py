"""Offline only: execute captured/public source, no user settings or network.
Python and lxml are verification tools, never product dependencies.
"""
import ast, importlib.util, json, sys, hashlib, threading, types
from pathlib import Path
from datetime import datetime, timedelta
from typing import Any, Dict, List, Optional, Tuple
from collections import Counter
from concurrent.futures import ThreadPoolExecutor, as_completed
from types import SimpleNamespace
ROOT=Path(__file__).resolve().parent

def load_module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);module=importlib.util.module_from_spec(spec);sys.modules[name]=module;spec.loader.exec_module(module);return module
core=load_module('sr_core_source',ROOT/'public-core.py')
namespace=dict(vars(core))
detail_ast=ast.parse((ROOT/'source/detail.py').read_text());detail_ast.body=[n for n in detail_ast.body if not isinstance(n,ast.ImportFrom) or n.level==0]
namespace['__name__']='sr_detail_source';sys.modules['sr_detail_source']=types.ModuleType('sr_detail_source');exec(compile(detail_ast,'actual_detail.py','exec'),namespace)
class Base:
 def __init__(self):self.data={};self.updates=[]
 def get_data(self,k):return self.data.get(k)
 def save_data(self,k,v):self.data[k]=v
 def update_config(self,v):self.updates.append(v)
class Logger:
 def __getattr__(self,k):return lambda *a,**kw:None
class Response:
 def __init__(self,success=False,message=None,data=None,**kw):self.success=success;self.message=message;self.data=data
namespace.update(Any=Any,Dict=Dict,List=List,Optional=Optional,Tuple=Tuple,Counter=Counter,ThreadPoolExecutor=ThreadPoolExecutor,as_completed=as_completed,threading=threading,datetime=datetime,timedelta=timedelta,SimpleNamespace=SimpleNamespace,_PluginBase=Base,logger=Logger(),CandidateSelectionPayload=Any,schemas=SimpleNamespace(Response=Response),NotificationType=SimpleNamespace(SiteMessage='SiteMessage'),IyuuHelper=None)
plugin_ast=ast.parse((ROOT/'source/__init__.py').read_text());plugin_ast.body=[n for n in plugin_ast.body if isinstance(n,ast.ClassDef)]
exec(compile(plugin_ast,'actual_plugin.py','exec'),namespace)
Plugin=namespace['SeedRescue']
cases=[]
def emit(op,arg,fn):
 try:value=fn();record={'op':op,'arg':arg,'value':value}
 except Exception as e:record={'op':op,'arg':arg,'error':type(e).__name__,'message':str(e)}
 cases.append(record)

def clean_ast(path):
 a=ast.parse(path.read_text())
 for n in ast.walk(a):
  if isinstance(n,(ast.Module,ast.ClassDef,ast.FunctionDef)) and n.body and isinstance(n.body[0],ast.Expr) and isinstance(n.body[0].value,ast.Constant) and isinstance(n.body[0].value.value,str):n.body=n.body[1:]
  if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='NON_FREE' for t in n.targets):n.value=ast.Constant(value='non_free')
 return ast.dump(a,include_attributes=False)
assert clean_ast(ROOT/'public-core.py')==clean_ast(ROOT/'source/core.py'), 'unexpected public/source divergence'
for v in [None,'',0,1,False,True,-1,.5,'0','0.0',' 1 ','bad','NaN','inf',[],{}]:emit('promotion',v,lambda v=v:core.promotion_kind(v))
for v in [None,'',0,1,-1,True,False,1.2,2.0,'1.0','1e2','1_0',' 03 ','+4','no',[],{},'0','-0']:
 emit('optional',v,lambda v=v:Plugin._optional_limit(v,7));emit('positive',v,lambda v=v:Plugin._positive_int(v,7))
for v in [None,{}, {'enabled':1,'onlyonce':True,'notify':False,'target_site_id':'1','source_site_ids':[0,1,2,'2',3],'rss_urls':'one\n two\none','selection_mode':' HYBRID ','confirm_download_risk':None}, {'minimum_seeders':'1.0','max_download_count':-1,'initial_limit':True,'allowed_promotions':['unknown','free','non_free','free']}, {'cron':'  ','save_path':' /owned ','token':' fixture-token ','allowed_promotions':[]}]:
 def config(v=v):
  p=Plugin();p.init_plugin(v);return p._current_config()
 emit('config',v,config)
for title in ['', 'Film.2024.1080p.WEB-DL','Show.S02E03.2160p','Movie Title','a.2024.x','电影名.2026.国语',' Title_2025 ','The.Show-S01E001-GROUP','S01E02']:
 emit('keywords',title,lambda title=title:core.search_keywords(title))
for url in ['https://site.test/details.php?id=42&passkey=fixture#x','https://u:p@site.test/a?id=2','ftp://site.test/123','https://site.test/detail/53','https%3A%2F%2Fsite.test%2Fdetails.php%3Fid%3D2','https://site.test/details.php?torrentid=91&token=x','https://site.test/a?k=%ZZ&x=&PASSKEY=xx','HTTPS://SITE.test/details.php?id=4']:
 emit('extract',url,lambda url=url:core.extract_torrent_id(url));emit('safe',url,lambda url=url:core.safe_page_url(url))
for raw in [b'd4:infod4:name1:xee',b'd4:infoi1_0ee',b'd4:infoi+01ee',b'd4:infoli1ei2eee',b'd4:info1:xeTRAIL',b'l',b'de',b'd4:infoiwee',b'd4:info999999999999999999999999:x',b'd4:infole',b'd4:infoi1',b'd4:infoi  2\t ee']:
 emit('hash',raw.hex(),lambda raw=raw:core.info_hash_from_torrent(raw))
for state in ['uploading','stalledUP','pausedUP','stoppedDL','checkingUP','error','missingFiles','unknown','queuedUP','downloading','']:
 for progress in [None,0,1,100,.5]:
  t={'state':state,'progress':progress,'amount_left':0};emit('torrent',t,lambda t=t:{'complete':core.torrent_is_complete(t),'status':core.target_torrent_status(t)})
for site in [{'url':'https://site.test/','schema':'NexusPhp'},{'url':'https://kp.m-team.cc'},{'url':'https://api2.m-team.io'},{'url':'https://site.test','parser':'custom','schema':'nexus'},{'url':'https://site.test'}]:
 emit('detail_url',site,lambda site=site:namespace['detail_url'](site,'42'))
for discount in ['NORMAL','FREE','PERCENT_50','PERCENT_70','_2X_FREE','UNKNOWN',None]:
 p={'code':'0','data':{'id':'42','name':'Fixture','size':'1,024','status':{'discount':discount,'seeders':'2','leechers':0,'timesCompleted':'13'}}};emit('mteam',p,lambda p=p:namespace['parse_mteam'](p,'42'))
for label in ['','[FREE]','【免费】','[2xFREE]','[50%]','[30%]','free period','[70%]','审核通过']:
 text=f'<html><h1 id="top">Fixture <span class="twoupfree">{label}</span></h1><a href="download.php?id=42&amp;passkey=fixture">D</a><div id="peercount">1,024 seeders 7 leechers</div><a href="viewsnatches.php?id=42">2,301 times</a><table><tr><td><b>大小：</b> 1.5 GiB</td></tr></table></html>'
 emit('nexus',text,lambda text=text:namespace['parse_nexus'](text,'https://site.test/details.php?id=42','42'))
for text in ['<h1>Title</h1><a href="https://evil.test/download.php?id=42">D</a>','<h1>Title</h1><input type="password"><a href="download.php?id=42">D</a>','<h1><span>[FREE]</span> Title</h1><a href="download.php?id=42">D</a>','<h1>Film FREE</h1><a href="download.php?id=42">D</a>']:
 emit('nexus',text,lambda text=text:namespace['parse_nexus'](text,'https://site.test/details.php?id=42','42'))
# Capture methods whose persistent outputs are independent of host UI internals.
for state in ['MATCHING','AWAITING_SELECTION','SOURCE_SUBMITTING','TARGET_SEEDING']:
 job={'target_hash':'a'*40,'target_site_id':1,'target_site_name':'Target','target_torrent_id':'42','title':'Fixture','state':state,'enclosure':'fixture-must-not-survive','target_page_url':'https://site.test/details.php?id=42&passkey=fixture','probes':[],'candidates':[]}
 emit('job_view',job,lambda job=job:Plugin._job_view(job))
exec(compile((ROOT/'flow_oracle_fragment.py').read_text(),'flow_oracle_fragment.py','exec'))
exec(compile((ROOT/'scan_oracle_fragment.py').read_text(),'scan_oracle_fragment.py','exec'))
result={'environment':{'python':sys.version,'lxml':__import__('lxml').__version__},'public_core_sha256':hashlib.sha256((ROOT/'public-core.py').read_bytes()).hexdigest(),'public_vs_capture':'identical AST after docstring removal and verified NON_FREE restoration','cases':cases}
(ROOT/'source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2,allow_nan=False)+'\n')
print(json.dumps({'cases':len(cases),'environment':result['environment']}))
