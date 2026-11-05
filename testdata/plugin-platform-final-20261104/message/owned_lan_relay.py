import http.server,http.client,json
class Relay(http.server.BaseHTTPRequestHandler):
 def do_GET(self):self.forward()
 def do_POST(self):self.forward()
 def do_PUT(self):self.forward()
 def do_DELETE(self):self.forward()
 def do_OPTIONS(self):self.forward()
 def forward(self):
  conn=http.client.HTTPConnection("127.0.0.1",60003,timeout=120)
  body=self.rfile.read(int(self.headers.get("Content-Length",0)))
  headers={k:v for k,v in self.headers.items() if k.lower() not in ["connection","transfer-encoding"]}
  conn.request(self.command,self.path,body,headers)
  response=conn.getresponse();self.send_response(response.status)
  for k,v in response.getheaders():
   if k.lower() not in ["connection","transfer-encoding"]:self.send_header(k,v)
  self.end_headers()
  try:
   while True:
    data=response.read1(64*1024)
    if not data:break
    self.wfile.write(data);self.wfile.flush()
  except (BrokenPipeError,ConnectionResetError,TimeoutError):pass
  finally:conn.close()
 def log_message(self,*a):pass
s=http.server.ThreadingHTTPServer(("192.168.8.129",0),Relay)
print(json.dumps({"origin":"http://192.168.8.129:"+str(s.server_port)}),flush=True)
s.serve_forever()
