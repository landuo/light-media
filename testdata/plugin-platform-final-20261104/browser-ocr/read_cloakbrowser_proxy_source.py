#!/usr/bin/env python3
import hashlib,json,pathlib,shlex,subprocess
out=pathlib.Path(__file__).resolve().parent
program="""import pathlib,json,hashlib
root=pathlib.Path('/opt/venv/lib/python3.12/site-packages/cloakbrowser')
rows=[]
for p in root.glob('*.py'):
 t=p.read_text();lines=t.splitlines();matches=[]
 for i,l in enumerate(lines):
  if any(x in l for x in ['def launch_context','def launch(', 'HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','proxy=', '\"proxy\"', '\"env\"','CLOAKBROWSER_BINARY_PATH']):
   matches.append({'line':i+1,'text':'\\n'.join(lines[max(0,i-4):i+12])})
 rows.append({'path':str(p.relative_to(root)),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'matches':matches})
print(json.dumps({'classification':'read-only paired wrapper source files from disposable no-network image; no old service/config executed','files':rows},indent=2))
"""
image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
cmd=shlex.join(['docker','run','--rm','--name','light-media-browser-proxy-source-read-r2','--network','none','--read-only','--no-healthcheck','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c',program])
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',cmd],capture_output=True,timeout=120)
(out/'cloakbrowser-proxy-source-stderr.log').write_bytes(r.stderr);assert r.returncode==0,r.stderr
j=json.loads(r.stdout);j['dependency_image']=image;j['driver_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
(out/'cloakbrowser-proxy-source.json').write_text(json.dumps(j,indent=2)+'\n')
print([(x['path'],len(x['matches']))for x in j['files']])
