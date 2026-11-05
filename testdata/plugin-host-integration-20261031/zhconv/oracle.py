#!/usr/bin/env python3
"""Run the real pinned wheel. Inputs come from upstream data and parser grammar."""
import hashlib, importlib.metadata, json, pathlib, random, sys
import zhconv_rs

ROOT = pathlib.Path(__file__).resolve().parents[3]
assert importlib.metadata.version('zhconv-rs') == '0.4.1'
pairs=[line.split('\t') for line in (ROOT/'internal/sourcezhconv/hans.tsv').read_text().splitlines()]
inputs=[('table',a) for a,b in pairs]
rng=random.Random(20261031)
for i in range(1200):
    inputs.append(('overlap', ''.join(rng.choice(pairs)[0] for _ in range(8))))
inputs += [('plain',s) for s in ['', '慶餘年 電腦軟體 北斗之拳 理髮 發行 乾隆 乾燥', 'A𠮷🍵臺灣日本語カナ123\n繁體', '\x00臺灣\x1f', '-{', '-{}-', '-{簡-{繁}', '-{臺灣}-', '-{ zh-hans:簡體;zh-hant:繁體 }-', '<code>繁體</code>繁體', '<code x="x">繁體</code>', '<CODE>繁體</CODE>', '<pre>繁\n體</pre>', '<script x="1">繁體</script>繁體', '-{'*12+'繁體'+'}-'*12]]
bodies=['繁體', ' zh-hans:簡體;zh-hant:繁體 ', 'zh-cn:大陆;zh-sg:新加坡', 'zh:原文', '繁體=>zh-hans:原樣;', 'zh-hans:&amp;繁體;zh-hant:繁體', 'zh-hans: a;bad', 'zh-HANS:簡;zh-hans:末', 'zh-hans:;zh-cn:中文', 'zh-hans:簡\n體', 'zh-hans:简;繁體=>zh-hans:短語;', 'zh-hans', 'zh-hant:繁', '', 'bad;', '=>zh-hans:甲']
flags=['','R','N','D','H','A','+','-','T','S','H+','HT','TA','TH','ND','HN','RH','SH','X',' H','+R','H-','-+']
for flag in flags:
    for body in bodies:
        inputs.append(('syntax', '繁體電腦-{'+(flag+'|' if flag else '')+body+'}-繁體電腦'))
inputs += [('global',s) for s in [
    '-{H|繁體=>zh-hans:短語;}-繁體',
    '-{H|zh-hans:甲;zh-hant:繁體;}-繁體',
    '-{H|繁體}-繁體',
    '-{H|繁體=>zh-hans:短語;}-繁體-{-|繁體=>zh-hans:短語;}-繁體',
    '-{-|繁體=>zh-hans:短語;}-繁體-{H|繁體=>zh-hans:短語;}-繁體',
    '-{-|发=>zh-hans:x;}-發髮',
    '<code>-{H|繁體=>zh-hans:短語;}-</code>繁體',
    '-{zh-hans:-{R|繁體}-;zh-hant:甲}-',
    '-{H|繁體=>zh-hans:短語;}-繁體-{H|繁體=>zh-hans:另一個;}-繁體',
    '-{H|繁體=>zh-hans:短語;}-繁體-{H|繁體電腦=>zh-hans:完整;}-繁體電腦',
]]
rows=[]
for typ,text in inputs:
    row={'type':typ,'input':text}
    try: row['output']=zhconv_rs.zhconv(text,'zh-hans')
    except BaseException as e: row['panic']=type(e).__name__
    # D descriptions iterate Rust HashMap. Retain the actual output but compare
    # permutation of complete clauses only when it is a pure bid description.
    if '-{D|' in text and '；' in row.get('output',''):
        row['description_order_unstable']=True
    rows.append(row)
wheel=pathlib.Path(zhconv_rs.__file__).parent
result={'version':importlib.metadata.version('zhconv-rs'),'python':sys.version,'files':{str(p.name):hashlib.sha256(p.read_bytes()).hexdigest() for p in wheel.iterdir() if p.is_file()},'case_count':len(rows),'cases':rows}
dest=ROOT/'internal/sourcezhconv/testdata/oracle.json';dest.parent.mkdir(parents=True,exist_ok=True);dest.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(rows),'panics':sum('panic' in r for r in rows),'artifact_sha256':hashlib.sha256(dest.read_bytes()).hexdigest()}))
