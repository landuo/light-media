import ast
import hashlib
import json
import pathlib
import re

ROOT=pathlib.Path(__file__).resolve().parent
legacy=json.loads((ROOT/'legacy-installed-source.json').read_text())
tree=ast.parse(legacy['source'])
assignment=next(n for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='__pinyin' for t in n.targets))
legacy_values=sorted(ast.literal_eval(assignment.value.args[0]))
source=ROOT/'source/inputx-pinyin-1.0.2/src/syllable.rs'
text=source.read_text();set_body=text.split('phf_set! {',1)[1].split('};',1)[0]
rust_values=re.findall(r'"([a-z]+)"',set_body)
assert len(rust_values)==len(set(rust_values))==403
parser=ROOT.parents[1]/'releasemeta-source/parser.rs'
parser_bytes=parser.read_bytes()
assert hashlib.sha256(parser_bytes).hexdigest()=='252b7e50adde31f7a96ce676c16a88dcb1f269fdad10737d1e4da9d1aa80795f'
wrapper=[]
for name in ['is_pinyin_like','is_ascii_pinyin_word']:
    original=parser_bytes.decode();start=original.index('fn '+name+'(');brace=original.index('{',start);depth=1;end=brace+1
    while depth:
        if original[end]=='{':depth+=1
        elif original[end]=='}':depth-=1
        end+=1
    wrapper.append(original[start:end])
(ROOT/'rust_oracle').mkdir(exist_ok=True)
(ROOT/'rust_oracle/name_wrapper.rs').write_text('\n\n'.join(wrapper)+'\n')
(ROOT/'legacy_syllables.txt').write_text('\n'.join(legacy_values)+'\n')
(ROOT/'rust_syllables.txt').write_text('\n'.join(rust_values)+'\n')
checksums={}
for relative in ['legacy-installed-source.json','legacy_syllables.txt','rust_syllables.txt','source/inputx-pinyin-1.0.2/src/syllable.rs','source/inputx-pinyin-1.0.2/src/segmenter.rs','source/inputx-pinyin-1.0.2/LICENSE-MIT','rust_oracle/name_wrapper.rs']:
    checksums[relative]=hashlib.sha256((ROOT/relative).read_bytes()).hexdigest()
(ROOT/'source-checksums.json').write_text(json.dumps({'Pinyin2Hanzi_version':'0.1.1','legacy_util_sha256':legacy['source_sha256'],'legacy_count':len(legacy_values),'crate_name':'inputx-pinyin','crate_version':'1.0.2','crate_sha256':'4fa1bf26d5923bb73e34c63e7ef21fc976c37e016ea13872045bf72335f0a43c','rust_parser_commit':'017b3a0e4ce2415d641c0eeb84ac907f9f10450c','rust_parser_sha256':hashlib.sha256(parser_bytes).hexdigest(),'rust_count':len(rust_values),'files':checksums},indent=2)+'\n')
print(json.dumps({'legacy_count':len(legacy_values),'rust_count':len(rust_values),'legacy_only':sorted(set(legacy_values)-set(rust_values)),'rust_only':sorted(set(rust_values)-set(legacy_values))}))
