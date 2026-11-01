#!/usr/bin/env python3
"""执行完整原缓存装饰器与有限响应节点；依赖为明确 recording port，不是 HTTP/TTL 验收。"""
import ast
import contextvars
from contextlib import contextmanager
from copy import deepcopy
from datetime import datetime as _RealDateTime
from functools import wraps
import hashlib
import inspect
import json
from pathlib import Path
import sys

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s11-cache-source-preparation'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    """防止未批准的依赖路径被原普通异常处理吞掉。"""


def _sha(raw):
    return hashlib.sha256(raw).hexdigest()


def _ordinary(value):
    if value is None or type(value) in (str, bool, int):
        return value
    if type(value) in (list, tuple):
        return [_ordinary(item) for item in value]
    if type(value) is dict and all(type(key) is str for key in value):
        return {key: _ordinary(item) for key, item in value.items()}
    raise CollectorBoundary('non-owned JSON input/output: ' + type(value).__name__)


def _guard():
    denied = ('app', 'requests', 'urllib3', 'socket', '_socket', 'httpx', 'aiohttp',
              'aiofiles', 'aioshutil', 'redis', 'cachetools', 'pydantic')
    imported = [name for name in sys.modules
                if any(name == prefix or name.startswith(prefix + '.') for prefix in denied)]
    if imported:
        raise CollectorBoundary('forbidden import: ' + ','.join(imported))


def _load_inputs():
    raw = (AUDIT / 'input-bindings.json').read_bytes()
    binding = json.loads(raw)
    for item in binding['inputs']:
        body = Path(item['path']).read_bytes()
        if len(body) != item['bytes'] or _sha(body) != item['sha256']:
            raise CollectorBoundary('input drift: ' + item['path'])
    return binding, _sha(raw)


class _PortObject:
    def __init__(self, **fields):
        self.__dict__.update(fields)

    def __getattr__(self, name):
        raise CollectorBoundary('unavailable fixed port field: ' + name)


class _RecordingLogger:
    def __init__(self, trace):
        self._trace = trace

    def debug(self, message):
        """记录原 DEBUG 消息。"""
        self._trace.append({'event': 'log', 'level': 'DEBUG', 'message': str(message)})

    def warning(self, message):
        """记录原 WARNING 消息。"""
        self._trace.append({'event': 'log', 'level': 'WARNING', 'message': str(message)})


class _RecordingCache:
    def __init__(self, trace, **config):
        self._trace, self._data = trace, {}
        trace.append({'event': 'recording_cache_factory', 'config': _ordinary(config)})

    def get(self, key, *, region):
        """记录读操作；仅提供无 TTL 的测试字典端口。"""
        value = self._data.get((region, key))
        self._trace.append({'event': 'recording_cache_get', 'key': key, 'region': region,
                            'value': deepcopy(_ordinary(value))})
        return value

    def set(self, key, value, **options):
        """记录原装饰器写操作；不模拟 TTL 或淘汰。"""
        self._trace.append({'event': 'recording_cache_set', 'key': key,
                            'value': deepcopy(_ordinary(value)), 'options': _ordinary(options)})
        self._data[(options['region'], key)] = value

    def exists(self, *, key, region):
        """仅提供原装饰器所需的存在性端口。"""
        self._trace.append({'event': 'recording_cache_exists', 'key': key, 'region': region})
        return (region, key) in self._data

    def clear(self, *, region):
        """只清理当前场景的自有字典。"""
        raise CollectorBoundary('cache_clear not selected')

    def delete(self, key, *, region):
        """禁止范围外的原装饰器删除路径。"""
        raise CollectorBoundary('cache_delete not selected')


class _CalendarPort:
    def __init__(self, trace):
        self.day = '20261101'
        self._trace = trace

    def now(self):
        """提供固定本地日期；不读取或改写系统时区。"""
        self._trace.append({'event': 'recording_local_day', 'day': self.day})
        return _RealDateTime(int(self.day[:4]), int(self.day[4:6]), int(self.day[6:8]), 12)

    def strftime(self, value, fmt):
        """保留标准库原日期格式化行为。"""
        return _RealDateTime.strftime(value, fmt)


class _NoRateLimitTime:
    def time(self):
        """范围外限流分支必须停止。"""
        raise CollectorBoundary('rate-limit clock not selected')

    def sleep(self, seconds):
        """禁止限流等待或重入。"""
        raise CollectorBoundary('rate-limit sleep not selected')


class _ResponsePort:
    def __init__(self, step, trace):
        self._step, self._trace = step, trace
        self.headers = deepcopy(step.get('headers', {'Content-Type': 'application/json'}))
        self.status_code = step.get('status', 200)
        self.text = step.get('text', '')

    def json(self):
        """提供固定 JSON 或固定 JSON 解析异常。"""
        self._trace.append({'event': 'recording_response_json'})
        if self._step.get('json_error'):
            raise ValueError('owned malformed JSON')
        return deepcopy(self._step['json'])


class _RecordingReads:
    def __init__(self, steps, trace):
        self._steps, self._trace, self.count = deepcopy(steps), trace, 0

    def _read(self, method, url, data, payload):
        if url != 'https://owned.invalid/3/movie/900011?api_key=owned-synthetic-key&&language=en-US':
            raise CollectorBoundary('non-fixed logical URL')
        if data is not None or payload is not None or self.count >= len(self._steps):
            raise CollectorBoundary('unselected read input/count')
        step = self._steps[self.count]
        self.count += 1
        self._trace.append({'event': 'recording_read', 'ordinal': self.count, 'method': method,
                            'url': url, 'data': data, 'json': payload})
        if set(step) == {'none'} and step['none'] is True:
            return None
        if set(step) == {'raises'} and step['raises'] == 'RuntimeError':
            raise RuntimeError('owned read failure')
        if 'json' not in step and not step.get('json_error'):
            raise CollectorBoundary('unselected response')
        return _ResponsePort(step, self._trace)

    def get_res(self, url, params, json):
        """记录 GET 读取，绝不发送网络请求。"""
        return self._read('GET', url, params, json)

    def post_res(self, url, data, json):
        """本片禁止 POST 扩围。"""
        raise CollectorBoundary('POST not selected')


class _SessionPort:
    def __init__(self, trace, ordinal):
        self._trace, self._ordinal = trace, ordinal

    def close(self):
        """记录原自有会话重建前的关闭操作。"""
        self._trace.append({'event': 'recording_session_close', 'ordinal': self._ordinal})


def _selected_module(binding, namespace):
    raw = Path(binding['path']).read_bytes()
    if _sha(raw) != binding['sha256']:
        raise CollectorBoundary('source hash drift')
    module = ast.parse(raw, filename=binding['path'])
    original = module if binding['class'] is None else next(
        node for node in module.body if isinstance(node, ast.ClassDef) and node.name == binding['class'])
    chosen = []
    for item in binding['nodes']:
        node = next(node for node in original.body if node.lineno == item['definition_line'])
        decorators = [ast.unparse(value) for value in getattr(node, 'decorator_list', [])]
        first = min([node.lineno] + [value.lineno for value in getattr(node, 'decorator_list', [])])
        segment = b''.join(raw.splitlines(keepends=True)[first - 1:node.end_lineno])
        if (_sha(ast.dump(node, include_attributes=True).encode()) != item['ast_sha256']
                or _sha(segment) != item['source_bytes_sha256'] or decorators != item['decorators']
                or any(isinstance(part, (ast.Import, ast.ImportFrom)) for part in ast.walk(node))):
            raise CollectorBoundary('complete selected node drift/import')
        chosen.append(deepcopy(node))
    if binding['class']:
        if [ast.unparse(base) for base in original.bases] != binding['original_bases']:
            raise CollectorBoundary('original class bases drift')
        if original.keywords or original.decorator_list:
            raise CollectorBoundary('unselected class metadata')
        # Original complete selected methods and descriptors are retained; only
        # unrelated original class statements and constructors are omitted.
        body = [ast.ClassDef(name=original.name, bases=deepcopy(original.bases),
                            keywords=[], body=chosen, decorator_list=[])]
    else:
        body = chosen
    future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    unit = ast.fix_missing_locations(ast.Module(body=[future, *body], type_ignores=[]))
    exec(compile(unit, binding['path'], 'exec'), namespace)


def _build(nodes, trace, steps, owns_session):
    calendar = _CalendarPort(trace)
    def cache_factory(**options):
        return _RecordingCache(trace, **options)
    def hashkey_port(*keys):
        values = _ordinary(keys)
        trace.append({'event': 'recording_hashkey_arguments', 'ordered_values': deepcopy(values)})
        # This intentionally is NOT cachetools.hashkey. Its finite deterministic
        # token only joins repeated calls in the recording backend.
        return 'recording-only:' + _sha(json.dumps(values, ensure_ascii=False, separators=(',', ':')).encode())
    common = {'contextvars': contextvars, 'contextmanager': contextmanager,
              'inspect': inspect, 'wraps': wraps, 'deepcopy': deepcopy,
              'Cache': cache_factory, 'hashkey': hashkey_port,
              'settings': _PortObject(CONF=_PortObject(tmdb=256, meta=86400)),
              'logger': _RecordingLogger(trace), 'datetime': calendar, 'time': _NoRateLimitTime()}
    cache_ns = {'__name__': 'app.core.cache', **common}
    _selected_module(nodes[0], cache_ns)
    error_ns = {'__name__': 'app.modules.themoviedb.tmdbv3api.exceptions'}
    _selected_module(nodes[1], error_ns)
    tmdb_ns = {'__name__': 'app.modules.themoviedb.tmdbv3api.tmdb', **common,
               'cached': cache_ns['cached'], 'fresh': cache_ns['fresh'],
               'TMDbException': error_ns['TMDbException']}
    _selected_module(nodes[2], tmdb_ns)
    receiver = object.__new__(tmdb_ns['TMDb'])
    receiver._api_key, receiver._domain, receiver._language = 'owned-synthetic-key', 'owned.invalid', 'en-US'
    receiver._wait_on_rate_limit = False
    receiver._remaining, receiver._reset = 40, None
    receiver._page = receiver._total_results = receiver._total_pages = None
    receiver._owns_session = owns_session
    receiver._session = _SessionPort(trace, 1)
    reads = _RecordingReads(steps, trace)
    receiver._req = reads
    resets = [0]
    def init_session_port():
        resets[0] += 1
        trace.append({'event': 'recording_init_session', 'ordinal': resets[0]})
        receiver._session = _SessionPort(trace, resets[0] + 1)
        receiver._req = reads
    receiver._init_session = init_session_port
    return receiver, cache_ns, calendar, reads, resets


def _run_case(case, nodes):
    trace = []
    receiver, cache_ns, calendar, reads, resets = _build(nodes, trace, case['steps'], case['owns_session'])
    outcomes, previous = [], None
    for action in case['actions']:
        if action['kind'] == 'mutate_previous':
            if not isinstance(previous, dict):
                raise CollectorBoundary('mutation without dictionary result')
            previous['owned_augmented'] = True
            previous['nested']['items'].append('owned mutation')
            trace.append({'event': 'owned_caller_mutation', 'value': deepcopy(previous)})
            continue
        if action['kind'] == 'fresh_context':
            states = [cache_ns['is_fresh']()]
            try:
                with cache_ns['fresh'](True):
                    states.append(cache_ns['is_fresh']())
                    with cache_ns['fresh'](False):
                        states.append(cache_ns['is_fresh']())
                    states.append(cache_ns['is_fresh']())
                    raise RuntimeError('owned context exit')
            except RuntimeError as error:
                if str(error) != 'owned context exit':
                    raise
            states.append(cache_ns['is_fresh']())
            with cache_ns['fresh'](False):
                states.append(cache_ns['is_fresh']())
            states.append(cache_ns['is_fresh']())
            outcomes.append({'kind': 'fresh_states', 'values': states})
            continue
        if action['kind'] != 'request_obj':
            raise CollectorBoundary('unselected case action')
        calendar.day = action['day']
        trace.append({'event': 'owned_action', 'action': deepcopy(action),
                      'fresh_before': cache_ns['is_fresh']()})
        try:
            with cache_ns['fresh'](action.get('outer_fresh', False)):
                previous = receiver._request_obj('/movie/900011', call_cached=action['call_cached'])
        except Exception as error:
            result = {'kind': 'uncaught_exception', 'class': type(error).__name__, 'message': str(error)}
        else:
            result = {'kind': 'return', 'value': deepcopy(_ordinary(previous)), 'python_type': type(previous).__name__}
        result['fresh_after'] = cache_ns['is_fresh']()
        trace.append({'event': 'owned_outcome', **deepcopy(result)})
        outcomes.append(result)
    if reads.count != len(case['steps']):
        raise CollectorBoundary('unused fixed dependency replies')
    _guard()
    return {'id': case['id'], 'input': case, 'outcomes': outcomes, 'trace': trace,
            'recording_read_count': reads.count, 'recording_session_reset_count': resets[0]}


def main():
    """按冻结输入采集原节点实际输出，不预设 Go 期望。"""
    _guard()
    manifest, before = _load_inputs()
    nodes = json.loads((AUDIT / 'source-nodes.json').read_bytes())['modules']
    payload = json.loads((AUDIT / 'owned-cases.json').read_bytes())
    cases = payload['cases']
    if len(cases) != manifest['case_count'] or len({case['id'] for case in cases}) != len(cases):
        raise CollectorBoundary('case count/identity drift')
    results = [_run_case(case, nodes) for case in cases]
    _guard()
    _, after = _load_inputs()
    if before != after:
        raise CollectorBoundary('manifest drift')
    print(json.dumps({'status': 'ORIGINAL_S11_CACHE_RECORDING_OBSERVATION_COMPLETE',
                      'source_commit': manifest['source_commit'], 'input_manifest_sha256': before,
                      'case_count': len(results), 'cases': results, 'source_nodes': nodes,
                      'python': {'executable': sys.executable, 'version': sys.version,
                                 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site,
                                 'dont_write_bytecode': sys.flags.dont_write_bytecode},
                      'boundaries': payload['boundaries'], 'forbidden_imports_absent': True,
                      'input_drift': []}, ensure_ascii=False, indent=2, allow_nan=False))


if __name__ == '__main__':
    main()
