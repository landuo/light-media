"""Owned temporary LAN relay; NAS sshd does not permit TCP forwarding."""
import argparse
import http.client
import http.server
import json
import os
import pathlib

p = argparse.ArgumentParser()
p.add_argument('--upstream-port', type=int, required=True)
p.add_argument('--state', required=True)
a = p.parse_args()

class Relay(http.server.BaseHTTPRequestHandler):
    def do_GET(self): self.forward()
    def do_POST(self): self.forward()
    def do_PUT(self): self.forward()
    def do_DELETE(self): self.forward()
    def do_OPTIONS(self): self.forward()
    def forward(self):
        conn = http.client.HTTPConnection('127.0.0.1', a.upstream_port, timeout=120)
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        headers = {k: v for k, v in self.headers.items() if k.lower() not in ('connection', 'transfer-encoding')}
        conn.request(self.command, self.path, body, headers)
        response = conn.getresponse()
        self.send_response(response.status)
        for k, v in response.getheaders():
            if k.lower() not in ('connection', 'transfer-encoding'):
                self.send_header(k, v)
        self.end_headers()
        try:
            while True:
                content = response.read1(64 * 1024)
                if not content: break
                self.wfile.write(content)
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            pass
        finally:
            conn.close()
    def log_message(self, *_): pass

server = http.server.ThreadingHTTPServer(('192.168.8.129', 0), Relay)
state = {'origin': 'http://192.168.8.129:' + str(server.server_port), 'pid': os.getpid()}
pathlib.Path(a.state).write_text(json.dumps(state) + '\n')
print(json.dumps(state), flush=True)
server.serve_forever()
