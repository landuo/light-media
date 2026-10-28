"""Real cmd/server Release acceptance with controlled repository HTTP only.

The index is copied from the running Go process harness's verified SDK
artifacts. Expected results come from cache-oracle.json's pinned endpoint AST.
Python serves development test metadata; the application/worker remain Go.
"""
import copy
import http.cookiejar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import sys
import threading
import time
import urllib.parse
import urllib.request

state = json.loads(Path(sys.argv[1]).read_text())
oracle = json.loads(Path(sys.argv[2]).read_text())["releases"]
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path, data=None, method="GET", headers=None, unwrap=True):
    req = urllib.request.Request(state["origin"] + path, method=method,
        headers={"Content-Type": "application/json", **(headers or {})},
        data=json.dumps(data).encode() if data is not None else None)
    with opener.open(req, timeout=40) as response:
        value = json.loads(response.read())
        return value["data"] if unwrap else value


request("/api/auth/login", {"username": "plugin-acceptance", "password": "owned-browser-test-password"}, "POST")
source = state["repository"] + "/index"
with urllib.request.urlopen(source, timeout=10) as response:
    index = next(p for p in json.loads(response.read())["plugins"] if p["plugin_id"] == "OwnedStandardFixture")
assert {r["version"] for r in index["releases"]} == {"1.0", "2.0"}
result = request("/api/plugins/install", {"plugin_id": index["plugin_id"], "repo_url": source, "release_version": "1.0", "force": True}, "POST")
assert result["success"] is True
key = request("/api/settings/api-token/reveal", {}, "POST")["token"]


def worker():
    value = request("/api/v1/plugin/OwnedStandardFixture/echo", headers={"X-API-KEY": key}, unwrap=False)
    return {name: value[name] for name in ["version", "nonce", "init_count", "config", "value"]}


before = worker()
assert before["version"] == "1.0"
no_source = request("/api/plugins/releases/OwnedStandardFixture")
for name, expected in oracle["no_source"].items():
    assert no_source[name] == expected, (name, no_source[name], expected)
local = request("/api/plugins/releases/OwnedStandardFixture?" + urllib.parse.urlencode({"repo_url": "local://OwnedStandardFixture?path=%2Fnot-configured"}))
assert local["release_supported"] is False and local["current_version"] == "1.0" and local["latest_version"] is None and local["items"] == []
promoted, entered, release = threading.Event(), threading.Event(), threading.Event()
lock = threading.Lock()
hits = 0


class Repository(BaseHTTPRequestHandler):
    def do_GET(self):
        global hits
        assert urllib.parse.urlparse(self.path).path == "/index", "metadata query requested an artifact"
        with lock:
            hits += 1
            number = hits
        if number == 4:
            entered.set()
            assert release.wait(15), "forced response did not return before background Release fetch"
        value = copy.deepcopy(index)
        value["current_version"] = "2.0" if promoted.is_set() else "1.0"
        if not promoted.is_set():
            value["releases"] = [r for r in value["releases"] if r["version"] == "1.0"]
        body = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


server = ThreadingHTTPServer(("127.0.0.1", 0), Repository)
threading.Thread(target=server.serve_forever, daemon=True).start()
endpoint = "/api/plugins/releases/OwnedStandardFixture?" + urllib.parse.urlencode({"repo_url": "http://127.0.0.1:" + str(server.server_port) + "/index"})
try:
    initial = request(endpoint)
    assert [r["version"] for r in initial["items"]] == ["1.0"] and hits == 2
    promoted.set()
    forced = request(endpoint + "&force=true")
    for name, expected in oracle["forced_with_existing_cache"].items():
        assert forced[name] == expected, (name, forced[name], expected)
    assert entered.wait(5)
    release.set()
    deadline = time.monotonic() + 5
    while True:
        refreshed = request(endpoint)
        if {r["version"] for r in refreshed["items"]} == {"1.0", "2.0"}:
            break
        assert time.monotonic() < deadline, "actual background fetch did not update its cache"
        time.sleep(0.05)
    assert hits == 4
    assert worker() == before, "Release metadata changed the real Go worker"
    rejected = request("/api/plugins/install", {"plugin_id": index["plugin_id"], "repo_url": source, "release_version": "999.0", "force": True}, "POST")
    assert rejected["success"] is False and worker() == before
finally:
    release.set()
    server.shutdown()
    server.server_close()
print(json.dumps({"kind": "real Linux cmd/server + synthetic metadata repository + actual verified Go SDK artifacts", "source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60", "no_source": {k: no_source[k] for k in oracle["no_source"]}, "local_release_supported": local["release_supported"], "forced_cached_response": {k: forced[k] for k in oracle["forced_with_existing_cache"]}, "background_versions": [r["version"] for r in refreshed["items"]], "repository_requests": hits, "sdk_before_and_after": before, "unknown_selected_release_rejected_without_worker_change": True}, ensure_ascii=False))
