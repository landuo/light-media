#!/usr/bin/env python3
"""Development-only extraction and oracle; no Python is used by the Go server."""
import hashlib, json, pathlib, re, sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
SOURCE = ROOT / 'work/plugin-host-resources-20261031/zhconv-source'
raw = (SOURCE / 'ZhConversion.php').read_bytes()
assert hashlib.sha256(raw).hexdigest() == '5cb0019b32bb39ec5c6e662029f90bd166f7a844efb3bc877f9be41fdd511bf2'
# Literal parse_mediawiki/sort_and_dedup from zhconv-rs 0.4.1 build.rs.
blocks = dict(re.findall(r'public const (\w+) = \[([^]]+)\]?;', raw.decode()))
pairs = re.findall(r"'(.+?)' *=> *'(.+?)' *,?\n", blocks['ZH_TO_HANS'])
pairs.sort(key=lambda pair: (-len(pair[0].encode()), pair[0].encode()))
seen = set()
pairs = [pair for pair in pairs if not (pair[0] in seen or seen.add(pair[0]))]
assert all('\n' not in a and '\t' not in a and '\n' not in b and '\t' not in b for a,b in pairs)
table = ''.join(a+'\t'+b+'\n' for a,b in pairs)
(ROOT/'internal/sourcezhconv/hans.tsv').write_text(table)
binding = {'source_commit':'00e1e97fcf49865b49166c302651e419289fb3d1', 'version':'0.4.1', 'mediawiki_commit':'ecf4342132cf089ac0c42436827e9038a738bb6f', 'mediawiki_sha256':hashlib.sha256(raw).hexdigest(), 'pairs':len(pairs), 'max_pattern_bytes':max(len(a.encode()) for a,b in pairs), 'table_sha256':hashlib.sha256(table.encode()).hexdigest(), 'source_files':{f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in SOURCE.iterdir() if f.is_file()}}
(pathlib.Path(__file__).parent/'source.json').write_text(json.dumps(binding, ensure_ascii=False, indent=2)+'\n')
print(json.dumps({k:v for k,v in binding.items() if k!='source_files'}))
