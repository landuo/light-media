#!/usr/bin/env python3
"""Read-only exact-source oracle; Python is never an application dependency."""
import ast
import json
import posixpath
import subprocess
import threading
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Dict, List, Optional

ROOT = Path("/Users/accidia/projects/MoviePilot")
COMMIT = "8a83efeacd3e26b82be495e17d50146e31c96a60"
BLOBS = {
    "app/core/auth.py": "038979d4d87430e9af1ab9a4b40789f5f1aad9e0",
    "app/core/plugin.py": "8fc8954d7e7df32b970f16f461efa244fa2f142c",
    "app/api/endpoints/auth.py": "66b02dd576355a3da5e1e18e6748e2deaf5f3e36",
}


def read(path):
    actual = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", f"{COMMIT}:{path}"], text=True).strip()
    assert actual == BLOBS[path]
    return ast.parse(subprocess.check_output(["git", "-C", str(ROOT), "show", f"{COMMIT}:{path}"], text=True))


def execute(nodes, namespace):
    module = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)] + nodes, type_ignores=[])
    exec(compile(ast.fix_missing_locations(module), "fixed_moviepilot_source", "exec"), namespace)


clock = SimpleNamespace(now=100.0)
counter = iter(range(10000))
ns = {"Singleton": type, "threading": threading, "time": SimpleNamespace(time=lambda: clock.now),
      "secrets": SimpleNamespace(token_urlsafe=lambda size: f"owned-{next(counter)}"),
      "Any": Any, "Optional": Optional}
execute([node for node in read("app/core/auth.py").body if isinstance(node, ast.ClassDef) and node.name == "AuthTicketStore"], ns)
store = ns["AuthTicketStore"]()
ticket = store.create(1, "owned", False)
clock.now = 220.0
data = store.consume(ticket)
assert data["metadata"] == {} and store.consume(ticket) is None
clock.now = 100.0
expired = store.create(1, "owned")
clock.now = 220.001
assert store.consume(expired) is None
clock.now = 100.0
keys = [store.create(1, "owned") for _ in range(1026)]
assert len(store._tickets) == 1025 and keys[0] not in store._tickets and keys[1] in store._tickets
store.consume("unknown")
assert len(store._tickets) == 1024

source_class = next(node for node in read("app/core/plugin.py").body if isinstance(node, ast.ClassDef) and node.name == "PluginManager")
methods = [node for node in source_class.body if isinstance(node, ast.FunctionDef) and node.name in {"get_plugin_auth_providers", "get_plugin_remote_entry"}]
manager_class = ast.ClassDef(name="PluginManager", bases=[], keywords=[], body=methods, decorator_list=[])
logged = []
env = {"Any": Any, "Dict": Dict, "List": List, "posixpath": posixpath,
       "ObjectUtils": SimpleNamespace(check_method=callable), "logger": SimpleNamespace(error=logged.append)}
execute([manager_class], env)
manager = env["PluginManager"]()


class Plugin:
    plugin_name = "Owned source"

    def __init__(self, rows, state=True, fail="", mode=("vuetify", "")):
        self.rows, self.state, self.fail, self.mode = rows, state, fail, mode

    def get_state(self):
        if self.fail == "state":
            raise RuntimeError("owned state failure")
        return self.state

    def get_auth_providers(self):
        if self.fail == "getter":
            raise RuntimeError("owned getter failure")
        return self.rows

    def get_render_mode(self):
        if self.fail == "render":
            raise RuntimeError("owned rendering failure")
        return self.mode


rows = [None, False, {}, 42, "skip", {"extra": 9007199254740993}, {"id": None, "name": "", "enabled": False},
        {"id": "duplicate", "enabled": []}, {"id": "duplicate", "enabled": 1}, {"id": "duplicate", "enabled": "yes", "remote": {"x": 2}}]
manager._running_plugins = {"OwnedAuth": Plugin(rows)}
providers = manager.get_plugin_auth_providers()
assert len(providers) == 5 and providers[0]["id"] == "plugin:OwnedAuth" and providers[1]["id"] is None
filtered = [provider for provider in providers if provider.get("enabled", True)]
assert len(filtered) == 3 and filtered[1]["id"] == filtered[2]["id"] == "duplicate"
assert "type" not in rows[5]  # Original shallow copies each provider.
iterables = []
for value in [None, False, 0, {}, {"key": 1}, "text", [{}, {"id": "kept"}]]:
    manager._running_plugins = {"OwnedAuth": Plugin(value)}
    count = len(manager.get_plugin_auth_providers())
    iterables.append({"input": value, "provider_count": count})
    assert count == (1 if isinstance(value, list) else 0)
errors = {}
for kind in ["getter", "state", "render"]:
    manager._running_plugins = {"OwnedAuth": Plugin([{"id": "owned"}], fail=kind)}
    try:
        errors[kind] = len(manager.get_plugin_auth_providers())
    except RuntimeError:
        errors[kind] = "propagated"
assert errors == {"getter": 0, "state": "propagated", "render": "propagated"}
manager._running_plugins = {"OwnedAuth": Plugin([{"id": None, "enabled": False, "remote": {"id": "forged"}}], mode=("vue", "/ui/"))}
remote = manager.get_plugin_auth_providers()[0]
assert remote["remote"] == {"id": "OwnedAuth", "name": "Owned source", "url": "/plugin/file/ownedauth/ui/remoteEntry.js"}
assert remote["component"] == "AuthPage" and remote["id"] is None and remote["enabled"] is False

class SkippedNamePlugin(Plugin):
    @property
    def plugin_name(self):
        raise RuntimeError("owned name must not be read for skipped rows")
manager._running_plugins = {"OwnedAuth": SkippedNamePlugin([None, False, {}, 42, "skip"])}
skipped_name = manager.get_plugin_auth_providers()
assert skipped_name == []

epoch_store = ns["AuthTicketStore"]()
clock.now = 1_700_000_000.0
rounded_ticket = epoch_store.create(1,"epoch")
clock.now = 1_700_000_120.0 + 0.000000001
assert epoch_store.consume(rounded_ticket) is not None
clock.now = 1_700_000_000.0
expired_epoch_ticket = epoch_store.create(1,"epoch")
clock.now = 1_700_000_120.0 + 0.000001
assert epoch_store.consume(expired_epoch_ticket) is None

class HTTPException(Exception):
    def __init__(self, status_code, detail):
        self.status_code = status_code


users = {1: SimpleNamespace(id=1, is_active=True), 2: SimpleNamespace(id=2, is_active=False)}
exchange_env = {"consume_plugin_auth_ticket": store.consume, "User": SimpleNamespace(get=lambda db, rid: users.get(rid)),
                "HTTPException": HTTPException, "build_token_response": lambda user: {"authenticated_user": user.id}}
exchange_node = next(node for node in read("app/api/endpoints/auth.py").body if isinstance(node, ast.FunctionDef) and node.name == "auth_exchange")
exchange_node.decorator_list = []
execute([exchange_node], exchange_env)
exchange = exchange_env["auth_exchange"]
exchange_results = []
for user_id in [1, 2, 9]:
    ticket = store.create(user_id, "already-disabled-provider")
    statuses = []
    for _ in range(2):
        try:
            assert exchange(SimpleNamespace(ticket=ticket))["authenticated_user"] == 1
            statuses.append(200)
        except HTTPException as error:
            statuses.append(error.status_code)
    assert statuses == ([200, 401] if user_id == 1 else [403, 401])
    exchange_results.append({"user_id": user_id, "statuses": statuses})

print(json.dumps({"commit": COMMIT, "source_blobs": BLOBS, "exact_120s_accepted": True,
                  "over_120s_rejected": True, "capacity_after_create": 1025, "capacity_after_unknown_consume": 1024,
                  "provider_rows": providers, "provider_iterables": iterables, "provider_errors": errors,
                  "vue_remote": remote, "skipped_rows_name_read": False, "modern_epoch_plus_1ns_accepted": True, "modern_epoch_plus_1us_rejected": True, "exchange": exchange_results}, ensure_ascii=False, indent=2))
