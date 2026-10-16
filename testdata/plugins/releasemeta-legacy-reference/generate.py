"""Print an offline installed-Python engine reference script to stdout.

Pipe it to the original interpreter/container yourself. This tool never connects
or authenticates and never reads application config. Python is reference-only.
"""
import gzip
import json
import pathlib

root = pathlib.Path(__file__).resolve().parent
source = json.loads((root.parent / 'release-meta-legacy-python-provenance.json').read_text())
with gzip.open(root.parent / 'release-meta-legacy-anitopy-oracle.json.gz', 'rt') as handle:
    corpus = json.load(handle)['cases']
cases = [{k: value for k, value in sample.items() if k in ('name', 'title', 'options')} for sample in corpus]
print((root / 'engine.py').read_text())
print('assert SOURCE_SHA == ' + repr(source['anitopy_source']))
print('run(' + repr(cases) + ')')
