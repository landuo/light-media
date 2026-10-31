#!/usr/bin/env python3
"""Selected original source algorithm with explicitly recording dependencies."""
import ast
import base64
import copy
import hashlib
import json
import re
import sys
from enum import Enum
from pathlib import Path
from types import SimpleNamespace
from typing import Dict, Optional, Tuple, Union
from urllib.parse import urlencode, urljoin, urlparse

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s12-material-source-preparation'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def check_inputs():
    for line in (AUDIT / 'collector-inputs.sha256').read_text().splitlines():
        expected, filename = line.split('  ', 1)
        assert Path(filename).is_file() and sha(filename) == expected, filename


def restricted_absent():
    assert not any(n == 'requests' or n.startswith('requests.') or
                   n == 'urllib3' or n.startswith('urllib3.') or
                   n == 'socket' or n == '_socket' for n in sys.modules)


check_inputs()
restricted_absent()
SELECTED = []


def record_node(relative, raw, node):
    first = min([node.lineno] + [d.lineno for d in getattr(node, 'decorator_list', [])])
    fragment = b''.join(raw.splitlines(keepends=True)[first - 1:node.end_lineno])
    SELECTED.append({'path': relative, 'name': node.name, 'first_line': first,
                     'last_line': node.end_lineno, 'file_sha256': hashlib.sha256(raw).hexdigest(),
                     'source_fragment_sha256': hashlib.sha256(fragment).hexdigest(),
                     'ast_sha256': hashlib.sha256(ast.dump(node, include_attributes=True).encode()).hexdigest()})


def execute_selected(nodes, filename):
    module = ast.Module(body=nodes, type_ignores=[])
    assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(module))
    exec(compile(ast.fix_missing_locations(module), str(SOURCE / filename), 'exec', dont_inherit=True), globals())


enum_relative = 'app/schemas/types.py'
enum_bytes = (SOURCE / enum_relative).read_bytes()
enum_tree = ast.parse(enum_bytes, filename=str(SOURCE / enum_relative))
enum_nodes = [n for n in enum_tree.body if isinstance(n, ast.ClassDef) and
              n.name in ('MessageChannel', 'NotificationType')]
assert {n.name for n in enum_nodes} == {'MessageChannel', 'NotificationType'}
for node in enum_nodes:
    record_node(enum_relative, enum_bytes, node)
execute_selected(enum_nodes, enum_relative)


def normal(value):
    if isinstance(value, bytes):
        return {'$bytes': base64.b64encode(value).decode('ascii')}
    if isinstance(value, Enum):
        return {'$enum': type(value).__name__ + '.' + value.name, 'value': value.value}
    if isinstance(value, (tuple, list)):
        return [normal(item) for item in value]
    if isinstance(value, dict):
        return {key: normal(item) for key, item in value.items()}
    assert value is None or isinstance(value, (str, bool, int, float)), type(value)
    return value


ACTIVE = None
TRACE = None
NOTICES = None


def record(op, **fields):
    TRACE.append({'op': op, **normal(copy.deepcopy(fields))})


class RecordingResponse:
    """Owned response inputs, NOT a requests Response or real HTTP result."""
    def __init__(self, fields):
        self.fields = fields

    def __bool__(self):
        result = self.fields['truthy']
        record('response.bool', value=result)
        return result

    @property
    def text(self):
        record('response.text', value=self.fields['text'])
        return self.fields['text']

    def json(self):
        record('response.json', value=self.fields['json'])
        return copy.deepcopy(self.fields['json'])


class RecordingRequestUtils:
    """Record the source's constructor and method arguments; perform no HTTP."""
    def __init__(self, **kwargs):
        record('request.init', **kwargs)

    def get_res(self, url, **kwargs):
        record('request.get_res', url=url, **kwargs)
        return RecordingResponse(ACTIVE['response'])

    def post_res(self, url, **kwargs):
        record('request.post_res', url=url, **kwargs)
        return RecordingResponse(ACTIVE['response'])


class RecordingTorrentHelper:
    """Explicit helper result input; does not parse/download/cache anything."""
    def __init__(self):
        record('torrent_helper.init')

    def download_torrent(self, **kwargs):
        record('torrent_helper.download_torrent', **kwargs)
        fields = copy.deepcopy(ACTIVE['helper_result'])
        content = fields['content']
        if isinstance(content, dict):
            assert set(content) == {'$bytes'}
            content = base64.b64decode(content['$bytes'], validate=True)
        return fields['cache_path'], content, fields['folder'], fields['files'], fields['error']


class RecordingNotification:
    """Record original constructor kwargs; no source Pydantic validation claimed."""
    def __init__(self, **kwargs):
        self.fields = kwargs
        record('notification.init', **kwargs)


class LogSink:
    def info(self, message):
        record('log.info', message=message)

    def error(self, message):
        record('log.error', message=message)


def record_notice(notice):
    assert isinstance(notice, RecordingNotification)
    record('notice.post_message', **notice.fields)
    NOTICES.append(normal(copy.deepcopy(notice.fields)))


# Explicit component dependency injection, not fake imports or sys.modules entries.
RequestUtils = RecordingRequestUtils
TorrentHelper = RecordingTorrentHelper
Notification = RecordingNotification
TorrentInfo = SimpleNamespace  # Input attributes only; no source model validation.
logger = LogSink()
relative = 'app/chain/download.py'
source_bytes = (SOURCE / relative).read_bytes()
tree = ast.parse(source_bytes, filename=str(SOURCE / relative))
original_class = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'DownloadChain')
names = {'download_torrent', '_normalize_indirect_download_url'}
methods = [n for n in original_class.body if isinstance(n, ast.FunctionDef) and n.name in names]
assert {n.name for n in methods} == names
method_asts = [ast.dump(n, include_attributes=True) for n in methods]
for node in methods:
    record_node(relative, source_bytes, node)
# Preserve both complete methods, including the complete inner redirect function.
# Only the unused ChainBase/class envelope is excluded from this component run.
envelope = ast.ClassDef(name='SelectedDownloadChain', bases=[], keywords=[],
                        body=methods, decorator_list=[], type_params=[])
execute_selected([envelope], relative)
assert [ast.dump(n, include_attributes=True) for n in methods] == method_asts

document = json.loads((AUDIT / 'owned-cases.json').read_text())
assert len(document['cases']) == 19
settings = SimpleNamespace(**document['settings'])
results = []
for case in document['cases']:
    ACTIVE, TRACE, NOTICES = case, [], []
    chain = SelectedDownloadChain()
    chain.post_message = record_notice
    torrent = TorrentInfo(**case['torrent'])
    channel = None if case['channel'] is None else MessageChannel[case['channel']]
    try:
        value = chain.download_torrent(torrent=torrent, channel=channel,
                                       source=case['source'], userid=case['userid'])
    except Exception as error:
        outcome = {'exception': {'module': type(error).__module__, 'type': type(error).__name__,
                                  'message': str(error), 'args': normal(error.args)}}
    else:
        outcome = {'value': normal(value), 'return_type': type(value).__name__,
                   'element_types': [type(item).__name__ for item in value]}
    results.append({'case': case['case'], 'inputs': copy.deepcopy(case),
                    'outcome': outcome, 'trace': TRACE, 'notices': NOTICES})

restricted_absent()
check_inputs()
print(json.dumps({
    'status': 'actual_selected_source_component_with_recording_dependencies',
    'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
    'driver_sha256': sha(__file__), 'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256'),
    'python_executable': sys.executable, 'python_executable_sha256': sha(sys.executable),
    'settings_input': document['settings'], 'cases': results, 'selected_original_nodes': SELECTED,
    'requests_urllib3_socket_absent_at_checks': True,
    'dependency_contract': document['dependency_contract'],
    'not_claimed': ['real RequestUtils/requests/HTTP/proxy/cookie transmission', 'real TorrentHelper/parser/cache',
                    'real Notification model or delivery', 'whole DownloadChain/DAO/downloader',
                    'target Go output or complete equivalence', 'unobserved internal syscall behavior'],
}, ensure_ascii=False, indent=2, allow_nan=False))
