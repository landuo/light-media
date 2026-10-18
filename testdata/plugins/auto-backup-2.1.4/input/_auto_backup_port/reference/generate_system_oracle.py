import ast,hashlib,io,json,os,pathlib,sys,types
R=pathlib.Path(__file__).parent;O=pathlib.Path(os.environ.get('AUTOBACKUP_ORACLE_OUTPUT',str(R)));O.mkdir(parents=True,exist_ok=True)
class Log:
 def __getattr__(self,k):return lambda *a,**kw:None
raw=(R/'app-helper-system.py').read_bytes();tree=ast.parse(raw);cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='SystemHelper');cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name=='_get_container_id'];cls.bases=[]
mounts=['','unrelated','resolv.conf','/resolv.conf','/a/resolv.conf','/var/lib/docker/containers/'+'a'*64+'/resolv.conf mounted','/short/resolv.conf previous /docker/'+'b'*64+' /sys/fs/cgroup/devices cgroup','/a/resolv.conf /b /sys/fs/cgroup/devices',' /  /resolv.conf ', '/docker/\u2003'+'c'*24+'\u2003/resolv.conf']
container=[]
for data in mounts:
 ns=dict(ConfigReloadMixin=object,logger=Log(),open=lambda *a,**kw:io.StringIO(data));exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'installed-helper-system','exec'),ns);container.append(dict(input=data,output=ns['SystemHelper']._get_container_id()))
praw=(R/'installed-plugin.py').read_bytes();tree=ast.parse(praw);cls=next(n for n in tree.body if isinstance(n,ast.ClassDef) and n.name=='AutoBackup');cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ('_get_postgresql_major_version','install_postgresql_client')];cls.bases=[]
class DB:
 def __init__(self,value):self.value=value
 def execute(self,sql):
  if self.value=='__error__':raise RuntimeError('owned query failure')
  return types.SimpleNamespace(fetchone=lambda:self.value)
versions=[]
ns=dict(db_query=lambda f:f,Session=object,logger=Log(),text=lambda x:x);exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'installed-plugin-postgres','exec'),ns);P=ns['AutoBackup']
for row in [None,[],['17.5'],['9.6.2'],['18beta1'],['１８.1'],[17],[18],[18.2],[None],'__error__']:
 versions.append(dict(row=row,output=P._get_postgresql_major_version(db=DB(row))))
plans=[]
for uid in (0,1000):
 for docker in (False,True):
  for cid in (None,'owned-container'):
   for proxy in ('','http://proxy.invalid:8080','https://proxy.invalid','HTTP://ignored'):
    calls=[]
    def exec_run(**kw):calls.append(['exec_run',kw]);return 0,b''
    def docker_client(**kw):calls.append(['docker_client',kw]);return types.SimpleNamespace(containers=types.SimpleNamespace(get=lambda container:calls.append(['container_get',container]) or types.SimpleNamespace(exec_run=exec_run)))
    ns.update(settings=types.SimpleNamespace(PROXY_HOST=proxy,DOCKER_CLIENT_API='unix:///owned/docker.sock'),os=types.SimpleNamespace(geteuid=lambda:uid),SystemUtils=types.SimpleNamespace(is_docker=lambda:docker),SystemHelper=types.SimpleNamespace(_get_container_id=lambda:cid),docker=types.SimpleNamespace(DockerClient=docker_client),subprocess=types.SimpleNamespace(run=lambda *a,**kw:calls.append(['run',list(a[0]),kw])))
    P.install_postgresql_client(17)
    call=calls[-1];is_container=call[0]=='exec_run';environment=call[1]['environment'] if is_container else call[2]['env'];plans.append(dict(uid=uid,docker=docker,container_id=cid,proxy=proxy,plan=dict(major_version=17,execution='container' if is_container else 'local',container_id=cid if is_container else None,environment=environment,original_package='postgresql-17'),source_calls=calls))
(O/'system-oracle.json').write_text(json.dumps(dict(source_sha256=hashlib.sha256(praw).hexdigest(),helper_source_sha256=hashlib.sha256(raw).hexdigest(),runtime=sys.version,container=container,versions=versions,plans=plans),ensure_ascii=False,indent=2)+'\n')
print(json.dumps(dict(container_cases=len(container),version_cases=len(versions),provision_cases=len(plans))))
