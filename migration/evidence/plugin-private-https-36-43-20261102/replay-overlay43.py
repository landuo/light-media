#!/usr/bin/env python3
"""Restore only three reviewed test-build objects and write Go overlay metadata.
Does not run Go, start a process, contact a peer, or change checkout files.
"""
import argparse, hashlib, json, pathlib, tarfile
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--checkout',required=True);p.add_argument('--output',required=True)
a=p.parse_args(); here=pathlib.Path(__file__).resolve().parent
checkout=pathlib.Path(a.checkout).resolve(strict=True); out=pathlib.Path(a.output).resolve()
if out==checkout or checkout in out.parents:raise SystemExit('output must be outside the checkout')
if out.exists():raise SystemExit('output must be a fresh owned directory')
spec=json.loads((here/'replay-spec.json').read_text())
archive=here/'public-originals.tar.gz'
if hashlib.sha256(archive.read_bytes()).hexdigest()!=spec['archive_sha256']:raise SystemExit('archive SHA mismatch')
for item in spec['ordinary_files']+spec['clean_head_files']:
 target=pathlib.Path(item['target'])
 if target.is_absolute() or '..' in target.parts:raise SystemExit('invalid target')
 original=checkout/target
 if not original.is_file() or original.is_symlink():raise SystemExit('ordinary file missing or symlink: '+str(target))
 b=original.read_bytes()
 if len(b)!=item['bytes'] or hashlib.sha256(b).hexdigest()!=item['sha256']:raise SystemExit('ordinary candidate mismatch: '+str(target))
for target in spec['must_be_absent']:
 if (checkout/target).exists():raise SystemExit('unaccepted Meta source exists; use an independently clean checkout: '+target)
selected=[]
with tarfile.open(archive,'r:gz') as tf:
 for item in spec['overlay_only']:
  target=pathlib.Path(item['target'])
  if target.is_absolute() or '..' in target.parts:raise SystemExit('invalid overlay target')
  m=tf.getmember(item['archive_member'])
  if not m.isfile() or m.size!=item['bytes']:raise SystemExit('invalid archive member')
  b=tf.extractfile(m).read()
  if hashlib.sha256(b).hexdigest()!=item['sha256']:raise SystemExit('overlay object SHA mismatch')
  selected.append((target,b))
if len(selected)!=3:raise SystemExit('expected exactly three overlay objects')
out.mkdir(parents=False,exist_ok=False); replace={}
for target,b in selected:
 dest=out/target; dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(b)
 replace[str(checkout/target)]=str(dest)
(out/'overlay.json').write_text(json.dumps({'Replace':replace},indent=2)+'\n')
print(json.dumps({'status':'METADATA_ONLY_RESTORED','overlay':str(out/'overlay.json'),'objects':3,'Go_or_process_execution':False}))
