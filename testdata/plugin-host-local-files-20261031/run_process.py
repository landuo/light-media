#!/usr/bin/env python3
"""Real isolated cmd/server + installed SDK artifact + readonly backup fixture.

No Python is used by light-media or its plugin: this is an external test driver.
Run on Linux with compiled Linux executables; --hold exposes only this synthetic
test instance for browser acceptance until the .browser-done marker is created.
"""
import argparse
import hashlib
import http.cookiejar
import http.server
import json
import os
import platform
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request

p = argparse.ArgumentParser()
p.add_argument("--server", required=True)
p.add_argument("--worker", required=True)
p.add_argument("--worker-v2", required=True)
p.add_argument("--directory", required=True)
p.add_argument("--hold", action="store_true")
args = p.parse_args()
target_platform = {"Linux": "linux", "Darwin": "darwin"}[platform.system()] + "-" + {"x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
directory = Path(args.directory).resolve()
directory.mkdir(mode=0o700, parents=True, exist_ok=True)
data = directory / "data"
backup = directory / "backup"
backup.mkdir(mode=0o700, exist_ok=True)
hash_value = "0123456789abcdef0123456789abcdef01234567"
files = {hash_value + ".torrent": b"owned synthetic torrent bytes\x00\xff", hash_value + ".fastresume": b"owned synthetic resume bytes\x00\xff"}
for name, content in files.items():
    (backup / name).write_bytes(content)
    (backup / name).chmod(0o400)
backup.chmod(0o500)
artifacts, releases = {}, []
for version, path in [("1.0", args.worker), ("2.0", args.worker_v2)]:
    path = Path(path).resolve()
    content = path.read_bytes()
    manifest = json.loads(subprocess.check_output([str(path), "--manifest"]))
    assert manifest["plugin_id"] == "OwnedLocalFiles" and manifest["version"] == version
    sha = hashlib.sha256(content).hexdigest()
    artifacts["/worker-" + version] = content
    releases.append({"version": version, "manifest": manifest, "artifacts": [{"platform": target_platform, "format": "executable", "sha256": sha, "executable_sha256": sha, "size": len(content), "host_contract": "light-media.sdk-resources/v1"}]})
index = {"schema_version": 1, "plugin_id": "OwnedLocalFiles", "current_version": "1.0", "releases": releases}
class Repo(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/index":
            body = json.dumps(index).encode()
        elif self.path in artifacts:
            body = artifacts[self.path]
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass
repository = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Repo)
repo_url = "http://127.0.0.1:" + str(repository.server_port)
for release in releases:
    release["artifacts"][0]["url"] = repo_url + "/worker-" + release["version"]
threading.Thread(target=repository.serve_forever, daemon=True).start()
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
origin = "http://127.0.0.1:" + str(port)
bootstrap = {"username": "owned-files-acceptance", "password": "owned-files-browser-password", "settings": {"plugin_markets": [repo_url + "/index"]}}
subprocess.run([args.server, "--data-dir", str(data), "--bootstrap-stdin"], input=json.dumps(bootstrap).encode(), check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
jar = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
observations = []
process = None
log = (directory / "server.log").open("wb")
def request(method, path, value=None, status=200):
    body = json.dumps(value).encode() if value is not None else None
    req = urllib.request.Request(origin + path, body, {"Content-Type": "application/json"}, method=method)
    try:
        response = client.open(req, timeout=20)
    except urllib.error.HTTPError as e:
        response = e
    raw = response.read()
    assert response.code == status, (method, path, response.code, status, raw[:500])
    return json.loads(raw) if raw else None
def success(method, path, value=None):
    result = request(method, path, value)
    assert result["data"]["success"], result
    return result["data"]
def start():
    global process
    process = subprocess.Popen([args.server, "--data-dir", str(data), "--listen", "127.0.0.1:" + str(port)], stdout=log, stderr=log)
    for _ in range(600):
        try:
            request("GET", "/api/auth/status")
            break
        except (OSError, AssertionError):
            assert process.poll() is None, "server exited"
            time.sleep(0.02)
    else:
        raise AssertionError("server did not start")
    request("POST", "/api/auth/login", {"username": bootstrap["username"], "password": bootstrap["password"]})
def stop():
    global process
    if process is not None:
        process.send_signal(signal.SIGTERM)
        assert process.wait(timeout=20) == 0
        process = None
def read(instance):
    return request("GET", "/api/v1/plugin/" + instance + "/snapshot")
def configure(instance, path):
    success("PUT", "/api/plugins/" + instance, {"backup_path": str(path), "hash": hash_value, "run_once": True})
    return read(instance)
source, clone = "OwnedLocalFiles", "OwnedLocalFilesfiles"
try:
    start()
    success("POST", "/api/plugins/install", {"plugin_id": source, "repo_url": repo_url + "/index"})
    first = configure(source, backup)
    assert not first["config"]["run_once"] and not first["observation"].get("error")
    for field, name in [("torrent_sha", hash_value + ".torrent"), ("resume_sha", hash_value + ".fastresume")]:
        assert first["observation"][field] == hashlib.sha256(files[name]).hexdigest()
    assert Path(first["observation"]["temp_path"]).read_bytes() == files[hash_value + ".torrent"]
    success("POST", "/api/plugins/" + source + "/clone", {"suffix": "Files"})
    copied = configure(clone, backup)
    assert copied["pid"] != first["pid"] and copied["data_path"] != first["data_path"]
    assert copied["observation"]["instance"] == clone
    failed = configure(clone, backup / "absent")
    assert failed["observation"]["error"] and not failed["config"]["run_once"]
    request("POST", "/api/v1/plugin/" + clone + "/run", status=422)
    configure(clone, backup)
    result = request("DELETE", "/api/v1/plugin/" + clone + "/cleanup")
    assert result["removed"] and not Path(copied["observation"]["temp_path"]).exists()
    assert Path(first["observation"]["temp_path"]).exists()
    # Upgrade source only; the clone keeps its actual copied executable.
    index["current_version"] = "2.0"
    success("POST", "/api/plugins/install", {"plugin_id": source, "release_version": "2.0", "repo_url": repo_url + "/index", "force": True})
    updated = read(source)
    assert updated["version"] == "2.0" and updated["pid"] != first["pid"]
    assert read(clone)["version"] == "1.0"
    assert updated["observation"] == first["observation"]
    before = {instance: read(instance) for instance in (source, clone)}
    stop()
    start()
    cold = {instance: read(instance) for instance in (source, clone)}
    for instance in before:
        assert before[instance]["pid"] != cold[instance]["pid"]
        assert before[instance]["observation"] == cold[instance]["observation"]
        assert not cold[instance]["config"]["run_once"]
    observations.append({"phase": "actual_process_lifecycle", "platform": target_platform, "first": first, "clone": copied, "failure": failed, "updated": updated, "before_restart": before, "after_restart": cold})
    if args.hold:
        state = {"origin": origin, "repository": repo_url + "/index", "directory": str(directory), "backup_path": str(backup), "hash": hash_value, "source": source, "clone": clone}
        (directory / "browser-state.json").write_text(json.dumps(state, indent=2))
        print(json.dumps({"status": "browser_ready", **state}), flush=True)
        while not (directory / ".browser-done").exists():
            assert process.poll() is None
            time.sleep(0.2)
    for instance in (clone, source):
        success("DELETE", "/api/plugins/" + instance)
        request("GET", "/api/v1/plugin/" + instance + "/snapshot", status=404)
    for name, content in files.items():
        assert (backup / name).read_bytes() == content
    observations.append({"phase": "uninstall", "apis_removed": True, "backups_unchanged": True})
    (directory / "result.json").write_text(json.dumps({"status": "passed", "platform": target_platform, "classification": "actual cmd/server, standard executable worker, owned loopback artifact repository and synthetic readonly backup directory; not production backup/downloader", "observations": observations}, indent=2))
    print(json.dumps({"status": "passed", "observations": len(observations)}), flush=True)
finally:
    stop()
    repository.shutdown()
    log.close()
    backup.chmod(0o700)
