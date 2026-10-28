"""Execute both fixed ordinary SiteDeleted core consumers against owned state."""
import ast
import copy
import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

root = Path('/Users/accidia/projects/MoviePilot')
paths = [root/'app/chain/search.py', root/'app/chain/subscribe.py']
functions = []
for index, path in enumerate(paths):
    cls = next(n for n in ast.parse(path.read_text()).body
               if isinstance(n, ast.ClassDef) and n.name in ('SearchChain', 'SubscribeChain'))
    node = copy.deepcopy(next(n for n in cls.body
                             if isinstance(n, ast.FunctionDef) and n.name == 'remove_site'))
    node.name = 'search_remove' if index == 0 else 'subscribe_remove'
    node.decorator_list = []
    for argument in node.args.args + node.args.kwonlyargs:
        argument.annotation = None
    node.returns = None
    functions.append(node)
cases = []
for site_id in [1, '*', None, 0, 99, '1', True, 1.0]:
    settings = {'indexer': [1, 2], 'rss': [1, 2]}
    subs = [SimpleNamespace(id=1, sites=[1, 2]),
            SimpleNamespace(id=2, sites=[2]), SimpleNamespace(id=3, sites=None)]
    updates = []
    config = SimpleNamespace(get=lambda key: settings[key],
                             set=lambda key, value: settings.__setitem__(key, value))
    def update(identifier, value):
        updates.append(dict(id=identifier, fields=copy.deepcopy(value)))
        next(s for s in subs if s.id == identifier).sites = value['sites']
    subscribe = SimpleNamespace(list=lambda: subs, update=update)
    ns = dict(SystemConfigOper=lambda: config,
              SystemConfigKey=SimpleNamespace(IndexerSites='indexer', RssSites='rss'),
              SubscribeOper=lambda: subscribe)
    exec(compile(ast.fix_missing_locations(ast.Module(body=functions, type_ignores=[])),
                 'fixed-site-deleted-consumers', 'exec'), ns)
    event = SimpleNamespace(event_data={'site_id': site_id})
    ns['search_remove'](None, event)
    ns['subscribe_remove'](None, event)
    cases.append(dict(site_id=site_id, settings=settings,
                      subscriptions=[dict(id=s.id, sites=s.sites) for s in subs],
                      updates=updates))
settings = {'indexer': [1, 2], 'rss': [1, 2]}
subs = [SimpleNamespace(id=1, sites=[1, 2]), SimpleNamespace(id=2, sites=[2]), SimpleNamespace(id=3, sites=None)]
updates = []
def fail_second(identifier, value):
    if identifier == 2:
        raise RuntimeError('owned second update boundary failure')
    updates.append(dict(id=identifier, fields=copy.deepcopy(value)))
    next(s for s in subs if s.id == identifier).sites = value['sites']
config = SimpleNamespace(get=lambda key: settings[key], set=lambda key, value: settings.__setitem__(key, value))
ns.update(SystemConfigOper=lambda: config, SubscribeOper=lambda: SimpleNamespace(list=lambda: subs, update=fail_second))
event = SimpleNamespace(event_data={'site_id': '*'})
ns['search_remove'](None, event)
exception = None
try:
    ns['subscribe_remove'](None, event)
except RuntimeError as error:
    exception = type(error).__name__
failure_case = dict(site_id='*', settings=settings, subscriptions=[dict(id=s.id, sites=s.sites) for s in subs], updates=updates, exception=exception)
result = dict(source_commit='8a83efeacd3e26b82be495e17d50146e31c96a60',
              kind='actual fixed SearchChain/SubscribeChain remove_site methods; owned configuration and subscriptions, no source service operations',
              source_sha256={str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
                             for p in paths}, cases=cases, failure_case=failure_case)
Path(__file__).with_name('site-deleted-consumer-oracle.json').write_text(
    json.dumps(result, ensure_ascii=False, indent=2)+'\n')
print('fixed SiteDeleted core consumer cases', len(cases))
