#!/usr/bin/env python3
"""Fixed original ORM/normalizer in owned network-none, read-only container.

No source service imports or mounts; source text arrives from fixed git objects.
SQLite is in memory. The production container is only inspected before/after.
"""
import argparse,ast,hashlib,json,shlex,subprocess
from pathlib import Path

def main():
    p=argparse.ArgumentParser();p.add_argument('--source-repo',required=True);p.add_argument('--directory',required=True);args=p.parse_args()
    out=Path(args.directory);out.mkdir(parents=True,exist_ok=False)
    commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
    paths=['app/db/models/subscribe.py','app/db/subscribe_oper.py','app/db/__init__.py']
    sources={path:subprocess.check_output(['git','-C',args.source_repo,'show',commit+':'+path]).decode() for path in paths}
    model=next(n for n in ast.parse(sources[paths[0]]).body if isinstance(n,ast.ClassDef) and n.name=='Subscribe')
    body=[ast.Assign(targets=[ast.Name(id='__tablename__',ctx=ast.Store())],value=ast.Constant('owned_subscribe'))]+[n for n in model.body if isinstance(n,ast.Assign)]
    orm=ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='Subscribe',bases=[ast.Name(id='Base',ctx=ast.Load())],keywords=[],body=body,decorator_list=[])],type_ignores=[]))
    base=next(n for n in ast.parse(sources[paths[2]]).body if isinstance(n,ast.ClassDef) and n.name=='Base')
    setter=next(n for n in base.body if isinstance(n,ast.FunctionDef) and n.name=='update');setter.decorator_list=[]
    mixin=ast.fix_missing_locations(ast.Module(body=[ast.ClassDef(name='SourceBase',bases=[],keywords=[],body=[setter],decorator_list=[])],type_ignores=[]))
    oper=ast.parse(sources[paths[1]]);normal=[n for n in oper.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='INTEGER_FLAG_FIELDS' for t in n.targets) or isinstance(n,ast.FunctionDef) and n.name=='_normalize_integer_flags']
    normal=ast.fix_missing_locations(ast.Module(body=normal,type_ignores=[]))
    full={'name':'Updated','imdbid':'tt0000042','year':'02025','poster':'','backdrop':'new backdrop','vote':8.1,'description':'','state':'P','season':2,'total_episode':24,'start_episode':3,'lack_episode':21,'note':[3,4],'downloader':'Owned downloader','best_version':2,'best_version_full':-1,'current_priority':99,'episode_priority':{'3':99},'search_imdbid':True,'manual_total_episode':True,'custom_words':'Owned => New','include':'','exclude':'','quality':'','resolution':'','effect':'','keyword':'Keyword','save_path':'/owned/download','sites':[],'filter_groups':[]}
    nullable=[k for k in full if k not in ['name','state','season']]
    cases=[{'name':'all_native_fields','payload':full},{'name':'equal_fields','payload':full},{'name':'null_native_columns','payload':dict.fromkeys(nullable)},{'name':'empty_values','payload':{'year':'','downloader':'','note':[],'episode_priority':{},'include':'','quality':'','custom_words':'','sites':[],'filter_groups':[],'best_version':False,'best_version_full':0,'search_imdbid':False,'manual_total_episode':0}},{'name':'empty_patch','payload':{}}]
    for value in [2,-1,None,9223372036854775807,9223372036854775806,-9223372036854775808,9223372036854775808,True,False,1.5,'not-an-integer','2','0','00','2.0',' -1 ','1e2','1e30','9223372036854775808','0x10','',' ','\u00a02',1.0,-0.0,0.5,-1.5,1e-7]:
        cases.append({'name':'integer_flag_'+repr(value),'payload':{'best_version':value}})
    for key in ['name','state']:cases.append({'name':'not_null_'+key,'payload':{key:None}})
    request={'commit':commit,'source_hashes':{k:hashlib.sha256(v.encode()).hexdigest() for k,v in sources.items()},'orm':ast.unparse(orm),'base':ast.unparse(mixin),'normalizer':ast.unparse(normal),'cases':cases}
    program=r'''import sys,json,hashlib,importlib.metadata
sys.path.append('/opt/venv/lib/python3.12/site-packages')
from typing import Tuple
from sqlalchemy import Column,Integer,String,Float,JSON,Index,inspect,create_engine
from sqlalchemy.orm import declarative_base,Session
r=json.loads(sys.stdin.readline())
exec(r['base']);Base=declarative_base(cls=SourceBase)
def get_id_column():return Column(Integer,primary_key=True)
exec(r['orm']);exec(r['normalizer'])
engine=create_engine('sqlite:///:memory:');Base.metadata.create_all(engine)
results=[]
with Session(engine) as db:
 row=Subscribe(id=1,name='Owned',year='2024',type='电视剧',state='S',season=1,total_episode=12,start_episode=1,best_version=0,best_version_full=0,search_imdbid=0,manual_total_episode=0)
 db.add(row);db.commit();db.refresh(row)
 for case in r['cases']:
  before={c.name:getattr(row,c.name) for c in Subscribe.__table__.columns}
  error=None
  try:row.update(db,_normalize_integer_flags(case['payload']));db.commit();db.refresh(row)
  except Exception as exc:error=type(exc).__name__;db.rollback();db.refresh(row)
  after={c.name:getattr(row,c.name) for c in Subscribe.__table__.columns}
  results.append(dict(case,before=before,after=after,error_class=error))
print(json.dumps({'source_commit':r['commit'],'classification':'actual SQLAlchemy source ORM columns + fixed Base.update/normalizer AST, owned in-memory SQLite, no source service imports/config/DB','sqlalchemy':importlib.metadata.version('SQLAlchemy'),'source_hashes':r['source_hashes'],'cases':results,'production_writes':0,'network_calls':0},ensure_ascii=False))
'''
    source='d1fd69248ee9fca7ed18b34ac7c121d98f982b8979fce065eeb50968e7c27bba';image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
    def docker(*parts,data=None,check=True):return subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',shlex.join(['docker',*parts])],input=data,capture_output=True,check=check,timeout=60)
    def snapshot():return docker('inspect','--format','{{.Id}} {{.Image}} {{.State.Pid}} {{.State.StartedAt}} {{.RestartCount}}',source).stdout.decode().strip()
    before=snapshot();assert before.startswith(source+' '+image);cid=None;name='light-media-owned-subscription-source-20261031'
    try:
        cid=docker('create','-i','--name',name,'--network','none','--read-only','--no-healthcheck','--cpus','1','--memory','512m','--pids-limit','64','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--tmpfs','/tmp:rw,noexec,nosuid,size=32m,uid=1000,gid=1001','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c','import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-subscription-source","exec"))').stdout.decode().strip()
        run=docker('start','-a','-i',cid,data=('exec('+repr(program)+')\n'+json.dumps(request)+'\n').encode(),check=False)
        (out/'stderr.log').write_bytes(run.stderr);assert run.returncode==0,(run.returncode,run.stderr.decode())
        result=json.loads(run.stdout);assert len(result['cases'])==len(cases)
        (out/'oracle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(json.dumps({'cases':len(cases),'source_commit':commit,'sqlalchemy':result['sqlalchemy']}))
    finally:
        if cid:docker('rm','-f','-v',cid)
        after=snapshot();absent=not docker('ps','-a','--filter','name=^/'+name+'$','--format','{{.ID}}').stdout.strip()
        (out/'cleanup.json').write_text(json.dumps({'source_before':before,'source_after':after,'source_unchanged':before==after,'owned_container_absent':absent,'driver_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest()},indent=2)+'\n');assert before==after and absent
if __name__=='__main__':main()
