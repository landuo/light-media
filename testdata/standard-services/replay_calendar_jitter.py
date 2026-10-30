"""Real APScheduler 3.11.2 bridge against the frozen 33 calendar / 4 jitter cases."""
import contextlib
import io
import json
import runpy
from datetime import datetime, timedelta, timezone
import sys
sys.dont_write_bytecode = True
from replay_support import HERE, digest, prepare, prohibit_network, provenance

args = prepare()
network, _ = prohibit_network()
import apscheduler
from apscheduler.triggers.cron import CronTrigger
assert apscheduler.__version__ == '3.11.2'
calendar_file = HERE.parent / 'plugins' / 'apscheduler-3.11.3-times.json'
jitter_file = HERE / 'jitter-oracle.json'
generator = HERE / 'generate_jitter_oracle.py'
old = json.loads(calendar_file.read_text())
cases = []
for case in old['cases']:
    got, error = [], None
    try:
        trigger = (CronTrigger.from_crontab(case['cron'], timezone=case['timezone']) if case.get('cron')
                   else CronTrigger(timezone=case['timezone'], **case['fields']))
        at = datetime.fromisoformat(case['start'])
        for _ in range(8):
            value = trigger.get_next_fire_time(None, at)
            if value is None:
                break
            got.append(value.isoformat())
            # Advance an instant, not a local wall clock (which loses DST fold).
            at = value.astimezone(timezone.utc) + timedelta(microseconds=1)
    except Exception as exc:
        error = type(exc).__name__
    same_times = len(got) == len(case['times']) and all(
        datetime.fromisoformat(a) == datetime.fromisoformat(b) for a, b in zip(got, case['times']))
    equal = bool(case.get('error')) == bool(error) and (bool(error) or same_times)
    cases.append({'input': {k: case[k] for k in ['timezone', 'start', 'cron', 'fields']},
                  'old_times': case['times'], 'current_times': got,
                  'old_error': case.get('error'), 'current_error': error, 'equal': equal})
buf = io.StringIO()
with contextlib.redirect_stdout(buf):
    runpy.run_path(str(generator))
jitter = json.loads(buf.getvalue())
old_jitter = json.loads(jitter_file.read_text())
result = {'actual_version': apscheduler.__version__, 'old_allowed_version': old['version'],
          'calendar_count': len(cases), 'calendar': cases, 'jitter': jitter,
          'jitter_cases_equal': jitter['cases'] == old_jitter['cases'],
          'network_attempts': network, 'provenance': provenance(args),
          'fixture_hashes': {'testdata/plugins/apscheduler-3.11.3-times.json': digest(calendar_file),
                             'testdata/standard-services/jitter-oracle.json': digest(jitter_file),
                             'testdata/standard-services/generate_jitter_oracle.py': digest(generator)}}
expected = json.loads((HERE / 'calendar-jitter-aps312.json').read_text())
result['frozen_bridge_equal'] = {k: result[k] for k in expected} == expected
(args.output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'calendar_equal': sum(c['equal'] for c in cases), 'calendar_count': len(cases),
                  'jitter_count': len(jitter['cases']), 'jitter_equal': result['jitter_cases_equal'],
                  'network_attempts': network}))
assert len(cases) == 33 and len(jitter['cases']) == 4
assert all(c['equal'] for c in cases) and result['jitter_cases_equal'] and result['frozen_bridge_equal'] and not network
