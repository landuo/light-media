"""Pinned original EmailChannel + actual Python email library, synthetic only."""
import base64,gzip,hashlib,json,os,pathlib,shlex,subprocess
root=pathlib.Path(__file__).parent.parent
source=(root/'reference/email-channel.py').read_bytes()
remote=r'''
import base64,datetime,gzip,hashlib,json,pathlib,signal,sys,types
signal.alarm(45)
source=pathlib.Path('/app/app/plugins/mergemessagenotify/channel/custom/email.py').read_bytes()
if hashlib.sha256(source).hexdigest()!=SOURCE_SHA:raise RuntimeError('source changed')
def mod(name):
 m=types.ModuleType(name);m.__path__=[];sys.modules[name]=m
 if '.' in name:
  p,k=name.rsplit('.',1)
  if p not in sys.modules:mod(p)
  setattr(sys.modules[p],k,m)
 return m
for n in ['app','app.plugins','app.plugins.mergemessagenotify','app.plugins.mergemessagenotify.channel','app.plugins.mergemessagenotify.channel.custom','app.schemas','app.schemas.types','app.core','app.core.config','app.log']:mod(n)
class CustomChannel:
 comp_key='custom';comp_order=2
 def __init__(self,cfg):self.cfg=cfg
 def get_config_item(self,config_key,use_default=True):
  v=self.cfg.get(config_key)
  return self.config_default.get(config_key) if v is None and use_default else v
sys.modules['app.plugins.mergemessagenotify.channel.custom'].CustomChannel=CustomChannel
sys.modules['app.schemas.types'].NotificationType=type('NotificationType',(),{})
sys.modules['app.core.config'].settings=types.SimpleNamespace(TZ='Asia/Shanghai')
sys.modules['app.log'].logger=types.SimpleNamespace()
ns={'__name__':'pinned_email'};exec(compile(source,'<pinned-email-channel>','exec'),ns)
class Date:
 @classmethod
 def now(cls,tz):return datetime.datetime.fromtimestamp(1700000000.125,tz)
ns['datetime']=Date
cases=[]
titles=['','A','ASCII title','a_b?= !*-+/','合成标题','emoji 🐈 café','line1\nline2','line1\r\n\tline2','\n','\t tab\x1cfield\x85last','  keep spaces  ','A'*77,'A'*1000,'中'*400,'𞤀'*45]
texts=['正文','a','a'*56,'a'*57,'a'*58,'a'*114,'before\r\nafter\nend\rlast','\x00\t\x1c\x85\u2028','From test\n.dot\n..two\n','emoji 🐈 café']
names=['MoviePilot','','simple name','last, first','quote"slash\\','中文署名','Náme','line1\nline2']
for title in titles:
 for body in texts:
  for image in [None,'https://image.invalid/a?x=1&y=2']:
   cases.append({'title':title,'text':body,'image':image,'from_name':'MoviePilot','to':['one@example.invalid']})
for name in names:
 for title in ['主题','ASCII subject']:
  for recipients in [['one@example.invalid'],['one@example.invalid','two@example.invalid'],['a'*90+'@example.invalid'],['用户@example.invalid']]:
   cases.append({'title':title,'text':'正文','image':None,'from_name':name,'to':recipients})
# Large exact-limit paths use the same original Header stages with an explicit
# smaller maxline; million-character original quadratic execution is avoided.
out=[]
for i,c in enumerate(cases):
 cfg={'username':'sender@example.invalid','from_name':c['from_name']};obj=ns['EmailChannel'](cfg)
 r={'id':i,'input':c,'request':{'Host':'smtp.synthetic.invalid','Encrypt':'NONE','Port':2525,'Username':cfg['username'],'Password':'synthetic-password','From':cfg['username'],'To':c['to'],'Title':c['title'],'Text':c['text'],'HTML':f'<div>{c["text"]}</div><br><img src="{c["image"]}"></img>' if c['image'] else '', 'FromName':c['from_name'],'Date':'Wed, 15 Nov 2023 06:13:20 +0800'}}
 try:
  message=obj._EmailChannel__build_message(c['title'],c['text'],c['to'],{'image':c['image']});raw=message.as_string().encode('ascii')
  r.update({'raw':base64.b64encode(raw).decode(),'sha256':hashlib.sha256(raw).hexdigest()})
 except Exception as e:r['error']=type(e).__name__
 out.append(r)
print(json.dumps(out,ensure_ascii=False,separators=(',',':')))
'''.replace('SOURCE_SHA',repr(hashlib.sha256(source).hexdigest()))
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,shlex.join(['docker','exec','-i','-e','PLUGIN_ORACLE_TAG=owned-merge-mime-bytes',container,'python3','-'])],input=remote,text=True,capture_output=True,check=True,timeout=60)
cases=json.loads(r.stdout);p=root/'testdata/mime-python-3.12.13.jsonl.gz'
with gzip.GzipFile(p,'wb',mtime=0) as f:
 for c in cases:f.write((json.dumps(c,ensure_ascii=False,separators=(',',':'))+'\n').encode())
print(json.dumps({'cases':len(cases),'errors':sum('error'in c for c in cases),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}))
