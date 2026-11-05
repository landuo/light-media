#!/usr/bin/env python3
"""Run fixed response parsing and RequestUtils retry methods with owned peers."""
import ast, hashlib, json, pathlib, subprocess, types

COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
hashes={}
def tree(path):
    raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True)
    hashes[path]=hashlib.sha256(raw.encode()).hexdigest()
    return ast.parse(raw)
def extract(path, cls, methods, ns):
    node=next(n for n in tree(path).body if isinstance(n,ast.ClassDef) and n.name==cls)
    node.bases=[];node.keywords=[];node.decorator_list=[]
    node.body=[n for n in node.body if isinstance(n,ast.FunctionDef) and n.name in methods]
    for n in node.body:n.decorator_list=[d for d in n.decorator_list if isinstance(d,ast.Name) and d.id in ['staticmethod','classmethod']]
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),node],type_ignores=[])),path,'exec'),ns)
    return ns[cls]
class Response:
    def __init__(self,status,body):self.status_code=status;self.body=body
    def json(self):return json.loads(self.body)
douban=extract('app/modules/douban/apiv2.py','DoubanApi',{'_handle_response'}, {})
bangumi=extract('app/modules/bangumi/bangumi.py','BangumiApi',{'__invoke'}, {})
responses=[]
for source in ['douban','bangumi']:
    for status in [200,404,429,500]:
        for body in ['{"owned":true}','{']:
            response=Response(status,body)
            api=bangumi();api._base_url='https://owned.invalid/';api._req=types.SimpleNamespace(get_res=lambda **kwargs:response)
            try:
                result=douban._handle_response(response) if source=='douban' else api._BangumiApi__invoke('owned')
                error=None
            except Exception as exc:result=None;error=type(exc).__name__
            responses.append(dict(source=source,status=status,body=body,expected=result,error=error))
class RequestException(Exception):pass
class ConnectionError(RequestException):pass
class ChunkedEncodingError(RequestException):pass
class ReadTimeout(RequestException):pass
exceptions=types.SimpleNamespace(RequestException=RequestException,ConnectionError=ConnectionError,ChunkedEncodingError=ChunkedEncodingError,ReadTimeout=ReadTimeout)
requests=types.SimpleNamespace(exceptions=exceptions)
ns=dict(requests=requests,logger=types.SimpleNamespace(debug=lambda *a:None),_REQUESTS_RETRY_IDEMPOTENT_METHODS={'GET','HEAD','OPTIONS','PUT','DELETE','TRACE'})
utility=extract('app/utils/http.py','RequestUtils',{'request'},ns)
retries=[]
for name in ['ConnectionError','ChunkedEncodingError','ReadTimeout','RequestException']:
    for success_after_failure in [True,False]:
        calls=[];closed=[]
        def request(*args,**kwargs):
            calls.append(1)
            if len(calls)==1 or not success_after_failure:raise getattr(exceptions,name)('owned')
            return Response(200,'{}')
        api=utility();api._session=types.SimpleNamespace(request=request,close=lambda:closed.append(1));api._headers={};api._cookies={};api._proxies={};api._timeout=1
        result=api.request('GET','https://owned.invalid/')
        retries.append(dict(failure=name,recovery=success_after_failure,calls=len(calls),closed=len(closed),returned=result is not None))
identities={}
for module,cls in [('themoviedb','TheMovieDbModule'),('douban','DoubanModule'),('bangumi','BangumiModule'),('anilist','AniListModule')]:
    path='app/modules/'+module+'/__init__.py'
    node=next(n for n in tree(path).body if isinstance(n,ast.ClassDef) and n.name==cls)
    method=next(n for n in node.body if isinstance(n,ast.FunctionDef) and n.name=='get_name')
    identities[module]={'module_id':cls,'module_name':ast.literal_eval(next(n for n in method.body if isinstance(n,ast.Return)).value)}
out={'commit':COMMIT,'source_sha256':hashes,'kind':'fixed source methods; synthetic response/network exception boundaries, no public-provider success claim','responses':responses,'retries':retries,'identities':identities}
pathlib.Path(__file__).with_name('http-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'responses':len(responses),'retries':len(retries),'identities':len(identities)}))
