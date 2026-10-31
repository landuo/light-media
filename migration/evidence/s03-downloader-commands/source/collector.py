#!/usr/bin/env python3
"""Fixed original leaf AST against recording ports; never imports downloader code."""
import ast
import base64
import copy
import hashlib
import json
import sys
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s03-source-preparation'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    """Not caught by the source's business Exception fallback."""


def forbidden_imports():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'qbittorrentapi',
              'transmission_rpc', 'transmissionrpc', 'packaging')
    present = [n for n in sys.modules if any(n == p or n.startswith(p + '.') for p in denied)]
    if present:
        raise CollectorBoundary('forbidden imported modules: ' + ','.join(present))


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load_bound_inputs():
    raw = (AUDIT / 'input-bindings.json').read_bytes()
    manifest = json.loads(raw)
    for row in manifest['inputs']:
        data = Path(row['path']).read_bytes()
        if len(data) != row['bytes'] or sha(data) != row['sha256']:
            raise CollectorBoundary('input drift: ' + row['path'])
    return manifest, sha(raw)


def decode_input(value):
    if isinstance(value, dict):
        if set(value) == {'$bytes_base64'}:
            return base64.b64decode(value['$bytes_base64'], validate=True)
        return {k: decode_input(v) for k, v in value.items()}
    if isinstance(value, list):
        return [decode_input(v) for v in value]
    if value is None or type(value) in (str, bool, int):
        return value
    raise CollectorBoundary('outside fixed JSON/bytes input domain')


def encode_value(value):
    if type(value) is bytes:
        return {'kind': 'bytes', 'base64': base64.b64encode(value).decode('ascii'),
                'length': len(value), 'sha256': sha(value)}
    if type(value) is tuple:
        return {'kind': 'tuple', 'items': [encode_value(v) for v in value]}
    if type(value) is list:
        return [encode_value(v) for v in value]
    if type(value) is dict:
        if not all(type(k) is str for k in value):
            raise CollectorBoundary('non-string recording-map key')
        return {k: encode_value(v) for k, v in value.items()}
    if value is None or type(value) in (str, bool, int):
        return value
    raise CollectorBoundary('unexpected non-JSON result object: ' + type(value).__name__)


class RecordingLogger:
    def __init__(self):
        self.trace = []

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})


class ForbiddenStringUtils:
    @staticmethod
    def is_magnet_link(*args, **kwargs):
        raise CollectorBoundary('URL/cookie helper branch is explicitly pending')


def forbidden_cookie_sync(*args, **kwargs):
    raise CollectorBoundary('cookie sync / API Version branch is explicitly pending')


class RecordingClient:
    def __init__(self, case, trace):
        self.method = case['vendor_method']
        self.reply = decode_input(copy.deepcopy(case['vendor_reply']))
        self.raises = case.get('vendor_raises')
        self.trace = trace
        self.calls = 0

    def call(self, method, kwargs):
        if method != self.method:
            raise CollectorBoundary('unexpected recording vendor method: ' + method)
        if method == 'torrents_delete' and kwargs.get('delete_files') is not False:
            raise CollectorBoundary('delete_files must be exact False')
        if method == 'remove_torrent' and kwargs.get('delete_data') is not False:
            raise CollectorBoundary('delete_data must be exact False')
        self.calls += 1
        self.trace.append({'event': 'vendor_call', 'method': method, 'kwargs': encode_value(kwargs)})
        if self.raises is not None:
            self.trace.append({'event': 'vendor_exception', 'class': 'RuntimeError', 'message': self.raises})
            raise RuntimeError(self.raises)
        self.trace.append({'event': 'vendor_return', 'value': encode_value(self.reply)})
        return self.reply

    def __getattr__(self, name):
        raise CollectorBoundary('unavailable vendor operation: ' + name)


class RecordingQbc(RecordingClient):
    def torrents_add(self, **kwargs):
        return self.call('torrents_add', kwargs)

    def torrents_resume(self, **kwargs):
        return self.call('torrents_resume', kwargs)

    def torrents_recheck(self, **kwargs):
        return self.call('torrents_recheck', kwargs)

    def torrents_delete(self, **kwargs):
        return self.call('torrents_delete', kwargs)


class RecordingTrc(RecordingClient):
    def add_torrent(self, **kwargs):
        return self.call('add_torrent', kwargs)

    def start_torrent(self, **kwargs):
        return self.call('start_torrent', kwargs)

    def verify_torrent(self, **kwargs):
        return self.call('verify_torrent', kwargs)

    def remove_torrent(self, **kwargs):
        return self.call('remove_torrent', kwargs)


def original_classes(nodes, logger):
    classes = {}
    observed = []
    for source in nodes['source_classes']:
        raw = Path(source['path']).read_bytes()
        if sha(raw) != source['sha256']:
            raise CollectorBoundary('source whole-file mismatch')
        text = raw.decode('utf-8')
        original = next(n for n in ast.parse(text).body
                        if isinstance(n, ast.ClassDef) and n.name == source['class'])
        if original.bases or original.keywords or original.decorator_list:
            raise CollectorBoundary('unexpected original class construction dependency')
        selected = []
        for expected in source['nodes']:
            node = next(n for n in original.body
                        if isinstance(n, ast.FunctionDef) and n.name == expected['name'])
            segment = ast.get_source_segment(text, node)
            if sha(segment.encode()) != expected['segment_sha256'] or sha(ast.dump(node, include_attributes=False).encode()) != expected['ast_sha256']:
                raise CollectorBoundary('original AST node mismatch')
            if any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node)):
                raise CollectorBoundary('source method acquired an import')
            if node.name == '__init__':
                raise CollectorBoundary('constructor forbidden')
            selected.append(node)
            observed.append(dict(expected, source_path=source['path'], original_class=source['class']))
        extracted = copy.deepcopy(original)
        extracted.body = selected
        future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
        unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
        namespace = {'__name__': '__owned_original_leaf__', 'logger': logger,
                     'StringUtils': ForbiddenStringUtils}
        # Only the selected complete original method nodes run. No source
        # top-level imports, constructor, vendor library or Version comparator.
        exec(compile(unit, source['path'], 'exec'), namespace)
        cls = namespace[source['class']]
        if source['class'] == 'Qbittorrent':
            setattr(cls, '_Qbittorrent__sync_download_cookies', forbidden_cookie_sync)
        classes[source['kind']] = cls
    return classes, observed


def run_case(case, classes, logger):
    if set(case) - {'id', 'kind', 'method', 'args', 'client_present', 'instance', 'vendor_method', 'vendor_reply', 'vendor_raises', 'purpose'}:
        raise CollectorBoundary('unknown case control')
    args = decode_input(copy.deepcopy(case['args']))
    if case['method'] == 'add_torrent' and args.get('cookie') is not None:
        raise CollectorBoundary('all add cases require cookie is None before source entry')
    if case['method'] == 'delete_torrents' and args.get('delete_file') is not False:
        raise CollectorBoundary('delete_file must be exact False before source entry')
    if case['method'] not in ('add_torrent', 'start_torrents', 'recheck_torrents', 'delete_torrents'):
        raise CollectorBoundary('unselected original method')
    logger.trace = []
    client_type = RecordingQbc if case['kind'] == 'qb' else RecordingTrc
    client = client_type(case, logger.trace)
    obj = object.__new__(classes[case['kind']])
    if case['kind'] == 'qb':
        obj.qbc = client if case['client_present'] else None
        cfg = case['instance']
        obj._category = cfg['category']
        obj._sequentail = cfg['sequential']
        obj._first_last_piece = cfg['first_last_piece']
    else:
        obj.trc = client if case['client_present'] else None
    try:
        result = getattr(obj, case['method'])(**args)
        outcome = {'kind': 'return', 'value': encode_value(result),
                   'python_truthy': bool(result), 'python_type': type(result).__name__}
        if case['kind'] == 'tr' and case['method'] == 'add_torrent':
            outcome['same_recording_reply_identity'] = result is client.reply
            outcome['vendor_return_observed'] = any(t['event'] == 'vendor_return' for t in logger.trace)
    except Exception as err:
        outcome = {'kind': 'uncaught_exception', 'class': type(err).__name__, 'message': str(err)}
    forbidden_imports()
    return {'id': case['id'], 'input': case, 'outcome': outcome,
            'vendor_calls': client.calls, 'trace': logger.trace}


def main():
    forbidden_imports()
    manifest, manifest_sha = load_bound_inputs()
    nodes = json.loads((AUDIT / 'source-nodes.json').read_text())
    payload = json.loads((AUDIT / 'owned-cases.json').read_text())
    cases = payload['cases']
    if len(cases) != manifest['case_count'] or len({c['id'] for c in cases}) != len(cases):
        raise CollectorBoundary('case set mismatch')
    logger = RecordingLogger()
    classes, observed_nodes = original_classes(nodes, logger)
    if len(observed_nodes) != 10:
        raise CollectorBoundary('not exactly 8 methods plus 2 qB helpers')
    results = [run_case(case, classes, logger) for case in cases]
    forbidden_imports()
    _, final_sha = load_bound_inputs()
    if final_sha != manifest_sha:
        raise CollectorBoundary('input manifest changed')
    result = {'status': 'ORIGINAL_LEAF_RECORDING_PORT_OBSERVATION_COMPLETE',
              'source_commit': manifest['source_commit'],
              'input_manifest_sha256': manifest_sha,
              'python': {'version': sys.version, 'executable': sys.executable,
                         'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                         'dont_write_bytecode': sys.flags.dont_write_bytecode},
              'source_nodes': observed_nodes, 'case_count': len(results),
              'cases': results,
              'boundaries': payload['boundaries'],
              'forbidden_imports_absent': True, 'input_drift': []}
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
