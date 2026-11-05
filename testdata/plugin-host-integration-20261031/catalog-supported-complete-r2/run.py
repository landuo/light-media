"""Read fixed resource binaries in an isolated, read-only, networkless image.
No production mounts, source imports, DB reads, credentials or full raw catalog output.
Only ordinary category/domain fields for existing-host supported schemas leave RAM.
"""
import base64,hashlib,json,pathlib,shlex,subprocess
root=pathlib.Path(__file__).resolve().parent
repo=root.parents[2]
source='d1fd69248ee9fca7ed18b34ac7c121d98f982b8979fce065eeb50968e7c27bba'
image='sha256:33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
name='light-media-owned-plugin-category-supported-complete-r2-20261031'
def docker(*args,data=None,check=True):
 return subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',shlex.join(['docker',*args])],input=data,capture_output=True,check=check,timeout=60)
def snapshot():
 return docker('inspect','--format','{{.Id}} {{.Image}} {{.State.Pid}} {{.State.StartedAt}} {{.RestartCount}}',source).stdout.decode().strip()
def save(name,obj):(root/name).write_text(json.dumps(obj,indent=2,ensure_ascii=False)+'\n')
before=snapshot();assert before.startswith(source+' '+image)
items=json.loads((repo/'testdata/plugins/site-indexer-context/original-selected-catalog.json').read_text())['rows']
identities=[{'id':r['source_site_id'],'domain':r['source_db_domain']} for r in items]
identity_sha=hashlib.sha256(json.dumps(identities,ensure_ascii=False,separators=(',',':')).encode()).hexdigest()
assert identity_sha=='c843eb520fbfdd88589fa74c58c45063e657213c81edf2c696cee777707358a7'
cipher=(repo/'work/plugin-host-business-20261031/resource-candidates/user.sites.v2.bin').read_bytes();catalog_sha=hashlib.sha256(cipher).hexdigest();assert catalog_sha=='f6099dcd4f55f99646d53dbe4cec2c3f01ad4ec95929052a4e348a4ad90205c0'
code=(root/'probe.py').read_bytes()
private={'source_container':source,'helper_sha256':'8a14c1668778305b9b40bdd5456e6a95afe6ef101b11db02750fbcd4e7ae0b64','catalog_sha256':catalog_sha,'catalog_b64':base64.b64encode(cipher).decode(),'sites':identities}
bootstrap='import sys;exec(compile(sys.stdin.buffer.readline().decode(),"owned-category-probe","exec"))'
cid=None
try:
 cid=docker('create','-i','--name',name,'--network','none','--read-only','--no-healthcheck','--cpus','1','--memory','512m','--pids-limit','64','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--tmpfs','/tmp:rw,noexec,nosuid,size=32m,uid=1000,gid=1001','--tmpfs','/config:rw,noexec,nosuid,size=8m,uid=1000,gid=1001','--entrypoint','/usr/local/bin/python',image,'-I','-B','-S','-c',bootstrap).stdout.decode().strip()
 info=json.loads(docker('inspect',cid).stdout)[0];h=info['HostConfig'];assert info['Image']==image and h['NetworkMode']=='none' and h['ReadonlyRootfs'] and info['Mounts']==[] and h['Memory']==512*1024**2
 save('isolation.json',{'id':cid,'image':image,'network':h['NetworkMode'],'read_only':h['ReadonlyRootfs'],'mounts':info['Mounts'],'memory':h['Memory'],'cpu':h['NanoCpus'],'caps_dropped':h['CapDrop'],'no_new_privileges':h['SecurityOpt']})
 run=docker('start','-a','-i',cid,data=('exec('+repr(code.decode())+')\n'+json.dumps(private)+'\n').encode(),check=False)
 result=json.loads(run.stdout)
 if not result.get('success'):
  save('failed.json',result);raise RuntimeError('isolated source projection failed')
 assert run.returncode==0 and not run.stderr
 assert result['catalog_sha256']==catalog_sha and len(result['rows'])>17 and result['real_db_or_config_loaded'] is False and result['real_network_requested'] is False and result['real_app_module_loaded'] is False
 for row in result['rows']:
  assert set(row)<=set(['source_site_id','source_db_domain','found','schema','domain','ext_domains','category','entry_sha256'])
 result['source_binding_note']='Resource bytes match current read-only NAS and pinned Resources commits; MoviePilot Dockerfile did not pin resource download. The primary fixed commit alone cannot select this resource revision.'
 save('projection.json',result)
 print(json.dumps({'status':'passed','rows':len(result['rows']),'with_category':sum(bool(x.get('category')) for x in result['rows']),'projection_sha256':result['allowed_projection_sha256']}))
finally:
 if cid:docker('rm','-f','-v',cid)
 after=snapshot();absent=not docker('ps','-a','--filter','name=^/'+name+'$','--format','{{.ID}}').stdout.strip();save('cleanup.json',{'source_before':before,'source_after':after,'source_unchanged':before==after,'owned_container_absent':absent,'driver_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),'probe_sha256':hashlib.sha256(code).hexdigest()});assert before==after and absent
