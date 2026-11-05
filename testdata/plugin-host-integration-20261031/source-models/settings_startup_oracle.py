#!/usr/bin/env python3
"""Read fixed ordinary settings declarations; execute that finite class AST.
The Go SDK exposes these as read-only snapshots, not SystemConfigOper records.
"""
import ast, hashlib, json, pathlib, subprocess

commit = '8a83efeacd3e26b82be495e17d50146e31c96a60'
path = 'app/core/config.py'
text = subprocess.check_output(['git', '-C', '/Users/accidia/projects/MoviePilot', 'show', commit + ':' + path], text=True)
names = ['BIG_MEMORY_MODE', 'META_CACHE_EXPIRE', 'TMDB_LOCALE', 'RECOGNIZE_SOURCE']
source = next(n for n in ast.parse(text).body if isinstance(n, ast.ClassDef) and n.name == 'ConfigModel')
assignments = [n for n in source.body if isinstance(n, ast.AnnAssign) and isinstance(n.target, ast.Name) and n.target.id in names]
node = ast.ClassDef(name='OrdinarySettings', bases=[], keywords=[], body=assignments, decorator_list=[])
namespace = {}
exec(compile(ast.fix_missing_locations(ast.Module(body=[node], type_ignores=[])), 'fixed/' + path, 'exec'), namespace)
config = namespace['OrdinarySettings']()
cases = [{'name':'fixed_defaults', 'values':{key:getattr(config, key) for key in names}}]
for name, patch in [('configured_raw', {'BIG_MEMORY_MODE':True, 'META_CACHE_EXPIRE':2, 'TMDB_LOCALE':'en-US', 'RECOGNIZE_SOURCE':'douban'}), ('empty_and_large_raw', {'BIG_MEMORY_MODE':False, 'META_CACHE_EXPIRE':99999999999999999999999999999, 'TMDB_LOCALE':'', 'RECOGNIZE_SOURCE':' owned-unrecognized '})]:
    config = namespace['OrdinarySettings']()
    for key, value in patch.items():
        setattr(config, key, value)
    cases.append({'name':name, 'values':{key:getattr(config, key) for key in names}})
result = {'commit':commit, 'source':path, 'source_sha256':hashlib.sha256(text.encode()).hexdigest(), 'positions':{n.target.id:n.lineno for n in assignments}, 'kind':'fixed source field AST, direct configured values; environment validation separately tested; no production or secrets', 'cases':cases}
pathlib.Path(__file__).with_name('settings-startup-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'keys':names}))
