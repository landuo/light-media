#!/usr/bin/env python3
"""Finite ordinary TR numeric-string spelling annex; original conversions, no vendor/HTTP."""
import __future__
import ast
import copy
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'source-collectors/s13-tr-numeric01'
SELECTED = ('set_torrent_tag', 'change_torrent')
PUBLIC = {'set_torrent_tag': {'ids', 'tags', 'org_tags'},
          'change_torrent': {'hash_string', 'upload_limit', 'download_limit',
                             'ratio_limit', 'seeding_time_limit'}}
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    """Collector misuse must not become a source-caught ordinary exception."""


def forbidden_imports():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'qbittorrentapi',
              'transmission_rpc', 'transmissionrpc', 'packaging', 'ssl', '_ssl',
              'http', 'urllib.request', 'subprocess', 'multiprocessing', 'ctypes', 'tzlocal')
    bad = [n for n in sys.modules if any(n == p or n.startswith(p + '.') for p in denied)]
    if bad:
        raise CollectorBoundary('forbidden imported modules: ' + ','.join(bad))


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def ordinary(value):
    if value is None or type(value) in (str, bool, int):
        return value
    if type(value) is float and float('-inf') < value < float('inf'):
        return value
    if type(value) is list:
        return [ordinary(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k: ordinary(v) for k, v in value.items()}
    raise CollectorBoundary('outside finite ordinary JSON: ' + type(value).__name__)


def load_inputs():
    raw = (AUDIT / 'input-bindings.json').read_bytes()
    manifest = json.loads(raw)
    for item in manifest['inputs']:
        body = Path(item['path']).read_bytes()
        if len(body) != item['bytes'] or sha(body) != item['sha256']:
            raise CollectorBoundary('input drift: ' + item['path'])
    for path in manifest['absent_cache_paths']:
        if Path(path).exists():
            raise CollectorBoundary('bound absent cache appeared: ' + path)
    return manifest, sha(raw)


def module_files(manifest):
    bound = {item['path']: item for item in manifest['inputs']}
    observed = {}
    forbidden_imports()
    for name, module in tuple(sys.modules.items()):
        filename = getattr(module, '__file__', None)
        if filename and Path(filename).is_file():
            path = str(Path(filename).resolve())
            if path not in bound:
                raise CollectorBoundary('unbound stdlib/module file: ' + path)
            body = Path(path).read_bytes()
            if len(body) != bound[path]['bytes'] or sha(body) != bound[path]['sha256']:
                raise CollectorBoundary('module bytes drift: ' + path)
            observed[name] = {'path': path, 'sha256': sha(body)}
    return observed


class RecordingLogger:
    def __init__(self, trace):
        self.trace = trace

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})


class RecordingClient:
    def __init__(self, steps, trace):
        self.steps = ordinary(copy.deepcopy(steps))
        self.trace, self.calls = trace, 0

    def change_torrent(self, **kwargs):
        if self.calls >= len(self.steps):
            raise CollectorBoundary('unplanned extra dependency call')
        step = self.steps[self.calls]
        self.calls += 1
        if step['method'] != 'change_torrent' or set(step) not in ({'method', 'reply'}, {'method', 'raises'}):
            raise CollectorBoundary('unexpected dependency method or step')
        self.trace.append({'event': 'recording_client_call', 'method': 'change_torrent',
                           'ordinal': self.calls, 'kwargs': ordinary(kwargs),
                           'python_types': {k: type(v).__name__ for k, v in kwargs.items()}})
        if 'raises' in step:
            error = step['raises']
            if error == {'class': 'RuntimeError', 'message': 'owned recording failure'}:
                exception = RuntimeError(error['message'])
            elif error == {'class': 'ValueError', 'message': 'No arguments to set'}:
                exception = ValueError(error['message'])
            else:
                raise CollectorBoundary('unplanned dependency exception')
            self.trace.append({'event': 'recording_client_exception', 'method': 'change_torrent', **error})
            raise exception
        reply = ordinary(copy.deepcopy(step['reply']))
        self.trace.append({'event': 'recording_client_return', 'method': 'change_torrent', 'value': reply})
        return reply

    def __getattr__(self, method):
        raise CollectorBoundary('unavailable dependency operation: ' + method)


def original_class(bound, logger):
    raw = Path(bound['path']).read_bytes()
    if sha(raw) != bound['sha256']:
        raise CollectorBoundary('original file changed')
    original = next(n for n in ast.parse(raw, filename=bound['path']).body
                    if isinstance(n, ast.ClassDef) and n.name == bound['class'])
    if original.bases or original.keywords or original.decorator_list:
        raise CollectorBoundary('unexpected original class dependencies')
    if sha(ast.dump(original, include_attributes=True).encode()) != bound['complete_class_ast_sha256']:
        raise CollectorBoundary('whole original class AST drift')
    selected, observed = [], []
    for expected in bound['nodes']:
        node = next(n for n in original.body if isinstance(n, ast.FunctionDef) and n.name == expected['name'])
        if node.name not in SELECTED or node.decorator_list or any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node)):
            raise CollectorBoundary('unselected node/import/decorator')
        segment = b''.join(raw.splitlines(keepends=True)[node.lineno - 1:node.end_lineno])
        actual = {'name': node.name, 'first_line': node.lineno, 'last_line': node.end_lineno,
                  'source_bytes_sha256': sha(segment),
                  'ast_sha256': sha(ast.dump(node, include_attributes=True).encode()), 'decorators': []}
        if actual != expected:
            raise CollectorBoundary('complete selected original node drift')
        selected.append(node)
        observed.append(actual)
    if tuple(n.name for n in selected) != SELECTED:
        raise CollectorBoundary('not exactly the original two-node closure')
    extracted = copy.deepcopy(original)
    extracted.body = selected  # Complete methods, including defaults/try boundaries.
    future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
    namespace = {'__name__': '__owned_original_tr_final_component__', 'logger': logger}
    # Preserve original class name; no constructor, login, module/vendor import.
    exec(compile(unit, bound['path'], 'exec'), namespace)
    return namespace[bound['class']], observed


def run_case(case, bound):
    if set(case) != {'id', 'method', 'args', 'client_present', 'steps', 'purpose'}:
        raise CollectorBoundary('unexpected case fields')
    if case['method'] not in PUBLIC or not set(case['args']) <= PUBLIC[case['method']] or type(case['client_present']) is not bool:
        raise CollectorBoundary('unselected source method or arguments')
    # This annex calls only the already-admitted original numeric method.
    # Its retained sibling declaration is never invoked.
    if case['method'] != 'change_torrent' or not case['client_present']:
        raise CollectorBoundary('annex permits only present-client change_torrent')
    if case['args'].get('hash_string') != 'owned-numeric':
        raise CollectorBoundary('only the fixed owned recording id is permitted')
    for key, value in case['args'].items():
        if key != 'hash_string' and value is not None and (type(value) is not str or len(value) > 64):
            raise CollectorBoundary('numeric annex requires short ordinary strings or None')
    if case['steps'] not in ([], [{'method': 'change_torrent', 'reply': None}]):
        raise CollectorBoundary('only zero/one fixed nonthrowing recording call permitted')
    required = {'ids', 'tags'} if case['method'] == 'set_torrent_tag' else {'hash_string'}
    if not required <= set(case['args']):
        raise CollectorBoundary('missing required original argument')
    trace = []
    cls, observed = original_class(bound, RecordingLogger(trace))
    client = RecordingClient(case['steps'], trace)
    receiver = object.__new__(cls)
    receiver.trc = client if case['client_present'] else None
    arguments = ordinary(copy.deepcopy(case['args']))
    try:
        value = getattr(receiver, case['method'])(**arguments)
    except Exception as error:
        outcome = {'kind': 'uncaught_exception', 'class': type(error).__name__, 'message': str(error)}
    else:
        outcome = {'kind': 'return', 'value': ordinary(value),
                   'python_type': type(value).__name__, 'python_truthy': bool(value)}
    if client.calls != len(case['steps']):
        raise CollectorBoundary('unused fixed dependency replies')
    forbidden_imports()
    return {'id': case['id'], 'input': case, 'outcome': outcome, 'trace': trace,
            'recording_client_calls': client.calls, 'arguments_after_source': ordinary(arguments)}, observed


def main():
    forbidden_imports()
    manifest, before = load_inputs()
    modules_before = module_files(manifest)
    bound = json.loads((AUDIT / 'source-nodes.json').read_text())['source_class']
    payload = json.loads((AUDIT / 'owned-cases.json').read_text())
    cases = payload['cases']
    if len(cases) != manifest['case_count'] or len({x['id'] for x in cases}) != len(cases):
        raise CollectorBoundary('case count/identity drift')
    results, observed = [], None
    for case in cases:
        result, nodes = run_case(case, bound)
        if observed is not None and nodes != observed:
            raise CollectorBoundary('selected source changed between cases')
        results.append(result)
        observed = nodes
    forbidden_imports()
    modules_after = module_files(manifest)
    _, after = load_inputs()
    if before != after:
        raise CollectorBoundary('input manifest drift')
    print(json.dumps({'status': 'ORIGINAL_TR_NUMERIC_STRING_RECORDING_OBSERVATION_COMPLETE',
                      'source_commit': manifest['source_commit'], 'input_manifest_sha256': before,
                      'python': {'version': sys.version, 'executable': sys.executable,
                                 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                                 'dont_write_bytecode': sys.flags.dont_write_bytecode,
                                 'hash_randomization': sys.flags.hash_randomization,
                                 'hash_algorithm': sys.hash_info.algorithm},
                      'source_nodes': observed, 'case_count': len(results), 'cases': results,
                      'modules_before': modules_before, 'modules_after': modules_after,
                      'numeric_scope': 'Short ordinary JSON strings; original int/float/round, no replacement normalization in collector.',
                      'boundaries': payload['boundaries'], 'forbidden_imports_absent': True,
                      'input_drift': []}, ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == '__main__':
    main()
