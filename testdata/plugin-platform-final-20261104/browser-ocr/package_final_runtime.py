#!/usr/bin/env python3
"""Compile unchanged current Go/Embed inputs and package over proven product runtime.
No source clone, dependency copy, or production deployment occurs.
"""
import datetime,hashlib,json,os,pathlib,re,subprocess,sys,tarfile,tempfile
revision=sys.argv[1] if len(sys.argv)>1 else 'r7'
assert re.fullmatch(r'r[1-9][0-9]*',revision),revision
root=pathlib.Path(__file__).resolve().parents[3]
out=root/'testdata/plugin-platform-final-20261104/browser-ocr'
work=root/'work/plugin-platform-final-20261104/browser-ocr'
binary=work/('light-media-final-'+revision)
base='sha256:b7016b2bd460beade564f5830c5fa82a9213ad8a8566e7522e4c0cad97d14569'
actual=subprocess.check_output(['docker','-H','ssh://admin@192.168.8.129','image','inspect','--format','{{.Id}}','light-media-plugin-browser-acceptance-final:20261104'],text=True).strip()
assert actual==base,actual
env=os.environ|{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}
def inputs():
 stream=subprocess.check_output(['go','list','-tags','modernconly','-deps','-json','./cmd/server'],env=env,cwd=root).decode()
 decoder=json.JSONDecoder();records={};position=0
 while position<len(stream):
  while position<len(stream)and stream[position].isspace():position+=1
  if position==len(stream):break
  package,position=decoder.raw_decode(stream,position);directory=pathlib.Path(package['Dir'])
  try:directory.relative_to(root)
  except ValueError:continue
  for field in ['GoFiles','CgoFiles','EmbedFiles','SFiles','HFiles']:
   for name in package.get(field,[]):
    p=directory/name
    if p.is_file():records[str(p.relative_to(root))]=hashlib.sha256(p.read_bytes()).hexdigest()
 for name in ['go.mod','go.sum','Dockerfile','scripts/install-browser-runtime.sh','web/package-lock.json','internal/plugins/concurrent_session.go']:
  records[name]=hashlib.sha256((root/name).read_bytes()).hexdigest()
 return records
began=datetime.datetime.now(datetime.timezone.utc).isoformat();before=inputs()
command=['go','build','-tags','modernconly','-buildvcs=false','-trimpath','-ldflags=-s -w','-o',str(binary.relative_to(root)),'./cmd/server']
subprocess.run(command,env=env,cwd=root,check=True)
after=inputs();differences=[name for name in sorted(set(before)|set(after)) if before.get(name)!=after.get(name)]
if differences:
 (out/('product-'+revision+'-invalid-build-inputs.json')).write_text(json.dumps({'classification':'invalid build with input changes; no image packaged or acceptance claimed','before':before,'after':after,'differences':differences},indent=2)+'\n')
 raise AssertionError(('compile inputs changed during build',differences))
binding={'classification':'new '+revision+' image layers exact R3 production runtime with an unchanged before/after compiled current-source Go executable; not a renamed predecessor digest','source_backend':'8a83efeacd3e26b82be495e17d50146e31c96a60','runtime_image':base,'server_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'go_version':subprocess.check_output(['go','version'],text=True).strip(),'go_build_command':command,'environment':{'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'},'compile_input_files':before,'compile_input_files_after':after,'input_differences':differences,'build_began_utc':began,'build_ended_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'packaging_dockerfile':(work/'FinalRuntime.Dockerfile').read_text(),'base_image_go_version':'go1.26.10','note':'Default repository Go cache and existing worktree-built frontend are reused. Actual new image/server/frontend is bound separately; historical R3/R6 content is not relabelled.'}
(out/('product-'+revision+'-build-inputs.json')).write_text(json.dumps(binding,indent=2)+'\n')
with tempfile.TemporaryFile(dir=work)as context:
 with tarfile.open(fileobj=context,mode='w:gz',format=tarfile.GNU_FORMAT)as archive:
  archive.add(work/'FinalRuntime.Dockerfile',arcname='Dockerfile');archive.add(binary,arcname='light-media')
 context.seek(0)
 with(out/('product-image-build-'+revision+'.log')).open('wb')as log:
  result=subprocess.run(['docker','-H','ssh://admin@192.168.8.129','build','--progress=plain','--tag','light-media-plugin-browser-acceptance-runtime-'+revision+':20261104','-'],stdin=context,stdout=log,stderr=log)
print(json.dumps({'build_exit':result.returncode,'compile_input_files':len(before),'server_sha256':binding['server_sha256']}),flush=True)
raise SystemExit(result.returncode)
