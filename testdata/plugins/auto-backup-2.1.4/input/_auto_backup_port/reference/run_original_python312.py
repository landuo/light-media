import subprocess,shlex,json,pathlib,uuid,tarfile,io,hashlib,datetime
R=pathlib.Path(__file__).parent

def ssh(argv,data=None):
 p=subprocess.run(['ssh','-o','BatchMode=yes','admin@192.168.8.129',shlex.join(argv)],input=data,capture_output=True)
 if p.returncode:raise RuntimeError(p.stderr.decode()[:5000])
 return p.stdout
image=ssh(['docker','inspect','--format','{{.Image}}','moviepilot']).decode().strip()
if not image.startswith('sha256:'):raise SystemExit('image')
token=str(uuid.uuid4());remote=ssh(['mktemp','-d','/tmp/light-media-autobackup-oracle.XXXXXXXX']).decode().strip();name='lm-autobackup-oracle-'+token[:8]
if not remote.startswith('/tmp/light-media-autobackup-oracle.'):raise SystemExit('remote')
b=io.BytesIO()
with tarfile.open(fileobj=b,mode='w:gz')as t:
 for f in ('installed-plugin.py','generate_oracle.py','generate_webdav_oracle.py','generate_archive_oracle.py','generate_system_oracle.py','app-helper-system.py','webdav-client.py','webdav-connection.py','webdav-urn.py','webdav-exceptions.py'):t.add(R/f,arcname=f)
ssh(['tar','-xzf','-','-C',remote],b.getvalue())
cmd=['docker','create','--name',name,'--network','none','--read-only','--user','1000:1001','--cap-drop','ALL','--security-opt','no-new-privileges','--memory','128m','--memory-swap','128m','--pids-limit','64','--tmpfs','/tmp:rw,nosuid,nodev,size=32m','--env','AUTOBACKUP_ORACLE_OUTPUT=/tmp/oracle','--mount',f'type=bind,src={remote},dst=/source,readonly','--entrypoint','/opt/venv/bin/python3',image,'-c',"import runpy,json,pathlib; runpy.run_path('/source/generate_oracle.py',run_name='__main__'); runpy.run_path('/source/generate_webdav_oracle.py',run_name='__main__'); runpy.run_path('/source/generate_archive_oracle.py',run_name='__main__'); runpy.run_path('/source/generate_system_oracle.py',run_name='__main__'); print('ORACLE_OUTPUT='+json.dumps({p.name:p.read_text() for p in pathlib.Path('/tmp/oracle').glob('*.json')}))"]
created=ssh(cmd).decode().strip();result={}
try:
 output=ssh(['docker','start','-a',name]);d=json.loads(ssh(['docker','inspect',name]))[0]
 if d['State']['ExitCode']!=0 or d['State']['OOMKilled']:raise RuntimeError('oracle failed')
 outputs=json.loads(next(line.removeprefix('ORACLE_OUTPUT=') for line in output.decode().splitlines() if line.startswith('ORACLE_OUTPUT=')))
 for f in ('business-oracle.json','form.json','metadata.json','webdav-oracle.json','archive-oracle.json','system-oracle.json'):
  data=outputs[f].encode();(R/f).write_bytes(data);result[f]=hashlib.sha256(data).hexdigest()
 (R/'python312-isolation.json').write_text(json.dumps(dict(id=token,recorded_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),image=image,container=name,container_id=created,source_sha256=hashlib.sha256((R/'installed-plugin.py').read_bytes()).hexdigest(),output_sha256=result,exit_code=d['State']['ExitCode'],oom_killed=d['State']['OOMKilled'],network=d['HostConfig']['NetworkMode'],read_only=d['HostConfig']['ReadonlyRootfs'],user=d['Config']['User'],source_mount_readonly=not d['Mounts'][0]['RW'],no_original_config_or_data_mounted=True,original_service_not_executed=True),indent=2)+'\n')
 print(json.dumps(dict(runtime=json.loads(outputs['business-oracle.json'])['runtime'],cases=len(json.loads(outputs['business-oracle.json'])['cases']))))
finally:
 ssh(['docker','rm','-f',name])
 # Fixed names from the owned tar only, then remove the owned mktemp directory.
 ssh(['rm',*[remote+'/'+f for f in ('installed-plugin.py','generate_oracle.py','generate_webdav_oracle.py','generate_archive_oracle.py','generate_system_oracle.py','app-helper-system.py','webdav-client.py','webdav-connection.py','webdav-urn.py','webdav-exceptions.py')]]);ssh(['rmdir',remote])
print(json.dumps(dict(completed=True,container_removed=True,owned_stage_removed=True,outputs=result)))
