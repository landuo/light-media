"""Source-only real installed AST/Pydantic oracle; no app imports or senders."""
import ast, copy, hashlib, json, pathlib, queue, sys, types
from datetime import datetime
from enum import Enum
from typing import Optional, Union, List, Dict, Any, Callable
import pydantic, pydantic_core
from pydantic import BaseModel

ROOT = pathlib.Path('/app/app')
paths = ['helper/message.py', 'chain/__init__.py', 'schemas/message.py', 'schemas/types.py', 'utils/identity.py']
sources = {p: (ROOT / p).read_text() for p in paths}
sha = {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest() for p in paths}

def norm(value):
    if isinstance(value, BaseModel): return norm(value.model_dump())
    if isinstance(value, Enum): return value.value
    if isinstance(value, dict): return {str(k):norm(v) for k,v in value.items()}
    if isinstance(value, (list,tuple)): return [norm(v) for v in value]
    return value

class Log:
    def __getattr__(self,name): return lambda *a,**kw: None

scope = dict(Enum=Enum,Optional=Optional,Union=Union,List=List,Dict=Dict,Any=Any,Callable=Callable,BaseModel=BaseModel,__name__='notice_oracle',logger=Log(),copy=copy,queue=queue)
types_tree = ast.parse(sources['schemas/types.py'])
for name in ['NotificationType','ContentType','MessageChannel']:
    node = next(n for n in types_tree.body if isinstance(n,ast.ClassDef) and n.name==name)
    exec(compile(ast.Module(body=[node],type_ignores=[]),'installed:'+name,'exec'),scope)
node = next(n for n in ast.parse(sources['schemas/message.py']).body if isinstance(n,ast.ClassDef) and n.name=='Notification')
exec(compile(ast.Module(body=[node],type_ignores=[]),'installed:Notification','exec'),scope)
Notification = scope['Notification']
exec(compile(ast.parse(sources['utils/identity.py']),'installed:identity','exec'),scope)

def cls(path,name,newname,methods):
    original = next(n for n in ast.parse(sources[path]).body if isinstance(n,ast.ClassDef) and n.name==name)
    nodes = [copy.deepcopy(n) for n in original.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in methods]
    result = ast.ClassDef(name=newname,bases=[],keywords=[],body=nodes,decorator_list=[])
    module = ast.fix_missing_locations(ast.Module(body=[result],type_ignores=[]))
    # Function annotations outside the actual Notification class are irrelevant
    # to their original executable statements; postponed annotation avoids app types.
    import __future__
    exec(compile(module,'installed:'+name,'exec',flags=__future__.annotations.compiler_flag),scope)
    return scope[newname]

Q = cls('helper/message.py','MessageQueueManager','OriginalQueue',{'_parse_schedule','_time_to_minutes','_is_in_scheduled_time','send_message','async_send_message','_send','_monitor_loop'})
Template = cls('helper/message.py','MessageTemplateHelper','MessageTemplateHelper',{'render','is_instance_valid','meets_update_conditions','_apply_template_data','_get_template'})
Chain = cls('chain/__init__.py','ChainBase','OriginalChain',{'post_message','_normalize_notification_for_dispatch','_build_notice_message_data'})
scope['MessageTemplateHelper'] = Template

envelope_inputs = [('defaults',{}),('extras-ignored',{'unrecognized':'discarded'}),('all-explicit-null',{k:None for k in Notification.model_fields})]
for field in ['title','source','text','image','voice_path','file_path','file_name','voice_caption','link','date','original_chat_id','parse_mode']:
    for value in [None,'','中文',1,True,[],{}]: envelope_inputs.append((field+'-'+repr(value),{field:value}))
for field in ['userid','username','original_message_id']:
    for value in [None,'','123',' 001 ',123,True,False,1.0,1.5,-1,2**100,1e30,[],{}]: envelope_inputs.append((field+'-'+repr(value),{field:value}))
for value in [None,0,1,-2,True,False,'1','01','1.0','1_0',' 2 ','NaN',1.0,1.5,1e30,-0.0,[],{}]: envelope_inputs.append(('action-'+repr(value),{'action':value}))
for value in ['+1','-01','1.00','1.0001','1e2','1E0','1.0e0','1.','01.00',' 1.0 ','1_0.0','1.0_0','1.00_0','1.0_','1._0','1.000_000','+0_1.00','١٢','１２','1\x1c','1\u00a0','--1','_1','1_','1__0','0x10','-0.000','9'*4301]: envelope_inputs.append(('action-extra-'+repr(value),{'action':value}))
for field in ['force_reply','save_history','disable_web_page_preview']:
    for value in [None,True,False,0,1,2,0.0,1.0,2.0,1.5,1e30,-0.0,0.00000000000000001,2**63-1,2**63,-(2**63),-(2**63)-1,2**100,'true','false','on','off','yes','no','y','n','t','f','0','1','False ',' true','TRUE',[],{}]: envelope_inputs.append((field+'-'+repr(value),{field:value}))
for field,enumname in [('channel','MessageChannel'),('mtype','NotificationType'),('ctype','ContentType')]:
    for value in [None,'',1,True,{},[]]+[e.value for e in scope[enumname]]: envelope_inputs.append((field+'-'+repr(value),{field:value}))
for field in ['targets','buttons']:
    for value in [None,{},[],[[]],[[{}]],{'one':'two'},[{}],[[{'text':'one','nested':[1,None]}]],'bad',1]: envelope_inputs.append((field+'-'+repr(value),{field:value}))
for value in [float(2**63-1024),float(2**63),float(-(2**63)),float(-(2**63)-2048),float(2**64-2048),float(2**64),1.0000000000000002]: envelope_inputs.append(('action-float-boundary-'+repr(value),{'action':value}))
envelope_inputs += [('multiple-errors',{'channel':'bad','title':1,'userid':[],'buttons':[{},[1,None]]}),('buttons-empty',{'buttons':[]}),('buttons-dict-keys',{'buttons':[[{'1':1}]]})]
envelopes = []
for name,data in envelope_inputs:
    try:
        out = Notification(**data)
        result = dict(value=norm(out),errors=None)
    except pydantic.ValidationError as e:
        result = dict(value=None,errors=[dict(type=r['type'],loc=list(r['loc'])) for r in e.errors(include_url=False)])
    envelopes.append(dict(name=name,input=data,**result))

period_inputs = [None,[],{},False,0,'',{'start':'09:30','end':'18:00'},[{'start':'22:30:99','end':'02:15:88'}],[{'start':'09:00:00:extra','end':'09:00'}],[{'start':'-1:90','end':'25:70'}],[{'start':'９:٣٠','end':'1_8:00'}],[{'start':'one','end':'18:00'}],[None,{},False,{'start':'10:00','end':'11:00'}],[{'start':True,'end':'11:00'}],['bad'],['',{'start':'1:00','end':'2:00'}],[{'start':'1:00','end':'2:00'},'bad'],1,True]
periods = []
for value in period_inputs:
    try: out,error = norm(Q._parse_schedule(value)),None
    except Exception as e: out,error = None,type(e).__name__
    periods.append(dict(input=value,value=out,error=error))
windows = []
for spec in [[],[[9,30,18,0]],[[22,30,2,15]],[[9,0,9,0]],[[-1,90,25,70]],[[25,0,26,0]],[[9,0,10,0],[15,0,16,0]]]:
    obj=Q.__new__(Q);obj.schedule_periods=[tuple(p) for p in spec]
    for hour,minute in [(0,0),(2,15),(2,16),(9,0),(9,30),(10,0),(10,1),(15,0),(18,0),(18,1),(22,30),(23,59)]:
        windows.append(dict(periods=spec,hour=hour,minute=minute,allowed=obj._is_in_scheduled_time(datetime(2026,1,2,hour,minute))))

class Clock:
    at=datetime(2026,1,2,12,34,56)
    @classmethod
    def now(cls): return cls.at
scope['datetime']=Clock

def queue_case(name,spec,now,immediately=False,fail=False):
    calls=[]
    obj=Q.__new__(Q);obj.schedule_periods=[tuple(x) for x in spec];obj.queue=queue.Queue()
    def sender(*a,**kw):
        calls.append(dict(args=norm(a),kwargs=norm(kw)))
        if fail: raise RuntimeError('synthetic sender failure')
    obj.send_callback=sender;Clock.at=datetime(2026,1,2,*now)
    kwargs=dict(message='owned-message',immediately=immediately,extra='retained')
    obj.send_message('post_message',**kwargs)
    pending=[]
    while not obj.queue.empty(): pending.append(norm(obj.queue.get_nowait()))
    return dict(name=name,periods=spec,now=now,immediately=immediately,callback_error=fail,calls=calls,pending=pending)
queue_cases=[queue_case('unrestricted',[],[12,0]),queue_case('inside',[[9,0,18,0]],[12,0]),queue_case('outside',[[9,0,18,0]],[23,0]),queue_case('force',[[9,0,18,0]],[23,0],True),queue_case('truthy-string-force',[[9,0,18,0]],[23,0],'false'),queue_case('caught-callback-error',[],[12,0],False,True)]

monitor_cases=[]
for name,stopped,clock_rows,fail,no_callback in [
    ('drain-fifo',False,[(12,0)],False,False),
    ('outside-window',False,[(23,0)],False,False),
    ('stopped-before-first',True,[(12,0)],False,False),
    ('window-closes-between-items',False,[(12,0),(12,0),(23,0)],False,False),
    ('caught-error-continues',False,[(12,0)],True,False),
    ('absent-callback-still-consumes',False,[(12,0)],False,True),
]:
    obj=Q.__new__(Q);obj.schedule_periods=[(9,0,18,0)];obj.queue=queue.Queue();obj._running=True
    for i in range(3):obj.queue.put(dict(args=['post_message'],kwargs=dict(message='owned-'+str(i))))
    calls=[]
    def sender(*a,**kw):
        calls.append(dict(args=norm(a),kwargs=norm(kw)))
        if fail:raise RuntimeError('synthetic sender failure')
    obj.send_callback=None if no_callback else sender
    class OnceWait:
        def wait(self,_):return True
    obj._stop_event=OnceWait();obj.check_interval=10
    class Sequence:
        index=0
        @classmethod
        def now(cls):
            h,m=clock_rows[min(cls.index,len(clock_rows)-1)];cls.index+=1
            return datetime(2026,1,2,h,m)
    scope['datetime']=Sequence;scope['global_vars']=types.SimpleNamespace(is_system_stopped=stopped)
    obj._monitor_loop()
    pending=[]
    while not obj.queue.empty():pending.append(norm(obj.queue.get_nowait()))
    monitor_cases.append(dict(name=name,stopped=stopped,clock=clock_rows,callback_error=fail,no_callback=no_callback,calls=calls,pending=pending))
scope['datetime']=Clock

routes=[]
route_inputs=[
 ('default',{},None,{}),('empty',{'title':None,'text':None},None,{}),
 ('history-off',{'save_history':False},None,{}),('explicit-user',{'userid':'42','mtype':'站点'},'admin',{}),
 ('internal-user',{'userid':' SYSTEM ','mtype':'站点'},'admin',{'root':{'admin':['one']}}),
 ('admin',{'mtype':'站点'},'admin',{'root':{'admin':['one']}}),
 ('user',{'mtype':'站点','username':'alice'},'user',{'alice':{'user':['one']}}),
 ('missing-user-fallback',{'mtype':'站点','username':'absent'},'user',{'root':{'admin':['one']}}),
 ('admin-user',{'mtype':'站点','username':'alice'},'admin,user',{'root':{},'alice':{}}),
 ('admin-missing-user',{'mtype':'站点','username':'absent'},'admin,user',{'root':{}}),
 ('admin-admin-user',{'mtype':'站点','username':'alice'},'admin,admin,user',{'root':{},'alice':{}}),
 ('user-is-admin',{'mtype':'站点','username':'root'},'user,admin',{'root':{}}),
 ('all',{'mtype':'站点'},'all',{}),('admin-all',{'mtype':'站点'},'admin,all',{'root':{}}),
 ('unknown-first',{'mtype':'站点'},'unknown,admin',{}),('user-without-name',{'mtype':'站点'},'user,admin',{}),
    ('channel-still-routes',{'channel':'Telegram','mtype':'站点'},'admin',{'root':{}}),
 ('empty-target-is-present',{'mtype':'站点','username':'alice'},'user',{'alice':{}}),
]
# Retain only channel values that actually exist in the installed enum.
valid_channels={e.value for e in scope['MessageChannel']}
route_inputs=[r for r in route_inputs if r[1].get('channel') is None or r[1]['channel'] in valid_channels]
for name,changes,switch,users in route_inputs:
    trace=[]
    class History:
        def add(self,**kw): trace.append(dict(op='history',message=norm(kw)))
    class Events:
        def send_event(self,etype=None,data=None): trace.append(dict(op='notice_event',event=norm(etype),data=norm(data)))
    class MQ:
        def send_message(self,*a,**kw): trace.append(dict(op='queue_call',args=norm(a),kwargs=norm(kw)))
    class User:
        def get_settings(self,key): trace.append(dict(op='user_settings',key=key)); return copy.deepcopy(users.get(key))
    class Service:
        @staticmethod
        def get_notification_switch(kind): trace.append(dict(op='switch',kind=norm(kind))); return switch
    scope['UserOper']=User;scope['ServiceConfigHelper']=Service
    scope['settings']=types.SimpleNamespace(SUPERUSER='root')
    scope['EventType']=types.SimpleNamespace(NoticeMessage='notice.message')
    data=dict(title='owned title',text='owned text');data.update(changes)
    obj=Chain();obj.messageoper=History();obj.eventmanager=Events();obj.messagequeue=MQ()
    Clock.at=datetime(2026,1,2,12,34,56)
    obj.post_message(Notification(**data))
    routes.append(dict(name=name,input=data,switch=switch,users=users,trace=trace))

# Exercise actual async statements without making an executor or network thread.
# The recording executor executes synchronously and returns a completed awaitable;
# this proves decision/argument behavior, not asyncio executor scheduling.
class DoneAwaitable:
 def __await__(self):
  if False:yield None
  return None
class RecordingLoop:
 def run_in_executor(self,executor,callback):callback();return DoneAwaitable()
scope['asyncio']=types.SimpleNamespace(get_running_loop=lambda:RecordingLoop())
async_queue=[]
for sync_case in queue_cases:
 calls=[];obj=Q.__new__(Q);obj.schedule_periods=[tuple(p) for p in sync_case['periods']];obj.queue=queue.Queue()
 def sender(*a,**kw):
  calls.append(dict(args=norm(a),kwargs=norm(kw)))
  if sync_case['callback_error']:raise RuntimeError('synthetic')
 obj.send_callback=sender;Clock.at=datetime(2026,1,2,*sync_case['now'])
 coroutine=obj.async_send_message('post_message',message='owned-message',immediately=sync_case['immediately'],extra='retained')
 try:next(coroutine.__await__());raise AssertionError('unexpected suspension')
 except StopIteration:pass
 pending=[]
 while not obj.queue.empty():pending.append(norm(obj.queue.get_nowait()))
 row=copy.deepcopy(sync_case);row.update(calls=calls,pending=pending);async_queue.append(row)
time_minutes=[]
for text in ['00:00','23:59','9:3','-1:90','９:٣٠','1_8:00',' 1 : 2 ','1:2:3','1','bad:2','1.0:2']:
 try:value,error=Q._time_to_minutes(text),None
 except Exception as e:value,error=None,type(e).__name__
 time_minutes.append(dict(input=text,value=value,error=error))

route_failures=[]
for name,failure,changes,switch,users,kw in [
 ('history-error','history',{},None,{},{}),
 ('switch-error','switch',{'mtype':'站点'},'admin',{},{}),
 ('user-settings-error','user_settings',{'mtype':'站点'},'admin',{},{}),
 ('event-error','notice_event',{},None,{},{}),
 ('queue-error','queue_call',{},None,{},{}),
 ('second-event-error','notice_event:2',{'mtype':'站点','username':'alice'},'admin,user',{'root':{},'alice':{}},{}),
 ('second-queue-error','queue_call:2',{'mtype':'站点','username':'alice'},'admin,user',{'root':{},'alice':{}},{}),
 ('switch-not-string',None,{'mtype':'站点'},True,{},{}),
 ('duplicate-message-original',None,{},None,{},{'message':'duplicate'}),
 ('duplicate-immediately-original',None,{},None,{},{'immediately':False}),
 ('duplicate-immediately-isolated-passed-through',None,{'mtype':'站点'},'admin',{'root':{}},{'immediately':'false'}),
 ('duplicate-message-isolated',None,{'mtype':'站点'},'admin',{'root':{}},{'message':'duplicate'}),
 ('explicit-current-time-null',None,{},None,{},{'current_time':None}),
 ('numeric-zero-id-routes',None,{'userid':0,'mtype':'站点'},'admin',{'root':{}},{}),
 ('unicode-system-normalized',None,{'userid':'\u001cSyStEm\u00a0','mtype':'站点'},'admin',{'root':{}},{}),
 ('empty-switch-no-isolation',None,{'mtype':'站点'},'',{},{}),
]:
 trace=[];counts={}
 def record(row):
  op=row['op'];counts[op]=counts.get(op,0)+1;trace.append(row)
  if failure==op or failure==op+':'+str(counts[op]): raise RuntimeError('synthetic '+op)
 class History:
  def add(self,**kw):record(dict(op='history',message=norm(kw)))
 class Events:
  def send_event(self,etype=None,data=None):record(dict(op='notice_event',event=norm(etype),data=norm(data)))
 class MQ:
  def send_message(self,*a,**kw):record(dict(op='queue_call',args=norm(a),kwargs=norm(kw)))
 class User:
  def get_settings(self,key):record(dict(op='user_settings',key=key));return copy.deepcopy(users.get(key))
 class Service:
  @staticmethod
  def get_notification_switch(kind):record(dict(op='switch',kind=norm(kind)));return switch
 scope['UserOper']=User;scope['ServiceConfigHelper']=Service
 data=dict(title='owned title',text='owned text');data.update(changes)
 obj=Chain();obj.messageoper=History();obj.eventmanager=Events();obj.messagequeue=MQ()
 Clock.at=datetime(2026,1,2,12,34,56)
 try:obj.post_message(Notification(**data),**kw);error=None
 except Exception as e:error=type(e).__name__
 route_failures.append(dict(name=name,input=data,switch=switch,users=users,kwargs=kw,failure=failure,error=error,trace=trace))

# Compact real-Pydantic coercion matrix; full envelopes above pin defaults.
coercions=[]
tokens=['','0','00','1','_1','1_','1__0','+1','-1','1_0','.0','1.0','1.00','1.0_0','1_0.0','1_0.0_0','01.0','+01.00','-0.000','1.','1e0','1E2','NaN','inf','٠','１２','1\x00','1\n2','0__1','00__0','0_0_1','0_.0','0_00.00','-0__1','0_1_2','000-1','+-1','-+1','--1']
for prefix in ['', ' ', '\t', '\u00a0', '\u001c']:
 for suffix in ['', ' ', '\n', '\u2003']:
  for token in tokens:
   value=prefix+token+suffix
   try:out,error=norm(Notification(action=value).action),None
   except pydantic.ValidationError as e:out,error=None,[dict(type=r['type'],loc=list(r['loc'])) for r in e.errors(include_url=False)]
   coercions.append(dict(field='action',input=value,value=out,errors=error))
for value in ['9'*4299,'9'*4300,'+'+'9'*4300,' '+'9'*4300,'9'*4300+' ', '1_'*2150+'1', '9'*4298+'.0','9'*4300+'.0','0'*4301,'a'*4301,'+'*4301,'0'*4301+'1','-0'+'9'*4300,'1.'+'0'*4301]:
 try:out,error=norm(Notification(action=value).action),None
 except pydantic.ValidationError as e:out,error=None,[dict(type=r['type'],loc=list(r['loc'])) for r in e.errors(include_url=False)]
 coercions.append(dict(field='action',input=value,value=out,errors=error))

integrated=[]
for name,changes in [('window-delays-core-not-notice',{}),('explicit-user-bypasses-window',{'userid':'42'}),('admin-route-still-windowed',{'mtype':'站点'})]:
 trace=[]
 class History:
  def add(self,**kw):trace.append(dict(op='history',message=norm(kw)))
 class Events:
  def send_event(self,etype=None,data=None):trace.append(dict(op='notice_event',event=norm(etype),data=norm(data)))
 class User:
  def get_settings(self,key):trace.append(dict(op='user_settings',key=key));return {}
 class Service:
  @staticmethod
  def get_notification_switch(kind):trace.append(dict(op='switch',kind=norm(kind)));return 'admin'
 scope['UserOper']=User;scope['ServiceConfigHelper']=Service
 obj=Chain();obj.messageoper=History();obj.eventmanager=Events()
 q=Q.__new__(Q);q.queue=queue.Queue();q.schedule_periods=[(9,0,18,0)]
 def sender(*a,**kw):trace.append(dict(op='sender_attempt',args=norm(a),kwargs=norm(kw)))
 q.send_callback=sender;obj.messagequeue=q;Clock.at=datetime(2026,1,2,23,0,0);scope['datetime']=Clock
 data=dict(title='owned title',text='owned text');data.update(changes)
 obj.post_message(Notification(**data))
 before=copy.deepcopy(trace)
 # Observe a queue-owned snapshot without consuming it.
 pending=norm(list(q.queue.queue))
 Clock.at=datetime(2026,1,2,12,0,0);q._running=True
 class OnceWait:
  def wait(self,_):return True
 q._stop_event=OnceWait();q.check_interval=10;scope['global_vars']=types.SimpleNamespace(is_system_stopped=False)
 q._monitor_loop()
 integrated.append(dict(name=name,input=data,before=before,pending=pending,after=trace,remaining=norm(list(q.queue.queue))))

print(json.dumps(dict(schema=1,provenance=dict(source_sha256=sha,python=sys.version,pydantic=pydantic.__version__,pydantic_core=pydantic_core.__version__,execution='Actual installed Notification AST with real installed Pydantic and enums; actual Chain/Template/Queue statements against explicit recording history/config/user/event/sender dependencies. No app import, runtime configuration, DB, real thread, network or message sender. Complete template engine/recipient database/transport not verified.'),enums={name:[e.value for e in scope[name]] for name in ['NotificationType','ContentType','MessageChannel']},envelopes=envelopes,periods=periods,windows=windows,queue=queue_cases,monitor=monitor_cases,routes=routes,route_failures=route_failures,async_queue=async_queue,time_minutes=time_minutes,coercions=coercions,integrated=integrated),ensure_ascii=False,indent=2))
