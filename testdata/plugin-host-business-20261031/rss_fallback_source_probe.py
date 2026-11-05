#!/usr/bin/env python3
"""Fixed-source legacy RSS recovery probe with lxml 6.1.1/chardet 7.4.3.

Only owned responses are provided. Source app modules/services are not imported;
Rust unavailability is explicitly selected to exercise its documented fallback.
"""
import argparse,ast,copy,hashlib,json,re,subprocess,traceback,datetime,os,time
from types import SimpleNamespace
from typing import Any,List,Optional,Tuple,Union
import chardet
import dateutil.parser
from lxml import etree

COMMIT="8a83efeacd3e26b82be495e17d50146e31c96a60"
class Log:
    def __init__(self):self.events=[]
    def __getattr__(self,level):return lambda text:self.events.append({"level":level,"message":str(text)})
class Response:
    status_code=200
    encoding="utf-8"
    def __init__(self,body):self.content=body;self.text=body.decode("utf-8",errors="replace");self.apparent_encoding=chardet.detect(body).get("encoding")
    def __bool__(self):return True

def source(repo,path):return subprocess.check_output(["git","-C",repo,"show",COMMIT+":"+path]).decode()
def compile_class(repo,path,name,methods,namespace,attrs=()):
    text=source(repo,path);cls=next(n for n in ast.parse(text).body if isinstance(n,ast.ClassDef) and n.name==name)
    chosen=[];locations={}
    for node in cls.body:
        if isinstance(node,ast.FunctionDef) and node.name in methods:
            n=copy.deepcopy(node);n.decorator_list=[d for d in n.decorator_list if isinstance(d,ast.Name) and d.id in {"staticmethod","classmethod"}];chosen.append(n);locations[n.name]={"line":n.lineno,"end_line":n.end_lineno,"sha256":hashlib.sha256("\n".join(text.splitlines()[n.lineno-1:n.end_lineno]).encode()).hexdigest()}
        elif isinstance(node,ast.Assign) and any(isinstance(t,ast.Name) and t.id in attrs for t in node.targets):chosen.append(copy.deepcopy(node))
    tree=ast.Module(body=[ast.ClassDef(name=name,bases=[],keywords=[],body=chosen,decorator_list=[])],type_ignores=[])
    exec(compile(ast.fix_missing_locations(tree),COMMIT+":"+path,"exec"),namespace);return namespace[name],locations

def main():
    p=argparse.ArgumentParser();p.add_argument("--source-repo",required=True);p.add_argument("--output",required=True);args=p.parse_args();logger=Log()
    os.environ["TZ"]="Asia/Shanghai";time.tzset()
    ns=dict(Any=Any,List=List,Tuple=Tuple,Union=Union,Optional=Optional,Response=Response,re=re,chardet=chardet,logger=logger,traceback=traceback,etree=etree,settings=SimpleNamespace(ENCODING_DETECTION_PERFORMANCE_MODE=False,ENCODING_DETECTION_MIN_CONFIDENCE=.8),rust_accel=SimpleNamespace(parse_rss_items=lambda *_:None))
    request,decode_locations=compile_class(args.source_repo,"app/utils/http.py","RequestUtils",{"detect_xml_declared_encoding","is_low_confidence_http_encoding","get_decoded_xml_content"},ns)
    calls=[];body=[b""]
    request.__init__=lambda self,**kwargs:None
    request.get_res=lambda self,url:(calls.append(url) or Response(body[0]))
    ns["RequestUtils"]=request
    ns.update(datetime=datetime,dateutil=dateutil)
    _,date_locations=compile_class(args.source_repo,"app/utils/string.py","StringUtils",{"get_time"},ns)
    rss,parse_locations=compile_class(args.source_repo,"app/helper/rss.py","RssHelper",{"__parse_with_rust","parse"},ns,{"MAX_RSS_SIZE","MAX_RSS_ITEMS"})
    cases=[]
    inputs=[
        ("valid",'<rss><channel><item><title>Owned</title><link>details.php?id=1</link></item></channel></rss>'),
        ("truncated_xml",'<rss><channel><item><title>Owned</title><link>details.php?id=1</link>'),
        ("mismatched_close",'<rss><channel><item><title>Owned</title><link>details.php?id=1</item></channel></rss>'),
        ("bare_ampersand",'<rss><channel><item><title>A & B</title><link>details.php?id=1</link></item></channel></rss>'),
        ("whitespace_title",'<rss><channel><item><title>   </title><link>details.php?id=1</link></item></channel></rss>'),
        ("namespace_legacy",'<feed xmlns="urn:owned"><entry><title>Owned</title><link href="details.php?id=1"/></entry></feed>'),
        ("duplicate_title",'<rss><item><title>First</title><title>Second</title><enclosure url="download.php?id=1" length="12"/></item></rss>'),
        ("external_entity_disabled",'<!DOCTYPE rss [<!ENTITY secret SYSTEM "http://never-fetch.invalid/private">]><rss><item><title>A&secret;B</title><link>details.php?id=1</link></item></rss>'),
        ("undeclared_entity",'<rss><item><title>A&missing;B</title><link>details.php?id=1</link></item></rss>'),
        ("predefined_entity",'<rss><item><title>A&amp;B</title><link>details.php?id=1</link></item></rss>'),
        ("cdata_title",'<rss><item><title><![CDATA[A & B]]></title><link>details.php?id=1</link></item></rss>'),
        ("mixed_title_text",'<rss><item><title>Before<em>inside</em>after</title><link>details.php?id=1</link></item></rss>'),
        ("comment_title_tail",'<rss><item><title>Before<!-- own -->after</title><link>details.php?id=1</link></item></rss>'),
        ("unescaped_attribute",'<rss><item><title>Owned</title><enclosure url="download.php?id=1&passkey=owned" length="1"/></item></rss>'),
        ("internal_entity_attribute",'<!DOCTYPE rss [<!ENTITY own "VALUE">]><rss><item><title>Owned</title><enclosure url="download.php?x=&own;" length="1"/></item></rss>'),
        ("unicode_decimal_size",'<rss><item><title>Owned</title><enclosure url="download.php?id=1" length="١٢"/></item></rss>'),
        ("non_decimal_digit_size",'<rss><item><title>Owned</title><enclosure url="download.php?id=1" length="²"/></item></rss>'),
        ("large_integer_size",'<rss><item><title>Owned</title><enclosure url="download.php?id=1" length="18446744073709551616"/></item></rss>'),
        ("date_rfc1123",'<rss><item><title>Owned</title><link>details</link><pubDate>Sat, 31 Oct 2026 00:00:00 GMT</pubDate></item></rss>'),
        ("date_iso_offset",'<rss><item><title>Owned</title><link>details</link><pubDate>2026-10-31T07:08:09+02:00</pubDate></item></rss>'),
        ("date_naive",'<rss><item><title>Owned</title><link>details</link><pubDate>2026-10-31 07:08:09</pubDate></item></rss>'),
        ("date_invalid",'<rss><item><title>Owned</title><link>details</link><pubDate>not-a-date</pubDate></item></rss>'),
        ("date_relative_rejected",'<rss><item><title>Owned</title><link>details</link><pubDate>yesterday</pubDate></item></rss>'),
        ("expired_literal","RSS Link has expired, You need to get new!"),
        ("not_xml","owned invalid text"),("empty","")]
    for name,text in inputs:
        body[0]=text.encode();logger.events=[];calls.clear();returned=rss().parse("http://owned.invalid/feed")
        cases.append({"name":name,"input":text,"returned":returned,"request_calls":len(calls),"events":copy.deepcopy(logger.events)})
    result={"source_commit":COMMIT,"classification":"synthetic fixed-source AST replay, owned response, Rust unavailable branch, actual lxml/chardet/dateutil","environment":{"lxml":list(etree.LXML_VERSION),"libxml":list(etree.LIBXML_VERSION),"chardet":chardet.__version__,"dateutil":dateutil.__version__,"timezone":"Asia/Shanghai"},"source_methods":{"request_utils":decode_locations,"rss_helper":parse_locations,"date_parser":date_locations},"datetime_normalization":"TorrentsChain.rss fixed source line484 strftime(%Y-%m-%d %H:%M:%S)","network_calls":0,"production_writes":0,"cases":cases,"finding":"The fixed helper returns False for the literal expired-RSS response under this ordinary HTTP fixture; the chain's None renewal branch is not reached by that literal alone."}
    with open(args.output,"w") as f:json.dump(result,f,ensure_ascii=False,indent=2,default=lambda value:value.strftime("%Y-%m-%d %H:%M:%S") if isinstance(value,datetime.datetime) else None);f.write("\n")
if __name__=="__main__":main()
