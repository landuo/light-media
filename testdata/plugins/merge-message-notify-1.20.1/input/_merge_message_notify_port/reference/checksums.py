"""Reproducible local evidence inventory; no network or application access."""
import hashlib,json,pathlib
root=pathlib.Path(__file__).parent.parent
files={}
for pattern in ['*.go','go.mod','forms.json','NOTICE','LICENSE-GPL-3.0.txt','reference/**/*.py','reference/**/*.json','reference/dependencies/**/*','testdata/*.gz']:
 for p in root.glob(pattern):
  if p.is_file():files[str(p.relative_to(root))]={'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'bytes':p.stat().st_size}
out={'plugin_id':'MergeMessageNotify','version':'1.20.1','counts':{'core':192,'channels':4720,'lifecycle':8,'test_once':102,'smtp_errors':18,'templates':19},'files':dict(sorted(files.items()))}
(root/'source-checksums.json').write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps({'files':len(files),'manifest_sha256':hashlib.sha256((root/'source-checksums.json').read_bytes()).hexdigest()}))
