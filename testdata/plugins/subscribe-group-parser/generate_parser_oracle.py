"""Run only the three inspected, pinned original parser methods on synthetic text."""
import ast
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).parent
raw = (ROOT / 'upstream-2.8.7.py').read_bytes()
assert hashlib.sha256(raw).hexdigest() == '06870f20e39237e4a69c9615ce005411feaa5e1a97cad50fd3b7b0645bead383'
tree = ast.parse(raw)
original = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'SubscribeGroup')
names = {'__parse_pix': 'resolution', '__parse_type': 'quality', '__parse_effect': 'effect'}
methods = [n for n in original.body if isinstance(n, ast.FunctionDef) and n.name in names]
assert len(methods) == 3
namespace = {'re': re}
for method in methods:
    method.decorator_list = []
    exec(compile(ast.Module(body=[method], type_ignores=[]), 'pinned-original-parser', 'exec'), namespace)
inputs = ['1080i', '720i', 'MiniBD', 'WEB-RIP', 'Dolby.s.Vision', 'DOVI', '4K', 'Atmos', 'HDTV', 'unknown', 'NotMiniBD', 'MiniBD\nrest']
samples = set(inputs)
for value in inputs:
    for letter in ('I', 'i', '\u0130', '\u0131'):
        samples.add(value.replace('i', letter).replace('I', letter))
    samples.add(value.lower())
    samples.add(value.upper())
cases = []
for value in sorted(samples):
    cases.append({'input': value, **{label: namespace[name](None, value) for name, label in names.items()}})
out = {'source_sha256': hashlib.sha256(raw).hexdigest(), 'scope': 'Three original parser functions, synthetic Unicode ignore-case inputs; no application imports or network.', 'cases': cases}
encoded = (json.dumps(out, ensure_ascii=True, sort_keys=True, indent=2) + '\n').encode()
(ROOT / 'parser-oracle.json').write_bytes(encoded)
print(json.dumps({'cases': len(cases), 'sha256': hashlib.sha256(encoded).hexdigest()}))
