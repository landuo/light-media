#!/usr/bin/env python3
"""Offline evidence only. Executes frozen source AST; never imports Requests or real os.
Run with --freeze SOURCE_DIR once, then without arguments to replay frozen excerpts.
No networking, live application, process environment, or production Python dependency.
"""
import ast
import collections
import collections.abc
import contextlib
import copy
import gzip
import hashlib
import json
import pathlib
import socket
import struct
import sys
import types
import typing
import urllib.parse

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parents[2]
FROZEN = HERE / 'frozen-source.json'
EXPECTED = {
    'utils.py': '657fd02343b2586bbd138a4ec0f03ceeb7610c2712f96ac54f28a708bf7d079c',
    'sessions.py': '96fbb30bbbf06a59a5268d13b57885149756aa3f31695b5c15e41dd7bb2f67a6',
    '_types.py': 'd06df79f54a279c0cc3faec596c446246d5ad14edfd529eb328defbd62a44a89',
}
UTILS = ['to_key_val_list', 'address_in_network', 'dotted_netmask', 'is_ipv4_address',
         'is_valid_cidr', 'set_environ', 'should_bypass_proxies', 'get_environ_proxies',
         'select_proxy', 'resolve_proxies']

def extract(text, names, cls=None):
    tree = ast.parse(text)
    nodes = tree.body if cls is None else next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == cls).body
    # Last implementation wins over preceding @overload declarations.
    found = {n.name: n for n in nodes if isinstance(n, (ast.FunctionDef, ast.ClassDef)) and n.name in names}
    assert set(found) == set(names)
    lines = text.splitlines(keepends=True)
    return {name: ''.join(lines[min([n.lineno] + [d.lineno for d in n.decorator_list])-1:n.end_lineno]) for name,n in found.items()}

if len(sys.argv) == 3 and sys.argv[1] == '--freeze':
    src = pathlib.Path(sys.argv[2]); sources = {}; hashes = {}
    for filename, expected in EXPECTED.items():
        raw = (src / filename).read_bytes(); hashes[filename] = hashlib.sha256(raw).hexdigest()
        assert hashes[filename] == expected
        sources[filename] = raw.decode()
    funcs = extract(sources['utils.py'], UTILS)
    funcs.update(extract(sources['sessions.py'], ['merge_setting']))
    funcs.update(extract(sources['sessions.py'], ['merge_environment_settings'], 'Session'))
    # Capture actual Session.request normalization assignment, not a reference rewrite.
    session = next(n for n in ast.parse(sources['sessions.py']).body if isinstance(n, ast.ClassDef) and n.name == 'Session')
    request = next(n for n in session.body if isinstance(n, ast.FunctionDef) and n.name == 'request')
    normalize = next(n for n in request.body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == 'proxies' for t in n.targets))
    normalization = ast.get_source_segment(sources['sessions.py'], normalize)
    raw = (src / '_types.py').read_bytes(); hashes['_types.py'] = hashlib.sha256(raw).hexdigest()
    funcs.update(extract(raw.decode(), ['SupportsItems']))
    original = json.loads(gzip.decompress((ROOT / 'testdata/plugins/native-requests-proxy/verification/reference-original-linux-proxy-source.json.gz').read_bytes()))
    funcs.update(original['functions'])
    data = {'requests_version':'2.34.2', 'full_source_sha256': hashes,
        'utils_attribution':'Exact SHA-256 match to original installed utils.py; official v2.34.2 source.',
        'sessions_attribution':'Official v2.34.2 tag source only; installed sessions.py exact hash NOT verified.',
        'types_attribution':'Official v2.34.2 tag source dependency, not installed-file hash verification.',
        'urllib_attribution':{'python':original['python'], 'full_source_sha256':original['urllib_sha256'], 'capture':'reference-original-linux-proxy-source.json.gz'},
        'upstream':'https://github.com/psf/requests/tree/v2.34.2/src/requests',
        'licenses':['internal/networkproxy/licenses/Requests-Apache-2.0.txt','internal/networkproxy/licenses/CPython-PSF.txt'],
        'normalization':normalization, 'functions':funcs}
    FROZEN.write_text(json.dumps(data, ensure_ascii=False, indent=2)+'\n')

frozen = json.loads(FROZEN.read_text())
class StripAnnotations(ast.NodeTransformer):
    def visit_FunctionDef(self, node):
        node.returns = None
        for a in node.args.posonlyargs + node.args.args + node.args.kwonlyargs:
            a.annotation = None
        if node.args.vararg: node.args.vararg.annotation = None
        if node.args.kwarg: node.args.kwarg.annotation = None
        return self.generic_visit(node)
    def visit_AnnAssign(self, node):
        return ast.copy_location(ast.Assign(targets=[node.target], value=node.value or ast.Constant(None)), node)

fake_os = types.SimpleNamespace(environ={})
ns = dict(os=fake_os, contextlib=contextlib, socket=socket, struct=struct,
          urlparse=urllib.parse.urlparse, _splitport=urllib.parse._splitport,
          OrderedDict=collections.OrderedDict, Mapping=collections.abc.Mapping,
          cast=typing.cast, Protocol=typing.Protocol, runtime_checkable=typing.runtime_checkable,
          Generic=typing.Generic, _KT_co=typing.TypeVar('_KT_co', covariant=True),
          _VT_co=typing.TypeVar('_VT_co', covariant=True))
# Execution order supplies exact source dependencies; no source imports run.
for name in ['SupportsItems'] + list(frozen['functions']):
    if name == 'SupportsItems' and name in ns: continue
    text = __import__('textwrap').dedent(frozen['functions'][name])
    tree = ast.fix_missing_locations(StripAnnotations().visit(ast.parse(text)))
    exec(compile(tree, '<frozen-source:'+name+'>', 'exec'), ns)
    if name == 'SupportsItems': ns['_SupportsItems'] = ns[name]
ns['getproxies'] = ns['getproxies_environment']
ns['proxy_bypass'] = ns['proxy_bypass_environment']

P = 'http://explicit.invalid:8100'; E = 'http://environment.invalid:8200'
cases = []
def add(name, env, explicit, urls=None, session=None, trust=True, mode='request', supported=True):
    cases.append(dict(name=name, environment=env, explicit=explicit, session={} if session is None else session,
                      trust_env=trust, mode=mode, urls=urls or ['https://target.invalid/start'], go_supported=supported))

for envname, env in [
    ('empty',{}), ('upper',{'HTTP_PROXY':E,'HTTPS_PROXY':E,'ALL_PROXY':'http://all.invalid:8300'}),
    ('lower-wins',{'HTTPS_PROXY':E,'https_proxy':'http://lower.invalid:8400'}),
    ('lower-empty',{'HTTPS_PROXY':E,'https_proxy':''}),
    ('cgi',{'REQUEST_METHOD':'GET','HTTP_PROXY':E,'HTTPS_PROXY':E}),
    ('cgi-lower',{'REQUEST_METHOD':'GET','HTTP_PROXY':E,'http_proxy':P}),
    ('no-proxy-upper',{'HTTPS_PROXY':E,'NO_PROXY':'target.invalid'}),
    ('no-proxy-lower',{'HTTPS_PROXY':E,'NO_PROXY':'target.invalid','no_proxy':'other.invalid'}),
    ('no-proxy-empty-lower',{'HTTPS_PROXY':E,'NO_PROXY':'target.invalid','no_proxy':''}),
    ('all',{'ALL_PROXY':'http://all.invalid:8300'}),
]:
    for explicitname, explicit in [('none',None),('empty',{}),('value',{'https':P}),('present-empty',{'https':''}),
        ('no-proxy-empty',{'no_proxy':''}),('no-proxy-value',{'no_proxy':'target.invalid'}),
        ('host-priority',{'https://target.invalid':P,'https':E,'all://target.invalid':'http://hostall.invalid:8500','all':'http://all.invalid:8300'}),
        ('all-host-priority',{'all://target.invalid':P,'all':E})]:
        add(envname+'-'+explicitname,env,explicit,['https://target.invalid/start','http://other.invalid/next','https://target.invalid/end'])
for explicit in [{'https':None},{'no_proxy':None},{'https://target.invalid':None,'https':P}]:
    add('nullable-'+str(len(cases)),{'HTTPS_PROXY':E},explicit,supported=False)
for mode in ['request','merge_method']:
    for explicit in [None,{}, {'https':None}, {'https':''}, {'https':P}]:
        add('session-'+mode+'-'+str(len(cases)), {'HTTPS_PROXY':E},explicit,session={'https':'http://session.invalid:8600'},mode=mode,supported=False)
for trust in [True,False]:
    add('trust-'+str(trust),{'HTTPS_PROXY':E},{},trust=trust,supported=trust)
add('bypass-retains-prior-map',{'HTTPS_PROXY':E,'NO_PROXY':'bypass.invalid'},{},['https://target.invalid','https://bypass.invalid','https://other.invalid'])
add('initial-bypass-then-resolve',{'HTTPS_PROXY':E,'NO_PROXY':'target.invalid'},{},['https://target.invalid','https://other.invalid','https://target.invalid'])
add('explicit-no-proxy-override',{'HTTPS_PROXY':E,'NO_PROXY':'*'},{'no_proxy':'other.invalid'},['https://target.invalid','https://other.invalid'])
add('ipv4-cidr',{'HTTPS_PROXY':E,'NO_PROXY':'192.0.2.0/24'},{},['https://192.0.2.8','https://198.51.100.8','https://192.0.2.9'])
add('invalid-port-authentic-error',{'HTTPS_PROXY':E,'NO_PROXY':'target.invalid'},{},['https://target.invalid:bad'],supported=False)

for case in cases:
    fake_os.environ = dict(case['environment'])
    session = types.SimpleNamespace(proxies=copy.deepcopy(case['session']),trust_env=case['trust_env'],stream=False,verify=True,cert=None)
    local = {'proxies':copy.deepcopy(case['explicit'])}
    if case['mode'] == 'request': exec(frozen['normalization'],{},local)
    hops = []
    try:
        proxies = ns['merge_environment_settings'](session,case['urls'][0],local['proxies'],None,None,None)['proxies']
        for index,url in enumerate(case['urls']):
            if index: proxies = ns['resolve_proxies'](types.SimpleNamespace(url=url), proxies,case['trust_env'])
            hops.append({'url':url,'proxies':copy.deepcopy(proxies),'selected':ns['select_proxy'](url,proxies)})
        case['expected'] = hops
    except Exception as error:
        case['expected'] = hops
        case['expected_error'] = {'type':type(error).__name__,'message':str(error)}
    assert fake_os.environ == case['environment'], 'source must restore synthetic environment'

result = {'kind':'synthetic frozen-source AST execution, not real service verification',
          'source_file':'frozen-source.json','source_sha256':hashlib.sha256(FROZEN.read_bytes()).hexdigest(),
          'scope':'Go comparisons only request mode, empty Session.proxies, trust_env=true, no null-valued explicit entries. Other cases preserve source evidence without parity claims.',
          'cases':cases}
(HERE / 'oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'go_supported':sum(c['go_supported'] for c in cases),'errors':sum('expected_error' in c for c in cases)}))
