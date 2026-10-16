"""Offline evidence checksum/stream count validation; never imports the app.
Use --refresh only after an owned edit window to refresh candidate manifests.
"""
import ast,collections,gzip,hashlib,json,pathlib,sys
root=pathlib.Path(__file__).resolve().parent.parent
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
ref=root/'reference'
if sys.argv[1:]==['--refresh']:
 go={p.name:sha(p) for p in sorted(root.glob('*.go'))}
 (ref/'candidate-go-checksums.json').write_text(json.dumps(go,indent=2)+'\n')
 paths=[p for p in ref.rglob('*') if p.is_file() and p.name!='source-checksums.json' and '__pycache__' not in p.parts]
 paths += [root/p for p in ['testdata/source-oracle.jsonl.gz','form.json','page-templates.json','test-card.json','mime-registry.json','source-contract.json']]
 checks={str(p.relative_to(root)):sha(p) for p in sorted(paths)}
 (ref/'source-checksums.json').write_text(json.dumps(checks,indent=2,ensure_ascii=False)+'\n')
else:
 assert not sys.argv[1:], 'only --refresh or no argument'
for manifest,base in [('source-checksums.json',root),('candidate-go-checksums.json',root)]:
 for name,expected in json.loads((ref/manifest).read_text()).items():
  assert sha(base/name)==expected, f'checksum mismatch: {name}'
source=json.loads((ref/'installed-source-provenance.json').read_text())
for row in source['files']:
 assert sha(ref/'source'/row['path'])==row['installed_raw_sha256']==row['original_source_sha256_from_cached_manifest']
counts=collections.Counter()
with gzip.open(root/'testdata/source-oracle.jsonl.gz','rt') as f:
 for line in f:counts[json.loads(line)['kind']]+=1
assert sum(counts.values())==1376, counts
assert counts['module_calls']==26 and counts['form']==1
print(json.dumps({'go_files':len(json.loads((ref/'candidate-go-checksums.json').read_text())),'reference_files':len(json.loads((ref/'source-checksums.json').read_text())),'cases':sum(counts.values()),'kinds':dict(sorted(counts.items()))},ensure_ascii=False))
