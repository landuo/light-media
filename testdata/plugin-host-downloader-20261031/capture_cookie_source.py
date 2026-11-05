"""Fixed wrapper AST + official CPython 3.12.13 SimpleCookie source replay."""
import ast,hashlib,json,pathlib,subprocess,types,sys
from packaging.version import Version,InvalidVersion
from urllib.parse import urlparse
root=pathlib.Path(__file__).resolve().parent;work=root.parents[1]/'work/plugin-host-downloader-20261031'
provenance=json.loads((root/'cpython-cookie-provenance.json').read_text());cookie_raw=(work/'cpython-3.12.13-cookies.py').read_bytes();assert hashlib.sha256(cookie_raw).hexdigest()==provenance['sha256']
cookie_module=types.ModuleType('owned_cpython_31213_cookies');sys.modules[cookie_module.__name__]=cookie_module;exec(compile(cookie_raw,'CPython3.12.13/Lib/http/cookies.py','exec'),cookie_module.__dict__)
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/modules/qbittorrent/qbittorrent.py'])
tree=ast.parse(raw);cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='Qbittorrent')
names=['__get_mapping_value','__normalize_cookie','__cookie_key','__build_site_cookies','__supports_cookie_api','__sync_download_cookies','__parse_add_torrent_response','add_torrent']
methods=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in names]
positions={m.name:m.lineno for m in methods}
for method in methods:
 method.returns=None
 for arg in method.args.args+method.args.kwonlyargs:arg.annotation=None
 if method.args.kwarg:method.args.kwarg.annotation=None
ns={'SimpleCookie':cookie_module.SimpleCookie,'urlparse':urlparse,'Version':Version,'InvalidVersion':InvalidVersion,'logger':types.SimpleNamespace(error=lambda *a:None,warn=lambda *a:None),'StringUtils':types.SimpleNamespace(is_magnet_link=lambda value:bool(value) and isinstance(value,str) and value.startswith('magnet:'))}
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Qbittorrent',bases=[],keywords=[],body=methods,decorator_list=[])],type_ignores=[])),'app/modules/qbittorrent/qbittorrent.py','exec'),ns)
result={'kind':'fixed-source AST + hash-verified CPython 3.12.13 cookie parser','source_commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','source_sha256':hashlib.sha256(raw).hexdigest(),'methods':positions,'cookie_cases':[],'hostname_cases':[],'sync_cases':[]}
headers=['uid=1; passkey=OWNED_COOKIE','dup=1; dup=2; alpha=a=b','quoted="a;b"; esc="a\\053b"','$Version=1; uid=1; $Path=/legacy','uid=1; Path=/legacy; HttpOnly; Secure; SameSite=Lax','Path=/legacy; uid=1','uid=1; broken','uid=1; @illegal=2','uid=1;; garbage','uid="line\\012break"','uid=""','uid=1; Partitioned']
for header in headers:
 try:c=cookie_module.SimpleCookie();c.load(header);result['cookie_cases'].append({'header':header,'pairs':[[m.key,m.value] for m in c.values()],'error':False})
 except Exception:result['cookie_cases'].append({'header':header,'pairs':[],'error':True})
urls=['https://TRACKER.invalid/download?token=OWNED_URL','https://user:password@tracker.invalid:badport/a','//TRACKER.invalid/a','https://tracker.invalid/a\nhttps://other.invalid/b','https://İ.example/a','http://[fe80::1%ZONE]:80/a','not-a-url','https://tracker.invalid\nhttps://other.invalid']
for address in urls:
 try:host=urlparse(address).hostname;result['hostname_cases'].append({'url':address,'hostname':host})
 except Exception:result['hostname_cases'].append({'url':address,'hostname':None})
existing=[{'domain':'old.invalid','path':'/','name':'old','value':'keep','expirationDate':0,'extra':'drop'},{'domain':'tracker.invalid','path':'/','name':'uid','value':'old-value','expirationDate':5},{'domain':'tracker.invalid','path':'/other','name':'uid','value':'other'},{'domain':'','name':'invalid','value':'bad'},{'name':'missingDomain','value':'bad'}]
for version,mode,content,header in [('2.11.2','ok',urls[0],headers[0]),('2.11.3','ok',urls[0],headers[0]),('2.11.3rc1','ok',urls[0],headers[0]),('2.11.3.post1','ok',urls[0],headers[0]),('bad-version','ok',urls[0],headers[0]),('2.11.4','read-failure',urls[0],headers[0]),('2.11.4','write-failure',urls[0],headers[0]),('2.11.4','version-failure',urls[0],headers[0]),('2.11.4','ok',urls[0],headers[6]),('2.11.4','ok','magnet:?xt=urn:btih:'+'a'*40,headers[0]),('2.11.4','ok','',headers[0]),('2.11.4','ok',urls[3],headers[0]),('2.11.4','ok',urls[0],'')]:
 calls=[]
 class Client:
  def app_web_api_version(self):
   calls.append({'method':'app_web_api_version'})
   if mode=='version-failure':raise RuntimeError('owned version failure')
   return version
  def app_cookies(self):
   calls.append({'method':'app_cookies'})
   if mode=='read-failure':raise RuntimeError('owned cookie read failure')
   return existing
  def app_set_cookies(self,**kwargs):
   calls.append({'method':'app_set_cookies','arguments':kwargs})
   if mode=='write-failure':raise RuntimeError('owned cookie write failure')
  def torrents_add(self,**kwargs):calls.append({'method':'torrents_add','arguments':kwargs});return 'Ok.'
 wrapper=ns['Qbittorrent']();wrapper.qbc=Client();wrapper._category=False;wrapper._sequentail=False;wrapper._first_last_piece=False
 accepted,ids=wrapper.add_torrent(content=content,cookie=header,is_paused=True,download_dir='/owned/downloads',tag=['owned'])
 result['sync_cases'].append({'version':version,'mode':mode,'url':content,'cookie':header,'existing':existing,'calls':calls,'accepted':accepted,'ids':ids})
(root/'cookie-source-oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
