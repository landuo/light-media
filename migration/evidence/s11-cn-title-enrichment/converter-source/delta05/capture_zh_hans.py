#!/usr/bin/env python3
"""Prepared exact-native baseline. Run only after separate coordinator admission."""
import base64
import hashlib
import json
import os
import sys
from pathlib import Path

ROOT = Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
HERE = ROOT / 'audits/s11-zhconv-source-preparation/delta03'
WHEEL_ROOT = ROOT / 'source-runtime/zhconv-rs-0.4.2-official/static-wheel-members'
INIT = WHEEL_ROOT / 'zhconv_rs/__init__.py'
EXTENSION = WHEEL_ROOT / 'zhconv_rs/zhconv_rs.abi3.so'
PYC_PROBE = INIT.parent / '__pycache__' / ('__init__.' + sys.implementation.cache_tag + '.pyc')
EXPECTED_INIT = 'b0451ddab68bd5a5e2be6228538f409eaa28eb53adcc3bb57998ab35b0cb129b'
EXPECTED_EXTENSION = 'c048aba5a41b4418754eeeba3457db5f5589522eab8010d646c156da66cf09e0'
assert sys.flags.isolated and sys.flags.no_site and sys.flags.dont_write_bytecode
assert len(sys.argv) == 1


class CollectorBoundary(BaseException):
    pass


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def checked_rows(rows):
    drift = []
    for row in rows:
        raw = Path(row['path']).read_bytes()
        if len(raw) != row['bytes'] or sha(raw) != row['sha256']:
            drift.append(row['path'])
    if drift:
        raise CollectorBoundary('bound input drift: ' + ','.join(drift))
    return drift


def module_files():
    rows = []
    for name, module in list(sys.modules.items()):
        path = getattr(module, '__file__', None)
        if path and Path(path).is_file():
            raw = Path(path).read_bytes()
            rows.append({'module': name, 'path': str(Path(path).absolute()),
                         'bytes': len(raw), 'sha256': sha(raw)})
    return rows


manifest_raw = (HERE / 'execution-inputs.json').read_bytes()
manifest = json.loads(manifest_raw)
checked_rows(manifest['inputs'])
owned = json.loads((HERE / 'owned-cases.json').read_bytes())
if (owned['case_count'] != 5 or owned['data_keys'] != 0 or
        owned['variant'] != 'zh-hans' or owned['positional_argument_count'] != 2 or
        owned['third_argument_omitted'] is not True):
    raise CollectorBoundary('wrong finite input profile')
cases = owned['cases']
if len(cases) != owned['case_count'] or len({c['id'] for c in cases}) != len(cases):
    raise CollectorBoundary('case count/identity')
for case in cases:
    if type(case['text']) is not str or len(case['text'].encode('utf-8')) >= 1024:
        raise CollectorBoundary('non-string/outside fixed text bound')
if sha(INIT.read_bytes()) != EXPECTED_INIT or sha(EXTENSION.read_bytes()) != EXPECTED_EXTENSION:
    raise CollectorBoundary('exact native package drift')

forbidden = ('app', 'requests', 'urllib3', 'socket', '_socket', 'subprocess',
             'ctypes', 'multiprocessing', 'pydantic', 'zhconv', 'zhconv_rs')
if any(n == p or n.startswith(p + '.') for n in sys.modules for p in forbidden):
    raise CollectorBoundary('forbidden/preloaded native module')
# Existing stdlib startup modules are measured, not inferred from another collector.
pre_modules = module_files()
allowed_reads = {row['path'] for row in manifest['inputs']}
allowed_reads.add(str(HERE / 'execution-inputs.json'))
for row in pre_modules:
    if row['path'] not in allowed_reads:
        raise CollectorBoundary('unbound stdlib runtime: ' + row['path'])
allowed_imports = {'zhconv_rs', 'zhconv_rs.zhconv_rs'}
audit_counts = {'package_import': 0, 'initializer_exec': 0, 'readonly_open': 0, 'unbound_bytecode_refused': 0}


def audit(event, args):
    if event == 'import':
        if args[0] not in allowed_imports:
            raise CollectorBoundary('unselected post-setup import: ' + str(args[0]))
        audit_counts['package_import'] += 1
    elif event == 'open':
        path, mode, flags = args
        if not isinstance(path, (str, bytes, os.PathLike)):
            raise CollectorBoundary('unselected fd open')
        path = os.fsdecode(path)
        if path == str(PYC_PROBE) and not (flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND)):
            # SourceLoader catches OSError and compiles the bound initializer.
            # Never read or execute unbound cached bytecode, even if it appears.
            audit_counts['unbound_bytecode_refused'] += 1
            raise FileNotFoundError(2, 'collector uses exact verified source, not bytecode cache', path)
        if path not in allowed_reads or (flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND)):
            raise CollectorBoundary('unselected file access: ' + path)
        audit_counts['readonly_open'] += 1
    elif event == 'compile':
        source, filename = args
        if filename != str(INIT):
            raise CollectorBoundary('unselected compilation: ' + str(filename))
        if type(source) is str:
            source = source.encode('utf-8')
        if sha(source) != EXPECTED_INIT:
            raise CollectorBoundary('initializer compile bytes changed')
    elif event == 'exec':
        if args[0].co_filename != str(INIT):
            raise CollectorBoundary('unselected exec')
        audit_counts['initializer_exec'] += 1
    elif event == 'os.listdir':
        if os.fsdecode(args[0]) not in (str(WHEEL_ROOT), str(WHEEL_ROOT / 'zhconv_rs')):
            raise CollectorBoundary('unselected directory enumeration')
    elif (event.startswith(('socket.', 'subprocess.', 'ctypes.', 'urllib.', 'http.')) or
          event in ('os.system', 'os.exec', 'os.posix_spawn', 'os.fork', 'os.forkpty',
                    'os.remove', 'os.rmdir', 'os.rename', 'os.mkdir', 'os.chmod',
                    'os.chown', 'os.symlink', 'os.link', 'os.truncate', 'os.chdir',
                    'os.putenv', 'os.unsetenv')):
        raise CollectorBoundary('forbidden event: ' + event)


sys.addaudithook(audit)
# Ordinary package import, not a custom loader/module impersonation.
sys.path.insert(0, str(WHEEL_ROOT))
import zhconv_rs

actual_native = sys.modules.get('zhconv_rs.zhconv_rs')
if (Path(zhconv_rs.__file__).absolute() != INIT or actual_native is None or
        Path(actual_native.__file__).absolute() != EXTENSION):
    raise CollectorBoundary('loaded package/native path mismatch')
convert = zhconv_rs.zhconv
if getattr(convert, '__name__', None) != 'zhconv':
    raise CollectorBoundary('wrong native symbol')
results = []
for case in cases:
    text = case['text']
    try:
        value = convert(text, 'zh-hans')  # EXACT two arguments; default wikitext.
    except BaseException as exc:
        if isinstance(exc, (CollectorBoundary, KeyboardInterrupt, SystemExit)):
            raise
        if not (isinstance(exc, Exception) or
                (type(exc).__module__ == 'pyo3_runtime' and type(exc).__name__ == 'PanicException')):
            raise
        frames = []
        tb = exc.__traceback__
        while tb is not None:
            frames.append({'file': tb.tb_frame.f_code.co_filename,
                           'line': tb.tb_lineno, 'function': tb.tb_frame.f_code.co_name})
            tb = tb.tb_next
        outcome = {'kind': 'exception', 'module': type(exc).__module__,
                   'class': type(exc).__name__, 'message': str(exc), 'frames': frames}
    else:
        if type(value) is not str:
            raise CollectorBoundary('native return outside declared string API')
        raw = value.encode('utf-8')
        outcome = {'kind': 'return', 'python_type': 'str', 'value': value,
                   'utf8_bytes': len(raw), 'utf8_base64': base64.b64encode(raw).decode('ascii'),
                   'utf8_sha256': sha(raw)}
    results.append({'input': case, 'input_utf8_sha256': sha(text.encode('utf-8')),
                    'outcome': outcome})

post_modules = module_files()
allowed_names = {r['module'] for r in pre_modules} | allowed_imports
if any(r['module'] not in allowed_names for r in post_modules):
    raise CollectorBoundary('unselected loaded module')
checked_rows(manifest['inputs'])
output = {
    'status': 'NATIVE_CALLS_COMPLETED_NOT_EXPECTED_VALUE_ASSERTIONS',
    'artifact': 'official zhconv-rs 0.4.2 / exact historical extension hash',
    'signature': "zhconv(text, 'zh-hans')", 'positional_argument_count': 2,
    'wikitext': 'default true; third argument omitted',
    'python': {'executable': sys.executable, 'version': sys.version},
    'manifest_sha256': sha(manifest_raw), 'input_count': len(manifest['inputs']),
    'case_count': len(results), 'cases': results,
    'module_files_before_native': pre_modules, 'module_files_after_native': post_modules,
    'audit_counts': audit_counts, 'input_drift': [],
    'boundaries': [
        'Only exact function/two arguments/fixed strings; no make_converter or path API.',
        'D descriptions retain raw HashMap order; no sorting/equality or stability assertion.',
        'Python audit events are not native syscall tracing or a native sandbox.',
        'Native stderr/exit are separately retained by coordinator; no expected values precomputed.',
        'No Go/native target implementation or complete name recognition credit.'
    ]
}
sys.stdout.write(json.dumps(output, ensure_ascii=False, indent=2) + '\n')
sys.stdout.flush()
