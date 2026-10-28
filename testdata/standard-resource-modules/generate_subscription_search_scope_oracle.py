"""Execute pinned SubscribeChain.search through its real process-call boundary.

Inventory/recognition/network are owned boundaries. The complete method executes,
with an explicit return inserted immediately after SearchChain.process so this
probe does not pretend to exercise source download/business persistence.
"""
import ast
import copy
import hashlib
import json
import pathlib
import threading
import types
from datetime import datetime

root = pathlib.Path('/Users/accidia/projects/MoviePilot')
path = root / 'app/chain/subscribe.py'
raw = path.read_text()
cls = next(n for n in ast.parse(raw).body if isinstance(n, ast.ClassDef) and n.name == 'SubscribeChain')
method = copy.deepcopy(next(n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name == 'search'))
cut = []
class StopAfterProcess(ast.NodeTransformer):
    def visit_Assign(self, node):
        if isinstance(node.value, ast.Call) and isinstance(node.value.func, ast.Attribute) and node.value.func.attr == 'process':
            cut.append(node.lineno)
            return [node, ast.Return(value=ast.Constant(value=None))]
        return self.generic_visit(node)
method = StopAfterProcess().visit(method)
assert cut == [1410]
ns = dict(datetime=datetime, global_vars=types.SimpleNamespace(is_system_stopped=False), logger=types.SimpleNamespace(debug=lambda *a: None, warn=lambda *a: None, info=lambda *a: None, error=lambda *a: None), _subscribe_media_key=lambda s: 'tmdb:900043', _subscribe_recognize_kwargs=lambda s: dict(tmdbid=s.tmdbid), build_subscribe_meta=lambda s: types.SimpleNamespace(type='电视剧'), SystemConfigKey=types.SimpleNamespace(BestVersionFilterRuleGroups='best', SubscribeFilterRuleGroups='ordinary'), SystemConfigOper=lambda: types.SimpleNamespace(get=lambda key: ['owned-rule']))
module = ast.Module(body=[ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0), ast.ClassDef(name='SubscribeChain', bases=[], keywords=[], body=[method], decorator_list=[])], type_ignores=[])
exec(compile(ast.fix_missing_locations(module), 'fixed/subscribe-search-scope.py', 'exec'), ns)
cases = []
for sites, missing in [([1, 2], [1, 2]), ([2], [2, 3]), ([1, 2], [2])]:
    observed = []
    sub = types.SimpleNamespace(id=43, name='Owned scope', date=None, custom_words=None, type='电视剧', state='R', keyword='Owned scope', search_imdbid=False, filter_groups=['owned-rule'], best_version=False, tmdbid=900043, doubanid=None, episode_group=None)
    media = types.SimpleNamespace(title='Owned scope')
    inventory = {'tmdb:900043': {1: dict(episodes=missing)}}
    ns['SubscribeOper'] = lambda: types.SimpleNamespace(get=lambda sid: sub)
    def process(**kwargs):
        observed.append(dict(sites=kwargs['sites'], missing=kwargs['no_exists'], keyword=kwargs['keyword'], rule_groups=kwargs['rule_groups'], same_recognized_media=kwargs['mediainfo'] is media))
        return []
    ns['SearchChain'] = lambda: types.SimpleNamespace(process=process)
    owner = ns['SubscribeChain']()
    owner._rlock, owner._LOCK_TIMOUT = threading.Lock(), 1
    recognized = []
    owner.recognize_media = lambda **kwargs: (recognized.append(kwargs) or media)
    owner.check_and_handle_existing_media = lambda **kwargs: (False, inventory)
    owner.get_sub_sites = lambda subscription: sites
    owner.get_params = lambda subscription: {}
    owner.search(sid=43)
    assert len(observed) == 1 and len(recognized) == 1 and observed[0]['same_recognized_media']
    cases.append(dict(selected_sites=sites, missing=missing, process_calls=observed, recognition_count=len(recognized), released_lock=not owner._rlock.locked()))
out = dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60', source_sha256={'app/chain/subscribe.py': hashlib.sha256(raw.encode()).hexdigest()}, kind='actual fixed SubscribeChain.search prefix to real SearchChain.process boundary; owned inventory/recognition; source downloads not executed', process_call_line=cut[0], cases=cases)
pathlib.Path(__file__).with_name('subscription-search-scope-source-oracle.json').write_text(json.dumps(out, ensure_ascii=False, indent=2)+'\n')
print('actual fixed subscription search scope cases', len(cases))
