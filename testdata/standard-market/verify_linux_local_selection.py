"""Disposable Linux acceptance against a real running light-media listener.

Run on the isolated test host with its browser state and actual v2 Go fixture.
Synthetic login only; no token/password/Cookie is printed or saved. Repository
data is confined to a TemporaryDirectory; original service settings are restored.
Source expectation: pinned core/plugin.py process_plugins_list, not Go output.
"""
import copy
import hashlib
import http.cookiejar
import json
from pathlib import Path
import shutil
import sys
import tempfile
import urllib.parse
import urllib.request

state = json.loads(Path(sys.argv[1]).read_text())
binary = Path(sys.argv[2])
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path, data=None, method="GET", headers=None):
    req = urllib.request.Request(state["origin"] + path, method=method,
        headers={"Content-Type": "application/json", **(headers or {})},
        data=json.dumps(data).encode() if data is not None else None)
    with opener.open(req, timeout=40) as response:
        return json.loads(response.read())


def save(data):
    result = request("/api/plugins/markets", data, "PUT")["data"]
    assert result["success"] is True


request("/api/auth/login", {"username": "plugin-acceptance", "password": "owned-browser-test-password"}, "POST")
settings = request("/api/plugins/markets")["data"]
key = request("/api/settings/api-token/reveal", {}, "POST")["data"]["token"]


def worker():
    value = request("/api/v1/plugin/OwnedStandardFixtureselection/echo", headers={"X-API-KEY": key})
    return {name: value[name] for name in ["version", "nonce", "init_count", "config", "value"]}


# A current installed class is deliberately absent from the market output.
# Keep an actual independent clone live while inspecting the uninstalled
# source's candidates, just as the pinned all_plugins contract requires.
installed = request("/api/plugins/install", {"plugin_id":"OwnedStandardFixture","repo_url":state["repository"]+"/index","release_version":"2.0","force":True}, "POST")["data"]
assert installed["success"]
created = request("/api/plugins/OwnedStandardFixture/clone", {"suffix":"Selection"}, "POST")["data"]
assert created["success"]
assert request("/api/plugins/OwnedStandardFixture", method="DELETE")["data"]["success"]
before = worker()
repository = state["repository"]
with urllib.request.urlopen(repository + "/version-two/index", timeout=10) as response:
    remote = json.loads(response.read())
index = copy.deepcopy(next(p for p in remote["plugins"] if p["plugin_id"] == "OwnedStandardFixture"))
index["releases"] = [r for r in index["releases"] if r["version"] == "2.0"]
artifact = index["releases"][0]["artifacts"][0]
assert artifact["sha256"] == hashlib.sha256(binary.read_bytes()).hexdigest()
artifact["url"] = "worker"
observed = []
try:
    with tempfile.TemporaryDirectory(prefix="owned-local-selection-") as directory:
        root = Path(directory)
        shutil.copyfile(binary, root / "worker")
        (root / "go-plugins.v1.json").write_text(json.dumps(index))
        for label, sources, expected in [
            ("local-higher-version", [repository + "/version-one/index"], "local"),
            ("online-equal-version", [repository + "/version-one/index", repository + "/version-two/index"], repository + "/version-two/index"),
            ("online-tie-source-order", [repository + "/same-version/index", repository + "/version-two/index"], repository + "/same-version/index"),
        ]:
            save({"plugin_markets": sources, "plugin_local_repo_paths": [directory], "plugin_auto_reload": False})
            cards = request("/api/plugins/market?force=true")["data"]
            selected = next(card for card in cards if card["instance_id"] == "OwnedStandardFixture")
            actual = selected["repo_url"]
            assert selected["repository_version"] == "2.0"
            if expected == "local":
                url = urllib.parse.urlparse(actual)
                assert url.scheme == "local" and url.netloc == "OwnedStandardFixture"
            else:
                assert actual == expected
            assert worker() == before, "metadata selection changed the actual running worker"
            observed.append({"case": label, "selected_source": actual, "repository_version": selected["repository_version"], "worker_unchanged": True})
finally:
    save({name: settings[name] for name in ["plugin_markets", "plugin_local_repo_paths", "plugin_auto_reload", "plugin_server_url", "plugin_statistic_share"]})
assert request("/api/plugins/install", {"plugin_id":"OwnedStandardFixture","repo_url":state["repository"]+"/index","release_version":"2.0","force":True}, "POST")["data"]["success"]
assert request("/api/plugins/OwnedStandardFixtureselection", method="DELETE")["data"]["success"]
assert request("/api/plugins/markets")["data"] == settings
print(json.dumps({"kind": "real Linux cmd/server; real independently running clone; uninstalled source candidate selection; actual v2 Go artifact", "source_commit": "8a83efeacd3e26b82be495e17d50146e31c96a60", "source": "app/core/plugin.py:1566-1604", "cases": observed, "settings_restored": True, "worker": before}, ensure_ascii=False))
