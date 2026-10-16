"""Actual installed plugin/smtplib against only owned localhost SMTP/TLS."""
import gzip,hashlib,json,os,pathlib,shlex,subprocess
root=pathlib.Path(__file__).parent.parent
source=(root/'reference/email-channel.py').read_bytes()
remote=r'''
import base64,datetime,gc,hashlib,ipaddress,json,os,pathlib,signal,socket,ssl,sys,tempfile,threading,types
from cryptography import x509
from cryptography.hazmat.primitives import hashes,serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID
signal.alarm(90)
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
class Logger:
 def __getattr__(self,n):return lambda *a,**kw:None
sys.modules['app.plugins.mergemessagenotify.channel.custom'].CustomChannel=CustomChannel
sys.modules['app.schemas.types'].NotificationType=type('NotificationType',(),{})
sys.modules['app.core.config'].settings=types.SimpleNamespace(TZ='Asia/Shanghai')
sys.modules['app.log'].logger=Logger()
ns={'__name__':'pinned_email'};exec(compile(source,'<pinned-email>','exec'),ns)
class Date:
 @classmethod
 def now(cls,tz):return datetime.datetime.fromtimestamp(1700000000.125,tz)
ns['datetime']=Date
socket.getfqdn=lambda *a:'client.synthetic.invalid'
key=ec.generate_private_key(ec.SECP256R1());name=x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,'localhost')])
cert=x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key()).serial_number(1234567).not_valid_before(datetime.datetime(2023,1,1)).not_valid_after(datetime.datetime(2035,1,1)).add_extension(x509.BasicConstraints(ca=True,path_length=None),True).add_extension(x509.SubjectAlternativeName([x509.DNSName('localhost'),x509.IPAddress(ipaddress.ip_address('127.0.0.1'))]),False).sign(key,hashes.SHA256())
tmp=tempfile.TemporaryDirectory(prefix='owned-merge-smtp-oracle-');directory=pathlib.Path(tmp.name);(directory/'cert.pem').write_bytes(cert.public_bytes(serialization.Encoding.PEM));(directory/'key.pem').write_bytes(key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()));os.chmod(directory/'key.pem',0o600)
server_tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);server_tls.load_cert_chain(directory/'cert.pem',directory/'key.pem')
class Server:
 def __init__(self,case):
  self.case=case;self.trace=[];self.failure=None;self.listener=socket.socket();self.listener.bind(('127.0.0.1',0));self.listener.listen(1);self.listener.settimeout(4);self.port=self.listener.getsockname()[1];self.thread=threading.Thread(target=self.run,daemon=True);self.thread.start()
 def emit(self,b):self.trace.append({'s':base64.b64encode(b).decode()});self.conn.sendall(b)
 def read(self):
  try:b=self.file.readline(1<<20)
  except ConnectionResetError:b=b''
  if b:self.trace.append({'c':base64.b64encode(b).decode()})
  else:self.trace.append({'eof':True})
  return b
 def run(self):
  try:
   self.conn,_=self.listener.accept();self.conn.settimeout(4)
   if self.case['mode']=='SSL':self.conn=server_tls.wrap_socket(self.conn,server_side=True)
   self.file=self.conn.makefile('rb');self.emit(self.case.get('greeting','220 synthetic SMTP\r\n').encode());auth=None;challenges=0;recipient=0
   while True:
    line=self.read()
    if not line:break
    raw=line.rstrip(b'\r\n');cmd=raw.split(b' ',1)[0].lower()
    if cmd==b'ehlo':
     if self.case.get('ehlo_fail'):self.emit(b'500 no EHLO\r\n');continue
     features=self.case.get('features',['SIZE 9999999','AUTH PLAIN','STARTTLS'])
     lines=['synthetic']+features
     self.emit(''.join('250'+('-' if i<len(lines)-1 else ' ')+v+'\r\n' for i,v in enumerate(lines)).encode('latin1'))
    elif cmd==b'helo':self.emit(('550 no HELO\r\n' if self.case.get('helo_fail') else '250 hello\r\n').encode())
    elif cmd==b'starttls':
     if self.case.get('starttls_close'):self.trace.append({'close':True});break
     if self.case.get('starttls_fail'):self.emit(b'454 not now\r\n');continue
     self.emit(b'220 begin TLS\r\n');self.file.close();self.trace.append({'tls':True});self.conn=server_tls.wrap_socket(self.conn,server_side=True);self.file=self.conn.makefile('rb')
    elif cmd==b'auth':
     auth=raw.split(b' ',2)[1].decode();challenges=0
     if self.case.get('auth_503'):self.emit(b'503 already authenticated\r\n');auth=None;continue
     if auth=='CRAM-MD5' or self.case.get('challenge_loop') or auth=='LOGIN':
      self.emit(('334 '+self.case.get('challenge','c3ludGhldGljLWNoYWxsZW5nZQ==')+'\r\n').encode());continue
     if self.case.get('auth_fail'):self.emit(b'535 rejected\r\n')
     else:self.emit(b'235 accepted\r\n')
     auth=None
    elif auth is not None:
     challenges+=1
     if self.case.get('challenge_loop'):self.emit(b'334 c3ludGhldGlj\r\n');continue
     if self.case.get('auth_fail') or self.case.get('cram_fail') and auth=='CRAM-MD5':self.emit(b'535 rejected\r\n')
     else:self.emit(b'235 accepted\r\n')
     auth=None
    elif cmd==b'mail':self.emit((str(self.case.get('mail_code',250))+' sender\r\n').encode())
    elif cmd==b'rcpt':
     codes=self.case.get('rcpt_codes',[250]);code=codes[min(recipient,len(codes)-1)];recipient+=1;self.emit((str(code)+' recipient\r\n').encode())
    elif cmd==b'data':
     code=self.case.get('data_code',354);self.emit((str(code)+' data\r\n').encode())
     if code==354:
      parts=[]
      while True:
       b=self.file.readline(1<<20)
       if not b:raise RuntimeError('DATA disconnected')
       parts.append(b)
       if b==b'.\r\n':break
      self.trace.append({'c':base64.b64encode(b''.join(parts)).decode()});self.emit((str(self.case.get('end_code',250))+' result\r\n').encode())
    elif cmd==b'rset':
     if self.case.get('rset_close'):self.trace.append({'close':True});break
     self.emit(b'250 reset\r\n')
    elif cmd==b'quit':
     if self.case.get('quit_close'):self.trace.append({'close':True});break
     self.emit((str(self.case.get('quit_code',221))+' bye\r\n').encode());break
    else:raise RuntimeError('unexpected synthetic command')
  except Exception as e:self.failure=type(e).__name__
  finally:
   try:self.file.close()
   except Exception:pass
   try:self.conn.close()
   except Exception:pass
   self.listener.close()
cases=[]
def add(id,**kw):cases.append(dict(id=id,mode='NONE',**kw))
for mode in ['NONE','SSL','TLS']:cases.append({'id':'success-'+mode,'mode':mode})
add('cram-only',features=['AUTH CRAM-MD5','SIZE 9999999'])
add('cram-fallback-plain',features=['AUTH PLAIN LOGIN CRAM-MD5','SIZE 9999999'],cram_fail=True)
add('login-only',features=['AUTH LOGIN'])
add('already-authenticated',auth_503=True)
add('auth-all-fail',features=['AUTH LOGIN PLAIN CRAM-MD5'],auth_fail=True)
add('auth-unsupported',features=['SIZE 9999999'])
add('auth-unknown-method',features=['AUTH SCRAM-SHA-256'])
add('auth-lowercase-unmatched',features=['AUTH plain'])
add('oldstyle-auth',features=['aUtH=PLAIN','SIZE 9999999'])
add('multiple-auth-lines',features=['AUTH CRAM-MD5','AUTH PLAIN LOGIN'],cram_fail=True)
add('ehlo-fallback-helo',ehlo_fail=True)
add('helo-fails',ehlo_fail=True,helo_fail=True)
add('sender-rejected',mail_code=550)
add('sender-421',mail_code=421)
add('all-recipients-rejected',rcpt_codes=[550])
add('partial-recipients-rejected',recipients=['one@example.invalid','two@example.invalid'],rcpt_codes=[550,251])
add('recipient-421',rcpt_codes=[421])
add('data-initial-rejected',data_code=550)
add('data-final-rejected',end_code=550)
add('data-final-421',end_code=421)
add('quit-500-is-not-error',quit_code=500)
add('quit-disconnect-overrides-success',quit_close=True)
add('rset-disconnect-overrides-failure',mail_code=550,rset_close=True)
add('greeting-not-220',greeting='554 not ready\r\n')
add('greeting-malformed',greeting='bad\r\n')
cases.append({'id':'tls-not-advertised','mode':'TLS','features':['AUTH PLAIN']})
cases.append({'id':'tls-rejected','mode':'TLS','starttls_fail':True})
cases.append({'id':'tls-disconnect','mode':'TLS','starttls_close':True})
add('challenge-limit-plain',challenge_loop=True)
add('challenge-limit-cram',features=['AUTH CRAM-MD5'],challenge_loop=True)
add('challenge-base64-malformed',features=['AUTH CRAM-MD5'],challenge='a')
add('password-nonascii',password='合成密码')
add('cram-password-nonascii',features=['AUTH CRAM-MD5'],password='合成密码')
add('recipient-nonascii',recipients=['用户@example.invalid'])
add('greeting-exact-8192',greeting='220 '+'x'*8186+'\r\n')
add('greeting-over-8192',greeting='220 '+'x'*8187+'\r\n')
add('ehlo-response-over-8192',features=['AUTH PLAIN','X'+'x'*9000])
add('mime-invalid-from-header',from_name='line1\nline2')
add('auth-latin1-nbsp-parameter',features=['AUTH\xa0PLAIN'])
add('cram-ignored-base64-characters',features=['AUTH CRAM-MD5'],challenge='c3lu#dGhldGljLWNoYWxsZW5nZQ==')
add('cram-excess-base64-padding',features=['AUTH CRAM-MD5'],challenge='c3ludGhldGljLWNoYWxsZW5nZQ====')
out=[]
for case in cases:
 server=Server(case);recipients=case.get('recipients',['one@example.invalid']);cfg={'smtp_host':'127.0.0.1','smtp_port':server.port,'smtp_encrypt_type':case['mode'],'username':'sender@example.invalid','password':case.get('password','synthetic-password'),'to_addrs':','.join(recipients),'from_name':case.get('from_name','MoviePilot')};obj=ns['EmailChannel'](cfg)
 try:result={'return':obj.send_message(title='合成标题',text='正文\n.dot\nFrom synthetic',ext_info={'image':'https://image.invalid/p.png'})}
 except Exception as e:result={'error':type(e).__name__}
 gc.collect()
 server.thread.join(5)
 if server.thread.is_alive():raise RuntimeError('owned server did not stop')
 if server.failure:raise RuntimeError('owned server failed '+case['id']+' '+server.failure)
 # Capture the actual source set-derived recipient order from the raw To field.
 actual_to=recipients
 for frame in server.trace:
  if 'c' in frame:
   data=base64.b64decode(frame['c'])
   if data.startswith(b'Content-Type:'):
    for line in data.split(b'\r\n'):
     if line.startswith(b'To: '):actual_to=line[4:].decode().split(',')
 request={'Host':'127.0.0.1','Encrypt':case['mode'],'Port':0,'Username':cfg['username'],'Password':cfg['password'],'From':cfg['username'],'To':actual_to,'Title':'合成标题','Text':'正文\n.dot\nFrom synthetic','HTML':'<div>正文\n.dot\nFrom synthetic</div><br><img src="https://image.invalid/p.png"></img>','FromName':cfg['from_name'],'Date':'Wed, 15 Nov 2023 06:13:20 +0800'}
 out.append({'id':case['id'],'scenario':case,'request':request,'outcome':result,'wire':server.trace})
context=ssl._create_stdlib_context()
metadata={'python':sys.version,'ssl_version':ssl.OPENSSL_VERSION,'original_context':{'verify_mode':int(context.verify_mode),'check_hostname':context.check_hostname,'minimum_version':int(context.minimum_version)},'source_sha256':hashlib.sha256(source).hexdigest()}
tmp.cleanup();print(json.dumps({'metadata':metadata,'cases':out},ensure_ascii=False,separators=(',',':')))
'''.replace('SOURCE_SHA',repr(hashlib.sha256(source).hexdigest()))
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
command=shlex.join(['docker','exec','-i','-e','PLUGIN_ORACLE_TAG=owned-merge-smtp-wire',container,'python3','-'])
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,command],input=remote,text=True,capture_output=True,timeout=105)
if r.returncode:raise RuntimeError('synthetic source execution failed: '+r.stderr[-2000:])
d=json.loads(r.stdout);(root/'reference/wire-metadata.json').write_text(json.dumps(d['metadata'],indent=2)+'\n');p=root/'testdata/smtp-python-3.12.13-wire.jsonl.gz'
with gzip.GzipFile(p,'wb',mtime=0) as f:
 for c in d['cases']:f.write((json.dumps(c,ensure_ascii=False,separators=(',',':'))+'\n').encode())
print(json.dumps({'cases':len(d['cases']),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'original_context':d['metadata']['original_context']}))
