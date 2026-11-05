#!/usr/bin/env python3
"""Execute the pinned ordinary helper AST against owned boundary doubles.

This is a protocol/branch oracle. It does not execute a browser or contact a
FlareSolverr service and cannot, by itself, prove rendered HTML equivalence.
"""
import ast
import hashlib
import json
from pathlib import Path
import subprocess
import types
import typing
from urllib.parse import quote, unquote

SHA = '8a83efeacd3e26b82be495e17d50146e31c96a60'
REPO = '/Users/accidia/projects/MoviePilot'
OUT = Path(__file__).resolve().parent
raw = subprocess.check_output(['git', '-C', REPO, 'show', SHA + ':app/helper/browser.py'])
tree = ast.parse(raw.decode())
helper = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'PlaywrightHelper')
code = compile(ast.fix_missing_locations(ast.Module(body=[helper], type_ignores=[])), 'fixed-browser.py', 'exec')
http_raw = subprocess.check_output(['git', '-C', REPO, 'show', SHA + ':app/utils/http.py'])
cookie_functions = [n for n in ast.parse(http_raw.decode()).body if isinstance(n, ast.FunctionDef) and n.name in {'_url_decode_if_latin', 'cookie_parse'}]
cookie_code = compile(ast.fix_missing_locations(ast.Module(body=cookie_functions, type_ignores=[])), 'fixed-http-cookie.py', 'exec')


def run(case, *, mode='cloakbrowser', replies=(), fail='', action=False, kwargs=None):
    calls = []
    queue = list(replies)

    class Logger:
        def __getattr__(self, _):
            return lambda *args, **kw: None

    class Page:
        def step(self, name, value=None):
            calls.append({'operation': name, 'value': value})
            if fail == name:
                raise RuntimeError('owned ' + name)

        def set_extra_http_headers(self, value):
            self.step('headers', value)

        def goto(self, url):
            self.step('goto', url)

        def wait_for_load_state(self, state, **kw):
            self.step('wait', {'state': state, **kw})

        def content(self):
            self.step('content')
            return '<html><body>owned rendered</body></html>'

        def close(self):
            self.step('page.close')

    class Context:
        def new_page(self):
            calls.append({'operation': 'new_page'})
            if fail == 'new_page':
                raise RuntimeError('owned new_page')
            return Page()

        def close(self):
            calls.append({'operation': 'context.close'})
            if fail == 'context.close':
                raise RuntimeError('owned context.close')

    def launch(**kw):
        calls.append({'operation': 'launch', 'value': kw})
        if fail == 'launch':
            raise RuntimeError('owned launch')
        return Context()

    class Requests:
        def __init__(self, **kw):
            self.kw = kw

        def post_json(self, **kw):
            calls.append({'operation': 'fs.post', 'request': kw, 'options': self.kw})
            response = queue.pop(0) if queue else None
            if response == 'raise':
                raise RuntimeError('owned fs')
            return response

    ns = dict(Any=typing.Any, Callable=typing.Callable, Optional=typing.Optional,
              BrowserPage=typing.Any, BrowserContext=typing.Any,
              settings=types.SimpleNamespace(BROWSER_EMULATION=mode,
                                             PLAYWRIGHT_BROWSER_TYPE='chromium',
                                             FLARESOLVERR_URL='http://owned.invalid/fs/'),
              uuid=types.SimpleNamespace(uuid4=lambda: 'owned-session'),
              RequestUtils=Requests, logger=Logger(), Union=typing.Union,
              quote=quote, unquote=unquote)
    exec(cookie_code, ns)
    exec(code, ns)
    cls = ns['PlaywrightHelper']
    cls._PlaywrightHelper__launch_cloakbrowser_context = staticmethod(launch)
    obj = cls()
    args = {'url': 'http://owned.invalid/page', **(kwargs or {})}
    if action:
        def callback(page):
            calls.append({'operation': 'callback'})
            return {'owned': True}
        args['callback'] = callback
    value = (obj.action if action else obj.get_page_source)(**args)
    return {'case': case, 'mode': mode, 'action': action, 'input': kwargs or {},
            'replies': list(replies), 'injected_failure': fail or None, 'result': value, 'calls': calls}


cases = [
    run('browser-default'),
    run('browser-explicit-empty-mode', mode=''),
    run('browser-cookie-ua-proxy', kwargs={'cookies': 'owned=a=b', 'ua': 'owned-UA', 'proxies': {'server': 'http://proxy.invalid:8080'}, 'headless': True, 'timeout': 3}),
    *[run('browser-failure-' + stage, fail=stage) for stage in ['launch', 'new_page', 'goto', 'wait', 'content']],
    run('browser-null-timeout', kwargs={'timeout': None}),
    run('fs-direct-response', mode='FLARESOLVERR', replies=[{'status': 'ok', 'solution': {'response': '<html>owned fs</html>'}}]),
    run('fs-truthy-solution-without-response', mode='flaresolverr', replies=[{'status': 'ok', 'solution': {'cookies': []}}]),
    run('fs-empty-solution-falls-back', mode='flaresolverr', replies=[{'status': 'ok', 'solution': {}}]),
    run('fs-error-falls-back', mode='flaresolverr', replies=[{'status': 'error', 'message': 'owned'}]),
    run('fs-authenticated-session', mode='flaresolverr', kwargs={'proxies': {'server': 'http://proxy.invalid:8080', 'username': 'owned-user', 'password': 'owned-password'}}, replies=[{'status': 'ok'}, {'status': 'ok', 'solution': {'response': 'owned'}}, {'status': 'ok'}]),
    run('fs-create-failure-still-destroys', mode='flaresolverr', kwargs={'proxies': {'server': 'http://proxy.invalid:8080', 'username': 'owned-user'}}, replies=[{'status': 'error'}, {'status': 'ok'}]),
    run('fs-request-exception-still-destroys', mode='flaresolverr', kwargs={'proxies': {'server': 'http://proxy.invalid:8080', 'password': 'owned-password'}}, replies=[{'status': 'ok'}, 'raise', {'status': 'ok'}]),
    run('action-always-browser-and-prefers-fs', action=True, mode='flaresolverr', kwargs={'cookies': 'original=owned', 'ua': 'original-UA'}, replies=[{'status': 'ok', 'solution': {'response': 'ignored', 'cookies': [{'name': 'cleared', 'value': 'owned'}], 'userAgent': 'cleared-UA'}}]),
    run('action-callback-return-survives-context-close-error', action=True, fail='context.close'),
    run('source-page-close-error-skips-context-close', action=True, fail='page.close'),
]
result = {'classification': 'fixed-source ordinary helper AST execution with synthetic browser/HTTP boundaries; not a real browser test',
          'source_commit': SHA, 'source_path': 'app/helper/browser.py',
          'source_sha256': hashlib.sha256(raw).hexdigest(),
          'http_cookie_source_sha256': hashlib.sha256(http_raw).hexdigest(),
          'source_symbol': 'PlaywrightHelper', 'source_lines': [helper.lineno, helper.end_lineno],
          'driver_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'cases': cases}
cookie_ns = dict(Union=typing.Union, quote=quote, unquote=unquote, logger=types.SimpleNamespace(error=lambda *a, **kw: None))
exec(cookie_code, cookie_ns)
result['cookie_cases'] = [{'input': text, 'expected': cookie_ns['cookie_parse'](text, array=True)} for text in ['', 'a=1;a=2', 'space=%20;plus=a+b', 'latin=%E9;utf8=%C3%A9', 'raw=中文%20', 'invalid=%GG;bare', 'equal=a=b', ' = value', 'mixed=%41%ZZ', 'mixed=%2B%GG%41', 'trim=\x1c%41\x1f']]
(OUT / 'browser-source-oracle.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'cases': len(cases), 'source': SHA, 'source_lines': result['source_lines']}))
