#!/usr/bin/env python3
"""Prepared selected-original qB Cookie chain; only coordinator executes after review."""
import __future__
import ast
import copy
import functools
import hashlib
import importlib.util
import json
import os
import re
import sys
import types
import typing
import warnings
from http.cookies import SimpleCookie
from pathlib import Path
from urllib.parse import urlparse

D = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/source-collectors/s03-qb-cookie01')
PACKAGE = Path('/opt/codex/runtimes/codex-primary-runtime/dependencies/python/lib/python3.12/site-packages/packaging')
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    """A collector/security boundary must not become source business False."""


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def bound_inputs():
    raw = (D / 'input-bindings.json').read_bytes()
    manifest = json.loads(raw)
    for row in manifest['inputs']:
        data = Path(row['path']).read_bytes()
        if len(data) != row['bytes'] or sha(data) != row['sha256']:
            raise CollectorBoundary('bound input drift: ' + row['path'])
    return manifest, sha(raw)


DENIED_MODULES = ('app', 'qbittorrentapi', 'requests', 'urllib3', 'socket', '_socket',
                  'ssl', '_ssl', 'http.client', 'urllib.request', 'subprocess',
                  'multiprocessing', 'ctypes', 'transmission_rpc', 'transmissionrpc')


def check_modules():
    for name, module in tuple(sys.modules.items()):
        if any(name == prefix or name.startswith(prefix + '.') for prefix in DENIED_MODULES):
            raise CollectorBoundary('forbidden imported module: ' + name)
        if name == 'packaging' or name.startswith('packaging.'):
            if name not in ('packaging', 'packaging.version'):
                raise CollectorBoundary('unbound packaging module: ' + name)
            expected = PACKAGE / ('__init__.py' if name == 'packaging' else 'version.py')
            if Path(module.__file__).resolve() != expected:
                raise CollectorBoundary('packaging module origin drift')


AUDIT_COUNTS = {}
AUDIT_REJECTIONS = []


def audit(event, args):
    AUDIT_COUNTS[event] = AUDIT_COUNTS.get(event, 0) + 1
    forbidden = event.startswith(('socket.', 'subprocess.', 'ctypes.', 'os.exec', 'os.spawn', 'os.posix_spawn', 'os.fork'))
    forbidden = forbidden or event in ('os.system', 'os.remove', 'os.rename', 'os.mkdir', 'os.rmdir', 'os.link', 'os.symlink', 'os.truncate', 'os.chmod', 'os.chown')
    if event == 'open':
        _, mode, flags = args
        forbidden = forbidden or (isinstance(mode, str) and any(c in mode for c in 'wax+'))
        forbidden = forbidden or bool(flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND))
    if event == 'import':
        name = args[0]
        forbidden = forbidden or any(name == p or name.startswith(p + '.') for p in DENIED_MODULES)
    if forbidden:
        row = {'event': event, 'reason': 'collector forbids network/process/native-library/write operations'}
        AUDIT_REJECTIONS.append(row)
        sys.stderr.write(json.dumps({'AUDIT_REJECT': row}) + '\n')
        raise CollectorBoundary('collector audit rejected ' + event)


def module_files():
    result = {}
    for name, module in tuple(sys.modules.items()):
        filename = getattr(module, '__file__', None)
        if filename and Path(filename).is_file():
            path = Path(filename).resolve()
            result[name] = {'path': str(path), 'sha256': sha(path.read_bytes())}
    return result


def original_version_module(manifest):
    # Execute only the two exact, pre-approved pure packaging files. This does
    # not add site-packages to sys.path or run .pth/site/custom startup code.
    # No loader bytecode cache is read or written: compile bound source bytes.
    for name, filename in (('packaging', '__init__.py'), ('packaging.version', 'version.py')):
        path = PACKAGE / filename
        row = next(row for row in manifest['inputs'] if row['path'] == str(path))
        raw = path.read_bytes()
        if sha(raw) != row['sha256']:
            raise CollectorBoundary('packaging bytes drift')
        if name in sys.modules:
            raise CollectorBoundary('packaging unexpectedly preloaded')
        spec = importlib.util.spec_from_file_location(name, path, submodule_search_locations=[str(PACKAGE)] if name == 'packaging' else None)
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        exec(compile(raw, str(path), 'exec'), module.__dict__)
        if name == 'packaging.version':
            sys.modules['packaging'].version = module
    check_modules()
    return sys.modules['packaging.version']


def encode(value):
    if type(value) is tuple:
        return {'kind': 'tuple', 'items': [encode(v) for v in value]}
    if type(value) is list:
        return [encode(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k: encode(v) for k, v in value.items()}
    if value is None or type(value) in (str, bool, int):
        return value
    raise CollectorBoundary('outside fixed ordinary JSON/tuple domain: ' + type(value).__name__)


class RecordingLogger:
    def __init__(self):
        self.trace = []

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})

    def warn(self, message):
        self.trace.append({'event': 'log', 'level': 'WARN', 'message': str(message)})


class RecordingQbc:
    """Outermost qB protocol port only; no vendor object/HTTP/daemon semantics."""
    def __init__(self, store):
        self.store = copy.deepcopy(store)
        self.protocol = {}
        self.trace = []
        self.count = 0

    def begin(self, protocol, trace):
        if set(protocol) - {'version_reply', 'version_raise', 'cookies_raise', 'set_reply', 'set_raise', 'add_reply', 'add_raise'}:
            raise CollectorBoundary('unknown recording protocol control')
        self.protocol = protocol
        self.trace = trace
        self.count = 0

    def call(self, method, kwargs, prefix, reply, commit=None):
        self.count += 1
        if self.count > 4:
            raise CollectorBoundary('more than four qB protocol calls in one fixed add')
        self.trace.append({'event': 'protocol_call', 'method': method, 'kwargs': encode(kwargs)})
        message = self.protocol.get(prefix + '_raise')
        if message is not None:
            self.trace.append({'event': 'protocol_exception', 'method': method, 'class': 'RuntimeError', 'message': message, 'effect_model': 'owned pre-effect exception'})
            raise RuntimeError(message)
        if commit is not None:
            self.store = copy.deepcopy(commit)
            self.trace.append({'event': 'owned_cookie_store_commit', 'value': encode(self.store)})
        result = copy.deepcopy(reply)
        self.trace.append({'event': 'protocol_return', 'method': method, 'value': encode(result)})
        return result

    def app_web_api_version(self):
        return self.call('app_web_api_version', {}, 'version', self.protocol['version_reply'])

    def app_cookies(self):
        return self.call('app_cookies', {}, 'cookies', self.store)

    def app_set_cookies(self, *, cookies):
        if type(cookies) is not list:
            raise CollectorBoundary('unexpected cookie store write representation')
        return self.call('app_set_cookies', {'cookies': cookies}, 'set', self.protocol['set_reply'], commit=cookies)

    def torrents_add(self, **kwargs):
        return self.call('torrents_add', kwargs, 'add', self.protocol['add_reply'])

    def __getattr__(self, name):
        raise CollectorBoundary('unavailable recording qB operation: ' + name)


def original_classes(nodes, version, logger):
    namespace = {'__name__': '__owned_qb_cookie_source__', 'logger': logger,
                 'SimpleCookie': SimpleCookie, 'urlparse': urlparse,
                 'Version': version.Version, 'InvalidVersion': version.InvalidVersion}
    observed = []
    for source in nodes['classes']:
        raw = Path(source['path']).read_bytes()
        if sha(raw) != source['sha256']:
            raise CollectorBoundary('original whole-file mismatch')
        text = raw.decode('utf-8')
        original = next(n for n in ast.parse(text, filename=source['path']).body if isinstance(n, ast.ClassDef) and n.name == source['class'])
        if original.bases or original.keywords or original.decorator_list:
            raise CollectorBoundary('unselected original class construction dependency')
        selected = []
        for expected in source['nodes']:
            node = next(n for n in original.body if isinstance(n, ast.FunctionDef) and n.name == expected['name'])
            if sha(ast.get_source_segment(text, node).encode()) != expected['segment_sha256'] or sha(ast.dump(node, include_attributes=False).encode()) != expected['ast_sha256']:
                raise CollectorBoundary('original full-node mismatch')
            if node.name == '__init__' or any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node)):
                raise CollectorBoundary('constructor/import entered selected nodes')
            selected.append(node)  # Original decorators are not removed.
            observed.append(dict(expected, source_path=source['path'], original_class=source['class']))
        extracted = copy.deepcopy(original)
        extracted.body = selected
        future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
        unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
        exec(compile(unit, source['path'], 'exec'), namespace)
    if len(observed) != 9:
        raise CollectorBoundary('expected exactly nine complete original nodes')
    return namespace['Qbittorrent'], observed


def run_case(case, cls, logger):
    if set(case) != {'id', 'purpose', 'initial_cookie_store', 'instance', 'steps'}:
        raise CollectorBoundary('unknown case fields')
    encode(case)
    obj = object.__new__(cls)  # No original constructor/login/config execution.
    client = RecordingQbc(case['initial_cookie_store'])
    obj.qbc = client
    obj._category = case['instance']['category']
    obj._sequentail = case['instance']['sequential']
    obj._first_last_piece = case['instance']['first_last_piece']
    steps = []
    for index, step in enumerate(case['steps']):
        args = copy.deepcopy(step['args'])
        if set(args) != {'content', 'cookie'} or args['content'] not in ('https://owned-cookie.invalid/owned.torrent', 'relative/owned.torrent', 'magnet:?xt=urn:btih:OwnedCookieOnly') or type(args['cookie']) is not str or not args['cookie']:
            raise CollectorBoundary('outside fixed owned URL/truthy-cookie cases')
        logger.trace = []
        client.begin(step['protocol'], logger.trace)
        before = copy.deepcopy(client.store)
        try:
            result = obj.add_torrent(**args)
            outcome = {'kind': 'return', 'value': encode(result), 'python_type': type(result).__name__, 'python_truthy': bool(result)}
        except Exception as err:
            outcome = {'kind': 'uncaught_exception', 'class': type(err).__name__, 'message': str(err)}
        check_modules()
        steps.append({'index': index, 'input': step, 'cookie_store_before': encode(before), 'cookie_store_after': encode(client.store), 'outcome': outcome, 'protocol_calls': client.count, 'trace': logger.trace})
    return {'id': case['id'], 'input': case, 'steps': steps, 'final_cookie_store': encode(client.store)}


def main():
    check_modules()
    manifest, manifest_sha = bound_inputs()
    before_modules = module_files()
    sys.addaudithook(audit)
    version = original_version_module(manifest)
    logger = RecordingLogger()
    nodes = json.loads((D / 'source-nodes.json').read_text())
    payload = json.loads((D / 'owned-cases.json').read_text())
    cls, observed = original_classes(nodes, version, logger)
    if len(payload['cases']) != 17 or sum(len(c['steps']) for c in payload['cases']) != 18 or len({c['id'] for c in payload['cases']}) != 17:
        raise CollectorBoundary('fixed case set changed')
    observations = [run_case(case, cls, logger) for case in payload['cases']]
    check_modules()
    _, after_sha = bound_inputs()
    if after_sha != manifest_sha:
        raise CollectorBoundary('input manifest changed')
    after_modules = module_files()
    result = {'status': 'ORIGINAL_QB_URL_COOKIE_RECORDING_OBSERVATIONS_COMPLETE',
              'source_commit': manifest['source_commit'], 'input_manifest_sha256': manifest_sha,
              'python': {'version': sys.version, 'executable': sys.executable, 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site, 'dont_write_bytecode': sys.flags.dont_write_bytecode},
              'packaging_version': sys.modules['packaging'].__version__,
              'case_count': len(observations), 'add_call_count': 18, 'source_nodes': observed,
              'cases': observations, 'module_files_before': before_modules, 'module_files_after': after_modules,
              'audit_counts': dict(AUDIT_COUNTS), 'audit_rejections': AUDIT_REJECTIONS,
              'forbidden_imports_absent': True, 'input_drift': [], 'boundaries': payload['boundaries']}
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
