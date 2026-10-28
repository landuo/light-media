"""Execute fixed SiteUpdated/refresh_userdata methods, with counted IO edges.

Run with the existing source-oracle venv (Pydantic 2.13.4). No source imports,
old service startup, or writes to the readonly reference checkout are needed.
"""
import ast
import base64
import copy
import hashlib
import inspect
import json
import re
import sys
import traceback
import types
from pathlib import Path
from typing import *
from urllib import parse
from urllib.parse import urljoin

from pydantic import BaseModel, Field, ConfigDict
from lxml import etree

root = Path('/Users/accidia/projects/MoviePilot')
paths = ['app/chain/__init__.py', 'app/chain/site.py', 'app/schemas/site.py',
         'app/utils/string.py', 'app/utils/object.py', 'app/modules/indexer/__init__.py']
raw = {p: (root / p).read_text() for p in paths}
ns = dict(BaseModel=BaseModel, Field=Field, ConfigDict=ConfigDict, parse=parse,
          Optional=Optional, Union=Union, Any=Any, Dict=Dict, List=List,
          re=re, inspect=inspect, traceback=traceback)

def execute(nodes, name):
    exec(compile(ast.fix_missing_locations(ast.Module(body=[
        ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0),
        *nodes], type_ignores=[])), 'fixed/' + name, 'exec'), ns)

def methods(path, cls, names, bases=()):
    original = next(n for n in ast.parse(raw[path]).body if isinstance(n, ast.ClassDef) and n.name == cls)
    nodes = [copy.deepcopy(n) for n in original.body if isinstance(n, ast.FunctionDef) and n.name in names]
    assert len(nodes) == len(names)
    for node in nodes:
        node.decorator_list = [d for d in node.decorator_list if isinstance(d, ast.Name) and d.id == 'staticmethod']
    execute([ast.ClassDef(name=cls, bases=[ast.Name(id=b, ctx=ast.Load()) for b in bases],
                         keywords=[], body=nodes, decorator_list=[])], path)

execute([n for n in ast.parse(raw['app/schemas/site.py']).body if isinstance(n, ast.ClassDef) and n.name == 'SiteUserData'], 'site-dto.py')
execute([n for n in ast.parse(raw['app/utils/string.py']).body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == '_special_domains' for t in n.targets)], 'special-domains.py')
methods('app/utils/string.py', 'StringUtils', ['get_url_netloc', 'get_url_domain', 'get_url_host', 'is_valid_html_element'])
methods('app/utils/object.py', 'ObjectUtils', ['check_signature'])
ns['get_type_hints'], ns['FunctionType'] = get_type_hints, types.FunctionType
class RateLimitExceededException(Exception):
    pass
ns['RateLimitExceededException'] = RateLimitExceededException
ns['logger'] = types.SimpleNamespace(**{name: lambda *a: None for name in ['info', 'debug', 'warn', 'error']})
ns['EventType'] = types.SimpleNamespace(SystemError='system.error', SiteRefreshed='site.refreshed')
ns['NotificationType'] = types.SimpleNamespace(SiteMessage='站点')
ns['Notification'] = lambda **kw: kw
methods('app/chain/__init__.py', 'ChainBase', ['__is_valid_empty', '__handle_plugin_error', '__handle_system_error', '__handle_rate_limit_error', '__execute_plugin_modules', '__execute_system_modules', 'run_module'])
methods('app/chain/site.py', 'SiteChain', ['refresh_userdata', '_post_site_messages', 'cache_site_userdata', 'clear_site_data'], ('ChainBase',))
DTO, Chain = ns['SiteUserData'], ns['SiteChain']
native_source = next(n for n in ast.walk(ast.parse(raw['app/modules/indexer/__init__.py'])) if isinstance(n, ast.FunctionDef) and n.name == 'refresh_userdata')
assert ast.unparse(native_source.args.args[1].annotation) == 'dict'
domain_cases = []
for value in ['', 'example.test', 'https://a.example.test:8443/path', 'http://127.0.0.1:43100', 'http://localhost:43100', 'https://u2.dmhy.org/a', 'https://a.b.example.test/path']:
    error = None
    try:
        host = ns['StringUtils'].get_url_host(value)
    except Exception as e:
        host, error = None, type(e).__name__
    domain_cases.append(dict(input=value, domain=ns['StringUtils'].get_url_domain(value), host=host, host_error=error))
site = dict(id=1, name='Owned site', domain='http://127.0.0.1:43100', url='http://127.0.0.1:43100')
cases = []
for label, value in [('none', None), ('false', False), ('zero', 0), ('empty_dict', {}),
                     ('empty_list', []), ('nonempty_list', [1]), ('bad_scalar', 'owned'),
                     ('dto', DTO(username='plugin', upload=40)),
                     ('dto_error', DTO(err_msg='owned parser error')),
                     ('summary_ratio', DTO(message_unread=2, ratio=0.5)),
                     ('vip_ratio', DTO(ratio=0.5, user_level='VIP')),
                     ('nullable_dto', DTO(upload=None, download=None, ratio=None, bonus=None, seeding=None, leeching=None, message_unread=None)),
                     ('arbitrary_seeding_rows', DTO(seeding_info=[None, {'owned': 1}, ['x', 2, 3]])),
                     ('message_then_bad_row', DTO(message_unread=2, message_unread_contents=[['head', 'date', 'body', 'source'], None])),
                     ('throw', None)]:
    trace, native_calls, notices = [], [], []
    native_dto = DTO(username='native', upload=80)
    class Native:
        def get_priority(self): return 0
        def get_name(self): return 'Owned indexer'
        def refresh_userdata(self, site: dict):
            native_calls.append(site)
            return native_dto
    def plugin(**kw):
        trace.append('plugin')
        if label == 'throw': raise RuntimeError('owned failure')
        return copy.deepcopy(value)
    c = Chain()
    c.pluginmanager = types.SimpleNamespace(get_plugin_modules=lambda: {('OwnedModuleOverride', 'owned'): {'refresh_userdata': plugin}})
    c.modulemanager = types.SimpleNamespace(get_running_modules=lambda method: [Native()])
    c.messagehelper = types.SimpleNamespace(put=lambda **kw: trace.append('error-message'))
    c.eventmanager = types.SimpleNamespace(send_event=lambda *a: trace.append('error-event'))
    c.messageoper = types.SimpleNamespace(exists_by_source=lambda source: False)
    c.post_message = lambda message: (trace.append('notice'), notices.append(message))
    ns['SiteOper'] = lambda: types.SimpleNamespace(update_userdata=lambda **kw: trace.append('update'))
    ns['eventmanager'] = types.SimpleNamespace(send_event=lambda *a: trace.append('site.refreshed'))
    exception = None
    try:
        result = c.refresh_userdata(site)
        returned = result.model_dump() if isinstance(result, DTO) else result
    except Exception as e:
        returned, exception = None, type(e).__name__
    cases.append(dict(name=label, plugin_result=value.model_dump() if isinstance(value, DTO) else value,
                      native_calls=len(native_calls), trace=trace, notices=notices,
                      returned=returned, exception=exception))
consumer_cases = []
for data in [{}, {'domain': ''}, {'domain': 'missing.test'}, {'domain': site['domain']}, {'domain': '127.0.0.1:43100'}, {'domain': 123}]:
    refreshed = []
    ns['SitesHelper'] = lambda: types.SimpleNamespace(get_indexer=lambda domain: site if domain == '127.0.0.1:43100' else None)
    c = types.SimpleNamespace(refresh_userdata=lambda **kw: refreshed.append(kw['site']['id']))
    error = None
    try: Chain.cache_site_userdata(c, types.SimpleNamespace(event_data=data))
    except Exception as e: error = type(e).__name__
    consumer_cases.append(dict(data=data, refreshed=refreshed, exception=error))
out = dict(commit='8a83efeacd3e26b82be495e17d50146e31c96a60', python=sys.version,
           kind='actual fixed sync dispatcher, SiteChain refresh/message and event consumer; native IO/DB/message deliveries are counted boundaries',
           source_sha256={p: hashlib.sha256(v.encode()).hexdigest() for p, v in raw.items()},
           domain_cases=domain_cases, cases=cases, consumer_cases=consumer_cases)
out['native_input'] = DTO(username='native', upload=80).model_dump()
clear_cases = []
for domain, fail in [('https://a.foo.test', None), ('https://a.foo.test', 'site.foobar.token'), ('http://127.0.0.1:43100', None), ('http://localhost:43100', None), ('', None)]:
    keys = ['site.foo.token', 'site.foobar.token', 'site.other.token', 'unrelated', 'site.0.token']
    deleted = []
    def delete(key):
        if key == fail: raise RuntimeError('owned SQL failure')
        deleted.append(key)
    ns['SystemConfigOper'] = lambda: types.SimpleNamespace(all=lambda: dict.fromkeys(keys), delete=delete)
    error = None
    try: Chain.clear_site_data(None, types.SimpleNamespace(event_data={'domain': domain}))
    except Exception as e: error = type(e).__name__
    clear_cases.append(dict(domain=domain, fail=fail, deleted=deleted, exception=error))
out['clear_cases'] = clear_cases
favicon_node = copy.deepcopy(next(n for n in ast.walk(ast.parse(raw['app/chain/site.py'])) if isinstance(n, ast.FunctionDef) and n.name == '__parse_favicon'))
favicon_node.decorator_list = []
ns.update(etree=etree, urljoin=urljoin, base64=base64)
execute([favicon_node], 'site-favicon.py')
icon_cases = []
for name, page, home_status, icon_status in [
    ('first_head_icon', '<html><head><link rel="shortcut icon" href="/owned.ico"><link rel="icon" href="/ignored.ico"></head><body>x</body></html>', 200, 200),
    ('missing_link', '<html><head></head><body>x</body></html>', 200, 200),
    ('case_sensitive_rel', '<html><head><link rel="ICON" href="/ignored.ico"></head><body>x</body></html>', 200, 200),
    ('body_ignored', '<html><head></head><body><link rel="icon" href="/ignored.ico"></body></html>', 200, 200),
    ('home_failure', '<html></html>', 503, 200),
    ('icon_failure', '<html><head><link rel="icon" href="/owned.ico"></head><body>x</body></html>', 200, 404),
    ('foreign_icon', '<html><head><link rel="icon" href="https://foreign.example.test/owned.ico"></head><body>x</body></html>', 200, 200),
]:
    requests = []
    class Response:
        def __init__(self, status, data): self.status, self.content, self.text = status, data, data.decode()
        def __bool__(self): return 200 <= self.status < 400
    def request_factory(**kwargs):
        def get_res(url):
            requests.append(dict(url=url, timeout=kwargs['timeout']))
            if len(requests) == 1: return Response(home_status, page.encode())
            return Response(icon_status, b'owned-icon')
        return types.SimpleNamespace(get_res=get_res)
    ns['RequestUtils'] = request_factory
    chosen, data = ns['__parse_favicon'](site['domain'], 'owned-cookie', 'Owned-UA')
    icon_cases.append(dict(name=name, page=page, home_status=home_status, icon_status=icon_status,
                           requests=requests, chosen=chosen, data_base64=data))
out['favicon_cases'] = icon_cases
Path(__file__).with_name('site-updated-consumer-oracle.json').write_text(json.dumps(out, ensure_ascii=False, indent=2)+'\n')
print('fixed SiteUpdated', len(cases), 'refresh;', len(consumer_cases), 'consumer;', len(domain_cases), 'domain cases')
