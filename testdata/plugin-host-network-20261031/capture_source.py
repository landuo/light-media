#!/usr/bin/env python3
"""Execute the pinned finite RequestUtils constructor/GET path without network."""
import ast,base64,hashlib,json,pathlib,subprocess,types,typing
ROOT=pathlib.Path(__file__).resolve().parents[2]
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/utils/http.py'])
tree=ast.parse(raw); cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='RequestUtils')
cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ['__init__','request','get_res']]
class RequestException(Exception): pass
class ConnectionError(RequestException): pass
class ReadTimeout(RequestException): pass
class ChunkedEncodingError(RequestException): pass
exc=types.SimpleNamespace(**{n:globals()[n] for n in ['RequestException','ConnectionError','ReadTimeout','ChunkedEncodingError']})
calls=[]; outcome=None
def request(method,url,**kwargs):
    calls.append({'method':method,'url':url,**kwargs})
    if isinstance(outcome,Exception): raise outcome
    return outcome
logger=types.SimpleNamespace(debug=lambda *a,**k:None)
env={'__name__':'owned_source','requests':types.SimpleNamespace(request=request,exceptions=exc),'settings':types.SimpleNamespace(USER_AGENT='owned-UA'),'get_caller':lambda:None,'cookie_parse':lambda value:value,'logger':logger,'_REQUESTS_RETRY_IDEMPOTENT_METHODS':('GET','HEAD','OPTIONS'),'Response':object,'Session':object,'Union':typing.Union,'Optional':typing.Optional,'Any':typing.Any}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'fixed:http.py','exec'),env)
cases=[]
for name,status,content in [('ok',200,b'owned\x00text'),('empty',200,b''),('created',201,b'created'),('forbidden',403,b'not allowed'),('missing',404,b'not found'),('invalid_utf8',200,b'\xff\xfe')]:
    outcome={'status_code':status,'content_b64':base64.b64encode(content).decode()};calls.clear()
    value=env['RequestUtils'](proxies={'http':'http://owned-proxy.test:8888','https':'http://owned-proxy.test:8888'},timeout=15).get_res('http://owned.test/'+name)
    cases.append({'name':name,'response':value,'calls':calls.copy()})
for name,error in [('connection',ConnectionError()),('read_timeout',ReadTimeout()),('chunked',ChunkedEncodingError()),('generic',RequestException())]:
    outcome=error;calls.clear();value=env['RequestUtils'](timeout=15).get_res('http://owned.test/'+name)
    cases.append({'name':name,'response':value,'calls':calls.copy()})
out={'commit':COMMIT,'source':'app/utils/http.py:329–560','sha256':hashlib.sha256(raw).hexdigest(),'classification':'pinned AST with owned request outcomes; no source or target live service executed','cases':cases}
pathlib.Path(__file__).with_name('source-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(cases),'source_sha256':out['sha256']}))
