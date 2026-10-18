# Local development oracle only; never imported or executed by the Go product.
import ast,copy,hashlib,json,pathlib,types,enum,bisect,re,sys,subprocess
repo=pathlib.Path(sys.argv[1])
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()=='8a83efeacd3e26b82be495e17d50146e31c96a60'
output=pathlib.Path(sys.argv[2]) if len(sys.argv)>2 else pathlib.Path(__file__).with_name('source-oracle.json')
paths=['app/chain/download.py','app/chain/__init__.py','app/modules/qbittorrent/__init__.py','app/modules/transmission/__init__.py','app/modules/qbittorrent/qbittorrent.py','app/modules/transmission/transmission.py','app/modules/__init__.py','app/schemas/transfer.py','app/utils/string.py','app/core/config.py','app/api/endpoints/history.py']
provenance={p:hashlib.sha256((repo/p).read_bytes()).hexdigest() for p in paths}
def find(file,cls,method):
 tree=ast.parse((repo/file).read_text()); c=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name==cls)
 return copy.deepcopy(next(n for n in c.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name==method))
def makeclass(name,methods,ns,bases=[]):
 node=ast.ClassDef(name=name,bases=[ast.Name(id=x,ctx=ast.Load()) for x in bases],keywords=[],body=methods,decorator_list=[])
 code=ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),node],type_ignores=[])
 exec(compile(ast.fix_missing_locations(code),'<actual source AST>','exec'),ns)
 return ns[name]
class Logger:
 def __getattr__(self,k):return lambda *a,**kw:None
ns={'logger':Logger(),'EventType':types.SimpleNamespace(DownloadDeleted='DownloadDeleted')}
m=find('app/chain/download.py','DownloadChain','download_file_deleted');m.decorator_list=[]
Source=makeclass('Source',[m],ns)
results=[]
for case,data,hit,answer in [('no-event',None,False,None),('no-hash',{},False,None),('src-only',{'src':'/owned/file'},False,None),('empty-hash',{'hash':''},True,True),('no-hit',{'hash':'a'*40},False,True),('success',{'hash':'a'*40},True,True),('false',{'hash':'a'*40},True,False),('none',{'hash':'a'*40},True,None)]:
 calls=[];published=[];o=Source();o.list_torrents=lambda **kw:calls.append(['list',kw]) or ([types.SimpleNamespace(model_dump=lambda:{'hash':'a'*40,'title':'fixture'})] if hit else []);o.remove_torrents=lambda **kw:calls.append(['remove',kw]) or answer;o.eventmanager=types.SimpleNamespace(send_event=lambda *a:published.append(a));o.download_file_deleted(types.SimpleNamespace(event_data=data) if data is not None else None)
 results.append({'case':case,'input':data,'list_hit':hit,'remove_return':answer,'calls':calls,'events':published})
# Use the real chain arbitration method to prove False terminates while None
# advances. No guessed all-matched downloader policy is used by the product.
ns2={'logger':Logger(),'ObjectUtils':types.SimpleNamespace(check_signature=lambda *a:False),'RateLimitExceededException':type('R',(Exception,),{}),'traceback':__import__('traceback')}
Chain=makeclass('SourceChain',[find('app/chain/__init__.py','ChainBase','__execute_system_modules'),find('app/chain/__init__.py','ChainBase','__is_valid_empty')],ns2)
arbitration=[]
for first in [True,False,None]:
 calls=[]
 class M:
  def __init__(self,p,answer):self.p=p;self.answer=answer
  def get_priority(self):return self.p
  def get_name(self):return str(self.p)
  def remove_torrents(self,**kw):calls.append(self.p);return self.answer
 c=Chain();c.modulemanager=types.SimpleNamespace(get_running_modules=lambda method:[M(2,True),M(1,first)])
 answer=c._SourceChain__execute_system_modules('remove_torrents',None,hashs=['a'*40],delete_file=False)
 arbitration.append({'first_return':first,'calls':calls,'result':answer})
# Extract the exact list_torrents builders and schema; only daemon objects and
# MetaInfo identity are injected. The actual Go MetaInfo dependency already has
# its own source oracle. No fake network or MetaInfo is used in production.
class Model:
 def __init__(self,**kw):
  self.fields={k:copy.deepcopy(getattr(type(self),k,None)) for k in type(self).__annotations__};self.fields.update(kw)
 def model_dump(self):return self.fields
schema=ast.parse((repo/'app/schemas/transfer.py').read_text());cl=next(n for n in schema.body if isinstance(n,ast.ClassDef) and n.name=='DownloaderTorrent');cl.bases=[ast.Name(id='Model',ctx=ast.Load())]
ns3={'Model':Model,'Field':lambda default_factory:default_factory()}
exec(compile(ast.fix_missing_locations(ast.Module(body=[ast.ImportFrom(module='__future__',names=[ast.alias(name='annotations')],level=0),cl],type_ignores=[])),'<actual schema AST>','exec'),ns3)
String=makeclass('StringUtils',[find('app/utils/string.py','StringUtils','str_filesize'),find('app/utils/string.py','StringUtils','str_secends')],{'re':re,'bisect':bisect})
EnumStatus=enum.Enum('Status',{n:v for n,v in [('ALL','all'),('TRANSFER','transfer'),('DOWNLOADING','downloading'),('COMPLETED','completed'),('PAUSED','paused')]})
rows=[]
for kind,cls,file in [('qb','QbittorrentModule','app/modules/qbittorrent/__init__.py'),('tr','TransmissionModule','app/modules/transmission/__init__.py')]:
 env={'logger':Logger(),'settings':types.SimpleNamespace(TORRENT_TAG='light-media'),'Path':pathlib.Path,'TorrentStatus':EnumStatus,'TorrentQueryStatus':EnumStatus,'DownloadTaskState':enum.Enum('Task',{n:n.lower() for n in ['DOWNLOADING','PAUSED','COMPLETED']}),'MetaInfo':lambda name:types.SimpleNamespace(name='History',year='2024',season_episode=''),'DownloaderTorrent':ns3['DownloaderTorrent'],'StringUtils':String,'_QBITTORRENT_PAUSED_STATES':{'paused','pauseddl','pausedup','stoppeddl','stoppedup'},'_QBITTORRENT_DOWNLOADING_STATES':{'allocating','checkingdl','downloading','forceddl','metadl','queueddl','stalleddl'},'_TRANSMISSION_PAUSED_STATES':{'stopped'},'_TRANSMISSION_DOWNLOADING_STATES':{'download_pending','downloading'}}
 C=makeclass(cls,[find(file,cls,n) for n in ['list_torrents','__normalize_query_status','__normalize_torrent_state']],env)
 row={'hash':'a'*40,'name':'History (2024).mkv','state':'uploading','save_path':'/owned/downloads','content_path':'/owned/downloads/History (2024).mkv','total_size':1024,'completed':1024,'progress':1,'ratio':1,'tags':'light-media','upspeed':1024,'dlspeed':0} if kind=='qb' else {'hashString':'a'*40,'name':'History (2024).mkv','status':'seeding','download_dir':'/owned/downloads','total_size':1024,'left_until_done':0,'progress':100,'labels':['light-media'],'rate_download':0,'rate_upload':0}
 inputrow=copy.deepcopy(row)
 server=types.SimpleNamespace(get_torrents=lambda **kw:([copy.deepcopy(row) if kind=='qb' else types.SimpleNamespace(**copy.deepcopy(row))],False))
 o=C();o.get_instances=lambda:{'owned':server};o.normalize_return_path=lambda p,n:str(p)
 got=o.list_torrents(hashs=['a'*40])
 rows.append({'kind':kind,'input':inputrow,'expected':[r.model_dump() for r in got]})
out={'source_git':'8a83efeacd3e26b82be495e17d50146e31c96a60','files':provenance,'scope':'Exact AST core and module methods with isolated injected IO/MetaInfo; source schema defaults; separate Go real HTTP tests. Original tag role is MOVIEPILOT; existing independent product maps its built-in role to light-media without adopting old torrents.','core':results,'module_arbitration':arbitration,'snapshots':rows}
output.write_text(json.dumps(out,ensure_ascii=False,indent=2,default=str)+'\n')
print({'core_cases':len(results),'module_cases':len(arbitration),'snapshot_cases':len(rows),'sha256':hashlib.sha256(output.read_bytes()).hexdigest()})
