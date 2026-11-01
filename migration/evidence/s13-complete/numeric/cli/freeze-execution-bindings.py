#!/usr/bin/env python3
"""Coordinator-only metadata seal after actual reviewed builds, no product run."""
import argparse
import datetime
import hashlib
import json
import shlex
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE = Path(__file__).resolve().parent
NATIVE = HERE.parent / 'native02'
P = argparse.ArgumentParser(description=__doc__)
P.add_argument('--expected-server-result-sha', required=True)
P.add_argument('--expected-fixture-result-sha', required=True)
args = P.parse_args()

def row(path):
    path = Path(path)
    raw = path.read_bytes()
    return {'path': str(path), 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}

paths = {Path(__file__), HERE / 'normal-cli-driver.py', HERE / 'preparation-seal.json',
         NATIVE / 'preparation-seal.json', NATIVE / 'candidate02.json',
         ROOT / 'evidence/s13-tr-numeric-source01/stdout.json'}
programs = {}
for kind in ('server', 'fixture'):
    E = ROOT / 'evidence' / ('s13-tr-numeric-' + kind + '02')
    result_path = E / 'result.json'
    assert row(result_path)['sha256'] == getattr(args, 'expected_' + kind + '_result_sha')
    result = json.loads(result_path.read_text())
    assert result['status'] == 'PASS' and result['base'] == '808148abfad0b7fe230c426387a3b7b49eeedb18'
    before = json.loads((E / 'inputs-before.json').read_text())
    after = json.loads((E / 'inputs-after.json').read_text())
    assert before == after
    for path, expected in before.items():
        current = row(path)
        assert current['bytes'] == expected['bytes'] and current['sha256'] == expected['sha256'], path
        paths.add(Path(path))
    program = ROOT / 'runtime' / ('s13-tr-numeric-' + kind + '02')
    assert result['program_path'] == str(program) and row(program)['sha256'] == result['program_sha256']
    paths.update({program, result_path, E / 'inputs-before.json', E / 'inputs-after.json',
                  E / 'virtual-compile-inputs.json', E / 'generated-source-map.json'})
    programs[kind] = row(program)
binding = {'status': 'COORDINATOR_FROZEN_READY_FOR_REVIEWED_EXECUTION',
           'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
           'base': '808148abfad0b7fe230c426387a3b7b49eeedb18',
           'server_sha256': programs['server']['sha256'], 'worker_sha256': programs['fixture']['sha256'],
           'driver_sha256': row(HERE / 'normal-cli-driver.py')['sha256'],
           'candidate_sha256': row(NATIVE / 'preparation-seal.json')['sha256'],
           'inputs': [row(p) for p in sorted(paths)]}
binding_path = HERE / 'execution-bindings.json'
with binding_path.open('x') as stream:
    stream.write(json.dumps(binding, indent=2) + '\n')
argv = ['python3', str(HERE / 'normal-cli-driver.py'), '--server', programs['server']['path'],
        '--worker', programs['fixture']['path'], '--output', str(ROOT / 'test-instances/s13-tr-numeric-normal02'),
        '--expected-server-sha', binding['server_sha256'], '--expected-worker-sha', binding['worker_sha256'],
        '--expected-driver-sha', binding['driver_sha256'], '--expected-bindings-sha', row(binding_path)['sha256']]
with (HERE / 'execution-command.json').open('x') as stream:
    stream.write(json.dumps({'status': 'PREPARED_NOT_EXECUTED', 'argv': argv,
                            'grant_marker': 'S13_TR_NUMERIC_CLI_GRANTED=1'}, indent=2) + '\n')
with (HERE / 'run-cli.sh').open('x') as stream:
    stream.write('#!/usr/bin/env bash\nset -euo pipefail\n[[ "$#" == 0 ]]\n')
    stream.write('export S13_TR_NUMERIC_CLI_GRANTED=1\n')
    stream.write('exec ' + shlex.join(argv) + '\n')
print(json.dumps({'bindings': row(binding_path), 'command': row(HERE / 'execution-command.json'),
                  'run_cli': row(HERE / 'run-cli.sh'), 'programs': programs}), flush=True)
