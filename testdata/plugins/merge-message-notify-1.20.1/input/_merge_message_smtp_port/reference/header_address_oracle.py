"""Actual bounded Python Header and strict address-parser stage references."""
import gzip,hashlib,json,os,pathlib,shlex,subprocess
root=pathlib.Path(__file__).parent.parent
remote=r'''
import base64,binascii,email.header,email.utils,hashlib,itertools,json,pathlib,signal,smtplib
signal.alarm(60)
headers=[]
texts=['','A','ASCII words with; separators, and\ttabs','中emoji 🐈 café','A'*120,'中'*30,'a\n b\n','\t a\r\n\tx','\x1c\x85 foo','one\nX-Test: bad','\n\n','a  b\t  c']
for subject in [False,True]:
 for value in texts:
  for maximum in [0,12,13,14,16,24,40,78,100]:
   c={'subject':subject,'name':'Subject' if subject else 'X-Test','value':value,'maximum':maximum}
   try:
    h=email.header.Header(value,'utf-8') if subject else email.header.Header(value,header_name='X-Test')
    c['result']=h.encode(maxlinelen=maximum)
   except Exception as e:c['error']=type(e).__name__
   headers.append(c)
values=set(['','sender@example.invalid','a','Name <a>','a@b','a..b@example.invalid','a@b@c','<a@b>','<','Name <','"quoted name" <user@example.invalid>','"x\\\"y" <a@b>','group:one@example.invalid;','group:one@example.invalid,two@example.invalid;','Name (comment) <a@b>','a(comment)@example.invalid','@relay:abc@example.invalid','<@relay1,@relay2:abc@example.invalid>','a@[127.0.0.1]','Name <a@[IPv6:::1]>','a\r\nBcc: other@example.invalid','用户@example.invalid','中文 <a@b>','a\x00@b'])
alphabet='a@.<>()[],;:\\" \t\r\n'
for n in [1,2,3]:values.update(''.join(x) for x in itertools.product(alphabet,repeat=n))
for v in ['a','a@b','Name <a@b>','"local part"@example.invalid']:
 for ws in [' ','\t','\r\n','\v','\f','\x1c','\x85','\xa0','\u2028']:
  values.add(ws+v+ws)
values.update(['Name (outer (inner) end) <a@b>','(one(two(three)))a@b','A (x) a (y) @ b (z)','g:sub:a@b;;','\"a\\\"(b)\" <a@b>','\"local part\" . word @ example . invalid','a(c1(c2(c3)))@b'])
addresses=[]
for value in sorted(values):
 c={'value':value}
 try:c.update({'parsed':email.utils.parseaddr(value),'quoted':smtplib.quoteaddr(value)})
 except Exception as e:c['error']=type(e).__name__
 addresses.append(c)
metadata={}
for p in [pathlib.Path(email.header.__file__),pathlib.Path(email.utils.__file__),pathlib.Path(smtplib.__file__)]:metadata[p.name]=hashlib.sha256(p.read_bytes()).hexdigest()
base64_inputs=set(bytes(x) for n in range(5) for x in itertools.product(b'Aa0+/=#! ',repeat=n))
for byte in range(256):
 for prefix,suffix in [(b'',b''),(b'YQ',b'=='),(b'YWJ',b'='),(b'YWJj',b''),(b'YQ==',b'ignored')]:base64_inputs.add(prefix+bytes([byte])+suffix)
base64_cases=[]
for value in sorted(base64_inputs):
 c={'raw':base64.b64encode(value).decode()}
 try:c['result']=base64.b64encode(base64.decodebytes(value)).decode()
 except Exception as e:c['error']=type(e).__name__
 base64_cases.append(c)
metadata['binascii_extension_sha256']=hashlib.sha256(pathlib.Path(binascii.__file__).read_bytes()).hexdigest()
print(json.dumps({'headers':headers,'addresses':addresses,'base64':base64_cases,'metadata':metadata},ensure_ascii=False,separators=(',',':')))
'''
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot');command=shlex.join(['docker','exec','-i','-e','PLUGIN_ORACLE_TAG=owned-merge-header-address',container,'python3','-'])
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,command],input=remote,text=True,capture_output=True,timeout=75)
if r.returncode:raise RuntimeError('synthetic source stage failed: '+r.stderr[-2000:])
d=json.loads(r.stdout);(root/'reference/header-address-metadata.json').write_text(json.dumps(d.pop('metadata'),indent=2)+'\n')
for kind,cases in d.items():
 p=root/'testdata'/('python-3.12.13-'+kind+'.jsonl.gz')
 with gzip.GzipFile(p,'wb',mtime=0) as f:
  for c in cases:f.write((json.dumps(c,ensure_ascii=False,separators=(',',':'))+'\n').encode())
 print(json.dumps({'kind':kind,'cases':len(cases),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}))
