import ast,base64,datetime,hashlib,json,os,pathlib,shutil,sqlite3,sys,tempfile,time,types,zipfile
from typing import Any,List,Dict,Tuple,Optional
R=pathlib.Path(__file__).parent;O=pathlib.Path(os.environ.get('AUTOBACKUP_ORACLE_OUTPUT',str(R)));O.mkdir(parents=True,exist_ok=True)
raw=(R/'installed-plugin.py').read_bytes();tree=ast.parse(raw);c=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='AutoBackup');c.body=[n for n in c.body if isinstance(n,ast.FunctionDef) and n.name=='backup_file']
class Log:
 def __getattr__(self,k):return lambda *a,**kw:None
class CallError(Exception):
 def __init__(self,*a,stderr='owned failure',**kw):self.stderr=stderr
class AutoBackup:pass
cases=[]
for kind in ('sqlite','sqlite_no_cookies','sqlite_missing_config','sqlite_existing_cookies','sqlite_overwrite_zip','sqlite_db_directory','postgresql','postgresql_failure','unknown'):
 with tempfile.TemporaryDirectory(prefix='owned-autobackup-archive-') as tmp:
  root=pathlib.Path(tmp);config=root/'config';dest=root/'backups';config.mkdir();dest.mkdir();calls=[]
  (config/'category.yaml').write_text('movie: owned\n');(config/'app.env').write_text('OWNED_SYNTHETIC=1\n');(config/'not-backed-up.txt').write_text('excluded');(config/'cookies').mkdir();(config/'cookies'/'nested').mkdir();(config/'cookies'/'nested'/'site.json').write_text('{"owned":true}')
  if kind=='sqlite_db_directory':(config/'user.db-directory').mkdir()
  else:
   db=sqlite3.connect(config/'user.db');db.execute('CREATE TABLE owned_items(id INTEGER PRIMARY KEY,title TEXT)');db.execute('INSERT INTO owned_items VALUES(1,?)',('owned 电影',));db.commit();db.close();(config/'user.db-wal').write_bytes(b'owned-wal');(config/'user.db-shm').write_bytes(b'owned-shm')
  if kind=='sqlite_no_cookies':shutil.rmtree(config/'cookies')
  if kind=='sqlite_missing_config':shutil.rmtree(config)
  if kind=='sqlite_existing_cookies':(dest/'bk_20261018090203'/'cookies').mkdir(parents=True)
  if kind=='sqlite_overwrite_zip':(dest/'bk_20261018090203.zip').write_bytes(b'old zip')
  settings=types.SimpleNamespace(CONFIG_PATH=config,DB_TYPE='sqlite' if kind.startswith('sqlite') else 'postgresql' if kind.startswith('postgresql') else 'other',DB_POSTGRESQL_HOST='owned-host',DB_POSTGRESQL_PORT=5432,DB_POSTGRESQL_DATABASE='owned-db',DB_POSTGRESQL_USERNAME='owned-user',DB_POSTGRESQL_PASSWORD='owned-password')
  def pg_run(cmd,**kwargs):
   calls.append(dict(argv=[str(x).replace(tmp,'<ROOT>') for x in cmd],password_in_env=kwargs['env'].get('PGPASSWORD')=='owned-password',check=kwargs['check'],capture_output=kwargs['capture_output'],text=kwargs['text']))
   if kind=='postgresql_failure':raise CallError()
   pathlib.Path(cmd[-1]).write_text('-- owned PostgreSQL dump\n')
  fake_shutil=types.SimpleNamespace(copy=shutil.copy,copytree=shutil.copytree,make_archive=shutil.make_archive,rmtree=shutil.rmtree,which=lambda _:True)
  ns=dict(_PluginBase=object,Path=pathlib.Path,settings=settings,logger=Log(),time=types.SimpleNamespace(strftime=lambda _: '20261018090203'),shutil=fake_shutil,os=os,subprocess=types.SimpleNamespace(run=pg_run,CalledProcessError=CallError),Any=Any,List=List,Dict=Dict,Tuple=Tuple,Optional=Optional)
  exec(compile(ast.fix_missing_locations(ast.Module(body=[c],type_ignores=[])),str(R/'installed-plugin.py'),'exec'),ns)
  p=ns['AutoBackup'];result=None;error=None
  try:result=p.backup_file(dest)
  except Exception as e:error=type(e).__name__
  files={};modes={}
  if result:
   with zipfile.ZipFile(result)as z:
    for info in z.infolist():files[info.filename]=base64.b64encode(z.read(info)).decode();modes[info.filename]=(info.external_attr>>16)&0o777
  remaining=sorted(str(p.relative_to(dest))+'/' if p.is_dir() else str(p.relative_to(dest)) for p in dest.rglob('*'))
  cases.append(dict(kind=kind,result=None if result is None else str(result).replace(tmp,'<ROOT>'),error=error,zip_files=files,modes=modes,remaining=remaining,postgres_calls=calls))
(O/'archive-oracle.json').write_text(json.dumps(dict(source_sha256=hashlib.sha256(raw).hexdigest(),runtime=sys.version,cases=cases),ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(archive_cases=len(cases))))
