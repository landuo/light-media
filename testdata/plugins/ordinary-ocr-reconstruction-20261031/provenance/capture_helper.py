"""New finite source-class capture. No source app import, sockets or requests.
Response objects are explicit deterministic fixtures, not requests/HTTP proof.
"""
import ast,base64,hashlib,json,math,sys,types,typing
from pathlib import Path
N=Path('/workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305')
S=Path('/workspace/scratch/a1d82674c6f8/plugins-cloud-20261028/source/MoviePilot/app/helper/ocr.py')
raw=S.read_bytes();module=ast.parse(raw);node=next(n for n in module.body if isinstance(n,ast.ClassDef) and n.name=='OcrHelper')
class OwnedResponse:
 def __init__(self,body=b'',status=200):self.content=body;self.status_code=status
 def __bool__(self):return not 400<=self.status_code<600
 def json(self):return json.loads(self.content.decode('utf-8'))
state={}
class OwnedRequestUtils:
 def __init__(self,**kw):self.kw=kw
 def get_res(self,url):state['calls'].append({'method':'GET','url':url,'options':self.kw});return state['get']
 def post_res(self,**kw):state['calls'].append({'method':'POST','options':self.kw,**kw});return state['post']
ns={'base64':base64,'Optional':typing.Optional,'settings':types.SimpleNamespace(OCR_HOST='https://owned.invalid'),'RequestUtils':OwnedRequestUtils}
exec(compile(ast.Module(body=[node],type_ignores=[]),str(S),'exec'),ns)
Helper=ns['OcrHelper']
def value(v):
 if v is None:return {'kind':'null'}
 if isinstance(v,bool):return {'kind':'bool','bool':v}
 if isinstance(v,int):return {'kind':'int','number':str(v)}
 if isinstance(v,float):return {'kind':'float','number':'nan' if math.isnan(v) else '+inf' if v==math.inf else '-inf' if v==-math.inf else repr(v)}
 if isinstance(v,str):return {'kind':'string','points':[ord(c) for c in v]}
 if isinstance(v,list):return {'kind':'list','items':[value(i) for i in v]}
 if isinstance(v,dict):return {'kind':'object','entries':[{'key':[ord(c) for c in k],'value':value(x)} for k,x in v.items()]}
 raise TypeError(type(v))
def observe(name,inp,get=None,post=b'{"result":"OK"}',status=200):
 state.clear();state.update(calls=[],get=get,post=None if post is None else OwnedResponse(post,status))
 row={'name':name,'input':inp,'get':None if get is None else {'status':get.status_code,'body_b64':base64.b64encode(get.content).decode()},'post':None if post is None else {'status':status,'body_b64':base64.b64encode(post).decode()}}
 try:row['value']=value(Helper().get_captcha_text(**inp))
 except Exception as e:row['error_class']=type(e).__name__
 row['calls']=list(state['calls']);return row
rows=[]
for name,text in [('none',None),('empty',''),('spaces',' Q Q\t\n'),('control','\x1cQ\x1fQ'),('unicode','\u3000Q\u0085Q'),('padding1','A'),('padding2','AA'),('padding3','AAA'),('already_padding','QQ=='),('invalid_alphabet','@@'),('data',' DATA:IMAGE/PNG;BASE64, Q Q '),('empty_data','data:image/png;base64,'),('missing_base64','data:image/png,QQ')]:
 rows.append(observe('normalize_'+name,{'image_b64':text}))
rows += [observe('data_url_precedence',{'image_url':'data:image/png;base64,Qg','image_b64':'QQ'}),observe('image_precedence',{'image_url':'http://owned.invalid/image','image_b64':'QQ'},OwnedResponse(b'B')),observe('image_missing_fallback',{'image_url':'http://owned.invalid/image','image_b64':'QQ'}),observe('image_missing_empty',{'image_url':'http://owned.invalid/image'}),observe('image_http404',{'image_url':'http://owned.invalid/image'},OwnedResponse(b'B',404)),observe('image_empty_no_fallback',{'image_url':'http://owned.invalid/image','image_b64':'QQ'},OwnedResponse(b'')),observe('image_cookie_ua',{'image_url':'http://owned.invalid/image','cookie':'owned=x','ua':'owned-UA'},OwnedResponse(b'B')),observe('post_none',{'image_b64':'QQ'},post=None),observe('post_badjson',{'image_b64':'QQ'},post=b'broken')]
for name,data in [('null',b'null'),('list',b'[]'),('missing',b'{}'),('false',b'{"result":false}'),('zero',b'{"result":0}'),('empty_list',b'{"result":[]}'),('truthy_list',b'{"result":[1]}'),('truthy_object',b'{"result":{"a":true}}')]:rows.append(observe('result_'+name,{'image_b64':'QQ'},post=data))
status=[observe('post_status_'+str(code),{'image_b64':'QQ'},status=code) for code in [0,199,200,204,399,400,499,500,599,600]]
extended=[]
for name,body in [('big_integer','{"result":123456789012345678901234567890}'),('nan','{"result":NaN}'),('positive_inf','{"result":Infinity}'),('negative_inf','{"result":-Infinity}'),('overflow','{"result":1e400}'),('negative_zero','{"result":-0.0}'),('fraction','{"result":0.12345678901234567}'),('surrogate','{"result":"\\ud800X\\ud83d\\ude00"}'),('duplicate','{"result":1,"result":2}'),('nested','{"result":{"x":[true,null,"A"]}}'),('int_over_limit','{"result":'+('1'*4301)+'}')]:extended.append(observe(name,{'image_b64':'QQ'},post=body.encode()))
meta={'kind':'fixed source OcrHelper AST with explicit owned response fixtures; not requests transport or full source app','source_file':str(S),'source_sha256':hashlib.sha256(raw).hexdigest(),'python':sys.version,'integer_digit_limit':sys.get_int_max_str_digits(),'recursion_limit':sys.getrecursionlimit(),'fixed_endpoint':'https://owned.invalid/captcha/base64','network_executed':False}
for name,data in [('helper-source.json',rows),('status-truthiness-source.json',status),('extended-http-source.json',extended)]:
 out=N/'light-media/internal/ocr/testdata'/name;out.parent.mkdir(parents=True,exist_ok=True)
 with out.open('x') as f:json.dump({'meta':meta,'cases':data},f,ensure_ascii=True,indent=2);f.write('\n')
 print(name,len(data),hashlib.sha256(out.read_bytes()).hexdigest())
