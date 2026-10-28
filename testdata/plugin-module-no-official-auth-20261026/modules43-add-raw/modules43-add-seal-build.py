import hashlib,json,os,pathlib,subprocess,time
root=pathlib.Path.cwd();out=root/'work/plugin-no-official-auth-20261026';env=os.environ|{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}
def inventory(target,test=False):
 cmd=['go','list','-deps']+(['-test']if test else[])+['-json',target]
 data=subprocess.check_output(cmd,env=env,text=True);decoder=json.JSONDecoder();pos=0;paths={root/'go.mod',root/'go.sum'}
 while pos<len(data):
  while pos<len(data)and data[pos].isspace():pos+=1
  if pos==len(data):break
  package,n=decoder.raw_decode(data,pos);pos=n
  for key in ['GoFiles','CgoFiles','SFiles','HFiles','SysoFiles','EmbedFiles']:
   for name in package.get(key,[]):paths.add(pathlib.Path(package['Dir'])/name)
 return {str(p):hashlib.sha256(p.read_bytes()).hexdigest()for p in sorted(paths)if p.is_file()}
a={'server':inventory('./cmd/server'),'test':inventory('./internal/app',True)};(out/'modules43-add-build-inputs.json').write_text(json.dumps(a,indent=2)+'\n');start=time.monotonic();results=[]
commands=[('server-modules43-add-linux',['go','build','-trimpath','-o',str(out/'server-modules43-add-linux'),'./cmd/server']),('process-modules43-add-linux.test',['go','test','-c','-o',str(out/'process-modules43-add-linux.test'),'./internal/app'])]
for name,cmd in commands:
 with(out/(name+'.build.log')).open('w')as f:subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
 results.append({'name':name,'command':cmd,'sha256':hashlib.sha256((out/name).read_bytes()).hexdigest()});print(results[-1],flush=True)
b={'server':inventory('./cmd/server'),'test':inventory('./internal/app',True)};changed={kind:[p for p in a[kind].keys()|b[kind].keys()if a[kind].get(p)!=b[kind].get(p)]for kind in a};result={'environment':{k:env[k]for k in ['GOOS','GOARCH','CGO_ENABLED']},'go_version':subprocess.check_output(['go','version'],text=True).strip(),'inputs':{k:len(v)for k,v in a.items()},'changed_during_build':changed,'elapsed_seconds':round(time.monotonic()-start,2),'binaries':results};(out/'modules43-add-build-result.json').write_text(json.dumps(result,indent=2)+'\n');print('stable',not any(changed.values()),'inputs',result['inputs'],flush=True)
if any(changed.values()):raise SystemExit(2)
