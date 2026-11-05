#!/usr/bin/env python3
"""Actual plugin host refresh, limited to a freshly owned fnOS test library.

Credentials are one JSON line on stdin, only kept in memory and the temporary
App's encrypted settings. No credentials appear in arguments, reports or logs.
Uses the existing scoped experiment CLI to create/reconcile/clean its library.
"""
import argparse
import hashlib
import http.cookiejar
import http.server
import json
from pathlib import Path
import signal
import socket
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error
import uuid

p = argparse.ArgumentParser()
for key in ["server", "worker", "experiment", "directory"]:
    p.add_argument("--" + key, required=True)
p.add_argument("--diagnostic")
a = p.parse_args()
credentials = json.loads(sys.stdin.readline())
assert credentials["host"] == "https://nas.accidia.top:5667/v"
root = Path(a.directory).resolve()
root.mkdir(parents=True, exist_ok=False)
experiment_id = str(uuid.uuid4())
remote = "/vol2/1000/media/light-media-m0-test-" + experiment_id
movie_name = "owned-" + experiment_id
movie = root / (movie_name + ".mkv")
subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-n", "-f", "lavfi", "-i", "color=c=blue:s=320x240:r=24", "-t", "1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", str(movie)], check=True)
nfo = root / (movie_name + ".nfo")
nfo.write_text('<movie><title>' + movie_name + '</title><year>2024</year><uniqueid type="lightmedia-owned" default="true">' + experiment_id + '</uniqueid></movie>')
ssh = ["ssh", "-o", "BatchMode=yes", "admin@192.168.8.129"]
def remote_python(script):
    return subprocess.run(ssh + ["python3 -"], input=script.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
remote_python("from pathlib import Path\np=Path(" + repr(remote) + ")\nassert p.parent.resolve()==p.parent and not p.exists()\np.mkdir(mode=0o755)\n(p/'movie').mkdir()\n")
file_hashes = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in [movie, nfo]}
subprocess.run(["scp", "-q", "-o", "BatchMode=yes", str(movie), str(nfo), "admin@192.168.8.129:" + remote + "/movie/"], check=True)
experiment_input = credentials | {"experiment_id": experiment_id, "files_prepared": True}
def experiment(action):
    run = subprocess.run([a.experiment, "--apply", "--action", action], input=json.dumps(experiment_input).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    value = json.loads(run.stdout)
    (root / ("fnos-" + action + ".json")).write_text(json.dumps(value, indent=2) + "\n")
    assert run.returncode == 0, (action, value.get("status"), value.get("code"))
    return value
process, log, repo = None, None, None
cleanup_succeeded = False
try:
    prepared = experiment("prepare")
    guid = prepared["owned_guid"]
    assert prepared["baseline_unchanged"] and prepared["root"] == remote
    payload = Path(a.worker).read_bytes()
    manifest = json.loads(subprocess.check_output([a.worker, "--manifest"]))
    assert manifest["plugin_id"] == "OwnedHostServices"
    index = {"schema_version": 1, "plugin_id": manifest["plugin_id"], "current_version": manifest["version"], "releases": [{"version": manifest["version"], "manifest": manifest, "artifacts": [{"platform": "darwin-arm64", "format": "executable", "sha256": hashlib.sha256(payload).hexdigest(), "executable_sha256": hashlib.sha256(payload).hexdigest(), "size": len(payload), "host_contract": "light-media.sdk-resources/v1"}]}]}
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
    repo_url = "http://127.0.0.1:" + str(repo.server_port)
    index["releases"][0]["artifacts"][0]["url"] = repo_url + "/worker"
    threading.Thread(target=repo.serve_forever, daemon=True).start()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = "http://127.0.0.1:" + str(port)
    bootstrap = {"username": "owned-live-fnos", "password": "owned-public-fnos-test-password", "settings": {"plugin_markets": [repo_url + "/index"], "fnos": credentials | {"enabled": True}}}
    subprocess.run([a.server, "--data-dir", str(root / "data"), "--bootstrap-stdin"], input=json.dumps(bootstrap).encode(), check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def request(method, path, data=None):
        body = json.dumps(data).encode() if data is not None else None
        try:
            with client.open(urllib.request.Request(origin + path, body, {"Content-Type": "application/json"}, method=method), timeout=25) as response:
                return json.loads(response.read())
        except urllib.error.HTTPError as error:
            failure = json.loads(error.read())
            (root / "app-error.json").write_text(json.dumps(failure, indent=2) + "\n")
            raise
    def call(method, data):
        return request("POST", "/api/v1/plugin/OwnedHostServices/run", {"method": method, "input": data})
    log = (root / "server.log").open("wb")
    process = subprocess.Popen([a.server, "--data-dir", str(root / "data"), "--listen", "127.0.0.1:" + str(port)], stdout=log, stderr=log)
    for _ in range(300):
        try:
            request("GET", "/api/auth/status")
            break
        except OSError:
            assert process.poll() is None
            time.sleep(.05)
    request("POST", "/api/auth/login", {"username": bootstrap["username"], "password": bootstrap["password"]})
    assert request("POST", "/api/plugins/install", {"plugin_id": manifest["plugin_id"], "repo_url": repo_url + "/index"})["data"]["success"]
    servers = call("media.servers.list", {"probe": True})
    # Retain only the test library; existing library names/paths are not copied
    # into public test evidence. Full response is used only for identity checks.
    values = servers.get("value", servers) if isinstance(servers, dict) else servers
    assert isinstance(values, list) and len(values) == 1 and values[0]["connected"]
    owned = [v for v in values[0]["libraries"] if v["id"] == guid]
    assert len(owned) == 1 and owned[0]["directories"][0].rstrip("/") == remote
    try:
        partial = call("media.servers.refresh", {"name": "fnos", "library_id": guid, "paths": [remote + "/movie"]})
    except urllib.error.HTTPError:
        if a.diagnostic:
            diagnostic = subprocess.run([a.diagnostic], input=json.dumps(credentials | {"guid": guid, "root": remote}).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            assert diagnostic.returncode == 0, "finite diagnostic failed"
            (root / "protocol-diagnostic.json").write_bytes(diagnostic.stdout)
        raise
    whole = call("media.servers.refresh", {"name": "fnos", "library_id": guid, "root": True})
    for value in [partial, whole]:
        assert value.get("value", value)["accepted"]
    observation = experiment("observe")
    assert observation["baseline_unchanged"]
    assert request("DELETE", "/api/plugins/OwnedHostServices")["data"]["success"]
    result = {"status": "passed", "classification": "actual macOS cmd/server -> installed Go SDK -> real fnOS media on NAS, exclusively new UUID synthetic library", "experiment_id": experiment_id, "own_library": owned[0], "partial_refresh": partial, "root_refresh": whole, "observe_status": observation["status"], "baseline_unchanged": True, "limitations": ["Refresh acceptance is the source contract; it is not a claim that every background index job completed.", "No existing library was refreshed or edited."], "driver_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "server_sha256": hashlib.sha256(Path(a.server).read_bytes()).hexdigest(), "worker_sha256": hashlib.sha256(payload).hexdigest()}
    (root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"status": "passed", "experiment_id": experiment_id}), flush=True)
finally:
    if process:
        process.send_signal(signal.SIGTERM)
        process.wait(timeout=20)
    if repo:
        repo.shutdown()
    if log:
        log.close()
    cleaned = experiment("cleanup")
    assert cleaned["status"] == "cleaned" and cleaned["baseline_unchanged"]
    remote_python("from pathlib import Path\nimport hashlib\np=Path(" + repr(remote) + ")\nexpected=" + repr(file_hashes) + "\nassert not p.is_symlink() and not (p/'movie').is_symlink()\nfiles=list((p/'movie').iterdir())\nassert {f.name for f in files}==set(expected)\nassert all(f.is_file() and not f.is_symlink() and hashlib.sha256(f.read_bytes()).hexdigest()==expected[f.name] for f in files)\nfor f in files:f.unlink()\n(p/'movie').rmdir()\np.rmdir()\n")
    print(json.dumps({"cleanup": "verified", "experiment_id": experiment_id}), flush=True)
