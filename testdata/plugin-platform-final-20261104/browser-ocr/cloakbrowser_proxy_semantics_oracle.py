#!/usr/bin/env python3
import hashlib,json,pathlib,shlex,subprocess
p=pathlib.Path(__file__).resolve().parent
program=r'''import sys,pathlib,hashlib,json,inspect,importlib.metadata,ast,re,subprocess
sys.path.append('/opt/venv/lib/python3.12/site-packages')
import cloakbrowser.browser as b
launch_source=inspect.getsource(b.launch)
assignments=[n for n in ast.walk(ast.parse(launch_source))if isinstance(n,ast.Assign)and any('proxy_kwargs'in ast.unparse(x)for x in n.targets)and isinstance(n.value,ast.Call)]
assert len(assignments)==1
name=assignments[0].value.func.id;fn=getattr(b,name)
root=pathlib.Path('/opt/venv/lib/python3.12/site-packages/playwright/driver')
core=root/'package/lib/coreBundle.js';text=core.read_text()
normalizer=re.search(r'function normalizeProxySettings\([^)]*\)\s*\{.*?\n\}',text,re.S)
assert normalizer,'paired Playwright native proxy normalizer missing'
node=r"const fs=require('fs');const x=JSON.parse(fs.readFileSync(0,'utf8'));try{const result=eval('('+x.definition+')')(x.proxy);process.stdout.write(JSON.stringify({result}));}catch(e){process.stdout.write(JSON.stringify({error_type:e.name,error_message:e.message,error_code:e.code}));}"
indexes=[m.start()for m in re.finditer(re.escape('options.proxyOverride || options.proxy'),text)]
fragments=[text[max(0,i-350):i+2400]for i in indexes if 'chromeArguments'in text[i:i+2400]]
assert fragments
fragment=fragments
authindexes=[m.start()for m in re.finditer(re.escape('proxy && proxy.username'),text)]
authfragments=[text[max(0,i-300):i+800]for i in authindexes]
rows=[]
for row in json.loads(sys.stdin.readline()):
 proxy=row['proxy_server'];resolved=fn(proxy);normalized=None
 if 'proxy'in resolved[0]:
  result=subprocess.run([str(root/'node'),'-e',node],input=json.dumps({'definition':normalizer.group(),'proxy':resolved[0]['proxy']}).encode(),capture_output=True,check=True)
  normalized=json.loads(result.stdout)
 rows.append({'case':row['name'],'proxy_host':row['proxy_host'],'source_proxy_server':proxy,'actual_paired_launch_proxy':resolved,'actual_playwright_normalizer':normalized})
module=pathlib.Path(b.__file__).read_bytes()
print(json.dumps({'classification':'actual paired wrapper proxy resolution plus actual extracted Playwright Node proxy normalizer; no native browser launched and no old service/config executed','wrapper_version':importlib.metadata.version('cloakbrowser'),'playwright_version':importlib.metadata.version('playwright'),'wrapper_module_sha256':hashlib.sha256(module).hexdigest(),'core_bundle_sha256':hashlib.sha256(core.read_bytes()).hexdigest(),'proxy_resolver_source':inspect.getsource(fn),'http_reconstructor_source':inspect.getsource(b._reconstruct_http_url),'socks_reconstructor_source':inspect.getsource(b._reconstruct_socks_url),'playwright_proxy_auth_source':authfragments,'playwright_native_proxy_args_source':fragment,'playwright_normalizer_source':normalizer.group(),'cases':rows},indent=2))
'''
source_path=p.parent/'settings/proxy-settings-independent-source-oracle.json';source=json.loads(source_path.read_text());cases=[r for r in source['cases']if r['name']in['explicit-encoded-auth','explicit-ipv6']]
cases.append(dict(name='explicit-password-only',proxy_host='http://:synthetic@proxy.example:3128',proxy_server=dict(server='http://proxy.example:3128',password='synthetic')))
for scheme in ['socks5','socks5h']:cases.append(dict(name=scheme+'-auth',proxy_host=scheme+'://owned:synthetic@proxy.example:1080',proxy_server=dict(server=scheme+'://proxy.example:1080',username='owned',password='synthetic')))
image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
command=shlex.join(['docker','run','--rm','-i','--name','light-media-browser-proxy-semantics-source-r5','--network','none','--read-only','--no-healthcheck','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c',program])
r=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',command],input=json.dumps(cases).encode()+b'\n',capture_output=True,timeout=120)
(p/'cloakbrowser-proxy-semantics-source-stderr.log').write_bytes(r.stderr);assert r.returncode==0,r.stderr
j=json.loads(r.stdout);j['additional_controlled_library_input']='explicit-password-only supplies a source-shaped controlled dict; this case is actual paired wrapper execution, not claimed to be in the17-getter oracle';j['dependency_image']=image;j['source_getter_oracle_sha256']=hashlib.sha256(source_path.read_bytes()).hexdigest();j['driver_sha256']=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest();j['program_sha256']=hashlib.sha256(program.encode()).hexdigest()
(p/'cloakbrowser-proxy-semantics-source-oracle.json').write_text(json.dumps(j,indent=2)+'\n');print(j['cases'])
