"""Replay exact retained source driver bytes into a fresh BASE/evidence child.
No product Python dependency: this is a fixed-source test-only component runner.
"""
import argparse,hashlib,json,os,pathlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--python',required=True);a=p.parse_args()
out=pathlib.Path(a.out).absolute();assert out.parent.name=='evidence','use a fresh BASE/evidence/<name> directory'
base=out.parents[1];assert (base/'audits/s05-source/deps').is_dir();out.mkdir()
fixtures=pathlib.Path(__file__).resolve().parent
for name in ('source_named_reset_v5.py','source_reset_cold_read_v2.py'):(out/name).write_bytes((fixtures/name).read_bytes())
commands=[]
for case in ('missing','string','null','empty','dynamic-delete','getter-error','success-with-getter-error'):
 command=[a.python,str(out/'source_named_reset_v5.py'),case]
 with (out/(case+'.log')).open('wb') as log:run=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,env={**os.environ,'PYTHONDONTWRITEBYTECODE':'1'})
 (out/(case+'.exit')).write_text(str(run.returncode)+'\n');commands.append({'command':command,'exit':run.returncode})
 assert run.returncode==0,case
(out/'replay.json').write_text(json.dumps({'commands':commands,'runner_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()},indent=2)+'\n')
