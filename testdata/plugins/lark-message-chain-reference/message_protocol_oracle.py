import ast,hashlib,json,math,re,sys,unicodedata,typing
from pathlib import Path
from enum import Enum
from types import SimpleNamespace
import cn2an
root=Path('/Users/accidia/projects/MoviePilot')
paths=['app/chain/message.py','app/utils/string.py','app/schemas/message.py']
sources={p:(root/p).read_bytes()for p in paths}
class MediaType(Enum):
 TV='电视剧'
 MOVIE='电影'
class MessageChannel(Enum):
 Feishu='飞书'
tree=ast.parse(sources[paths[2]])
feishu=None
for n in ast.walk(tree):
 if isinstance(n,ast.Dict):
  for k,v in zip(n.keys,n.values):
   if isinstance(k,ast.Attribute)and k.attr=='Feishu'and isinstance(v,ast.Call)and isinstance(v.func,ast.Name)and v.func.id=='ChannelCapabilities':feishu=v
if feishu is None:raise RuntimeError('source Feishu capabilities missing')
caps={k.arg:ast.literal_eval(k.value)for k in feishu.keywords if k.arg.startswith('max_')}
class ChannelCapabilityManager:
 @staticmethod
 def supports_buttons(c):return c==MessageChannel.Feishu
 @staticmethod
 def supports_callbacks(c):return c==MessageChannel.Feishu
 @staticmethod
 def get_max_buttons_per_row(c):return caps['max_buttons_per_row']
 @staticmethod
 def get_max_button_text_length(c):return caps['max_button_text_length']
ns={**vars(typing),'re':re,'math':math,'cn2an':cn2an,'MediaType':MediaType,'MessageChannel':MessageChannel,'ChannelCapabilityManager':ChannelCapabilityManager,'ChainBase':object,'MediaInfo':SimpleNamespace,'Context':SimpleNamespace,'PendingMediaInteraction':SimpleNamespace,'DownloadDirectory':SimpleNamespace}
string_tree=ast.parse(sources[paths[1]])
nodes=[]
for node in string_tree.body:
 if isinstance(node,ast.Assign)and any(isinstance(x,ast.Name)and x.id in {'_max_media_title_words','_min_media_title_length','_non_media_title_pattern','_chat_intent_pattern','_media_feature_pattern','_media_separator_pattern','_media_sentence_punctuation_pattern','_media_title_char_pattern'}for x in node.targets):nodes.append(node)
 if isinstance(node,ast.ClassDef)and node.name=='StringUtils':
  node.body=[x for x in node.body if isinstance(x,ast.FunctionDef)and x.name in {'count_words','is_media_title_like','is_link','get_keyword'}];nodes.append(node)
exec(compile(ast.fix_missing_locations(ast.Module(body=nodes,type_ignores=[])),str(root/paths[1]),'exec'),ns)
methods={'parse_callback','_resolve_action','_page_items','_page_size','_supports_interactive_buttons','_navigation_buttons','_create_media_buttons','_create_torrent_buttons','_create_download_dir_buttons','_format_download_dir_label'}
media=next(x for x in ast.parse(sources[paths[0]]).body if isinstance(x,ast.ClassDef)and x.name=='MediaInteractionChain')
media.body=[x for x in media.body if isinstance(x,ast.FunctionDef)and x.name in methods or isinstance(x,ast.Assign)]
exec(compile(ast.fix_missing_locations(ast.Module(body=[media],type_ignores=[])),str(root/paths[0]),'exec'),ns)
StringUtils=ns['StringUtils'];chain=ns['MediaInteractionChain']()
cases=[]
def add(kind,inp,fn):
 try:out={'output':fn()}
 except Exception as e:out={'error':type(e).__name__}
 cases.append({'kind':kind,'input':inp,**out})
texts=['','飞驰人生3','Avatar','The Last of Us','订阅:绝世唐门 第2季','洗版：流浪地球','搜索 搜索:流浪地球','下载：The Last of Us S02','推荐电影','帮我找电影','请问你好','你好？','继续','你好呀','...','https://example.test/x','阿凡达 (2025)','S01E02','阿凡达\x1c2025','阿凡达\u20032025','The '+('word '*11),'测试\u034f电影','测试\n','１２３４','测试电影！']
for text in texts:add('action',{'text':text},lambda text=text:dict(zip(['action','content'],chain._resolve_action(text))))
keywords=['','电影 阿凡达 (2025)','电视剧 绝世唐门 第2季第12集','动漫 名侦探 第十季 第三十一集 2026','测试 第十二季 第0集','测试 第0季 第1集','测试 第3十季 第一二集','测试 第十十季','电影 2026','测试 (١٩٩٩)','\x1c电影\u3000片名 第 2 季','  电视剧 甲 第1季 第2集 2025','Avatar 2026','测试 第00000000000000000002季']
for text in keywords:
 def keyword(text=text):
  typ,key,season,episode,year,content=StringUtils.get_keyword(text)
  return {'type':typ.value if typ else None,'keyword':key,'season':str(season)if season is not None else None,'episode':str(episode)if episode is not None else None,'year':year,'content':content}
 add('keyword',{'text':text},keyword)
for text in ['','media:id:select:2','media::download:0','media:id:select:١٢','media:id:select:²','media:id:select:-1','media:id:download-dir:1:extra','media:id','select_12','download_١٢','select_1\n','page_p','page_n','select_99999999999999999999999999999','unknown']:
 def callback(text=text):
  got=chain.parse_callback(text)
  return None if got is None else {'request':got[0],'action':got[1],'index':str(got[2])if got[2]is not None else None}
 add('callback',{'text':text},callback)
for total in [0,1,8,9,17,30]:
 for page in [-1,0,1,100]:
  def paging(total=total,page=page):
   items,p,pages=chain._page_items(list(range(total)),page,8)
   return {'items':items,'page':p,'pages':pages}
  add('page',{'total':total,'page':page},paging)
for phase in ['media','torrent','download-dir']:
 for total in [0,1,3,8,9,20]:
  for page in [0,1,2]:
   count=min(8,max(0,total-page*8));pages=max(1,math.ceil(total/8))
   request=SimpleNamespace(request_id='ownedrequest',page=page)
   if phase=='media':items=[SimpleNamespace(title_year='Synthetic (2026)')for _ in range(count)];method=chain._create_media_buttons
   elif phase=='torrent':items=[SimpleNamespace(torrent_info=SimpleNamespace(site_name='Owned',seeders=1))for _ in range(count)];method=chain._create_torrent_buttons
   else:items=[SimpleNamespace(name='Owned',save_path='/owned/downloads',download_path='/owned/downloads')for _ in range(count)];method=chain._create_download_dir_buttons
   add('buttons',{'phase':phase,'count':count,'total':total,'page':page,'pages':pages},lambda method=method,request=request,items=items,total=total,pages=pages:method(MessageChannel.Feishu,request,items,total,pages))
print(json.dumps({'scope':'Actual unchanged AST methods; real cn2an0.5.24; fixed Feishu channel ABI values extracted from actual source. Does not prove media/P0 external consumers.','python':sys.version,'unicode':unicodedata.unidata_version,'cn2an':cn2an.__version__,'source_sha256':{p:hashlib.sha256(b).hexdigest()for p,b in sources.items()},'source_methods':sorted(methods),'feishu_capabilities':caps,'cases':cases},ensure_ascii=False,indent=2))
