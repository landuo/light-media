#!/usr/bin/env python3
"""Complete original qB tag cleanup/polling nodes; recording ports, not HTTP."""
import ast
import copy
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s13-qb-tags-source-preparation'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1
PUBLIC = {'delete_torrents_tag': {'ids', 'tag'}, 'get_torrent_id_by_tag': {'tags', 'status'}}
SELECTED = ('get_torrents', 'delete_torrents_tag', '__get_last_add_torrentid_by_tag', 'get_torrent_id_by_tag')


class CollectorBoundary(BaseException):
    """Unsupported collector behavior must not become source-caught failure."""


def forbidden_imports():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'qbittorrentapi',
              'transmission_rpc', 'transmissionrpc', 'packaging')
    bad = [n for n in sys.modules if any(n == p or n.startswith(p + '.') for p in denied)]
    if bad:
        raise CollectorBoundary('forbidden imported modules: ' + ','.join(bad))


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def ordinary(value):
    if value is None or type(value) in (str, bool, int):
        return value
    if type(value) is list:
        return [ordinary(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k: ordinary(v) for k, v in value.items()}
    raise CollectorBoundary('outside ordinary JSON: ' + type(value).__name__)


def load_inputs():
    raw = (AUDIT / 'input-bindings.json').read_bytes()
    manifest = json.loads(raw)
    for item in manifest['inputs']:
        body = Path(item['path']).read_bytes()
        if len(body) != item['bytes'] or sha(body) != item['sha256']:
            raise CollectorBoundary('input drift: ' + item['path'])
    return manifest, sha(raw)


class RecordingLogger:
    def __init__(self, trace):
        self.trace = trace

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})


class RecordingTime:
    def __init__(self, trace):
        self.trace, self.requested = trace, []

    def sleep(self, seconds):
        if type(seconds) is not int or seconds != 3 or len(self.requested) >= 9:
            raise CollectorBoundary('outside fixed nine 3-second sleep requests')
        self.requested.append(seconds)
        self.trace.append({'event': 'sleep_request', 'seconds': seconds,
                           'ordinal': len(self.requested), 'actual_wall_sleep': False})


class RecordingQbc:
    def __init__(self, steps, trace):
        self.steps = ordinary(copy.deepcopy(steps))
        self.trace, self.calls, self.returned = trace, 0, []

    def call(self, method, kwargs):
        if self.calls >= len(self.steps):
            raise CollectorBoundary('unplanned extra dependency call')
        step = self.steps[self.calls]
        self.calls += 1
        if step['method'] != method or set(step) not in ({'method', 'reply'}, {'method', 'raises'}):
            raise CollectorBoundary('unexpected dependency method or step')
        self.trace.append({'event': 'vendor_call', 'method': method,
                           'ordinal': self.calls, 'kwargs': ordinary(kwargs)})
        if 'raises' in step:
            error = step['raises']
            if error != {'class': 'RuntimeError', 'message': 'owned recording failure'}:
                raise CollectorBoundary('unplanned dependency exception')
            self.trace.append({'event': 'vendor_exception', 'method': method, **error})
            raise RuntimeError(error['message'])
        reply = copy.deepcopy(step['reply'])
        self.trace.append({'event': 'vendor_return', 'method': method, 'value': ordinary(reply)})
        # Keep the actual ordinary list object. Original get_torrents.clear()
        # therefore remains visible after the source call, including errors.
        self.returned.append((method, self.calls, reply))
        return reply

    def torrents_info(self, **kwargs):
        return self.call('torrents_info', kwargs)

    def torrents_remove_tags(self, **kwargs):
        return self.call('torrents_remove_tags', kwargs)

    def torrents_delete_tags(self, **kwargs):
        return self.call('torrents_delete_tags', kwargs)

    def __getattr__(self, method):
        raise CollectorBoundary('unavailable dependency operation: ' + method)


def original_class(bound, logger, clock):
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
                  'ast_sha256': sha(ast.dump(node, include_attributes=True).encode()),
                  'decorators': []}
        if actual != expected:
            raise CollectorBoundary('complete selected original node drift')
        selected.append(node)
        observed.append(actual)
    if tuple(n.name for n in selected) != SELECTED:
        raise CollectorBoundary('not exactly the original four-node closure')
    extracted = copy.deepcopy(original)
    extracted.body = selected  # No algorithm/default/try/loop/inner-function rewriting.
    future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
    namespace = {'__name__': '__owned_original_qb_tag_component__', 'logger': logger, 'time': clock}
    # Same explicit annotation deferral as accepted S03/S13 components. Keep
    # original Qbittorrent class name for real private-method name mangling.
    exec(compile(unit, bound['path'], 'exec'), namespace)
    return namespace[bound['class']], observed


def run_case(case, bound):
    if set(case) != {'id', 'method', 'args', 'client_present', 'steps', 'purpose'}:
        raise CollectorBoundary('unexpected case fields')
    if case['method'] not in PUBLIC or set(case['args']) != PUBLIC[case['method']] or type(case['client_present']) is not bool:
        raise CollectorBoundary('unselected source method or arguments')
    trace = []
    logger, clock = RecordingLogger(trace), RecordingTime(trace)
    cls, observed = original_class(bound, logger, clock)
    client = RecordingQbc(case['steps'], trace)
    receiver = object.__new__(cls)  # No original constructor/config/client/login.
    receiver.qbc = client if case['client_present'] else None
    try:
        value = getattr(receiver, case['method'])(**ordinary(copy.deepcopy(case['args'])))
    except Exception as error:
        outcome = {'kind': 'uncaught_exception', 'class': type(error).__name__, 'message': str(error)}
    else:
        outcome = {'kind': 'return', 'value': ordinary(value),
                   'python_type': type(value).__name__, 'python_truthy': bool(value)}
    if client.calls != len(case['steps']):
        raise CollectorBoundary('unused fixed dependency replies')
    forbidden_imports()
    return {'id': case['id'], 'input': case, 'outcome': outcome, 'trace': trace,
            'vendor_calls': client.calls, 'sleep_requests': clock.requested,
            'requested_sleep_seconds': sum(clock.requested), 'actual_wall_sleep': False,
            'dependency_return_values_after_source': [
                {'method': method, 'ordinal': number, 'value': ordinary(value)}
                for method, number, value in client.returned]}, observed


def main():
    forbidden_imports()
    manifest, before = load_inputs()
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
    _, after = load_inputs()
    if before != after:
        raise CollectorBoundary('input manifest drift')
    print(json.dumps({'status': 'ORIGINAL_QB_TAG_RECORDING_COMPONENT_OBSERVATION_COMPLETE',
                      'source_commit': manifest['source_commit'], 'input_manifest_sha256': before,
                      'python': {'version': sys.version, 'executable': sys.executable,
                                 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                                 'dont_write_bytecode': sys.flags.dont_write_bytecode},
                      'source_nodes': observed, 'case_count': len(results), 'cases': results,
                      'boundaries': payload['boundaries'], 'forbidden_imports_absent': True,
                      'input_drift': []}, ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == '__main__':
    main()
