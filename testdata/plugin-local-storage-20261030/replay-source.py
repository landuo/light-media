"""Replay unchanged fixed-source components with explicit readonly dependency inputs."""
import argparse,hashlib,json,os,shutil,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--source',required=True);p.add_argument('--base',required=True);p.add_argument('--python',required=True);p.add_argument('--out',required=True);a=p.parse_args()
s=Path(a.source).resolve();b=Path(a.base).resolve();o=Path(a.out).resolve()
sha=subprocess.check_output(['git','-C',str(s),'rev-parse','HEAD'],text=True).strip()
if sha!='8a83efeacd3e26b82be495e17d50146e31c96a60':raise SystemExit('fixed source revision mismatch')
o.mkdir(parents=True,exist_ok=False)
fixture=Path(__file__).with_name('source_ordinary.py');driver=o/'source_ordinary.py';shutil.copyfile(fixture,driver)
env={'PATH':'/usr/bin:/bin','HOME':str(o),'PYTHONDONTWRITEBYTECODE':'1','OWNED_FIXED_SOURCE':str(s),'OWNED_SOURCE_INPUT_BASE':str(b)}
records=[]
for cold in (False,True):
 name='cold' if cold else 'warm';cmd=[a.python,str(driver),'--base',str(b),'--source',str(s),'--runtime',str(o/'owned-runtime'),'--out',str(o/(name+'.json'))]+(['--cold'] if cold else [])
 with (o/(name+'.log')).open('wb') as log:r=subprocess.run(cmd,env=env,stdout=log,stderr=subprocess.STDOUT)
 records.append({'phase':name,'exit':r.returncode,'command':cmd})
 if r.returncode:break
files={str(x.relative_to(o)):hashlib.sha256(x.read_bytes()).hexdigest() for x in o.iterdir() if x.is_file()}
(o/'replay.json').write_text(json.dumps({'source':sha,'executions':records,'files':files},indent=2)+'\n')
raise SystemExit(any(x['exit'] for x in records))
