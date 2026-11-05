#!/usr/bin/env python3
"""Actual daemon-produced owned backup -> real installed SDK file worker.

Never reads production downloader directories. Requires exact hashes of the
two externally prepared no-tracker test materials, and does not delete them.
"""
import argparse
import hashlib
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request

p = argparse.ArgumentParser()
for name in ["server", "worker", "directory", "backup", "hash", "torrent-sha", "resume-sha"]:
    p.add_argument("--" + name, required=True)
a = p.parse_args()
root = Path(a.directory).resolve()
root.mkdir(parents=True, exist_ok=False)
backup = Path(a.backup).resolve()
assert backup.name == "backup-export" and backup.parent.name.startswith(".light-media-plugin-downloader-20261031-")
expected = {".torrent": a.torrent_sha, ".fastresume": a.resume_sha}
for suffix, sha in expected.items():
    assert hashlib.sha256((backup / (a.hash + suffix)).read_bytes()).hexdigest() == sha
payload = Path(a.worker).read_bytes()
manifest = json.loads(subprocess.check_output([a.worker, "--manifest"]))
assert manifest["plugin_id"] == "OwnedLocalFiles"
index = {"schema_version": 1, "plugin_id": "OwnedLocalFiles", "current_version": manifest["version"], "releases": [{"version": manifest["version"], "manifest": manifest, "artifacts": [{"platform": "linux-amd64", "format": "executable", "sha256": hashlib.sha256(payload).hexdigest(), "executable_sha256": hashlib.sha256(payload).hexdigest(), "size": len(payload), "host_contract": "light-media.sdk-resources/v1"}]}]}
class Repo(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps(index).encode() if self.path == "/index" else payload if self.path == "/worker" else None
        if body is None:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass
repo = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Repo)
url = "http://127.0.0.1:" + str(repo.server_port)
index["releases"][0]["artifacts"][0]["url"] = url + "/worker"
threading.Thread(target=repo.serve_forever, daemon=True).start()
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
origin = "http://127.0.0.1:" + str(port)
config = {"username": "owned-backup", "password": "owned-public-backup-test-password", "settings": {"plugin_markets": [url + "/index"]}}
subprocess.run([a.server, "--data-dir", str(root / "data"), "--bootstrap-stdin"], input=json.dumps(config).encode(), check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(method, path, body=None):
    value = json.dumps(body).encode() if body is not None else None
    with client.open(urllib.request.Request(origin + path, value, {"Content-Type": "application/json"}, method=method), timeout=20) as reply:
        return json.loads(reply.read())
log = (root / "server.log").open("wb")
process = subprocess.Popen([a.server, "--data-dir", str(root / "data"), "--listen", "127.0.0.1:" + str(port)], stdout=log, stderr=log)
try:
    for _ in range(300):
        try:
            request("GET", "/api/auth/status")
            break
        except OSError:
            assert process.poll() is None
            time.sleep(.05)
    request("POST", "/api/auth/login", {"username": config["username"], "password": config["password"]})
    assert request("POST", "/api/plugins/install", {"plugin_id": "OwnedLocalFiles", "repo_url": url + "/index"})["data"]["success"]
    assert request("PUT", "/api/plugins/OwnedLocalFiles", {"backup_path": str(backup), "hash": a.hash, "run_once": True})["data"]["success"]
    result = request("GET", "/api/v1/plugin/OwnedLocalFiles/snapshot")
    observed = result["observation"]
    assert not observed.get("error") and observed["torrent_sha"] == a.torrent_sha and observed["resume_sha"] == a.resume_sha
    assert hashlib.sha256(Path(observed["temp_path"]).read_bytes()).hexdigest() == a.torrent_sha
    assert request("DELETE", "/api/v1/plugin/OwnedLocalFiles/cleanup")["removed"]
    assert request("DELETE", "/api/plugins/OwnedLocalFiles")["data"]["success"]
    for suffix, sha in expected.items():
        assert hashlib.sha256((backup / (a.hash + suffix)).read_bytes()).hexdigest() == sha
    record = {"status": "passed", "classification": "actual Linux cmd/server -> installed Go SDK worker -> two real isolated qBittorrent BT_backup files; no production data", "observation": observed, "backup_preserved": True, "source": str(backup), "driver_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "server_sha256": hashlib.sha256(Path(a.server).read_bytes()).hexdigest(), "worker_sha256": hashlib.sha256(payload).hexdigest()}
    (root / "result.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps({"status": "passed", "pid": observed["pid"], "backup_preserved": True}))
finally:
    process.send_signal(signal.SIGTERM)
    process.wait(timeout=20)
    repo.shutdown()
    log.close()
