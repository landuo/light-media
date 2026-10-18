"""Actual fixed RssHelper AST with IO/Rust disabled, public lxml6.1.1/dateutil offline."""
import sys,pathlib,json,types,ast,time
p=pathlib.Path(__file__).resolve().parent
for root in sys.argv[1:]:sys.path.insert(0,str(pathlib.Path(root).resolve()))
from lxml import etree
import dateutil.parser
time.tzset()
class Log:
 def __getattr__(self,key):return lambda *a,**kw:None
class Response:
 status_code=200
 def __init__(self,text):self.content=text.encode();self.text=text
class Request:
 text=''
 def __init__(self,**kw):pass
 def get_res(self,url):return Response(self.text)
 @staticmethod
 def get_decoded_xml_content(response,**kw):return response.text
class StringUtils:
 @staticmethod
 def get_time(value):
  try:return dateutil.parser.parse(value)
  except dateutil.parser.ParserError:return None
ns={'Optional':__import__('typing').Optional,'List':list,'Union':__import__('typing').Union,'Tuple':tuple,'RequestUtils':Request,'settings':types.SimpleNamespace(PROXY=None,ENCODING_DETECTION_PERFORMANCE_MODE=False,ENCODING_DETECTION_MIN_CONFIDENCE=.8),'rust_accel':types.SimpleNamespace(parse_rss_items=lambda *a:None),'etree':etree,'StringUtils':StringUtils,'logger':Log(),'traceback':__import__('traceback')}
t=ast.parse((p/'app/helper/rss.py').read_text());c=next(x for x in t.body if isinstance(x,ast.ClassDef)and x.name=='RssHelper');c.body=[x for x in c.body if not isinstance(x,ast.FunctionDef)or x.name in ['__parse_with_rust','parse']];exec(compile(ast.fix_missing_locations(ast.Module(body=[c],type_ignores=[])),'fixed_rss_helper','exec'),ns)
source=json.loads((p/'rss-oracle.json').read_text());cases=[]
for row in source['cases']:
 Request.text=row['text']
 result=ns['RssHelper']().parse('https://owned.invalid/rss')
 if isinstance(result,list):
  for item in result:
   if item.get('pubdate'):item['pubdate']=item['pubdate'].strftime('%Y-%m-%d %H:%M:%S')
 cases.append({'text':row['text'],'expected':result})
out={'lxml':list(etree.LXML_VERSION),'libxml2':list(etree.LIBXML_VERSION),'runtime':__import__('platform').python_version(),'scope':'actual fixed RssHelper parse AST with public fixed lib; no app import/network, Rust disabled for fallback source comparison; dates full fixed strings','cases':cases}
(p/'rss-fallback-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(cases),'lxml':out['lxml'],'libxml2':out['libxml2']}))
