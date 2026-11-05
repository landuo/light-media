#!/usr/bin/env python3
"""Read-only deployed response-decoder oracle; synthetic bytes, no network/files writes."""
import json,pathlib,subprocess
script=r'''
import base64,gzip,hashlib,io,json,pathlib,zlib
import requests,urllib3,brotli,zstandard
plain='owned compressed 汉字'.encode()+bytes([0])
raw=zlib.compressobj(wbits=-zlib.MAX_WBITS); raw_deflate=raw.compress(plain)+raw.flush()
payloads=[('gzip','gzip',gzip.compress(plain)),('deflate','deflate',zlib.compress(plain)),('raw-deflate','deflate',raw_deflate),('br','br',brotli.compress(plain)),('zstd','zstd',zstandard.ZstdCompressor().compress(plain)),('gzip-br','gzip, br',brotli.compress(gzip.compress(plain))),('broken-gzip','gzip',b'not gzip')]
cases=[]
for name,encoding,body in payloads:
    response=requests.Response();response.status_code=200
    response.raw=urllib3.response.HTTPResponse(body=io.BytesIO(body),headers={'Content-Encoding':encoding},preload_content=False)
    try: value=base64.b64encode(response.content).decode();error=None
    except Exception as e:value=None;error=type(e).__name__
    finally:response.close()
    cases.append({'name':name,'encoding':encoding,'wire_b64':base64.b64encode(body).decode(),'content_b64':value,'exception':error})
print(json.dumps({'requests_version':requests.__version__,'urllib3_version':urllib3.__version__,'requests_models_sha256':hashlib.sha256(pathlib.Path(requests.models.__file__).read_bytes()).hexdigest(),'urllib3_response_sha256':hashlib.sha256(pathlib.Path(urllib3.response.__file__).read_bytes()).hexdigest(),'classification':'actual deployed vendor Response.content on owned in-memory byte streams; no HTTP request or service mutation','cases':cases}))
'''
container='d1fd69248ee9fca7ed18b34ac7c121d98f982b8979fce065eeb50968e7c27bba'
r=subprocess.run(['ssh','admin@192.168.8.129','docker','exec','-i',container,'python','-B','-'],input=script,text=True,capture_output=True,check=True)
d=json.loads(r.stdout)
pathlib.Path(__file__).with_name('vendor-content-oracle.json').write_text(json.dumps(d,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'cases':len(d['cases']),'requests_version':d['requests_version'],'urllib3_version':d['urllib3_version']}))
