#!/usr/bin/env python3
"""Execute the fixed complete ordinary OcrHelper using owned HTTP responses."""
import ast,base64,copy,hashlib,json,pathlib,subprocess,types,typing
OUT=pathlib.Path(__file__).resolve().parent;COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
source=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/helper/ocr.py']);node=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef)and n.name=='OcrHelper')
PNG=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2DTwAAAAASUVORK5CYII=')
rows=[]
def run(label,request,image='png',post=200,result='ABC123',response_json=None,invalid_json=False,host='https://owned-ocr.invalid'):
 trace=[]
 class Response:
  def __init__(self,status,content=b'',payload=None):self.status_code=status;self.content=content;self.payload=payload
  def __bool__(self):return self.status_code<400
  def json(self):
   if invalid_json:raise ValueError('owned malformed JSON')
   return copy.deepcopy(self.payload)
 class Request:
  def __init__(self,**kwargs):self.kwargs=kwargs
  def get_res(self,url):
   trace.append(dict(method='GET',url=url,constructor=self.kwargs))
   if image is None:return None
   data=PNG if image=='png'else b''if image=='empty'else b'owned image error body'
   return Response(503 if image=='503'else 200,data)
  def post_res(self,url,json):
   trace.append(dict(method='POST',url=url,constructor=self.kwargs,body=json))
   if post is None:return None
   return Response(post,payload=response_json if response_json is not None else {'result':result})
 env=dict(vars(typing),base64=base64,settings=types.SimpleNamespace(OCR_HOST=host),RequestUtils=Request)
 exec(compile(ast.fix_missing_locations(ast.Module(body=[copy.deepcopy(node)],type_ignores=[])),COMMIT+':app/helper/ocr.py','exec'),env)
 row=dict(case=label,request=request,images={'png_base64':base64.b64encode(PNG).decode()},post_status=post,post_json=response_json if response_json is not None else {'result':result},invalid_json=invalid_json,image_mode=image,ocr_host=host,trace=trace)
 try:row['expected']=env['OcrHelper']().get_captcha_text(**request)
 except Exception as exc:row['error']=type(exc).__name__
 rows.append(row)
run('empty',{})
run('base64',{'image_b64':'YWJj'})
run('padding',{'image_b64':'YWJjZA'})
run('all_python_whitespace',{'image_b64':' Y W\nJ\tj\u001c\u0085\u00a0ZA\u3000 '})
run('data_url',{'image_b64':' data:image/png;base64,YWJjZA '})
run('uppercase_metadata',{'image_b64':'DATA:IMAGE/PNG;BASE64,YWJjZA'})
run('invalid_base64_is_not_decoded',{'image_b64':'!not-valid!'})
run('whitespace_only',{'image_b64':' \n\t\u001c'})
run('unicode_padding_counts_characters',{'image_b64':'中'})
run('url_overrides_supplied_base64',{'image_url':'https://owned-site.invalid/image.png','image_b64':'ZmFsbGJhY2s=','cookie':'owned-cookie=1','ua':'Owned/OCR'})
run('image_non200_body_still_encoded',{'image_url':'https://owned-site.invalid/image.png','image_b64':'ZmFsbGJhY2s=','cookie':'owned-cookie=1','ua':'Owned/OCR'},image='503')
run('image_empty_stops_even_with_fallback',{'image_url':'https://owned-site.invalid/image.png','image_b64':'ZmFsbGJhY2s='},image='empty')
run('image_network_none_keeps_fallback',{'image_url':'https://owned-site.invalid/image.png','image_b64':'ZmFsbGJhY2s='},image=None)
run('image_network_none_without_fallback',{'image_url':'https://owned-site.invalid/image.png'},image=None)
run('image_data_url_overrides',{'image_url':'data:image/png;base64,YWJjZA','image_b64':'ZmFsbGJhY2s='})
run('empty_data_url_is_failed_get_fallback',{'image_url':'data:image/png;base64,','image_b64':'YWJj'},image=None)
run('wrong_data_url_is_failed_get_fallback',{'image_url':'data:text/plain;base64,YWJj','image_b64':'YWJj'},image=None)
run('empty_service',{'image_b64':'YWJj'},host='',post=None)
run('post_network_none',{'image_b64':'YWJj'},post=None)
run('post_non200_falsey',{'image_b64':'YWJj'},post=503)
run('post_302_truthy_without_redirect',{'image_b64':'YWJj'},post=302)
for name,value in [('null',None),('zero',0),('false',False),('empty_list',[]),('empty_object',{}),('empty_text',''),('true',True),('integer',123456),('list',['A','B']),('object',{'owned':'text'})]:run('result_'+name,{'image_b64':'YWJj'},result=value)
run('post_missing_result',{'image_b64':'YWJj'},response_json={'owned':'no result'})
run('post_json_array_raises',{'image_b64':'YWJj'},response_json=['owned'])
run('post_json_invalid_raises',{'image_b64':'YWJj'},invalid_json=True)
manifest=dict(source_commit=COMMIT,source_path='app/helper/ocr.py:8-75',source_sha256=hashlib.sha256(source).hexdigest(),classification='actual fixed complete helper AST, synthetic owned PNG/HTTP Response boundary; no app imports, old config/DB reads, network calls or challenge execution',cases=rows)
(OUT/'oracle.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(cases=len(rows),source_errors=sum('error'in r for r in rows))))
