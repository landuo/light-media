"""Additional fixed frontend Unicode/physical-name boundaries; original nine trees stay intact."""
import runpy,tempfile,json
from pathlib import Path
ns=runpy.run_path(str(Path(__file__).with_name('generate_oracle.py')))
cases=[]
for original,target in [('Owned','Owned备份'),('Owned','Ownedi\u0307copy'),('ΟΣ','ΟΣcopy'),('Owned','Owned'+'a'*250)]+[('Owned','Owned'+suffix) for suffix in ['💫','!','a?b','a#b','a%b','a:b',"a'b",'a"b','a+b','a;b']]:
 files={'owned-a.css':b'.Owned .owned {}','remoteEntry.js':b'window.MoviePilotAPI.get("plugin/Owned/state"); css__Owned__;'}
 if original=='ΟΣ':files={'ος-a.css':'.ΟΣ .ος {}'.encode(),'remoteEntry.js':'ΟΣ ος'.encode()}
 with tempfile.TemporaryDirectory() as tmp:
  dist=Path(tmp)
  for relative,raw in files.items():(dist/relative).write_bytes(raw)
  status=ns['ns']['Fixed']()._modify_federation_files(dist,original,target)
  result={str(f.relative_to(dist)):f.read_bytes().hex() for f in sorted(dist.rglob('*')) if f.is_file()}
  cases.append({'name':original+'-'+target,'source_id':original,'clone_id':target,'input_hex':{k:v.hex() for k,v in files.items()},'status':list(status),'expected_hex':result})
output=ns['output']|{'cases':cases,'kind':'additional actual fixed frontend methods with real Unicode/255-byte name filesystem failures'}
Path(__file__).with_name('identifier-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');print(len(cases),'additional identifier tree cases')
