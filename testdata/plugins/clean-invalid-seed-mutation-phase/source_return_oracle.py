"""Offline installed-method AST oracle. No MoviePilot imports or networking.

Execute original wrapper failure handling and the plugin's real clean method;
tracker/list/size dependencies are explicit fakes. This proves continuation and
attempt-count text, not whether a particular daemon packet was transmitted.
"""
import ast,copy,hashlib,json,pathlib,types,urllib.parse,warnings
warnings.filterwarnings('ignore',category=SyntaxWarning)
root=pathlib.Path(__file__).resolve().parent
expected={'installed-plugin.py':'fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda','installed-qb.py':'114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0','installed-tr.py':'6fc20e208b86e9e68ada074643f82b88270c2e51749e460ad881b10f744cf676'}
trees={}
for name,sha in expected.items():
 raw=(root/name).read_bytes();assert hashlib.sha256(raw).hexdigest()==sha
 trees[name]=ast.parse(raw)
def method_class(tree,class_name,names,bases):
 old=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==class_name)
 selected=[]
 for n in old.body:
  if isinstance(n,(ast.Assign,ast.AnnAssign)):
   try:ast.literal_eval(n.value)
   except Exception:continue
   selected.append(copy.deepcopy(n))
  if isinstance(n,ast.FunctionDef) and n.name in names:
   n=copy.deepcopy(n);n.decorator_list=[];n.returns=None
   for a in n.args.args:a.annotation=None
   selected.append(n)
 return ast.fix_missing_locations(ast.ClassDef(name=class_name,bases=bases,keywords=[],body=selected,decorator_list=[]))
class Quiet:
 def __getattr__(self,name):return lambda *args,**kw:None
class Utils:
 @staticmethod
 def get_url_netloc(url):return ('https',urllib.parse.urlparse(url).netloc)
 @staticmethod
 def str_filesize(value):return str(value)
rows=[]
for kind in ['qbittorrent','transmission']:
 for operation in ['delete','tags']:
  attempts=[];writes=[];notices=[]
  first,second,good='a'*40,'c'*40,'b'*40
  class Client:
   def __getattr__(self,name):
    def call(**kw):
     h=kw.get('torrent_hashes',kw.get('ids'));attempts.append({'method':name,'hash':h})
     if h==first:raise RuntimeError('synthetic wrapper failure')
     writes.append(h)
    return call
  wrapper_name='Qbittorrent' if kind=='qbittorrent' else 'Transmission'
  file='installed-qb.py' if kind=='qbittorrent' else 'installed-tr.py'
  wrapper=method_class(trees[file],wrapper_name,{'delete_torrents','set_torrents_tag','set_torrent_tag'},[])
  env={'logger':Quiet()};exec(compile(ast.fix_missing_locations(ast.Module(body=[wrapper],type_ignores=[])),'<installed-native-wrapper-AST>','exec'),env)
  daemon=env[wrapper_name]();daemon.qbc=Client();daemon.trc=Client()
  torrent=lambda h:types.SimpleNamespace(hash=h,hashString=h,name='owned-'+h[0],category='movies',tags='',labels=[],size=4,total_size=4)
  torrents=[torrent(first),torrent(second),torrent(good)]
  class Dependencies:
   @property
   def service_info(self):return {'owned':types.SimpleNamespace(name='owned',instance=daemon)}
   def get_downloader_type(self,service):return kind
   def get_all_torrents(self,service):return torrents
   def get_tracker_info(self,torrent,downloader_type):return [{'url':'https://tracker.fixture/announce','tier':0,'status':2 if torrent.hash==good else 4,'msg':'' if torrent.hash==good else 'torrent not registered with this tracker'}]
   def is_torrent_paused(self,*args):return True
   def post_message(self,**kw):notices.append(kw)
  plugin=method_class(trees['installed-plugin.py'],'CleanInvalidSeed',{'clean_invalid_seed','get_torrent_category','get_torrent_tags','get_torrent_hash','set_torrent_label'},[ast.Name(id='Dependencies',ctx=ast.Load())])
  env={'Dependencies':Dependencies,'logger':Quiet(),'StringUtils':Utils,'NotificationType':types.SimpleNamespace(SiteMessage='SiteMessage')};exec(compile(ast.fix_missing_locations(ast.Module(body=[plugin],type_ignores=[])),'<installed-CleanInvalidSeed-AST>','exec'),env)
  p=env['CleanInvalidSeed']();p._exclude_categories='not-owned';p._exclude_labels='not-owned';p._custom_error_msg='';p._more_logs=False;p._delete_invalid_torrents=True;p._label_only=operation=='tags';p._label='owned';p._notify=True;p._notify_all=True;p._detect_invalid_files=False
  error=None
  try:p.clean_invalid_seed()
  except Exception as e:error=type(e).__name__
  wanted='已标记 2 个失效种子' if operation=='tags' else '已删除 2 个失效种子'
  case={'kind':kind,'operation':operation,'error':error,'attempts':attempts,'synthetic_successes':writes,'source_count_two':any(wanted in n.get('text','') for n in notices)}
  assert error is None and [a['hash'] for a in attempts]==[first,second] and writes==[second] and case['source_count_two'],case
  rows.append(case)
print(json.dumps({'source_sha256':expected,'scope':'offline actual method AST; explicit synthetic list/tracker/size dependencies; no network or real daemon','cases':rows},ensure_ascii=False,indent=2))
