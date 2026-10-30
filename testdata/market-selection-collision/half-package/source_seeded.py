"""Seeded observations from the normally imported complete fixed Manager.

This runs the complete component fixture first. It generates inputs only; all
selected outputs come from the original Manager, never the Go implementation.
"""
import hashlib, json, os, pathlib, random, runpy, sys

HERE = pathlib.Path(__file__).resolve().parent.parent
OUT = pathlib.Path(os.environ['MARKET_SELECTION_SOURCE_OUTPUT']).resolve()
sys.path.insert(0, str(HERE))
n = runpy.run_path(str(HERE / 'source_upstream.py'))
from app.schemas import Plugin

settings, manager, rng = n['settings'], n['manager'], random.Random(2683)
repos = ['https://github.com/Owned/First', 'https://github.com/Owned/Second', 'https://github.com/Owned/Unknown', 'local://Owned', 'LOCAL://Owned']
ids = ['OwnedA', 'OwnedA1', 'OwnedB', 'OwnedB1', 'OwnedC']
versions = ['1.0', '2.0', '12.0', '1.0.0', '0.1', '2.0rc1', 'v2.0']
observations = []
for number in range(400):
    markets = rng.choice([repos[:2], repos[1::-1], [], [repos[0], '', repos[1], repos[0]]])
    settings.update_setting('PLUGIN_MARKET', ','.join(markets))
    def rows():
        return [{'id': rng.choice(ids), 'version': rng.choice(versions), 'repo': rng.choice(repos)} for _ in range(rng.randrange(0, 24))]
    higher, base = rows(), rows()
    def make(values):
        return [Plugin(id=r['id'], plugin_version=r['version'], repo_url=r['repo']) for r in values]
    selected = manager.process_plugins_list(make(higher), make(base))
    observations.append({'name': f'seed2683-case{number:03d}', 'markets': markets, 'higher': higher, 'base': base, 'selected': [{'id': p.id, 'version': p.plugin_version, 'repo': p.repo_url} for p in selected]})
result = {'source_commit': n['inputs']['source_commit'], 'seed': 2683, 'cases': observations, 'base_source_result_sha256': hashlib.sha256((OUT / 'result.json').read_bytes()).hexdigest(), 'driver_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(), 'boundary': __doc__}
(OUT / 'seeded-result.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps({'actual_source_seeded_cases': len(observations)}))
