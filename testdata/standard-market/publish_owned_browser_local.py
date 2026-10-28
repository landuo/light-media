"""Atomically publish actual owned SDK bytes inside this process test only."""
import hashlib,json,os,pathlib,subprocess,sys
root=pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
state=json.loads((root/'browser-local-dev-state.json').read_text())
local=pathlib.Path(state['local_repository_root'])
assert local==pathlib.Path(state['data_dir'])/'owned-local-publications'
version=sys.argv[1];assert version in ['1.0','2.0']
corrupt=len(sys.argv)>2 and sys.argv[2]=='corrupt'
worker=root/('standard-v1-linux' if version=='1.0' else 'standard-v2-linux')
manifest=json.loads(subprocess.check_output([str(worker),'--manifest']))
assert manifest['plugin_id']=='OwnedStandardFixture' and manifest['version']==version
payload=worker.read_bytes();digest=hashlib.sha256(payload).hexdigest()
name='browser-worker-'+version+('-invalid' if corrupt else '')
pending=local/(name+'.pending')
if corrupt:pending.write_bytes(b'owned invalid browser publication')
else:os.link(worker,pending)
os.replace(pending,local/name)
index=dict(schema_version=1,plugin_id=manifest['plugin_id'],current_version=version,releases=[dict(version=version,manifest=manifest,artifacts=[dict(platform='linux-amd64',url=name,format='executable',sha256=digest,executable_sha256=digest,size=len(payload),host_contract='light-media.sdk-base/v1')])])
publication=local/'go-plugins.v1.json.pending';publication.write_text(json.dumps(index));os.replace(publication,local/'go-plugins.v1.json')
print(json.dumps(dict(kind='actual owned Go SDK atomic local publication',version=version,corrupt=corrupt,root=str(local),sha256=digest)))
