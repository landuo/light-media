"""Build directly in the shared worktree; hash Go's resolved source/embed closure."""
import gzip,hashlib,json,os,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];WORK=ROOT/'work/plugin-host-downloader-20261031';decoder=json.JSONDecoder()
def inputs():
 paths={ROOT/'go.mod',ROOT/'go.sum'}
 for target in ['linux','darwin']:
  env=dict(os.environ,GOOS=target,GOARCH='amd64' if target=='linux' else 'arm64',CGO_ENABLED='0')
  text=subprocess.check_output(['go','list','-deps','-json','./cmd/server','./cmd/plugin-host-downloader-fixture'],cwd=ROOT,env=env).decode();pos=0
  while pos<len(text):
   while pos<len(text) and text[pos].isspace():pos+=1
   if pos==len(text):break
   package,pos=decoder.raw_decode(text,pos);directory=pathlib.Path(package['Dir'])
   for field in ['GoFiles','CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SwigFiles','SwigCXXFiles','SysoFiles','EmbedFiles']:
    paths.update(directory/name for name in package.get(field,[]))
   if package.get('Module',{}).get('GoMod'):paths.add(pathlib.Path(package['Module']['GoMod']))
 return {str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}
before=inputs();commands=[]
for target,arch,suffix in [('linux','amd64','linux'),('darwin','arm64','mac')]:
 for package,name in [('./cmd/server','light-media-server-'+suffix),('./cmd/plugin-host-downloader-fixture','plugin-host-downloader-fixture'+('-linux' if suffix=='linux' else ''))]:
  command=['go','build','-o',str(WORK/name),package];env=dict(os.environ,GOOS=target,GOARCH=arch,CGO_ENABLED='0');subprocess.run(command,cwd=ROOT,env=env,check=True);commands.append({'GOOS':target,'GOARCH':arch,'CGO_ENABLED':'0','command':command})
 env=dict(os.environ,GOOS='linux',GOARCH='amd64',CGO_ENABLED='0')
subprocess.run(['go','build','-ldflags','-X main.fixtureVersion=2.0','-o',str(WORK/'plugin-host-downloader-fixture-v2-linux'),'./cmd/plugin-host-downloader-fixture'],cwd=ROOT,env=env,check=True)
after=inputs();changed=sorted(p for p in set(before)|set(after) if before.get(p)!=after.get(p));binaries={name:hashlib.sha256((WORK/name).read_bytes()).hexdigest() for name in ['light-media-server-linux','light-media-server-mac','plugin-host-downloader-fixture-linux','plugin-host-downloader-fixture','plugin-host-downloader-fixture-v2-linux']};result={'branch':subprocess.check_output(['git','branch','--show-current'],cwd=ROOT).decode().strip(),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT).decode().strip(),'go_version':subprocess.check_output(['go','version'],cwd=ROOT).decode().strip(),'commands':commands,'resolved_Go_source_and_embed_inputs':before,'changed_inputs':changed,'binaries':binaries}
with gzip.open(WORK/'final-enabled-build-binding.json.gz','wt') as f:json.dump(result,f,indent=2)
print(json.dumps({'changed_inputs':changed,'input_count':len(before),'binaries':binaries}),flush=True);assert not changed
