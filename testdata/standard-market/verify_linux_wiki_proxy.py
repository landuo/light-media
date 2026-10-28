"""Disposable real-host Wiki failure harness; Python is test tooling only.

Configure only the isolated process's existing proxy control, serve failing
CONNECTs, wait for browser actions, then clear the test proxy. No TLS bypass,
public credential, old service or media file is used. The browser state, proxy
state and completion flag all belong to this one disposable acceptance run.
"""
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import sys
import threading
import time
import urllib.request

state = json.loads(Path(sys.argv[1]).read_text())
output = Path(sys.argv[2])
done = Path(str(output) + ".done")
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path, data=None, method="GET"):
    req = urllib.request.Request(state["origin"] + path, method=method,
        headers={"Content-Type": "application/json"},
        data=json.dumps(data).encode() if data is not None else None)
    with opener.open(req, timeout=40) as response:
        return json.loads(response.read())["data"]


request("/api/auth/login", {"username": "plugin-acceptance", "password": "owned-browser-test-password"}, "POST")
assert not request("/api/settings")["secret_state"]["proxy_host"], "isolated harness requires no existing proxy"
observed = {"kind": "real Linux cmd/server with controlled failing CONNECT proxy", "connect_hosts": []}
lock = threading.Lock()


class Proxy(BaseHTTPRequestHandler):
    def do_CONNECT(self):
        with lock:
            observed["connect_hosts"].append(self.path)
            output.write_text(json.dumps(observed))
        self.send_error(502, "owned synthetic proxy unavailable")

    def do_GET(self):
        self.send_error(502, "owned synthetic proxy unavailable")

    def log_message(self, *args):
        pass


proxy = ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
threading.Thread(target=proxy.serve_forever, daemon=True).start()
observed["proxy_endpoint"] = "http://127.0.0.1:" + str(proxy.server_port)
try:
    settings = request("/api/settings")
    settings.pop("secret_state", None)
    settings["proxy_host"] = observed["proxy_endpoint"]
    saved = request("/api/settings", settings, "PUT")
    assert saved["secret_state"]["proxy_host"] is True
    observed["configured"] = True
    output.write_text(json.dumps(observed))
    deadline = time.monotonic() + 600
    while time.monotonic() < deadline and not done.exists():
        time.sleep(0.2)
    assert done.exists(), "browser did not finish proxy failure verification"
finally:
    settings = request("/api/settings")
    settings.pop("secret_state", None)
    settings["clear_proxy_host"] = True
    saved = request("/api/settings", settings, "PUT")
    observed["cleared"] = saved["secret_state"]["proxy_host"] is False
    output.write_text(json.dumps(observed))
    proxy.shutdown()
    proxy.server_close()
assert observed["cleared"]
assert observed["connect_hosts"] and set(observed["connect_hosts"]) == {"raw.githubusercontent.com:443"}
print(json.dumps(observed))
