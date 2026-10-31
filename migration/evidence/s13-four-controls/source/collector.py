#!/usr/bin/env python3
"""Four complete original S13 methods, recording ports only; no vendor imports."""
import ast
import copy
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s13-four-source-preparation'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1
METHODS = {
    ('qb', 'set_torrents_tag'): ('torrents_add_tags', {'ids', 'tags'}),
    ('qb', 'set_torrent_category'): ('torrents_set_category', {'hash_string', 'category'}),
    ('qb', 'stop_torrents'): ('torrents_pause', {'ids'}),
    ('tr', 'stop_torrents'): ('stop_torrent', {'ids'}),
}


class CollectorBoundary(BaseException):
    """Collector limitations must not become source-caught business failures."""


def forbidden_imports():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'qbittorrentapi',
              'transmission_rpc', 'transmissionrpc', 'packaging')
    present = [n for n in sys.modules if any(n == p or n.startswith(p + '.') for p in denied)]
    if present:
        raise CollectorBoundary('forbidden imported modules: ' + ','.join(present))


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load_inputs():
    raw = (AUDIT / 'input-bindings.json').read_bytes()
    manifest = json.loads(raw)
    for item in manifest['inputs']:
        body = Path(item['path']).read_bytes()
        if len(body) != item['bytes'] or sha(body) != item['sha256']:
            raise CollectorBoundary('input drift: ' + item['path'])
    return manifest, sha(raw)


def ordinary(value):
    if value is None or type(value) in (str, bool, int):
        return value
    if type(value) is list:
        return [ordinary(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k: ordinary(v) for k, v in value.items()}
    raise CollectorBoundary('outside fixed ordinary JSON domain: ' + type(value).__name__)


class RecordingLogger:
    def __init__(self):
        self.trace = []

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})


class RecordingClient:
    def __init__(self, case, trace):
        self.method = case['vendor_method']
        self.reply = ordinary(copy.deepcopy(case['vendor_reply']))
        self.raises = case.get('vendor_raises')
        self.trace = trace
        self.calls = 0

    def call(self, method, kwargs):
        if method != self.method:
            raise CollectorBoundary('unexpected recording method: ' + method)
        self.calls += 1
        if self.calls > 1:
            raise CollectorBoundary('unexpected repeated vendor call')
        self.trace.append({'event': 'vendor_call', 'method': method, 'kwargs': ordinary(kwargs)})
        if self.raises is not None:
            types = {'RuntimeError': RuntimeError, 'ValueError': ValueError}
            if set(self.raises) != {'class', 'message'} or self.raises['class'] not in types or self.raises['message'] != 'owned recording failure':
                raise CollectorBoundary('unplanned dependency exception')
            self.trace.append({'event': 'vendor_exception', **self.raises})
            raise types[self.raises['class']](self.raises['message'])
        self.trace.append({'event': 'vendor_return', 'value': ordinary(self.reply)})
        return self.reply

    def __getattr__(self, method):
        raise CollectorBoundary('unavailable dependency operation: ' + method)


class RecordingQbc(RecordingClient):
    def torrents_add_tags(self, **kwargs):
        return self.call('torrents_add_tags', kwargs)

    def torrents_set_category(self, **kwargs):
        return self.call('torrents_set_category', kwargs)

    def torrents_pause(self, **kwargs):
        return self.call('torrents_pause', kwargs)


class RecordingTrc(RecordingClient):
    def stop_torrent(self, **kwargs):
        return self.call('stop_torrent', kwargs)


def original_classes(nodes, logger):
    classes, observations = {}, []
    for source in nodes['source_classes']:
        raw = Path(source['path']).read_bytes()
        if sha(raw) != source['sha256']:
            raise CollectorBoundary('source whole-file drift')
        original = next(n for n in ast.parse(raw, filename=source['path']).body
                        if isinstance(n, ast.ClassDef) and n.name == source['class'])
        if original.bases or original.keywords or original.decorator_list:
            raise CollectorBoundary('unreviewed original class dependency')
        if sha(ast.dump(original, include_attributes=True).encode()) != source['complete_class_ast_sha256']:
            raise CollectorBoundary('original class AST drift')
        selected = []
        for expected in source['nodes']:
            node = next(n for n in original.body if isinstance(n, ast.FunctionDef) and n.name == expected['name'])
            if node.name == '__init__' or node.decorator_list:
                raise CollectorBoundary('constructor/decorator outside the four-node closure')
            if any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node)):
                raise CollectorBoundary('source node acquired an import')
            segment = b''.join(raw.splitlines(keepends=True)[node.lineno - 1:node.end_lineno])
            actual = {'name': node.name, 'first_line': node.lineno, 'last_line': node.end_lineno,
                      'source_bytes_sha256': sha(segment),
                      'ast_sha256': sha(ast.dump(node, include_attributes=True).encode()),
                      'decorators': [], 'direct_original_helpers': []}
            if actual != expected:
                raise CollectorBoundary('original selected node drift')
            selected.append(node)
            observations.append(dict(actual, source_path=source['path'], original_class=source['class']))
        extracted = copy.deepcopy(original)
        extracted.body = selected  # Original method bodies/defaults/try/except unmodified.
        future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
        unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
        namespace = {'__name__': '__owned_original_s13_leaf__', 'logger': logger}
        # Annotation deferral is the same explicit component construction as
        # the accepted S03 collector, not a fake vendor type or source import.
        exec(compile(unit, source['path'], 'exec'), namespace)
        classes[source['kind']] = namespace[source['class']]
    return classes, observations


def run_case(case, classes, logger):
    allowed = {'id', 'kind', 'method', 'args', 'client_present', 'vendor_method', 'vendor_reply', 'vendor_raises', 'purpose'}
    if set(case) - allowed or type(case['client_present']) is not bool:
        raise CollectorBoundary('unplanned case fields')
    key = (case['kind'], case['method'])
    if key not in METHODS:
        raise CollectorBoundary('unselected method')
    vendor_method, argument_names = METHODS[key]
    if case['vendor_method'] != vendor_method or set(case['args']) != argument_names:
        raise CollectorBoundary('unplanned source arguments or dependency method')
    args = ordinary(copy.deepcopy(case['args']))
    logger.trace = []
    client_type = RecordingQbc if case['kind'] == 'qb' else RecordingTrc
    client = client_type(case, logger.trace)
    receiver = object.__new__(classes[case['kind']])  # No original constructor/config/service.
    setattr(receiver, 'qbc' if case['kind'] == 'qb' else 'trc', client if case['client_present'] else None)
    try:
        returned = getattr(receiver, case['method'])(**args)
    except Exception as error:
        outcome = {'kind': 'uncaught_exception', 'class': type(error).__name__, 'message': str(error)}
    else:
        outcome = {'kind': 'return', 'value': ordinary(returned),
                   'python_type': type(returned).__name__, 'python_truthy': bool(returned)}
    forbidden_imports()
    return {'id': case['id'], 'input': case, 'outcome': outcome,
            'vendor_calls': client.calls, 'trace': copy.deepcopy(logger.trace)}


def main():
    forbidden_imports()
    manifest, before_sha = load_inputs()
    nodes = json.loads((AUDIT / 'source-nodes.json').read_text())
    payload = json.loads((AUDIT / 'owned-cases.json').read_text())
    cases = payload['cases']
    if len(cases) != manifest['case_count'] or len({c['id'] for c in cases}) != len(cases):
        raise CollectorBoundary('case identity/count drift')
    logger = RecordingLogger()
    classes, observed_nodes = original_classes(nodes, logger)
    if len(observed_nodes) != 4 or set(classes) != {'qb', 'tr'}:
        raise CollectorBoundary('not exactly the four original methods')
    results = [run_case(case, classes, logger) for case in cases]
    forbidden_imports()
    _, after_sha = load_inputs()
    if after_sha != before_sha:
        raise CollectorBoundary('manifest changed')
    output = {'status': 'ORIGINAL_FOUR_LEAF_RECORDING_PORT_OBSERVATION_COMPLETE',
              'source_commit': manifest['source_commit'], 'input_manifest_sha256': before_sha,
              'python': {'version': sys.version, 'executable': sys.executable,
                         'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                         'dont_write_bytecode': sys.flags.dont_write_bytecode},
              'source_nodes': observed_nodes, 'case_count': len(results), 'cases': results,
              'boundaries': payload['boundaries'], 'forbidden_imports_absent': True, 'input_drift': []}
    print(json.dumps(output, ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == '__main__':
    main()
