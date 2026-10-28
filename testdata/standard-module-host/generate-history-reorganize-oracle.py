"""Run fixed ordinary manual-history source/cleanup branches, with controlled
history and storage boundaries. The selected AST statements are unmodified;
this is source replay, not a running MoviePilot installation.
"""
import ast
import copy
import json
import subprocess
from pathlib import Path
from types import SimpleNamespace as NS

FIXED = '8a83efeacd3e26b82be495e17d50146e31c96a60'
ROOT = '/Users/accidia/projects/MoviePilot'
def tree(path):
    return ast.parse(subprocess.check_output(['git', '-C', ROOT, 'show', FIXED + ':' + path], text=True))
endpoint = tree('app/api/endpoints/transfer.py')
selection = next(n for n in ast.walk(endpoint) if isinstance(n, ast.If) and 299 <= n.lineno <= 320 and ast.unparse(n.test) == "history.status and 'move' in history.mode")
chain = tree('app/chain/transfer.py')
cleanup = next(n for n in ast.walk(chain) if isinstance(n, ast.If) and ast.unparse(n.test) == 'cleanup_dest_fileitem and planned_file_count and (not preview)')
fn = ast.FunctionDef(name='cleanup', args=ast.arguments(posonlyargs=[], args=[], kwonlyargs=[], kw_defaults=[], defaults=[]), body=[copy.deepcopy(cleanup), ast.Return(value=ast.Constant(value='continue'))], decorator_list=[])
rows = []
for name, status, mode, preview, reorganize, count, accepted in [
    ('success-hardlink', True, 'hardlink', False, False, 1, True),
    ('success-move', True, 'move', False, False, 1, True),
    ('failed-move', False, 'move', False, False, 1, True),
    ('preview', True, 'hardlink', True, False, 1, True),
    ('reorganize', True, 'hardlink', False, True, 1, True),
    ('no-files', True, 'hardlink', False, False, 0, True),
    ('delete-false', True, 'hardlink', False, False, 1, False),
]:
    trace = []
    history = NS(status=status, mode=mode, src_fileitem={'path': '/owned/source.mkv'}, dest_fileitem={'path': '/owned/target.mkv'})
    class Storage:
        def delete_media_file(self, item):
            trace.append({'method': 'delete_media_file', 'path': item.path})
            return accepted
    ns = dict(history=history, transer_item=NS(preview=preview, reorganize=reorganize), FileItem=lambda **kw: NS(**kw), cleanup_dest_fileitem=None, StorageChain=Storage, planned_file_count=count, preview=preview)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[copy.deepcopy(selection), copy.deepcopy(fn)], type_ignores=[])), '<fixed:manual history cleanup>', 'exec'), ns)
    result = ns['cleanup']()
    rows.append(dict(name=name, source=[item.path for item in ns['src_fileitems']], trace=trace, result=result))
Path(__file__).with_name('history-reorganize-source-oracle.json').write_text(json.dumps(dict(fixed_backend=FIXED, source=['app/api/endpoints/transfer.py:299-320', 'app/chain/transfer.py:3389-3394'], boundary=__doc__, cases=rows), ensure_ascii=False, indent=2) + '\n')
print(len(rows), 'fixed manual history source and cleanup cases')
