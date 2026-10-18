import hashlib,importlib.util,json,os,pathlib,sys,tempfile,types
R=pathlib.Path(__file__).parent
O=pathlib.Path(os.environ.get('AUTOBACKUP_ORACLE_OUTPUT',str(R)));O.mkdir(parents=True,exist_ok=True)
# Import only frozen source, with immutable installed dependency byte assertions.
package=types.ModuleType('webdav3');package.__path__=[];sys.modules['webdav3']=package
for name,file in [('exceptions','webdav-exceptions.py'),('urn','webdav-urn.py'),('connection','webdav-connection.py')]:
 dep_spec=importlib.util.spec_from_file_location('webdav3.'+name,R/file);dep=importlib.util.module_from_spec(dep_spec);sys.modules['webdav3.'+name]=dep;dep_spec.loader.exec_module(dep);setattr(package,name,dep)
spec=importlib.util.spec_from_file_location('frozen_webdav_client',R/'webdav-client.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
xml='<d:multistatus xmlns:d="DAV:"><d:response><d:href>/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response><d:response><d:href>/bk_20260101000000.zip</d:href></d:response><d:response><d:href>/folder/</d:href><d:collection/></d:response><d:response><d:href>https://dav.invalid/root/a%20b.zip</d:href></d:response></d:multistatus>'
class Response:
 def __init__(self,status,body):self.status_code=status;self.content=body
class Session:
 auth=None
 def __init__(self,c):self.c=c;self.trace=[]
 def request(self,**kw):
  body=kw['data'].read() if hasattr(kw.get('data'),'read') else kw.get('data')
  self.trace.append(dict(method=kw['method'],url=kw['url'],auth=list(kw['auth']) if kw['auth'] is not None else None,headers=kw['headers'],body=None if body is None else body.decode(),timeout=kw['timeout'],verify=kw['verify']))
  return Response(self.c.get('status',{}).get(kw['method'],{'PROPFIND':207,'HEAD':200,'PUT':201,'DELETE':204}.get(kw['method'],200)),self.c.get('xml',xml).encode() if kw['method']=='PROPFIND' else b'owned response')
cases=[]
inputs=[{},dict(disable_check=True),dict(webdav_digest_auth=True),dict(xml='<bad'),dict(xml='<d:multistatus xmlns:d="DAV:"><d:response><d:href/></d:response></d:multistatus>'),dict(status={'HEAD':404}),dict(status={'HEAD':201}),dict(status={'PUT':507}),dict(status={'PUT':423}),dict(status={'DELETE':405}),dict(status={'PROPFIND':500})]
with tempfile.TemporaryDirectory(prefix='owned-autobackup-') as tmp:
 p=pathlib.Path(tmp)/'owned.zip';p.write_bytes(b'owned zip bytes')
 for c in inputs:
  for op in ('list','check','check_url','upload','delete'):
   options=dict(webdav_hostname='https://dav.invalid/root',webdav_login='owned',webdav_password='synthetic')
   options.update({k:v for k,v in c.items() if k in ('disable_check','webdav_digest_auth')})
   client=m.Client(options);session=Session(c);client.session=session;result=None;error=None;message=None;callback=[]
   try:
    if op=='list':result=client.list('/')
    elif op=='check':result=client.check('/owned.zip')
    elif op=='check_url':result=client.check('https://dav.invalid/root/owned.zip')
    elif op=='upload':
     client.upload_sync('owned.zip',str(p),callback=lambda:callback.append('called'))
    else:client.clean('/owned.zip')
   except Exception as e:error=type(e).__name__;message=str(e)
   cases.append(dict(input=c,options=options,operation=op,trace=session.trace,result=result,error=error,message=message,callback=callback))
(O/'webdav-oracle.json').write_text(json.dumps(dict(source_sha256=hashlib.sha256((R/'webdav-client.py').read_bytes()).hexdigest(),runtime=sys.version,cases=cases),ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(webdav_cases=len(cases))))
