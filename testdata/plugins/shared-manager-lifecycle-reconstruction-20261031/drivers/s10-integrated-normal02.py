#!/usr/bin/env python3
"""Only the cwd-failed async root, exact normal01 binaries; no rebuild."""
import datetime, hashlib, json, os, shutil, subprocess
from pathlib import Path

R = Path(__file__).resolve().parent.parent
OLD = R / 'evidence/s10-integrated-normal01'
E = R / 'evidence/s10-integrated-normal02'
E.mkdir(mode=0o700, exist_ok=False)
ENV = os.environ.copy()
ENV.update(GOMAXPROCS='2', CGO_ENABLED='1')
record = {'started': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'status': 'RUNNING', 'environment_proxy_unchanged': True, 'driver_pid': os.getpid(), 'prior_failure_preserved': str(OLD)}
def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def freeze(paths): return {str(p): {'sha256': sha(p), 'bytes': p.stat().st_size} for p in sorted(paths)}
def save(): (E / 'result.json').write_text(json.dumps(record, indent=2) + '\n')
try:
    old = json.loads((OLD / 'result.json').read_text())
    expected = json.loads((OLD / 'inputs-after-compile.json').read_text())
    assert freeze({Path(x) for x in expected}) == expected, 'compiled closure drift'
    binary = R / 'runtime/s10-integrated-normal01.test'
    assert sha(binary) == old['program_sha256']
    for env, binding in old['fixture_bindings'].items():
        assert sha(binding['path']) == binding['sha256'], 'worker drift'
        ENV[env] = binding['path']
    oracle = R / 'light-media/testdata/standard-services/async-oracle.json'
    assert sha(oracle) == '8c209e25d6d0fab34032f63e75081a04efbb577565ea64a2107f2e841d2cc91a'
    paths = {Path(x) for x in expected} | {Path(__file__).resolve(), oracle, binary, OLD / 'result.json', OLD / 'inputs-after-compile.json'} | {Path(x['path']) for x in old['fixture_bindings'].values()}
    before = freeze(paths)
    (E / 'inputs-before.json').write_text(json.dumps(before, indent=2) + '\n')
    record.update(input_count=len(before), program_sha256=sha(binary), fixture_bindings=old['fixture_bindings'], cwd=str(R / 'light-media/internal/app'))
    record['free_before'] = {'scratch': shutil.disk_usage(R).free, 'tmp': shutil.disk_usage('/tmp').free}
    assert min(record['free_before'].values()) >= 1 << 30
    command = ['go', 'tool', 'test2json', '-t', '-p', './internal/app', str(binary), '-test.v', '-test.run', '^TestOriginalServiceActualSDKAsyncReplacementSource$', '-test.count=1', '-test.timeout=90s']
    record['command'] = command
    save()
    with (E / 'tests.stdout').open('wb') as out, (E / 'tests.stderr').open('wb') as err:
        result = subprocess.run(command, cwd=R / 'light-media/internal/app', env=ENV, stdout=out, stderr=err)
    record['exit'] = result.returncode
    (E / 'tests.exit').write_text(str(result.returncode) + '\n')
    after = freeze(paths)
    (E / 'inputs-after-test.json').write_text(json.dumps(after, indent=2) + '\n')
    assert after == before, 'test input drift'
    assert result.returncode == 0, 'targeted test failed'
    events = [json.loads(x) for x in (E / 'tests.stdout').read_text().splitlines() if x.startswith('{')]
    roots = [x['Test'] for x in events if x.get('Action') == 'pass' and 'Test' in x and '/' not in x['Test']]
    assert roots == ['TestOriginalServiceActualSDKAsyncReplacementSource']
    assert not [x for x in events if x.get('Action') in ('skip', 'fail')]
    record.update(status='PASS', root_passes=roots)
except BaseException as err:
    record.update(status='FAIL', error_class=type(err).__name__, error=str(err))
    raise
finally:
    record['ended'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    save()
    print(json.dumps({k: v for k, v in record.items() if k != 'fixture_bindings'}), flush=True)
