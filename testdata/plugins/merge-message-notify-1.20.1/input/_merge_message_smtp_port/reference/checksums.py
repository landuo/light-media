"""Local evidence inventory only; no network, application or config access."""
import hashlib,json,pathlib
root=pathlib.Path(__file__).parent.parent
files={}
for pattern in ['*.go','go.mod','NOTICE','LICENSE-GPL-3.0.txt','reference/**/*','testdata/*.gz']:
 for p in root.glob(pattern):
  if p.is_file():files[str(p.relative_to(root))]={'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
dependency=root.parent/'_merge_message_notify_port'
external={}
for p in dependency.glob('*.go'):
 external[str(p.relative_to(root.parent))]={'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
for p in [dependency/'forms.json',dependency/'source-checksums.json']:
 external[str(p.relative_to(root.parent))]={'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
out={'plugin':'MergeMessageNotify','version':'1.20.1','python':'3.12.13','counts':{'mime':364,'header':216,'address':6237,'base64':8651,'wire':44},'files':dict(sorted(files.items())),'frozen_external':dict(sorted(external.items()))}
p=root/'source-checksums.json';p.write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({'files':len(files),'frozen_external':len(external),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}))
