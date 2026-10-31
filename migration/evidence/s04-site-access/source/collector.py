#!/usr/bin/env python3
"""Prepared original SiteOper.success/fail + real owned SQLite component capture.

Coordinator execution only after independent review. No whole MoviePilot import.
The clock and seeded rows are explicit inputs, not a production clock/database.
"""
from __future__ import annotations

import ast
import hashlib
import json
import re
import sys
import threading
from datetime import datetime as RealDateTime
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Optional, Tuple, Union

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s04-source-preparation'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
SITE = ROOT / 'source-runtime/s10-venv/lib/python3.12/site-packages'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 3 and sys.argv[1] in ('warm', 'cold')
assert re.fullmatch(r'capture[0-9]+', sys.argv[2])
PHASE, RUN = sys.argv[1], AUDIT / sys.argv[2]
DB = RUN / 'owned.sqlite3'


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def forbidden_absent():
    prefixes = ('app', 'requests', 'urllib3')
    assert not any(name == p or name.startswith(p + '.')
                   for name in sys.modules for p in prefixes)


def check_inputs():
    records = []
    for line in (AUDIT / 'collector-inputs.sha256').read_text().splitlines():
        digest, filename = line.split('  ', 1)
        assert Path(filename).is_file() and sha(filename) == digest, filename
        records.append({'path': filename, 'sha256': digest})
    return records


forbidden_absent()
INPUTS = check_inputs()
THREADS_BEFORE = [t.name for t in threading.enumerate()]
MODULES_BEFORE = set(sys.modules)
# Same existing dependency root/import surface as the reviewed S06 DAO collector.
# No site/.pth processing, import hook, module replacement, new package or engine config.
sys.path.insert(0, str(SITE))
from sqlalchemy import Column, Integer, Sequence, Identity, String, JSON, create_engine, inspect
from sqlalchemy.orm import Session, as_declarative, declared_attr, scoped_session, sessionmaker
from sqlalchemy.ext.asyncio import AsyncSession
forbidden_absent()

settings = SimpleNamespace(DB_TYPE='sqlite')
SELECTED = []


class ClockInput:
    """Explicit input port: real naive datetime values, fixed caller-supplied times."""
    value = RealDateTime(2026, 10, 31, 12, 0, 0)
    calls = 0

    @classmethod
    def now(cls):
        cls.calls += 1
        return cls.value


datetime = ClockInput


def compile_selected(relative, names=(), class_name=None, methods=()):
    path = SOURCE / relative
    raw = path.read_bytes()
    tree = ast.parse(raw, filename=str(path))
    if class_name is None:
        selected = [n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name in names]
        assert {n.name for n in selected} == set(names)
        records = selected
    else:
        original = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == class_name)
        chosen = [n for n in original.body
                  if not isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) or n.name in methods]
        assert {n.name for n in chosen if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))} == set(methods)
        records = chosen
        original.body = chosen  # Omit unrelated methods only; all selected bodies/decorators/bases intact.
        selected = [original]
    for node in records:
        start = min([node.lineno] + [d.lineno for d in getattr(node, 'decorator_list', [])])
        fragment = b''.join(raw.splitlines(keepends=True)[start - 1:node.end_lineno])
        SELECTED.append({'file': relative, 'class': class_name,
                         'name': getattr(node, 'name', '<field-or-docstring>'),
                         'first_line': start, 'last_line': node.end_lineno,
                         'source_bytes_sha256': hashlib.sha256(fragment).hexdigest(),
                         'ast_sha256': hashlib.sha256(ast.dump(node, include_attributes=True).encode()).hexdigest()})
    module = ast.Module(body=selected, type_ignores=[])
    assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(module))
    exec(compile(ast.fix_missing_locations(module), str(path), 'exec', dont_inherit=True), globals())


compile_selected('app/db/__init__.py', names=('get_id_column', '_get_args_db', '_update_args_db', 'db_query', 'db_update'))
compile_selected('app/db/__init__.py', class_name='Base', methods=('create', 'update', 'to_dict', '__tablename__'))
compile_selected('app/db/__init__.py', class_name='DbOper', methods=('__init__',))
compile_selected('app/db/models/sitestatistic.py', class_name='SiteStatistic', methods=('get_by_domain',))
compile_selected('app/db/site_oper.py', class_name='SiteOper', methods=('success', 'fail'))
assert SELECTED == json.loads((AUDIT / 'selected-nodes.json').read_text())
COLUMNS = [c.name for c in SiteStatistic.__table__.columns]
assert COLUMNS == ['id', 'domain', 'success', 'fail', 'seconds', 'lst_state', 'lst_mod_date', 'note']
CASES = json.loads((AUDIT / 'owned-cases.json').read_text())
assert len({case['domain'] for case in CASES}) == len(CASES)
for case in CASES:
    assert type(case['domain']) is str and isinstance(case['actions'], list)
    for action in case['actions']:
        assert action['method'] in ('success', 'fail')
        assert action.get('session', 'auto') in ('auto', 'borrowed')
        assert type(action.get('seconds')) in (int, type(None))
        assert not (action['method'] == 'fail' and 'seconds' in action)
        assert RealDateTime.fromisoformat(action['time']).tzinfo is None


def all_rows(engine):
    with Session(engine) as db:
        return [row.to_dict() for row in db.query(SiteStatistic).order_by(SiteStatistic.id).all()]


def observe(call):
    # Only exceptions from the original call are projected. Setup/projection failures remain fatal.
    try:
        result = call()
    except Exception as error:
        original = getattr(error, 'orig', None)
        return {'exception': type(error).__name__, 'message': str(error),
                'sqlite_errorname': getattr(original, 'sqlite_errorname', None),
                'stage': 'original_source_call'}
    return {'returned': result, 'type': type(result).__name__}


def invoke(domain, action, db=None):
    ClockInput.value = RealDateTime.fromisoformat(action['time'])
    before = ClockInput.calls
    oper = SiteOper(db)
    if action['method'] == 'success':
        outcome = observe(lambda: oper.success(domain, action.get('seconds')))
    else:
        outcome = observe(lambda: oper.fail(domain))
    return {'outcome': outcome, 'clock_calls': ClockInput.calls - before}


if PHASE == 'warm':
    RUN.mkdir(mode=0o700)  # Exclusive new output; no overwrite of previous observations.
else:
    assert RUN.is_dir() and not RUN.is_symlink() and DB.is_file() and not DB.is_symlink()
    receipt = json.loads((RUN / 'warm-receipt.json').read_text())
    assert receipt['database_sha256'] == sha(DB)
    assert receipt['driver_sha256'] == sha(__file__)
    assert receipt['input_manifest_sha256'] == sha(AUDIT / 'collector-inputs.sha256')

url = 'sqlite:///' + str(DB) if PHASE == 'warm' else 'sqlite:///file:' + str(DB) + '?mode=ro&uri=true'
engine = create_engine(url)
SessionFactory = sessionmaker(bind=engine)
ScopedSession = scoped_session(SessionFactory)
observations = []
try:
    if PHASE == 'warm':
        Base.metadata.create_all(engine)
        for case in CASES:
            # Input seeds use actual ORM, not a rewritten SiteOper producer or expected output.
            if case['seed'] is not None:
                assert set(case['seed']) == set(COLUMNS) - {'id', 'domain'}
                with Session(engine) as seed:
                    seed.add(SiteStatistic(domain=case['domain'], **case['seed']))
                    seed.commit()
            before = all_rows(engine)
            actions = []
            for action in case['actions']:
                if action.get('session') == 'borrowed':
                    with Session(engine) as borrowed:
                        item = invoke(case['domain'], action, borrowed)
                        item['borrowed_session_usable_count'] = borrowed.query(SiteStatistic).count()
                else:
                    item = invoke(case['domain'], action)
                item.update({'input': action, 'rows_after': all_rows(engine)})
                actions.append(item)
            observations.append({'case': case['case'], 'domain': case['domain'], 'seed': case['seed'],
                                 'rows_before_actions': before, 'actions': actions})
    rows = all_rows(engine)
    if PHASE == 'cold':
        assert rows == receipt['rows']
        for case in CASES:
            row = SiteStatistic.get_by_domain(None, case['domain'])
            observations.append({'domain': case['domain'], 'row': None if row is None else row.to_dict()})
finally:
    ScopedSession.remove()
    engine.dispose()

before_errors = sha(DB)
errors = []
if PHASE == 'warm':
    # Real SQL error, separate NEW empty in-memory DB: no drop/rename/delete.
    empty = create_engine('sqlite:///:memory:')
    try:
        with Session(empty) as db:
            action = {'method': 'success', 'seconds': 3, 'time': '2026-10-31 13:00:00'}
            errors.append({'case': 'get_missing_table', 'domain': 'missing-owned.invalid',
                           'input': action, **invoke('missing-owned.invalid', action, db)})
    finally:
        empty.dispose()
    # Actual SQLite read-only write rejection reaches original db_update rollback/rethrow.
    # This is the just-created owned DB, not a production DB or a schema mutation.
    readonly = create_engine('sqlite:///file:' + str(DB) + '?mode=ro&uri=true')
    try:
        for method in ('success', 'fail'):
            for existence in ('existing', 'new'):
                domain = CASES[0]['domain'] if existence == 'existing' else 'readonly-' + method + '.invalid'
                action = {'method': method, 'time': '2026-10-31 13:00:01'}
                if method == 'success':
                    action['seconds'] = 9
                with Session(readonly) as db:
                    before = [row.to_dict() for row in db.query(SiteStatistic).order_by(SiteStatistic.id).all()]
                    item = invoke(domain, action, db)
                    after = [row.to_dict() for row in db.query(SiteStatistic).order_by(SiteStatistic.id).all()]
                    assert before == after
                    errors.append({'case': 'readonly_' + method + '_' + existence, 'domain': domain,
                                   'input': action, **item, 'rows_unchanged': before == after,
                                   'session_usable_after_source_error': True})
    finally:
        readonly.dispose()
assert before_errors == sha(DB)
if PHASE == 'cold':
    assert sha(DB) == receipt['database_sha256']
forbidden_absent()
assert THREADS_BEFORE == [t.name for t in threading.enumerate()]
imports = []
for name in sorted(sys.modules):
    filename = getattr(sys.modules[name], '__file__', None)
    if filename and Path(filename).is_file():
        imports.append({'module': name, 'file': filename, 'sha256': sha(filename),
                        'present_before_vendor_imports': name in MODULES_BEFORE})
check_inputs()
result = {'phase': PHASE, 'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
          'layer': 'complete selected SiteOper.success/fail, original ORM/Base/decorators, real owned SQLite; explicit fixed clock',
          'not_claimed': ['whole MoviePilot application', 'production clock/environment', 'PostgreSQL',
                          'other SiteOper methods', 'real site requests', 'Go equivalence', 'concurrent source transactions'],
          'driver_sha256': sha(__file__), 'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256'),
          'selected_original_nodes': SELECTED, 'inputs': INPUTS,
          'columns': [{'name': c.name, 'type': str(c.type), 'nullable': c.nullable, 'primary_key': c.primary_key}
                      for c in SiteStatistic.__table__.columns],
          'cases': observations, 'database_errors': errors, 'rows_final': rows,
          'database_sha256_before_errors_or_cold_end': before_errors, 'database_sha256_final': sha(DB),
          'database_read_only_open': PHASE == 'cold', 'python_executable': sys.executable,
          'python_executable_sha256': sha(sys.executable), 'imported_file_bindings': imports,
          'app_requests_urllib3_absent': True, 'socket_module_loaded': 'socket' in sys.modules,
          'threads_before': THREADS_BEFORE, 'threads_after': [t.name for t in threading.enumerate()]}
if PHASE == 'warm':
    with (RUN / 'warm-receipt.json').open('x') as stream:
        json.dump({'database_sha256': sha(DB), 'driver_sha256': sha(__file__),
                   'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256'), 'rows': rows},
                  stream, ensure_ascii=False, indent=2, allow_nan=False)
        stream.write('\n')
print(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False))
