"""Execute pinned installed plugin in synthetic modules; no real transport/config."""
import ast,base64,copy,enum,gzip,hashlib,importlib,json,os,pathlib,shlex,subprocess,types

base=pathlib.Path(__file__).parent
manifest=json.loads((base/'frozen-source-manifest.json').read_text())
remote=r'''
import ast,base64,copy,datetime,enum,hashlib,importlib,json,pathlib,sys,types,uuid
from collections import OrderedDict
manifest=MANIFEST
sources={}
for item in manifest:
 raw=(pathlib.Path('/app/app/plugins/mergemessagenotify')/item['path']).read_bytes()
 if hashlib.sha256(raw).hexdigest()!=item['source_sha256']:raise RuntimeError('source changed')
 sources[item['path']]=raw.decode()
def mod(name):
 m=types.ModuleType(name);m.__path__=[];sys.modules[name]=m
 if '.' in name:
  parent,child=name.rsplit('.',1)
  if parent not in sys.modules:mod(parent)
  setattr(sys.modules[parent],child,m)
 return m
for name in ['app','app.core','app.core.event','app.core.config','app.helper','app.helper.module','app.log','app.plugins','app.schemas','app.schemas.types','app.plugins.mergemessagenotify']:mod(name)
class Logger:
 def __getattr__(self,name):return lambda *args,**kwargs:None
sys.modules['app.log'].logger=Logger()
settings=types.SimpleNamespace(PROXY={'https':'http://proxy.invalid:9000'},TZ='Asia/Shanghai')
sys.modules['app.core.config'].settings=settings
types_source=pathlib.Path('/app/app/schemas/types.py').read_text()
tree=ast.parse(types_source)
for n in tree.body:
 if isinstance(n,ast.ClassDef) and n.name in ('EventType','NotificationType'):exec(compile(ast.Module(body=[n],type_ignores=[]),'<real-types>','exec'),sys.modules['app.schemas.types'].__dict__|{}) if False else None
typesmod=sys.modules['app.schemas.types'];typesmod.Enum=enum.Enum
for n in tree.body:
 if isinstance(n,ast.ClassDef) and n.name in ('EventType','NotificationType'):exec(compile(ast.Module(body=[n],type_ignores=[]),'<real-types>','exec'),typesmod.__dict__)
class Event:
 def __init__(self,event_data=None):self.event_data=event_data
sys.modules['app.core.event'].Event=Event
sys.modules['app.core.event'].eventmanager=types.SimpleNamespace(register=lambda *args:lambda f:f)
class Base:
 def get_config(self):return copy.deepcopy(self.stored)
 def update_config(self,config):self.updates.append(copy.deepcopy(config));self.stored=copy.deepcopy(config);return self.update_result
sys.modules['app.plugins']._PluginBase=Base
comps=[]
sys.modules['app.helper.module'].ModuleHelper=types.SimpleNamespace(load=lambda package_path,filter_func:[c for c in comps if filter_func(c.__name__,c)] if package_path.endswith('.custom') else [])
trace=[];replies=[]
def canon(v):
 if isinstance(v,bytes):return {'$bytes':base64.b64encode(v).decode()}
 if isinstance(v,enum.Enum):return {'$enum':v.name,'value':v.value}
 if isinstance(v,dict):return {str(k):canon(x) for k,x in v.items()}
 if isinstance(v,(list,tuple)):return [canon(x) for x in v]
 return v
class Response:
 def __init__(self,r):self.r=r;self.status_code=r.get('status',200);self.reason='synthetic';self.text=r.get('text','');self.content=base64.b64decode(r.get('content',''))
 @property
 def ok(self):return self.status_code<400
 def __bool__(self):return self.ok
 def json(self):
  if self.r.get('json_error'):raise ValueError('synthetic JSON failure')
  return copy.deepcopy(self.r.get('json',{}))
 def raise_for_status(self):
  if not self.ok:raise RuntimeError('synthetic HTTP status')
def request(method=None,**kw):
 trace.append({'kind':'http','method':method,**canon(kw)})
 r=replies.pop(0) if replies else {'json':{'errcode':0,'code':0,'res':0,'retcode':0,'status':1},'text':'success'}
 if r.get('error'):raise RuntimeError('synthetic transport failure')
 return Response(r)
requests=mod('requests');requests.request=request;requests.post=lambda **kw:request('post',**kw);requests.get=lambda **kw:request('get',**kw)
apprise=mod('apprise')
class Apprise:
 def __init__(self):self.urls=[]
 def add(self,url):self.urls.append(url);return True
 def notify(self,**kw):trace.append({'kind':'apprise','urls':self.urls,**canon(kw)});return replies.pop(0).get('result',True) if replies else True
apprise.Apprise=Apprise
smtp=mod('smtplib')
smtp_fail=None
def smtp_maybe(stage):
 if smtp_fail==stage:raise RuntimeError('synthetic SMTP '+stage)
class SMTP:
 def __init__(self,**kw):trace.append({'kind':'smtp_connect','ssl':type(self).__name__=='SMTP_SSL',**canon(kw)});smtp_maybe('connect')
 def starttls(self):trace.append({'kind':'smtp_starttls'});smtp_maybe('starttls')
 def login(self,**kw):trace.append({'kind':'smtp_login',**canon(kw)});smtp_maybe('login')
 def sendmail(self,**kw):trace.append({'kind':'smtp_sendmail',**canon(kw)});smtp_maybe('sendmail');return {}
 def quit(self):trace.append({'kind':'smtp_quit'});smtp_maybe('quit')
class SMTP_SSL(SMTP):pass
smtp.SMTP=SMTP;smtp.SMTP_SSL=SMTP_SSL
prefix='app.plugins.mergemessagenotify'
for path in ['module.py','util.py','channel/__init__.py','channel/custom/__init__.py']+sorted(p for p in sources if p.startswith('channel/custom/') and not p.endswith('__init__.py')):
 name=prefix+'.'+path[:-3].replace('/','.')
 if name.endswith('.__init__'):name=name[:-9]
 m=mod(name);m.__file__='<pinned>/'+path;exec(compile(sources[path],m.__file__,'exec'),m.__dict__)
 for v in list(m.__dict__.values()):
  if isinstance(v,type) and v.__module__==name and hasattr(v,'comp_key') and v.__name__ not in ('Channel','CustomChannel'):comps.append(v)
m=sys.modules[prefix];m.__file__='<pinned>/__init__.py';exec(compile(sources['__init__.py'],m.__file__,'exec'),m.__dict__)
Plugin=m.MergeMessageNotify
for name,module in list(sys.modules.items()):
 if name.startswith(prefix+'.channel.custom.'):
  if 'time' in module.__dict__:module.time=types.SimpleNamespace(time=lambda:1700000000.125)
  if 'uuid' in module.__dict__:module.uuid=types.SimpleNamespace(uuid4=lambda:uuid.UUID('01234567-89ab-cdef-0123-456789abcdef'))
  if 'datetime' in module.__dict__:
   class Date:
    @classmethod
    def now(cls,tz):return datetime.datetime.fromtimestamp(1700000000.125,tz)
   module.datetime=Date
def new(config):
 Plugin._MergeMessageNotify__comp_objs=OrderedDict()
 p=Plugin();p.stored=copy.deepcopy(config);p.updates=[];p.update_result=True;p.init_plugin(copy.deepcopy(config));return p
def outcome(fn):
 try:return {'return':canon(fn())}
 except Exception as e:return {'error':type(e).__name__}
out={'forms':{},'core':[],'channels':[]}
p=new({});form=p.get_form();out['forms']['plugin']={'elements':form[0],'data':form[1],'updates':p.updates}
for c in sorted(comps,key=lambda c:(c.comp_order,c.comp_key)):
 p=new({});obj=p._MergeMessageNotify__comp_objs[c.comp_key];forms=obj.get_form()
 out['forms'][c.comp_key]={'name':c.comp_name,'order':c.comp_order,'defaults':c.config_default,'elements':forms[0],'data':forms[1],'updates':p.updates,'save_default':bool(p.updates)}
configs=[None,{}, {'enabled':False,'enable':False},{'enable':False},{'enable':True},{'enable':'yes','channel_strategy':None},{'enable':True,'channel_strategy':'ORDER_SUCCESS_ONE'},{'enable':True,'channel_strategy':'unknown'}]
events=[None,{}, {'channel':'system','title':'t'},{'title':''},{'title':'t'},{'text':'x'},{'title':'t','text':'x','type':typesmod.NotificationType.Download},{'title':'t','text':'x','mtype':typesmod.NotificationType.Plugin}]
for cfg in configs:
 for event in events:
  for results in [[False,True,True],[True,False,True],['error',True,False]]:
   config=copy.deepcopy(cfg)
   if config is not None:config['enable_channels']=['custom.iyuu',None,'missing','custom.bark','custom.iyuu','custom.pushplus']
   p=new(config);calls=[]
   for k,obj in p._MergeMessageNotify__comp_objs.items():
    def send(*args,_key=k,**kw):
     calls.append({'key':_key,**canon(kw)});v=results[(len(calls)-1)%len(results)]
     if v=='error':raise ValueError('synthetic')
     return v
    obj.send_message=send
   r=outcome(lambda:p.listen_notice_message_event(Event(copy.deepcopy(event))))
   out['core'].append({'config':canon(config),'event':canon(event),'results':results,'state':p.get_state(),'calls':calls,'outcome':r,'config_after':p._MergeMessageNotify__config})
# Each channel uses synthetic-only credentials/endpoints. Missing config, exact
# response comparison (None/False/0/"0"), type filtering and body variants.
cfgs={
 'apprise':{'service_urls':'  json://notify.invalid/a\n\n  json://notify.invalid/b  '},
 'bark':{'server_url':'https://notify.invalid/','push_key':'synthetic-key'},
 'chanify':{'token':'synthetic-token'},'dingtalkrobot':{'access_token':'synthetic-token'},
 'email':{'smtp_host':'mail.invalid','smtp_port':465,'smtp_encrypt_type':'SSL','username':'sender@example.invalid','password':'synthetic-password','to_addrs':'a@example.invalid'},
 'feishubot':{'access_token':'synthetic-token'},'gotify':{'server_url':'https://notify.invalid/','token':'synthetic-token'},
 'http':{'url':'https://notify.invalid/${title}','headers':'Content-Type:application/json','body':'{"title":"${title}","text":"${text}"}'},
 'iyuu':{'token':'synthetic-token'},'ntfy':{'token':'synthetic-token'},
 'onebot-11':{'server_url':'https://notify.invalid/','user_ids':'101'},
 'pushdeer':{'push_key':'synthetic-key'},'pushme':{'server_url':'https://notify.invalid/','key_type':'push_key','key':'synthetic-key'},
 'pushover':{'user_key':'synthetic-user','api_token':'synthetic-token'},'pushplus':{'token':'synthetic-token'},
 'qiyeweixinbot':{'key':'synthetic-key'},'serverchan':{'send_key':'synthetic-key'}}
responses=[{'json':{}},{'json':{'code':0,'errcode':0,'res':0,'retcode':0,'status':1},'text':'success'},{'json':{'code':200,'errcode':'0','res':None,'retcode':1,'status':0}},{'status':500,'json':{}},{'status':500,'json':{'code':0,'errcode':0,'res':0,'retcode':0}},{'status':200,'json_error':True},{'error':True},{'json':{'code':'0','errcode':False,'res':False,'retcode':True,'status':True}}]
messages=[{'title':'合成标题','text':'合成正文'}, {'title':None,'text':'正文'}, {'title':'标题','text':None}, {'title':'','text':''}, {'title':'标题','text':'正文','ext_info':{'image':'https://image.invalid/a.png','link':'https://detail.invalid/a','extra':2}}]
for c in sorted(comps,key=lambda c:(c.comp_order,c.comp_key)):
 key=c.comp_key;cfg=cfgs[key.removeprefix('custom.')]
 for variant in [{},cfg,dict(cfg,enable_notify_types=['Plugin']),dict(cfg,enable_notify_types=[]),dict(cfg,enable_notify_types='Download')]:
  for msg in messages:
   for reply in responses:
    trace.clear();replies[:]=[copy.deepcopy(reply)]*8;p=Plugin();p.stored={key+'.'+k:v for k,v in variant.items()};p._MergeMessageNotify__config=copy.deepcopy(p.stored);p.updates=[];p.update_result=True;obj=c(plugin=p)
    r=outcome(lambda:obj.send_message(**copy.deepcopy(msg),type=typesmod.NotificationType.Download))
    out['channels'].append({'key':key,'config':variant,'message':msg,'type':'Download','replies':[reply]*8,'outcome':r,'trace':copy.deepcopy(trace)})
# Additional source branch cases (including original mistakes) are executable
# references, not assertions synthesized by the Go port.
extra={
 'bark':[],
 'dingtalkrobot':[{'secret':'synthetic-signing-key','atMobiles':'101','atUserIds':'202','isAtAll':True}],
 'feishubot':[{'secret':'synthetic-signing-key','at_user_ids':'202','at_all':True}],
 'qiyeweixinbot':[{'mentioned_mobile_list':'101','mentioned_list':'202','isAtAll':True}],
 'ntfy':[{'token':None,'username':'合成用户','password':'synthetic-password','enable_md':False,'topic':'主题/with space'}],
 'pushme':[{'key_type':'temp_key','message_topic':'success','message_channel':'合成渠道'},{'key_type':'unknown','message_topic':'unknown'}],
 'pushover':[{'priority':2,'sound':'bike'},{'priority':0},{'priority':'2'}],
 'serverchan':[{'channel':[18,9,1],'noip':True}],
 'onebot-11':[{'user_ids':'101','group_ids':'202'},{'user_ids':' , ','group_ids':None}],
 'http':[{'headers':'Content-Type:application/x-www-form-urlencoded','body':'{"title":"${title}"}'},{'headers':'X-Header:a:b'},{'headers':'[]','body':'x'},{'headers':None,'params':'a:b','body':None},{'template_variables':'{"title":"自定义"}'},{'body':'{"title":"${title}","value":${value}}','template_variables':'{"value":9007199254740993}'}, {'body':'literal','headers':'Content-Type:text/plain'}],
 'email':[{'smtp_encrypt_type':'TLS'},{'smtp_encrypt_type':'NONE'},{'smtp_encrypt_type':'unknown'}],
}
extra['bark']=[{'sound':'bell','autoCopy':True,'isArchive':False,'more_json':'{"title":"覆盖","extra":1,"body":"\\u263a"}'},{'more_json':'not JSON'}]+[{'cipher_enable':True,'cipher_algorithm':a,'cipher_mode':m,'cipher_padding':p,'cipher_key':'k'*n} for a,n in [('AES128',16),('AES192',24),('AES256',32)] for m in ['CBC','ECB','GCM'] for p in ['pkcs7','noPadding']]+[{'cipher_enable':True,'cipher_algorithm':'AES128','cipher_mode':'GCM','cipher_key':'short'}]
extra['bark'] += [{'more_json':v,'cipher_enable':True,'cipher_algorithm':'AES128','cipher_mode':'ECB','cipher_padding':'noPadding','cipher_key':'k'*16} for v in ['[[\"title\",\"覆盖\"],[\"other\",2]]','[\"xy\"]','[[true,1],[1,2],[null,3],[1.5,4]]','[[\"first\",1],[\"bad\"],[\"last\",2]]','[[{},1]]','{\"float\":1000000.0,\"tiny\":1e-5,\"exp\":1e20}']]
for c in sorted(comps,key=lambda c:(c.comp_order,c.comp_key)):
 key=c.comp_key;basic=cfgs[key.removeprefix('custom.')]
 for more in extra.get(key.removeprefix('custom.'),[])+[{'enable_proxy':True}]:
  variant=dict(basic,**more)
  for msg in messages:
   for reply in [responses[1],responses[3],responses[5],responses[6]]:
    trace.clear();replies[:]=[copy.deepcopy(reply)]*8;p=Plugin();p.stored={key+'.'+k:v for k,v in variant.items()};p._MergeMessageNotify__config=copy.deepcopy(p.stored);p.updates=[];p.update_result=True;obj=c(plugin=p)
    r=outcome(lambda:obj.send_message(**copy.deepcopy(msg),type=typesmod.NotificationType.Download))
    out['channels'].append({'key':key,'config':variant,'message':msg,'type':'Download','replies':[reply]*8,'outcome':r,'trace':copy.deepcopy(trace)})
out['lifecycle']=[]
save_ns={}
exec(compile('def set_plugin_config(p, config):\n p.init_plugin(config)\n','/synthetic/app/api/endpoints/plugin.py','exec'),save_ns)
for initial in [None,{}, {'enabled':False,'enable':False},{'enable':True,'_tabs':'keep','undefined':7,'channel_strategy':None,'custom.bark.server_url':None,'custom.bark.isArchive':False,'unknown':{'number':9007199254740993}}]:
 for update_result in [True,False]:
  p=new(initial);p.update_result=update_result;steps=[]
  for action,key,value in [('get','custom.bark',None),('update','custom.bark',{'token':'synthetic','':1,'server_url':None,'isArchive':False}),('get','custom.bark',None),('update','',{'x':1}),('update','custom.bark',None),('form',None,None),('stop',None,None),('form',None,None)]:
   if action=='get':r=outcome(lambda:p.get_comp_config(key))
   elif action=='update':r=outcome(lambda:p.update_comp_config(key,value))
   elif action=='form':r=outcome(lambda:p.get_form())
   else:r=outcome(lambda:p.stop_service())
   steps.append({'action':action,'key':key,'value':value,'outcome':r,'config':copy.deepcopy(p._MergeMessageNotify__config),'stored':p.get_config(),'registered':list(p._MergeMessageNotify__comp_objs.keys())})
  out['lifecycle'].append({'initial':initial,'update_result':update_result,'steps':steps,'updates':p.updates,'api':p.get_api(),'commands':p.get_command(),'page':p.get_page(),'services':p.get_service()})
out['test_once']=[]
for cls in sorted(comps,key=lambda c:(c.comp_order,c.comp_key)):
 for save_request in [False,True]:
  for result in [False,True,'error']:
   config={'enable':False,cls.comp_key+'.test_once':True,'unknown':7}
   p=new(None);calls=[];obj=p._MergeMessageNotify__comp_objs[cls.comp_key]
   def send(**kw):
    calls.append(canon(kw))
    if result=='error':raise ValueError('synthetic')
    return result
   obj.send_message=send
   p.stored=copy.deepcopy(config)
   r=outcome(lambda:save_ns['set_plugin_config'](p,copy.deepcopy(config)) if save_request else p.init_plugin(copy.deepcopy(config)))
   out['test_once'].append({'key':cls.comp_key,'save_request':save_request,'send_result':result,'config':config,'outcome':r,'calls':calls,'updates':p.updates,'config_after':p._MergeMessageNotify__config,'stored':p.get_config(),'registered':list(p._MergeMessageNotify__comp_objs.keys())})
out['smtp_errors']=[]
email_cls=next(c for c in comps if c.comp_key=='custom.email')
for encrypt in ['NONE','SSL','TLS']:
 for smtp_fail in [None,'connect','starttls','login','sendmail','quit']:
  variant=dict(cfgs['email'],smtp_encrypt_type=encrypt);p=Plugin();p._MergeMessageNotify__config={'custom.email.'+k:v for k,v in variant.items()};obj=email_cls(plugin=p);trace.clear()
  r=outcome(lambda:obj.send_message(title='合成标题',text='合成正文'))
  out['smtp_errors'].append({'encrypt':encrypt,'stage':smtp_fail,'config':variant,'outcome':r,'trace':copy.deepcopy(trace)})
out['templates']=[]
render=sys.modules[prefix+'.util'].TemplateUtil.render_text
template_vars={'title':'合成标题<&>','text':'正文\n第二行','image':None,'n':1000000.0,'flag':True,'xs':['甲','乙']}
for required,text in [(True,'plain'),(True,'${title}'),(True,'${missing}'),(True,'${n}'),(True,'${flag}'),(True,'% if image:\nimage=${image}\n% endif\n% if title:\n${title}\n% endif\n'),(False,'${xs}'),(False,'${None}'),(False,'${True}'),(False,'${title.upper()}'),(False,'${1+2}'),(False,'${title | h}'),(False,'## comment\nplain'),(False,'abc\\\ndef'),(False,'% if image:\nyes\n% else:\nno\n% endif\n'),(False,'% for x in xs:\n${x}\n% endfor\n'),(False,'<% x=1 %>${x}'),(False,'<%def name="hello(x)">Hello ${x}</%def>${hello(title)}'),(False,'<%text>${literal}</%text>')]:
 out['templates'].append({'basic':required,'text':text,'variables':template_vars,'result':render(text,copy.deepcopy(template_vars))})
print(json.dumps(canon(out),ensure_ascii=False,separators=(',',':')))
'''.replace('MANIFEST',repr(manifest),1)
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,shlex.join(['docker','exec','-i',container,'python3','-'])],input=remote,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
(base.parent/'forms.json').write_text(json.dumps(d.pop('forms'),ensure_ascii=False,indent=2)+'\n')
for kind,cases in d.items():
 p=base.parent/'testdata'/('installed-'+kind+'.jsonl.gz')
 with gzip.GzipFile(p,'wb',mtime=0) as f:
  for case in cases:f.write((json.dumps(case,ensure_ascii=False,separators=(',',':'))+'\n').encode())
 print(kind,len(cases),hashlib.sha256(p.read_bytes()).hexdigest())
