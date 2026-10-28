"""Fixed FileItem models and actual LocalStorage item constructors, readonly source."""
import ast
import copy
import hashlib
import json
import os
import pathlib
import re
import tempfile
import types
from typing import Optional
from pydantic import BaseModel, Field

root = pathlib.Path('/Users/accidia/projects/MoviePilot')
files = ['app/schemas/file.py', 'app/modules/filemanager/storages/local.py']
raw = {f: (root/f).read_text() for f in files}
ns = dict(BaseModel=BaseModel, Field=Field, Optional=Optional, Path=pathlib.Path, re=re)
def execute(nodes, name):
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), *nodes], type_ignores=[])), name, 'exec'), ns)
execute([n for n in ast.parse(raw[files[0]]).body if isinstance(n, ast.ClassDef) and n.name in ['FileURI','FileItem']], files[0])
for model in ['FileURI', 'FileItem']:
    ns[model].model_rebuild(_types_namespace=ns)
cls = next(n for n in ast.parse(raw[files[1]]).body if isinstance(n, ast.ClassDef))
nodes = [copy.deepcopy(n) for n in cls.body if isinstance(n, ast.FunctionDef) and n.name in ['__get_fileitem','__get_diritem']]
assert len(nodes) == 2
execute([ast.ClassDef(name=cls.name, bases=[], keywords=[], body=nodes, decorator_list=[])], files[1])
owner = ns[cls.name]()
owner.schema = types.SimpleNamespace(value='local')
ns['schemas'] = types.SimpleNamespace(FileItem=ns['FileItem'])
cases = []
with tempfile.TemporaryDirectory(prefix='owned-fileitem-source-') as temp:
    base = pathlib.Path(temp)
    for name, directory in [('Movie.2026.mkv',False),('.hidden',False),('trailing.',False),('..dot.mkv',False),('字幕.zh-cn.srt',False),('folder.2026',True),('folder',True)]:
        path = base/name
        if directory: path.mkdir()
        else: path.write_bytes(b'owned-fileitem')
        os.utime(path, ns=(1712345678123456789,1712345678123456789))
        fn = getattr(owner, '_' + cls.name + ('__get_diritem' if directory else '__get_fileitem'))
        item = fn(path)
        value = item.model_dump()
        value['path'] = value['path'].replace(temp,'__ROOT__')
        cases.append(dict(name=name, directory=directory, expected=value))
out = dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60', kind='actual fixed Pydantic FileURI/FileItem and LocalStorage constructors; owned real files with fixed mtime', source_sha256={f:hashlib.sha256(s.encode()).hexdigest() for f,s in raw.items()}, default=ns['FileItem']().model_dump(), file_bytes='owned-fileitem', mtime_ns=1712345678123456789, cases=cases)
pathlib.Path(__file__).with_name('local-file-item-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print('actual fixed local FileItem cases',len(cases))
