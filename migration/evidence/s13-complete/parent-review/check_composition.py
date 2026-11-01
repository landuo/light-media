"""Read-only finite hash composition; never runs source, Go, HTTP or Git."""
from pathlib import Path
import datetime
import hashlib
import json

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
OUT = ROOT / 'audits/s13-parent-closure-final01'


def file(path):
    path = Path(path)
    data = path.read_bytes()
    return {'path': str(path), 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()}


def check(binding):
    got = file(binding['path'])
    assert got['sha256'] == binding['sha256'], binding['path']
    if 'bytes' in binding:
        assert got['bytes'] == binding['bytes'], binding['path']
    return got


precheck = ROOT / 'audits/s13-parent-closure-precheck01'
preseal = json.loads((precheck / 'precheck-seal.json').read_text())
for binding in preseal['files']:
    check(binding)
old = json.loads((precheck / 'bindings.json').read_text())
history = []
for layer in old['historical_layers']:
    current = [check(entry['current']) for entry in layer['current_target_bindings']]
    history.append({'family': layer['family'], 'index': check(layer['index']),
                    'source_raw': check(layer['source_raw']), 'CLI_result': check(layer['cli_result']),
                    'current_bindings': current, 'historical_differences_already_reviewed': layer['changed_paths']})
white = ROOT / 'target-drafts/s13-tr-numeric01/publication01/adoption-whitelist.pending.json'
plan = json.loads(white.read_text())
assert len(plan['files']) == 11
for entry in plan['files']:
    check(entry['accepted_backing'])
    target = ROOT / 'release-light-media' / entry['target_relative_path']
    if entry['current_preimage'] is not None:
        check(entry['current_preimage'])
    else:
        assert not target.exists(), str(target)
seals = []
for relative in ['source-final01/final-seal.json', 'target-final01/final-seal.json',
                 'native-final02/final-seal.json', 'cli-preexec02/preexec-seal.json',
                 'cli-final02/final-seal.json']:
    path = ROOT / 'audits/s13-tr-numeric-independent' / relative
    seal = json.loads(path.read_text())
    for binding in seal['files']:
        check(binding)
    seals.append(file(path))
result = {'status': 'EXACT_EIGHT_METHOD_BEHAVIOR_AND_HOST_CHAIN_ACCEPTED_PUBLICATION_PENDING',
          'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'inherited_precheck': file(precheck / 'precheck-seal.json'),
          'inherited_current': history, 'new_layer_seals': seals,
          'adoption_draft': file(white), 'adoption_paths': [x['target_relative_path'] for x in plan['files']],
          'current_drift': [], 'publication_executed': False,
          'execution': 'read/hash only; no source/Go/HTTP/Git or product/ledger write'}
(OUT / 'readback.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'status': result['status'], 'inherited_layers': len(history), 'new_layer_seals': len(seals), 'adoption_paths': 11}))
