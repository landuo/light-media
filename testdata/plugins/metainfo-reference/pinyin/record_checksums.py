import hashlib
import json
import pathlib

ROOT=pathlib.Path(__file__).resolve().parent
(ROOT/'licenses/inputx-pinyin-1.0.2-LICENSE-MIT').write_bytes((ROOT/'source/inputx-pinyin-1.0.2/LICENSE-MIT').read_bytes())
files=list((ROOT/'source/inputx-pinyin-1.0.2').rglob('*'))
files += [ROOT/path for path in ['pinyin.go','pinyin_test.go','legacy_syllables.txt','rust_syllables.txt','legacy-installed-source.json','source-checksums.json','legacy-license-provenance.json','oracle.json','rust_oracle/Cargo.toml','rust_oracle/Cargo.lock','rust_oracle/src/main.rs','rust_oracle/name_wrapper.rs']]
files += list((ROOT/'licenses').rglob('*'))
checksums={str(path.relative_to(ROOT)):hashlib.sha256(path.read_bytes()).hexdigest() for path in files if path.is_file()}
(ROOT/'complete-source-checksums.json').write_text(json.dumps({'files':dict(sorted(checksums.items()))},indent=2)+'\n')
print(json.dumps({'recorded_files':len(checksums),'oracle_sha256':checksums['oracle.json']}))
