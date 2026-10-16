"""Restore an original oracle JSON from complete gzip NDJSON records, offline."""
import argparse,gzip,json,pathlib
a=argparse.ArgumentParser();a.add_argument('fixture',type=pathlib.Path);a.add_argument('output',type=pathlib.Path);args=a.parse_args()
with gzip.open(args.fixture,'rt') as f:
 records=(json.loads(line)for line in f);header=next(records);assert header['record']=='header';out=header['data'];out.pop('original_json_sha256',None)
 for rec in records:
  key={'case':'cases','method':'cases','bit':'video_bits','syllable':'syllables','name':'names','segment':'segments'}[rec['record']];out.setdefault(key,[]).append(rec['data'])
args.output.write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
