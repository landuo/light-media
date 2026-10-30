"""Owned normal GitHub mirror-prefix HTTP origin, never a forwarding proxy.

Only GET /https://raw.githubusercontent.com/Owned/... is served, using supplied
synthetic bytes. There is no TLS, certificate creation/trust, CONNECT tunnel,
DNS rewrite, environmental proxy or external connection.
"""
import hashlib, http.server, os, threading, urllib.parse

FORBIDDEN_ENV = ('SSL_CERT_FILE','SSL_CERT_DIR','REQUESTS_CA_BUNDLE','CURL_CA_BUNDLE',
                 'HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy')
def assert_clean_environment():
    present=[name for name in FORBIDDEN_ENV if name in os.environ]
    if present:raise RuntimeError('This fixture requires an explicit clean environment; forbidden variable names: '+','.join(present))

class OwnedHTTPMirror:
    def __init__(self,response):
        assert_clean_environment()
        self.requests=[];self.rejected=[];owner=self
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self,*unused):pass
            def reject(self,reason):
                owner.rejected.append({'method':self.command,'reason':reason})
                self.send_response(405);self.send_header('Content-Length','0');self.end_headers()
            def do_CONNECT(self):self.reject('no CONNECT support')
            def do_POST(self):self.reject('GET-only static origin')
            def do_PUT(self):self.reject('GET-only static origin')
            def do_GET(self):
                if self.client_address[0]!='127.0.0.1':return self.reject('not loopback')
                if self.headers.get('Authorization') or self.headers.get('Proxy-Authorization') or self.headers.get('Cookie'):
                    return self.reject('unexpected credential header')
                # The official upstream URL is only a path value in the product's
                # existing mirror protocol. This server never connects to it.
                if not self.path.startswith('/https://raw.githubusercontent.com/Owned/'):
                    return self.reject('not an owned fixture route')
                parsed=urllib.parse.urlsplit(self.path[1:])
                if parsed.scheme!='https' or parsed.netloc!='raw.githubusercontent.com' or parsed.fragment:
                    return self.reject('invalid mirror path')
                path=parsed.path+('?' + parsed.query if parsed.query else '')
                row={'host':'raw.githubusercontent.com','method':'GET','path':path}
                owner.requests.append(row)
                try:
                    status,body=response('raw.githubusercontent.com',path)
                    raw=body.encode() if isinstance(body,str) else body
                    if not isinstance(raw,bytes) or not isinstance(status,int):raise ValueError('fixture response type')
                except Exception as error:
                    owner.rejected.append({'method':'GET','reason':'callback '+type(error).__name__})
                    status,raw=500,b'{}'
                row.update(response_status=status,response_size=len(raw),response_sha256=hashlib.sha256(raw).hexdigest())
                self.send_response(status);self.send_header('Content-Type','application/json')
                self.send_header('Content-Length',str(len(raw)));self.send_header('Connection','close');self.end_headers();self.wfile.write(raw)
        self.server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
        self.server.daemon_threads=True
        self.url='http://127.0.0.1:'+str(self.server.server_address[1])+'/'
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True);self.thread.start()
    def close(self):
        self.server.shutdown();self.server.server_close();self.thread.join(timeout=5)
        if self.thread.is_alive():raise RuntimeError('owned mirror did not stop')
