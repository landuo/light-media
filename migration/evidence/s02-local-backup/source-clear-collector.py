#!/usr/bin/env python3
"""Original complete clear AST; only recording in-memory Path/clock ports."""
import ast
import datetime as std_datetime
import hashlib
import json
import sys
import time
from pathlib import Path, PurePosixPath
from types import SimpleNamespace

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
AUDIT = ROOT / 'audits/s02-source-clear-preparation'
SOURCE = Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot/app/utils/system.py')
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def restricted_absent():
    forbidden = ('app', 'requests', 'urllib3', 'socket', '_socket', 'torrentool')
    assert not any(name == prefix or name.startswith(prefix + '.')
                   for name in sys.modules for prefix in forbidden)


def check_inputs():
    for row in json.loads((AUDIT / 'input-bindings.json').read_text())['inputs']:
        raw = Path(row['path']).read_bytes()
        assert len(raw) == row['bytes'] and digest(raw) == row['sha256'], row['path']


class RecordingTree:
    """Owned dependency input. No Path filesystem mutator is called."""

    def __init__(self, case, fixed_now):
        self.root_exists = case['root_exists']
        self.trace = []
        self.nodes = {}
        for original in case['nodes']:
            node = dict(original)
            name = node.pop('path')
            assert name and not name.startswith('/') and '..' not in name.split('/')
            assert name not in self.nodes and node['kind'] in ('file', 'dir')
            if node['kind'] == 'file':
                mtime = fixed_now - std_datetime.timedelta(seconds=node['age_seconds'])
                node['mtime'] = mtime.timestamp()
                assert std_datetime.datetime.fromtimestamp(node['mtime']) == mtime
            self.nodes[name] = node
        for name in self.nodes:
            parent = str(PurePosixPath(name).parent)
            assert parent == '.' or self.nodes[parent]['kind'] == 'dir'
        assert self.root_exists or not self.nodes

    def record(self, operation, path, **detail):
        self.trace.append({'operation': operation, 'path': path, **detail})

    def snapshot(self):
        return [{'path': name, **node} for name, node in self.nodes.items()]


class RecordingPath:
    """The entire input tree is virtual; names are not local OS paths."""

    def __init__(self, tree, name='.'):
        self.tree, self.name = tree, name

    def __lt__(self, other):
        assert self.tree is other.tree
        return PurePosixPath(self.name).parts < PurePosixPath(other.name).parts

    def exists(self):
        result = self.tree.root_exists if self.name == '.' else self.name in self.tree.nodes
        self.tree.record('exists', self.name, result=result)
        return result

    def rglob(self, pattern):
        assert self.name == '.' and pattern == '*'
        # Explicit dependency order: insertion order of the remaining owned nodes.
        # This is not claimed to reproduce any particular filesystem enumeration.
        names = list(self.tree.nodes)
        self.tree.record('rglob', self.name, pattern=pattern, result=names)
        return iter([RecordingPath(self.tree, name) for name in names])

    def is_file(self):
        result = self.tree.nodes[self.name]['kind'] == 'file'
        self.tree.record('is_file', self.name, result=result)
        return result

    def stat(self):
        mtime = self.tree.nodes[self.name]['mtime']
        self.tree.record('stat', self.name, st_mtime=mtime)
        return SimpleNamespace(st_mtime=mtime)

    def unlink(self):
        node = self.tree.nodes[self.name]
        assert node['kind'] == 'file'
        if node.get('unlink_error'):
            self.tree.record('unlink', self.name, result='injected_error')
            raise PermissionError(node['unlink_error'])
        self.tree.record('unlink', self.name, result='memory_only')
        del self.tree.nodes[self.name]

    def is_dir(self):
        result = self.tree.nodes[self.name]['kind'] == 'dir'
        self.tree.record('is_dir', self.name, result=result)
        return result

    def iterdir(self):
        names = [name for name in self.tree.nodes
                 if str(PurePosixPath(name).parent) == self.name]
        self.tree.record('iterdir', self.name, result=names)
        return iter([RecordingPath(self.tree, name) for name in names])

    def rmdir(self):
        assert self.tree.nodes[self.name]['kind'] == 'dir'
        assert not any(str(PurePosixPath(name).parent) == self.name
                       for name in self.tree.nodes)
        self.tree.record('rmdir', self.name, result='memory_only')
        del self.tree.nodes[self.name]


def main():
    check_inputs()
    restricted_absent()
    source_bytes = SOURCE.read_bytes()
    module = ast.parse(source_bytes, filename=str(SOURCE))
    cls, = [n for n in module.body if isinstance(n, ast.ClassDef) and n.name == 'SystemUtils']
    method, = [n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == 'clear']
    assert not any(isinstance(n, (ast.Import, ast.ImportFrom)) for n in ast.walk(method))
    start = min([method.lineno] + [n.lineno for n in method.decorator_list])
    fragment = b''.join(source_bytes.splitlines(keepends=True)[start - 1:method.end_lineno])
    selected = {
        'symbol': 'SystemUtils.clear', 'start_line': start, 'end_line': method.end_lineno,
        'source_bytes_sha256': digest(fragment),
        'ast_no_attributes_sha256': digest(ast.dump(method, include_attributes=False).encode()),
    }
    assert selected == json.loads((AUDIT / 'selected-node.json').read_text())
    assert fragment == (AUDIT / 'SystemUtils.clear.py').read_bytes()
    # Only the class envelope is synthetic; the complete original FunctionDef,
    # decorator, annotations, branches and both loops are unchanged.
    wrapper = ast.ClassDef(name='SystemUtils', bases=[], keywords=[], body=[method],
                           decorator_list=[], type_params=[])
    code = compile(ast.fix_missing_locations(ast.Module(body=[wrapper], type_ignores=[])),
                   str(SOURCE), 'exec', dont_inherit=True)
    cases = json.loads((AUDIT / 'owned-cases.json').read_text())
    results = []
    for case in cases:
        fixed_now = std_datetime.datetime.fromisoformat(case['now_local'])
        tree = RecordingTree(case, fixed_now)

        class ControlledDateTime:
            @staticmethod
            def now():
                tree.record('datetime.now', '.', result=fixed_now.isoformat())
                return fixed_now

            @staticmethod
            def fromtimestamp(timestamp):
                value = std_datetime.datetime.fromtimestamp(timestamp)
                tree.record('datetime.fromtimestamp', '.', timestamp=timestamp,
                            result=value.isoformat())
                return value

        namespace = {'__name__': 'owned_selected_clear', 'Path': RecordingPath,
                     'datetime': SimpleNamespace(datetime=ControlledDateTime)}
        exec(code, namespace)
        before = tree.snapshot()
        returned, error = None, None
        try:
            returned = namespace['SystemUtils'].clear(RecordingPath(tree), case['days'])
        except Exception as exc:
            # Outside the original method: captures whether its exception escaped.
            error = {'class': type(exc).__name__, 'message': str(exc)}
        results.append({'id': case['id'], 'input': case, 'before': before,
                        'return': returned, 'exception': error, 'trace': tree.trace,
                        'after': tree.snapshot(), 'root_exists_after': tree.root_exists})
    restricted_absent()
    check_inputs()
    print(json.dumps({
        'layer': 'complete_original_AST_with_recording_path_and_controlled_clock_ports',
        'source_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
        'python': sys.version, 'timezone': {'tzname': time.tzname, 'timezone': time.timezone},
        'real_filesystem_deletions': 0,
        'clock': 'fixed local-naive input; real stdlib datetime arithmetic/fromtimestamp; no module patch',
        'limits': ['not actual OS deletion', 'not whole startup cleanup',
                   'not filesystem permissions/inodes/symlink probes',
                   'not torrent parsing or fastresume reconstruction'],
        'selected_node': selected, 'cases': results,
    }, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
