#!/usr/bin/env python3
import hashlib,json,pathlib,shlex,subprocess
p=pathlib.Path(__file__).resolve().parent
program=r'''import sys,pathlib,hashlib,json,os,inspect,importlib.metadata,ast
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import cloakbrowser.browser as b
launch_source=inspect.getsource(b.launch)
assignments=[n for n in ast.walk(ast.parse(launch_source))if isinstance(n,ast.Assign)and any('proxy_kwargs'in ast.unparse(x)for x in n.targets)and isinstance(n.value,ast.Call)]
assert len(assignments)==1,[(ast.unparse(n))for n in assignments]
name=assignments[0].value.func.id;fn=getattr(b,name);signature=inspect.signature(fn)
required={n:None for n,v in signature.parameters.items()if n!='proxy'and v.default is inspect.Parameter.empty}
functions={n:inspect.getsource(getattr(b,n))for n in ['launch_context','launch',name]}
rows=[]
for mode,environment in [('unset',{}),('uppercase',{'HTTP_PROXY':'http://owned.invalid:8123','HTTPS_PROXY':'http://owned.invalid:8124','ALL_PROXY':'socks5://owned.invalid:8125'}),('lowercase',{'http_proxy':'http://owned.invalid:8126','https_proxy':'http://owned.invalid:8127'})]:
 for n in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(n,None)
 os.environ.update(environment)
 rows.append({'case':mode,'environment':environment,'actual_none_proxy_result':fn(None,**required)})
source=pathlib.Path(b.__file__).read_bytes()
print(json.dumps({'classification':'actual paired wrapper _proxy_launch_config(None) under controlled environment in disposable read-only/no-network image; launch_context->launch->proxy config source retained; no old service configuration or native browser execution in this oracle','wrapper_version':importlib.metadata.version('cloakbrowser'),'playwright_version':importlib.metadata.version('playwright'),'proxy_function':name,'signature':str(signature),'module_sha256':hashlib.sha256(source).hexdigest(),'functions':functions,'cases':rows},indent=2))
'''
image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
command=shlex.join(['docker','run','--rm','--name','light-media-browser-proxy-none-source-r2','--network','none','--read-only','--no-healthcheck','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c',program])
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',command],capture_output=True,timeout=120)
(p/'cloakbrowser-proxy-none-source-stderr.log').write_bytes(r.stderr);assert r.returncode==0,r.stderr
j=json.loads(r.stdout);j['dependency_image']=image;j['driver_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest();j['program_sha256']=hashlib.sha256(program.encode()).hexdigest()
(p/'cloakbrowser-proxy-none-source-oracle.json').write_text(json.dumps(j,indent=2)+'\n');print(j['wrapper_version'],j['playwright_version'],j['cases'])
