"""Owned offline public MoviePilot-Rust0.2.6 wheel oracle. No service imports or IO."""
import sys,pathlib,json,time,platform,hashlib
root=pathlib.Path(__file__).resolve().parent
wheelroot=pathlib.Path(sys.argv[1]).resolve()
sys.path.insert(0,str(wheelroot))
import moviepilot_rust
time.tzset()
base='<rss><channel><item><title>{title}</title><link>https://pt.invalid/1</link>{extra}</item></channel></rss>'
texts=[]
for title in ['Movie',' 中文 ', 'A &amp; B','A &unknown; B','A &#65; B','A &#x1F600; B','A &#0; B','A &#xD800; B','A &bad','<![CDATA[ C <tag> ]]>','A<b>B</b>C','A\r\nB']:
 texts.append(base.format(title=title,extra=''))
for extra in ['<title>second</title>','<enclosure url="u" length="-2"/>','<enclosure url="u" length="+2"/>','<enclosure url="u" length="١٢"/>','<enclosure url="u" length="9223372036854775808"/>','<enclosure url="&bad;"/>','<enclosure url="u" url="v"/>','<link href="x"/>','<enclosure url="u"/><enclosure url="v" length="4"/>','<enclosure length="4"/><enclosure url="v" length="5"/>','<description>D <b>B</b> tail</description>','<summary>S</summary><description>D</description>','<dc:creator xmlns:dc="owned">Nick</dc:creator>']:
 texts.append(base.format(title='Movie',extra=extra))
for doc in ['<feed><entry><title>A</title><link href="x"/></entry></feed>','<root><ITEM><TITLE>U</TITLE><LINK>u</LINK></ITEM></root>','<rss><item><title>A</title><link>x</link>','<rss><item><title>A</link></item></rss>','<rss><item><title>A</title><link>x</link></item>','<rss><item><title/><title>A</title><link>x</link></item></rss>','<rss><item><title></title><link>x</link></item></rss>','<rss><item><title>A</title></item></rss>','<rss><item><title>A</title><link href="x">y</link></item></rss>','<rss><item><title>A</title><link foo="&bad;">x</link></item></rss>','<rss><item><title foo=bad>A</title><link>x</link></item></rss>','<rss><item><title>A</title><link href=x/></item></rss>','<!DOCTYPE rss SYSTEM "https://never.invalid/x"><rss><item><title>&custom;</title><link>x</link></item></rss>']:
 texts.append(doc)
for date in ['Tue, 19 May 2026 08:30:00 GMT','Tue, 19 May 2026 08:30:00 +0800','2026-05-19T09:30:00Z','2026-05-19T09:30:00+08:00','2026-05-19 09:30:00 UTC','19 May 2026 09:30:00 GMT','2026-05-19T09:30:00 GMT','2026-05-19 09:30:00','2026-05-19 09:30','2026/05/19 09:30:00','2026/05/19 09:30','19 May 2026 09:30:00','Tue, 19 May 2026 09:30:00','2026-05-19','2026/05/19','19 May 2026','bad','Wed, 19 May 2026 08:30:00 GMT','2026-5-9 1:02:03','2026-05-19T09:30:00.123Z','2026-05-19T09:30:60Z']:
 texts.append(base.format(title='D',extra='<pubDate>'+date+'</pubDate>'))
cases=[]
for text in texts:
 try:
  result=moviepilot_rust.parse_rss_items_fast(text,1001)
  for row in result:
   if row.get('pubdate'):row['pubdate']=row['pubdate'].strftime('%Y-%m-%d %H:%M:%S')
  expected={'value':result}
 except Exception as e:expected={'error_class':type(e).__name__}
 cases.append({'text':text,'expected':expected})
out={'version':'0.2.6','public_wheel_sha256':'461e538c1025fcb65737e933deaf5e16918cf55df04c90bf6703cb5ff0958d6d','runtime':platform.python_version(),'tz':'Asia/Shanghai','scope':'actual public version0.2.6 local macOSarm64 native parser, no product runtime/NAS/user data','cases':cases}
(root/'rss-oracle.json').write_text(json.dumps(out,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(cases),'version':out['version'],'runtime':out['runtime']}))
