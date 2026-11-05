#!/usr/bin/env python3
"""Read-only deployed decoder edge oracle; only owned in-memory streams."""
import json,pathlib,subprocess
script=r'''
import base64,gzip,hashlib,io,json,pathlib,zlib
import requests,urllib3,brotli
plain=b'owned edge';gz=gzip.compress(plain,mtime=0);df=zlib.compress(plain);br=brotli.compress(plain)
items=[('single-whitespace',' gzip ',gz),('unknown-single','unknown',gz),('unknown-gzip','unknown, gzip',gzip.compress(df,mtime=0)),('gzip-unknown','gzip, unknown',zlib.compress(gz)),('unknown-broken','gzip, unknown',gz),('too-many','gzip,gzip,gzip,gzip,gzip,gzip',gz),('gzip-truncated','gzip',gz[:-4]),('gzip-header-truncated','gzip',gz[:5]),('gzip-trailing','gzip',gz+b'not another gzip'),('gzip-concat','gzip',gz+gz),('gzip-checksum','gzip',gz[:-8]+b'bad!'+gz[-4:]),('deflate-truncated','deflate',df[:-3]),('br-truncated','br',br[:-1]),('empty-gzip','gzip',b''),('empty-br','br',b'')]
results=[]
for name,encoding,wire in items:
    response=requests.Response();response.status_code=200;response.raw=urllib3.response.HTTPResponse(body=io.BytesIO(wire),headers={'Content-Encoding':encoding},preload_content=False)
    try:content=base64.b64encode(response.content).decode();error=None
    except Exception as exc:content=None;error=type(exc).__name__
    finally:response.close()
    results.append(dict(name=name,encoding=encoding,wire_b64=base64.b64encode(wire).decode(),content_b64=content,exception=error))

print(json.dumps(dict(requests_version=requests.__version__,urllib3_version=urllib3.__version__,requests_models_sha256=hashlib.sha256(pathlib.Path(requests.models.__file__).read_bytes()).hexdigest(),urllib3_response_sha256=hashlib.sha256(pathlib.Path(urllib3.response.__file__).read_bytes()).hexdigest(),classification="actual deployed vendor decoder on owned in-memory bytes; no network or mutation",cases=results)))
'''
container='d1fd69248ee9fca7ed18b34ac7c121d98f982b8979fce065eeb50968e7c27bba'
r=subprocess.run(['ssh','admin@192.168.8.129','docker','exec','-i',container,'python','-B','-'],input=script,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
original=json.loads(pathlib.Path(__file__).with_name('vendor-content-oracle.json').read_text())
for key in ['requests_models_sha256','urllib3_response_sha256']:assert d[key]==original[key]
pathlib.Path(__file__).with_name('vendor-edges-oracle.json').write_text(json.dumps(d,indent=2)+'\n')
print(json.dumps(d['cases']))
