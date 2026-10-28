"""Pinned UID algorithm; synthetic filesystem/node inputs, no host ID output."""
import ast,hashlib,json,subprocess,sys
from pathlib import Path
from types import SimpleNamespace as NS

commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/utils/system.py'])
method=next(n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name=='generate_user_unique_id')
method.decorator_list=[]
source=ast.unparse(method)
cases=[]
for kind,device,inode,root_failure,node in [('filesystem',5,9,False,None),('zero_filesystem',0,0,False,None),('fallback_mac',0,0,True,0x001122334455),('fallback_local_mac',0,0,True,0x021122334455),('reject_multicast',0,0,True,0x011122334455),('both_unavailable',0,0,True,None)]:
    calls=[]
    def stat(path):
        assert path=='/'
        if root_failure: raise OSError('owned unavailable root')
        return NS(st_dev=device,st_ino=inode)
    def getnode():
        calls.append('node')
        if node is None: raise OSError('owned unavailable node')
        return node
    ns=dict(os=NS(stat=stat),uuid=NS(getnode=getnode),hashlib=hashlib,print=lambda *args:None)
    exec(compile(source,'fixed/SystemUtils.generate_user_unique_id','exec'),ns)
    uid=ns['generate_user_unique_id']()
    cases.append(dict(kind=kind,device=device,inode=inode,root_failure=root_failure,node=node,uid=uid,node_called=bool(calls)))
assert not cases[0]['node_called'] and cases[4]['uid'] is None and cases[5]['uid'] is None
config_raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':app/core/config.py'])
version_raw=subprocess.check_output(['git','-C',sys.argv[1],'show',commit+':version.py'])
version=ast.literal_eval(next(n.value for n in ast.parse(version_raw).body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='APP_VERSION' for t in n.targets)))
project=ast.literal_eval(next(n.value for n in ast.walk(ast.parse(config_raw)) if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id=='PROJECT_NAME'))
server_default=ast.literal_eval(next(n.value for n in ast.walk(ast.parse(config_raw)) if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id=='MP_SERVER_HOST'))
share_default=ast.literal_eval(next(n.value for n in ast.walk(ast.parse(config_raw)) if isinstance(n,ast.AnnAssign) and isinstance(n.target,ast.Name) and n.target.id=='PLUGIN_STATISTIC_SHARE'))
cpu_methods=[n for n in ast.walk(ast.parse(raw)) if isinstance(n,ast.FunctionDef) and n.name in ['cpu_arch','is_x86_64','is_x86_32','is_aarch64','is_aarch']]
cpu_source=ast.unparse(ast.ClassDef(name='SystemUtils',bases=[],keywords=[],body=cpu_methods,decorator_list=[],type_params=[]))
agent_method=next(n for n in ast.walk(ast.parse(config_raw)) if isinstance(n,ast.FunctionDef) and n.name=='USER_AGENT');agent_method.decorator_list=[]
agent_source=ast.unparse(agent_method)
cpu_cases=[]
for machine in ['x86_64','AMD64','i386','i686','x86','386','x86_32','aarch64','arm64','armv7l','AARCH32','riscv64']:
    ns=dict(platform=NS(machine=lambda:machine,system=lambda:'Linux',release=lambda:'owned-test-kernel'),APP_VERSION=version)
    exec(compile(cpu_source,'fixed/SystemUtils.cpu_arch','exec'),ns);exec(compile(agent_source,'fixed/settings.USER_AGENT','exec'),ns)
    cpu_cases.append(dict(machine=machine,arch=ns['SystemUtils'].cpu_arch(),user_agent=ns['USER_AGENT'](NS(PROJECT_NAME=project))))
Path(__file__).with_name('server-uid-oracle.json').write_text(json.dumps(dict(source_commit=commit,source_path='app/utils/system.py',source_sha256=hashlib.sha256(raw).hexdigest(),config_sha256=hashlib.sha256(config_raw).hexdigest(),version_sha256=hashlib.sha256(version_raw).hexdigest(),method_source=source,cpu_methods_source=cpu_source,user_agent_method_source=agent_source,compatibility_version=version,project_name=project,server_default=server_default,share_default=share_default,kind='actual pinned methods/default literals; synthetic filesystem/node/platform boundaries; no physical host identity saved',cases=cases,cpu_cases=cpu_cases),indent=2)+'\n')
