#!/usr/bin/env python3
"""One owned parser/path observation; prepared for coordinator execution only."""
import ast
import hashlib
import json
import sys
from pathlib import Path
from typing import List, Tuple

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s12-parser-join-source'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot')
VENDOR = ROOT / 'source-runtime/s06-torrentool-source/unpacked'
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
# Import the actual fixed package normally, as in the reviewed S06 collector.
# Remote helpers are definitions only: no remote/file/parser-write APIs are called.
sys.path.insert(0, str(VENDOR))
from torrentool.api import Torrent
restricted_absent()

LOGS = []


class LogSink:
    def debug(self, message):
        LOGS.append(str(message))


logger = LogSink()
source_path = SOURCE / 'app/helper/torrent.py'
source_bytes = source_path.read_bytes()
tree = ast.parse(source_bytes, filename=str(source_path))
original_class = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'TorrentHelper')
methods = [n for n in original_class.body if isinstance(n, ast.FunctionDef) and
           n.name == 'get_fileinfo_from_torrent']
assert len(methods) == 1
method = methods[0]
assert len(method.decorator_list) == 1
assert isinstance(method.decorator_list[0], ast.Name) and method.decorator_list[0].id == 'staticmethod'
original_method_ast = ast.dump(method, include_attributes=True)
first_line = min(method.lineno, method.decorator_list[0].lineno)
fragment = b''.join(source_bytes.splitlines(keepends=True)[first_line - 1:method.end_lineno])
# New envelope only: preserve the complete original static method, decorators,
# annotations and body. Do not evaluate source class metaclass or constructor.
envelope = ast.ClassDef(name='SelectedTorrentHelper', bases=[], keywords=[],
                        body=[method], decorator_list=[], type_params=[])
module = ast.Module(body=[envelope], type_ignores=[])
assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(module))
exec(compile(ast.fix_missing_locations(module), str(source_path), 'exec', dont_inherit=True), globals())
assert ast.dump(method, include_attributes=True) == original_method_ast

fixture = AUDIT / 'owned-join-separator.torrent'
content = fixture.read_bytes()
parsed = Torrent.from_string(content)
raw_files = [{'name': entry.name, 'length': entry.length} for entry in parsed.files]
folder, files = SelectedTorrentHelper.get_fileinfo_from_torrent(parsed)
restricted_absent()
check_inputs()
print(json.dumps({
    'status': 'actual_single_owned_parser_and_selected_static_source_method',
    'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
    'driver_sha256': sha(__file__),
    'input_manifest_sha256': sha(AUDIT / 'collector-inputs.sha256'),
    'python_executable': sys.executable,
    'python_executable_sha256': sha(sys.executable),
    'fixture_sha256': hashlib.sha256(content).hexdigest(),
    'fixture_input': {'name': 'Root', 'files': [{'length': 1, 'path': ['sub/', 'x']}],
                      'piece_length': 16384, 'pieces': '20 zero bytes'},
    'actual_parser_name': parsed.name,
    'actual_parser_files_in_order': raw_files,
    'source_folder': folder,
    'source_files_in_order': files,
    'selected_method': {'path': str(source_path), 'first_line': first_line,
                        'last_line': method.end_lineno,
                        'file_sha256': hashlib.sha256(source_bytes).hexdigest(),
                        'source_fragment_sha256': hashlib.sha256(fragment).hexdigest(),
                        'ast_sha256': hashlib.sha256(original_method_ast.encode()).hexdigest()},
    'imported_vendor_files': [{'module': name, 'path': str(Path(mod.__file__)),
                               'sha256': sha(mod.__file__)}
                              for name, mod in sorted(sys.modules.items())
                              if (name == 'torrentool' or name.startswith('torrentool.')) and
                              getattr(mod, '__file__', None)],
    'requests_urllib3_socket_absent_at_checks': True,
    'logs': LOGS,
    'not_claimed': ['whole TorrentHelper class or constructor', 'whole application',
                    'content-wrapper exception handling', 'Go parser execution/equivalence',
                    'network or filesystem torrent APIs', 'unobserved internal syscall behavior'],
}, ensure_ascii=False, indent=2, allow_nan=False))
