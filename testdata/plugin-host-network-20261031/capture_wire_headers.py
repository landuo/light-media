#!/usr/bin/env python3
import json,pathlib,subprocess
base=pathlib.Path(__file__).parent
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show','8a83efeacd3e26b82be495e17d50146e31c96a60:app/utils/http.py']).decode()
script='source='+repr(raw)+'\n'+r'''
import ast,json,http.server,threading,types,typing,requests,hashlib
cls=next(n for n in ast.parse(source).body if isinstance(n,ast.ClassDef) and n.name=='RequestUtils')
cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ['__init__','request','get_res']]
ns={'requests':requests,'settings':types.SimpleNamespace(USER_AGENT='owned-UA'),'get_caller':lambda:None,'cookie_parse':lambda v:v,'logger':types.SimpleNamespace(debug=lambda *a,**k:None),'_REQUESTS_RETRY_IDEMPOTENT_METHODS':('GET','HEAD','OPTIONS'),'Response':requests.Response,'Session':requests.Session,'Union':typing.Union,'Optional':typing.Optional,'Any':typing.Any}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'fixed:http.py','exec'),ns)
rows=[]
class Peer(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        rows.append(dict(self.headers));self.send_response(200);self.send_header('Content-Length','0');self.end_headers()
    def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start()
try:
    for headers in [None,{'Authorization':'Bearer owned-header-sentinel','User-Agent':'owned-GitHub-UA'}]:
        response=ns['RequestUtils'](headers=headers,timeout=15).get_res('http://127.0.0.1:'+str(peer.server_port));assert response is not None;response.close()
finally:peer.shutdown();peer.server_close()
for row in rows:row.pop('Host',None)
print(json.dumps({'source_sha256':hashlib.sha256(source.encode()).hexdigest(),'classification':'fixed AST and actual vendor HTTP in disposable network-none image','requests_version':requests.__version__,'ordinary':rows[0],'github_headers':rows[1]}))
'''
(base/'wire-header-source-driver.py').write_text(script)
image='33354456e8e19c65193b9ca471e45450a620c541759cc42e76cf00a03b025881'
r=subprocess.run(['ssh','admin@192.168.8.129','docker','run','--rm','-i','--network','none','--entrypoint','python',image,'-B','-'],input=script,text=True,capture_output=True)
(base/'wire-headers-source-stderr.log').write_text(r.stderr)
if r.returncode:print(r.stderr);raise SystemExit(r.returncode)
d=json.loads(r.stdout);(base/'wire-headers-oracle.json').write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d))
