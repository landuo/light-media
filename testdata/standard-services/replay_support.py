"""Shared, development-only replay validation; no production dependency."""
import argparse
import hashlib
import importlib.metadata
import json
import platform
import socket
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
COMMIT = '8a83efeacd3e26b82be495e17d50146e31c96a60'


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def prepare(source=False):
    parser = argparse.ArgumentParser(description=__doc__)
    if source:
        parser.add_argument('--source-root', required=True, type=Path)
    parser.add_argument('--dependency-root', required=True, action='append', type=Path,
                        help='Installed package directory; repeat in import priority order')
    parser.add_argument('--output', required=True, type=Path,
                        help='New output directory (refuses to overwrite evidence)')
    args = parser.parse_args()
    args.dependency_root = [p.resolve(strict=True) for p in args.dependency_root]
    args.output = args.output.resolve()
    if source:
        args.source_root = args.source_root.resolve(strict=True)
        actual = subprocess.check_output(['git', '-C', str(args.source_root), 'rev-parse', 'HEAD'], text=True).strip()
        if actual != COMMIT:
            raise RuntimeError('source checkout does not match fixed commit')
        subprocess.run(['git', '-C', str(args.source_root), 'diff', '--quiet', COMMIT, '--'], check=True)
        bindings = json.loads((HERE / 'whole-source-bindings.json').read_text())
        if bindings['source_commit'] != COMMIT:
            raise RuntimeError('source binding commit mismatch')
        for name, expected in bindings['files'].items():
            if digest(args.source_root / name) != expected:
                raise RuntimeError('fixed source file changed: ' + name)
    sys.dont_write_bytecode = True
    sys.path[:0] = [str(p) for p in args.dependency_root] + ([str(args.source_root)] if source else [])
    required = {'APScheduler': '3.11.2'}
    if source:
        for line in (HERE / 'replay-requirements.txt').read_text().splitlines():
            if line and not line.startswith('#'):
                name, version = line.split('==')
                required[name] = version
    for name, expected in required.items():
        if importlib.metadata.version(name) != expected:
            raise RuntimeError('actual dependency version differs: ' + name)
    args.output.mkdir(parents=True, exist_ok=False)
    return args


def prohibit_network():
    attempts = []
    def deny(*args, **kwargs):
        attempts.append('blocked')
        raise RuntimeError('owned replay forbids network')
    socket.socket.connect = deny
    socket.socket.connect_ex = deny
    socket.create_connection = deny
    return attempts, deny


def provenance(args):
    # Labels remain portable; do not disclose machine-local dependency roots.
    roots = [('SOURCE', args.source_root)] if hasattr(args, 'source_root') else []
    roots += [(f'DEPENDENCY_{i}', p) for i, p in enumerate(args.dependency_root)]
    roots += [('FIXTURE', HERE), ('PYTHON', Path(sys.base_prefix).resolve())]
    imports = {}
    for name, module in sorted(sys.modules.copy().items()):
        file = getattr(module, '__file__', None)
        if not file or not Path(file).is_file():
            continue
        file = Path(file).resolve()
        if hasattr(args, 'source_root') and (name == 'version' or name == 'app' or name.startswith('app.')):
            if not file.is_relative_to(args.source_root):
                raise RuntimeError('source import shadowed by an external package: ' + name)
        for label, root in roots:
            if file.is_relative_to(root):
                imports[label + '/' + file.relative_to(root).as_posix()] = digest(file)
                break
        else:
            # Ambient third-party imports must not silently evade dependency binding.
            raise RuntimeError('import outside explicit source/dependency/Python roots: ' + name)
    from zoneinfo import TZPATH
    zones = {}
    for zone in ['UTC', 'America/New_York', 'Europe/London', 'Asia/Shanghai', 'Australia/Lord_Howe']:
        for root in TZPATH:
            path = Path(root) / zone
            if path.is_file():
                zones[zone] = digest(path)
                break
    return {'python': platform.python_version(), 'platform': platform.platform(),
            'dependency_precedence': [f'DEPENDENCY_{i}' for i in range(len(args.dependency_root))],
            'imports': dict(sorted(imports.items())), 'system_zoneinfo_sha256': zones}


def imported_versions():
    # Check executing packages, not just a dist-info record elsewhere on sys.path.
    import apscheduler
    import cachetools
    import pydantic
    import sqlalchemy
    actual = {name: module.__version__ for name, module in [
        ('apscheduler', apscheduler), ('cachetools', cachetools),
        ('pydantic', pydantic), ('sqlalchemy', sqlalchemy)]}
    expected = {'apscheduler': '3.11.2', 'cachetools': '7.1.8',
                'pydantic': '2.14.2', 'sqlalchemy': '2.0.55'}
    if actual != expected:
        raise RuntimeError('executing dependency versions differ: ' + repr(actual))
    return actual


def normalized_manual(observations):
    results = {}
    for item in observations:
        if item['case'].startswith('manual_'):
            progress = json.loads(json.dumps(item['progress']))
            for fields in [progress, progress['data']]:
                fields.pop('started_at')
                fields.pop('finished_at')
            results[item['case']] = progress
    return results
