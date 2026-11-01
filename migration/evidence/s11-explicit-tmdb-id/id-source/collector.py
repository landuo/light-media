#!/usr/bin/env python3
"""Selected original explicit-ID nodes; recording read ports, not HTTP."""
import ast
import copy
from enum import Enum
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s11-id-source-preparation'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    """Harness misuse is not an ordinary source exception."""


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def forbidden_imports():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'qbittorrentapi',
              'transmission_rpc', 'transmissionrpc', 'pydantic', 'cn2an', 'zhconv')
    names = [n for n in sys.modules if any(n == p or n.startswith(p + '.') for p in denied)]
    if names:
        raise CollectorBoundary('forbidden imports: ' + ','.join(names))


def ordinary(value):
    if isinstance(value, Enum):
        return value.value
    if value is None or type(value) in (str, bool, int):
        return value
    if type(value) is list:
        return [ordinary(v) for v in value]
    if type(value) is dict and all(type(k) is str for k in value):
        return {k: ordinary(v) for k, v in value.items()}
    raise CollectorBoundary('outside finite JSON: ' + type(value).__name__)


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

    def info(self, message):
        self.trace.append({'event': 'log', 'level': 'INFO', 'message': str(message)})

    def warn(self, message):
        self.trace.append({'event': 'log', 'level': 'WARN', 'message': str(message)})

    def error(self, message):
        self.trace.append({'event': 'log', 'level': 'ERROR', 'message': str(message)})


class RecordingReads:
    def __init__(self, steps, trace):
        self.steps, self.trace, self.calls = copy.deepcopy(steps), trace, 0

    def get_info(self, *, mtype, tmdbid):
        if self.calls >= len(self.steps):
            raise CollectorBoundary('extra dependency read')
        step = self.steps[self.calls]
        self.calls += 1
        self.trace.append({'event': 'recording_read', 'method': 'get_info',
                           'ordinal': self.calls, 'kwargs': ordinary({'mtype': mtype, 'tmdbid': tmdbid}),
                           'python_types': {'mtype': type(mtype).__name__, 'tmdbid': type(tmdbid).__name__}})
        if set(step) == {'raises'} and step['raises'] == {'class': 'RuntimeError', 'message': 'owned read failure'}:
            self.trace.append({'event': 'recording_exception', **step['raises']})
            raise RuntimeError(step['raises']['message'])
        if set(step) != {'reply'}:
            raise CollectorBoundary('unselected dependency response')
        value = ordinary(copy.deepcopy(step['reply']))
        self.trace.append({'event': 'recording_return', 'value': value})
        return value

    def __getattr__(self, name):
        raise CollectorBoundary('unavailable dependency: ' + name)


def selected_classes(bindings, logger):
    namespace = {'__name__': '__owned_original_s11_id__', 'Enum': Enum, 'logger': logger}
    observed = []
    for bound in bindings:
        raw = Path(bound['path']).read_bytes()
        if sha(raw) != bound['sha256']:
            raise CollectorBoundary('source changed')
        original = next(n for n in ast.parse(raw, filename=bound['path']).body
                        if isinstance(n, ast.ClassDef) and n.name == bound['class'])
        if sha(ast.dump(original, include_attributes=True).encode()) != bound['complete_class_ast_sha256']:
            raise CollectorBoundary('original class drift')
        if original.decorator_list or original.keywords:
            raise CollectorBoundary('unexpected class decorator/metaclass')
        chosen = []
        for expected in bound['nodes']:
            node = original if expected['name'] == bound['class'] else next(
                n for n in original.body if isinstance(n, ast.FunctionDef) and n.name == expected['name'])
            decorators = [ast.unparse(n) for n in node.decorator_list]
            first = min([node.lineno] + [n.lineno for n in node.decorator_list])
            segment = b''.join(raw.splitlines(keepends=True)[first - 1:node.end_lineno])
            actual = {'name': expected['name'], 'first_line': first, 'last_line': node.end_lineno,
                      'source_bytes_sha256': sha(segment),
                      'ast_sha256': sha(ast.dump(node, include_attributes=True).encode()),
                      'decorators': decorators}
            if actual != expected or any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(node)):
                raise CollectorBoundary('selected complete node drift/import')
            if any(d not in ('staticmethod', 'classmethod') for d in decorators):
                raise CollectorBoundary('unselected decorator')
            chosen.append(copy.deepcopy(node))
            observed.append({'class': bound['class'], **actual})
        if bound['class'] == 'MediaType':
            if len(chosen) != 1 or [ast.unparse(n) for n in original.bases] != ['Enum']:
                raise CollectorBoundary('not the complete original MediaType enum')
            extracted = chosen[0]
        else:
            expected_bases = ['_ModuleBase'] if bound['class'] == 'TheMovieDbModule' else []
            if [ast.unparse(n) for n in original.bases] != expected_bases:
                raise CollectorBoundary('unexpected original base')
            # The class shell is a declared extraction boundary. Complete method
            # nodes/descriptors stay original; no _ModuleBase or ctor executes.
            extracted = ast.ClassDef(name=original.name, bases=[], keywords=[],
                                     body=chosen, decorator_list=[])
        future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
        unit = ast.fix_missing_locations(ast.Module(body=[future, extracted], type_ignores=[]))
        exec(compile(unit, bound['path'], 'exec'), namespace)
    return namespace, observed


def run_case(case, bindings):
    if set(case) != {'id', 'operation', 'args', 'steps', 'purpose'} or case['args'].get('meta', 'missing') is not None:
        raise CollectorBoundary('case shape or meta outside initial ID slice')
    trace = []
    ns, observed = selected_classes(bindings, RecordingLogger(trace))
    reads = RecordingReads(case['steps'], trace)
    receiver = object.__new__(ns['TheMovieDbModule'])
    receiver.tmdb = reads
    arguments = ordinary(copy.deepcopy(case['args']))
    if 'mtype' in arguments:
        key = arguments['mtype']
        if key is not None:
            if key not in ('MOVIE', 'TV'):
                raise CollectorBoundary('unselected media type')
            arguments['mtype'] = ns['MediaType'][key]
    try:
        if case['operation'] == 'validate' and set(arguments) <= {'meta', 'tmdbid', 'source'}:
            value = receiver._validate_recognize_params(**arguments)
        elif case['operation'] == 'get_info' and set(arguments) == {'meta', 'tmdbid', 'mtype'}:
            value = receiver._get_info_by_tmdbid(**arguments)
        elif case['operation'] == 'report_payload' and set(arguments) == {'meta', 'mediainfo', 'keyword_meta'}:
            value = ns['MoviePilotServerHelper']._build_recognize_report_payload(**arguments)
        else:
            raise CollectorBoundary('unselected operation/arguments')
    except Exception as error:
        outcome = {'kind': 'uncaught_exception', 'class': type(error).__name__, 'message': str(error)}
    else:
        outcome = {'kind': 'return', 'value': ordinary(value), 'python_type': type(value).__name__}
    if reads.calls != len(case['steps']):
        raise CollectorBoundary('unused fixed dependency replies')
    forbidden_imports()
    return {'id': case['id'], 'input': case, 'outcome': outcome, 'trace': trace,
            'recording_read_count': reads.calls}, observed


def main():
    forbidden_imports()
    manifest, before = load_inputs()
    nodes = json.loads((AUDIT / 'source-nodes.json').read_bytes())['classes']
    payload = json.loads((AUDIT / 'owned-cases.json').read_bytes())
    cases = payload['cases']
    if len(cases) != manifest['case_count'] or len({c['id'] for c in cases}) != len(cases):
        raise CollectorBoundary('case count/identity drift')
    results, observed = [], None
    for case in cases:
        result, current = run_case(case, nodes)
        if observed is not None and observed != current:
            raise CollectorBoundary('nodes changed between cases')
        observed = current
        results.append(result)
    forbidden_imports()
    _, after = load_inputs()
    if before != after:
        raise CollectorBoundary('input manifest changed')
    print(json.dumps({'status': 'ORIGINAL_S11_ID_RECORDING_OBSERVATION_COMPLETE',
                      'source_commit': manifest['source_commit'], 'input_manifest_sha256': before,
                      'python': {'executable': sys.executable, 'version': sys.version,
                                 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                                 'dont_write_bytecode': sys.flags.dont_write_bytecode},
                      'case_count': len(results), 'cases': results, 'source_nodes': observed,
                      'boundaries': payload['boundaries'], 'forbidden_imports_absent': True,
                      'input_drift': []}, ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == '__main__':
    main()
