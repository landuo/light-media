"""Actual fixed RequestUtils XML decoding AST; owned public chardet, no IO."""
import ast,json,pathlib,sys,types,typing,platform
p=pathlib.Path(__file__).resolve().parent
runtime=pathlib.Path(sys.argv[1]).resolve();sys.path.insert(0,str(runtime));import chardet
# CPython3.14 changed punycode's exception subclasses. Execute the already
# captured actual3.12 source for this consumed codec rather than accept drift.
import codecs,encodings
puny=types.ModuleType('fixed_cpython312_punycode');exec(compile((p/'cpython-punycode.py').read_text(),'fixed_cpython312_punycode','exec'),puny.__dict__)
codecs.unregister(encodings.search_function)
codecs.register(lambda name:puny.getregentry() if name=='punycode' else None)
codecs.register(encodings.search_function)

class Log:
 def debug(self,*a,**kw):pass
class Response:
 def __init__(self,raw,ct):
  self.content=raw;self.encoding=None
  parts=ct.split(';')
  for part in parts[1:]:
   key,sep,value=part.partition('=')
   if sep and key.strip().lower()=='charset':self.encoding=value.strip().strip('\"\'')
  if self.encoding is None:
   if 'text' in parts[0]:self.encoding='ISO-8859-1'
   elif 'application/json' in parts[0]:self.encoding='utf-8'
 @property
 def apparent_encoding(self):return chardet.detect(self.content)['encoding']
ns={'Response':Response,'Optional':typing.Optional,'re':__import__('re'),'chardet':chardet,'logger':Log()}
t=ast.parse((p/'app/utils/http.py').read_text());c=next(x for x in t.body if isinstance(x,ast.ClassDef) and x.name=='RequestUtils');c.body=[x for x in c.body if isinstance(x,ast.FunctionDef) and x.name in ['detect_xml_declared_encoding','is_low_confidence_http_encoding','get_decoded_xml_content']];exec(compile(ast.fix_missing_locations(ast.Module(body=[c],type_ignores=[])),'fixed_http_xml','exec'),ns)
inputs=[('empty',b'','text/xml'),('utf8','<rss>测试</rss>'.encode(),'text/xml; charset=ISO-8859-1'),('gb18030','<?xml version="1.0" encoding="gb18030"?><rss>测试</rss>'.encode('gb18030'),'text/xml; charset=ISO-8859-1'),('decl_lookup',b'<?xml encoding="does_not_exist"?><rss>x</rss>','application/xml'),('decl_unicode',b'<?xml encoding="undefined"?><rss>x</rss>','application/xml'),('decl_punycode',b'<?xml encoding="punycode"?><rss>x</rss>','application/xml'),('badutf8',b'<?xml encoding="utf-8"?><rss>\xff</rss>','text/xml; charset=cp1252'),('bom16','<rss>测试</rss>'.encode('utf-16'),'application/xml'),('be16','<?xml encoding="utf-16-be"?><rss>测试</rss>'.encode('utf-16-be'),'application/xml'),('latin1',b'<rss>caf\xe9</rss>','text/xml; charset=latin1'),('highbytes',bytes(range(128,256)),'text/xml; charset=undefined'),('http_unicode','<rss>测试</rss>'.encode('utf-16-be'),'text/xml; charset=undefined')]
cases=[]
for name,raw,ct in inputs:
 try:expected={'text':ns['RequestUtils'].get_decoded_xml_content(Response(raw,ct))}
 except Exception as e:expected={'error_class':type(e).__name__}
 cases.append({'name':name,'raw_hex':raw.hex(),'content_type':ct,'expected':expected})
out={'scope':'actual fixed RequestUtils XML default decoding AST, public original chardet7.4.3, no requests/network/user config','python':platform.python_version(),'punycode':'actual captured CPython3.12 source; subclass changed in local3.14 stdlib','chardet':chardet.__version__,'cases':cases};(p/'rss-decode-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(cases),'python':out['python'],'chardet':out['chardet']}))
