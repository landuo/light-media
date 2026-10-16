import shlex
import os
import pathlib,subprocess,gzip,hashlib,json
root=pathlib.Path(__file__).parent
script=(root/'generate_runtime.py').read_text()
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',os.environ["JPEG_REFERENCE_SSH_TARGET"],shlex.join(['docker','exec','-i',os.environ.get('JPEG_REFERENCE_CONTAINER','moviepilot'),'python3','-'])],input=script,text=True,capture_output=True,check=True)
lines=r.stdout.encode();data=gzip.compress(lines,compresslevel=9,mtime=0);out=root.parent/'testdata/jpeg-pillow-12.2.0.jsonl.gz';out.write_bytes(data)
cases=[json.loads(s) for s in r.stdout.splitlines()];meta=cases.pop(0);(root/'installed-runtime.json').write_text(json.dumps(meta,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'errors':sum(v['error'] is not None for v in cases),'gzip_bytes':len(data),'sha256':hashlib.sha256(data).hexdigest(),'runtime':meta},indent=2))
