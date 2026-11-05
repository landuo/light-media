#!/usr/bin/env python3
"""Read-only pinned image in a disposable network-none container; owned HTTP."""
import json,pathlib,subprocess
base=pathlib.Path(__file__).parent
cases=json.loads((base/'vendor-edges-oracle.json').read_text())['cases']
script='import json\ncases=json.loads('+repr(json.dumps(cases))+')\n'+r'''
import base64,hashlib,http.server,pathlib,threading,requests,urllib3
class Peer(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        case=next(c for c in cases if self.path.split('?')[0]=='/'+c['name']);body=base64.b64decode(case['wire_b64'])
        self.send_response(200);self.send_header('Content-Encoding',case['encoding'].strip() if '?canonical=1' in self.path else case['encoding']);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
    def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start()
rows=[]
try:
    for case in cases:
        try:
            with requests.get('http://127.0.0.1:'+str(peer.server_port)+'/'+case['name'],timeout=2) as response:
                content=base64.b64encode(response.content).decode();error=None;header=response.headers['Content-Encoding']
        except requests.exceptions.RequestException as exc:content=None;error=type(exc).__name__;header=None
        rows.append(dict(case,name=case['name'],content_b64=content,exception=error,parsed_header=header))
    with requests.get('http://127.0.0.1:'+str(peer.server_port)+'/single-whitespace?canonical=1',timeout=2) as response:
        canonical=dict(content_b64=base64.b64encode(response.content).decode(),parsed_header=response.headers['Content-Encoding'])
finally:peer.shutdown();peer.server_close()
print(json.dumps(dict(requests_version=requests.__version__,urllib3_version=urllib3.__version__,requests_models_sha256=hashlib.sha256(pathlib.Path(requests.models.__file__).read_bytes()).hexdigest(),urllib3_response_sha256=hashlib.sha256(pathlib.Path(urllib3.response.__file__).read_bytes()).hexdigest(),classification='actual requests GET and content decoding against own loopback HTTP in disposable network-none image; no old service execution',cases=rows,go_header_ows_adaptation=canonical)))
'''
image='33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
result=subprocess.run(['ssh','admin@192.168.8.129','docker','run','--rm','-i','--network','none','--entrypoint','python',image,'-B','-'],input=script,text=True,capture_output=True,check=True)
d=json.loads(result.stdout);old=json.loads((base/'vendor-content-oracle.json').read_text())
for key in ['requests_models_sha256','urllib3_response_sha256']:assert d[key]==old[key],key
d['docker_image_sha256']=image
(base/'vendor-http-oracle.json').write_text(json.dumps(d,indent=2)+'\n')
print(json.dumps({'cases':len(d['cases']),'whitespace':d['cases'][0]}))
