"""Original installed AST, fake transports only. Run in a disposable Python process.
No import of app, no access to config or plugin instances, no external HTTP.
"""
import ast,asyncio,base64,copy,enum,gzip,hashlib,json,pathlib,re,signal,sys,time,types,typing,uuid,__future__
from typing import *
from pathlib import Path
from pydantic import BaseModel,Field,field_validator
signal.alarm(60)
SOURCE=pathlib.Path('/app/app/plugins/larkmessager')
EXPECTED={'__init__.py':'d9f6a1078706b03dc97247450a8084a8c67f07dbfccb675235fc0e1b91b18372','client.py':'28ac4a3dae4879a86bba956f493b60121948fb2ca5e631d81c4868c604468654','crypto.py':'8867ad8e0f7fedd59cc0c3b64058d8d76b1e851459188688dbd35904b11e9529','schemas.py':'701b1bda58ea5ccc4eda6e71b173813c9e3b340716748086f68abfd0bd5234c4'}
class Logger:
 def __getattr__(self,n):return lambda *a,**k:None
logger=Logger()
class Resp:
 status_code=200;headers={};content=b'';text=''
 def __init__(self,data=None,status=200):self.data=data;self.status_code=status;self.text=json.dumps(data)
 def json(self):return copy.deepcopy(self.data)
 def raise_for_status(self):
  if self.status_code>=400:raise RuntimeError('synthetic status')
class Requests:
 def reset(self,fault=0):self.calls=[];self.fault=fault
 def __getattr__(self,method):
  def call(url,**kwargs):
   self.calls.append({'method':method.upper(),'url':url,**copy.deepcopy(kwargs)})
   if self.fault==len(self.calls):raise RuntimeError('synthetic transport fault')
   if '/tenant_access_token/' in url:return Resp({'code':0,'tenant_access_token':'SYNTHETIC_TOKEN','expire':7200})
   return Resp({'code':0,'data':{'message_id':'om_reply','chat_id':'oc_reply','msg_type':'text','root_id':'om_root','parent_id':'om_parent','thread_id':'omt_thread'}})
  return call
requests=Requests();requests.reset()
ns=dict(globals());ns['API_BASE']='https://open.larksuite.com/open-apis';ns['_PluginBase']=object
# Exact source enum/model classes, with no application imports or runtime objects.
for source,names in [('/app/app/schemas/types.py',{'MessageChannel','NotificationType','ContentType'}),('/app/app/schemas/message.py',{'MessageResponse','CommingMessage','Notification'})]:
 t=ast.parse(pathlib.Path(source).read_text());nodes=[n for n in t.body if isinstance(n,ast.ClassDef) and n.name in names];ns['Enum']=enum.Enum
 exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),source,'exec',flags=__future__.annotations.compiler_flag),ns)
# make only the source's local NotificationType import resolvable, never import app.
m=types.ModuleType('app.schemas.types');m.NotificationType=ns['NotificationType'];sys.modules['app.schemas.types']=m
for name in ('client.py','crypto.py','schemas.py','__init__.py'):
 raw=(SOURCE/name).read_bytes();assert hashlib.sha256(raw).hexdigest()==EXPECTED[name]
 tree=ast.parse(raw);nodes=[n for n in tree.body if isinstance(n,ast.ClassDef)]
 for c in nodes:
  if c.name=='LarkMessager':
   for f in c.body:
    if isinstance(f,(ast.FunctionDef,ast.AsyncFunctionDef)):
     f.decorator_list=[d for d in f.decorator_list if not isinstance(d,ast.Call)]
 exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),str(SOURCE/name),'exec',flags=__future__.annotations.compiler_flag),ns)
# Crypto's only external primitive is the actual installed AES package.
from Crypto.Cipher import AES
ns['AES']=AES;ns['hashlib']=hashlib;ns['hmac']=__import__('hmac')
Client=ns['LarkClient'];Plugin=ns['LarkMessager'];Crypto=ns['LarkCrypto']
for name in ['CommingMessage','Notification','MessageResponse','LarkWebhookEvent']:
 ns[name].model_rebuild(_types_namespace=ns)

uuid.uuid4=lambda:uuid.UUID('11111111-2222-4333-8444-555555555555');time.time=lambda:2000000000
cases=[]
def record(kind,input,fn):
 try:
  out=fn()
  if hasattr(out,'model_dump'):out=out.model_dump(mode='json')
  cases.append({'kind':kind,'input':input,'output':out})
 except Exception as e:cases.append({'kind':kind,'input':input,'error':type(e).__name__})
# Pure card construction includes exact source double escaping and clipping.
for title in [None,'','Title',' <title>\\ ','中文🙂'*8,'\x1c']:
 for text in [None,'','hello',' <hello>\\ ','![alt](https://example.invalid/a.png)','\n  \n']:
  for key in [None,'img_owned']:
   for buttons in [None,[[{'text':'Go','callback_data':'/search a'},{'text':'Link','url':'https://example.invalid/'}]],[[],[{'text':''},{'text':'🙂'*24}]]]:
    inp={'title':title,'text':text,'link':'https://example.invalid/details' if text=='hello' else None,'buttons':buttons,'image_key':key,'header_template':'green'}
    record('card',inp,lambda:Client.build_card_v2(**inp))
for items in [[],[{'title':'A'}],[{'title':'','image_key':'img_a'}],[{'title':' <A>\\ ','image_key':'img_a'},{'title':' B '}],[{},{}]]:
 inp={'title':'List','text':'Body','link':None,'buttons':None,'image_items':items};record('card',inp,lambda:Client.build_card_v2(**inp))
for s in ['', 'plain', 'before ![ cat ](https://example.invalid/a) after','![x](http://example.invalid/a) ![x](http://example.invalid/a)','![local](feishu://image/om/img)', 'hello ![unfinished', 'hello ![x](part', 'x ![a]\n(b)', '![a](url) trailing','![a](x))']:
 record('stream_helpers',{'text':s},lambda:{'strip':Client._strip_streaming_markdown_images(s),'urls':Client._extract_markdown_image_urls(s),'card':Client._build_streaming_card_payload(' <T>\\ ',s)})
for value in [None,'',' x ',0,False,True,1,{'callback_data':' x '},{'data':'/search'},{'value':'v'}, {'callback_data':'','data':'x'},{},['x']]:
 record('callback',{'value':value,'name':'fallback'},lambda:Client.extract_card_callback_data(value,'fallback'))
for status in [199,200,204,299,300,404,500]:
 for s in ['', 'null\x00','{}','[]','bad','{"code":0}\x00','{"code":1}','{"code":false}', '{"code":"0"}']:
  r=Resp();r.text=s;r.status_code=status;record('cardkit',{'status':status,'body':s},lambda:{'parsed':Client._safe_parse_cardkit(r),'ok':Client._cardkit_ok(r,Client._safe_parse_cardkit(r))})
# Target precedence and source memory.
for uid in [None,'',' u ']:
 for chat in [None,' c ']:
  for typ in [None,'open_id','user_id','chat_id','other']:
   c=Client('SYNTHETIC_APP','SYNTHETIC_SECRET');c.remember_user_id_type('open',' u ');inp={'userid':uid,'chat_id':chat,'receive_id_type':typ,'default_open_id':'default','default_chat_id':'defaultchat'};record('target',inp,lambda:c.resolve_target(**inp))
# Source actual message/post/card receive methods, only forwarding is controlled.
for typ in ['text','image','audio','media','file','post','unknown']:
 for message_id in ['om_in','']:
  for senderid in [{'open_id':'ou_in','user_id':'u_in'},'legacy_sender',{}]:
   content={'text':' hello ','image_key':' img ','file_key':' file ','file_name':' name.opus '}
   if typ=='post':content={'post':{'en_us':{'title':' Heading ','content':[[{'tag':'text','text':'Hi '},{'tag':'at','user_name':'Alice'},{'tag':'a','text':'Link','href':'https://example.invalid/'},{'tag':'img','image_key':'img'},{'tag':'code_block','text':' x ','language':'go'}]]}}}
   body={'header':{'event_type':'im.message.receive_v1'},'event':{'sender':{'sender_id':senderid},'message':{'message_id':message_id,'chat_id':'oc_in','chat_type':'p2p','message_type':typ,'content':json.dumps(content)}}}
   p=Plugin();p._client=Client('synthetic','synthetic');forward=[];p._forward_to_message_chain=lambda payload:forward.append(payload)
   record('receive',body,lambda:(asyncio.run(p._handle_message_receive(ns['LarkWebhookEvent'](**body),body)),forward)[1])
# Full forms and page incl. read-once KV transitions.
p=Plugin();record('form',{},p.get_form)
for enabled in [False,True]:
 for missing in [[],['App ID'],['App ID','App Secret']]:
  p=Plugin();p._enabled=enabled;p._client=Client('synthetic','synthetic') if enabled else None;p._app_id='synthetic';p._app_secret='synthetic';p._config_missing=missing;kv={};p.get_data=lambda key:copy.deepcopy(kv.get(key));p.save_data=lambda k,v:kv.update({k:copy.deepcopy(v)});record('page',{'enabled':enabled,'missing':missing},lambda:{'page':p.get_page(),'kv':kv})
# Transport request/failure prefixes: original client code executed unchanged.
for method in ['send_text','_send_message','_reply_message','add_message_reaction','delete_message_reaction']:
 for fault in [0,1,2]:
  requests.reset(fault);c=Client('SYNTHETIC_APP','SYNTHETIC_SECRET')
  args={'send_text':{'text':'你好','userid':'ou_owned'},'_send_message':{'receive_id':'ou_owned','receive_id_type':'open_id','msg_type':'text','content':{'text':'你好'}},'_reply_message':{'message_id':'om_owned','msg_type':'text','content':{'text':'你好'}},'add_message_reaction':{'message_id':'om_owned','emoji_type':'GLANCE'},'delete_message_reaction':{'message_id':'om_owned','reaction_id':'r_owned'}}[method]
  def run():
   try:return {'result':getattr(c,method)(**args),'calls':requests.calls}
   except Exception as e:return {'error':type(e).__name__,'calls':requests.calls}
  record('client_calls',{'method':method,'args':args,'fault':fault},run)
# Crypto encrypt/decrypt and signature use installed AES, synthetic data only.
import os
old_urandom=os.urandom;os.urandom=lambda n:bytes(range(n))
for key in ['', 'synthetic-encrypt-key','中文 key']:
 for plain in ['', 'hello','中文🙂','{"event":{}}']:
  c=Crypto(key,'unused');record('crypto',{'key':key,'plaintext':plain},lambda:{'encrypted':c.encrypt(plain),'decrypted':c.decrypt(c.encrypt(plain))})
os.urandom=old_urandom
# Additional exact source contracts, no app runtime.
for enabled in [False,True]:
 for missing in [[],['App ID'],['App Secret'],['App ID','App Secret']]:
  p=Plugin();p._enabled=enabled;p._client=Client('synthetic','synthetic') if enabled else None;p._app_id='synthetic';p._app_secret='synthetic';p._config_missing=missing;kv={};p.get_data=lambda key:copy.deepcopy(kv.get(key));p.save_data=lambda k,v:kv.update({k:copy.deepcopy(v)});record('page_template',{'enabled':enabled,'missing':missing},lambda:p.get_page())
for config in [{},{'enabled':True},{'enabled':True,'app_id':' app ','app_secret':' secret ','FEISHU_ADMINS':'ou_a, a@example.invalid, +86123, ou_a','admin_emails':'old@example.invalid','admin_mobiles':'456','FEISHU_OPEN_ID':' default '},{'enabled':'false','app_id':'app','app_secret':'secret','admin_users':'a@example.invalid,ou_a','user_id':'legacy'},{'enabled':False,'app_id':'app','app_secret':'secret','FEISHU_ADMINS':' ','admin_users':'ou_legacy','switchs':['订阅']}]:
 p=Plugin();p._start_admin_sync=lambda:None
 def init():
  p.init_plugin(copy.deepcopy(config));return {k:getattr(p,'_'+k) for k in ['enabled','app_id','app_secret','chat_id','user_id_raw','user_id','verification_token','encrypt_key','admin_users','admin_openids_direct','admin_emails','admin_mobiles','switchs','config_missing']}
 record('init',config,init)
# Actual schema construction and source message_parser, fake binding lookup only.
for message in [ {'sender':{'open_id':'ou_a'},'text':' hi '}, {'sender':{'user_id':'u_a'},'text':'/search film'}, {'sender':{'open_id':'ou_a'},'type':'cardAction','callback_data':'/search a'}, {'sender':{'open_id':'ou_a'},'images':['a',{'url':'b','filename':'B','size':'1_000'},None,False]}, {'sender':{'open_id':'ou_a'},'audio_refs':[' a ','',None,1,False]}, {'sender':{'open_id':'ou_a'},'files':[{'ref':'file','size':'2'},{}]}, {'sender':{},'text':'missing'}, {'sender':{'open_id':'ou_a'},'text':' '}, {'sender':{'open_id':'ou_a'},'type':'cardAction','callback_data':' '}, {'sender':{'open_id':'ou_a'},'images':{'ref':'x','size':'nonnumeric'}} ]:
 p=Plugin();p._admin_users=[];p._client=None;p._resolve_username=lambda open,user,fallback:fallback
 record('message_parser',message,lambda:p.message_parser('Lark',copy.deepcopy(message),None,None))
for body in [{'header':{'event_type':'card.action.trigger'},'action':{'value':{'callback_data':'/search'}},'operator':{'operator_id':{'open_id':'ou_a','user_id':'u_a'}},'message':{'message_id':'om_a'}},{'header':{'event_type':'card.action.trigger'},'event':{'action':{'value':'button'},'operator':{'open_id':'ou_b'},'context':{'open_message_id':'om_b','open_chat_id':'oc_b'}}},{'event':{'action':{'name':'fallback','tag':'button'},'operator':{},'message':{'message_id':'om_c'},'chat_id':'oc_c'}}]:
 p=Plugin();p._client=None;forward=[];p._forward_to_message_chain=lambda v:forward.append(v)
 record('card_action',body,lambda:(asyncio.run(p._handle_card_action(ns['LarkWebhookEvent'](**body),body)),forward)[1])
# The original sync uses a private fake systemconfig, preserving unknown fields.
class SystemConfigKey:Notifications='notifications'
ns['SystemConfigKey']=SystemConfigKey
for configs in [None,[],[{'name':'Other','config':{'x':9007199254740993}}],[{'name':'Lark','type':'old','config':{'FEISHU_ADMINS':'ou_a','FEISHU_OPEN_ID':'ou_old','keep':1}}],[{'name':'Lark','config':'invalid'}],[{'name':'Lark','config':{'FEISHU_ADMINS':'','FEISHU_OPEN_ID':'','keep':1}}]]:
 for ids,user in [([],''),(['ou_b','ou_a','ou_b'],'ou_new'),(['ou_a'],'')]:
  saved=[]
  class Store:
   def get(self,key):return copy.deepcopy(configs)
   def set(self,key,value):saved.append(copy.deepcopy(value))
  p=Plugin();p.systemconfig=Store();record('sync_config',{'configs':configs,'ids':ids,'user':user},lambda:(p._sync_admin_to_notification_config(ids,user),saved)[1])
# Source webhook via actual FastAPI JSONResponse and installed schema/crypto.
from fastapi.responses import JSONResponse
from starlette.datastructures import Headers
ns['JSONResponse']=JSONResponse
for encrypt_key in ['', 'synthetic-key']:
 for token in ['', 'synthetic-token']:
  for body in [{'challenge':'owned-challenge','token':'wrong'},{'type':'url_verification'},{'header':{'event_type':'unknown'}},{'header':{'event_type':'unknown','token':'synthetic-token'}},{'header':{'event_type':'card.action.trigger','token':'synthetic-token'},'event':{}},['not-an-object']]:
   p=Plugin();p._encrypt_key=encrypt_key;p._verification_token=token;p._app_secret='synthetic-secret';p._crypto=Crypto(encrypt_key) if encrypt_key else None
   forwards=[];p._forward_to_message_chain=lambda v:forwards.append(v);p._client=None
   raw=json.dumps(body,ensure_ascii=False).encode()
   class Request:
    method='POST';url=types.SimpleNamespace(path='/webhook');headers=Headers({});query_params={}
    async def body(self):return raw
   def run():
    r=asyncio.run(p._webhook_endpoint(Request()));return {'status':r.status_code,'reply':json.loads(r.body),'forwards':forwards}
   record('webhook',{'raw':raw.decode(),'encrypt_key':encrypt_key,'verification_token':token,'headers':{},'query_token':''},run)
# Deterministic source test-card literal, preserving original fields and time slot.
c=next(n for n in ast.parse((SOURCE/'__init__.py').read_text()).body if isinstance(n,ast.ClassDef));f=next(n for n in c.body if isinstance(n,ast.FunctionDef) and n.name=='_test_endpoint');assignment=next(n for n in ast.walk(f) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='card' for t in n.targets));local={'self':types.SimpleNamespace(plugin_version='0.9.1'),'now':'{{LARK_TEST_TIME}}'};exec(compile(ast.fix_missing_locations(ast.Module(body=[assignment],type_ignores=[])),'source-test-card','exec'),local);record('test_card',{},lambda:local['card'])
exec(compile(base64.b64decode('__ADDITIONAL_BASE64__'),'additional-algorithm-cases','exec'))
for case in cases:print(json.dumps(case,ensure_ascii=False,separators=(',',':')))
