"""Fixed ProgressHelper and memory TLRU, only their clock is controlled."""
import ast, hashlib, json
from enum import Enum
from pathlib import Path
from typing import Any, Dict, Optional, Union
from types import SimpleNamespace
from cachetools import TLRUCache

root = Path('/Users/accidia/projects/MoviePilot')
paths = [root/'app/core/cache.py', root/'app/helper/progress.py']
clock = [0.0]
class ControlledTLRU(TLRUCache):
    def __init__(self, **kwargs):
        super().__init__(**kwargs, timer=lambda: clock[0])

ns = {'MemoryTLRUCache': ControlledTLRU, 'Dict': Dict, 'Any': Any}
tree = ast.parse(paths[0].read_text())
node = next(x for x in tree.body if isinstance(x, ast.ClassDef) and x.name == '_MemoryTLRUCache')
exec(compile(ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[])), 'fixed-cache', 'exec'), ns)
cache = ns['_MemoryTLRUCache'](maxsize=1024, ttl=86400)
helper_ns = {'Enum': Enum, 'Optional': Optional, 'Union': Union,
             'ProgressKey': str, 'TTLCache': lambda **kwargs: cache,
             'LocaleHelper': SimpleNamespace(translate_text=lambda text, locale=None: text)}
tree = ast.parse(paths[1].read_text())
node = next(x for x in tree.body if isinstance(x, ast.ClassDef) and x.name == 'ProgressHelper')
exec(compile(ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[])), 'fixed-progress', 'exec'), helper_ns)
Helper = helper_ns['ProgressHelper']
for i in range(1024):
    clock[0] = float(i)
    Helper(str(i)).start()
Helper('0').get()
Helper('1024').start()
lru = {'read_key_retained': Helper('0').get() is not None,
       'least_recent_key_retained': Helper('1').get() is not None,
       'inserted_key_retained': Helper('1024').get() is not None}
clock[0] = 100000.0
Helper('expiry').start()
clock[0] += 86400.0
at_expiry = Helper('expiry').get()
Helper('expiry').update(value=51, text='must not revive')
after_update = Helper('expiry').get()
Helper('expiry').end(value=100, text='must not revive')
after_end = Helper('expiry').get()
print(json.dumps({'fixed_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60',
                  'kind': 'actual fixed ProgressHelper and _MemoryTLRUCache, actual cachetools TLRU; controlled clock only',
                  'source_sha256': {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths},
                  'lru': lru, 'expiry': {'at_expiry': at_expiry, 'after_update': after_update, 'after_end': after_end}}, indent=2))
