import hashlib,json,os,pathlib,subprocess,time
r=pathlib.Path.cwd();w=r/'work/deploy-main-20261029';sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest();commit=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();remote=subprocess.check_output(['git','ls-remote','--heads','origin','main'],text=True).split()[0];assert commit==remote;assert not subprocess.check_output(['git','status','--porcelain'],text=True).strip()
env=os.environ|{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}
raw=subprocess.check_output(['go','list','-deps','-tags','modernconly','-json','./cmd/server'],env=env,text=True);decoder=json.JSONDecoder();pos=0;files={r/'go.mod',r/'go.sum'}
while pos<len(raw):
 while pos<len(raw)and raw[pos].isspace():pos+=1
 if pos==len(raw):break
 pkg,pos=decoder.raw_decode(raw,pos)
 for key in ['GoFiles','CgoFiles','SFiles','HFiles','SysoFiles','EmbedFiles']:
  for name in pkg.get(key,[]):files.add(pathlib.Path(pkg['Dir'])/name)
inputs={str(p):sha(p)for p in sorted(files)if p.is_file()};(w/'release-build-inputs.json').write_text(json.dumps(inputs,indent=2)+'\n');out=w/'light-media-server';cmd=['go','build','-tags','modernconly','-buildvcs=false','-trimpath','-ldflags=-s -w','-o',str(out),'./cmd/server'];start=time.monotonic()
with(w/'release-build.log').open('w')as f:subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
changed=[p for p,h in inputs.items()if sha(pathlib.Path(p))!=h];assert not changed
record={'source_commit':commit,'remote_main_at_build':remote,'command':cmd,'environment':{k:env[k]for k in ['GOOS','GOARCH','CGO_ENABLED']},'go_version':subprocess.check_output(['go','version'],text=True).strip(),'sha256':sha(out),'bytes':out.stat().st_size,'input_count':len(inputs),'changed_inputs':changed,'seconds':round(time.monotonic()-start,3),'go_build_info':subprocess.check_output(['go','version','-m',str(out)],text=True)};(w/'release-build.json').write_text(json.dumps(record,indent=2)+'\n');print({k:record[k]for k in ['source_commit','sha256','bytes','input_count','changed_inputs','seconds']})
