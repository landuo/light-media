"""Copy only installed library source/license data, never application state."""
import base64,gzip,hashlib,json,os,pathlib,shlex,subprocess

remote=r'''
import base64,hashlib,importlib.metadata,json,pathlib,sys,sysconfig
out={'python':sys.version,'packages':{},'stdlib':[]}
for name,prefix in [('mako','mako/'),('apprise','apprise/')]:
 dist=importlib.metadata.distribution(name);files=[]
 for p in dist.files:
  s=str(p)
  if (s.startswith(prefix) and s.endswith('.py')) or ('.dist-info/licenses/' in s):
   raw=pathlib.Path(dist.locate_file(p)).read_bytes()
   files.append({'path':s,'sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'source':base64.b64encode(raw).decode()})
 out['packages'][name]={'version':dist.version,'files':files}
base=pathlib.Path(sysconfig.get_path('stdlib'))
for p in sorted((base/'email').rglob('*.py'))+[base/'smtplib.py']:
 raw=p.read_bytes();out['stdlib'].append({'path':str(p.relative_to(base)),'sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'source':base64.b64encode(raw).decode()})
print(json.dumps(out))
'''
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,shlex.join(['docker','exec','-i',container,'python3','-'])],input=remote,text=True,capture_output=True,check=True)
d=json.loads(r.stdout);root=pathlib.Path(__file__).parent/'dependencies'
for name,items in [(k,v['files']) for k,v in d['packages'].items()]+[('stdlib',d['stdlib'])]:
 for item in items:
  raw=base64.b64decode(item.pop('source'));p=root/name/item['path'];p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw)
(root/'manifest.json').write_text(json.dumps(d,indent=2)+'\n')
print(json.dumps({'python':d['python'],'packages':{k:{'version':v['version'],'files':len(v['files']),'bytes':sum(f['bytes'] for f in v['files'])} for k,v in d['packages'].items()},'stdlib_files':len(d['stdlib'])},indent=2))
