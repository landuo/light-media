"""Offline input derivation only. Never import/call zhconv or create expectations."""
import hashlib
import json
import re
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE = ROOT / 'audits/s11-zhconv-source-preparation'
DATA = ROOT / 'audits/s11-zhconv-go-scope/reference/ZhConversion.php'
raw = DATA.read_bytes()
assert hashlib.sha256(raw).hexdigest() == '5cb0019b32bb39ec5c6e662029f90bd166f7a844efb3bc877f9be41fdd511bf2'
blocks = re.findall(r'public const (\w+) = \[([^]]+)\]?;', raw.decode('utf-8'))
body = next(body for name, body in blocks if name == 'ZH_TO_HANS')
pairs = re.findall(r"'(.+?)' *=> *'(.+?)' *,?\n", body)
assert len(pairs) == len({x for x, _ in pairs}) == 4685
cases = []
def add(name, text, group='wikitext', **origin):
    assert type(text) is str
    text.encode('utf-8')
    cases.append({'id': name, 'group': group, 'text': text, **origin})
for i, (source, _) in enumerate(pairs):
    add(f'hans-key-{i:04d}', source, 'all-hans-data-keys', table_index=i)
# Input combinations only: source-key prefix/suffix overlap, never expected replacement.
keys = {x for x, _ in pairs}
overlaps = []
for key in sorted(keys, key=lambda s: s.encode('utf-8')):
    for j in range(1, len(key)):
        for side, part in [('prefix', key[:j]), ('suffix', key[j:])]:
            if part in keys:
                overlaps.append((key, part, side))
for i, (long, short, side) in enumerate(overlaps[:24]):
    add(f'overlap-{i:02d}', long + short + long + ' / ' + short + long,
        'fixed-data-overlap', long_key=long, short_key=short, relation=side)
for name, text in [
    ('empty',''), ('ascii','Owned.Title.2026 + A&B / -x'),
    ('mixed','電影臺灣髮型後臺 / Owned 星 😀 𠀀'),
    ('controls','前\x00中\t後\r\n尾'),
    ('phrase-context','乾隆乾燥乾坤乾杯頭髮發展理髮'),
    ('adjacent','頭髮頭髮發展頭髮'),
    ('nbsp','\u00a0頭髮\u3000後臺\u0085'),
]: add('plain-'+name, text, 'plain-controls')
syntax = [
 ('inline-bid','前-{zh-hans:简體;zh-hant:繁體;}-後'),
 ('inline-fallback-cn','-{zh-cn:大陸;zh-sg:新加坡;zh-my:大馬;}-'),
 ('inline-fallback-sg','-{zh-sg:新加坡;zh-my:大馬;}-'),
 ('inline-fallback-my','-{zh-my:大馬;}-'),
 ('inline-no-fallback-hant','-{zh-hant:繁體;zh:原文;}-'),
 ('inline-duplicate','-{zh-hans:第一;zh-hans:第二;}-'),
 ('inline-case-space','-{ ZH-HANS : 文字 ;zh-hant:繁體;}-'),
 ('inline-unid-only','-{Owned=>zh-hans:輸出;}-Owned'),
 ('inline-mixed','-{zh-hans:內文;Owned=>zh-hans:映射;}-Owned'),
 ('inline-literal','前-{頭髮}-後'), ('empty-rule','前-{}-後'),
 ('empty-flag','前-{|頭髮}-後'), ('raw-flag','前-{R|zh-hans:頭髮;}-後'),
 ('raw-flag-order','-{+R|頭髮}-頭髮'), ('raw-after-unknown','-{?R|頭髮}-'),
 ('normal-flag','-{S|zh-hans:頭髮;}-'),
 ('hidden-after-text','Owned前-{H|Owned=>zh-hans:頭髮;}-Owned後'),
 ('add-flag','Owned-{+|Owned=>zh-hans:頭髮;}-Owned'),
 ('a-flag','Owned-{A|Owned=>zh-hans:頭髮;}-Owned'),
 ('title-flag','前-{T|zh-hans:标题;zh-hant:標題;}-後'),
 ('title-invalid-asis','前-{T|頭髮}-後'),
 ('remove-builtin-replacement','後-{-|后}-後'),
 ('remove-builtin-source','後-{-|後}-後'),
 ('add-remove-add','Owned-{H|Owned=>zh-hans:甲;}-{-|Owned=>zh-hans:甲;}-'+
                   '-{H|Owned=>zh-hans:乙;}-Owned'),
 ('secondary-wins','後-{H|後=>zh-hans:Owned;}-後'),
 ('secondary-longer','OwnedAB-{H|Owned=>zh-hans:甲;OwnedAB=>zh-hans:乙;}-OwnedAB'),
 ('replacement-not-reconverted','Owned-{H|Owned=>zh-hans:頭髮;}-Owned'),
 ('global-bid-hans','標籤-{H|zh-hans:标签;zh-hant:標籤;}-標籤'),
 ('global-no-regional-fallback','Owned-{H|Owned=>zh-cn:大陸;}-Owned'),
 ('global-last-add','Owned-{H|Owned=>zh-hans:甲;}-'+
                    '-{H|Owned=>zh-hans:乙;}-Owned'),
 ('multiflag-hs','-{HS|zh-hans:頭髮;}-'),
 ('multiflag-minus-plus','Owned-{H|Owned=>zh-hans:甲;}-{-+|Owned=>zh-hans:乙;}-Owned'),
 ('multiflag-plus-minus','Owned-{+-|Owned=>zh-hans:甲;}-Owned'),
 ('entity-semicolon','-{zh-hans:A&amp;B;zh-hant:C;}-'),
 ('entity-numeric','-{zh-hans:A&#65;B;zh-hant:C;}-'),
 ('entity-bare-amp','-{zh-hans:A&;zh-hant:C;}-'),
 ('entity-reset','-{zh-hans:A& x;zh-hant:C;}-'),
 ('unicode-trim','-{\u3000zh-hans\u00a0: A \u3000;zh-hant:B;}-'),
 ('unid-from-whitespace','Owned -{H| Owned =>zh-hans:甲;}-Owned '),
 ('unknown-flag','前-{Q|zh-hans:頭髮;}-後'),
 ('unknown-variant','前-{zh-owned:頭髮;}-後'),
 ('bad-mixed-conv','前-{zh-hans:甲;bad}-後'),
 ('nested','前-{zh-hans:甲-{zh-hans:乙;}-丙;zh-hant:丁;}-後'),
 ('nested-unclosed','前-{頭髮-{後臺'),
 ('unclosed','前-{頭髮'), ('unmatched-close','前頭髮}-後'),
 ('depth-10','-{'*10+'頭髮'+'}-'*10),
 ('depth-11','-{'*11+'頭髮'+'}-'*11),
 ('global-newline','Owned-{H|Owned=>zh-hans:甲;\n}-Owned'),
 ('html-script','前<script type="text/plain">頭髮-{zh-hans:甲;}-</script>後'),
 ('html-style','前<style>頭髮</style>後'),
 ('html-code','前<code>頭髮-{R|後臺}-</code>後'),
 ('html-code-attr','前<code class="owned">頭髮</code>後'),
 ('html-pre','前<pre class="owned">頭髮</pre>後'),
 ('html-case','前<SCRIPT>頭髮</SCRIPT>後'),
 ('html-newline','前<script>頭髮\n後臺</script>後'),
 ('html-global-rule-visible','Owned<script>-{H|Owned=>zh-hans:甲;}-</script>Owned'),
 ('description-single','-{D|zh-hans:頭髮;}-'),
 ('description-multi','-{D|zh-hans:甲;zh-hant:乙;zh-cn:丙;}-'),
 ('description-unid','-{D|Owned=>zh-hans:甲;Other=>zh-cn:乙;}-'),
]
for name, text in syntax:
    add(name,text, comparison_note='retain native string exactly; multi-variant D order not normalized' if name.startswith('description-') else 'no precomputed expected')
for variant in ['zh','zh-hans','zh-hant','zh-cn','zh-sg','zh-my','zh-tw','zh-hk','zh-mo']:
    add('name-'+variant, '-{N|'+variant+'}-')
for i in range(3):
    add(f'description-repeat-{i}', '-{D|zh-hans:甲;zh-hant:乙;zh-cn:丙;}-',
        'unordered-description-observation', comparison_note='separate calls; raw order retained, no stability assertion')
# Ordinary fixed string reaches an assert in original Conv::from_str. Observe only;
# future native PanicException/stderr are not assumed or rewritten.
add('malformed-empty-from', '-{=>zh-hans:甲;}-', 'malformed-native-observation')
assert len({c['id'] for c in cases}) == len(cases)
assert max(len(c['text'].encode()) for c in cases) < 1024
out={'status':'PREPARED INPUTS ONLY; NO EXPECTED OUTPUTS', 'variant':'zh-hans',
     'positional_argument_count':2,'third_argument_omitted':True,
     'source_table_sha256':hashlib.sha256(raw).hexdigest(),
     'data_keys':4685,'fixed_overlap_cases':24,'case_count':len(cases),'cases':cases}
(HERE/'owned-cases.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'case_count':len(cases),'data_keys':4685,'extra_cases':len(cases)-4685,
 'input_sha256':hashlib.sha256((HERE/'owned-cases.json').read_bytes()).hexdigest()}))
