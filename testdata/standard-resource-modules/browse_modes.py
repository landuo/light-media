"""Owned browser fixture modes over real authenticated configuration HTTP.

Original configuration and session stay in this process memory. It never edits
the application DB/files or prints private config/auth. stdin: throw/none/restore/close.
"""
import argparse
import copy
import http.cookiejar
import json
import pathlib
import sys
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--state", required=True)
args = parser.parse_args()
state = json.loads(pathlib.Path(args.state).read_text())
origin = urllib.parse.urlsplit(state["origin"])
assert origin.scheme == "http" and origin.hostname == "127.0.0.1" and origin.port
assert state["username"] == "resource-module-acceptance"
assert state["source_id"] == "OwnedModuleOverride" and state["clone_id"] == "OwnedModuleOverrideresource"
assert pathlib.Path(state["data_dir"]).parent.name.startswith("TestPluginSiteResourceModulesProductionCLI")
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def request(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(state["origin"] + path, data=data, method=method, headers={"Content-Type": "application/json"})
    with client.open(req, timeout=12) as response:
        assert response.status == 200
        return json.load(response)

request("POST", "/api/auth/login", {"username": state["username"], "password": state["password"]})
path = "/api/plugins/" + state["clone_id"]
backup = request("GET", path)["data"]
assert backup["enabled"] is True and backup["modules"]["async_refresh_torrents"] == "module_execute"
assert len(backup["result"]) == 1 and backup["result"][0]["title"] == "Owned clone resource"
print(json.dumps(dict(ready=True, target=state["clone_id"], backup="memory-only", scope="owned synthetic resource fixture")), flush=True)
for line in sys.stdin:
    mode = line.strip()
    if mode == "close":
        break
    assert mode in ["throw", "none", "restore"]
    cfg = copy.deepcopy(backup)
    if mode == "throw":
        cfg["modules"]["async_refresh_torrents"] = "module_fail"
    elif mode == "none":
        cfg["result"] = None
    result = request("PUT", path, cfg)
    assert result["data"]["success"] is True
    actual = request("GET", path)["data"]
    assert actual == cfg
    print(json.dumps(dict(mode=mode, saved=True, complete_readback_equal=True, original_backup="memory-only")), flush=True)
