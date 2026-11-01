#!/usr/bin/env python3
"""Coordinator-only: serial normal worker/server compile, no program execution."""
import argparse
import datetime
import hashlib
import json
import os
import shutil
import subprocess
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
REPO = ROOT / 'release-light-media'
HERE = Path(__file__).resolve().parent
NATIVE = HERE.parent / 'native02'
GATE = NATIVE / 'gates02'
BASE = '808148abfad0b7fe230c426387a3b7b49eeedb18'
P = argparse.ArgumentParser(description=__doc__)
for key in ('fixture-normal', 'app-normal', 'app-race'):
    P.add_argument('--expected-' + key + '-result-sha', required=True)
args = P.parse_args()
ENV = os.environ.copy()
assert ENV['GOTOOLCHAIN'] == 'local' and ENV['GOPROXY'] == 'off' and ENV['GOSUMDB'] == 'off'
assert ENV['GOMAXPROCS'] == '2' and ENV['CGO_ENABLED'] == '1'
assert ENV['GOCACHE'] == '/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/cache/go-build'
assert shutil.which('go') == '/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/toolchain/go/bin/go'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def freeze(paths):
    return {str(p): {'bytes': p.stat().st_size, 'sha256': sha(p)} for p in sorted(paths)}

overlay = json.loads((GATE / 'overlay.json').read_text())['Replace']
binding = json.loads((GATE / 'overlay-inputs-app-normal.json').read_text())
assert binding['baseline'] == BASE
rows = [binding['active_original'], binding['source_oracle'], binding['source_original']] + binding['additional_bound_files']
assert set(overlay) == {r['virtual_path'] for r in binding['replacements']}
for r in binding['replacements']:
    assert Path(r['virtual_path']).exists() == r['virtual_present_before']
    if r['backing'] is None:
        assert overlay[r['virtual_path']] == ''
        rows.append(r['removed_preimage'])
    else:
        assert overlay[r['virtual_path']] == r['backing']['path']
        rows.append(r['backing'])
for r in rows:
    assert Path(r['path']).stat().st_size == r['bytes'] and sha(r['path']) == r['sha256'], r['path']

prior_paths = set()
prior_roots = {
    'fixture-normal': ['TestOwnedTRFinalFixtureManifestAndRoute', 'TestOwnedTRNumericFixedSourceInputs'],
    'app-normal': ['TestOriginalTRNumericNativeSourceCases', 'TestOriginalTRFinalHostScopeAndUnknown'],
    'app-race': ['TestOriginalTRNumericNativeSourceCases', 'TestOriginalTRFinalHostScopeAndUnknown'],
}
prior_records = {}
for name, expected in prior_roots.items():
    label = 's13-tr-numeric-' + name + ('01' if name == 'fixture-normal' else '02')
    evidence = ROOT / 'evidence' / label
    result_path = evidence / 'result.json'
    expected_sha = getattr(args, 'expected_' + name.replace('-', '_') + '_result_sha')
    assert sha(result_path) == expected_sha, name + ' actual result changed'
    result = json.loads(result_path.read_text())
    assert result['status'] == 'PASS' and sorted(result['root_passes']) == sorted(expected)
    assert result['race'] == name.endswith('race')
    snapshots = [json.loads((evidence / f).read_text()) for f in
                 ('inputs-before.json', 'inputs-after-compile.json', 'inputs-after-test.json')]
    assert snapshots[0] == snapshots[1] == snapshots[2]
    assert freeze({Path(p) for p in snapshots[0]}) == snapshots[0], name + ' actual inputs drifted'
    program = ROOT / 'runtime' / (label + '.test')
    assert sha(program) == result['program_sha256']
    prior_paths.update(Path(p) for p in snapshots[0])
    prior_paths.update({result_path, evidence / 'inputs-before.json', evidence / 'inputs-after-compile.json',
                        evidence / 'inputs-after-test.json', program})
    prior_records[name] = {'result_sha256': expected_sha, 'input_count': len(snapshots[0]),
                           'program_sha256': result['program_sha256']}

# A single invocation builds the fixed worker first and then the normal server.
# Existing evidence/binary paths cause a stop; never overwrite or retry a gate.
for kind, package in (('fixture', './cmd/owned-tr-final-fixture'), ('server', './cmd/server')):
    label = 's13-tr-numeric-' + kind + '02'
    E = ROOT / 'evidence' / label
    BIN = ROOT / 'runtime' / label
    assert not BIN.exists()
    E.mkdir(exist_ok=False, mode=0o700)
    record = {'status': 'RUNNING', 'started': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'kind': 'normal compile only; no service/worker execution', 'base': BASE,
              'package': package, 'overlay': overlay, 'commands': [], 'prior_gates': prior_records,
              'environment_proxy_unchanged': True}

    def save():
        (E / 'result.json').write_text(json.dumps(record, indent=2) + '\n')

    def run(phase, argv):
        sample = {'phase': phase, 'scratch_free': shutil.disk_usage(ROOT).free,
                  'tmp_free': shutil.disk_usage('/tmp').free}
        record.setdefault('resource_samples', []).append(sample)
        assert min(sample['scratch_free'], sample['tmp_free']) >= 1 << 30
        command = {'phase': phase, 'argv': argv}
        record['commands'].append(command)
        save()
        with (E / (phase + '.stdout')).open('xb') as out, (E / (phase + '.stderr')).open('xb') as err:
            result = subprocess.run(argv, cwd=REPO, env=ENV, stdout=out, stderr=err)
        command['exit'] = result.returncode
        (E / (phase + '.exit')).write_text(str(result.returncode) + '\n')
        save()
        assert result.returncode == 0, phase + ' failed'
        return (E / (phase + '.stdout')).read_text()

    try:
        flags = ['-overlay=' + str(GATE / 'overlay.json'), '-mod=readonly', '-buildvcs=false']
        raw = run('go-list', ['go', 'list', *flags, '-deps', '-json', package])
        decoder, offset, declarations = json.JSONDecoder(), 0, []
        paths = {REPO / 'go.mod', REPO / 'go.sum', Path(__file__), GATE / 'overlay.json',
                 GATE / 'overlay-inputs-app-normal.json', HERE / 'preparation-seal.json',
                 NATIVE / 'preparation-seal.json'} | prior_paths
        paths.update(Path(r['path']) for r in rows)
        while offset < len(raw):
            while offset < len(raw) and raw[offset].isspace():
                offset += 1
            if offset == len(raw):
                break
            item, offset = decoder.raw_decode(raw, offset)
            if not item.get('Dir'):
                continue
            for key in ('GoFiles', 'CgoFiles', 'CFiles', 'CXXFiles', 'MFiles', 'HFiles', 'FFiles',
                        'SFiles', 'SwigFiles', 'SwigCXXFiles', 'SysoFiles', 'EmbedFiles'):
                for name in item.get(key, []):
                    virtual = str((Path(item['Dir']) / name).resolve())
                    backing = overlay.get(virtual, virtual)
                    assert backing, 'excluded Meta Go file declared: ' + virtual
                    path = Path(backing)
                    assert path.is_file(), 'missing compiler input: ' + virtual
                    paths.add(path)
                    declarations.append({'package': item.get('ImportPath'), 'kind': key,
                                         'virtual_path': virtual, 'backing_path': backing})
        (E / 'virtual-compile-inputs.json').write_text(json.dumps(declarations, indent=2) + '\n')
        before = freeze(paths)
        (E / 'inputs-before.json').write_text(json.dumps(before, indent=2) + '\n')
        generated, cache = {}, Path(ENV['GOCACHE']).resolve()
        for path in paths:
            if path.is_relative_to(cache):
                dest = E / 'generated-source' / (before[str(path)]['sha256'] + '.go')
                dest.parent.mkdir(exist_ok=True)
                dest.write_bytes(path.read_bytes())
                generated[str(path)] = str(dest.relative_to(E))
        (E / 'generated-source-map.json').write_text(json.dumps(generated, indent=2) + '\n')
        record['input_count'] = len(before)
        save()
        run('build', ['go', 'build', *flags, '-p=1', '-o', str(BIN), package])
        after = freeze(paths)
        (E / 'inputs-after.json').write_text(json.dumps(after, indent=2) + '\n')
        assert after == before, 'compile inputs drifted'
        record.update(status='PASS', program_path=str(BIN), program_sha256=sha(BIN), input_drift=[])
    except BaseException as error:
        record.update(status='FAIL', error_class=type(error).__name__, error=str(error))
        raise
    finally:
        record['ended'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save()
        print(json.dumps(record), flush=True)
