"""Pinned cached/TMDb/RequestUtils bodies with actual cachetools7.1.4.

Owned finite response/session/clock fixtures avoid provider requests and waits.
This measures source HTTP status, snapshot, fresh, rate-limit and retry order.
"""
import ast
from collections import defaultdict
from contextlib import contextmanager
import contextvars
from copy import deepcopy
from datetime import datetime
from functools import wraps
import hashlib
import inspect
import json
import subprocess
from types import SimpleNamespace
from typing import Any,Optional
import cachetools
from cachetools.keys import hashkey

SHA='8a83efeacd3e26b82be495e17d50146e31c96a60';REPO='/Users/accidia/projects/MoviePilot';sources={};clock=[1000.,10.];regions={}
class Backend:
    def __init__(self,cache_type,maxsize,ttl):self.maxsize,self.ttl=maxsize,ttl
    def cache(self,region):
        if region not in regions:regions[region]=cachetools.TLRUCache(self.maxsize,ttu=lambda _,__,now:now+self.ttl,timer=lambda:clock[1])
        return regions[region]
    def get(self,key,region):return self.cache(region).get(key)
    def set(self,key,value,region,**_):self.cache(region)[key]=value
    def exists(self,key,region):return key in self.cache(region)
    def clear(self,region):self.cache(region).clear()
    def delete(self,key,region):self.cache(region).pop(key,None)
env=dict(__name__='fixed_provider_oracle',Any=Any,Optional=Optional,inspect=inspect,wraps=wraps,hashkey=hashkey,Cache=Backend,contextmanager=contextmanager,_fresh=contextvars.ContextVar('owned-fresh',default=False),settings=SimpleNamespace(CONF=SimpleNamespace(tmdb=256,meta=3600)),logger=SimpleNamespace(debug=lambda *a:None,warning=lambda *a:None),datetime=datetime,deepcopy=deepcopy)
def source(path):
    raw=subprocess.check_output(['git','-C',REPO,'show',SHA+':'+path]);sources[path]=hashlib.sha256(raw).hexdigest();return ast.parse(raw)
cache_tree=source('app/core/cache.py')
nodes=[node for node in cache_tree.body if isinstance(node,ast.FunctionDef) and node.name in ['cached','fresh','is_fresh']]
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),'cache.py','exec'),env)
class RequestException(Exception):pass
class ConnectionError(RequestException):pass
env['requests']=SimpleNamespace(exceptions=SimpleNamespace(RequestException=RequestException,ConnectionError=ConnectionError,ChunkedEncodingError=ConnectionError,ReadTimeout=ConnectionError))
env['Response']=SimpleNamespace;env['_REQUESTS_RETRY_IDEMPOTENT_METHODS']={'GET','HEAD','OPTIONS','PUT','DELETE','TRACE'}
def extract(tree,name,methods):
    cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==name)
    nodes=[node for node in cls.body if isinstance(node,ast.FunctionDef) and node.name in methods]
    exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name=name,bases=[],keywords=[],body=nodes,decorator_list=[])],type_ignores=[])),name,'exec'),env);return env[name]
http_type=extract(source('app/utils/http.py'),'RequestUtils',{'request','get_res','post_res'})
env['TMDbException']=type('TMDbException',(Exception,),{})
tmdb_type=extract(source('app/modules/themoviedb/tmdbv3api/tmdb.py'),'TMDb',{'api_key','domain','language','wait_on_rate_limit','request','_request_once','_reset_owned_session','_snapshot_response','_get_response_headers','_get_response_json','_decode_response_json','_get_header_value','_get_json_key','_validate_json_response','_build_invalid_json_message','_handle_errors','_handle_headers','_handle_rate_limit','_process_json_response','_request_obj','_build_url','_validate_api_key'})
tmdb_type._RESPONSE_SNAPSHOT_MARKER='__mp_tmdb_response_snapshot__'
cases=[
 dict(case='valid_status500_snapshot',replies=[dict(status=500,body={'results':[]})],calls=[{},{}]),
 dict(case='error_envelope_cached',replies=[dict(status=404,body={'success':False,'status_message':'owned miss'})],calls=[{},{}]),
 dict(case='malformed_not_cached',replies=[dict(status=502,body='owned nonJSON'),dict(status=502,body='owned nonJSON')],calls=[{},{}]),
 dict(case='network_four_attempts_each',replies=[None]*8,calls=[{},{}]),
 dict(case='generic_request_two_attempts',replies=[dict(exception='generic')]*2,calls=[{}]),
 dict(case='rate_limit_fresh_retry',replies=[dict(body={'results':[]},headers={'X-RateLimit-Remaining':'0','X-RateLimit-Reset':'1001'}),dict(body={'results':[{'id':2}]},headers={'X-RateLimit-Remaining':'40'})],calls=[{},{}]),
 dict(case='past_reset_abs_delay',replies=[dict(body={'results':[]},headers={'X-RateLimit-Remaining':'0','X-RateLimit-Reset':'999'}),dict(body={'results':[{'id':3}]},headers={'X-RateLimit-Remaining':'40'})],calls=[{}]),
 dict(case='reset_now_no_retry',replies=[dict(body={'results':[]},headers={'X-RateLimit-Remaining':'0','X-RateLimit-Reset':'1000'})],calls=[{}]),
 dict(case='missing_reset_cached_error',replies=[dict(body={'results':[]},headers={'X-RateLimit-Remaining':'0'})],calls=[{},{}]),
 dict(case='fresh_replaces_snapshot',replies=[dict(body={'results':[{'id':1}]}),dict(body={'results':[{'id':2}]})],calls=[{},dict(fresh=True),{}]),
]
results=[]
for case in cases:
    clock[:]=[1000.,10.];regions.clear();remaining=deepcopy(case['replies']);trace=[];sleeps=[]
    class Response:
        def __init__(self,row):self.status_code=row.get('status',200);self.headers=row.get('headers',{});self.payload=row['body'];self.text=self.payload if isinstance(self.payload,str) else json.dumps(self.payload)
        def json(self):
            if isinstance(self.payload,str):raise ValueError('owned nonJSON')
            return deepcopy(self.payload)
    class Session:
        def request(self,method,url,**kwargs):
            trace.append('request');assert remaining,(case['case'],'exhausted source responses')
            reply=remaining.pop(0)
            if reply is None:raise ConnectionError('owned connection failure')
            if reply.get('exception')=='generic':raise RequestException('owned generic request failure')
            return Response(reply)
        def close(self):trace.append('close-session')
    api=tmdb_type();api._api_key='owned';api._domain='api.themoviedb.org';api._language='zh';api._remaining=40;api._reset=None;api._wait_on_rate_limit=True;api._owns_session=True
    def init_session():
        api._session=Session();api._req=http_type();api._req._session=api._session;api._req._headers={};api._req._cookies=None;api._req._proxies=None;api._req._timeout=20
    api._init_session=init_session;init_session()
    def sleep(seconds):sleeps.append(seconds);clock[0]+=seconds;clock[1]+=seconds
    env['time']=SimpleNamespace(time=lambda:clock[0],sleep=sleep)
    observations=[]
    for call in case['calls']:
        try:
            with env['fresh'](call.get('fresh',False)):value=api._request_obj('/search/multi','query=Owned&page=1',key='results')
            observations.append(dict(value=value,error=False))
        except Exception:observations.append(dict(value=None,error=True))
    assert not remaining,(case['case'],remaining)
    results.append(dict(**case,observations=observations,requests=trace.count('request'),session_closes=trace.count('close-session'),sleeps=sleeps))
generic_cases=[
 dict(case='cache_empty',skip_empty=False,operations=[dict(result={}),dict(result={'new':True})]),
 dict(case='skip_empty',skip_empty=True,operations=[dict(result=value) for value in [{},[],None,False,0,'',{'id':1},{'id':2}]]),
 dict(case='none_retries',skip_empty=False,operations=[dict(result=value) for value in [None,None,{'id':1},{'id':2}]]),
 dict(case='fresh_overwrites',skip_empty=False,operations=[dict(result={'id':1}),dict(result={'id':2},fresh=True),dict(result={'id':3})]),
 dict(case='monotonic_expiry',skip_empty=False,operations=[dict(result={'id':1}),dict(result={'id':2},mono=4000)]),
 dict(case='full_key_identity',skip_empty=False,operations=[dict(key='a',result={'id':1}),dict(key='b',result={'id':2}),dict(key='a',result={'id':3})]),
]
generic_results=[]
for case in generic_cases:
    clock[:]=[1000.,10.];regions.clear();calls=[];current=None
    @env['cached'](region='owned-generic',maxsize=256,ttl=3600,skip_empty=case['skip_empty'])
    def getter(key):calls.append(key);return deepcopy(current)
    observed=[]
    for operation in case['operations']:
        if 'mono' in operation:clock[1]=operation['mono']
        current=operation['result']
        with env['fresh'](operation.get('fresh',False)):value=getter(operation.get('key','owned'))
        observed.append(dict(value=value,loads=len(calls)))
    generic_results.append(dict(**case,observations=observed))
print(json.dumps(dict(source_commit=SHA,cachetools_version=cachetools.__version__,classification='unchanged pinned core.cached + TMDb request/snapshot/rate bodies and RequestUtils.request with actual cachetools; owned finite HTTP/session/clock fixtures; no real provider',sources=sources,cases=results,generic_cases=generic_results),indent=2))
