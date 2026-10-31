#!/usr/bin/env python3
"""Frozen finite S10 App gate; serial builds, no proxy changes or hidden builds."""
import argparse, datetime, hashlib, json, os, re, shutil, subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--label', required=True)
p.add_argument('--race', action='store_true')
a = p.parse_args()
assert a.race, 'this corrected driver is the frozen race23 gate'
ROOT = Path(__file__).resolve().parent.parent
REPO = ROOT / 'light-media'
PLAN = ROOT / 'audits/s10-coordinator-integration01/app-gate.json'
plan = json.loads(PLAN.read_text())
assert len(plan['roots']) == len(set(plan['roots'])) == 23
assert all(re.fullmatch(r'Test[A-Za-z0-9]+', x) for x in plan['roots'])
pattern = '^(' + '|'.join(plan['roots']) + ')$'
E = ROOT / 'evidence' / a.label
E.mkdir(exist_ok=False, mode=0o700)
BIN = ROOT / 'runtime' / (a.label + '.test')
assert not BIN.exists()
ENV = os.environ.copy()
ENV.update(GOMAXPROCS='2', CGO_ENABLED='1')
record = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'status': 'RUNNING', 'race': a.race, 'commands': [],
          'pattern': pattern, 'environment_proxy_unchanged': True,
          'driver_pid': os.getpid(), 'fixture_bindings': {}}

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def save():
    (E / 'result.json').write_text(json.dumps(record, indent=2) + '\n')

def freeze(paths):
    return {str(x): {'sha256': sha(x), 'bytes': x.stat().st_size} for x in sorted(paths)}

def run(label, cmd):
    sample = {'phase': label, 'at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'scratch_free': shutil.disk_usage(ROOT).free, 'tmp_free': shutil.disk_usage('/tmp').free}
    record.setdefault('resource_samples', []).append(sample)
    assert min(sample['scratch_free'], sample['tmp_free']) >= 1 << 30, 'actual disk safety floor reached'
    record['commands'].append({'label': label, 'argv': cmd})
    save()
    with (E / (label + '.stdout')).open('wb') as out, (E / (label + '.stderr')).open('wb') as err:
        result = subprocess.run(cmd, cwd=REPO / 'internal/app' if label == 'tests' else REPO, env=ENV, stdout=out, stderr=err)
    (E / (label + '.exit')).write_text(str(result.returncode) + '\n')
    record['commands'][-1]['exit'] = result.returncode
    save()
    if result.returncode:
        raise RuntimeError(label + ' exited ' + str(result.returncode))
    return (E / (label + '.stdout')).read_text()

try:
    flags = ['-race'] if a.race else []
    raw = run('go-list', ['go', 'list', '-mod=readonly', '-buildvcs=false', '-deps', '-test', '-json'] + flags + ['./internal/app'] + list(plan['fixtures'].values()))
    oracle = REPO / 'testdata/standard-services/async-oracle.json'
    assert sha(oracle) == '8c209e25d6d0fab34032f63e75081a04efbb577565ea64a2107f2e841d2cc91a'
    paths = {REPO / 'go.mod', REPO / 'go.sum', Path(__file__).resolve(), PLAN, oracle}
    decoder, offset = json.JSONDecoder(), 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset == len(raw):
            break
        package, offset = decoder.raw_decode(raw, offset)
        if not package.get('Dir'):
            continue
        for key in ('GoFiles', 'CgoFiles', 'TestGoFiles', 'XTestGoFiles', 'CFiles', 'CXXFiles', 'MFiles', 'HFiles', 'FFiles', 'SFiles', 'SwigFiles', 'SwigCXXFiles', 'SysoFiles', 'EmbedFiles', 'TestEmbedFiles', 'XTestEmbedFiles'):
            for name in package.get(key, []):
                path = Path(package['Dir']) / name
                if path.is_file():
                    paths.add(path.resolve())
    before = freeze(paths)
    (E / 'inputs-before.json').write_text(json.dumps(before, indent=2) + '\n')
    record['input_count'] = len(before)
    generated, cache = {}, Path(ENV['GOCACHE']).resolve()
    for path in paths:
        if path.is_relative_to(cache):
            target = E / 'generated-source' / (before[str(path)]['sha256'] + '.go')
            target.parent.mkdir(exist_ok=True)
            target.write_bytes(path.read_bytes())
            generated[str(path)] = str(target.relative_to(E))
    (E / 'generated-source-map.json').write_text(json.dumps(generated, indent=2) + '\n')
    save()
    # Detect App static errors before producing the remaining worker programs.
    run('compile', ['go', 'test', '-mod=readonly', '-buildvcs=false', '-p=1', '-c', '-o', str(BIN)] + flags + ['./internal/app'])
    record['program_sha256'] = sha(BIN)
    for index, (env, package) in enumerate(plan['fixtures'].items()):
        if env == 'OWNED_MANAGER_FIXTURE_BINARY' and not a.race:
            prior = ROOT / 'evidence' / plan['normal_manager_reuse_evidence']
            receipt = json.loads((prior / 'result.json').read_text())
            inputs = json.loads((prior / 'inputs-after.json').read_text())
            assert receipt['status'] == 'PASS'
            assert all(Path(name).is_file() and sha(name) == value['sha256'] for name, value in inputs.items()), 'manager compile closure drift'
            worker = ROOT / 'runtime/s10-d3-fixture01.worker'
            assert sha(worker) == receipt['program_sha256']
            origin = {'reused_compile': str(prior), 'receipt_sha256': sha(prior / 'result.json'), 'inputs_sha256': sha(prior / 'inputs-after.json')}
        else:
            worker = ROOT / 'runtime' / (a.label + '.worker' + str(index))
            assert not worker.exists()
            run('compile-worker-' + str(index), ['go', 'build', '-mod=readonly', '-buildvcs=false', '-p=1', '-o', str(worker)] + flags + [package])
            origin = {'new_compile': True}
        ENV[env] = str(worker)
        record['fixture_bindings'][env] = {'path': str(worker), 'sha256': sha(worker), 'package': package, **origin}
        save()
    compiled = freeze(paths)
    assert compiled == before, 'compile input drift'
    (E / 'inputs-after-compile.json').write_text(json.dumps(compiled, indent=2) + '\n')
    run('tests', ['go', 'tool', 'test2json', '-t', '-p', './internal/app', str(BIN), '-test.v', '-test.run', pattern, '-test.count=1', '-test.timeout=240s'])
    after = freeze(paths)
    assert after == before, 'test input drift'
    assert sha(BIN) == record['program_sha256'], 'App test program drift'
    assert all(sha(v['path']) == v['sha256'] for v in record['fixture_bindings'].values()), 'worker drift'
    (E / 'inputs-after-test.json').write_text(json.dumps(after, indent=2) + '\n')
    events = [json.loads(line) for line in (E / 'tests.stdout').read_text().splitlines() if line.startswith('{')]
    record['root_passes'] = [x['Test'] for x in events if x.get('Action') == 'pass' and 'Test' in x and '/' not in x['Test']]
    assert sorted(record['root_passes']) == sorted(plan['roots']), 'exact root set not executed'
    assert not [x for x in events if x.get('Action') in ('skip', 'fail')], 'unexpected fail/skip'
    record['status'] = 'PASS'
except BaseException as err:
    record.update(status='FAIL', error_class=type(err).__name__, error=str(err))
    raise
finally:
    record['ended'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    save()
    print(json.dumps({k: v for k, v in record.items() if k != 'commands'}), flush=True)
