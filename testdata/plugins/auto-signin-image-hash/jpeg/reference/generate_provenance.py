"""Run the read-only binary/runtime probe via an existing authorized SSH session."""
import os,pathlib,shlex,subprocess,json
root=pathlib.Path(__file__).parent
command=shlex.join(['docker','exec','-i',os.environ.get('JPEG_REFERENCE_CONTAINER','moviepilot'),'python3','-'])
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',os.environ['JPEG_REFERENCE_SSH_TARGET'],command],input=(root/'generate_provenance_runtime.py').read_text(),text=True,capture_output=True,check=True)
# Parse before writing; no credential/configuration queries are made by the probe.
data=json.loads(r.stdout)
(root/'installed-binary-provenance.json').write_text(json.dumps(data,indent=2)+'\n')
