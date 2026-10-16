# Included by the offline oracle only. Original client methods execute unchanged.
import tempfile
class MultiRequests:
 def reset(self,fault=0):self.calls=[];self.fault=fault
 def __getattr__(self,method):
  def call(url,**kwargs):
   safe={}
   for k,v in kwargs.items():
    if k=='files':
     safe[k]={}
     for key,(name,fp,mime) in v.items():safe[k][key]={'name':name,'base64':base64.b64encode(fp.read()).decode(),'mime':mime}
    else:safe[k]=copy.deepcopy(v)
   self.calls.append({'method':method.upper(),'url':url,**safe})
   if len(self.calls)==self.fault:raise RuntimeError('synthetic transport fault')
   if '/tenant_access_token/' in url:return Resp({'code':0,'tenant_access_token':'SYNTHETIC_TOKEN','expire':7200})
   data={'message_id':'om_reply','chat_id':'oc_reply','msg_type':(kwargs.get('json') or {}).get('msg_type','text'),'root_id':'om_root','parent_id':'om_parent','thread_id':'omt_thread','image_key':'img_owned','file_key':'file_owned','card_id':'card_owned','reaction_id':'r_owned'}
   return Resp({'code':0,'data':data})
  return call
multi=MultiRequests();ns['requests']=multi
with tempfile.TemporaryDirectory(prefix='owned-lark-oracle-') as directory:
 root=Path(directory)
 for name in ['owned.png','owned.bin','owned.opus']:(root/name).write_bytes(b'owned bytes')
 methods=[('send_file',{'file_path':str(root/'owned.png'),'userid':'ou_owned','title':'Image','text':'Body'}),('send_file',{'file_path':str(root/'owned.bin'),'userid':'ou_owned','title':'File','text':'Body','file_name':'shown.bin'}),('send_voice',{'voice_path':str(root/'owned.opus'),'userid':'ou_owned','caption':'Caption'}),('send_notification',{'message':ns['Notification'](title='Title',text='Body',image='feishu://image/img_owned')}),('send_notification',{'message':ns['Notification'](title='Agent',text='Hello',mtype=ns['NotificationType'].Agent)}),('edit_message',{'message_id':'om_owned','title':'Edited','text':'Body'}),('edit_message',{'message_id':'om_owned','title':'Agent','text':'Updated','metadata':{'feishu_streaming':{'card_id':'card_owned','element_id':'mp_stream_body','sequence':0}}}),('close_streaming_card',{'card_id':'card_owned','sequence':2})]
 for method,args in methods:
  for fault in [0,1,2,3,4]:
   multi.reset(fault);client=Client('SYNTHETIC_APP','SYNTHETIC_SECRET');callargs=copy.deepcopy(args)
   if method=='send_notification':callargs['userid']='ou_owned'
   inp={'method':method,'fault':fault,'args':{}}
   for k,v in callargs.items():
    if hasattr(v,'model_dump'):v=v.model_dump(mode='json')
    if k in ('file_path','voice_path'):v='owned:'+Path(v).name
    inp['args'][k]=copy.deepcopy(v)
   def run():
    try:return {'result':getattr(client,method)(**callargs),'calls':multi.calls,'args_after':{'metadata':callargs.get('metadata')}}
    except Exception as e:return {'error':type(e).__name__,'calls':multi.calls}
   record('multi_calls',inp,run)
# Actual endpoint methods and both private KV shapes, controlled HTTP only.
import datetime as original_datetime
fixed_datetime=types.ModuleType('datetime');fixed_datetime.__dict__.update(original_datetime.__dict__)
class FixedDateTime(original_datetime.datetime):
 @classmethod
 def now(cls,tz=None):return cls(2030,1,2,3,4,5,tzinfo=tz)
fixed_datetime.datetime=FixedDateTime;sys.modules['datetime']=fixed_datetime
time.strftime=lambda pattern,*args:'2030-01-02 03:04:05'
for method in ['_test_endpoint','_status_endpoint','_fetch_chats_endpoint']:
 for cfg in [{},{'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET'},{'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_OPEN_ID':'ou_owned'},{'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_OPEN_ID':'ou_owned','chat_id':'oc_owned'}]:
  for fault in [0,1,2,3]:
   multi.reset(fault);p=Plugin();p._start_admin_sync=lambda:None;p.init_plugin(copy.deepcopy(cfg));kv={'last_test_result':{'ok':True,'msg':'old','time':'old','displayed':False,'unknown':9007199254740993},'last_chats':{'ok':True,'msg':'old','time':'old','displayed':False,'chats':[]}};p.get_data=lambda k:copy.deepcopy(kv.get(k));p.save_data=lambda k,v:kv.update({k:copy.deepcopy(v)});p.del_data=lambda k:kv.pop(k,None)
   def run():
    response=getattr(p,method)(types.SimpleNamespace(query_params={}));return {'result':json.loads(response.body),'kv':kv,'calls':multi.calls}
   record('endpoint_calls',{'method':method,'config':cfg,'fault':fault},run)
# Dynamic value semantics consumed by callback/post and size/identity models.
values=[None,False,True,0,1,-1,0.0,-0.0,1.5,1e-5,1e-4,1e15,1e16,9007199254740993,'','a\'b','a\"b',"a'\"\\\n",'中文🙂','\x1c\u200b\u2028\ue000','１_２','2.0','2.5','2e2',[],[1,'x',None,True],{}, {'b':2,'a':1},{'outer':{'b':False,'a':[1,'x']}}]
for v in values:
 def convert():
  out={'str':str(v),'repr':repr(v),'truth':bool(v)}
  try:out['int']=int(v)
  except Exception as e:out['int_error']=type(e).__name__
  return out
 record('python_value',{'value':v},convert)
for v in values:
 record('attachment_model',{'size':v},lambda:ns['CommingMessage'].MessageAttachment(ref='owned',size=v).model_dump(mode='json'))
for id in [1,True,1.0,1.5,False,0,'1',' 1 ',{'k':'v'},['x']]:
 p=Plugin();p._admin_users=[];p._client=None;p._resolve_username=lambda open,user,fallback:fallback
 msg={'sender':{'open_id':id},'text':'hello','message_id':id}
 record('message_parser',msg,lambda:p.message_parser('Lark',copy.deepcopy(msg),None,None))
for value in [{'b':2,'a':1},[1,'中文',{'b':False,'a':None}],True,1,1.5,0.0]:
 record('callback',{'value':{'callback_data':value},'name':'fallback'},lambda:Client.extract_card_callback_data({'callback_data':value},'fallback'))
 for text_value in ['Go',['one','two'],1,None,False]:
  args={'title':'T','text':'Body','link':None,'buttons':[[{'text':text_value,'callback_data':value,'url':{'b':2,'a':1}}]]}
  record('card',args,lambda:Client.build_card_v2(**args))
# Exact installed MIME dependency and original remote-image algorithm. The
# latter writes only its own TemporaryDirectory and uses no network.
import mimetypes,urllib.parse
# These are the source module's real imported dependencies; AST compilation
# intentionally omits top-level app imports, not standard-library semantics.
ns['tempfile']=tempfile;ns['urlparse']=urllib.parse.urlparse
mimetypes.init()
mime_names=[]
for ext in mimetypes.types_map:
 for name in ['owned'+ext,'owned'+ext.upper(),'owned'+ext+'.gz']:
  mime_names.append(name)
mime_names += ['owned.webp','.png','..png','...jpg','a/.png','a.jpg?x=1','a.jpg#frag','owned.jpg.GZ','owned.jpg.svgz','https://image.invalid/a.PNG;x?y#z','file:///owned.jpg?x','data:image/webp;base64,eA==','data:base64,eA==','data:x=y/z,eA==','data:image/png','a:owned.png','aa:owned.png','HTTPS://image.invalid/a%2Epng','https://[::1]/a.jpg','https://[bad]/a.jpg','https://image.invalid\uff0fa/a.jpg','\x00 \tHTTPS://image.invalid/owned.PNG','https://image.invalid/a.png;params','weird://image.invalid/a.png;params']
for name in mime_names:
 record('mime_guess',{'name':name},lambda:mimetypes.guess_type(name))
class ImageRequests:
 def __init__(self,header):self.header=header;self.calls=[]
 def get(self,url,**kwargs):
  self.calls.append({'url':url,**kwargs});r=Resp();r.content=b'owned-image';r.headers={'Content-Type':self.header};return r
with tempfile.TemporaryDirectory(prefix='owned-lark-image-') as image_dir:
 old_tempdir=tempfile.tempdir;tempfile.tempdir=image_dir
 try:
  for url in ['https://image.invalid/owned.PNG?x#y','https://image.invalid/owned.webp','https://image.invalid/.png','https://image.invalid/owned.ico','https://image.invalid/owned.TIFF;x','https://image.invalid/a%2Epng','https://[bad]/a.png']:
   for header in ['', 'image/png','image/gif','image/webp','image/jpeg','IMAGE/PNG','image/png; gif=true']:
    image_requests=ImageRequests(header);ns['requests']=image_requests;client=Client('SYNTHETIC_APP','SYNTHETIC_SECRET');uploaded=[]
    def upload(image_path,image_type='message'):
     mime,_=mimetypes.guess_type(image_path)
     if not mime or not mime.startswith('image/'):mime='image/png'
     uploaded.append({'suffix':Path(image_path).suffix,'mime':mime,'base64':base64.b64encode(Path(image_path).read_bytes()).decode()});return 'img_owned'
    client.upload_image=upload
    record('remote_image',{'url':url,'header':header},lambda:{'key':client._upload_remote_image(url),'uploaded':uploaded,'calls':image_requests.calls})
 finally:tempfile.tempdir=old_tempdir
ns['requests']=multi
# Original administrator resolution/cache/one-retry worker, not a recording
# MetaInfo or algorithm stub. The contact method is a controlled dependency.
class DirectoryClient:
 def __init__(self,ids,failure):self.ids=ids;self.failure=failure;self.calls=[]
 def batch_get_id(self,**kwargs):
  self.calls.append(copy.deepcopy(kwargs))
  if self.failure=='all' or self.failure=='first' and len(self.calls)==1:raise RuntimeError('synthetic directory failure')
  return {key:self.ids[key] for values in kwargs.values() for key in values if key in self.ids}
for cfg in [
 {'enabled':False,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_ADMINS':'a@example.invalid'},
 {'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET'},
 {'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_ADMINS':'a@example.invalid,123,ou_direct,a@example.invalid','FEISHU_OPEN_ID':'default@example.invalid'},
 {'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_ADMINS':'a@example.invalid,missing@example.invalid','FEISHU_OPEN_ID':'unknown@example.invalid'},
]:
 for failure in ['none','first','all']:
  for method in ['resolve','worker','admin_cache']:
   p=Plugin();p._start_admin_sync=lambda:None;p.init_plugin(copy.deepcopy(cfg));directory=DirectoryClient({'a@example.invalid':' ou_a ','123':'ou_b','default@example.invalid':'ou_default'},failure)
   if p._client:p._client=directory
   saved=[];sleeps=[];configs=[{'name':'Other','config':{'n':9007199254740993}},{'name':'Lark','config':{'keep':True}}]
   class ConfigStore:
    def get(self,key):return copy.deepcopy(configs)
    def set(self,key,value):saved.append(copy.deepcopy(value))
   p.systemconfig=ConfigStore();time.sleep=lambda seconds:sleeps.append(seconds)
   def run():
    if method=='resolve':result=p._resolve_admin_identifiers()
    elif method=='worker':result=p._admin_sync_worker()
    else:result=[p._is_admin(id) for id in [' ou_a ','ou_b','ou_unknown','ou_unknown']]
    return {'result':result,'calls':directory.calls,'saved':saved,'sleeps':sleeps,'user_id':p._user_id,'resolved':p._admin_users_resolved}
   record('admin_work',{'config':cfg,'failure':failure,'method':method,'identities':directory.ids},run)
for ids in [[' ou_a ','ou_a',' ou_a '],['','ou_a',''],['',''],['ou_β','ou_中','ou_a']]:
 saved=[]
 class ConfigStore:
  def get(self,key):return []
  def set(self,key,value):saved.append(copy.deepcopy(value))
 p=Plugin();p.systemconfig=ConfigStore()
 record('sync_config',{'configs':[],'ids':ids,'user':''},lambda:(p._sync_admin_to_notification_config(ids,''),saved)[1])
for failure in ['99991672','contact:user.id:readonly','other private synthetic']:
 p=Plugin();p._chat_id='';p._user_id='owned@example.invalid'
 class PermissionClient:
  def batch_get_id(self,**kwargs):raise RuntimeError(failure)
 p._client=PermissionClient()
 record('resolve_permission',{'failure':failure},lambda:p._resolve_targets())
# AES padding/base64 and signed/encrypted callback branches execute the actual
# installed primitive and full source endpoint, with only owned synthetic data.
key='synthetic-key';iv=bytes(range(16));aes_key=hashlib.sha256(key.encode()).digest()
encodings=[]
for pad in [0,1,2,15,16,17,32,255]:
 clear=b'A'*15+bytes([pad]);encodings.append(base64.b64encode(iv+AES.new(aes_key,AES.MODE_CBC,iv).encrypt(clear)).decode())
encodings += [base64.b64encode(iv).decode(),base64.b64encode(iv+b'A').decode(),base64.b64encode(iv[:-1]).decode(),base64.b64encode(iv+AES.new(aes_key,AES.MODE_CBC,iv).encrypt(b'\xff'*15+b'\1')).decode(),'','a','====','中文']
valid=encodings[1]
encodings += [valid+'====trailing',valid[:3]+'!\n'+valid[3:],valid.rstrip('='),valid[:3]+'='+valid[3:]]
for encoded in encodings:
 record('crypto_decrypt',{'key':key,'encoded':encoded},lambda:Crypto(key).decrypt(encoded))
for raw in [b'{}','中文'.encode(),b'\xff']:
 for timestamp,nonce in [('123','nonce'),('','nonce'),('123','')]:
  signature=hashlib.sha256((timestamp+nonce+key).encode()+raw).hexdigest()
  for sig in [signature,signature.upper(),'wrong']:
   record('crypto_signature',{'key':key,'raw':base64.b64encode(raw).decode(),'timestamp':timestamp,'nonce':nonce,'signature':sig},lambda:Crypto(key).verify_signature(sig,raw,timestamp,nonce))
def encrypted_payload(plain):
 b=json.dumps(plain,ensure_ascii=False,separators=(',',':')).encode();pad=16-len(b)%16;b+=bytes([pad])*pad
 return base64.b64encode(iv+AES.new(aes_key,AES.MODE_CBC,iv).encrypt(b)).decode()
for encoding in ['plain','wrapped','raw_cipher']:
 for token in ['','synthetic-token']:
  for signature_mode in ['valid','wrong','missing']:
   body={'schema':'2.0','header':{'event_type':'im.message.receive_v1','token':'synthetic-token'},'event':{'sender':{'sender_id':{'open_id':'ou_owned'}},'message':{'message_type':'text','message_id':'om_owned','content':'{"text":"hello"}'}}}
   if encoding=='plain':raw=json.dumps(body,ensure_ascii=False).encode()
   elif encoding=='wrapped':raw=json.dumps({'encrypt':encrypted_payload(body)}).encode()
   else:raw=encrypted_payload(body).encode()
   timestamp,nonce='123','nonce';signature=hashlib.sha256((timestamp+nonce+key).encode()+raw).hexdigest()
   headers={'X-Lark-Request-Timestamp':timestamp,'X-Lark-Request-Nonce':nonce}
   if signature_mode!='missing':headers['X-Lark-Signature']=signature if signature_mode=='valid' else 'wrong'
   p=Plugin();p._encrypt_key=key;p._verification_token=token;p._app_secret='synthetic-secret';p._crypto=Crypto(key);p._client=None;forwards=[];p._forward_to_message_chain=lambda v:forwards.append(v)
   class Request:
    method='POST';url=types.SimpleNamespace(path='/webhook');query_params={}
    async def body(self):return raw
   request=Request();request.headers=Headers(headers)
   def run():
    response=asyncio.run(p._webhook_endpoint(request));return {'status':response.status_code,'reply':json.loads(response.body),'forwards':forwards}
   record('webhook',{'raw':raw.decode(),'encrypt_key':key,'verification_token':token,'headers':headers,'query_token':''},run)
for schema in [None,'2.0',True,1,[],{}]:
 p=Plugin();p._encrypt_key='';p._verification_token='';p._crypto=None;p._client=None;forwards=[];p._forward_to_message_chain=lambda v:forwards.append(v);raw=json.dumps({'schema':schema}).encode()
 request=Request();request.headers=Headers({})
 record('webhook',{'raw':raw.decode(),'encrypt_key':'','verification_token':'','headers':{},'query_token':''},run)
# The real legacy builders/reply shape, including explicit empty button type.
for button_text in ['Go',['one','two'],1,None]:
 args={'title':'Title','content':'Body','buttons':[{'text':button_text,'value':{'b':2,'a':1},'action_id':'fallback'}],'color':'blue','img_key':None,'link':None}
 record('legacy_card',args,lambda:Client.build_card(**args))
for kind in ['default','','primary']:
 record('legacy_button',{'kind':kind},lambda:Client.build_button('Go','action','value',kind))
for content in ['plain','{"b":2,"a":1}','[1,"x"]','null',1,True,None,[1,'x']]:
 for msg_type in ['text','interactive']:
  multi.reset();ns['requests']=multi;client=Client('SYNTHETIC_APP','SYNTHETIC_SECRET')
  record('legacy_reply',{'content':content,'msg_type':msg_type},lambda:{'result':client.reply_message('om_owned',content,msg_type),'calls':multi.calls})
# All thirteen actual MessageChain entries, with real original Client REST
# algorithms and controlled requests. Media/Context inputs below are explicitly
# consumed-property views, not full MediaInfo identity or network recognition.
module_inputs=[
 ('post_message',{'notification':{'title':'Title','text':'Body'}}),
 ('send_direct_message',{'notification':{'title':'Title','text':'Body','original_message_id':'om_owned'}}),
 ('post_medias_message',{'notification':{'title':'Choose','text':'discarded','original_message_id':'om_owned'},'medias':[{'title_year':'Film (2030)','poster':'feishu://image/img_owned'},{'title':'No poster'}]*6}),
 ('post_torrents_message',{'notification':{'title':'Choose','text':'discarded'},'torrents':[{'title':'Release','site_name':'Site','media':{'poster_path':'feishu://image/img_owned'}},{'site_name':'Site'}]*6}),
 ('edit_message',{'channel':'飞书','source':'Lark','message_id':'om_owned','chat_id':'oc_owned','title':'T','text':'Edited','metadata':{'feishu_streaming':{'card_id':'card_owned','element_id':'mp_stream_body','sequence':0}}}),
 ('finalize_message',{'channel':'飞书','metadata':{'feishu_streaming':{'card_id':'card_owned','sequence':1}}}),
 ('mark_message_processing_started',{'channel':'飞书','source':'Lark','userid':'ou_owned','message_id':'om_owned','chat_id':'oc_owned','text':'work'}),
 ('mark_message_processing_finished',{'channel':'飞书','source':'Lark','message_id':'om_owned','status':{'message_id':'om_owned','metadata':{'reaction_id':'r_owned'}}}),
 ('download_feishu_image_to_data_url',{'image_ref':'feishu://image/om_owned/img_owned','source':'Lark'}),
 ('download_feishu_file_bytes',{'file_ref':'feishu://file/om_owned/file_owned/file.opus','source':'Lark'}),
 ('add_feishu_message_reaction',{'message_id':'om_owned','emoji_type':'GLANCE','source':'Lark'}),
 ('delete_feishu_message_reaction',{'message_id':'om_owned','reaction_id':'r_owned','source':'Lark'}),
 ('message_parser',{'source':'Lark','body':{'sender':{'open_id':'ou_owned'},'text':'hello','message_id':'om_owned'},'form':None,'args':None}),
]
for enabled in [False,True]:
 for method,arguments in module_inputs:
  cfg={'enabled':enabled,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_OPEN_ID':'ou_owned'};p=Plugin();p._start_admin_sync=lambda:None;p.init_plugin(cfg);p._resolve_username=lambda open,user,fallback:fallback;multi.reset();ns['requests']=multi;args=copy.deepcopy(arguments)
  if 'notification' in args:args['message']=ns['Notification'](**args.pop('notification'))
  if 'channel' in args:args['channel']=ns['MessageChannel'].Feishu
  if method=='finalize_message':args={'response':ns['MessageResponse'](channel=args['channel'],metadata=args['metadata'])}
  if 'medias' in args:args['medias']=[types.SimpleNamespace(**m) for m in args['medias']]
  if 'torrents' in args:args['torrents']=[types.SimpleNamespace(torrent_info=types.SimpleNamespace(**{k:v for k,v in m.items() if k!='media'}),media_info=types.SimpleNamespace(**m['media']) if m.get('media') else None) for m in args['torrents']]
  def run():
   result=getattr(p,method)(**args)
   if hasattr(result,'model_dump'):result=result.model_dump(mode='json')
   if isinstance(result,bytes):result={'base64':base64.b64encode(result).decode()}
   return {'result':result,'calls':multi.calls}
  record('module_calls',{'config':cfg,'method':method,'args':arguments},run)
# Successful API payload still passes the real MessageResponse Union model.
class ResponseIDRequests(MultiRequests):
 def __getattr__(self,method):
  original=super().__getattr__(method)
  def call(url,**kwargs):
   r=original(url,**kwargs)
   if '/tenant_access_token/' not in url:r.data['data'].update({'message_id':self.ids[0],'chat_id':self.ids[1]})
   return r
  return call
for ids in [[None,None],[False,True],[1,1.0],[1.5,'oc_owned'],['om_owned','oc_owned'],[[],{}]]:
 p=Plugin();p._start_admin_sync=lambda:None;p.init_plugin({'enabled':True,'app_id':'SYNTHETIC_APP','app_secret':'SYNTHETIC_SECRET','FEISHU_OPEN_ID':'ou_owned'});req=ResponseIDRequests();req.reset();req.ids=ids;ns['requests']=req
 def run():
  result=p.send_direct_message(ns['Notification'](title='T',text='Body'))
  return result.model_dump(mode='json') if result else None
 record('module_response_ids',{'ids':ids},run)
for raw in ['{"code":-0}','{"code":0e0}','{"code":0.00}','{"code":1e-9999}']:
 r=Resp();r.text=raw;r.status_code=200;record('cardkit',{'status':200,'body':raw},lambda:{'parsed':Client._safe_parse_cardkit(r),'ok':Client._cardkit_ok(r,Client._safe_parse_cardkit(r))})
