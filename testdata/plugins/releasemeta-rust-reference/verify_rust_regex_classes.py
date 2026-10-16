"""Offline verification of the pinned Rust regex character-class translation.

Pass the unmodified regex-syntax 0.8.10 src/unicode_tables directory. This checks
its source digests and every recorded range against the emitted Go constants;
it never imports MoviePilot, runs services, or modifies source files.
"""
import argparse, hashlib, json, pathlib, re
p=argparse.ArgumentParser();p.add_argument('source_dir',type=pathlib.Path);args=p.parse_args()
base=pathlib.Path(__file__).resolve().parent
reference=json.loads((base/'routing-numbers.json').read_text())['regex_source']
for name,digest in reference['source_sha256'].items():
 assert hashlib.sha256((args.source_dir/name).read_bytes()).hexdigest()==digest,name

def codepoint(s):
 if s.startswith('\\u{'):return int(s[3:-1],16)
 if s=='\\t':return 9
 if s=='\\r':return 13
 return ord(s)
def source_ranges(name):
 return [[codepoint(a),codepoint(b)] for a,b in re.findall(r"\('([^']+)', '([^']+)'\)",(args.source_dir/name).read_text())]
def char_class(ranges):
 return '['+''.join('\\x{%x}'%lo+('-\\x{%x}'%hi if lo!=hi else '') for lo,hi in ranges)+']'
go=(base.parents[2]/'internal/releasemeta/rust_regex_classes.go').read_text()
for name,field,constant in [('perl_decimal.rs','decimal_ranges','rustRegexDigits'),('perl_space.rs','space_ranges','rustRegexSpace')]:
 ranges=source_ranges(name);assert ranges==reference[field],name
 assert re.search(r'const '+constant+r' = `([^`]+)`',go).group(1)==char_class(ranges),constant
print('pinned Rust regex source hashes, all recorded ranges, and Go classes match')
