#!/usr/bin/env python3
"""Pinned RequestUtils + exact deployed vendor versions against owned HTTP."""
import ast,hashlib,http.server,json,os,pathlib,subprocess,threading,time,types,typing
import requests,urllib3
commit='8a83efeacd3e26b82be495e17d50146e31c96a60'
raw=subprocess.check_output(['git','-C','/Users/accidia/projects/MoviePilot','show',commit+':app/utils/http.py'])
cls=next(n for n in ast.parse(raw).body if isinstance(n,ast.ClassDef) and n.name=='RequestUtils')
cls.body=[n for n in cls.body if isinstance(n,ast.FunctionDef) and n.name in ['__init__','request','get_res']]
env={'requests':requests,'settings':types.SimpleNamespace(USER_AGENT='owned-UA'),'get_caller':lambda:None,'cookie_parse':lambda v:v,'logger':types.SimpleNamespace(debug=lambda *a,**k:None),'_REQUESTS_RETRY_IDEMPOTENT_METHODS':('GET','HEAD','OPTIONS'),'Response':requests.Response,'Session':requests.Session,'Union':typing.Union,'Optional':typing.Optional,'Any':typing.Any}
exec(compile(ast.fix_missing_locations(ast.Module(body=[cls],type_ignores=[])),'fixed:http.py','exec'),env)
trace=[]
class Peer(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        trace.append(self.path)
        try:
            if self.path=='/header-idle':time.sleep(.8)
            if self.path.startswith('/redirect-'):
                index=int(self.path.rsplit('-',1)[1]);time.sleep(.3)
                if index<2:self.send_response(302);self.send_header('Location','/redirect-'+str(index+1));self.send_header('Content-Length','0');self.end_headers();return
            self.send_response(200);self.send_header('Content-Length','5');self.end_headers()
            for i in range(5):
                self.wfile.write(b'x');self.wfile.flush()
                if self.path=='/progress' and i<4:time.sleep(.3)
                if self.path=='/body-idle' and i==0:time.sleep(.8)
        except (BrokenPipeError,ConnectionResetError):pass
    def log_message(self,*_):pass
peer=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);threading.Thread(target=peer.serve_forever,daemon=True).start()
for name in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy']:os.environ.pop(name,None)
results=[]
try:
    for name in ['progress','header-idle','body-idle','redirect-0']:
        before=len(trace);start=time.monotonic();value=env['RequestUtils'](timeout=.5).get_res('http://127.0.0.1:'+str(peer.server_port)+'/'+name)
        results.append(dict(name=name,timeout_seconds=.5,elapsed_seconds=round(time.monotonic()-start,3),content=value.content.decode() if value is not None else None,requests=trace[before:]))
finally:peer.shutdown();peer.server_close()
assert [v['content'] for v in results]==['xxxxx',None,None,'xxxxx'],results
output=dict(commit=commit,source_sha256=hashlib.sha256(raw).hexdigest(),requests=requests.__version__,urllib3=urllib3.__version__,classification='fixed AST with actual vendor HTTP on owned loopback; scaled 0.5 second scalar timeout, no mock',cases=results)
pathlib.Path(__file__).with_name('timeout-oracle.json').write_text(json.dumps(output,indent=2)+'\n');print(json.dumps(output))
