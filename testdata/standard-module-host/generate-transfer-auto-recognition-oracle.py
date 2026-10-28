"""Run the pinned ordinary transfer recognition prefix and MediaChain implementation.

Only the prefix before the failure-notification/AI branch is extracted. Provider
and database boundaries are controlled; source conditionals and image order run.
"""
import ast
import copy
import json
import subprocess
from pathlib import Path
from types import SimpleNamespace as NS

FIXED = '8a83efeacd3e26b82be495e17d50146e31c96a60'
ROOT = '/Users/accidia/projects/MoviePilot'
def method(path, owner, name):
    tree = ast.parse(subprocess.check_output(['git', '-C', ROOT, 'show', FIXED + ':' + path], text=True))
    cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == owner)
    return copy.deepcopy(next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == name))

recognize = method('app/chain/media.py', 'MediaChain', '_recognize_with_fallback_by_meta')
handle = method('app/chain/transfer.py', 'TransferChain', '__handle_transfer')
prefix = copy.deepcopy(handle.body[1].body) if not isinstance(handle.body[0], ast.Try) else copy.deepcopy(handle.body[0].body)
# First node is the docstring; __handle_transfer's second node is the try.
prefix = copy.deepcopy(next(n for n in handle.body if isinstance(n, ast.Try)).body[:5])
branch = prefix[-1]
assert isinstance(branch, ast.If) and ast.unparse(branch.test) == 'not mediainfo'
cut = next(i for i, n in enumerate(branch.body) if isinstance(n, ast.If) and ast.unparse(n.test) == 'not mediainfo')
branch.body = branch.body[:cut]
rows = []
for name, loaded, history_id, history_present, conflict, recognized in [
    ('automatic_success', False, False, False, False, True),
    ('automatic_missing', False, False, False, False, False),
    ('preloaded', True, False, False, False, True),
    ('history_id', False, True, True, False, True),
    ('history_without_id', False, False, True, False, True),
    ('history_year_conflict', False, True, True, True, True),
]:
    trace = []
    media = NS(type=NS(value='电影'), title_year='Owned (2024)', poster_path=None)
    class Host:
        def recognize_media(self, **kw):
            trace.append({'method': 'recognize_media', 'by_meta': 'meta' in kw})
            return media if recognized else None
        def obtain_images(self, mediainfo):
            trace.append({'method': 'obtain_images'})
            mediainfo.poster_path = 'mutated-input'
            return NS(poster_path='ignored-return')
        def select_recognize_source(self, **kw): return kw['native_fn']()
        def _is_movie_year_conflict(self, *args): return conflict
    ns = {'deepcopy': copy.deepcopy, 'logger': NS(info=lambda *a: None), 'TransferHistoryOper': lambda: None, 'MediaType': lambda v: v}
    cls = ast.ClassDef(name='PinnedMedia', bases=[ast.Name(id='Host', ctx=ast.Load())], keywords=[], body=[recognize], decorator_list=[])
    ns['Host'] = Host
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), cls], type_ignores=[])), '<fixed:MediaChain>', 'exec'), ns)
    obj = ns['PinnedMedia']()
    obj.recognize_by_meta = lambda meta, **kw: obj._recognize_with_fallback_by_meta(meta, **kw)
    ns['MediaChain'] = lambda: obj
    history = NS(username='owned', media_id=None, tmdbid=123 if history_id else None, doubanid=None, bangumiid=None, anilistid=None, type='电影', media_source=None, episode_group=None, media_category=None, year=2023)
    task = NS(mediainfo=media if loaded else None, download_history=history if history_present else None, meta=NS(title='Owned.2024', year=2024), media_source=None, fileitem=NS(name='Owned.2024.mkv'))
    ns.update(self=obj, task=task)
    exec(compile(ast.fix_missing_locations(ast.Module(body=prefix, type_ignores=[])), '<fixed:ordinary transfer recognition>', 'exec'), ns)
    result = ns['mediainfo']
    rows.append({'name': name, 'trace': trace, 'recognized': result is not None, 'poster_path': result.poster_path if result else None})
Path(__file__).with_name('transfer-auto-recognition-source-oracle.json').write_text(json.dumps({'fixed_backend': FIXED, 'source': ['app/chain/transfer.py:1782-1857', 'app/chain/media.py:723-779'], 'kind': __doc__, 'cases': rows}, ensure_ascii=False, indent=2) + '\n')
print(len(rows), 'fixed ordinary recognition cases')
