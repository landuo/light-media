"""Development-only fixed-source oracle; never an application dependency."""
import ast
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

source = Path('/Users/accidia/projects/MoviePilot/app/core/plugin.py').read_bytes()
tree = ast.parse(source)
original = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'PluginManager')
methods = [n for n in original.body if isinstance(n, ast.FunctionDef) and n.name in ('__set_and_check_auth_level', '__get_plugin_private_key')]
assert len(methods) == 2
for method in methods:
    method.returns = None
    for argument in method.args.args:
        argument.annotation = None
program = ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='PluginManager', bases=[], keywords=[], body=methods, decorator_list=[])], type_ignores=[]))
state = {}
lookups = []
class SyntheticEnvironment:
    @staticmethod
    def get(key):
        lookups.append(key)
        return 'synthetic-private'
class SyntheticRSA:
    @staticmethod
    def verify_rsa_keys(public_key, private_key):
        assert public_key == 'synthetic-public' and private_key == 'synthetic-private'
        return state['matches']
class Plugin:
    pass
namespace = {'SitesHelper': lambda: SimpleNamespace(auth_level=state['current']), 'schemas': SimpleNamespace(Plugin=Plugin), 'RSAUtils': SyntheticRSA, 'os': SimpleNamespace(environ=SyntheticEnvironment), 'logger': SimpleNamespace(debug=lambda _: None)}
exec(compile(program, 'fixed-core-plugin.py', 'exec'), namespace)
check = namespace['PluginManager']._PluginManager__set_and_check_auth_level
rows = []
for current in [0, 1, 2, 3, 99]:
    for required in [None, 0, 1, 2, 3, 99]:
        for public in [None, '', 'synthetic-public']:
            for matches in [False, True]:
                state.update(current=current, matches=matches)
                lookups.clear()
                plugin = SimpleNamespace(__name__='OwnedLicenseclone')
                if required is not None: plugin.auth_level = required
                if public is not None: plugin.plugin_public_key = public
                allowed = check(plugin)
                rows.append({'current': current, 'required': required, 'public_key': public, 'matches': matches, 'allowed': allowed, 'lookups': list(lookups)})
Path(__file__).with_name('source-oracle.json').write_text(json.dumps({'backend_commit': '8a83efeacd3e26b82be495e17d50146e31c96a60', 'plugin_source_sha256': hashlib.sha256(source).hexdigest(), 'method': 'PluginManager.__set_and_check_auth_level', 'evidence_type': 'fixed-source method execution with synthetic SitesHelper/RSA providers; not official authentication', 'cases': rows}, ensure_ascii=False, indent=2)+'\n')
print(len(rows), 'original-method cases')
