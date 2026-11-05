#!/usr/bin/env python3
"""Fixed selected Settings field/validator in an owned isolated Pydantic process."""
import ast,hashlib,json,pathlib,shlex,subprocess
OUT=pathlib.Path(__file__).resolve().parent;COMMIT='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',COMMIT+':app/core/config.py']);tree=ast.parse(raw)
config=next(n for n in tree.body if isinstance(n,ast.ClassDef)and n.name=='ConfigModel');settings=next(n for n in tree.body if isinstance(n,ast.ClassDef)and n.name=='Settings')
field=next(n for n in config.body if isinstance(n,ast.AnnAssign)and n.target.id=='RMT_MEDIAEXT')
methods=[n for n in settings.body if isinstance(n,ast.FunctionDef)and n.name in {'generic_type_converter','generic_type_validator'}]
live_methods=[n for n in settings.body if isinstance(n,ast.FunctionDef)and n.name in {'generic_type_converter','generic_type_validator','update_setting','update_settings'}]
live_source=ast.unparse(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='OwnedLiveSettings',bases=[ast.Name(id='BaseSettings',ctx=ast.Load())],keywords=[],body=[ast.Assign(targets=[ast.Name(id='model_config',ctx=ast.Store())],value=ast.Call(func=ast.Name(id='SettingsConfigDict',ctx=ast.Load()),args=[],keywords=[ast.keyword(arg='case_sensitive',value=ast.Constant(value=True))])),field,*live_methods],decorator_list=[])],type_ignores=[])))
source=ast.unparse(ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='OwnedSettings',bases=[ast.Name(id='BaseModel',ctx=ast.Load())],keywords=[],body=[field,*methods],decorator_list=[])],type_ignores=[])))
cases=[dict(case='default')]
for name,value in [('empty',[]),('ordered_case_duplicates',['.UPPER','.mkv','.mkv']),('custom',['.owned']),('null',None),('object',{}),('false',False),('zero',0),('json_string','[".custom"]'),('literal_empty_string','[]'),('invalid_json','broken'),('spaced_json','  [".srt"]  '),('unsupported_native_member',[1,None,True])]:cases.append(dict(case=name,configuration=value))
for name,value in [('environment_empty',[]),('environment_custom',['.env','.UPPER','.env'])]:cases.append(dict(case=name,configuration=['.configured'],environment=value))
program=r'''import sys,json,copy,typing,importlib.metadata,os,types
sys.path.append('/opt/venv/lib/python3.12/site-packages')
from pydantic import BaseModel,Field,model_validator
from pydantic_settings import BaseSettings,SettingsConfigDict
from typing import *
r=json.loads(sys.stdin.readline());logger=type('Log',(),{'error':staticmethod(lambda*x:None)})();exec(r['source'])
updates=[];OwnedSettings.update_env_config=staticmethod(lambda*args:updates.append(args))
rows=[]
for c in r['cases']:
 values={}
 if 'configuration'in c:values['RMT_MEDIAEXT']=c['configuration']
 if 'environment'in c:values['RMT_MEDIAEXT']=c['environment']
 row=dict(c)
 try:row['expected']=OwnedSettings(**copy.deepcopy(values)).RMT_MEDIAEXT
 except Exception as e:row['error']=type(e).__name__
 rows.append(row)
exec(r['live_source']);Settings=OwnedLiveSettings;log_settings=types.SimpleNamespace()
OwnedLiveSettings.update_env_config=staticmethod(lambda*args:(True,''))
os.environ['RMT_MEDIAEXT']='[".startup-env"]';live=OwnedLiveSettings();initial=list(live.RMT_MEDIAEXT)
os.environ['RMT_MEDIAEXT']='[".late-env"]';after_environment_change=list(live.RMT_MEDIAEXT)
updated=live.update_setting('RMT_MEDIAEXT',['.runtime',1,None,True]);after_api=list(live.RMT_MEDIAEXT)
cold=OwnedLiveSettings()
live_observation=dict(initial=initial,after_environment_change=after_environment_change,update_result=updated,after_api=after_api,cold=cold.RMT_MEDIAEXT,real_base_settings_environment=True,env_file_disabled=True,update_env_config_boundary='owned success recorder; no app.env writes')
print(json.dumps(dict(source_commit=r['commit'],source_sha256=r['sha256'],pydantic=importlib.metadata.version('pydantic'),classification='fixed selected actual field/default factory + generic converter/validator AST and real Pydantic; explicit native environment-over-config input boundary, no full App settings init or config I/O',cases=rows,live_settings=live_observation,production_writes=0),ensure_ascii=False))
'''
def docker(*args,data=None,check=True):return subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',shlex.join(['docker',*args])],input=data,capture_output=True,check=check,timeout=60)
original='d1fd69248ee9';image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
def snapshot():return docker('inspect','--format','{{.State.Pid}} {{.State.StartedAt}} {{.RestartCount}}',original).stdout.decode().strip()
before=snapshot();cid=None;name='light-media-owned-rmt-source-20261101'
try:
 cid=docker('create','-i','--name',name,'--network','none','--read-only','--no-healthcheck','--cpus','1','--memory','384m','--pids-limit','64','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c','import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-rmt-source","exec"))').stdout.decode().strip()
 result=docker('start','-a','-i',cid,data=('exec('+repr(program)+')\n'+json.dumps(dict(commit=COMMIT,sha256=hashlib.sha256(raw).hexdigest(),source=source,live_source=live_source,cases=cases))+'\n').encode(),check=False)
 (OUT/'rmt-source-stderr.log').write_bytes(result.stderr);assert result.returncode==0,result.stderr.decode();value=json.loads(result.stdout)
 (OUT/'rmt-source-oracle.json').write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(value['cases']),'pydantic':value['pydantic'],'errors':sum('error'in c for c in value['cases'])}))
finally:
 if cid:docker('rm','-f','-v',cid)
 after=snapshot();absent=not docker('ps','-a','--filter','name=^/'+name+'$','--format','{{.ID}}').stdout.strip()
 (OUT/'rmt-source-cleanup.json').write_text(json.dumps(dict(original_before=before,original_after=after,original_unchanged=before==after,owned_container_absent=absent,driver_sha256=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()),indent=2)+'\n');assert before==after and absent
