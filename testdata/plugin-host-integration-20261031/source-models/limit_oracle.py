#!/usr/bin/env python3
import ast,asyncio,functools,hashlib,inspect,json,pathlib,subprocess,threading,types
from typing import *
COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
def source(path):return subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':'+path],text=True)
class LimitException(Exception):pass
class RateLimitExceededException(LimitException):pass
class ImmediateException(Exception):pass
clock=[1000.];sleeps=[]
time=types.SimpleNamespace(time=lambda:clock[0],sleep=lambda seconds:(sleeps.append(seconds),clock.__setitem__(0,clock[0]+seconds)))
ns=dict(time=time,threading=threading,functools=functools,inspect=inspect,asyncio=asyncio,wraps=functools.wraps,LimitException=LimitException,RateLimitExceededException=RateLimitExceededException,ImmediateException=ImmediateException,Any=Any,Tuple=Tuple,List=List,Callable=Callable,Optional=Optional,logger=types.SimpleNamespace(info=lambda *a:None,warning=lambda *a:None,error=lambda *a:None,warn=lambda *a:None))
paths=['app/utils/limit.py','app/utils/common.py'];hashes={}
for path in paths:
 text=source(path);hashes[path]=hashlib.sha256(text.encode()).hexdigest();nodes=[n for n in ast.parse(text).body if isinstance(n,(ast.ClassDef,ast.FunctionDef)) and n.name in ['BaseRateLimiter','ExponentialBackoffRateLimiter','rate_limit_handler','rate_limit_exponential','retry']];exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),path,'exec'),ns)
limiters={name:ns['ExponentialBackoffRateLimiter'](source=name) for name in ['match','detail']}
called={name:0 for name in limiters};mode=['limit']
def make_call(name):
 def call():
  called[name]+=1
  if mode[0]=='limit':raise LimitException('owned')
  if mode[0]=='empty':return None
  return {'ok':True}
 return ns['rate_limit_handler'](limiters[name])(call)
wrapped={name:make_call(name) for name in limiters}
events=[]
for now,name,op in [(1000,'detail','limit'),(1001,'detail','ok'),(1001,'match','ok'),(1060,'detail','limit'),(1180,'detail','limit'),(1420,'detail','limit'),(1900,'detail','limit'),(2500,'detail','ok'),(2501,'detail','limit'),(2561,'detail','empty'),(2562,'detail','limit')]:
 clock[0]=now;mode[0]=op;result=wrapped[name]();limiter=limiters[name];events.append(dict(now=now,method=name,operation=op,result=result,calls=called[name],next=limiter.next_allowed_time,wait=limiter.current_wait))
retry_cases=[]
for failures,limited in [(0,False),(2,False),(5,False),(1,True)]:
 clock[0]=0;sleeps.clear();attempts=[0];limiter=ns['ExponentialBackoffRateLimiter']()
 def invoke():
  attempts[0]+=1
  if attempts[0]<=failures:
   if limited:raise LimitException('owned')
   raise ValueError('owned')
  return {'ok':True}
 wrapped_retry=ns['retry'](Exception,5,3,3,logger=ns['logger'])(ns['rate_limit_handler'](limiter)(invoke))
 try:result=wrapped_retry();error=None
 except Exception as exc:result=None;error=type(exc).__name__
 retry_cases.append(dict(failures=failures,limited=limited,calls=attempts[0],sleeps=list(sleeps),result=result,error=error))
out={'commit':COMMIT,'source_sha256':hashes,'kind':'fixed rate limiter/handler/retry source AST with owned clock/log/call boundary','events':events,'retry_cases':retry_cases}
pathlib.Path(__file__).with_name('douban-limit-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'events':len(events),'retry_cases':len(retry_cases)}))
