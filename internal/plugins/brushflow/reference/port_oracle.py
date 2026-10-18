"""Execute fixed original AST offline; no MoviePilot import, no network or user config."""
import ast,base64,calendar,copy,hashlib,json,os,pathlib,random,re,threading,time as real_time,types,uuid as real_uuid
from collections import Counter
from contextlib import contextmanager
from datetime import datetime as RealDateTime,timedelta,timezone
from typing import Any,Dict,Iterator,List,Optional,Set,Tuple,Union
from urllib.parse import parse_qs,parse_qsl,unquote,urlencode,urljoin,urlparse,urlunparse
from zoneinfo import ZoneInfo
os.environ['TZ']='Asia/Shanghai';real_time.tzset()
root=pathlib.Path(__file__).resolve().parent
source=root/'__init__.py'
NOW=RealDateTime(2026,10,18,12,0,0,tzinfo=ZoneInfo('Asia/Shanghai'))
class FixedDateTime(RealDateTime):
 @classmethod
 def now(cls,tz=None):
  t=NOW.astimezone(tz) if tz is not None else NOW.replace(tzinfo=None)
  return cls.fromisoformat(t.isoformat())
class FakeEventManager:
 def register(self,*a,**kw):return lambda x:x
class Empty:pass
ns=dict(globals());ns.update({'_PluginBase':Empty,'datetime':FixedDateTime,'time':types.SimpleNamespace(time=lambda:NOW.timestamp(),sleep=lambda _:None,strftime=real_time.strftime,localtime=real_time.localtime),'uuid':types.SimpleNamespace(uuid4=lambda:types.SimpleNamespace(hex='owned0000000000000000000000000000')),'eventmanager':FakeEventManager(),'EventType':types.SimpleNamespace(PluginReload='reload'),'settings':types.SimpleNamespace(TZ='Asia/Shanghai'),'logger':types.SimpleNamespace(info=lambda *_:None,error=lambda *_:None,warning=lambda *_:None),'schemas':types.SimpleNamespace(Response=lambda **kw:kw),'Query':lambda default,**kw:default})
tree=ast.parse(source.read_text());tree.body.insert(0,ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0));tree.body=[n for n in tree.body if not isinstance(n,(ast.Import,ast.ImportFrom)) or isinstance(n,ast.ImportFrom)and n.module=='__future__']
exec(compile(ast.fix_missing_locations(tree),str(source),'exec'),ns)
Task=ns['BrushTaskConfig'];Core=ns['BrushFlowLanduo']
ns['DownloaderHelper']=lambda:types.SimpleNamespace(is_downloader=lambda kind,service:kind==service.kind)
class Harness(Core):
 @property
 def service_info(self):return self.serviceOverride
p=Harness();p._task_context=threading.local();p._task_context.task_id='task';p._task_configs={'task':Task({'id':'task'})}
def norm(v):
 if isinstance(v,RealDateTime):return v.isoformat()
 if isinstance(v,timedelta):return v.total_seconds()
 if isinstance(v,(tuple,list)):return [norm(x) for x in v]
 if isinstance(v,dict):return {str(k):norm(x) for k,x in v.items()}
 if isinstance(v,Task):return norm(v.to_dict())
 return v
cases=[]
def case(method,args,fn):
 try:out={'value':norm(fn())}
 except Exception as e:out={'error_class':type(e).__name__}
 cases.append({'method':method,'args':args,'expected':out})
for raw in [{'id':'task'},{'id':'task','name':' A ','brush_interval':0,'check_interval':-2,'enabled':'false','size':' '},{'id':'task','site_id':'2','disksize':'0','site_ratio_target':'12.5','timezone_offset':'-8'},{'id':'task','freeleech':'','del_no_free':True},{'id':'task','freeleech':'2xfree','del_no_free':True,'auto_archive_days':'bad'},{'id':'task','downloader':' qb ','include':' a ','maxdlcount':True}]:case('task_config',raw,lambda raw=raw:Task(raw).to_dict())
for value in [None,'','暂无','2026-09-20','2026/9/3 8:30','2026.9.3 08:30:59','2026-09-20T12:00:00Z','2026-09-20 12:00:00 +0800','免费截止 2026-09-20 12:00:00','2026-02-30','2026-09-20 25:30','２０２６-０９-２０ １２:００:００','2026-09-20 12:00+14:59','2026-09-20 12:00+15:00','2026-09-20 12:00 +12:60']:
 case('deadline',{'value':value},lambda value=value:p._parse_promotion_deadline(value))
for value in [None,'--','2天3小时5分','1 day, 2:03:04','02:59','24:01:59','1:60:00','5hours30mins','０２小时３０分','1天 2:03','3','5hr','2日','45分钟']:
 case('duration',{'value':value},lambda value=value:p._promotion_duration_seconds(value))
for value in ['2026-09-20T12:00:00Z','2026-09-20','剩余 2小时30分','暂无']:
 for offset in [0,-8,8]:case('expiry_at',{'value':value,'offset':offset},lambda value=value,offset=offset:p._promotion_expiry_at(value,offset))
for r in [{'progress':1,'downloaded':0,'size':100},{'progress':.99,'downloaded':1000,'size':100},{'size':100,'downloaded':100},{'size':0,'downloaded':0},{'progress':None,'total_size':100,'downloaded':'100'},{'progress':'bad','size':100,'downloaded':100},{'downloadvolumefactor':0},{'downloadvolumefactor':'0'},{'downloadvolumefactor':'bad','volume_factor':'免费'},{'volume_factor':'4X免费'},{'volume_factor':'FREE'}]:
 case('complete',r,lambda r=r:p._torrent_complete(r));case('added_free',r,lambda r=r:p._record_added_as_free(r))
base={'site_name':'S','page_url':'/details.php?id=1','title':'Movie 2026','size':1073741824,'description':'电影','downloadvolumefactor':0,'uploadvolumefactor':2,'hit_and_run':False,'seeders':3,'pubdate':'2026-10-18 11:30:00'}
configs=[{}, {'freeleech':'2xfree'},{'include':'movie'},{'exclude':'MOVIE'},{'include':r'(?<=Movie )\d+'},{'size':'2'},{'size':'1-2'},{'seeder':'2'},{'seeder':'2-4'},{'pubtime':'10'},{'pubtime':'20-40'},{'hr':'yes'},{'freeleech':''}]
for cfg in configs:
 for change in [{},{'downloadvolumefactor':1,'uploadvolumefactor':1,'hit_and_run':True}]:
  raw={'id':'task',**cfg};p._task_configs={'task':Task(raw)};torrent=types.SimpleNamespace(**{**base,**change})
  case('candidate',{'config':raw,'torrent':{**base,**change},'records':{}},lambda torrent=torrent:p._BrushFlowLanduo__evaluate_conditions_for_brush(torrent,{}))
for old in [{'site_name':'S','page_url':'/details.php?id=1','title':'other'},{'site_name':'S','page_url':'/details.php?id=2','title':'Movie 2026'},{'site_name':'X','title':'Movie 2026','size':100,'downloaded':0,'progress':0}]:
 p._task_configs={'task':Task({'id':'task'})};torrent=types.SimpleNamespace(**base)
 case('candidate',{'config':{'id':'task'},'torrent':base,'records':{'old':old}},lambda old=old:p._BrushFlowLanduo__evaluate_conditions_for_brush(torrent,{'old':old}))
for cfg in [{'seed_time':1},{'hr_seed_time':2,'seed_ratio':2},{'seed_ratio':2},{'seed_size':1},{'download_time':1},{'seed_avgspeed':10},{'seed_inactivetime':1},{}]:
 for hr in [False,True]:
  raw={'id':'task',**cfg};p._task_configs={'task':Task(raw)};info={'progress':.5,'seeding_time':3600,'ratio':2,'uploaded':1073741824,'downloaded':0,'dltime':7200,'avg_upspeed':100,'iatime':60}
  case('delete_condition',{'config':raw,'info':info,'record':{'hit_and_run':hr}},lambda info=info,hr=hr:p._BrushFlowLanduo__evaluate_conditions_for_delete(info,{'hit_and_run':hr}))
for kind,raw in [('qbittorrent',{'hash':'a','name':'A','added_on':NOW.timestamp()-7200,'completion_on':NOW.timestamp()-3600,'last_activity':NOW.timestamp()-60,'total_size':1000,'progress':1,'downloaded':0,'uploaded':999,'ratio':2,'tags':'a, b','tracker':'https://pt.example/announce'}),('transmission',{'hashString':'b','name':'B','total_size':1000,'progress':100,'ratio':2,'done_date':NOW-timedelta(hours=1),'added_date':NOW-timedelta(hours=2),'activity_date':NOW-timedelta(minutes=1),'labels':['a','b'],'tracker_list':['https://tr.example/announce']})]:
 p.serviceOverride=types.SimpleNamespace(kind=kind);torrent=raw if kind=='qbittorrent' else types.SimpleNamespace(**raw)
 args={'kind':kind,'raw':{k:(v.timestamp() if isinstance(v,RealDateTime)else v)for k,v in raw.items()}}
 case('torrent_info',args,lambda torrent=torrent:p._BrushFlowLanduo__get_torrent_info(torrent))
# Ordered global candidates cover source pre-delete, normal/proxy conditional,
# fallback, duplicate physical torrent and exact high/low threshold boundaries.
for total,min_size,max_size in [(90,50,100),(100,50,100),(150,50,100),(50,50,50)]:
 rows=[]
 for i,(proxy,completed,hr,pre,condition,seed)in enumerate([(False,True,False,'','normal',1),(True,True,False,'','proxy',2),(True,True,False,'','',30),(True,False,True,'expired','',3)]):
  task=Task({'id':f't{i}','name':f'T{i}'})
  rows.append({'task':task,'torrent_hash':str(i),'downloader_name':'qb','size':30,'pre_delete_reason':pre,'conditional_reason':condition,'proxy_delete':proxy,'completed':completed,'hit_and_run':hr,'seeding_time':seed})
 def select(rows=rows,total=total,min_size=min_size,max_size=max_size):
  chosen,remaining,triggered=p._select_global_dynamic_deletions(rows,total,min_size,max_size)
  return {'selected':[{'hash':r['torrent_hash'],'reason':r['delete_reason'],'task_reasons':r['task_delete_reasons']}for r in chosen],'remaining':remaining,'threshold':triggered}
 case('global_select',{'candidates':norm(rows),'total':total,'min':min_size,'max':max_size},select)
# Actual fixed models.py validators, using installed offline Pydantic only.
model_ns={}
exec(compile((root/'models.py').read_text(),str(root/'models.py'),'exec'),model_ns)
for patch in [{},{'name':'  '},{'name':'x'*81},{'site_id':0},{'site_id':'1','maxdlcount':'2'},{'maxdlcount':'1.5'},{'disksize':0},{'disksize':''},{'disksize':'-1'},{'site_ratio_control':True},{'site_ratio_control':True,'site_ratio_target':2},{'active_time_range':'23:00-06:00'},{'active_time_range':'25:00-06:00'},{'size':'10-80'},{'size':'80-10'},{'size':'0'},{'freeleech':'bad'},{'include':'(?<=a)b'},{'include':'('},{'notify':'false'},{'timezone_offset':-8},{'save_path':' /downloads '}]:
 raw={'name':'Task','site_id':1,'downloader':'qb',**patch}
 case('model_task',raw,lambda raw=raw:model_ns['BrushTaskPayload'].model_validate(raw).model_dump())
for raw in [{},{'enabled':'false'},{'global_disksize':0},{'global_maxdlcount':'2'},{'global_proxy_delete':True},{'global_proxy_delete':True,'global_delete_size_range':'50-100'},{'global_proxy_delete':True,'global_delete_size_range':'100-50'},{'global_proxy_delete':True,'global_delete_size_range':'0'},{'global_proxy_delete':False,'global_delete_size_range':'bad'},{'global_proxy_delete':'false','global_delete_size_range':'bad'},{'global_proxy_delete':'FALSE','global_delete_size_range':'bad'},{'global_proxy_delete':True,'global_delete_size_range':' 100 '}]:
 case('model_settings',raw,lambda raw=raw:model_ns['BrushFlowSettingsPayload'].model_validate(raw).model_dump())
# Consumed text ranges use Python Unicode decimal and _strptime semantics.
for patch in [{'size':'１.５-２.５'}, {'size':'١.٥-٢.٥'}, {'active_time_range':'0１:0２-12:3０'}, {'active_time_range':'０1:02-12:30'}, {'active_time_range':'21:30-2３:00'}, {'active_time_range':'23:59-00:01'}]:
 raw={'name':'Task','site_id':1,'downloader':'qb',**patch}
 case('model_task',raw,lambda raw=raw:model_ns['BrushTaskPayload'].model_validate(raw).model_dump())
for raw in [{'global_proxy_delete':True,'global_delete_size_range':'５０-１００'}, {'global_proxy_delete':True,'global_delete_size_range':'١٠٠-٥٠'}]:
 case('model_settings',raw,lambda raw=raw:model_ns['BrushFlowSettingsPayload'].model_validate(raw).model_dump())
result={'source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'runtime':__import__('platform').python_version(),'runtime_bound':'local offline CPython source AST, no original3.12 claim','now':NOW.isoformat(),'network':False,'pydantic':__import__('pydantic').__version__,'cases':cases}
(root/'port-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'source_sha256':result['source_sha256'],'runtime':result['runtime']}))
