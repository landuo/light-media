"""Explicitly opt-in read-only execution of source AST against synthetic fakes."""
import gzip,json,os,pathlib,subprocess,base64
p=pathlib.Path(__file__).parent
target=os.environ['PLUGIN_REFERENCE_SSH_TARGET'];container=os.environ.get('PLUGIN_REFERENCE_CONTAINER','moviepilot')
if not container.replace('-','').replace('_','').isalnum():raise ValueError('container')
r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8',target,f'docker exec -i -e PLUGIN_ORACLE_TAG=owned-lark-oracle {container} python3 -'],input=(p/'oracle.py').read_bytes().replace(b'__ADDITIONAL_BASE64__',base64.b64encode((p/'additional_algorithm_cases.py').read_bytes())),stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=70)
if r.returncode:raise SystemExit(r.stderr.decode())
lines=r.stdout.splitlines();counts={}
for line in lines:
 item=json.loads(line);counts[item['kind']]=counts.get(item['kind'],0)+1
with gzip.GzipFile(filename=str(p.parent/'testdata/source-oracle.jsonl.gz'),mode='wb',mtime=0) as f:f.write(r.stdout)
print(json.dumps({'count':len(lines),'kinds':counts}))
