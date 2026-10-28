"""Fixed defaults + actual stdlib rotation + fixed filesystem ZIP selection.
No original application modules or credentials are imported.
"""
import ast,hashlib,io,json,logging,logging.handlers,pathlib,re,sys,tempfile,types,zipfile,urllib.request,os,time
from datetime import datetime
from typing import Optional
import pydantic
from pydantic import BaseModel,ConfigDict,ValidationError
assert pydantic.__version__=='2.13.4'
stdlib_url='https://raw.githubusercontent.com/python/cpython/v3.12.13/Lib/logging/handlers.py'
stdlib_sha='e0d31066e1d11142dc795af60351ea3610c2a08ff1cedf705743ce4522517495'
cache=pathlib.Path(__file__).parents[2]/'work/plugin-no-official-auth-20261026/cpython-3.12.13-handlers.py'
stdlib_raw=cache.read_bytes() if cache.exists() else urllib.request.urlopen(stdlib_url,timeout=30).read()
assert hashlib.sha256(stdlib_raw).hexdigest()==stdlib_sha
stdlib_tree=ast.parse(stdlib_raw.decode());stdlib_class=next(n for n in stdlib_tree.body if isinstance(n,ast.ClassDef) and n.name=='RotatingFileHandler')
methods=[n for n in stdlib_class.body if isinstance(n,ast.FunctionDef) and n.name in ['shouldRollover','doRollover']]
stdlib_ns={'os':os};exec(compile(ast.Module(body=methods,type_ignores=[]),'<fixed CPython3.12.13 rotation>','exec'),stdlib_ns)
SourceRotatingFileHandler=type('SourceRotatingFileHandler',(logging.handlers.RotatingFileHandler,),{name:stdlib_ns[name] for name in ['shouldRollover','doRollover']})
source=pathlib.Path('/Users/accidia/projects/MoviePilot')
log_path=source/'app/log.py';log_tree=ast.parse(log_path.read_text());model=next(n for n in log_tree.body if isinstance(n,ast.ClassDef) and n.name=='LogConfigModel')
values={n.target.id:ast.literal_eval(n.value) for n in model.body if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id in ['LOG_MAX_FILE_SIZE','LOG_BACKUP_COUNT','LOG_FILE_FORMAT']}
system_path=source/'app/api/endpoints/system.py';system_tree=ast.parse(system_path.read_text());select=next(n for n in system_tree.body if isinstance(n,ast.FunctionDef) and n.name=='_collect_named_log_files');limit=next(ast.literal_eval(n.value) for n in system_tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='_LOG_DOWNLOAD_LIMIT' for t in n.targets));pattern=next(n.value for n in system_tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='_LOG_DOWNLOAD_NAME_PATTERN' for t in n.targets))
class HTTPException(Exception):pass
with tempfile.TemporaryDirectory(prefix='owned-log-source-') as tmp:
 root=pathlib.Path(tmp);(root/'plugins').mkdir();files={}
 for name,mtime in [('owned.log',1)]+[(f'owned.log.{i}',100+i) for i in range(1,12)]:
  mtime+=946684800
  p=root/'plugins'/name;p.write_text(name+'\n');os.chmod(p,0o600);os.utime(p,(mtime,mtime));files[name]=mtime
 ns={'Path':pathlib.Path,'settings':types.SimpleNamespace(LOG_PATH=root),'_LOG_DOWNLOAD_LIMIT':limit,'_LOG_DOWNLOAD_NAME_PATTERN':eval(compile(ast.Expression(pattern),'<fixed pattern>','eval'),{'re':re}),'HTTPException':HTTPException}
 exec(compile(ast.Module(body=[select],type_ignores=[]),'<fixed log file selection>','exec'),ns)
 selection=[p.name for p in ns['_collect_named_log_files'](' Owned ')]
 build_zip=next(n for n in system_tree.body if isinstance(n,ast.FunctionDef) and n.name=='_build_log_zip_data')
 ns.update(io=io,zipfile=zipfile,datetime=datetime,SecurityUtils=types.SimpleNamespace(is_safe_path=lambda base_path,user_path:user_path.resolve().is_relative_to(base_path.resolve())))
 exec(compile(ast.Module(body=[build_zip],type_ignores=[]),'<fixed ZIP builder>','exec'),ns)
 archive_bytes,stem=ns['_build_log_zip_data'](' Owned ')
 with zipfile.ZipFile(io.BytesIO(archive_bytes)) as archive:
  zip_metadata=[dict(name=pathlib.Path(f.filename).name,mode=(f.external_attr>>16)&0o777,compression=f.compress_type,date_time=f.date_time,epoch_seconds=int(time.mktime(f.date_time+(0,0,-1)))) for f in archive.infolist()]
 zip_invalid=[]
 for year in [1970,2110]:
  stamp=datetime(year,1,1).timestamp();os.utime(root/'plugins/owned.log',(stamp,stamp))
  try:ns['_build_log_zip_data']('Owned');raise AssertionError('fixed ZIP unexpectedly accepted year')
  except (ValueError,OverflowError) as error:zip_invalid.append(dict(year=year,error=type(error).__name__))
  except Exception as error:
   if type(error).__name__!='error':raise
   zip_invalid.append(dict(year=year,error=type(error).__name__))
 current_text=(root/'plugins/owned.log').read_text();plain_current='\n'.join(current_text.split('\n')[::-1])
# Rotation cases directly exercise the stdlib handler used by the fixed source.
# Threshold uses Unicode len(format(record)+newline), combined with tell bytes.
record=logging.LogRecord('owned',logging.INFO,'',0,'plugins - 中文界',(),None);formatter=logging.Formatter(values['LOG_FILE_FORMAT']);line=formatter.format(record)+'\n';threshold=values['LOG_MAX_FILE_SIZE']*1024*1024
rotation=[]
for name,size in [('below',threshold-len(line)-1),('equal',threshold-len(line)),('empty',0)]:
 with tempfile.TemporaryDirectory(prefix='owned-rotate-source-') as tmp:
  p=pathlib.Path(tmp)/'owned.log';p.touch();with_file=open(p,'r+b');with_file.truncate(size);with_file.close();handler=SourceRotatingFileHandler(p,maxBytes=threshold,backupCount=values['LOG_BACKUP_COUNT'],encoding='utf-8');handler.setFormatter(formatter);rotates=handler.shouldRollover(record);handler.close();rotation.append(dict(name=name,size=size,line_characters=len(line),line_utf8_bytes=len(line.encode()),rotates=rotates))
output={'source':{'commit':'8a83efeacd3e26b82be495e17d50146e31c96a60','log_sha256':hashlib.sha256(log_path.read_bytes()).hexdigest(),'system_sha256':hashlib.sha256(system_path.read_bytes()).hexdigest()},'kind':'fixed defaults/selector/ZIP builder with actual temporary files; actual pinned CPython3.12.13 rollover methods on native file handles','stdlib':{'url':stdlib_url,'sha256':stdlib_sha,'functions':['shouldRollover','doRollover']},'defaults':values,'download_limit':limit,'files_mtimes':files,'selected_names':selection,'plain_current':plain_current,'rotation':rotation,'zip_metadata':zip_metadata,'zip_invalid_years':zip_invalid}
ns=dict(BaseModel=BaseModel,ConfigDict=ConfigDict,Optional=Optional)
exec(compile(ast.fix_missing_locations(ast.Module(body=[model],type_ignores=[])),'<fixed LogConfigModel>','exec'),ns)
integers=[]
for raw in ['0','-2','+3',' 3 ','3.0','3.','1_0','1e0','true','','NaN','3.1','１２','9223372036854775808123','1'*4301]:
 try:value=ns['LogConfigModel'].model_validate({'LOG_MAX_FILE_SIZE':raw,'LOG_BACKUP_COUNT':raw});ok=True;parsed=str(value.LOG_MAX_FILE_SIZE);assert value.LOG_BACKUP_COUNT==value.LOG_MAX_FILE_SIZE
 except ValidationError:ok=False;parsed=None
 integers.append(dict(raw=raw,ok=ok,value=parsed))
configured=[]
for name,mib,backups in [('max_zero',0,3),('max_negative',-1,3),('backup_zero',1,0),('backup_negative',1,-2),('backup_three',1,3)]:
 with tempfile.TemporaryDirectory(prefix='owned-configured-source-') as tmp:
  p=pathlib.Path(tmp)/'owned.log';p.write_text('owned prefix\n');f=open(p,'r+b');f.truncate((1<<20)-1);f.close();(p.parent/'owned.log.1').write_text('owned previous backup\n')
  handler=SourceRotatingFileHandler(p,maxBytes=mib*(1<<20),backupCount=backups,encoding='utf-8');handler.setFormatter(formatter);handler.emit(record);handler.close()
  content=p.read_bytes();backup=(p.parent/'owned.log.1').read_bytes()
  configured.append(dict(name=name,mib=mib,backups=backups,current_keeps_prefix=content.startswith(b'owned prefix\n'),previous_backup_preserved=backup==b'owned previous backup\n',files=sorted(f.name for f in p.parent.iterdir())))
output.update(pydantic_version=pydantic.__version__,integer_inputs=integers,configured_rotation=configured)
pathlib.Path(__file__).with_name('file-source-oracle.json').write_text(json.dumps(output,ensure_ascii=False,indent=2)+'\n');print('fixed defaults/selection/rotation replay generated')
